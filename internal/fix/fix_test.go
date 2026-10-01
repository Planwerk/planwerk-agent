package fix

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/planwerk/planwerk-agent/internal/github"
	"github.com/planwerk/planwerk-agent/internal/github/githubtest"
	"github.com/planwerk/planwerk-agent/internal/patterns"
	"github.com/planwerk/planwerk-agent/internal/patterns/patternstest"
)

type fakeClaude struct {
	called atomic.Int32
	report string
	model  string
	err    error
	ctx    Context
	// ctxs holds the Context of every Fix call in order; ctx is the last.
	ctxs []Context
	// onFix, when set, runs inside Fix after the call is recorded, so a test
	// can observe on-disk state while the session "runs".
	onFix func(Context)
}

func (f *fakeClaude) Fix(_ string, ctx Context) (string, string, error) {
	f.called.Add(1)
	f.ctx = ctx
	f.ctxs = append(f.ctxs, ctx)
	if f.onFix != nil {
		f.onFix(ctx)
	}
	return f.report, f.model, f.err
}

type fakePrompter struct {
	answers []bool
	idx     atomic.Int32
	asked   []string
}

func (p *fakePrompter) Confirm(message string) (bool, error) {
	p.asked = append(p.asked, message)
	i := int(p.idx.Add(1)) - 1
	if i >= len(p.answers) {
		return false, nil
	}
	return p.answers[i], nil
}

func passing(name string) github.CheckRun {
	return github.CheckRun{ID: 1, Name: name, Status: "completed", Conclusion: "success"}
}

func failing(id int64, name string) github.CheckRun {
	return github.CheckRun{ID: id, Name: name, Status: "completed", Conclusion: "failure",
		HTMLURL: "https://example.com/" + name, WorkflowRunID: 99}
}

func newRunner(gh *githubtest.Fake, cl *fakeClaude, pr *fakePrompter) *Runner {
	return &Runner{
		Claude:   cl,
		GitHub:   gh,
		Prompter: pr,
		Sleep:    func(time.Duration) {}, // tests must not sleep
	}
}

func TestRun_AllChecksAlreadyPassing(t *testing.T) {
	gh := &githubtest.Fake{
		PR:       github.PR{Title: "demo", HeadBranch: "feat/x", HeadSHA: "abc1234"},
		Checks:   [][]github.CheckRun{{passing("lint"), passing("test")}},
		HeadSHAs: []string{"abc1234"},
	}
	cl := &fakeClaude{}
	r := newRunner(gh, cl, &fakePrompter{})

	var buf bytes.Buffer
	if err := r.Run(&buf, Options{PRRef: "owner/repo#1"}); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if cl.called.Load() != 0 {
		t.Errorf("Claude.Fix called %d times, want 0 (no failures)", cl.called.Load())
	}
	if !strings.Contains(buf.String(), "All 2 checks passed") {
		t.Errorf("missing success banner: %s", buf.String())
	}
}

func TestRun_FixesFailureInOneIteration(t *testing.T) {
	gh := &githubtest.Fake{
		PR: github.PR{Title: "demo", HeadBranch: "feat/x", HeadSHA: "old"},
		// Iteration 1 sees a failure; iteration 2 (post-push) sees green.
		Checks: [][]github.CheckRun{
			{failing(1, "test"), passing("lint")},
			{passing("test"), passing("lint")},
		},
		HeadSHAs: []string{"new"},
		Logs:     "FAIL: TestX\n",
	}
	cl := &fakeClaude{report: "fixed TestX"}
	r := newRunner(gh, cl, &fakePrompter{})

	var buf bytes.Buffer
	if err := r.Run(&buf, Options{PRRef: "o/r#7"}); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if cl.called.Load() != 1 {
		t.Errorf("Claude.Fix called %d times, want 1", cl.called.Load())
	}
	out := buf.String()
	if !strings.Contains(out, "1 failed check(s)") {
		t.Errorf("missing failure banner: %s", out)
	}
	if !strings.Contains(out, "Claude fix report") || !strings.Contains(out, "fixed TestX") {
		t.Errorf("missing claude report passthrough: %s", out)
	}
	if !strings.Contains(out, "All 2 checks passed") {
		t.Errorf("missing final success banner: %s", out)
	}
}

// fixReport is a realistic orchestrator-driven report, carrying the mandated
// "## Fix Report (iteration N)" heading the PR comment keeps as its title.
const fixReport = `## Fix Report (iteration 1)

### Per check
- test
  - Category: test
  - Root cause: off-by-one in the loop bound
  - Fix: foo.go — corrected the slice bound
### Status
STATUS: DONE`

func TestRun_PostsFixComment(t *testing.T) {
	gh := &githubtest.Fake{
		PR: github.PR{Title: "demo", HeadBranch: "feat/x", HeadSHA: "old"},
		Checks: [][]github.CheckRun{
			{failing(1, "test")},
			{passing("test")},
		},
		HeadSHAs: []string{"new"},
		Logs:     "FAIL: TestX\n",
	}
	cl := &fakeClaude{report: fixReport}
	r := newRunner(gh, cl, &fakePrompter{})

	var buf bytes.Buffer
	if err := r.Run(&buf, Options{PRRef: "o/r#7"}); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if gh.Count("AddPRComment") != 1 {
		t.Fatalf("AddPRComment called %d times, want 1", gh.Count("AddPRComment"))
	}
	body := gh.Comments()[0]
	if !strings.Contains(body, "## Fix Report (iteration 1)") {
		t.Errorf("posted comment must keep the report heading as its title:\n%s", body)
	}
	if !strings.Contains(body, "corrected the slice bound") || !strings.Contains(body, "### Per check") {
		t.Errorf("posted comment dropped the fix details:\n%s", body)
	}
	if !strings.Contains(body, fixCommentFooter(cl.model)) {
		t.Errorf("posted comment is missing the attribution footer:\n%s", body)
	}
	if !strings.Contains(buf.String(), "Posted the fix report as a comment on PR #7") {
		t.Errorf("missing fix-comment confirmation in output:\n%s", buf.String())
	}
}

