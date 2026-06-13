// Package rebase orchestrates the `rebase` subcommand: it replays a PR's
// commits onto a freshly fetched base branch, dispatches Claude to resolve each
// conflict semantically, then analyzes the rebased commits against the upstream
// range that entered the base since the PR forked. The Go orchestrator owns the
// loop and every git invocation; Claude does the per-step semantic work.
package rebase

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/planwerk/planwerk-review/internal/detect"
	"github.com/planwerk/planwerk-review/internal/github"
	"github.com/planwerk/planwerk-review/internal/patterns"
	"github.com/planwerk/planwerk-review/internal/report"
	"github.com/planwerk/planwerk-review/internal/workspace"
)

// Default loop parameters and the default base branch. Each can be overridden
// via Options / CLI flags.
const (
	// DefaultOnto is the base branch a rebase targets unless --onto overrides
	// it. Per the issue this is literally "main", independent of the PR's own
	// base branch.
	DefaultOnto = "main"
	// DefaultMaxIterations caps how many conflicting commits the loop will
	// resolve before aborting.
	DefaultMaxIterations = 10

	// BundledPatternsURLBase is the public raw-markdown URL prefix the
	// bare-prompt catalog uses to point Claude at planwerk-review's bundled
	// pattern files. Pinned to "main" so manual sessions always pick up the
	// latest patterns, mirroring the fix package.
	BundledPatternsURLBase = "https://raw.githubusercontent.com/planwerk/planwerk-review/main/internal/patterns/patterns"
)

// Options configures the rebase subcommand. Mirrors the Options style used by
// the fix package.
type Options struct {
	PRRef             string
	Onto              string // base branch to rebase onto; defaults to DefaultOnto
	Push              bool   // force-push the rewritten branch with --force-with-lease
	ApplyAdjustments  bool   // apply the post-rebase analysis as fixup commits
	MaxIterations     int    // cap on conflict-resolution iterations
	NoAnalysis        bool   // skip the post-rebase commit analysis
	NoAnalysisComment bool   // do not post the analysis as a PR comment
	DryRun            bool   // show the plan + conflicting commit without resolving
	PrintPrompt       bool   // render the analysis prompt to stdout and exit
	Local             bool   // operate on the current working directory instead of cloning
	Force             bool   // with Local, skip the dirty-working-tree confirmation prompt
	Version           string

	// Pattern loading mirrors fix so both the conflict resolution and the
	// analysis are grounded in the same review catalog plus any
	// project-specific patterns under .planwerk/review_patterns/.
	PatternDirs     []string
	NoRepoPatterns  bool
	NoLocalPatterns bool
	MaxPatterns     int
}

// Runner glues together the git client, the Claude rebaser, and the analysis
// prompt builder. Tests inject fakes via the exported fields.
type Runner struct {
	Claude ClaudeRebaser
	GitHub GitClient
	// AnalysisPrompt renders the analysis prompt without invoking Claude.
	// Required when Options.PrintPrompt is set; nil otherwise is fine.
	AnalysisPrompt AnalysisPromptFn
}

// NewRunner builds a Runner with the production git backend and the given
// Claude functions wired in. The CLI calls this with the claude.* functions so
// the import direction stays claude -> rebase.
func NewRunner(resolve ResolveConflictFn, analyze AnalyzeFn, analysisPrompt AnalysisPromptFn, apply ApplyFn) *Runner {
	return &Runner{
		Claude:         claudeFns{resolve: resolve, analyze: analyze, apply: apply},
		GitHub:         defaultGitClient{},
		AnalysisPrompt: analysisPrompt,
	}
}

// Run is a package-level convenience that delegates to
// NewRunner(...).Run.
func Run(w io.Writer, opts Options, resolve ResolveConflictFn, analyze AnalyzeFn, analysisPrompt AnalysisPromptFn, apply ApplyFn) error {
	return NewRunner(resolve, analyze, analysisPrompt, apply).Run(w, opts)
}

// ErrMaxIterations is returned when conflict resolution exhausts its iteration
// budget before the rebase completes. The rebase is aborted before it is
// returned. Exported so the CLI can distinguish it from other failures.
var ErrMaxIterations = errors.New("reached max iterations without completing the rebase")

