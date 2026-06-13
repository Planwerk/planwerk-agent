package rebase

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/planwerk/planwerk-review/internal/github"
	"github.com/planwerk/planwerk-review/internal/report"
)

// fakeGit is a scripted GitClient. CommitsInRange answers by range shape: any
// "..HEAD" range yields headCommits (the replay set, and the rebased set after
// a rebase); anything else yields upstreamCommits. The rebase step states are
// consumed in order — StartRebase takes the first, each RebaseContinue the
// next — clamping to the last so a single Conflicted state repeats forever.
type fakeGit struct {
	prTitle      string
	prBranch     string
	prBaseBranch string
	prHeadSHA    string
	cloneDir     string

	cloneCalls       atomic.Int32
	localCalls       atomic.Int32
	fetchBranchCalls atomic.Int32

	mergeBase       string
	headCommits     []github.Commit
	upstreamCommits []github.Commit

	rebaseStates     []github.RebaseState
	rebaseIdx        atomic.Int32
	startRebaseCalls atomic.Int32
	continueCalls    atomic.Int32
	abortCalls       atomic.Int32
	resetCalls       atomic.Int32

	pushCalls atomic.Int32
	pushErr   error

	commentCalls  atomic.Int32
	commentBodies []string
	commentErr    error
}

func (f *fakeGit) FetchAndCheckout(ref string) (*github.PR, error) {
	f.cloneCalls.Add(1)
	return f.makePR(ref, false)
}

func (f *fakeGit) FetchAndCheckoutLocal(ref string, _ github.LocalOptions) (*github.PR, error) {
	f.localCalls.Add(1)
	return f.makePR(ref, true)
}

func (f *fakeGit) makePR(ref string, local bool) (*github.PR, error) {
	owner, repo, number, err := github.ParseRef(ref)
	if err != nil {
		return nil, err
	}
	return &github.PR{
		Owner:      owner,
		Repo:       repo,
		Number:     number,
		Title:      f.prTitle,
		HeadBranch: f.prBranch,
		BaseBranch: f.prBaseBranch,
		HeadSHA:    f.prHeadSHA,
		Dir:        f.cloneDir,
		Local:      local,
	}, nil
}

func (f *fakeGit) FetchBranch(_, _ string) error {
	f.fetchBranchCalls.Add(1)
	return nil
}

func (f *fakeGit) MergeBase(_, _, _ string) (string, error) {
	return f.mergeBase, nil
}

func (f *fakeGit) CommitsInRange(_, rangeExpr string) ([]github.Commit, error) {
	if strings.HasSuffix(rangeExpr, "..HEAD") {
		return f.headCommits, nil
	}
	return f.upstreamCommits, nil
}

func (f *fakeGit) StartRebase(_, _ string) (github.RebaseState, error) {
	f.startRebaseCalls.Add(1)
	return f.nextRebaseState(), nil
}

func (f *fakeGit) RebaseContinue(_ string) (github.RebaseState, error) {
	f.continueCalls.Add(1)
	return f.nextRebaseState(), nil
}

func (f *fakeGit) nextRebaseState() github.RebaseState {
	if len(f.rebaseStates) == 0 {
		return github.RebaseState{Done: true}
	}
	i := int(f.rebaseIdx.Add(1)) - 1
	if i >= len(f.rebaseStates) {
		i = len(f.rebaseStates) - 1
	}
	return f.rebaseStates[i]
}

func (f *fakeGit) RebaseAbort(_ string) error {
	f.abortCalls.Add(1)
	return nil
}

func (f *fakeGit) ResetHard(_, _ string) error {
	f.resetCalls.Add(1)
	return nil
}

func (f *fakeGit) ForceWithLeasePush(_, _ string) error {
	f.pushCalls.Add(1)
	return f.pushErr
}

func (f *fakeGit) AddPRComment(owner, repo string, number int, body string) (string, error) {
	f.commentCalls.Add(1)
	if f.commentErr != nil {
		return "", f.commentErr
	}
	f.commentBodies = append(f.commentBodies, body)
	return fmt.Sprintf("https://github.com/%s/%s/pull/%d#issuecomment-1", owner, repo, number), nil
}

type fakeClaude struct {
	resolveCalls atomic.Int32
	analyzeCalls atomic.Int32
	applyCalls   atomic.Int32

	resolveErr error
	analyzeErr error
	applyErr   error

	analysis *report.RebaseAnalysis

	lastConflict ConflictContext
	lastAnalysis AnalysisContext
	lastApply    ApplyContext
}

