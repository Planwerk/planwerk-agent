// Package address orchestrates the `address` subcommand: it fetches a PR's
// human review threads, lets the operator select which unresolved ones to act
// on, dispatches Claude to incorporate each as a follow-up commit, pushes the
// commits, and (gated) replies to and resolves each addressed thread. The Go
// orchestrator owns the loop, the push, and every reply/resolve; Claude does
// the per-thread code change.
package address

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/planwerk/planwerk-agent/internal/detect"
	"github.com/planwerk/planwerk-agent/internal/github"
	"github.com/planwerk/planwerk-agent/internal/patterns"
	"github.com/planwerk/planwerk-agent/internal/report"
	"github.com/planwerk/planwerk-agent/internal/skills"
	"github.com/planwerk/planwerk-agent/internal/styleguide"
	"github.com/planwerk/planwerk-agent/internal/workspace"
)

const (
	// DefaultMaxIterations caps how many threads the per-thread loop addresses
	// in one run before reporting the remainder. It is higher than fix's
	// default because an operator may select many threads at once.
	DefaultMaxIterations = 10

	// BundledPatternsURLBase is the public raw-markdown URL prefix the
	// bare-prompt catalog uses to point Claude at planwerk-agent's bundled
	// pattern files. Pinned to "main" so manual sessions always pick up the
	// latest patterns, mirroring the fix and rebase packages.
	BundledPatternsURLBase = "https://raw.githubusercontent.com/planwerk/planwerk-agent/main/internal/patterns/patterns"
)

// Options configures the address subcommand. Mirrors the Options style used by
// the fix and rebase packages.
type Options struct {
	PRRef           string
	All             bool     // address every unresolved thread without prompting
	ThreadIDs       []string // address only these thread IDs (repeatable --thread)
	IncludeResolved bool     // also offer threads GitHub already marks resolved
	Reply           bool     // post a per-thread reply summarizing the change
	Resolve         bool     // mark addressed threads resolved (outward-facing)
	// OneCommitPerThread keeps one commit per thread (default) so the
	// comment->commit mapping stays legible; false folds all selected threads
	// into a single aggregate commit.
	OneCommitPerThread bool
	NoAddressComment   bool // do not post the aggregate report as a PR comment
	MaxIterations      int  // cap on per-thread address iterations
	DryRun             bool // list the selected threads and exit; no Claude, no commit
	PrintPrompt        bool // render the address prompt and exit; no Claude, no commit
	Local              bool // operate on the current working directory instead of cloning
	Force              bool // with Local, skip the dirty-working-tree confirmation prompt
	Version            string

	// Pattern loading mirrors fix/rebase so the change is grounded in the same
	// review catalog plus any project-specific patterns under
	// .planwerk/review_patterns/ in the target repo.
	PatternDirs     []string
	NoRepoPatterns  bool
	NoLocalPatterns bool
	MaxPatterns     int
	// Remote configures how remote pattern URIs (--patterns github:..., git+...)
	// resolve into local directories; carries the --remote-patterns-ttl value.
	Remote patterns.RemoteOptions
}

// Runner glues together the GitHub client, the Claude addresser, and the
// interactive thread selector. Tests inject fakes via the exported fields.
type Runner struct {
	Claude ClaudeAddresser
	GitHub GitHubClient
	// BuildPrompt renders the address prompt without invoking Claude. Required
	// when Options.PrintPrompt is set; nil otherwise is fine.
	BuildPrompt PromptBuildFn
	// In is the stream the interactive thread selector reads from.
	In io.Reader
	// IsTTY reports whether the selector should prompt interactively. When it
	// returns false the run defaults to addressing every selected thread.
	IsTTY func() bool
}

// NewRunner builds a Runner with the production GitHub backend, the given Claude
// address function, and the prompt builder wired in. The CLI calls this with
// claude.Address and claude.BuildAddressPrompt so the import direction stays
// claude -> address.
func NewRunner(fn AddressFn, build PromptBuildFn) *Runner {
	return &Runner{
		Claude:      addressFnAdapter{fn: fn},
		GitHub:      defaultGitHubClient{},
		BuildPrompt: build,
		In:          os.Stdin,
		IsTTY:       workspace.IsStdinTTY,
	}
}

// Run is a package-level convenience that delegates to NewRunner(fn, build).Run.
func Run(w io.Writer, opts Options, fn AddressFn, build PromptBuildFn) error {
	return NewRunner(fn, build).Run(w, opts)
}

