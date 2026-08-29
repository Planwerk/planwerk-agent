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

	"github.com/planwerk/planwerk-agent/internal/github"
	"github.com/planwerk/planwerk-agent/internal/github/githubtest"
	"github.com/planwerk/planwerk-agent/internal/report"
)

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

func newRunner(g *githubtest.Fake, c *fakeClaude) *Runner {
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
	gh := &githubtest.Fake{
		PR:               github.PR{HeadBranch: "feat/x", HeadSHA: "headsha"},
		CommitsInRangeFn: commitsByRange([]github.Commit{{SHA: "c1", Subject: "first"}}, []github.Commit{{SHA: "u1", Subject: "upstream one"}}),
		MergeBaseSHA:     "base000",
		RebaseStates:     []github.RebaseState{done()},
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
	if gh.Count("AddPRComment") != 1 {
		t.Errorf("AddPRComment called %d times, want 1", gh.Count("AddPRComment"))
	}
	if gh.Count("ForceWithLeasePush") != 0 {
		t.Errorf("ForceWithLeasePush called %d times, want 0 without --push", gh.Count("ForceWithLeasePush"))
	}
	if cl.lastAnalysis.Onto != "main" {
		t.Errorf("analysis Onto = %q, want default main", cl.lastAnalysis.Onto)
	}
	if !strings.Contains(buf.String(), "Rebase analysis") {
		t.Errorf("missing rendered analysis in output:\n%s", buf.String())
	}
}

func TestRun_ConflictResolveContinueLoop(t *testing.T) {
	gh := &githubtest.Fake{
		PR:               github.PR{HeadBranch: "feat/x", HeadSHA: "headsha"},
		CommitsInRangeFn: commitsByRange([]github.Commit{{SHA: "c1", Subject: "first"}}, nil),
		MergeBaseSHA:     "base000",
		RebaseStates:     []github.RebaseState{conflicted("c1", "first", "a.go"), conflicted("c2", "second", "b.go"), done()},
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
	if gh.Count("RebaseContinue") != 2 {
		t.Errorf("RebaseContinue called %d times, want 2", gh.Count("RebaseContinue"))
	}
	if gh.Count("RebaseAbort") != 0 {
		t.Errorf("RebaseAbort called %d times, want 0 on a successful resolve loop", gh.Count("RebaseAbort"))
	}
	// The last resolved conflict's context must carry the stopped commit.
	if cl.lastConflict.Commit.Subject != "second" {
		t.Errorf("last conflict subject = %q, want second", cl.lastConflict.Commit.Subject)
	}
}

func TestRun_MaxIterationsAborts(t *testing.T) {
	gh := &githubtest.Fake{
		PR:           github.PR{HeadBranch: "feat/x", HeadSHA: "headsha"},
		MergeBaseSHA: "base000",
		RebaseStates: []github.RebaseState{conflicted("deadbee", "stubborn commit", "a.go")},
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
	if gh.Count("RebaseAbort") != 1 {
		t.Errorf("RebaseAbort called %d times, want 1", gh.Count("RebaseAbort"))
	}
	if cl.analyzeCalls.Load() != 0 {
		t.Errorf("analysis must not run after an aborted rebase, got %d calls", cl.analyzeCalls.Load())
	}
}

func TestRun_DryRunPrintsPlanNoClaudeNoPush(t *testing.T) {
	gh := &githubtest.Fake{
		PR:               github.PR{HeadBranch: "feat/x", HeadSHA: "headsha"},
		CommitsInRangeFn: commitsByRange([]github.Commit{{SHA: "c1", Subject: "first"}, {SHA: "c2", Subject: "second"}}, nil),
		MergeBaseSHA:     "base000",
		RebaseStates:     []github.RebaseState{conflicted("c2", "second", "b.go")},
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
	if gh.Count("ForceWithLeasePush") != 0 {
		t.Errorf("dry-run must not push, got %d", gh.Count("ForceWithLeasePush"))
	}
	// The probe must be undone so --dry-run never leaves the tree rewritten.
	if gh.Count("ResetHard") != 1 {
		t.Errorf("ResetHard called %d times, want 1 (restore after probe)", gh.Count("ResetHard"))
	}
}

func TestRun_LocalSkipsClone(t *testing.T) {
	gh := &githubtest.Fake{
		PR:               github.PR{HeadBranch: "feat/x", BaseBranch: "main", HeadSHA: "headsha"},
		CommitsInRangeFn: commitsByRange([]github.Commit{{SHA: "c1", Subject: "first"}}, nil),
		Dir:              t.TempDir(),
		MergeBaseSHA:     "base000",
		RebaseStates:     []github.RebaseState{done()},
	}
	cl := &fakeClaude{}
	r := newRunner(gh, cl)

	opts := hermeticOpts("o/r#7")
	opts.Local = true
	if err := r.Run(io.Discard, opts); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if gh.Count("OpenLocalPR") != 1 {
		t.Errorf("OpenLocalPR calls = %d, want 1", gh.Count("OpenLocalPR"))
	}
	if gh.Count("FetchAndCheckout") != 0 {
		t.Errorf("FetchAndCheckout calls = %d, want 0 in local mode", gh.Count("FetchAndCheckout"))
	}
	// The local working tree must survive: Cleanup is a no-op when Local.
	if _, err := os.Stat(gh.Dir); err != nil {
		t.Fatalf("local checkout must survive the rebase: %v", err)
	}
}

func TestRun_ClonePath(t *testing.T) {
	gh := &githubtest.Fake{
		PR:           github.PR{HeadBranch: "feat/x", HeadSHA: "headsha"},
		MergeBaseSHA: "base000",
		RebaseStates: []github.RebaseState{done()},
	}
	cl := &fakeClaude{}
	r := newRunner(gh, cl)

	if err := r.Run(io.Discard, hermeticOpts("o/r#7")); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if gh.Count("FetchAndCheckout") != 1 {
		t.Errorf("FetchAndCheckout calls = %d, want 1", gh.Count("FetchAndCheckout"))
	}
	if gh.Count("OpenLocalPR") != 0 {
		t.Errorf("OpenLocalPR calls = %d, want 0", gh.Count("OpenLocalPR"))
	}
}

func TestRun_PushGatesForcePush(t *testing.T) {
	makeRunner := func() (*githubtest.Fake, *Runner) {
		gh := &githubtest.Fake{
			PR:           github.PR{HeadBranch: "feat/x", HeadSHA: "headsha"},
			MergeBaseSHA: "base000",
			RebaseStates: []github.RebaseState{done()},
		}
		return gh, newRunner(gh, &fakeClaude{})
	}

	t.Run("no push by default", func(t *testing.T) {
		gh, r := makeRunner()
		if err := r.Run(io.Discard, hermeticOpts("o/r#7")); err != nil {
			t.Fatalf("Run returned %v", err)
		}
		if gh.Count("ForceWithLeasePush") != 0 {
			t.Errorf("ForceWithLeasePush called %d times, want 0 without --push", gh.Count("ForceWithLeasePush"))
		}
	})

	t.Run("force-push only with --push", func(t *testing.T) {
		gh, r := makeRunner()
		opts := hermeticOpts("o/r#7")
		opts.Push = true
		if err := r.Run(io.Discard, opts); err != nil {
			t.Fatalf("Run returned %v", err)
		}
		if gh.Count("ForceWithLeasePush") != 1 {
			t.Errorf("ForceWithLeasePush called %d times, want 1 with --push", gh.Count("ForceWithLeasePush"))
		}
	})
}

func TestRun_ApplyAdjustmentsCallsApply(t *testing.T) {
	t.Run("report-only by default", func(t *testing.T) {
		gh := &githubtest.Fake{PR: github.PR{HeadBranch: "feat/x", HeadSHA: "h"}, MergeBaseSHA: "b", RebaseStates: []github.RebaseState{done()}}
		cl := &fakeClaude{}
		if err := newRunner(gh, cl).Run(io.Discard, hermeticOpts("o/r#7")); err != nil {
			t.Fatalf("Run returned %v", err)
		}
		if cl.applyCalls.Load() != 0 {
			t.Errorf("ApplyAdjustments called %d times, want 0 by default", cl.applyCalls.Load())
		}
	})

	t.Run("applies with --apply-adjustments", func(t *testing.T) {
		gh := &githubtest.Fake{PR: github.PR{HeadBranch: "feat/x", HeadSHA: "h"}, MergeBaseSHA: "b", RebaseStates: []github.RebaseState{done()}}
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
	gh := &githubtest.Fake{PR: github.PR{HeadBranch: "feat/x", HeadSHA: "h"}, MergeBaseSHA: "b", RebaseStates: []github.RebaseState{done()}}
	cl := &fakeClaude{}
	opts := hermeticOpts("o/r#7")
	opts.NoAnalysis = true
	if err := newRunner(gh, cl).Run(io.Discard, opts); err != nil {
		t.Fatalf("Run returned %v", err)
	}
	if cl.analyzeCalls.Load() != 0 {
		t.Errorf("AnalyzeRebasedCommits called %d times, want 0 with --no-analysis", cl.analyzeCalls.Load())
	}
	if gh.Count("AddPRComment") != 0 {
		t.Errorf("AddPRComment called %d times, want 0 with --no-analysis", gh.Count("AddPRComment"))
	}
}

func TestRun_NoAnalysisCommentSkipsComment(t *testing.T) {
	gh := &githubtest.Fake{PR: github.PR{HeadBranch: "feat/x", HeadSHA: "h"}, MergeBaseSHA: "b", RebaseStates: []github.RebaseState{done()}}
	cl := &fakeClaude{}
	opts := hermeticOpts("o/r#7")
	opts.NoAnalysisComment = true
	if err := newRunner(gh, cl).Run(io.Discard, opts); err != nil {
		t.Fatalf("Run returned %v", err)
	}
	if cl.analyzeCalls.Load() != 1 {
		t.Errorf("analysis must still run, got %d calls", cl.analyzeCalls.Load())
	}
	if gh.Count("AddPRComment") != 0 {
		t.Errorf("AddPRComment called %d times, want 0 with --no-analysis-comment", gh.Count("AddPRComment"))
	}
}

func TestRun_RequiresRefWithoutLocal(t *testing.T) {
	r := newRunner(&githubtest.Fake{}, &fakeClaude{})
	err := r.Run(io.Discard, Options{})
	if err == nil || !strings.Contains(err.Error(), "PR reference is required") {
		t.Fatalf("expected a missing-ref error, got %v", err)
	}
}

func TestRun_PrintPromptWritesPromptSkipsClaude(t *testing.T) {
	gh := &githubtest.Fake{
		PR:               github.PR{HeadBranch: "feat/x", HeadSHA: "headsha"},
		CommitsInRangeFn: commitsByRange([]github.Commit{{SHA: "c1", Subject: "first"}}, []github.Commit{{SHA: "u1", Subject: "upstream one"}}),
		MergeBaseSHA:     "base000",
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
	if gh.Count("StartRebase") != 0 {
		t.Errorf("print-prompt must not perform the rebase, got %d StartRebase calls", gh.Count("StartRebase"))
	}
	if !strings.HasSuffix(out, "\n") {
		t.Errorf("prompt output should end with a newline, got: %q", out)
	}
}

func TestRun_PrintPromptWithoutBuilderErrors(t *testing.T) {
	r := newRunner(&githubtest.Fake{PR: github.PR{HeadSHA: "h"}, RebaseStates: []github.RebaseState{done()}}, &fakeClaude{})
	opts := hermeticOpts("o/r#1")
	opts.PrintPrompt = true
	err := r.Run(io.Discard, opts)
	if err == nil || !strings.Contains(err.Error(), "prompt builder") {
		t.Fatalf("expected prompt-builder error, got %v", err)
	}
}

func TestRun_AnalysisCommentFailureIsNonFatal(t *testing.T) {
	gh := &githubtest.Fake{
		PR:           github.PR{HeadBranch: "feat/x", HeadSHA: "h"},
		MergeBaseSHA: "b",
		RebaseStates: []github.RebaseState{done()},
		CommentErr:   errors.New("github down"),
	}
	cl := &fakeClaude{}
	var buf bytes.Buffer
	if err := newRunner(gh, cl).Run(&buf, hermeticOpts("o/r#7")); err != nil {
		t.Fatalf("Run returned %v, want nil despite the comment failure", err)
	}
	if !strings.Contains(buf.String(), "Could not post the rebase analysis") {
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

	gh := &githubtest.Fake{
		PR:           github.PR{HeadBranch: "feat/x", HeadSHA: "h"},
		MergeBaseSHA: "b",
		Dir:          t.TempDir(),
		RebaseStates: []github.RebaseState{conflicted("c1", "first", "a.go"), done()},
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
	gh := &githubtest.Fake{PR: github.PR{HeadBranch: "feat/x", HeadSHA: "h"}, Dir: t.TempDir()}
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

// commitsByRange scripts CommitsInRange by range shape: any "..HEAD" range
// yields head (the replay set, and the rebased set after a rebase); anything
// else yields upstream.
func commitsByRange(head, upstream []github.Commit) func(dir, rangeExpr string) ([]github.Commit, error) {
	return func(_, rangeExpr string) ([]github.Commit, error) {
		if strings.HasSuffix(rangeExpr, "..HEAD") {
			return head, nil
		}
		return upstream, nil
	}
}