func TestRun_NoFixCommentSkipsComment(t *testing.T) {
	gh := &githubtest.Fake{
		PR: github.PR{Title: "demo", HeadBranch: "feat/x", HeadSHA: "old"},
		Checks: [][]github.CheckRun{
			{failing(1, "test")},
			{passing("test")},
		},
		HeadSHAs: []string{"new"},
		Logs:     "FAIL: TestX\n",
	}
	cl := &fakeClaude{report: fixReport}
	r := newRunner(gh, cl, &fakePrompter{})

	if err := r.Run(io.Discard, Options{PRRef: "o/r#7", NoFixComment: true}); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if gh.Count("AddPRComment") != 0 {
		t.Errorf("AddPRComment called %d times, want 0 with --no-fix-comment", gh.Count("AddPRComment"))
	}
}

func TestRun_FixCommentFailureIsNonFatal(t *testing.T) {
	gh := &githubtest.Fake{
		PR: github.PR{Title: "demo", HeadBranch: "feat/x", HeadSHA: "old"},
		Checks: [][]github.CheckRun{
			{failing(1, "test")},
			{passing("test")},
		},
		HeadSHAs:   []string{"new"},
		Logs:       "FAIL: TestX\n",
		CommentErr: errors.New("github down"),
	}
	cl := &fakeClaude{report: fixReport}
	r := newRunner(gh, cl, &fakePrompter{})

	var buf bytes.Buffer
	if err := r.Run(&buf, Options{PRRef: "o/r#7"}); err != nil {
		t.Fatalf("Run returned %v, want nil despite the comment-post failure", err)
	}
	if !strings.Contains(buf.String(), "Could not post the fix report") {
		t.Errorf("expected a non-fatal warning about the failed comment post, got:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "All 1 checks passed") {
		t.Errorf("loop must still complete after a non-fatal comment failure:\n%s", buf.String())
	}
}

func TestRun_PostsEscalatedFixComment(t *testing.T) {
	const blocked = "## Fix Report (iteration 1)\n\n### Status\nSTATUS: BLOCKED"
	gh := &githubtest.Fake{
		PR:       github.PR{Title: "demo", HeadBranch: "feat/x", HeadSHA: "old"},
		Checks:   [][]github.CheckRun{{failing(1, "test")}},
		HeadSHAs: []string{"new"},
		Logs:     "FAIL: TestX\n",
	}
	cl := &fakeClaude{report: blocked}
	r := newRunner(gh, cl, &fakePrompter{})

	err := r.Run(io.Discard, Options{PRRef: "o/r#7"})
	if err == nil || !strings.Contains(err.Error(), "escalated") {
		t.Fatalf("Run err = %v, want an escalation error", err)
	}
	// An escalated report must still reach the PR so the human who has to
	// intervene sees why the loop stopped.
	if gh.Count("AddPRComment") != 1 {
		t.Errorf("AddPRComment called %d times, want 1 — an escalated report must still be posted", gh.Count("AddPRComment"))
	}
}

func TestRun_ExhaustsMaxIterations(t *testing.T) {
	failures := []github.CheckRun{failing(1, "test")}
	gh := &githubtest.Fake{
		PR:     github.PR{Title: "demo", HeadBranch: "b", HeadSHA: "sha0"},
		Checks: [][]github.CheckRun{failures, failures, failures},
		// Each iteration advances HEAD so the loop doesn't bail on
		// "no new commit detected".
		HeadSHAs: []string{"sha1", "sha2"},
	}
	cl := &fakeClaude{report: "tried"}
	r := newRunner(gh, cl, &fakePrompter{})

	err := r.Run(io.Discard, Options{PRRef: "o/r#1", MaxIterations: 2})
	if !errors.Is(err, ErrMaxIterations) {
		t.Fatalf("Run err = %v, want ErrMaxIterations", err)
	}
	if got := cl.called.Load(); got != 2 {
		t.Errorf("Claude.Fix called %d times, want 2", got)
	}
}

func TestRun_StopsWhenNoNewCommitPushed(t *testing.T) {
	gh := &githubtest.Fake{
		PR:     github.PR{Title: "demo", HeadBranch: "b", HeadSHA: "stuck"},
		Checks: [][]github.CheckRun{{failing(1, "test")}},
		// Head never advances → waitForNewHead returns the same SHA.
		HeadSHAs: []string{"stuck"},
	}
	cl := &fakeClaude{report: "no-op"}
	r := newRunner(gh, cl, &fakePrompter{})

	err := r.Run(io.Discard, Options{PRRef: "o/r#1", MaxIterations: 5})
	if err == nil || !strings.Contains(err.Error(), "no new commit") {
		t.Fatalf("expected 'no new commit' error, got %v", err)
	}
}

func TestRun_InteractiveStopsOnUserNo(t *testing.T) {
	failures := []github.CheckRun{failing(1, "test")}
	gh := &githubtest.Fake{
		PR: github.PR{Title: "demo", HeadBranch: "b", HeadSHA: "sha0"},
		// Two iterations of failures so the prompt fires before the second.
		Checks:   [][]github.CheckRun{failures, failures},
		HeadSHAs: []string{"sha1"},
	}
	cl := &fakeClaude{report: "patch"}
	pr := &fakePrompter{answers: []bool{false}}
	r := newRunner(gh, cl, pr)

	err := r.Run(io.Discard, Options{PRRef: "o/r#1", MaxIterations: 5, Interactive: true})
	if !errors.Is(err, ErrUserStopped) {
		t.Fatalf("Run err = %v, want ErrUserStopped", err)
	}
	if cl.called.Load() != 1 {
		t.Errorf("Claude.Fix called %d times, want 1 (stopped before iteration 2)", cl.called.Load())
	}
	if len(pr.asked) != 1 {
		t.Errorf("Confirm called %d times, want 1", len(pr.asked))
	}
}

func TestRun_PrintPromptWritesPromptAndSkipsClaude(t *testing.T) {
	gh := &githubtest.Fake{
		PR:       github.PR{Title: "demo", HeadBranch: "feat/x", HeadSHA: "abc1234"},
		Checks:   [][]github.CheckRun{{failing(1, "test"), passing("lint")}},
		HeadSHAs: []string{"abc1234"},
		Logs:     "FAIL: TestX\n",
	}
	cl := &fakeClaude{}
	r := newRunner(gh, cl, &fakePrompter{})
	r.BuildPrompt = func(ctx Context) string {
		return fmt.Sprintf("PROMPT pr=%s#%d head=%s iter=%d failed=%d",
			ctx.RepoFullName, ctx.PRNumber, ctx.HeadSHA, ctx.Iteration, len(ctx.FailedChecks))
	}

	var buf bytes.Buffer
	if err := r.Run(&buf, Options{PRRef: "owner/repo#7", PrintPrompt: true}); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if cl.called.Load() != 0 {
		t.Errorf("Claude.Fix called %d times in print-prompt mode, want 0", cl.called.Load())
	}
	if gh.Count("FetchAndCheckout") != 1 {
		t.Errorf("FetchAndCheckout called %d times, want 1 (no fresh checkout for Claude)", gh.Count("FetchAndCheckout"))
	}
	out := buf.String()
	if !strings.Contains(out, "PROMPT pr=owner/repo#7 head=abc1234 iter=1 failed=1") {
		t.Errorf("expected rendered prompt on stdout, got: %q", out)
	}
	if strings.Contains(out, "Iteration") || strings.Contains(out, "failed check(s)") {
		t.Errorf("status banners leaked to stdout in print-prompt mode: %q", out)
	}
	if !strings.HasSuffix(out, "\n") {
		t.Errorf("prompt output should end with a newline, got: %q", out)
	}
}

func TestRun_PrintPromptWithoutBuilderErrors(t *testing.T) {
	gh := &githubtest.Fake{
		PR:       github.PR{Title: "demo", HeadBranch: "b", HeadSHA: "sha0"},
		Checks:   [][]github.CheckRun{{failing(1, "test")}},
		HeadSHAs: []string{"sha0"},
	}
	cl := &fakeClaude{}
	r := newRunner(gh, cl, &fakePrompter{})
	// BuildPrompt left nil intentionally.

	err := r.Run(io.Discard, Options{PRRef: "o/r#1", PrintPrompt: true})
	if err == nil || !strings.Contains(err.Error(), "prompt builder") {
		t.Fatalf("expected prompt-builder error, got %v", err)
	}
}

func TestRun_PrintPromptAllGreenStillExits(t *testing.T) {
	gh := &githubtest.Fake{
		PR:       github.PR{Title: "demo", HeadBranch: "b", HeadSHA: "sha0"},
		Checks:   [][]github.CheckRun{{passing("test")}},
		HeadSHAs: []string{"sha0"},
	}
	cl := &fakeClaude{}
	r := newRunner(gh, cl, &fakePrompter{})
	r.BuildPrompt = func(Context) string { return "should not run" }

	var buf bytes.Buffer
	if err := r.Run(&buf, Options{PRRef: "o/r#1", PrintPrompt: true}); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if strings.Contains(buf.String(), "should not run") {
		t.Errorf("prompt rendered despite all checks passing: %q", buf.String())
	}
}

// barePromptRunner returns a fix.Runner whose GitHub fake clones into a
// throwaway dir so PrintBarePrompt can run detect.Technologies +
// patterns.LoadFiltered without hitting the network.
func barePromptRunner(t *testing.T) (*Runner, *githubtest.Fake) {
	t.Helper()
	gh := &githubtest.Fake{
		PR:  github.PR{Title: "demo", HeadBranch: "feat/x", HeadSHA: "abc1234"},
		Dir: t.TempDir(),
	}
	r := newRunner(gh, &fakeClaude{}, &fakePrompter{})
	return r, gh
}

func TestPrintBarePrompt_WritesPromptForRef(t *testing.T) {
	r, _ := barePromptRunner(t)
	build := func(ctx BareContext) string {
		return fmt.Sprintf("BARE repo=%s pr=%d", ctx.RepoFullName, ctx.PRNumber)
	}
	var buf bytes.Buffer
	if err := r.PrintBarePrompt(&buf, Options{PRRef: "https://github.com/owner/repo/pull/42"}, build); err != nil {
		t.Fatalf("PrintBarePrompt returned %v, want nil", err)
	}
	out := buf.String()
	if !strings.HasPrefix(out, "BARE repo=owner/repo pr=42") {
		t.Errorf("expected rendered bare prompt with parsed ref, got: %q", out)
	}
	if !strings.HasSuffix(out, "\n") {
		t.Errorf("output should end with a newline, got: %q", out)
	}
}

func TestPrintBarePrompt_AcceptsShortForm(t *testing.T) {
	r, _ := barePromptRunner(t)
	var got BareContext
	build := func(ctx BareContext) string {
		got = ctx
		return "ok"
	}
	if err := r.PrintBarePrompt(io.Discard, Options{PRRef: "owner/repo#7"}, build); err != nil {
		t.Fatalf("PrintBarePrompt returned %v, want nil", err)
	}
	if got.RepoFullName != "owner/repo" || got.PRNumber != 7 {
		t.Errorf("builder got repo=%q pr=%d, want owner/repo / 7", got.RepoFullName, got.PRNumber)
	}
}

func TestPrintBarePrompt_RejectsBadRef(t *testing.T) {
	r, _ := barePromptRunner(t)
	err := r.PrintBarePrompt(io.Discard, Options{PRRef: "not-a-ref"}, func(BareContext) string { return "" })
	if err == nil || !strings.Contains(err.Error(), "parsing PR ref") {
		t.Fatalf("expected parsing error, got %v", err)
	}
}

func TestPrintBarePrompt_RequiresBuilder(t *testing.T) {
	r, _ := barePromptRunner(t)
	err := r.PrintBarePrompt(io.Discard, Options{PRRef: "owner/repo#1"}, nil)
	if err == nil || !strings.Contains(err.Error(), "prompt builder") {
		t.Fatalf("expected builder-required error, got %v", err)
	}
}

// TestPrintBarePrompt_LoadsAndPassesPatterns proves that the bare-prompt
// path also runs patterns.Resolve + patterns.LoadFiltered and surfaces
// the result through BareContext, just like the orchestrator-driven Run.
func TestPrintBarePrompt_LoadsAndPassesPatterns(t *testing.T) {
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

	r, _ := barePromptRunner(t)
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
	// The pattern came from --patterns (not bundled, not repo), so it has
	// neither a URL nor a LocalPath — just the explicit-source note.
	entry := got.PatternCatalog[0]
	if entry.URL != "" || entry.LocalPath != "" {
		t.Errorf("expected no URL/LocalPath for --patterns entry, got URL=%q LocalPath=%q", entry.URL, entry.LocalPath)
	}
	if entry.OriginNote == "" {
		t.Errorf("expected OriginNote for --patterns entry, got empty")
	}
}

func TestRun_DryRunSkipsClaude(t *testing.T) {
	gh := &githubtest.Fake{
		PR:       github.PR{Title: "demo", HeadBranch: "b", HeadSHA: "sha0"},
		Checks:   [][]github.CheckRun{{failing(1, "test")}},
		HeadSHAs: []string{"sha0"},
	}
	cl := &fakeClaude{}
	r := newRunner(gh, cl, &fakePrompter{})

	var buf bytes.Buffer
	if err := r.Run(&buf, Options{PRRef: "o/r#1", DryRun: true}); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if cl.called.Load() != 0 {
		t.Errorf("Claude.Fix called %d times in dry-run, want 0", cl.called.Load())
	}
	if !strings.Contains(buf.String(), "[dry-run]") {
		t.Errorf("missing dry-run notice: %s", buf.String())
	}
}

func TestRun_PendingChecksWaitThenSucceed(t *testing.T) {
	pending := github.CheckRun{ID: 1, Name: "test", Status: "in_progress"}
	gh := &githubtest.Fake{
		PR:       github.PR{Title: "demo", HeadBranch: "b", HeadSHA: "sha0"},
		Checks:   [][]github.CheckRun{{pending}, {pending}, {passing("test")}},
		HeadSHAs: []string{"sha0"},
	}
	cl := &fakeClaude{}
	var sleeps atomic.Int32
	r := newRunner(gh, cl, &fakePrompter{})
	r.Sleep = func(time.Duration) { sleeps.Add(1) }

	if err := r.Run(io.Discard, Options{PRRef: "o/r#1"}); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if sleeps.Load() < 2 {
		t.Errorf("sleeps = %d, want >= 2 (waited for pending checks)", sleeps.Load())
	}
}

func TestRun_PropagatesClaudeError(t *testing.T) {
	gh := &githubtest.Fake{
		PR:       github.PR{Title: "demo", HeadBranch: "b", HeadSHA: "sha0"},
		Checks:   [][]github.CheckRun{{failing(1, "test")}},
		HeadSHAs: []string{"sha1"},
	}
	cl := &fakeClaude{err: fmt.Errorf("boom")}
	r := newRunner(gh, cl, &fakePrompter{})

	err := r.Run(io.Discard, Options{PRRef: "o/r#1"})
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected wrapped 'boom' error, got %v", err)
	}
}

