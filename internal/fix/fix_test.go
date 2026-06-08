package fix

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/planwerk/planwerk-review/internal/github"
)

// fakeGitHub is a scripted GitHubClient. checkResponses[i] is what the i-th
// ListChecks call returns; the head SHA the fix loop polls advances along
// headSequence as fix iterations push commits.
type fakeGitHub struct {
	checkResponses [][]github.CheckRun
	checkErr       error
	checkIdx       atomic.Int32

	logs    string
	logsErr error

	headSequence []string
	headIdx      atomic.Int32

	prTitle    string
	prBranch   string
	prHeadSHA  string
	cloneDir   string // optional: directory used as PR.Dir; "" → no-op cleanup
	cloneCalls atomic.Int32
	localCalls atomic.Int32
	pullCalls  atomic.Int32
}

func (f *fakeGitHub) FetchAndCheckout(ref string) (*github.PR, error) {
	f.cloneCalls.Add(1)
	return f.makePR(ref, false)
}

// FetchAndCheckoutLocal mirrors github.OpenLocalPR: it returns a Local PR so
// Cleanup is a no-op and the working tree survives across iterations.
func (f *fakeGitHub) FetchAndCheckoutLocal(ref string, _ github.LocalOptions) (*github.PR, error) {
	f.localCalls.Add(1)
	return f.makePR(ref, true)
}

// PullOnBranch records a fast-forward refresh of the local checkout.
func (f *fakeGitHub) PullOnBranch(_, _ string) error {
	f.pullCalls.Add(1)
	return nil
}

func (f *fakeGitHub) makePR(ref string, local bool) (*github.PR, error) {
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
		HeadSHA:    f.prHeadSHA,
		Dir:        f.cloneDir,
		Local:      local,
	}, nil
}

func (f *fakeGitHub) ListChecks(_, _, _ string) ([]github.CheckRun, error) {
	if f.checkErr != nil {
		return nil, f.checkErr
	}
	i := int(f.checkIdx.Add(1)) - 1
	if i >= len(f.checkResponses) {
		i = len(f.checkResponses) - 1
	}
	return f.checkResponses[i], nil
}

func (f *fakeGitHub) FailedRunLogs(_, _ string, _ int64) (string, error) {
	return f.logs, f.logsErr
}

func (f *fakeGitHub) HeadSHA(_, _, _ string) (string, error) {
	i := int(f.headIdx.Add(1)) - 1
	if i >= len(f.headSequence) {
		i = len(f.headSequence) - 1
	}
	return f.headSequence[i], nil
}

type fakeClaude struct {
	called atomic.Int32
	report string
	err    error
	ctx    Context
}