// ErrMaxIterations is returned when more threads are selected than
// --max-iterations allows; the run addresses the cap and reports the remainder.
var ErrMaxIterations = errors.New("reached max iterations before addressing every selected thread")

// Run executes the address pipeline:
//  1. Resolve the PR (clone once or --local).
//  2. Fetch and filter the review threads (drop resolved + the tool's own).
//  3. Select which to address (--thread / --all / no-TTY default / interactive).
//  4. Dispatch Claude per thread (or once for an aggregate commit), pushing the
//     follow-up commits and (gated) replying to and resolving each thread.
//  5. Post the aggregate report back onto the PR.
func (r *Runner) Run(w io.Writer, opts Options) error {
	r.applyDefaults(&opts)

	if opts.PrintPrompt && r.BuildPrompt == nil {
		return errors.New("--print-prompt requires a prompt builder; wire claude.BuildAddressPrompt via NewRunner")
	}
	if !opts.Local && opts.PRRef == "" {
		return errors.New("a PR reference is required (or use --local)")
	}

	// In --print-prompt mode the only stdout payload is the prompt itself, so
	// progress chatter is silenced.
	statusW := w
	if opts.PrintPrompt {
		statusW = io.Discard
	}

	pr, err := r.fetchPR(opts)
	if err != nil {
		return fmt.Errorf("fetching PR: %w", err)
	}
	// The same checkout is reused across every thread (address only appends
	// commits), so cleanup is deferred to the end of the run.
	defer pr.Cleanup()
	owner, repo, number := pr.Owner, pr.Repo, pr.Number
	fullName := fmt.Sprintf("%s/%s", owner, repo)
	slog.Info("address starting",
		"pr", fmt.Sprintf("%s#%d", fullName, number),
		"all", opts.All,
		"threads", len(opts.ThreadIDs),
		"include_resolved", opts.IncludeResolved,
		"reply", opts.Reply,
		"resolve", opts.Resolve,
		"one_commit_per_thread", opts.OneCommitPerThread,
		"dry_run", opts.DryRun,
		"print_prompt", opts.PrintPrompt,
		"local", opts.Local,
	)
	if opts.Local {
		slog.Info("working tree left on PR branch", "branch", pr.HeadBranch, "dir", pr.Dir)
	}

	threads, err := r.GitHub.FetchReviewThreads(owner, repo, number)
	if err != nil {
		return fmt.Errorf("fetching review threads: %w", err)
	}
	threads = github.FilterReviewThreads(threads, opts.IncludeResolved)
	if len(threads) == 0 {
		_, _ = fmt.Fprintf(w, "No unresolved review threads to address on %s#%d.\n", fullName, number)
		return nil
	}

	selected, err := r.selectThreads(statusW, opts, threads)
	if err != nil {
		return err
	}
	if len(selected) == 0 {
		_, _ = fmt.Fprintln(w, "No threads selected — nothing to address.")
		return nil
	}

	if opts.DryRun {
		printDryRun(w, fullName, number, opts, selected)
		return nil
	}

	pats := loadPatterns(opts, pr.Dir)
	sks := skills.Load(pr.Dir)

	if opts.PrintPrompt {
		unit := selected
		if opts.OneCommitPerThread {
			unit = selected[:1] // render the first thread's prompt
		}
		prompt := r.BuildPrompt(r.contextFor(opts, pr, unit, pats, sks))
		if _, err := io.WriteString(w, prompt); err != nil {
			return fmt.Errorf("writing prompt: %w", err)
		}
		if !strings.HasSuffix(prompt, "\n") {
			_, _ = fmt.Fprintln(w)
		}
		return nil
	}

	return r.dispatch(w, opts, pr, fullName, selected, pats, sks)
}

