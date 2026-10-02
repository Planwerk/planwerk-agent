package main

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/planwerk/planwerk-agent/internal/brain"
	"github.com/planwerk/planwerk-agent/internal/claude"
	"github.com/planwerk/planwerk-agent/internal/github"
	"github.com/planwerk/planwerk-agent/internal/mirror"
	"github.com/planwerk/planwerk-agent/internal/search"
)

// The values of --source of `brain bootstrap`.
const (
	brainSourceAPI    = "api"
	brainSourceMirror = "mirror"
)

// newBrainCmd builds the "brain" command group, which reads, builds, mirrors,
// and searches what a repository knows. Its child "memory" prints the project
// memory of a repository's GitHub Wiki: the index, or one page. The plugin
// skills call it, so a skill reads the memory through the same opt-in,
// authentication, clone cache, and page guards as the headless commands. Its
// child "bootstrap" (newBrainBootstrapCmd) builds that memory from the
// repository's history. Its child "sync" (newBrainSyncCmd) keeps a local
// mirror of the repository's issues, pull requests, commit list, and wiki.
// Its child "search" (newBrainSearchCmd) answers a keyword query over that
// mirror.
func newBrainCmd(deps *runtimeDeps) *cobra.Command {
	var wiki wikiFlags

	brainCmd := &cobra.Command{
		Use:   "brain",
		Short: "Read, build, mirror, and search what a repository knows",
	}

	memoryCmd := &cobra.Command{
		Use:   "memory <repo-ref> [page]",
		Short: "Print the project memory index of a repository, or one page",
		Long: `Print the project memory a repository keeps on its GitHub Wiki.

With a repository reference alone, print the memory index: a header line with
the wiki, its commit, and the number of pages, a blank line, and one line per
page with the file name, the title, and after a "|" the page's summary where
it states one. With the file name of a page as a second argument, print that
page.

A page is listed and printed only when its file name starts with a letter or
a digit and holds nothing but letters, digits, ".", "_", and "-", so a caller
can pass the name back on a command line as it is. Any other page is skipped:
one warning gives the number of skipped pages, and --verbose logs their names.
Control characters other than newline and tab are dropped from the output.

The wiki is off by default. It is read when --wiki, the wiki section of
.planwerk/config.yaml in the working directory, or PLANWERK_WIKI enables it,
in that order of precedence. With the wiki off, or with a wiki that has no
memory pages, the index form prints nothing and exits 0. A page that the
memory does not hold is an error.

The command starts no Claude session and writes no file.

Repository reference can be a URL (https://github.com/owner/repo)
or short form (owner/repo).`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := brain.MemoryOptions{
				RepoRef: args[0],
				Wiki:    wiki.resolve(cmd.Flags(), deps.fileCfg.Wiki),
				Remote:  deps.remoteOpts,
			}
			if len(args) == 2 {
				opts.Page = args[1]
			}
			return brain.Memory(cmd.OutOrStdout(), opts)
		},
	}
	wiki.register(memoryCmd.Flags())

	brainCmd.AddCommand(memoryCmd, newBrainBootstrapCmd(deps), newBrainSyncCmd(deps), newBrainSearchCmd(deps))
	return brainCmd
}

// newBrainSyncCmd builds "brain sync": mirror a repository's issues, pull
// requests, commit list, and wiki to local markdown. Like sync and brain
// bootstrap, the command registers --wiki-ref only: running it is the wiki
// opt-in, so it has no --wiki and no --no-wiki.
func newBrainSyncCmd(deps *runtimeDeps) *cobra.Command {
	var full bool
	var wikiRef string

	syncCmd := &cobra.Command{
		Use:   "sync <repo-ref>",
		Short: "Mirror the issues, pull requests, and wiki of a repository to local markdown",
		Long: `Keep a local mirror of what a repository knows on GitHub.

The mirror holds one markdown file per issue and per pull request with the
whole conversation (the body, the comments, and for a pull request the
reviews, the review threads, and the commits), the commit list of the default
branch, and a full clone of the wiki. It lives in the user cache directory,
under planwerk-agent/brain/<owner>/<name>, and the command prints the path.

A run lists the issues and pull requests updated since the last run and
fetches only those, reads the commits the default branch gained, and clones
the wiki again. A run right after another one fetches no item. A run that
fails continues where it stopped when the same command runs again.

--full deletes this repository's mirror first and builds it again from
GitHub. The mirror is a copy: deleting it loses nothing. A comment deleted on
GitHub leaves the mirror when its issue or pull request is fetched again, and
a deleted or transferred issue or pull request leaves it on --full.

The files hold the text as GitHub returns it, with every secret someone
pasted into a comment. The command starts no Claude session, and no session is
given the mirror directory. With --brain, a read-only session reaches the
mirrored text only through "brain search", redacted.

This is not the "sync" command, which reconciles the wiki's knowledge pages
with the code.

Repository reference can be a URL (https://github.com/owner/repo)
or short form (owner/repo).`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return (&mirror.Syncer{}).Run(cmd.OutOrStdout(), mirror.Options{
				RepoRef: args[0],
				Full:    full,
				Wiki:    resolveSyncWiki(wikiRef, cmd.Flags().Changed("wiki-ref"), deps.fileCfg.Wiki),
			})
		},
	}

	flags := syncCmd.Flags()
	flags.BoolVar(&full, "full", false, "Delete this repository's mirror first, then sync")
	flags.StringVar(&wikiRef, "wiki-ref", "", "Pin the wiki to a branch, tag, or commit (env: "+envWikiRef+"; empty uses the wiki's default branch)")

	return syncCmd
}