// ErrRebaseStopped is returned when a rebase step stops for a reason other than
// a conflict (an unexpected state the loop cannot drive forward). The rebase is
// aborted before it is returned.
var ErrRebaseStopped = errors.New("rebase stopped without a resolvable conflict")

// Run executes the rebase pipeline:
//  1. Resolve the PR (clone or --local).
//  2. Fetch the base branch and pin the PR's original merge-base.
//  3. Replay the PR's commits onto origin/<onto>, resolving each conflict with
//     Claude and continuing, until the rebase completes or --max-iterations is
//     hit (then abort).
//  4. Analyze the rebased commits against the upstream range and report
//     per-commit adjustments; optionally apply them as fixup commits.
//  5. Force-push only when --push is given.
func (r *Runner) Run(w io.Writer, opts Options) error {
	r.applyDefaults(&opts)

	if opts.PrintPrompt && r.AnalysisPrompt == nil {
		return errors.New("--print-prompt requires a prompt builder; wire claude.BuildRebaseAnalysisPrompt via NewRunner")
	}
	if !opts.Local && opts.PRRef == "" {
		return errors.New("a PR reference is required (or use --local)")
	}

	pr, err := r.fetchPR(opts)
	if err != nil {
		return fmt.Errorf("fetching PR: %w", err)
	}
	defer pr.Cleanup()
	owner, repo, number := pr.Owner, pr.Repo, pr.Number
	fullName := fmt.Sprintf("%s/%s", owner, repo)
	onto := opts.Onto

	slog.Info("rebase starting",
		"pr", fmt.Sprintf("%s#%d", fullName, number),
		"onto", onto,
		"max_iterations", opts.MaxIterations,
		"dry_run", opts.DryRun,
		"print_prompt", opts.PrintPrompt,
		"push", opts.Push,
		"apply_adjustments", opts.ApplyAdjustments,
		"local", opts.Local,
	)
	if opts.Local {
		slog.Info("working tree left on PR branch", "branch", pr.HeadBranch, "dir", pr.Dir)
	}

	// Fetch the base so origin/<onto> reflects the latest remote state, then
	// pin the PR's original fork point before any rewrite.
	if err := r.GitHub.FetchBranch(pr.Dir, onto); err != nil {
		return fmt.Errorf("fetching base branch %s: %w", onto, err)
	}
	origin := "origin/" + onto
	origMergeBase, err := r.GitHub.MergeBase(pr.Dir, pr.HeadSHA, origin)
	if err != nil {
		return fmt.Errorf("computing merge-base of %s and %s: %w", shortSHA(pr.HeadSHA), origin, err)
	}
	replay, err := r.GitHub.CommitsInRange(pr.Dir, origin+"..HEAD")
	if err != nil {
		return fmt.Errorf("listing PR commits to replay: %w", err)
	}

	pats := loadPatterns(opts, pr.Dir)

	// --print-prompt renders the analysis prompt from the computed ranges
	// without performing the rebase, so it never mutates the working tree.
	if opts.PrintPrompt {
		return r.printAnalysisPrompt(w, opts, pr, onto, origin, origMergeBase, replay, pats)
	}

	if opts.DryRun {
		return r.dryRun(w, pr, onto, origin, origMergeBase, replay)
	}

	// Replay onto the base, resolving each conflict with Claude.
	if err := r.runRebaseLoop(w, opts, pr, onto, fullName, number, pats); err != nil {
		return err
	}

	// The rebase is clean. Analyze the rebased commits against the upstream
	// range, then optionally apply the adjustments.
	if !opts.NoAnalysis {
		if err := r.analyzeAndReport(w, opts, pr, onto, origin, origMergeBase, fullName, owner, repo, number, pats); err != nil {
			return err
		}
	}

	// Publish only when asked. Rewriting history requires a force-push, and we
	// never do that implicitly.
	if opts.Push {
		if err := r.GitHub.ForceWithLeasePush(pr.Dir, pr.HeadBranch); err != nil {
			return fmt.Errorf("force-pushing rebased branch %s: %w", pr.HeadBranch, err)
		}
		_, _ = fmt.Fprintf(w, "Force-pushed the rebased branch to %s with --force-with-lease.\n", pr.HeadBranch)
	} else {
		_, _ = fmt.Fprintln(w, "Rebase complete. The rewritten branch was NOT pushed (pass --push to publish it with --force-with-lease).")
	}
	return nil
}