// dispatch drives the address work: aggregate (one session over all threads) or
// per-thread (one session per thread, bounded by MaxIterations). It collects
// the per-thread results, posts the aggregate report, and returns an escalation
// or max-iterations error when the run could not finish cleanly.
func (r *Runner) dispatch(w io.Writer, opts Options, pr *github.PR, fullName string, selected []github.ReviewThread, pats []patterns.Pattern, sks []skills.Skill) error {
	var addressed []report.AddressedThread
	var summaries []string
	var escalated Status
	var model string
	processed := 0

	if opts.OneCommitPerThread {
		for _, t := range selected {
			if processed >= opts.MaxIterations {
				break
			}
			processed++
			_, _ = fmt.Fprintf(w, "Addressing thread %s (%s)...\n", t.ID, threadLocation(t))
			result, err := r.addressUnit(w, opts, pr, []github.ReviewThread{t}, pats, sks)
			if err != nil {
				return err
			}
			model = result.Model
			addressed = append(addressed, result.Threads...)
			if s := strings.TrimSpace(result.Summary); s != "" {
				summaries = append(summaries, s)
			}
			if st := parseStatus(result.Status); st.ShouldEscalate() {
				escalated = st
				break
			}
		}
	} else {
		_, _ = fmt.Fprintf(w, "Addressing %d thread(s) as one aggregate commit...\n", len(selected))
		result, err := r.addressUnit(w, opts, pr, selected, pats, sks)
		if err != nil {
			return err
		}
		model = result.Model
		processed = len(selected)
		addressed = result.Threads
		if s := strings.TrimSpace(result.Summary); s != "" {
			summaries = append(summaries, s)
		}
		if st := parseStatus(result.Status); st.ShouldEscalate() {
			escalated = st
		}
	}

	// Record what each thread changed on the PR itself — posted before the
	// escalation/max-iterations checks so an escalated report still lands
	// where the human who must intervene will see it.
	r.postAddressComment(w, opts, pr.Owner, pr.Repo, pr.Number, addressed, summaries, model)

	if escalated.ShouldEscalate() {
		_, _ = fmt.Fprintf(w, "\nClaude reported %s — stopping the address run and escalating.\n", escalated)
		return fmt.Errorf("address escalated with status %s", escalated)
	}
	if processed < len(selected) {
		return fmt.Errorf("%w: addressed %d of %d selected thread(s) on %s#%d (raise --max-iterations)",
			ErrMaxIterations, processed, len(selected), fullName, pr.Number)
	}
	_, _ = fmt.Fprintf(w, "Addressed %d thread(s) on %s#%d.\n", len(addressed), fullName, pr.Number)
	return nil
}

// addressUnit runs one Claude address session, publishes the follow-up
// commit(s) it made, and (gated) replies to and resolves each addressed thread.
// The push is fatal on failure; replying and resolving are best-effort.
func (r *Runner) addressUnit(w io.Writer, opts Options, pr *github.PR, threads []github.ReviewThread, pats []patterns.Pattern, sks []skills.Skill) (*report.AddressResult, error) {
	result, err := r.Claude.Address(pr.Dir, r.contextFor(opts, pr, threads, pats, sks))
	if err != nil {
		return nil, fmt.Errorf("claude address: %w", err)
	}
	if result == nil {
		return nil, errors.New("address session returned no result")
	}
	if result.Summary != "" {
		_, _ = fmt.Fprintf(w, "\n%s\n", strings.TrimSpace(result.Summary))
	}

	// Only publish and reply when the session actually committed something.
	if anyAddressed(result.Threads) {
		if err := r.GitHub.PushHead(pr.Dir, pr.HeadBranch); err != nil {
			return nil, fmt.Errorf("pushing follow-up commits to %s: %w", pr.HeadBranch, err)
		}
		r.replyAndResolve(w, opts, result.Threads, result.Model)
	}
	return result, nil
}

// replyAndResolve posts a per-thread reply (under --reply) and marks the thread
// resolved (under --resolve) for every addressed thread. Both are best-effort,
// mirroring fix's postFixComment: a GitHub failure is logged and surfaced but
// never aborts the run — the follow-up commit is already pushed.
func (r *Runner) replyAndResolve(w io.Writer, opts Options, threads []report.AddressedThread, model string) {
	for _, t := range threads {
		if t.ThreadID == "" || !parseStatus(t.Status).addressed() {
			continue
		}
		if opts.Reply {
			if url, err := r.GitHub.AddReviewThreadReply(t.ThreadID, formatThreadReply(t, model)); err != nil {
				slog.Warn("posting thread reply failed", "thread", t.ThreadID, "err", err)
				_, _ = fmt.Fprintf(w, "Could not reply to thread %s: %v\n", t.ThreadID, err)
			} else {
				slog.Info("posted thread reply", "thread", t.ThreadID, "url", url)
			}
		}
		if opts.Resolve {
			if err := r.GitHub.ResolveReviewThread(t.ThreadID); err != nil {
				slog.Warn("resolving thread failed", "thread", t.ThreadID, "err", err)
				_, _ = fmt.Fprintf(w, "Could not resolve thread %s: %v\n", t.ThreadID, err)
			} else {
				_, _ = fmt.Fprintf(w, "Resolved thread %s.\n", t.ThreadID)
			}
		}
	}
}