// The values of --type and --state of `brain search`.
var (
	brainSearchTypes  = []string{github.ItemKindIssue, github.ItemKindPull, search.TypeWiki}
	brainSearchStates = []string{"open", "closed", "merged"}
)

// The bounds of --limit of `brain search`.
const (
	brainSearchMinLimit = 1
	brainSearchMaxLimit = 100
)

// newBrainSearchCmd builds "brain search": rank the issues, pull requests,
// and wiki pages of the local mirror against a keyword query, or print one
// block of them in full. The arguments after the repository are the query.
// The command registers no wiki flag and reads no config file: it reads the
// mirror `brain sync` wrote and nothing else.
//
// A session runs the command in the checkout under review, with whatever its
// shell expands into the arguments. So the command carries
// mirrorOnlyAnnotation, which keeps the root command from loading the
// checkout's .planwerk/config.yaml, and no error it returns holds an argument:
// an error is printed back to the session.
func newBrainSearchCmd(_ *runtimeDeps) *cobra.Command {
	var opts brain.SearchOptions

	searchCmd := &cobra.Command{
		Use:   "search <repo-ref> [query...]",
		Short: "Search the local mirror of a repository by keyword",
		Long: `Search the local mirror that "brain sync" keeps of a repository: its issues
and pull requests with their comments, reviews, review threads, and commit
messages, and the pages of its wiki.

The result lists the best matches first. A hit is one issue, pull request, or
wiki page with the block of it that matched best: a title, a body, a comment,
a review, the diff hunk of a review thread, a thread comment, a commit
message, or a section of a wiki page. Each hit prints an excerpt of that block
and its id.

The arguments after the repository are the query. A block matches when it
holds at least one of the words, matched whole and without regard to case or
accents, and a block that holds more of the words, and rarer ones, ranks
higher. "*" after a word matches every word that begins with it, and double
quotes hold an exact phrase. Every other character is searched for as text:
the query has no operators. A word that starts with "-" is read as a flag:
put "--" before the query, and every flag before the "--", to search for it.

--type, --state, and --label keep only the hits that match, and --limit sets
how many are printed. A wiki page has no state and no label. --json prints the
result as one JSON object.

--show <id> prints the block with the id of a hit in full, in place of a
search. Every line of the block's text follows "| ", so a line without it is
the command's own.

The text is redacted: a recognized secret is printed as a marker. The output
names a file relative to the mirror directory and never holds the query, and
no error message holds an argument.

The command never syncs. It reads the mirror as the last "brain sync" left it
and prints the time of that sync, and it stops when the mirror is missing or
no sync of it has finished. It keeps its index in the file index.sqlite in the
mirror directory and brings it up to date before it searches. It starts no
Claude session and calls no GitHub API.

Repository reference can be a URL (https://github.com/owner/repo)
or short form (owner/repo).`,
		Args:        cobra.MinimumNArgs(1),
		Annotations: map[string]string{mirrorOnlyAnnotation: "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			flags := cmd.Flags()
			switch {
			case opts.Show != "" && len(args) > 1:
				return fmt.Errorf("--show takes no query")
			case opts.Show != "" && (flags.Changed("type") || flags.Changed("state") || flags.Changed("label") || flags.Changed("limit")):
				return fmt.Errorf("--show takes no filter")
			case opts.Show == "" && len(args) == 1:
				return fmt.Errorf("a query is required, or --show <id>")
			}
			for _, t := range opts.Types {
				if !slices.Contains(brainSearchTypes, t) {
					return fmt.Errorf("--type must be one of %s", strings.Join(brainSearchTypes, ", "))
				}
			}
			if flags.Changed("state") && !slices.Contains(brainSearchStates, opts.State) {
				return fmt.Errorf("--state must be one of %s", strings.Join(brainSearchStates, ", "))
			}
			if opts.Limit < brainSearchMinLimit || opts.Limit > brainSearchMaxLimit {
				return fmt.Errorf("--limit must be between %d and %d", brainSearchMinLimit, brainSearchMaxLimit)
			}

			run := opts
			run.RepoRef = args[0]
			run.Query = strings.Join(args[1:], " ")
			return brain.Search(cmd.OutOrStdout(), run)
		},
	}

	flags := searchCmd.Flags()
	// A label can hold a comma, so the two repeatable flags take one value per
	// occurrence.
	flags.StringArrayVar(&opts.Types, "type", nil, "Keep only hits of this type: "+strings.Join(brainSearchTypes, ", ")+" (repeatable)")
	flags.StringVar(&opts.State, "state", "", "Keep only items in this state: "+strings.Join(brainSearchStates, ", "))
	flags.StringArrayVar(&opts.Labels, "label", nil, "Keep only items that carry this label, compared without case (repeatable; every given label must match)")
	flags.IntVar(&opts.Limit, "limit", search.DefaultLimit, fmt.Sprintf("The largest number of hits, %d to %d", brainSearchMinLimit, brainSearchMaxLimit))
	flags.BoolVar(&opts.JSON, "json", false, "Print JSON")
	flags.StringVar(&opts.Show, "show", "", "Print the block with this id in full, in place of a search")
	// The error of the flag parser quotes the flag and its value.
	searchCmd.SetFlagErrorFunc(func(*cobra.Command, error) error {
		return errors.New("invalid flag or flag value; see brain search --help")
	})

	return searchCmd
}