func (f *fakeClaude) ResolveConflict(_ string, ctx ConflictContext) (string, error) {
	f.resolveCalls.Add(1)
	f.lastConflict = ctx
	return "resolved", f.resolveErr
}

func (f *fakeClaude) AnalyzeRebasedCommits(_ string, ctx AnalysisContext) (*report.RebaseAnalysis, error) {
	f.analyzeCalls.Add(1)
	f.lastAnalysis = ctx
	if f.analyzeErr != nil {
		return nil, f.analyzeErr
	}
	if f.analysis != nil {
		return f.analysis, nil
	}
	return &report.RebaseAnalysis{Summary: "all clear"}, nil
}

func (f *fakeClaude) ApplyAdjustments(_ string, ctx ApplyContext) (string, error) {
	f.applyCalls.Add(1)
	f.lastApply = ctx
	return "applied", f.applyErr
}

func newRunner(g *fakeGit, c *fakeClaude) *Runner {
	return &Runner{Claude: c, GitHub: g}
}

func conflicted(sha, subject string, files ...string) github.RebaseState {
	return github.RebaseState{Conflicted: true, StoppedSHA: sha, StoppedSubject: subject, ConflictedFiles: files}
}

func done() github.RebaseState { return github.RebaseState{Done: true} }

// hermeticOpts disables pattern loading so Run does not touch the embedded
// catalog or the filesystem during these fake-driven tests.
func hermeticOpts(ref string) Options {
	return Options{PRRef: ref, NoLocalPatterns: true, NoRepoPatterns: true}
}

func TestRun_CleanRebaseThenAnalysis(t *testing.T) {
	gh := &fakeGit{
		prBranch:        "feat/x",
		prHeadSHA:       "headsha",
		mergeBase:       "base000",
		headCommits:     []github.Commit{{SHA: "c1", Subject: "first"}},
		upstreamCommits: []github.Commit{{SHA: "u1", Subject: "upstream one"}},
		rebaseStates:    []github.RebaseState{done()},
	}
	cl := &fakeClaude{}
	r := newRunner(gh, cl)

	var buf bytes.Buffer
	if err := r.Run(&buf, hermeticOpts("o/r#7")); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if cl.resolveCalls.Load() != 0 {
		t.Errorf("ResolveConflict called %d times, want 0 (clean rebase)", cl.resolveCalls.Load())
	}
	if cl.analyzeCalls.Load() != 1 {
		t.Errorf("AnalyzeRebasedCommits called %d times, want 1", cl.analyzeCalls.Load())
	}
	if gh.commentCalls.Load() != 1 {
		t.Errorf("AddPRComment called %d times, want 1", gh.commentCalls.Load())
	}
	if gh.pushCalls.Load() != 0 {
		t.Errorf("ForceWithLeasePush called %d times, want 0 without --push", gh.pushCalls.Load())
	}
	if cl.lastAnalysis.Onto != "main" {
		t.Errorf("analysis Onto = %q, want default main", cl.lastAnalysis.Onto)
	}
	if !strings.Contains(buf.String(), "Rebase analysis") {
		t.Errorf("missing rendered analysis in output:\n%s", buf.String())
	}
}

func TestRun_ConflictResolveContinueLoop(t *testing.T) {
	gh := &fakeGit{
		prBranch:     "feat/x",
		prHeadSHA:    "headsha",
		mergeBase:    "base000",
		headCommits:  []github.Commit{{SHA: "c1", Subject: "first"}},
		rebaseStates: []github.RebaseState{conflicted("c1", "first", "a.go"), conflicted("c2", "second", "b.go"), done()},
	}
	cl := &fakeClaude{}
	r := newRunner(gh, cl)

	var buf bytes.Buffer
	if err := r.Run(&buf, hermeticOpts("o/r#7")); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if cl.resolveCalls.Load() != 2 {
		t.Errorf("ResolveConflict called %d times, want 2", cl.resolveCalls.Load())
	}
	if gh.continueCalls.Load() != 2 {
		t.Errorf("RebaseContinue called %d times, want 2", gh.continueCalls.Load())
	}
	if gh.abortCalls.Load() != 0 {
		t.Errorf("RebaseAbort called %d times, want 0 on a successful resolve loop", gh.abortCalls.Load())
	}
	// The last resolved conflict's context must carry the stopped commit.
	if cl.lastConflict.Commit.Subject != "second" {
		t.Errorf("last conflict subject = %q, want second", cl.lastConflict.Commit.Subject)
	}
}