func (f *fakeClaude) Fix(_ string, ctx Context) (string, error) {
	f.called.Add(1)
	f.ctx = ctx
	return f.report, f.err
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

func newRunner(gh *fakeGitHub, cl *fakeClaude, pr *fakePrompter) *Runner {
	return &Runner{
		Claude:   cl,
		GitHub:   gh,
		Prompter: pr,
		Sleep:    func(time.Duration) {}, // tests must not sleep
		Now:      time.Now,
	}
}

func TestRun_AllChecksAlreadyPassing(t *testing.T) {
	gh := &fakeGitHub{
		prTitle:        "demo",
		prBranch:       "feat/x",
		prHeadSHA:      "abc1234",
		checkResponses: [][]github.CheckRun{{passing("lint"), passing("test")}},
		headSequence:   []string{"abc1234"},
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
	gh := &fakeGitHub{
		prTitle:    "demo",
		prBranch:   "feat/x",
		prHeadSHA:  "old",
		// Iteration 1 sees a failure; iteration 2 (post-push) sees green.
		checkResponses: [][]github.CheckRun{
			{failing(1, "test"), passing("lint")},
			{passing("test"), passing("lint")},
		},
		headSequence: []string{"new"},
		logs:         "FAIL: TestX\n",
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

func TestRun_ExhaustsMaxIterations(t *testing.T) {
	failures := []github.CheckRun{failing(1, "test")}
	gh := &fakeGitHub{
		prTitle:        "demo",
		prBranch:       "b",
		prHeadSHA:      "sha0",
		checkResponses: [][]github.CheckRun{failures, failures, failures},
		// Each iteration advances HEAD so the loop doesn't bail on
		// "no new commit detected".
		headSequence: []string{"sha1", "sha2"},
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
	gh := &fakeGitHub{
		prTitle:        "demo",
		prBranch:       "b",
		prHeadSHA:      "stuck",
		checkResponses: [][]github.CheckRun{{failing(1, "test")}},
		// Head never advances → waitForNewHead returns the same SHA.
		headSequence: []string{"stuck"},
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
	gh := &fakeGitHub{
		prTitle:   "demo",
		prBranch:  "b",
		prHeadSHA: "sha0",
		// Two iterations of failures so the prompt fires before the second.
		checkResponses: [][]github.CheckRun{failures, failures},
		headSequence:   []string{"sha1"},
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
	gh := &fakeGitHub{
		prTitle:        "demo",
		prBranch:       "feat/x",
		prHeadSHA:      "abc1234",
		checkResponses: [][]github.CheckRun{{failing(1, "test"), passing("lint")}},
		headSequence:   []string{"abc1234"},
		logs:           "FAIL: TestX\n",
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
	if gh.cloneCalls.Load() != 1 {
		t.Errorf("FetchAndCheckout called %d times, want 1 (no fresh checkout for Claude)", gh.cloneCalls.Load())
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
	gh := &fakeGitHub{
		prTitle:        "demo",
		prBranch:       "b",
		prHeadSHA:      "sha0",
		checkResponses: [][]github.CheckRun{{failing(1, "test")}},
		headSequence:   []string{"sha0"},
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
	gh := &fakeGitHub{
		prTitle:        "demo",
		prBranch:       "b",
		prHeadSHA:      "sha0",
		checkResponses: [][]github.CheckRun{{passing("test")}},
		headSequence:   []string{"sha0"},
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
func barePromptRunner(t *testing.T) (*Runner, *fakeGitHub) {
	t.Helper()
	gh := &fakeGitHub{
		prTitle:    "demo",
		prBranch:   "feat/x",
		prHeadSHA:  "abc1234",
		cloneDir:   t.TempDir(),
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
// path also runs collectPatternDirs + patterns.LoadFiltered and surfaces
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
	gh := &fakeGitHub{
		prTitle:        "demo",
		prBranch:       "b",
		prHeadSHA:      "sha0",
		checkResponses: [][]github.CheckRun{{failing(1, "test")}},
		headSequence:   []string{"sha0"},
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
	gh := &fakeGitHub{
		prTitle:        "demo",
		prBranch:       "b",
		prHeadSHA:      "sha0",
		checkResponses: [][]github.CheckRun{{pending}, {pending}, {passing("test")}},
		headSequence:   []string{"sha0"},
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
	gh := &fakeGitHub{
		prTitle:        "demo",
		prBranch:       "b",
		prHeadSHA:      "sha0",
		checkResponses: [][]github.CheckRun{{failing(1, "test")}},
		headSequence:   []string{"sha1"},
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

func TestShortSHA(t *testing.T) {
	if got := shortSHA("abcdef0123456789"); got != "abcdef0" {
		t.Errorf("shortSHA long = %q, want abcdef0", got)
	}
	if got := shortSHA("abc"); got != "abc" {
		t.Errorf("shortSHA short = %q, want abc", got)
	}
}

func TestRun_PassesPatternsToClaude(t *testing.T) {
	// Use an in-process patterns dir so LoadFiltered returns at least one
	// pattern, proving the wiring from Options through to Claude.Fix
	// actually carries patterns into the prompt context.
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

	gh := &fakeGitHub{
		prTitle:    "demo",
		prBranch:   "feat/x",
		prHeadSHA:  "old",
		checkResponses: [][]github.CheckRun{
			{failing(1, "test")},
			{passing("test")},
		},
		headSequence: []string{"new"},
		logs:         "FAIL: TestX\n",
	}
	cl := &fakeClaude{report: "fixed"}
	r := newRunner(gh, cl, &fakePrompter{})

	opts := Options{
		PRRef:           "owner/repo#7",
		PatternDirs:     []string{patternsDir},
		NoLocalPatterns: true,
		NoRepoPatterns:  true,
	}
	if err := r.Run(io.Discard, opts); err != nil {
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
}

func TestCollectPatternDirs_IncludesExplicitDirs(t *testing.T) {
	opts := Options{
		PatternDirs:     []string{"/explicit/patterns"},
		NoLocalPatterns: true,
		NoRepoPatterns:  true,
	}
	dirs := collectPatternDirs(opts, "/tmp/repo")

	if len(dirs) != 1 || dirs[0] != "/explicit/patterns" {
		t.Errorf("dirs = %v, want only /explicit/patterns", dirs)
	}
}

func TestCollectPatternDirs_HonorsNoRepoPatterns(t *testing.T) {
	opts := Options{
		NoLocalPatterns: true,
		NoRepoPatterns:  true,
	}
	dirs := collectPatternDirs(opts, "/tmp/repo-that-does-not-exist")

	for _, d := range dirs {
		if d == "/tmp/repo-that-does-not-exist/.planwerk/review_patterns" {
			t.Error("repo patterns should be skipped when NoRepoPatterns is true")
		}
	}
}

func TestCollectPatternDirs_EmptyWhenEverythingDisabled(t *testing.T) {
	opts := Options{
		NoLocalPatterns: true,
		NoRepoPatterns:  true,
	}
	dirs := collectPatternDirs(opts, "/tmp/does-not-exist")

	if len(dirs) != 0 {
		t.Errorf("expected no pattern dirs with both flags disabled and no explicit dirs, got %v", dirs)
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
	gh := &fakeGitHub{
		prTitle:        "demo",
		prBranch:       "feat/x",
		prHeadSHA:      "sha0",
		cloneDir:       t.TempDir(),
		checkResponses: [][]github.CheckRun{failures, failures, failures},
		// Each iteration advances HEAD so the loop reaches the iteration cap
		// instead of bailing on "no new commit".
		headSequence: []string{"s1", "s2", "s3"},
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
	if gh.localCalls.Load() != 1 {
		t.Errorf("FetchAndCheckoutLocal calls = %d, want 1 (initial metadata fetch)", gh.localCalls.Load())
	}
	if gh.cloneCalls.Load() != 0 {
		t.Errorf("FetchAndCheckout (temp-dir re-clone) calls = %d, want 0 in local mode", gh.cloneCalls.Load())
	}
	if gh.pullCalls.Load() != 2 {
		t.Errorf("PullOnBranch calls = %d, want 2 (one per iteration after the first)", gh.pullCalls.Load())
	}
	// The local working tree must survive: Cleanup is a no-op when Local.
	if _, err := os.Stat(gh.cloneDir); err != nil {
		t.Fatalf("local checkout must survive the fix loop: %v", err)
	}
}

func TestRunLocalAllChecksPassingNoPull(t *testing.T) {
	gh := &fakeGitHub{
		prTitle:        "demo",
		prBranch:       "feat/x",
		prHeadSHA:      "abc1234",
		cloneDir:       t.TempDir(),
		checkResponses: [][]github.CheckRun{{passing("lint"), passing("test")}},
		headSequence:   []string{"abc1234"},
	}
	cl := &fakeClaude{}
	r := newRunner(gh, cl, &fakePrompter{})

	if err := r.Run(io.Discard, Options{PRRef: "o/r#1", Local: true}); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if gh.localCalls.Load() != 1 {
		t.Errorf("FetchAndCheckoutLocal calls = %d, want 1", gh.localCalls.Load())
	}
	if gh.cloneCalls.Load() != 0 {
		t.Errorf("FetchAndCheckout calls = %d, want 0", gh.cloneCalls.Load())
	}
	if gh.pullCalls.Load() != 0 {
		t.Errorf("PullOnBranch calls = %d, want 0 when checks already pass", gh.pullCalls.Load())
	}
}