// newBrainBootstrapCmd builds "brain bootstrap": distill a repository's
// history, unit by unit, into project memory pages and review patterns, keep
// them in a state directory, and push them to the wiki only under --write-wiki
// and after a confirmation at a terminal. Like sync, the command registers
// --wiki-ref only: running it is the wiki opt-in, so it has no --wiki and no
// --no-wiki. It has no --yes either. --source selects where the history is
// read from (brainBootstrapSource).
func newBrainBootstrapCmd(deps *runtimeDeps) *cobra.Command {
	var opts brain.BootstrapOptions
	var wikiRef, reviewModel, reviewEffort, source string

	bootstrapCmd := &cobra.Command{
		Use:   "bootstrap <repo-ref>",
		Short: "Build the project memory of a repository from its history",
		Long: `Distill the history of a repository into project memory pages and review
patterns for its GitHub Wiki.

The history is read as units, in the order of the default branch: every closed
issue with the pull requests and the commit that closed it, every merged pull
request that closed no issue (a bot's is skipped), the commits pushed without
a pull request in ranges of up to 25, and the decision documents of the
repository in chunks. For each unit one session proposes pages, and a second
session on its own model reviews every proposed page against the unit and the
code at HEAD.

The pages and the progress are kept in .planwerk-brain-sync in the working
directory, and the state is saved after every unit. A run that is interrupted
or fails continues with the unit it stopped at when the same command runs
again, and a later run processes only what was closed, merged, or committed
since. Delete the directory to start over.

Nothing is written to the wiki unless --write-wiki is given. The push runs once
no unit remains, lists every page, and asks for a confirmation at a terminal;
there is no flag that skips the question, and a run whose stdin is not a
terminal never pushes. --dry-run clones the repository, lists the units, and
stops without a Claude session.

The history is read from the GitHub API. With --source mirror it is read from
the local mirror that "brain sync" keeps, and the run calls no GitHub API for
it. The mirror is read as it is: the run does not sync it, so it sees the
history up to the last "brain sync", and it stops when the mirror is missing
or no sync of it has finished. The repository is still cloned.

The analysis runs on --claude-model and --claude-effort, the page review on
--review-model and --review-effort.

Repository reference can be a URL (https://github.com/owner/repo)
or short form (owner/repo).`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			flags := cmd.Flags()
			if opts.DryRun && opts.WriteWiki {
				return fmt.Errorf("--dry-run and --write-wiki are mutually exclusive")
			}
			if flags.Changed("decision-docs") && opts.NoDecisionDocs {
				return fmt.Errorf("--decision-docs and --no-decision-docs are mutually exclusive")
			}
			if opts.MaxUnits < 0 {
				return fmt.Errorf("--max-units must be >= 0, got %d", opts.MaxUnits)
			}
			src, err := brainBootstrapSource(source, args[0])
			if err != nil {
				return err
			}
			model, effort, err := resolveBrainReviewTier(reviewModel, flags.Changed("review-model"), reviewEffort, flags.Changed("review-effort"))
			if err != nil {
				return err
			}

			// The page review runs on its own tier, so build a client that
			// layers the resolved --review-* options on top of the shared
			// --claude-* options. The command runs its sessions on this client,
			// not deps.claude, so it prints its own usage totals.
			clientOpts := append([]claude.Option{}, deps.claudeOpts...)
			clientOpts = append(clientOpts, claude.WithBrainReviewModel(model), claude.WithBrainReviewEffort(effort))
			client := claude.NewClient(clientOpts...)
			defer client.LogUsageSummary(cmd.ErrOrStderr())

			run := opts
			run.RepoRef = args[0]
			run.Wiki = resolveSyncWiki(wikiRef, flags.Changed("wiki-ref"), deps.fileCfg.Wiki)
			run.Remote = deps.remoteOpts
			return (&brain.Bootstrapper{
				Source:  src,
				Analyze: client.BootstrapUnit,
				Review:  client.BootstrapReview,
				Usage:   client.UsageTotals,
			}).Run(cmd.OutOrStdout(), run)
		},
	}

	flags := bootstrapCmd.Flags()
	flags.BoolVar(&opts.DryRun, "dry-run", false, "Clone, list the units, and stop: no Claude session, no wiki clone, no state write")
	flags.IntVar(&opts.MaxUnits, "max-units", 0, "Stop after this many units in this run (0 processes every remaining unit)")
	flags.BoolVar(&opts.WriteWiki, "write-wiki", false, "Once no unit remains, push the changed pages to the wiki after a confirmation at a terminal")
	flags.StringVar(&wikiRef, "wiki-ref", "", "Pin the wiki to a branch, tag, or commit (env: "+envWikiRef+"; empty uses the wiki's default branch)")
	flags.StringVar(&reviewModel, "review-model", claude.DefaultBrainReviewModel, "Model of the page review (env: "+envBrainReviewModel+")")
	flags.StringVar(&reviewEffort, "review-effort", claude.DefaultBrainReviewEffort, "Reasoning effort of the page review: low, medium, high, xhigh, or max (env: "+envBrainReviewEffort+")")
	flags.StringSliceVar(&opts.DecisionDocs, "decision-docs", nil, "Repository-relative paths of the decision documents to read, in place of the discovered ones")
	flags.BoolVar(&opts.NoDecisionDocs, "no-decision-docs", false, "Read no decision document")
	flags.StringVar(&source, "source", brainSourceAPI, "Where the history is read from: api (the GitHub API) or mirror (the local mirror of brain sync)")

	return bootstrapCmd
}

