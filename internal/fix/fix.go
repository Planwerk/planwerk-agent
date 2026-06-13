package fix

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/planwerk/planwerk-review/internal/detect"
	"github.com/planwerk/planwerk-review/internal/github"
	"github.com/planwerk/planwerk-review/internal/patterns"
	"github.com/planwerk/planwerk-review/internal/workspace"
)

// Default loop parameters. Each can be overridden via Options / CLI flags.
const (
	DefaultPollInterval  = 1 * time.Minute
	DefaultMaxIterations = 5
	// MaxLogChars caps how much of any single failed-step log we keep in the
	// per-iteration prompt, before tail-trimming to the last lines. The
	// claude package then keeps only the last 200 lines of what we pass.
	MaxLogChars = 64 * 1024

	// BundledPatternsURLBase is the public raw-markdown URL prefix the
	// bare-prompt catalog uses to point Claude at planwerk-review's bundled
	// pattern files. We pin to "main" so manual sessions always pick up the
	// latest patterns without us baking the binary's version into URLs that
	// then drift on dev builds.
	BundledPatternsURLBase = "https://raw.githubusercontent.com/planwerk/planwerk-review/main/internal/patterns/patterns"
)

// Options configures the fix subcommand. Mirrors the Options style used by
// the review/audit/elaborate packages.
type Options struct {
	PRRef         string
	PollInterval  time.Duration // delay between status polls; defaults to DefaultPollInterval
	MaxIterations int           // safety cap on fix attempts; defaults to DefaultMaxIterations
	Interactive   bool          // ask before each new fix iteration
	DryRun        bool          // skip the Claude invocation; report status only
	PrintPrompt   bool          // render the fix prompt to stdout and exit; never invoke Claude
	NoFixComment  bool          // do not post each iteration's fix report as a PR comment
	Local         bool          // operate on the current working directory instead of cloning
	Force         bool          // with Local, skip the dirty-working-tree confirmation prompt
	Version       string

	// Pattern loading mirrors review/audit/elaborate so the fix is grounded
	// in the same review catalog and any project-specific patterns under
	// .planwerk/review_patterns/ in the target repo.
	PatternDirs     []string
	NoRepoPatterns  bool
	NoLocalPatterns bool
	MaxPatterns     int
}

// Runner glues together the GitHub status query, the Claude fixer, and the
// interactive prompt. Tests inject fakes via the exported fields.
type Runner struct {
	Claude   ClaudeFixer
	GitHub   GitHubClient
	Prompter Prompter
	// BuildPrompt renders the fix prompt without invoking Claude. Required
	// when Options.PrintPrompt is set; nil otherwise is fine.
	BuildPrompt PromptBuildFn
	// Sleep is overridable so tests don't actually sleep between polls.
	Sleep func(time.Duration)
	// Now is overridable so iteration banners are deterministic in tests.
	Now func() time.Time
}

// NewRunner builds a Runner with the production GitHub backend, the given
// Claude fix function, and the prompt builder wired in. The CLI is expected
// to call this with claude.Fix and claude.BuildFixPrompt so the import
// direction stays claude -> fix.
func NewRunner(fn FixFn, build PromptBuildFn) *Runner {
	return &Runner{
		Claude:      fixFnAdapter{fn: fn},
		GitHub:      defaultGitHubClient{},
		Prompter:    stdinPrompter{In: os.Stdin, Out: os.Stderr},
		BuildPrompt: build,
		Sleep:       time.Sleep,
		Now:         time.Now,
	}
}

// Run is a package-level convenience that delegates to NewRunner(fn, build).Run.
func Run(w io.Writer, opts Options, fn FixFn, build PromptBuildFn) error {
	return NewRunner(fn, build).Run(w, opts)
}

// localOptions builds the github.LocalOptions for a --local run from the fix
// Options, wiring the stdin prompter that backs the dirty-tree confirmation.
func localOptions(opts Options) github.LocalOptions {
	return github.LocalOptions{Force: opts.Force, Prompter: workspace.NewStdinPrompter()}
}