// runRebaseLoop performs the rebase and drives the conflict-resolution loop
// until it completes cleanly or --max-iterations is exhausted (then aborts).
func (r *Runner) runRebaseLoop(w io.Writer, opts Options, pr *github.PR, onto, fullName string, number int, pats []patterns.Pattern) error {
	state, err := r.GitHub.StartRebase(pr.Dir, onto)
	if err != nil {
		return fmt.Errorf("starting rebase onto %s: %w", onto, err)
	}

	iterations := 0
	for !state.Done {
		if !state.Conflicted {
			_ = r.GitHub.RebaseAbort(pr.Dir)
			return fmt.Errorf("%w (onto %s)", ErrRebaseStopped, onto)
		}
		iterations++
		if iterations > opts.MaxIterations {
			_ = r.GitHub.RebaseAbort(pr.Dir)
			return fmt.Errorf("%w: stopped on %s %q after %d iterations on %s#%d",
				ErrMaxIterations, shortSHA(state.StoppedSHA), state.StoppedSubject, opts.MaxIterations, fullName, number)
		}

		_, _ = fmt.Fprintf(w, "Resolving conflict on %s %q (%d file(s))...\n",
			shortSHA(state.StoppedSHA), state.StoppedSubject, len(state.ConflictedFiles))

		if _, err := r.Claude.ResolveConflict(pr.Dir, ConflictContext{
			RepoFullName:    fullName,
			PRNumber:        number,
			Onto:            onto,
			HeadBranch:      pr.HeadBranch,
			Commit:          github.Commit{SHA: state.StoppedSHA, Subject: state.StoppedSubject},
			ConflictedFiles: state.ConflictedFiles,
			Patterns:        pats,
			MaxPatterns:     opts.MaxPatterns,
		}); err != nil {
			_ = r.GitHub.RebaseAbort(pr.Dir)
			return fmt.Errorf("resolving conflict on %s: %w", shortSHA(state.StoppedSHA), err)
		}

		state, err = r.GitHub.RebaseContinue(pr.Dir)
		if err != nil {
			_ = r.GitHub.RebaseAbort(pr.Dir)
			return fmt.Errorf("continuing rebase after %s: %w", shortSHA(state.StoppedSHA), err)
		}
	}

	_, _ = fmt.Fprintf(w, "Rebase onto %s completed; %d conflict(s) resolved.\n", onto, iterations)
	return nil
}

// analyzeAndReport runs the post-rebase analysis, renders it, posts it as a PR
// comment (unless suppressed), and optionally applies the adjustments.
func (r *Runner) analyzeAndReport(w io.Writer, opts Options, pr *github.PR, onto, origin, origMergeBase, fullName, owner, repo string, number int, pats []patterns.Pattern) error {
	upstream, err := r.GitHub.CommitsInRange(pr.Dir, origMergeBase+".."+origin)
	if err != nil {
		return fmt.Errorf("listing upstream commits: %w", err)
	}
	rebased, err := r.GitHub.CommitsInRange(pr.Dir, origin+"..HEAD")
	if err != nil {
		return fmt.Errorf("listing rebased commits: %w", err)
	}

	analysis, err := r.Claude.AnalyzeRebasedCommits(pr.Dir, AnalysisContext{
		RepoFullName:    fullName,
		PRNumber:        number,
		Onto:            onto,
		RebasedCommits:  rebased,
		UpstreamCommits: upstream,
		Patterns:        pats,
		MaxPatterns:     opts.MaxPatterns,
	})
	if err != nil {
		return fmt.Errorf("analyzing rebased commits: %w", err)
	}

	var rendered strings.Builder
	report.NewRenderer(&rendered).RenderRebaseAnalysisMarkdown(*analysis, fullName, number, onto, opts.Version)
	_, _ = fmt.Fprintf(w, "\n%s\n", rendered.String())

	r.postAnalysisComment(w, opts, owner, repo, number, rendered.String())

	if opts.ApplyAdjustments {
		applyReport, err := r.Claude.ApplyAdjustments(pr.Dir, ApplyContext{
			RepoFullName: fullName,
			PRNumber:     number,
			Onto:         onto,
			HeadBranch:   pr.HeadBranch,
			Analysis:     *analysis,
			Patterns:     pats,
			MaxPatterns:  opts.MaxPatterns,
		})
		if err != nil {
			return fmt.Errorf("applying rebase adjustments: %w", err)
		}
		if applyReport != "" {
			_, _ = fmt.Fprintf(w, "\nClaude apply report:\n%s\n", applyReport)
		}
	}
	return nil
}