func TestTrimLogs(t *testing.T) {
	if got := trimLogs("short", 100); got != "short" {
		t.Errorf("under-cap log was modified: %q", got)
	}
	long := strings.Repeat("x", 200)
	got := trimLogs(long, 50)
	if !strings.Contains(got, "earlier characters truncated") {
		t.Errorf("missing truncation header: %q", got)
	}
	if !strings.HasSuffix(got, strings.Repeat("x", 50)) {
		t.Errorf("trimmed log should keep the tail")
	}
}

// writeSamplePattern seeds a --patterns directory holding one pattern named
// "Sample wiring check", whose catalog file is sampleCatalogFile, and returns
// the directory.
func writeSamplePattern(t *testing.T) string {
	t.Helper()
	patternsDir := t.TempDir()
	patternFile := patternsDir + "/sample.md"
	const patternBody = `# Review Pattern: Sample wiring check
**Review-Area**: meta
**Detection-Hint**: anything
**Severity**: WARNING

## Rule
Wired patterns must reach the fix Context.
`
	if err := os.WriteFile(patternFile, []byte(patternBody), 0o644); err != nil {
		t.Fatalf("seeding pattern file: %v", err)
	}
	return patternsDir
}

// sampleCatalogFile is the catalog file name of the pattern writeSamplePattern
// seeds.
const sampleCatalogFile = "sample-wiring-check.md"