// fetchPR resolves the PR for opts: a no-clone local checkout when opts.Local
// is set, otherwise the temp-dir clone+checkout. Used for the initial metadata
// fetch and the bare-prompt build.
func (r *Runner) fetchPR(opts Options) (*github.PR, error) {
	if opts.Local {
		return r.GitHub.FetchAndCheckoutLocal(opts.PRRef, localOptions(opts))
	}
	return r.GitHub.FetchAndCheckout(opts.PRRef)
}

// PrintBarePrompt is a package-level convenience that delegates to
// NewRunner(nil, nil).PrintBarePrompt. The prompt itself is built without
// invoking Claude, so the FixFn / PromptBuildFn passed to NewRunner are
// not used here.
func PrintBarePrompt(w io.Writer, opts Options, build BarePromptBuildFn) error {
	return NewRunner(nil, nil).PrintBarePrompt(w, opts, build)
}

// PrintBarePrompt builds a self-contained ("bare") fix prompt from the PR
// reference. Even though no Claude call is made, we still clone the target
// repo so the prompt can carry concrete context: detected technologies and
// the filtered review-pattern catalog (local + .planwerk/review_patterns/
// + --patterns sources), inlined so the manual Claude session that pastes
// this prompt does not need access to planwerk-review or its pattern dirs.
//
// The pasted-into Claude session is still expected to operate on its own
// checkout of the PR head; the rendered prompt instructs it to discover and
// fix the failing checks itself.
func (r *Runner) PrintBarePrompt(w io.Writer, opts Options, build BarePromptBuildFn) error {
	if build == nil {
		return errors.New("--print-bare-prompt requires a prompt builder; wire claude.BuildBareFixPrompt")
	}
	// In non-local mode validate the ref up front so a bad ref fails fast
	// before any clone. In local mode the ref may be empty (inferred from the
	// current branch), so identity is read from the resolved PR instead.
	if !opts.Local {
		if _, _, _, err := github.ParseRef(opts.PRRef); err != nil {
			return fmt.Errorf("parsing PR ref: %w", err)
		}
	}

	// Clone the PR head so we can run tech detection and pick the right
	// pattern subset. In --local mode this is the user's working tree; the
	// prompt is the only artifact that escapes this call.
	pr, err := r.fetchPR(opts)
	if err != nil {
		return fmt.Errorf("fetching PR for bare prompt build: %w", err)
	}
	defer pr.Cleanup()
	// Identity comes from the resolved PR so it works whether the ref was
	// explicit or inferred from the local branch.
	owner, repo, number := pr.Owner, pr.Repo, pr.Number

	tags := detect.Technologies(pr.Dir)
	if len(tags) > 0 {
		slog.Info("detected technologies for bare prompt", "technologies", strings.Join(tags, ", "))
	}
	dirs, err := patterns.Resolve(patterns.ResolveOptions{
		NoLocal: opts.NoLocalPatterns,
		NoRepo:  opts.NoRepoPatterns,
		RepoDir: pr.Dir,
		Extra:   opts.PatternDirs,
	})
	if err != nil {
		slog.Warn("resolving pattern sources failed; bare prompt will omit them", "err", err)
	}
	pats, err := patterns.LoadFilteredWithOptions(patterns.LoadOptions{Remote: patterns.RemoteOpts(), NoEmbedded: opts.NoLocalPatterns}, tags, dirs...)
	if err != nil {
		slog.Warn("loading review patterns failed; bare prompt will omit them", "err", err)
		pats = nil
	}
	if len(pats) > 0 {
		slog.Info("loaded review patterns for bare prompt", "count", len(pats))
	}

	catalog := patterns.BuildCatalogReferences(pats, patterns.CatalogRefOptions{
		BundledRoot:    patterns.LocalPatternDir(opts.NoLocalPatterns),
		BundledURLBase: BundledPatternsURLBase,
		RepoRoot:       patterns.RepoPatternDir(opts.NoRepoPatterns, pr.Dir),
		RepoRelBase:    ".planwerk/review_patterns",
	})

	hasRepoLocal := false
	for _, c := range catalog {
		if c.LocalPath != "" {
			hasRepoLocal = true
			break
		}
	}

	prompt := build(BareContext{
		RepoFullName:     fmt.Sprintf("%s/%s", owner, repo),
		PRNumber:         number,
		TechTags:         tags,
		PatternCatalog:   catalog,
		BundledURLBase:   BundledPatternsURLBase,
		HasRepoLocalRefs: hasRepoLocal,
	})
	if _, err := io.WriteString(w, prompt); err != nil {
		return fmt.Errorf("writing prompt: %w", err)
	}
	if !strings.HasSuffix(prompt, "\n") {
		_, _ = fmt.Fprintln(w)
	}
	return nil
}