func TestRun_MaxIterationsAborts(t *testing.T) {
	gh := &fakeGit{
		prBranch:     "feat/x",
		prHeadSHA:    "headsha",
		mergeBase:    "base000",
		rebaseStates: []github.RebaseState{conflicted("deadbee", "stubborn commit", "a.go")},
	}
	cl := &fakeClaude{}
	r := newRunner(gh, cl)

	opts := hermeticOpts("o/r#7")
	opts.MaxIterations = 2
	err := r.Run(io.Discard, opts)
	if !errors.Is(err, ErrMaxIterations) {
		t.Fatalf("Run err = %v, want ErrMaxIterations", err)
	}
	if !strings.Contains(err.Error(), "stubborn commit") {
		t.Errorf("error must name the conflicting commit, got: %v", err)
	}
	if cl.resolveCalls.Load() != 2 {
		t.Errorf("ResolveConflict called %d times, want 2 (the cap)", cl.resolveCalls.Load())
	}
	if gh.abortCalls.Load() != 1 {
		t.Errorf("RebaseAbort called %d times, want 1", gh.abortCalls.Load())
	}
	if cl.analyzeCalls.Load() != 0 {
		t.Errorf("analysis must not run after an aborted rebase, got %d calls", cl.analyzeCalls.Load())
	}
}

func TestRun_DryRunPrintsPlanNoClaudeNoPush(t *testing.T) {
	gh := &fakeGit{
		prBranch:     "feat/x",
		prHeadSHA:    "headsha",
		mergeBase:    "base000",
		headCommits:  []github.Commit{{SHA: "c1", Subject: "first"}, {SHA: "c2", Subject: "second"}},
		rebaseStates: []github.RebaseState{conflicted("c2", "second", "b.go")},
	}
	cl := &fakeClaude{}
	r := newRunner(gh, cl)

	opts := hermeticOpts("o/r#7")
	opts.DryRun = true
	var buf bytes.Buffer
	if err := r.Run(&buf, opts); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	out := buf.String()
	if !strings.Contains(out, "Rebase plan: replay 2 commit(s)") {
		t.Errorf("missing rebase plan in dry-run output:\n%s", out)
	}
	if !strings.Contains(out, "First conflicting commit: c2") {
		t.Errorf("missing conflicting-commit line in dry-run output:\n%s", out)
	}
	if cl.resolveCalls.Load() != 0 || cl.analyzeCalls.Load() != 0 || cl.applyCalls.Load() != 0 {
		t.Errorf("dry-run must not invoke Claude")
	}
	if gh.pushCalls.Load() != 0 {
		t.Errorf("dry-run must not push, got %d", gh.pushCalls.Load())
	}
	// The probe must be undone so --dry-run never leaves the tree rewritten.
	if gh.resetCalls.Load() != 1 {
		t.Errorf("ResetHard called %d times, want 1 (restore after probe)", gh.resetCalls.Load())
	}
}

func TestRun_LocalSkipsClone(t *testing.T) {
	gh := &fakeGit{
		prBranch:     "feat/x",
		prBaseBranch: "main",
		prHeadSHA:    "headsha",
		cloneDir:     t.TempDir(),
		mergeBase:    "base000",
		headCommits:  []github.Commit{{SHA: "c1", Subject: "first"}},
		rebaseStates: []github.RebaseState{done()},
	}
	cl := &fakeClaude{}
	r := newRunner(gh, cl)

	opts := hermeticOpts("o/r#7")
	opts.Local = true
	if err := r.Run(io.Discard, opts); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if gh.localCalls.Load() != 1 {
		t.Errorf("FetchAndCheckoutLocal calls = %d, want 1", gh.localCalls.Load())
	}
	if gh.cloneCalls.Load() != 0 {
		t.Errorf("FetchAndCheckout calls = %d, want 0 in local mode", gh.cloneCalls.Load())
	}
	// The local working tree must survive: Cleanup is a no-op when Local.
	if _, err := os.Stat(gh.cloneDir); err != nil {
		t.Fatalf("local checkout must survive the rebase: %v", err)
	}
}

