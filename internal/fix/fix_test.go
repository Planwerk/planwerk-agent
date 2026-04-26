package fix

import (
	"bytes"
	"errors"
	"fmt"
	"io"
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
	cloneCalls atomic.Int32
}

func (f *fakeGitHub) FetchAndCheckout(ref string) (*github.PR, error) {
	f.cloneCalls.Add(1)
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
		Dir:        "", // empty dir → Cleanup is a no-op
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
}

func (f *fakeClaude) Fix(_ string, _ Context) (string, error) {
	f.called.Add(1)
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