// samplePatternOptions runs ref against only the pattern in patternsDir, so
// the loaded set is exactly that one pattern.
func samplePatternOptions(ref, patternsDir string) Options {
	return Options{
		PRRef:           ref,
		PatternDirs:     []string{patternsDir},
		NoLocalPatterns: true,
		NoRepoPatterns:  true,
	}
}

func TestRun_PassesPatternsToClaude(t *testing.T) {
	// Use an in-process patterns dir so LoadFiltered returns at least one
	// pattern, proving the wiring from Options through to Claude.Fix
	// actually carries patterns into the prompt context.
	patternsDir := writeSamplePattern(t)

	gh := &githubtest.Fake{
		PR: github.PR{Title: "demo", HeadBranch: "feat/x", HeadSHA: "old"},
		Checks: [][]github.CheckRun{
			{failing(1, "test")},
			{passing("test")},
		},
		HeadSHAs: []string{"new"},
		Logs:     "FAIL: TestX\n",
	}
	cl := &fakeClaude{report: "fixed"}
	cl.onFix = func(ctx Context) {
		if ctx.Catalog.Dir == "" {
			t.Errorf("fix session got no catalog directory, want the one Run wrote")
			return
		}
		if _, err := os.Stat(filepath.Join(ctx.Catalog.Dir, sampleCatalogFile)); err != nil {
			t.Errorf("stat %s in the catalog during the session: %v, want the file on disk", sampleCatalogFile, err)
		}
	}
	r := newRunner(gh, cl, &fakePrompter{})

	if err := r.Run(io.Discard, samplePatternOptions("owner/repo#7", patternsDir)); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if cl.called.Load() != 1 {
		t.Fatalf("Claude.Fix called %d times, want 1", cl.called.Load())
	}
	if got := len(cl.ctx.Patterns); got != 1 {
		t.Fatalf("Claude got %d patterns, want 1 (wiring broken)", got)
	}
	if cl.ctx.Patterns[0].Name != "Sample wiring check" {
		t.Errorf("Claude got pattern name %q, want %q", cl.ctx.Patterns[0].Name, "Sample wiring check")
	}
	if cl.ctx.Catalog.Dir == "" {
		t.Fatalf("Claude got no catalog directory, want the one Run wrote")
	}
	if _, err := os.Stat(cl.ctx.Catalog.Dir); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("stat %s after Run: %v, want fs.ErrNotExist (the catalog is removed with its iteration's checkout)", cl.ctx.Catalog.Dir, err)
	}
}