// ErrMaxIterations is returned when the loop exhausts its iteration budget
// before all checks turn green. It is exported so the CLI can distinguish
// "Claude bailed" from "we ran out of attempts".
var ErrMaxIterations = errors.New("reached max iterations without all checks passing")

// ErrUserStopped is returned when the user answers "no" to the interactive
// "continue?" prompt.
var ErrUserStopped = errors.New("stopped by user")

// Run executes the fix loop:
//  1. Resolve the PR (gh CLI).
//  2. Wait for in-flight checks to complete.
//  3. If all green, exit success.
//  4. Otherwise fetch failed-step logs, optionally confirm with user, then
//     run a fresh Claude session inside a fresh checkout to apply a fix and
//     push it as a follow-up commit.
//  5. Wait for the new commit's checks to start, then loop back to step 2.
func (r *Runner) Run(w io.Writer, opts Options) error {
	r.applyDefaults(&opts)

	if opts.PrintPrompt && r.BuildPrompt == nil {
		return errors.New("--print-prompt requires a prompt builder; wire claude.BuildFixPrompt via NewRunner")
	}
	if !opts.Local && opts.PRRef == "" {
		return errors.New("a PR reference is required (or use --local)")
	}

	// Initial PR fetch — we need head branch + title for context, plus the
	// initial head SHA to query checks against, plus the repo identity. In
	// --local mode this also performs gh pr checkout + base fetch on the user's
	// working tree; in temp-dir mode the checkout is throw-away metadata.
	pr, err := r.fetchPR(opts)
	if err != nil {
		return fmt.Errorf("fetching PR: %w", err)
	}
	owner, repo, number := pr.Owner, pr.Repo, pr.Number
	fullName := fmt.Sprintf("%s/%s", owner, repo)
	slog.Info("fix loop starting",
		"pr", fmt.Sprintf("%s#%d", fullName, number),
		"interval", opts.PollInterval,
		"max_iterations", opts.MaxIterations,
		"interactive", opts.Interactive,
		"dry_run", opts.DryRun,
		"print_prompt", opts.PrintPrompt,
		"local", opts.Local,
	)
	if opts.Local {
		slog.Info("working tree left on PR branch", "branch", pr.HeadBranch, "dir", pr.Dir)
	} else {
		pr.Cleanup() // we only needed the metadata; subsequent iterations re-clone
	}

	// In --print-prompt mode the only stdout payload is the prompt itself;
	// status chatter (iteration banner, polling progress, failure banner) is
	// silenced so the output is safe to pipe into another tool.
	statusW := w
	if opts.PrintPrompt {
		statusW = io.Discard
	}

	currentSHA := pr.HeadSHA

	for iteration := 1; iteration <= opts.MaxIterations; iteration++ {
		_, _ = fmt.Fprintf(statusW, "\n=== Iteration %d/%d for %s#%d (head %s) ===\n",
			iteration, opts.MaxIterations, fullName, number, shortSHA(currentSHA))

		summary, err := r.waitForChecks(statusW, owner, repo, currentSHA, opts.PollInterval)
		if err != nil {
			return err
		}

		if summary.AllPassed() {
			_, _ = fmt.Fprintf(w, "All %d checks passed on %s. Done.\n", summary.Total, shortSHA(currentSHA))
			slog.Info("fix loop complete", "head", currentSHA, "passed", len(summary.Passed))
			return nil
		}

		printFailureBanner(statusW, summary)

		if opts.Interactive && iteration > 1 {
			ok, err := r.Prompter.Confirm(fmt.Sprintf(
				"Continue with iteration %d/%d? (y/N): ", iteration, opts.MaxIterations))
			if err != nil {
				return fmt.Errorf("interactive prompt: %w", err)
			}
			if !ok {
				return ErrUserStopped
			}
		}

		if opts.DryRun {
			_, _ = fmt.Fprintln(w, "[dry-run] skipping fix invocation")
			return nil
		}

		failed := r.collectFailedChecks(owner, repo, summary.Failed)

		// In --print-prompt mode there is no fresh checkout, so we cannot
		// detect technologies or load .planwerk/review_patterns from the
		// target repo. The bare prompt instead instructs Claude to inspect
		// those locations itself if they exist.
		if opts.PrintPrompt {
			prompt := r.BuildPrompt(Context{
				RepoFullName:  fullName,
				PRNumber:      number,
				PRTitle:       pr.Title,
				HeadBranch:    pr.HeadBranch,
				HeadSHA:       currentSHA,
				Iteration:     iteration,
				MaxIterations: opts.MaxIterations,
				FailedChecks:  failed,
				MaxPatterns:   opts.MaxPatterns,
				Local:         opts.Local,
				BaseBranch:    pr.BaseBranch,
			})
			if _, err := io.WriteString(w, prompt); err != nil {
				return fmt.Errorf("writing prompt: %w", err)
			}
			if !strings.HasSuffix(prompt, "\n") {
				_, _ = fmt.Fprintln(w)
			}
			return nil
		}

		// Ensure the Claude session sees the latest head — which now includes
		// any follow-up commits the previous iteration pushed. In --local mode
		// we keep the user's working tree and fast-forward it (Safety #6);
		// otherwise we take a fresh throw-away checkout.
		var fresh *github.PR
		if opts.Local {
			if iteration > 1 {
				if err := r.GitHub.PullOnBranch(pr.Dir, pr.HeadBranch); err != nil {
					return fmt.Errorf("refreshing local checkout for iteration %d: %w", iteration, err)
				}
			}
			pr.HeadSHA = currentSHA
			fresh = pr // same working tree; Cleanup is a no-op because Local
		} else {
			fresh, err = r.GitHub.FetchAndCheckout(opts.PRRef)
			if err != nil {
				return fmt.Errorf("re-checking out PR for iteration %d: %w", iteration, err)
			}
		}

		pats := loadPatterns(opts, fresh.Dir)

		report, fixErr := r.Claude.Fix(fresh.Dir, Context{
			RepoFullName:  fullName,
			PRNumber:      number,
			PRTitle:       pr.Title,
			HeadBranch:    pr.HeadBranch,
			HeadSHA:       fresh.HeadSHA,
			Iteration:     iteration,
			MaxIterations: opts.MaxIterations,
			FailedChecks:  failed,
			Patterns:      pats,
			MaxPatterns:   opts.MaxPatterns,
			Local:         opts.Local,
			BaseBranch:    pr.BaseBranch,
		})
		fresh.Cleanup()
		if fixErr != nil {
			return fmt.Errorf("claude fix iteration %d: %w", iteration, fixErr)
		}
		if report != "" {
			_, _ = fmt.Fprintf(w, "\nClaude fix report:\n%s\n", report)
			// Record what this iteration changed on the PR itself — posted
			// before the escalation check below, so an escalated report still
			// lands where the human who must intervene will see it.
			r.postFixComment(w, opts, owner, repo, number, report)
		}

		// React to the session's terminal status: an escalation means stop and
		// hand off rather than burn another iteration on a fix that signaled it
		// cannot proceed.
		switch status := parseStatus(report); {
		case status.ShouldEscalate():
			_, _ = fmt.Fprintf(w, "\nClaude reported %s — stopping the fix loop and escalating instead of retrying.\n", status)
			return fmt.Errorf("fix escalated with status %s on iteration %d", status, iteration)
		case status == StatusDoneWithConcerns:
			slog.Warn("fix iteration reported DONE_WITH_CONCERNS", "iteration", iteration)
		}

		// After the push, give the remote a moment, then re-resolve the head
		// SHA so the next iteration polls against the new commit's checks.
		newSHA, err := r.waitForNewHead(owner, repo, pr.HeadBranch, currentSHA, opts.PollInterval)
		if err != nil {
			return err
		}
		if newSHA == currentSHA {
			_, _ = fmt.Fprintln(w, "No new commit detected on the head branch — Claude likely had nothing to push. Stopping.")
			return fmt.Errorf("iteration %d produced no new commit on %s", iteration, pr.HeadBranch)
		}
		currentSHA = newSHA
	}

	return fmt.Errorf("%w (after %d iterations on %s#%d)", ErrMaxIterations, opts.MaxIterations, fullName, number)
}

