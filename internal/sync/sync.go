package sync

import (
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/planwerk/planwerk-review/internal/github"
	"github.com/planwerk/planwerk-review/internal/patterns"
	"github.com/planwerk/planwerk-review/internal/workspace"
)

// Options configures the sync subcommand.
type Options struct {
	// RepoRef is the target repository whose wiki is reconciled. Required.
	RepoRef string
	// Prune enables the write phase: after reporting, delete the flagged entries
	// on the wiki and push. Off by default (a dry run reports only).
	Prune bool
	// Yes skips the interactive confirmation gate on the write phase, for
	// non-interactive (CI) prunes.
	Yes bool
	// Format selects the report output: "markdown" (default) or "json".
	Format string
	// Version is the planwerk-review build version, named in the report footer.
	Version string
	// Remote configures how the wiki clone resolves through the remote-cache
	// machinery (carries the --remote-patterns-ttl value).
	Remote patterns.RemoteOptions
	// Wiki configures the wiki knowledge source. sync always sets Enabled=true
	// (reconciling the wiki is its whole purpose); the CLI fills Repo and Ref
	// from --wiki-ref / PLANWERK_WIKI_REF / the config file.
	Wiki patterns.WikiOptions
}

// Runner glues together the Claude analyzer, the GitHub client, the wiki
// resolver, and the wiki writer. Tests inject fakes via the exported fields.
type Runner struct {
	Claude ClaudeSyncer
	GitHub GitHubClient
	// ResolveWiki resolves the target repo's wiki. Defaults to
	// patterns.ResolveWiki.
	ResolveWiki resolveWikiFn
	// Writer performs the --prune write phase. Defaults to a patterns-backed
	// implementation.
	Writer WikiWriter
	// In is the stream the write-phase confirmation reads from.
	In io.Reader
	// IsTTY reports whether the write phase may prompt interactively. When it
	// returns false a --prune run without --yes refuses rather than failing open.
	IsTTY func() bool
}

// NewRunner returns a Runner wired with the given Claude sync function; the
// production GitHub, wiki-resolver, and wiki-writer seams are filled in by Run.
func NewRunner(syncFn SyncFn) *Runner {
	return &Runner{Claude: syncFnAdapter{fn: syncFn}}
}

// Run is a package-level convenience that delegates to NewRunner(syncFn).Run.
func Run(w io.Writer, opts Options, syncFn SyncFn) error {
	return NewRunner(syncFn).Run(w, opts)
}

// Run executes the sync pipeline:
//  1. Resolve the target repo identity and its wiki (clone root, commit).
//  2. Enumerate the wiki's review_patterns/ and memory/ entries.
//  3. Clone the target repo and run the read-only Claude analysis that flags
//     stale and redundant entries.
//  4. Render the dry-run report.
//  5. Under --prune, dispatch to the confirmed write phase that deletes the
//     flagged entries on the wiki and pushes.
func (r *Runner) Run(w io.Writer, opts Options) error {
	r.applyDefaults()

	owner, name, err := github.ParseRepoRef(opts.RepoRef)
	if err != nil {
		return fmt.Errorf("parsing repo ref: %w", err)
	}

	wiki := r.ResolveWiki(owner, name, opts.Wiki, opts.Remote)
	if wiki.Dir == "" {
		return fmt.Errorf("no wiki to reconcile for %s/%s: the wiki is missing, uninitialized, or offline", owner, name)
	}

	entries, err := ReadWikiEntries(wiki.Dir)
	if err != nil {
		return fmt.Errorf("reading wiki entries: %w", err)
	}
	if len(entries) == 0 {
		_, _ = fmt.Fprintf(w, "The %s wiki has no review_patterns/ or memory/ entries to reconcile.\n", wiki.Repo)
		return nil
	}

	repo, err := r.GitHub.CloneRepo(opts.RepoRef)
	if err != nil {
		return fmt.Errorf("cloning repository: %w", err)
	}
	defer repo.Cleanup()
	slog.Info("cloned repository", "dir", repo.Dir)

	slog.Info("reconciling wiki against code", "entries", len(entries))
	result, err := r.Claude.Sync(repo.Dir, SyncContext{Entries: entries, RepoName: repo.FullName()})
	if err != nil {
		return fmt.Errorf("claude sync analysis: %w", err)
	}
	result.WikiRepo = wiki.Repo
	result.WikiCommit = wiki.CommitSHA

	if err := renderResult(w, result, repo.FullName(), opts); err != nil {
		return err
	}

	// The write phase is strictly separate from the read-only analysis above and
	// runs only under --prune, and only when there is something to delete.
	if opts.Prune && len(result.DeletionPaths()) > 0 {
		// Gate deletion on the entries actually enumerated and analyzed. The
		// flagged paths are model output built from untrusted, world-editable wiki
		// bodies, so a crafted page could steer the analysis to flag — and --prune
		// to delete — a page that was never enumerated (Home.md, _Sidebar.md, any
		// tracked wiki file). The allowlist confines the write to the
		// review_patterns/ and memory/ entries ReadWikiEntries surfaced.
		allowed := make(map[string]bool, len(entries))
		for _, e := range entries {
			allowed[e.Path] = true
		}
		return r.runWritePhase(w, opts, result, allowed)
	}
	return nil
}

// renderResult writes the report in the requested format.
func renderResult(w io.Writer, result *SyncResult, repoFullName string, opts Options) error {
	renderer := NewRenderer(w)
	if opts.Format == "json" {
		return renderer.RenderJSON(*result)
	}
	renderer.RenderMarkdown(*result, repoFullName, opts.Version)
	return nil
}

// applyDefaults fills in the Runner seams with their production defaults.
func (r *Runner) applyDefaults() {
	if r.GitHub == nil {
		r.GitHub = defaultGitHubClient{}
	}
	if r.ResolveWiki == nil {
		r.ResolveWiki = patterns.ResolveWiki
	}
	if r.Writer == nil {
		r.Writer = defaultWikiWriter{}
	}
	if r.In == nil {
		r.In = os.Stdin
	}
	if r.IsTTY == nil {
		r.IsTTY = workspace.IsStdinTTY
	}
}