func TestRun_ClonePath(t *testing.T) {
	gh := &fakeGit{
		prBranch:     "feat/x",
		prHeadSHA:    "headsha",
		mergeBase:    "base000",
		rebaseStates: []github.RebaseState{done()},
	}
	cl := &fakeClaude{}
	r := newRunner(gh, cl)

	if err := r.Run(io.Discard, hermeticOpts("o/r#7")); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if gh.cloneCalls.Load() != 1 {
		t.Errorf("FetchAndCheckout calls = %d, want 1", gh.cloneCalls.Load())
	}
	if gh.localCalls.Load() != 0 {
		t.Errorf("FetchAndCheckoutLocal calls = %d, want 0", gh.localCalls.Load())
	}
}

func TestRun_PushGatesForcePush(t *testing.T) {
	makeRunner := func() (*fakeGit, *Runner) {
		gh := &fakeGit{
			prBranch:     "feat/x",
			prHeadSHA:    "headsha",
			mergeBase:    "base000",
			rebaseStates: []github.RebaseState{done()},
		}
		return gh, newRunner(gh, &fakeClaude{})
	}

	t.Run("no push by default", func(t *testing.T) {
		gh, r := makeRunner()
		if err := r.Run(io.Discard, hermeticOpts("o/r#7")); err != nil {
			t.Fatalf("Run returned %v", err)
		}
		if gh.pushCalls.Load() != 0 {
			t.Errorf("ForceWithLeasePush called %d times, want 0 without --push", gh.pushCalls.Load())
		}
	})

	t.Run("force-push only with --push", func(t *testing.T) {
		gh, r := makeRunner()
		opts := hermeticOpts("o/r#7")
		opts.Push = true
		if err := r.Run(io.Discard, opts); err != nil {
			t.Fatalf("Run returned %v", err)
		}
		if gh.pushCalls.Load() != 1 {
			t.Errorf("ForceWithLeasePush called %d times, want 1 with --push", gh.pushCalls.Load())
		}
	})
}

func TestRun_ApplyAdjustmentsCallsApply(t *testing.T) {
	t.Run("report-only by default", func(t *testing.T) {
		gh := &fakeGit{prBranch: "feat/x", prHeadSHA: "h", mergeBase: "b", rebaseStates: []github.RebaseState{done()}}
		cl := &fakeClaude{}
		if err := newRunner(gh, cl).Run(io.Discard, hermeticOpts("o/r#7")); err != nil {
			t.Fatalf("Run returned %v", err)
		}
		if cl.applyCalls.Load() != 0 {
			t.Errorf("ApplyAdjustments called %d times, want 0 by default", cl.applyCalls.Load())
		}
	})

	t.Run("applies with --apply-adjustments", func(t *testing.T) {
		gh := &fakeGit{prBranch: "feat/x", prHeadSHA: "h", mergeBase: "b", rebaseStates: []github.RebaseState{done()}}
		cl := &fakeClaude{}
		opts := hermeticOpts("o/r#7")
		opts.ApplyAdjustments = true
		if err := newRunner(gh, cl).Run(io.Discard, opts); err != nil {
			t.Fatalf("Run returned %v", err)
		}
		if cl.applyCalls.Load() != 1 {
			t.Errorf("ApplyAdjustments called %d times, want 1", cl.applyCalls.Load())
		}
	})
}

func TestRun_NoAnalysisSkipsAnalysis(t *testing.T) {
	gh := &fakeGit{prBranch: "feat/x", prHeadSHA: "h", mergeBase: "b", rebaseStates: []github.RebaseState{done()}}
	cl := &fakeClaude{}
	opts := hermeticOpts("o/r#7")
	opts.NoAnalysis = true
	if err := newRunner(gh, cl).Run(io.Discard, opts); err != nil {
		t.Fatalf("Run returned %v", err)
	}
	if cl.analyzeCalls.Load() != 0 {
		t.Errorf("AnalyzeRebasedCommits called %d times, want 0 with --no-analysis", cl.analyzeCalls.Load())
	}
	if gh.commentCalls.Load() != 0 {
		t.Errorf("AddPRComment called %d times, want 0 with --no-analysis", gh.commentCalls.Load())
	}
}