// selectThreads resolves which threads to address: explicit --thread IDs, --all,
// a no-TTY default to every thread (with a logged note, never a silent partial
// run), the dry-run/print-prompt default to every thread, or the interactive
// selector.
func (r *Runner) selectThreads(w io.Writer, opts Options, threads []github.ReviewThread) ([]github.ReviewThread, error) {
	if len(opts.ThreadIDs) > 0 {
		return pickByID(w, threads, opts.ThreadIDs), nil
	}
	if opts.All || opts.DryRun || opts.PrintPrompt {
		return threads, nil
	}
	if r.IsTTY == nil || !r.IsTTY() {
		slog.Info("stdin is not a TTY; addressing every unresolved thread (pass --thread to target specific threads)",
			"threads", len(threads))
		return threads, nil
	}
	return RunInteractiveThreadSelection(w, r.In, threads)
}

// pickByID returns the threads whose ID is one of the requested IDs, preserving
// the input order. Unknown IDs are warned about so a typo does not silently
// address nothing.
func pickByID(w io.Writer, threads []github.ReviewThread, ids []string) []github.ReviewThread {
	want := make(map[string]bool, len(ids))
	for _, id := range ids {
		want[id] = true
	}
	var out []github.ReviewThread
	seen := make(map[string]bool, len(ids))
	for _, t := range threads {
		if want[t.ID] {
			out = append(out, t)
			seen[t.ID] = true
		}
	}
	for _, id := range ids {
		if !seen[id] {
			_, _ = fmt.Fprintf(w, "Warning: --thread %s did not match any unresolved review thread.\n", id)
		}
	}
	return out
}

// contextFor assembles the Claude prompt context for a unit of work.
func (r *Runner) contextFor(opts Options, pr *github.PR, threads []github.ReviewThread, pats []patterns.Pattern, sks []skills.Skill) Context {
	return Context{
		RepoFullName:       fmt.Sprintf("%s/%s", pr.Owner, pr.Repo),
		PRNumber:           pr.Number,
		PRTitle:            pr.Title,
		HeadBranch:         pr.HeadBranch,
		BaseBranch:         pr.BaseBranch,
		Threads:            threads,
		OneCommitPerThread: opts.OneCommitPerThread,
		Patterns:           pats,
		MaxPatterns:        opts.MaxPatterns,
		Skills:             sks,
		StyleGuidePath:     styleguide.Find(pr.Dir),
		Local:              opts.Local,
	}
}

// fetchPR resolves the PR for opts: a no-clone local checkout when opts.Local is
// set, otherwise a temp-dir clone+checkout.
func (r *Runner) fetchPR(opts Options) (*github.PR, error) {
	if opts.Local {
		return r.GitHub.FetchAndCheckoutLocal(opts.PRRef, github.LocalOptions{Force: opts.Force, Prompter: workspace.NewStdinPrompter()})
	}
	return r.GitHub.FetchAndCheckout(opts.PRRef)
}

// applyDefaults fills in zero-valued options and Runner seams with their
// defaults.
func (r *Runner) applyDefaults(opts *Options) {
	if opts.MaxIterations <= 0 {
		opts.MaxIterations = DefaultMaxIterations
	}
	if r.GitHub == nil {
		r.GitHub = defaultGitHubClient{}
	}
	if r.In == nil {
		r.In = os.Stdin
	}
	if r.IsTTY == nil {
		r.IsTTY = workspace.IsStdinTTY
	}
}

// anyAddressed reports whether any thread in the result was committed (DONE or
// DONE_WITH_CONCERNS), so the orchestrator can skip the push when the session
// changed nothing.
func anyAddressed(threads []report.AddressedThread) bool {
	for _, t := range threads {
		if parseStatus(t.Status).addressed() {
			return true
		}
	}
	return false
}

// loadPatterns runs technology detection on the checkout and loads the
// review-pattern catalog filtered by those tags. Mirrors fix.loadPatterns so
// the address change is grounded in the same pattern set the rest of the tool
// uses, plus any project-specific patterns under .planwerk/review_patterns/.
// Failures are non-fatal: the run falls back to no patterns.
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
	pats, err := patterns.LoadFilteredWithOptions(patterns.LoadOptions{Remote: opts.Remote, NoEmbedded: opts.NoLocalPatterns}, tags, dirs...)
	if err != nil {
		slog.Warn("loading review patterns failed; continuing without them", "err", err)
		return nil
	}
	if len(pats) > 0 {
		slog.Info("loaded review patterns", "count", len(pats))
	}
	return pats
}