// TestRun_FixErrorRemovesTheCatalog locks that a failed fix session still
// removes its iteration's catalog and its memory directory: the loop removes
// them explicitly rather than by defer, so the removal must come before the
// error return.
func TestRun_FixErrorRemovesTheCatalog(t *testing.T) {
	patternsDir := writeSamplePattern(t)
	tmp := patternstest.IsolateTempDir(t)

	gh := &githubtest.Fake{
		PR:     github.PR{Title: "demo", HeadBranch: "feat/x", HeadSHA: "old"},
		Checks: [][]github.CheckRun{{failing(1, "test")}},
		Logs:   "FAIL: TestX\n",
	}
	boom := errors.New("boom")
	cl := &fakeClaude{err: boom}
	cl.onFix = func(ctx Context) { patternstest.AssertMemoryPageReadable(t, ctx.Memory) }
	r := newRunner(gh, cl, &fakePrompter{})
	r.ResolveWiki = (&patternstest.WikiSeam{Wiki: patternstest.MemoryWiki()}).Resolve

	err := r.Run(io.Discard, samplePatternOptions("owner/repo#7", patternsDir))
	if !errors.Is(err, boom) {
		t.Fatalf("Run err = %v, want it to wrap %v", err, boom)
	}
	if cl.ctx.Catalog.Dir == "" {
		t.Fatalf("Claude got no catalog directory, want the one the iteration wrote")
	}
	if _, err := os.Stat(cl.ctx.Catalog.Dir); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("stat %s after the failed iteration: %v, want fs.ErrNotExist", cl.ctx.Catalog.Dir, err)
	}
	patternstest.AssertNoMemoryDir(t, tmp)
}

// TestRun_ResolvesTheWikiOncePerRun locks the two lifetimes of the wiki in a
// fix run: the wiki is resolved once, and every iteration writes its own
// memory directory, which its session can read and which is gone afterwards.
func TestRun_ResolvesTheWikiOncePerRun(t *testing.T) {
	tmp := patternstest.IsolateTempDir(t)
	failures := []github.CheckRun{failing(1, "test")}
	gh := &githubtest.Fake{
		PR:       github.PR{Title: "demo", HeadBranch: "b", HeadSHA: "sha0"},
		Checks:   [][]github.CheckRun{failures, failures, failures},
		HeadSHAs: []string{"sha1", "sha2"},
	}
	cl := &fakeClaude{report: "tried"}
	cl.onFix = func(ctx Context) { patternstest.AssertMemoryPageReadable(t, ctx.Memory) }
	seam := &patternstest.WikiSeam{Wiki: patternstest.MemoryWiki()}
	r := newRunner(gh, cl, &fakePrompter{})
	r.ResolveWiki = seam.Resolve

	opts := samplePatternOptions("o/r#1", writeSamplePattern(t))
	opts.MaxIterations = 2
	if err := r.Run(io.Discard, opts); !errors.Is(err, ErrMaxIterations) {
		t.Fatalf("Run err = %v, want ErrMaxIterations", err)
	}
	if got := len(cl.ctxs); got != 2 {
		t.Fatalf("Claude.Fix called %d times, want 2", got)
	}
	if seam.Calls != 1 {
		t.Errorf("the wiki was resolved %d times, want once for the whole run", seam.Calls)
	}
	first, second := cl.ctxs[0].Memory.Dir, cl.ctxs[1].Memory.Dir
	if first == "" || second == "" || first == second {
		t.Errorf("memory directories = %q, %q, want a fresh one per iteration", first, second)
	}
	patternstest.AssertNoMemoryDir(t, tmp)
}