// dryRun prints the rebase plan and probes for the first conflicting commit,
// always restoring the pre-rebase state so --dry-run never mutates the
// checkout, and never invokes Claude, applies adjustments, or pushes.
func (r *Runner) dryRun(w io.Writer, pr *github.PR, onto, origin, origMergeBase string, replay []github.Commit) error {
	_, _ = fmt.Fprintf(w, "Rebase plan: replay %d commit(s) from %s onto %s\n", len(replay), pr.HeadBranch, origin)
	for _, c := range replay {
		_, _ = fmt.Fprintf(w, "  - %s %s\n", shortSHA(c.SHA), c.Subject)
	}
	if upstream, err := r.GitHub.CommitsInRange(pr.Dir, origMergeBase+".."+origin); err == nil {
		_, _ = fmt.Fprintf(w, "Upstream range: %d commit(s) entered %s since the PR forked.\n", len(upstream), origin)
	}

	state, err := r.GitHub.StartRebase(pr.Dir, onto)
	if err != nil {
		return fmt.Errorf("[dry-run] probing rebase onto %s: %w", onto, err)
	}
	if state.Conflicted {
		_, _ = fmt.Fprintf(w, "[dry-run] First conflicting commit: %s %q (files: %s)\n",
			shortSHA(state.StoppedSHA), state.StoppedSubject, strings.Join(state.ConflictedFiles, ", "))
	} else {
		_, _ = fmt.Fprintln(w, "[dry-run] No conflicts detected — the rebase would apply cleanly.")
	}

	// Restore the pre-rebase state unconditionally: abort an in-progress
	// rebase, then reset back in case a clean probe advanced HEAD.
	_ = r.GitHub.RebaseAbort(pr.Dir)
	if err := r.GitHub.ResetHard(pr.Dir, pr.HeadSHA); err != nil {
		slog.Warn("restoring working tree after dry-run probe failed", "err", err)
	}
	return nil
}

// printAnalysisPrompt renders the analysis prompt from the computed ranges
// (without rebasing) and writes it to w. The rebased-commit list is the
// pre-rebase replay set, so the prompt shows exactly which commits would be
// analyzed against the upstream range.
func (r *Runner) printAnalysisPrompt(w io.Writer, opts Options, pr *github.PR, onto, origin, origMergeBase string, replay []github.Commit, pats []patterns.Pattern) error {
	upstream, err := r.GitHub.CommitsInRange(pr.Dir, origMergeBase+".."+origin)
	if err != nil {
		return fmt.Errorf("listing upstream commits: %w", err)
	}
	prompt := r.AnalysisPrompt(AnalysisContext{
		RepoFullName:    fmt.Sprintf("%s/%s", pr.Owner, pr.Repo),
		PRNumber:        pr.Number,
		Onto:            onto,
		RebasedCommits:  replay,
		UpstreamCommits: upstream,
		Patterns:        pats,
		MaxPatterns:     opts.MaxPatterns,
	})
	if _, err := io.WriteString(w, prompt); err != nil {
		return fmt.Errorf("writing prompt: %w", err)
	}
	if !strings.HasSuffix(prompt, "\n") {
		_, _ = fmt.Fprintln(w)
	}
	return nil
}