// applyDefaults fills in zero-valued options with their defaults.
func (r *Runner) applyDefaults(opts *Options) {
	if opts.PollInterval <= 0 {
		opts.PollInterval = DefaultPollInterval
	}
	if opts.MaxIterations <= 0 {
		opts.MaxIterations = DefaultMaxIterations
	}
	if r.Sleep == nil {
		r.Sleep = time.Sleep
	}
	if r.Now == nil {
		r.Now = time.Now
	}
	if r.Prompter == nil {
		r.Prompter = stdinPrompter{In: os.Stdin, Out: os.Stderr}
	}
}

// waitForChecks polls the Checks API every interval until no checks are
// pending. Returns the final summary so the caller can branch on
// pass/fail. Logs only when state changes to keep CI logs readable.
func (r *Runner) waitForChecks(w io.Writer, owner, repo, sha string, interval time.Duration) (github.CheckRunSummary, error) {
	var lastPending = -1
	for {
		runs, err := r.GitHub.ListChecks(owner, repo, sha)
		if err != nil {
			return github.CheckRunSummary{}, fmt.Errorf("listing checks for %s: %w", shortSHA(sha), err)
		}
		summary := github.SummarizeChecks(runs)
		if summary.Total == 0 {
			_, _ = fmt.Fprintf(w, "No checks reported yet for %s; waiting %s...\n", shortSHA(sha), interval)
			r.Sleep(interval)
			continue
		}
		if !summary.AnyPending() {
			_, _ = fmt.Fprintf(w, "Checks complete for %s: %d passed, %d failed\n",
				shortSHA(sha), len(summary.Passed), len(summary.Failed))
			return summary, nil
		}
		if len(summary.Pending) != lastPending {
			_, _ = fmt.Fprintf(w, "Waiting on %d/%d pending check(s) for %s...\n",
				len(summary.Pending), summary.Total, shortSHA(sha))
			lastPending = len(summary.Pending)
		}
		r.Sleep(interval)
	}
}