func TestRun_NoAnalysisCommentSkipsComment(t *testing.T) {
	gh := &fakeGit{prBranch: "feat/x", prHeadSHA: "h", mergeBase: "b", rebaseStates: []github.RebaseState{done()}}
	cl := &fakeClaude{}
	opts := hermeticOpts("o/r#7")
	opts.NoAnalysisComment = true
	if err := newRunner(gh, cl).Run(io.Discard, opts); err != nil {
		t.Fatalf("Run returned %v", err)
	}
	if cl.analyzeCalls.Load() != 1 {
		t.Errorf("analysis must still run, got %d calls", cl.analyzeCalls.Load())
	}
	if gh.commentCalls.Load() != 0 {
		t.Errorf("AddPRComment called %d times, want 0 with --no-analysis-comment", gh.commentCalls.Load())
	}
}

func TestRun_RequiresRefWithoutLocal(t *testing.T) {
	r := newRunner(&fakeGit{}, &fakeClaude{})
	err := r.Run(io.Discard, Options{})
	if err == nil || !strings.Contains(err.Error(), "PR reference is required") {
		t.Fatalf("expected a missing-ref error, got %v", err)
	}
}

func TestRun_PrintPromptWritesPromptSkipsClaude(t *testing.T) {
	gh := &fakeGit{
		prBranch:        "feat/x",
		prHeadSHA:       "headsha",
		mergeBase:       "base000",
		headCommits:     []github.Commit{{SHA: "c1", Subject: "first"}},
		upstreamCommits: []github.Commit{{SHA: "u1", Subject: "upstream one"}},
	}
	cl := &fakeClaude{}
	r := newRunner(gh, cl)
	r.AnalysisPrompt = func(ctx AnalysisContext) string {
		return fmt.Sprintf("PROMPT pr=%s#%d onto=%s rebased=%d upstream=%d",
			ctx.RepoFullName, ctx.PRNumber, ctx.Onto, len(ctx.RebasedCommits), len(ctx.UpstreamCommits))
	}

	opts := hermeticOpts("owner/repo#7")
	opts.PrintPrompt = true
	var buf bytes.Buffer
	if err := r.Run(&buf, opts); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	out := buf.String()
	if !strings.Contains(out, "PROMPT pr=owner/repo#7 onto=main rebased=1 upstream=1") {
		t.Errorf("expected rendered analysis prompt, got: %q", out)
	}
	if cl.analyzeCalls.Load() != 0 || cl.resolveCalls.Load() != 0 {
		t.Errorf("print-prompt must not invoke Claude")
	}
	if gh.startRebaseCalls.Load() != 0 {
		t.Errorf("print-prompt must not perform the rebase, got %d StartRebase calls", gh.startRebaseCalls.Load())
	}
	if !strings.HasSuffix(out, "\n") {
		t.Errorf("prompt output should end with a newline, got: %q", out)
	}
}

func TestRun_PrintPromptWithoutBuilderErrors(t *testing.T) {
	r := newRunner(&fakeGit{prHeadSHA: "h", rebaseStates: []github.RebaseState{done()}}, &fakeClaude{})
	opts := hermeticOpts("o/r#1")
	opts.PrintPrompt = true
	err := r.Run(io.Discard, opts)
	if err == nil || !strings.Contains(err.Error(), "prompt builder") {
		t.Fatalf("expected prompt-builder error, got %v", err)
	}
}

func TestRun_AnalysisCommentFailureIsNonFatal(t *testing.T) {
	gh := &fakeGit{
		prBranch:     "feat/x",
		prHeadSHA:    "h",
		mergeBase:    "b",
		rebaseStates: []github.RebaseState{done()},
		commentErr:   errors.New("github down"),
	}
	cl := &fakeClaude{}
	var buf bytes.Buffer
	if err := newRunner(gh, cl).Run(&buf, hermeticOpts("o/r#7")); err != nil {
		t.Fatalf("Run returned %v, want nil despite the comment failure", err)
	}
	if !strings.Contains(buf.String(), "Could not post the analysis") {
		t.Errorf("expected a non-fatal warning about the failed comment post, got:\n%s", buf.String())
	}
}