// TestRun_NoWikiResolveWithoutAFixSession locks the lazy resolve: a run that
// dispatches no session never pays for the wiki, and a printed prompt, which
// outlives the run, carries no project memory.
func TestRun_NoWikiResolveWithoutAFixSession(t *testing.T) {
	cases := []struct {
		name   string
		checks []github.CheckRun
		opts   Options
	}{
		{"the checks pass at the first poll", []github.CheckRun{passing("test")}, Options{}},
		{"a dry run", []github.CheckRun{failing(1, "test")}, Options{DryRun: true}},
		{"a printed prompt", []github.CheckRun{failing(1, "test")}, Options{PrintPrompt: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gh := &githubtest.Fake{
				PR:       github.PR{Title: "demo", HeadBranch: "b", HeadSHA: "sha0"},
				Checks:   [][]github.CheckRun{tc.checks},
				HeadSHAs: []string{"sha0"},
			}
			cl := &fakeClaude{}
			seam := &patternstest.WikiSeam{Wiki: patternstest.MemoryWiki()}
			r := newRunner(gh, cl, &fakePrompter{})
			r.ResolveWiki = seam.Resolve
			// Stands in for claude.BuildFixPrompt, which renders the section
			// exactly when the context carries memory pages.
			r.BuildPrompt = func(ctx Context) string {
				if len(ctx.Memory.Pages) > 0 {
					return "PROMPT\n## Project Memory\n"
				}
				return "PROMPT\n"
			}

			opts := tc.opts
			opts.PRRef = "o/r#1"
			opts.Wiki = patterns.WikiOptions{Enabled: true}
			var out bytes.Buffer
			if err := r.Run(&out, opts); err != nil {
				t.Fatalf("Run returned %v, want nil", err)
			}
			if seam.Calls != 0 {
				t.Errorf("the wiki was resolved %d times, want 0", seam.Calls)
			}
			if cl.called.Load() != 0 {
				t.Errorf("Claude.Fix called %d times, want 0", cl.called.Load())
			}
			if tc.opts.PrintPrompt && !strings.Contains(out.String(), "PROMPT") {
				t.Fatalf("the prompt was not printed: %q", out.String())
			}
			if strings.Contains(out.String(), "## Project Memory") {
				t.Errorf("the output carries the project memory: %q", out.String())
			}
		})
	}
}

// TestRun_PassesWikiOptionsToTheSeam locks the wiring of the wiki opt-in: the
// resolver sees the pull request's repository and the options the command
// resolved.
func TestRun_PassesWikiOptionsToTheSeam(t *testing.T) {
	gh := &githubtest.Fake{
		PR:       github.PR{Title: "demo", HeadBranch: "feat/x", HeadSHA: "old"},
		Checks:   [][]github.CheckRun{{failing(1, "test")}, {passing("test")}},
		HeadSHAs: []string{"new"},
	}
	seam := &patternstest.WikiSeam{}
	r := newRunner(gh, &fakeClaude{report: "fixed"}, &fakePrompter{})
	r.ResolveWiki = seam.Resolve

	opts := Options{PRRef: "owner/repo#7", NoLocalPatterns: true, NoRepoPatterns: true}
	opts.Wiki = patterns.WikiOptions{Enabled: true, Repo: "acme/handbook", Ref: "v1"}
	opts.Remote = patterns.RemoteOptions{TTL: time.Hour}
	if err := r.Run(io.Discard, opts); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if seam.Repo != "owner/repo" || seam.Opts != opts.Wiki || seam.Remote.TTL != time.Hour {
		t.Errorf("resolver got %s, %+v, %+v; want owner/repo and the run's options", seam.Repo, seam.Opts, seam.Remote)
	}
}

// TestRun_WikiPatternsReachTheContext locks the wiki tier of the pattern
// load: a pattern file under the resolved wiki's review_patterns/ is part of
// the catalog the fix is held to.
func TestRun_WikiPatternsReachTheContext(t *testing.T) {
	gh := &githubtest.Fake{
		PR:       github.PR{Title: "demo", HeadBranch: "feat/x", HeadSHA: "old"},
		Checks:   [][]github.CheckRun{{failing(1, "test")}, {passing("test")}},
		HeadSHAs: []string{"new"},
	}
	cl := &fakeClaude{report: "fixed"}
	r := newRunner(gh, cl, &fakePrompter{})
	// writeSamplePattern stands in for the wiki's review_patterns/ directory.
	r.ResolveWiki = (&patternstest.WikiSeam{Wiki: patterns.ResolvedWiki{Repo: "owner/repo", CommitSHA: "wikisha", PatternsDir: writeSamplePattern(t)}}).Resolve

	opts := Options{PRRef: "owner/repo#7", NoLocalPatterns: true, NoRepoPatterns: true}
	if err := r.Run(io.Discard, opts); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if len(cl.ctx.Patterns) != 1 || cl.ctx.Patterns[0].Name != "Sample wiring check" {
		t.Errorf("Context.Patterns = %+v, want the one wiki pattern", cl.ctx.Patterns)
	}
}

// TestRun_DisabledWikiLeavesMemoryZero covers the run without a wiki: the
// zero ResolvedWiki (disabled, uninitialized, or unresolvable) hands the fix
// session no memory and writes no directory.
func TestRun_DisabledWikiLeavesMemoryZero(t *testing.T) {
	tmp := patternstest.IsolateTempDir(t)
	gh := &githubtest.Fake{
		PR:       github.PR{Title: "demo", HeadBranch: "feat/x", HeadSHA: "old"},
		Checks:   [][]github.CheckRun{{failing(1, "test")}, {passing("test")}},
		HeadSHAs: []string{"new"},
	}
	cl := &fakeClaude{report: "fixed"}
	r := newRunner(gh, cl, &fakePrompter{})
	r.ResolveWiki = (&patternstest.WikiSeam{}).Resolve

	if err := r.Run(io.Discard, samplePatternOptions("owner/repo#7", writeSamplePattern(t))); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if cl.called.Load() != 1 {
		t.Fatalf("Claude.Fix called %d times, want 1", cl.called.Load())
	}
	if !reflect.DeepEqual(cl.ctx.Memory, patterns.MemoryCatalog{}) {
		t.Errorf("Context.Memory = %+v, want the zero MemoryCatalog", cl.ctx.Memory)
	}
	patternstest.AssertNoMemoryDir(t, tmp)
}