// waitForNewHead polls the branch HEAD until it advances past oldSHA, so the
// next iteration's check-status query targets the commit Claude just pushed.
// Bounded by maxAttempts to avoid waiting forever if the push silently
// failed.
func (r *Runner) waitForNewHead(owner, repo, branch, oldSHA string, interval time.Duration) (string, error) {
	const maxAttempts = 12 // ~12 * interval before we give up
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		sha, err := r.GitHub.HeadSHA(owner, repo, branch)
		if err != nil {
			return "", fmt.Errorf("polling branch head %s: %w", branch, err)
		}
		if sha != oldSHA {
			return sha, nil
		}
		r.Sleep(interval)
	}
	// Return the (still-unchanged) SHA; caller decides what to do.
	return oldSHA, nil
}

// collectFailedChecks fetches workflow logs for each failed Actions-backed
// check. Failures from third-party providers (no Actions run id) get an
// empty Logs field — the prompt notes this case explicitly.
func (r *Runner) collectFailedChecks(owner, repo string, failed []github.CheckRun) []FailedCheck {
	// Sort for deterministic prompt order — easier to diff identical
	// iterations when debugging.
	sort.Slice(failed, func(i, j int) bool { return failed[i].Name < failed[j].Name })

	out := make([]FailedCheck, 0, len(failed))
	for _, c := range failed {
		fc := FailedCheck{
			Name:          c.Name,
			Conclusion:    c.Conclusion,
			HTMLURL:       c.HTMLURL,
			OutputTitle:   c.Output.Title,
			OutputSummary: c.Output.Summary,
			WorkflowRunID: c.WorkflowRunID,
		}
		if c.WorkflowRunID != 0 {
			logs, err := r.GitHub.FailedRunLogs(owner, repo, c.WorkflowRunID)
			if err != nil {
				slog.Warn("could not fetch failed-step logs",
					"check", c.Name, "run_id", c.WorkflowRunID, "err", err)
			} else {
				fc.Logs = trimLogs(logs, MaxLogChars)
			}
		}
		out = append(out, fc)
	}
	return out
}

