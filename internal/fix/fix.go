package fix

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/planwerk/planwerk-review/internal/github"
)

// Default loop parameters. Each can be overridden via Options / CLI flags.
const (
	DefaultPollInterval  = 1 * time.Minute
	DefaultMaxIterations = 5
	// MaxLogChars caps how much of any single failed-step log we keep in the
	// per-iteration prompt, before tail-trimming to the last lines. The
	// claude package then keeps only the last 200 lines of what we pass.
	MaxLogChars = 64 * 1024
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
	Version       string
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

	owner, repo, number, err := github.ParseRef(opts.PRRef)
	if err != nil {
		return fmt.Errorf("parsing PR ref: %w", err)
	}
	fullName := fmt.Sprintf("%s/%s", owner, repo)
	slog.Info("fix loop starting",
		"pr", fmt.Sprintf("%s#%d", fullName, number),
		"interval", opts.PollInterval,
		"max_iterations", opts.MaxIterations,
		"interactive", opts.Interactive,
		"dry_run", opts.DryRun,
		"print_prompt", opts.PrintPrompt,
	)

	// In --print-prompt mode the only stdout payload is the prompt itself;
	// status chatter (iteration banner, polling progress, failure banner) is
	// silenced so the output is safe to pipe into another tool.
	statusW := w
	if opts.PrintPrompt {
		statusW = io.Discard
	}

	// Initial PR fetch — we need head branch + title for context, plus the
	// initial head SHA to query checks against.
	pr, err := r.GitHub.FetchAndCheckout(opts.PRRef)
	if err != nil {
		return fmt.Errorf("fetching PR: %w", err)
	}
	pr.Cleanup() // we only needed the metadata; subsequent iterations re-clone

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
			})
			if _, err := io.WriteString(w, prompt); err != nil {
				return fmt.Errorf("writing prompt: %w", err)
			}
			if !strings.HasSuffix(prompt, "\n") {
				_, _ = fmt.Fprintln(w)
			}
			return nil
		}

		// Fresh checkout per iteration ensures the Claude session sees the
		// latest head — which now includes any follow-up commits the previous
		// iteration pushed.
		fresh, err := r.GitHub.FetchAndCheckout(opts.PRRef)
		if err != nil {
			return fmt.Errorf("re-checking out PR for iteration %d: %w", iteration, err)
		}

		report, fixErr := r.Claude.Fix(fresh.Dir, Context{
			RepoFullName:  fullName,
			PRNumber:      number,
			PRTitle:       pr.Title,
			HeadBranch:    pr.HeadBranch,
			HeadSHA:       fresh.HeadSHA,
			Iteration:     iteration,
			MaxIterations: opts.MaxIterations,
			FailedChecks:  failed,
		})
		fresh.Cleanup()
		if fixErr != nil {
			return fmt.Errorf("claude fix iteration %d: %w", iteration, fixErr)
		}
		if report != "" {
			_, _ = fmt.Fprintf(w, "\nClaude fix report:\n%s\n", report)
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

// stdinPrompter is the production Prompter: reads a single y/n line from
// stdin and writes the question to the given Writer (typically stderr so it
// stays visible when the caller is redirecting stdout).
type stdinPrompter struct {
	In  io.Reader
	Out io.Writer
}

func (p stdinPrompter) Confirm(message string) (bool, error) {
	if _, err := fmt.Fprint(p.Out, message); err != nil {
		return false, err
	}
	r := bufio.NewReader(p.In)
	line, err := r.ReadString('\n')
	if err != nil && err != io.EOF {
		return false, err
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes", nil
}