// TestRun_EachFixIterationGetsItsOwnCatalog locks that every iteration writes
// a fresh catalog and removes it before the next iteration's session runs, so
// no catalog outlives the checkout its patterns were loaded from.
func TestRun_EachFixIterationGetsItsOwnCatalog(t *testing.T) {
	patternsDir := writeSamplePattern(t)

	failures := []github.CheckRun{failing(1, "test")}
	gh := &githubtest.Fake{
		PR:     github.PR{Title: "demo", HeadBranch: "b", HeadSHA: "sha0"},
		Checks: [][]github.CheckRun{failures, failures, failures},
		// Each iteration advances HEAD so the loop doesn't bail on
		// "no new commit detected".
		HeadSHAs: []string{"sha1", "sha2"},
	}
	cl := &fakeClaude{report: "tried"}
	cl.onFix = func(ctx Context) {
		if len(cl.ctxs) != 2 {
			return
		}
		first := cl.ctxs[0].Catalog.Dir
		if _, err := os.Stat(first); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("stat iteration 1's catalog %s during iteration 2: %v, want fs.ErrNotExist", first, err)
		}
		if _, err := os.Stat(filepath.Join(ctx.Catalog.Dir, sampleCatalogFile)); err != nil {
			t.Errorf("stat %s in iteration 2's catalog during the session: %v, want the file on disk", sampleCatalogFile, err)
		}
	}
	r := newRunner(gh, cl, &fakePrompter{})

	opts := samplePatternOptions("o/r#1", patternsDir)
	opts.MaxIterations = 2
	err := r.Run(io.Discard, opts)
	if !errors.Is(err, ErrMaxIterations) {
		t.Fatalf("Run err = %v, want ErrMaxIterations", err)
	}
	if got := len(cl.ctxs); got != 2 {
		t.Fatalf("Claude.Fix called %d times, want 2", got)
	}
	first, second := cl.ctxs[0].Catalog.Dir, cl.ctxs[1].Catalog.Dir
	if first == "" || second == "" {
		t.Fatalf("catalog directories = %q, %q, want both set", first, second)
	}
	if first == second {
		t.Errorf("both iterations got catalog %q, want a fresh one per iteration", first)
	}
	for _, dir := range []string{first, second} {
		if _, err := os.Stat(dir); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("stat %s after Run: %v, want fs.ErrNotExist", dir, err)
		}
	}
}

func TestStdinPrompter_YesNo(t *testing.T) {
	cases := []struct {
		input string
		want  bool
	}{
		{"y\n", true},
		{"Y\n", true},
		{"yes\n", true},
		{"YES\n", true},
		{"n\n", false},
		{"no\n", false},
		{"\n", false},
		{"garbage\n", false},
	}
	for _, c := range cases {
		p := stdinPrompter{In: strings.NewReader(c.input), Out: io.Discard}
		got, err := p.Confirm("?")
		if err != nil {
			t.Errorf("Confirm(%q) err = %v", c.input, err)
		}
		if got != c.want {
			t.Errorf("Confirm(%q) = %v, want %v", c.input, got, c.want)
		}
	}
}

func TestRunLocalSkipsReclone(t *testing.T) {
	failures := []github.CheckRun{failing(1, "test")}
	gh := &githubtest.Fake{
		PR:     github.PR{Title: "demo", HeadBranch: "feat/x", BaseBranch: "main", HeadSHA: "sha0"},
		Dir:    t.TempDir(),
		Checks: [][]github.CheckRun{failures, failures, failures},
		// Each iteration advances HEAD so the loop reaches the iteration cap
		// instead of bailing on "no new commit".
		HeadSHAs: []string{"s1", "s2", "s3"},
	}
	cl := &fakeClaude{report: "tried"}
	r := newRunner(gh, cl, &fakePrompter{})

	err := r.Run(io.Discard, Options{
		PRRef:           "o/r#1",
		MaxIterations:   3,
		Local:           true,
		NoLocalPatterns: true,
		NoRepoPatterns:  true,
	})
	if !errors.Is(err, ErrMaxIterations) {
		t.Fatalf("Run err = %v, want ErrMaxIterations", err)
	}
	// The Claude session must learn it is a --local run and which base branch
	// bounds the autosquash fold, so the prompt can render the fixup workflow.
	if !cl.ctx.Local {
		t.Error("Claude context Local = false, want true in --local mode")
	}
	if cl.ctx.BaseBranch != "main" {
		t.Errorf("Claude context BaseBranch = %q, want \"main\"", cl.ctx.BaseBranch)
	}
	if gh.Count("OpenLocalPR") != 1 {
		t.Errorf("OpenLocalPR calls = %d, want 1 (initial metadata fetch)", gh.Count("OpenLocalPR"))
	}
	if gh.Count("FetchAndCheckout") != 0 {
		t.Errorf("FetchAndCheckout (temp-dir re-clone) calls = %d, want 0 in local mode", gh.Count("FetchAndCheckout"))
	}
	if gh.Count("PullFFOnly") != 2 {
		t.Errorf("PullFFOnly calls = %d, want 2 (one per iteration after the first)", gh.Count("PullFFOnly"))
	}
	// The local working tree must survive: Cleanup is a no-op when Local.
	if _, err := os.Stat(gh.Dir); err != nil {
		t.Fatalf("local checkout must survive the fix loop: %v", err)
	}
}