// trimLogs caps a log payload at maxChars by keeping the trailing window —
// CI failure messages cluster at the end. A short header is prepended so
// Claude knows that earlier output was elided.
func trimLogs(s string, maxChars int) string {
	if maxChars <= 0 || len(s) <= maxChars {
		return s
	}
	return fmt.Sprintf("[... %d earlier characters truncated ...]\n%s",
		len(s)-maxChars, s[len(s)-maxChars:])
}

func printFailureBanner(w io.Writer, summary github.CheckRunSummary) {
	_, _ = fmt.Fprintf(w, "%d failed check(s):\n", len(summary.Failed))
	for _, f := range summary.Failed {
		_, _ = fmt.Fprintf(w, "  - %s (%s) %s\n", f.Name, f.Conclusion, f.HTMLURL)
	}
}

func shortSHA(sha string) string {
	if len(sha) <= 7 {
		return sha
	}
	return sha[:7]
}

// fixCommentFooter attributes the posted fix report to planwerk-review,
// matching the footer the implement/propose/elaborate/audit subcommands append
// to the artifacts they leave on GitHub.
const fixCommentFooter = "_Fix report generated by [planwerk-review](https://github.com/planwerk/planwerk-review) fix with Claude Code_"

// fixReportHeading is the heading the fix prompt mandates as the first line of
// every report ("## Fix Report (iteration N)"). stripFixReportHeading anchors
// on it to drop the report framing from the PR comment.
const fixReportHeading = "## Fix Report"

// postFixComment posts the iteration's fix report as a fresh comment on the PR,
// so the record of what each pushed follow-up commit changed lives on the PR
// itself — the same way the implement command records its plan on the source
// issue. Disabled by --no-fix-comment.
//
// Posting is best-effort: a failure to reach GitHub is logged and surfaced to
// the operator but never aborts the loop — the fix is already pushed, and the
// next poll cycle can still proceed.
func (r *Runner) postFixComment(w io.Writer, opts Options, owner, repo string, number int, report string) {
	if opts.NoFixComment {
		return
	}
	url, err := r.GitHub.AddPRComment(owner, repo, number, formatFixComment(report))
	if err != nil {
		slog.Warn("posting fix comment failed", "pr", number, "err", err)
		_, _ = fmt.Fprintf(w, "\nCould not post the fix report as a PR comment: %v\n", err)
		return
	}
	slog.Info("posted fix report comment", "pr", number, "url", url)
	_, _ = fmt.Fprintf(w, "\nPosted the fix report as a comment on PR #%d", number)
	if url != "" {
		_, _ = fmt.Fprintf(w, " (%s)", url)
	}
	_, _ = fmt.Fprintln(w)
}

// formatFixComment builds the PR-comment body from the iteration's report: the
// report with its "## Fix Report (iteration N)" heading stripped — the comment
// carries just what was fixed, not the report framing — followed by the
// attribution footer.
func formatFixComment(report string) string {
	return stripFixReportHeading(report) + "\n\n---\n\n" + fixCommentFooter + "\n"
}

// stripFixReportHeading removes a leading "## Fix Report ..." heading from the
// report, returning just the body that follows it — what was actually fixed.
// When the heading is absent (unexpected output) the trimmed report is returned
// unchanged rather than risk discarding content.
func stripFixReportHeading(report string) string {
	lines := strings.Split(report, "\n")
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), fixReportHeading) {
			return strings.TrimSpace(strings.Join(lines[i+1:], "\n"))
		}
	}
	return strings.TrimSpace(report)
}

// stdinPrompter is the production Prompter: reads a single y/n line from
// stdin and writes the question to the given Writer (typically stderr so it
// stays visible when the caller is redirecting stdout). It is a thin alias
// over workspace.StdinPrompter so the dirty-tree gate and the interactive
// fix-iteration prompt share one implementation.
type stdinPrompter = workspace.StdinPrompter

// loadPatterns runs technology detection on the fresh checkout and loads
// the review-pattern catalog filtered by those tags. Mirrors the
// audit/elaborate/propose helpers so the fix prompt is grounded in the same
// pattern set the rest of the tool uses, plus any project-specific patterns
// under .planwerk/review_patterns/ in the target repo.
//
// Failures are non-fatal: the loop falls back to running without patterns
// rather than blocking a CI fix on a corrupt pattern source.
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