// applyDefaults fills in zero-valued options with their defaults.
func (r *Runner) applyDefaults(opts *Options) {
	if opts.Onto == "" {
		opts.Onto = DefaultOnto
	}
	if opts.MaxIterations <= 0 {
		opts.MaxIterations = DefaultMaxIterations
	}
	if r.GitHub == nil {
		r.GitHub = defaultGitClient{}
	}
}

// fetchPR resolves the PR for opts: a no-clone local checkout when opts.Local
// is set, otherwise a temp-dir clone+checkout.
func (r *Runner) fetchPR(opts Options) (*github.PR, error) {
	if opts.Local {
		return r.GitHub.FetchAndCheckoutLocal(opts.PRRef, localOptions(opts))
	}
	return r.GitHub.FetchAndCheckout(opts.PRRef)
}

// localOptions builds the github.LocalOptions for a --local run, wiring the
// stdin prompter that backs the dirty-tree confirmation.
func localOptions(opts Options) github.LocalOptions {
	return github.LocalOptions{Force: opts.Force, Prompter: workspace.NewStdinPrompter()}
}

// analysisCommentFooter attributes the posted analysis to planwerk-review,
// matching the footer the other subcommands append to GitHub artifacts.
const analysisCommentFooter = "_Rebase analysis generated by [planwerk-review](https://github.com/planwerk/planwerk-review) rebase with Claude Code_"

// postAnalysisComment posts the rendered analysis as a fresh comment on the PR
// so the record of what the rebase implied lives on the PR itself. Disabled by
// --no-analysis-comment. Best-effort: a GitHub failure is logged and surfaced
// but never aborts the run — the rebase already succeeded.
func (r *Runner) postAnalysisComment(w io.Writer, opts Options, owner, repo string, number int, body string) {
	if opts.NoAnalysisComment {
		return
	}
	comment := strings.TrimSpace(body) + "\n\n---\n\n" + analysisCommentFooter + "\n"
	url, err := r.GitHub.AddPRComment(owner, repo, number, comment)
	if err != nil {
		slog.Warn("posting analysis comment failed", "pr", number, "err", err)
		_, _ = fmt.Fprintf(w, "\nCould not post the analysis as a PR comment: %v\n", err)
		return
	}
	slog.Info("posted rebase analysis comment", "pr", number, "url", url)
	_, _ = fmt.Fprintf(w, "Posted the rebase analysis as a comment on PR #%d", number)
	if url != "" {
		_, _ = fmt.Fprintf(w, " (%s)", url)
	}
	_, _ = fmt.Fprintln(w)
}

// shortSHA abbreviates a commit SHA for display.
func shortSHA(sha string) string {
	if len(sha) <= 7 {
		return sha
	}
	return sha[:7]
}

// loadPatterns runs technology detection on the checkout and loads the
// review-pattern catalog filtered by those tags, so both the conflict
// resolution and the analysis are grounded in the same set plus any
// project-specific patterns under .planwerk/review_patterns/. Failures are
// non-fatal: the run falls back to no patterns.
func loadPatterns(opts Options, repoDir string) []patterns.Pattern {
	tags := detect.Technologies(repoDir)
	if len(tags) > 0 {
		slog.Info("detected technologies", "technologies", strings.Join(tags, ", "))
	}
	dirs, err := patterns.Resolve(patterns.ResolveOptions{
		NoLocal: opts.NoLocalPatterns,
		NoRepo:  opts.NoRepoPatterns,
		RepoDir: repoDir,
		Extra:   opts.PatternDirs,
	})
	if err != nil {
		slog.Warn("resolving pattern sources failed; continuing without them", "err", err)
	}
	pats, err := patterns.LoadFilteredWithOptions(patterns.LoadOptions{Remote: patterns.RemoteOpts(), NoEmbedded: opts.NoLocalPatterns}, tags, dirs...)
	if err != nil {
		slog.Warn("loading review patterns failed; continuing without them", "err", err)
		return nil
	}
	if len(pats) > 0 {
		slog.Info("loaded review patterns", "count", len(pats))
	}
	return pats
}