func TestRunLocalAllChecksPassingNoPull(t *testing.T) {
	gh := &githubtest.Fake{
		PR:       github.PR{Title: "demo", HeadBranch: "feat/x", HeadSHA: "abc1234"},
		Dir:      t.TempDir(),
		Checks:   [][]github.CheckRun{{passing("lint"), passing("test")}},
		HeadSHAs: []string{"abc1234"},
	}
	cl := &fakeClaude{}
	r := newRunner(gh, cl, &fakePrompter{})

	if err := r.Run(io.Discard, Options{PRRef: "o/r#1", Local: true}); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if gh.Count("OpenLocalPR") != 1 {
		t.Errorf("OpenLocalPR calls = %d, want 1", gh.Count("OpenLocalPR"))
	}
	if gh.Count("FetchAndCheckout") != 0 {
		t.Errorf("FetchAndCheckout calls = %d, want 0", gh.Count("FetchAndCheckout"))
	}
	if gh.Count("PullFFOnly") != 0 {
		t.Errorf("PullFFOnly calls = %d, want 0 when checks already pass", gh.Count("PullFFOnly"))
	}
}

// TestRun_NoChecksEverReportedStopsCleanly is the regression test for the hang:
// the Total == 0 branch looped forever, and because AllPassed deliberately
// requires a non-empty set it could never converge. A pull request on a repo
// without CI — or a fork PR whose workflows await approval — parked the fix
// loop on a sleep nothing would end, and through ship the whole fleet run with
// it.
func TestRun_NoChecksEverReportedStopsCleanly(t *testing.T) {
	gh := &githubtest.Fake{
		PR:       github.PR{Title: "demo", HeadBranch: "feat/x", HeadSHA: "abc1234"},
		Checks:   [][]github.CheckRun{{}}, // every poll: no checks at all
		HeadSHAs: []string{"abc1234"},
	}
	cl := &fakeClaude{}
	r := newRunner(gh, cl, &fakePrompter{})
	var sleeps atomic.Int32
	r.Sleep = func(time.Duration) { sleeps.Add(1) }

	done := make(chan error, 1)
	go func() { var buf bytes.Buffer; done <- r.Run(&buf, Options{PRRef: "owner/repo#1"}) }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run = %v, want nil: a repo without CI is not a broken pull request", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run never returned: waitForChecks is still polling for a check that will never appear")
	}

	if got := int(sleeps.Load()); got != maxNoCheckPolls {
		t.Errorf("slept %d times, want %d — the wait must be bounded", got, maxNoCheckPolls)
	}
	if cl.called.Load() != 0 {
		t.Errorf("claude was invoked %d times although there was nothing to fix", cl.called.Load())
	}
}

// TestWaitForChecks_LateCheckStillCounts guards the other direction: a check
// that shows up after a few empty polls resets the budget and is waited on.
func TestWaitForChecks_LateCheckStillCounts(t *testing.T) {
	gh := &githubtest.Fake{
		Checks: [][]github.CheckRun{{}, {}, {passing("lint")}},
	}
	r := newRunner(gh, &fakeClaude{}, &fakePrompter{})

	var buf bytes.Buffer
	summary, err := r.waitForChecks(&buf, "owner", "repo", "abc1234", time.Millisecond)
	if err != nil {
		t.Fatalf("waitForChecks = %v, want the late check", err)
	}
	if !summary.AllPassed() {
		t.Errorf("summary = %+v, want the late check counted", summary)
	}
}

func TestRun_QuotedStatusDoesNotEscalate(t *testing.T) {
	// A report may restate an earlier verdict on its own line (a superseded
	// iteration's status) before closing with its real one. Only the last
	// standalone STATUS line counts, so the earlier BLOCKED must not stop the
	// loop.
	const quoted = "## Fix Report (iteration 2)\n\n### Previous iteration\n- STATUS: BLOCKED\n\n### Status\nSTATUS: DONE"
	gh := &githubtest.Fake{
		PR: github.PR{Title: "demo", HeadBranch: "feat/x", HeadSHA: "old"},
		Checks: [][]github.CheckRun{
			{failing(1, "test")},
			{passing("test")},
		},
		HeadSHAs: []string{"new"},
		Logs:     "FAIL: TestX\n",
	}
	cl := &fakeClaude{report: quoted}
	r := newRunner(gh, cl, &fakePrompter{})

	if err := r.Run(io.Discard, Options{PRRef: "o/r#7"}); err != nil {
		t.Fatalf("Run returned %v, want nil — a quoted BLOCKED before the DONE verdict must not escalate", err)
	}
}

func TestRun_StatusWithTrailingReasonEscalates(t *testing.T) {
	// A verdict followed by a reason on the same line ("STATUS: BLOCKED — the
	// registry is unreachable") is still that verdict.
	const blocked = "## Fix Report (iteration 1)\n\n### Status\nSTATUS: BLOCKED — the registry is unreachable"
	gh := &githubtest.Fake{
		PR:       github.PR{Title: "demo", HeadBranch: "feat/x", HeadSHA: "old"},
		Checks:   [][]github.CheckRun{{failing(1, "test")}},
		HeadSHAs: []string{"new"},
		Logs:     "FAIL: TestX\n",
	}
	cl := &fakeClaude{report: blocked}
	r := newRunner(gh, cl, &fakePrompter{})

	err := r.Run(io.Discard, Options{PRRef: "o/r#7"})
	if err == nil || !strings.Contains(err.Error(), "escalated") {
		t.Fatalf("Run err = %v, want an escalation error for a BLOCKED verdict with a trailing reason", err)
	}
}