// brainBootstrapSource resolves --source of `brain bootstrap` for the
// repository repoRef names. "api" yields nil, which the run takes as the
// GitHub API. "mirror" yields the reader of the repository's mirror under the
// default root; the mirror itself is checked when the run lists the history.
func brainBootstrapSource(source, repoRef string) (brain.Source, error) {
	switch source {
	case brainSourceAPI:
		return nil, nil
	case brainSourceMirror:
		owner, name, err := github.ParseRepoRef(repoRef)
		if err != nil {
			return nil, fmt.Errorf("parsing repo ref: %w", err)
		}
		root, err := mirror.DefaultRoot()
		if err != nil {
			return nil, err
		}
		dir, err := mirror.Dir(root, owner, name)
		if err != nil {
			return nil, err
		}
		return brain.MirrorSource{Dir: dir}, nil
	default:
		return nil, fmt.Errorf("--source must be %q or %q, got %q", brainSourceAPI, brainSourceMirror, source)
	}
}

// resolveBrainReviewTier resolves the model and the effort of the page review
// of `brain bootstrap`: the flag when it was set, else the environment
// variable, else the compiled-in default. The effort is validated here, before
// any session runs.
func resolveBrainReviewTier(model string, modelSet bool, effort string, effortSet bool) (m, e string, err error) {
	m = resolveString(model, modelSet, envBrainReviewModel, claude.DefaultBrainReviewModel)
	e, err = resolveEffort(effort, effortSet, envBrainReviewEffort, claude.DefaultBrainReviewEffort, false, "--review-effort")
	if err != nil {
		return "", "", err
	}
	return m, e, nil
}