func TestRun_PassesPatternsToClaude(t *testing.T) {
	patternsDir := t.TempDir()
	const patternBody = `# Review Pattern: Sample wiring check
**Review-Area**: meta
**Detection-Hint**: anything
**Severity**: WARNING

## Rule
Wired patterns must reach the rebase contexts.
`
	if err := os.WriteFile(patternsDir+"/sample.md", []byte(patternBody), 0o644); err != nil {
		t.Fatalf("seeding pattern file: %v", err)
	}

	gh := &fakeGit{
		prBranch:     "feat/x",
		prHeadSHA:    "h",
		mergeBase:    "b",
		cloneDir:     t.TempDir(),
		rebaseStates: []github.RebaseState{conflicted("c1", "first", "a.go"), done()},
	}
	cl := &fakeClaude{}
	opts := Options{
		PRRef:           "owner/repo#7",
		PatternDirs:     []string{patternsDir},
		NoLocalPatterns: true,
		NoRepoPatterns:  true,
	}
	if err := newRunner(gh, cl).Run(io.Discard, opts); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if got := len(cl.lastConflict.Patterns); got != 1 {
		t.Fatalf("conflict ctx got %d patterns, want 1 (wiring broken)", got)
	}
	if cl.lastConflict.Patterns[0].Name != "Sample wiring check" {
		t.Errorf("conflict ctx pattern = %q, want %q", cl.lastConflict.Patterns[0].Name, "Sample wiring check")
	}
	if got := len(cl.lastAnalysis.Patterns); got != 1 {
		t.Errorf("analysis ctx got %d patterns, want 1", got)
	}
}

// barePromptRunner returns a Runner whose git fake clones into a throwaway dir
// so PrintBarePrompt can run detect + pattern loading without the network.
func barePromptRunner(t *testing.T) *Runner {
	t.Helper()
	gh := &fakeGit{prBranch: "feat/x", prHeadSHA: "h", cloneDir: t.TempDir()}
	return newRunner(gh, &fakeClaude{})
}

func TestPrintBarePrompt_WritesPromptForRef(t *testing.T) {
	r := barePromptRunner(t)
	build := func(ctx BareContext) string {
		return fmt.Sprintf("BARE repo=%s pr=%d onto=%s", ctx.RepoFullName, ctx.PRNumber, ctx.Onto)
	}
	var buf bytes.Buffer
	if err := r.PrintBarePrompt(&buf, Options{PRRef: "https://github.com/owner/repo/pull/42"}, build); err != nil {
		t.Fatalf("PrintBarePrompt returned %v, want nil", err)
	}
	if !strings.HasPrefix(buf.String(), "BARE repo=owner/repo pr=42 onto=main") {
		t.Errorf("expected rendered bare prompt with parsed ref + default onto, got: %q", buf.String())
	}
}

func TestPrintBarePrompt_RejectsBadRef(t *testing.T) {
	r := barePromptRunner(t)
	err := r.PrintBarePrompt(io.Discard, Options{PRRef: "not-a-ref"}, func(BareContext) string { return "" })
	if err == nil || !strings.Contains(err.Error(), "parsing PR ref") {
		t.Fatalf("expected parsing error, got %v", err)
	}
}

func TestPrintBarePrompt_RequiresBuilder(t *testing.T) {
	r := barePromptRunner(t)
	err := r.PrintBarePrompt(io.Discard, Options{PRRef: "owner/repo#1"}, nil)
	if err == nil || !strings.Contains(err.Error(), "prompt builder") {
		t.Fatalf("expected builder-required error, got %v", err)
	}
}

func TestPrintBarePrompt_SurfacesPatterns(t *testing.T) {
	patternsDir := t.TempDir()
	const patternBody = `# Review Pattern: Bare wiring check
**Review-Area**: meta
**Detection-Hint**: anything
**Severity**: WARNING

## Rule
Bare prompts must surface patterns through BareContext.
`
	if err := os.WriteFile(patternsDir+"/sample.md", []byte(patternBody), 0o644); err != nil {
		t.Fatalf("seeding pattern file: %v", err)
	}

	r := barePromptRunner(t)
	var got BareContext
	build := func(ctx BareContext) string {
		got = ctx
		return "ok"
	}
	opts := Options{
		PRRef:           "owner/repo#7",
		PatternDirs:     []string{patternsDir},
		NoLocalPatterns: true,
		NoRepoPatterns:  true,
	}
	if err := r.PrintBarePrompt(io.Discard, opts, build); err != nil {
		t.Fatalf("PrintBarePrompt returned %v, want nil", err)
	}
	if len(got.PatternCatalog) != 1 || got.PatternCatalog[0].Name != "Bare wiring check" {
		t.Errorf("BareContext catalog = %+v, want one entry named %q", got.PatternCatalog, "Bare wiring check")
	}
}
