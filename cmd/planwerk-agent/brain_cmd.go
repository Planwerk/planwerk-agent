package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/planwerk/planwerk-agent/internal/brain"
	"github.com/planwerk/planwerk-agent/internal/claude"
)

// newBrainCmd builds the "brain" command group, which reads and builds what a
// repository keeps for its agents. Its child "memory" prints the project
// memory of a repository's GitHub Wiki: the index, or one page. The plugin
// skills call it, so a skill reads the memory through the same opt-in,
// authentication, clone cache, and page guards as the headless commands. Its
// child "bootstrap" (newBrainBootstrapCmd) builds that memory from the
// repository's history.
func newBrainCmd(deps *runtimeDeps) *cobra.Command {
	var wiki wikiFlags

	brainCmd := &cobra.Command{
		Use:   "brain",
		Short: "Read and build the project memory of a repository",
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

	brainCmd.AddCommand(memoryCmd, newBrainBootstrapCmd(deps))
	return brainCmd
}

// newBrainBootstrapCmd builds "brain bootstrap": distill a repository's
// history, unit by unit, into project memory pages and review patterns, keep
// them in a state directory, and push them to the wiki only under --write-wiki
// and after a confirmation at a terminal. Like sync, the command registers
// --wiki-ref only: running it is the wiki opt-in, so it has no --wiki and no
// --no-wiki. It has no --yes either.
func newBrainBootstrapCmd(deps *runtimeDeps) *cobra.Command {
	var opts brain.BootstrapOptions
	var wikiRef, reviewModel, reviewEffort string

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
			return brain.Bootstrap(cmd.OutOrStdout(), run, client.BootstrapUnit, client.BootstrapReview, client.UsageTotals)
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

	return bootstrapCmd
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
