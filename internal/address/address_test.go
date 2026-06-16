package address

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/planwerk/planwerk-review/internal/github"
	"github.com/planwerk/planwerk-review/internal/report"
)

// Thread IDs reused across the address tests' fixtures and assertions.
const (
	threadID1 = "RT_1"
	threadID2 = "RT_2"
	threadID3 = "RT_3"
)

// fakeGitHub is a scripted GitHubClient recording every outward-facing call so
// tests can assert which threads were pushed, replied to, and resolved.
type fakeGitHub struct {
	threads    []github.ReviewThread
	threadsErr error

	fetchCalls        atomic.Int32
	localCalls        atomic.Int32
	fetchThreadsCalls atomic.Int32

	pushCalls atomic.Int32
	pushErr   error

	replyErr       error
	repliedThreads []string

	resolveErr      error
	resolvedThreads []string

	commentErr    error
	commentCalls  atomic.Int32
	commentBodies []string

	prTitle   string
	prBranch  string
	prBase    string
	prHeadSHA string
	cloneDir  string
}

func (f *fakeGitHub) FetchAndCheckout(ref string) (*github.PR, error) {
	f.fetchCalls.Add(1)
	return f.makePR(ref, false)
}

func (f *fakeGitHub) FetchAndCheckoutLocal(ref string, _ github.LocalOptions) (*github.PR, error) {
	f.localCalls.Add(1)
	return f.makePR(ref, true)
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
		BaseBranch: f.prBase,
		HeadSHA:    f.prHeadSHA,
		Dir:        f.cloneDir,
		Local:      local,
	}, nil
}

func (f *fakeGitHub) FetchReviewThreads(_, _ string, _ int) ([]github.ReviewThread, error) {
	f.fetchThreadsCalls.Add(1)
	if f.threadsErr != nil {
		return nil, f.threadsErr
	}
	return f.threads, nil
}

func (f *fakeGitHub) PushHead(_, _ string) error {
	f.pushCalls.Add(1)
	return f.pushErr
}

func (f *fakeGitHub) AddReviewThreadReply(threadID, _ string) (string, error) {
	if f.replyErr != nil {
		return "", f.replyErr
	}
	f.repliedThreads = append(f.repliedThreads, threadID)
	return "https://github.com/o/r/pull/1#discussion_r1", nil
}

func (f *fakeGitHub) ResolveReviewThread(threadID string) error {
	if f.resolveErr != nil {
		return f.resolveErr
	}
	f.resolvedThreads = append(f.resolvedThreads, threadID)
	return nil
}

func (f *fakeGitHub) AddPRComment(owner, repo string, number int, body string) (string, error) {
	f.commentCalls.Add(1)
	if f.commentErr != nil {
		return "", f.commentErr
	}
	f.commentBodies = append(f.commentBodies, body)
	return "https://github.com/o/r/pull/1#issuecomment-1", nil
}

// fakeClaude returns scripted AddressResults in sequence (the last repeats when
// exhausted) and records every Context it was handed.
type fakeClaude struct {
	called  atomic.Int32
	results []*report.AddressResult
	err     error
	ctxs    []Context
}

func (f *fakeClaude) Address(_ string, ctx Context) (*report.AddressResult, error) {
	i := int(f.called.Add(1)) - 1
	f.ctxs = append(f.ctxs, ctx)
	if f.err != nil {
		return nil, f.err
	}
	if len(f.results) == 0 {
		return &report.AddressResult{Status: "DONE"}, nil
	}
	if i >= len(f.results) {
		i = len(f.results) - 1
	}
	return f.results[i], nil
}

func sampleThreads() []github.ReviewThread {
	return []github.ReviewThread{
		{ID: threadID1, Path: "a.go", Line: 1, Comments: []github.ReviewThreadComment{{Author: "rev", Body: "fix a"}}},
		{ID: threadID2, Path: "b.go", Line: 2, Comments: []github.ReviewThreadComment{{Author: "rev", Body: "fix b"}}},
	}
}

func doneResult(threadID string) *report.AddressResult {
	return &report.AddressResult{
		Threads: []report.AddressedThread{{ThreadID: threadID, Status: "DONE", Summary: "addressed " + threadID, Files: []string{"f.go"}}},
		Summary: "addressed " + threadID,
		Status:  "DONE",
	}
}

// newRunner wires the fakes with a non-TTY default; tests that exercise the
// interactive selector override In and IsTTY.
func newRunner(gh *fakeGitHub, cl *fakeClaude) *Runner {
	return &Runner{
		Claude: cl,
		GitHub: gh,
		In:     strings.NewReader(""),
		IsTTY:  func() bool { return false },
	}
}

// baseOpts returns Options with the production defaults (--reply on, --resolve
// off, one commit per thread) that the CLI would supply.
func baseOpts(prRef string) Options {
	return Options{PRRef: prRef, Reply: true, OneCommitPerThread: true}
}

func TestRun_NoThreads(t *testing.T) {
	gh := &fakeGitHub{prBranch: "feat/x"}
	cl := &fakeClaude{}
	r := newRunner(gh, cl)

	var buf bytes.Buffer
	if err := r.Run(&buf, baseOpts("o/r#1")); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if cl.called.Load() != 0 {
		t.Errorf("Claude.Address called %d times, want 0 with no threads", cl.called.Load())
	}
	if !strings.Contains(buf.String(), "No unresolved review threads") {
		t.Errorf("missing no-threads notice: %s", buf.String())
	}
}

func TestRun_AllPerThread(t *testing.T) {
	gh := &fakeGitHub{prBranch: "feat/x", threads: sampleThreads()}
	cl := &fakeClaude{results: []*report.AddressResult{doneResult(threadID1), doneResult(threadID2)}}
	r := newRunner(gh, cl)

	opts := baseOpts("o/r#1")
	opts.All = true
	var buf bytes.Buffer
	if err := r.Run(&buf, opts); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if cl.called.Load() != 2 {
		t.Errorf("Claude.Address called %d times, want 2 (one per thread)", cl.called.Load())
	}
	if gh.pushCalls.Load() != 2 {
		t.Errorf("PushHead called %d times, want 2", gh.pushCalls.Load())
	}
	// --reply on by default, --resolve off by default.
	if len(gh.repliedThreads) != 2 {
		t.Errorf("replied to %v, want both threads", gh.repliedThreads)
	}
	if len(gh.resolvedThreads) != 0 {
		t.Errorf("resolved %v, want none without --resolve", gh.resolvedThreads)
	}
	if gh.commentCalls.Load() != 1 {
		t.Errorf("AddPRComment called %d times, want 1 aggregate report", gh.commentCalls.Load())
	}
}

func TestRun_Aggregate(t *testing.T) {
	gh := &fakeGitHub{prBranch: "feat/x", threads: sampleThreads()}
	cl := &fakeClaude{results: []*report.AddressResult{{
		Threads: []report.AddressedThread{
			{ThreadID: threadID1, Status: "DONE", Summary: "a"},
			{ThreadID: threadID2, Status: "DONE", Summary: "b"},
		},
		Summary: "both",
		Status:  "DONE",
	}}}
	r := newRunner(gh, cl)

	opts := baseOpts("o/r#1")
	opts.All = true
	opts.OneCommitPerThread = false
	if err := r.Run(io.Discard, opts); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if cl.called.Load() != 1 {
		t.Errorf("Claude.Address called %d times, want 1 in aggregate mode", cl.called.Load())
	}
	if gh.pushCalls.Load() != 1 {
		t.Errorf("PushHead called %d times, want 1 in aggregate mode", gh.pushCalls.Load())
	}
	if cl.ctxs[0].OneCommitPerThread {
		t.Error("aggregate session should get OneCommitPerThread=false")
	}
	if len(cl.ctxs[0].Threads) != 2 {
		t.Errorf("aggregate session got %d threads, want both", len(cl.ctxs[0].Threads))
	}
}

func TestRun_ThreadIDSelectsSubset(t *testing.T) {
	gh := &fakeGitHub{prBranch: "feat/x", threads: sampleThreads()}
	cl := &fakeClaude{results: []*report.AddressResult{doneResult(threadID2)}}
	r := newRunner(gh, cl)

	opts := baseOpts("o/r#1")
	opts.ThreadIDs = []string{threadID2}
	if err := r.Run(io.Discard, opts); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if cl.called.Load() != 1 {
		t.Fatalf("Claude.Address called %d times, want 1", cl.called.Load())
	}
	if cl.ctxs[0].Threads[0].ID != threadID2 {
		t.Errorf("addressed thread %q, want RT_2", cl.ctxs[0].Threads[0].ID)
	}
}

func TestRun_ThreadIDUnknownWarns(t *testing.T) {
	gh := &fakeGitHub{prBranch: "feat/x", threads: sampleThreads()}
	cl := &fakeClaude{}
	r := newRunner(gh, cl)

	opts := baseOpts("o/r#1")
	opts.ThreadIDs = []string{"RT_999"}
	var buf bytes.Buffer
	if err := r.Run(&buf, opts); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if cl.called.Load() != 0 {
		t.Errorf("Claude.Address called %d times, want 0 (no thread matched)", cl.called.Load())
	}
	if !strings.Contains(buf.String(), "did not match") {
		t.Errorf("expected an unknown-thread warning, got: %s", buf.String())
	}
	if !strings.Contains(buf.String(), "No threads selected") {
		t.Errorf("expected the no-selection notice, got: %s", buf.String())
	}
}

func TestRun_Resolve(t *testing.T) {
	gh := &fakeGitHub{prBranch: "feat/x", threads: sampleThreads()[:1]}
	cl := &fakeClaude{results: []*report.AddressResult{doneResult(threadID1)}}
	r := newRunner(gh, cl)

	opts := baseOpts("o/r#1")
	opts.All = true
	opts.Resolve = true
	if err := r.Run(io.Discard, opts); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if len(gh.resolvedThreads) != 1 || gh.resolvedThreads[0] != threadID1 {
		t.Errorf("resolved %v, want [RT_1] with --resolve", gh.resolvedThreads)
	}
}

func TestRun_ReplyResolveFailuresAreNonFatal(t *testing.T) {
	gh := &fakeGitHub{
		prBranch:   "feat/x",
		threads:    sampleThreads()[:1],
		replyErr:   errors.New("reply down"),
		resolveErr: errors.New("resolve down"),
		commentErr: errors.New("comment down"),
	}
	cl := &fakeClaude{results: []*report.AddressResult{doneResult(threadID1)}}
	r := newRunner(gh, cl)

	opts := baseOpts("o/r#1")
	opts.All = true
	opts.Resolve = true
	var buf bytes.Buffer
	if err := r.Run(&buf, opts); err != nil {
		t.Fatalf("Run returned %v, want nil despite best-effort failures", err)
	}
	out := buf.String()
	if !strings.Contains(out, "Could not reply to thread RT_1") {
		t.Errorf("expected a non-fatal reply warning, got: %s", out)
	}
	if !strings.Contains(out, "Could not resolve thread RT_1") {
		t.Errorf("expected a non-fatal resolve warning, got: %s", out)
	}
	if !strings.Contains(out, "Could not post the address report") {
		t.Errorf("expected a non-fatal comment warning, got: %s", out)
	}
}

func TestRun_NoAddressCommentSkipsComment(t *testing.T) {
	gh := &fakeGitHub{prBranch: "feat/x", threads: sampleThreads()[:1]}
	cl := &fakeClaude{results: []*report.AddressResult{doneResult(threadID1)}}
	r := newRunner(gh, cl)

	opts := baseOpts("o/r#1")
	opts.All = true
	opts.NoAddressComment = true
	if err := r.Run(io.Discard, opts); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if gh.commentCalls.Load() != 0 {
		t.Errorf("AddPRComment called %d times, want 0 with --no-address-comment", gh.commentCalls.Load())
	}
}

func TestRun_EscalationStopsAndStillPostsComment(t *testing.T) {
	gh := &fakeGitHub{prBranch: "feat/x", threads: sampleThreads()}
	blocked := &report.AddressResult{
		Threads: []report.AddressedThread{{ThreadID: threadID1, Status: "BLOCKED", Summary: "stale ref"}},
		Summary: "blocked",
		Status:  "BLOCKED",
	}
	cl := &fakeClaude{results: []*report.AddressResult{blocked}}
	r := newRunner(gh, cl)

	opts := baseOpts("o/r#1")
	opts.All = true
	err := r.Run(io.Discard, opts)
	if err == nil || !strings.Contains(err.Error(), "escalated") {
		t.Fatalf("Run err = %v, want an escalation error", err)
	}
	if cl.called.Load() != 1 {
		t.Errorf("Claude.Address called %d times, want 1 (stopped after escalation)", cl.called.Load())
	}
	// A BLOCKED thread was not committed: no push, no reply, no resolve.
	if gh.pushCalls.Load() != 0 {
		t.Errorf("PushHead called %d times, want 0 for a blocked thread", gh.pushCalls.Load())
	}
	if len(gh.repliedThreads) != 0 {
		t.Errorf("replied %v, want none for a blocked thread", gh.repliedThreads)
	}
	// The escalated report must still reach the PR.
	if gh.commentCalls.Load() != 1 {
		t.Errorf("AddPRComment called %d times, want 1 — escalated report must still post", gh.commentCalls.Load())
	}
}

func TestRun_ExhaustsMaxIterations(t *testing.T) {
	threads := []github.ReviewThread{
		{ID: threadID1, Comments: []github.ReviewThreadComment{{Body: "a"}}},
		{ID: threadID2, Comments: []github.ReviewThreadComment{{Body: "b"}}},
		{ID: threadID3, Comments: []github.ReviewThreadComment{{Body: "c"}}},
	}
	gh := &fakeGitHub{prBranch: "feat/x", threads: threads}
	cl := &fakeClaude{results: []*report.AddressResult{doneResult(threadID1), doneResult(threadID2)}}
	r := newRunner(gh, cl)

	opts := baseOpts("o/r#1")
	opts.All = true
	opts.MaxIterations = 2
	err := r.Run(io.Discard, opts)
	if !errors.Is(err, ErrMaxIterations) {
		t.Fatalf("Run err = %v, want ErrMaxIterations", err)
	}
	if cl.called.Load() != 2 {
		t.Errorf("Claude.Address called %d times, want 2 (capped)", cl.called.Load())
	}
}

func TestRun_PropagatesClaudeError(t *testing.T) {
	gh := &fakeGitHub{prBranch: "feat/x", threads: sampleThreads()[:1]}
	cl := &fakeClaude{err: errors.New("boom")}
	r := newRunner(gh, cl)

	opts := baseOpts("o/r#1")
	opts.All = true
	err := r.Run(io.Discard, opts)
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected wrapped 'boom' error, got %v", err)
	}
}

func TestRun_PushFailureIsFatal(t *testing.T) {
	gh := &fakeGitHub{prBranch: "feat/x", threads: sampleThreads()[:1], pushErr: errors.New("push rejected")}
	cl := &fakeClaude{results: []*report.AddressResult{doneResult(threadID1)}}
	r := newRunner(gh, cl)

	opts := baseOpts("o/r#1")
	opts.All = true
	err := r.Run(io.Discard, opts)
	if err == nil || !strings.Contains(err.Error(), "push rejected") {
		t.Fatalf("expected a fatal push error, got %v", err)
	}
}

func TestRun_NonTTYDefaultsToAll(t *testing.T) {
	gh := &fakeGitHub{prBranch: "feat/x", threads: sampleThreads()}
	cl := &fakeClaude{results: []*report.AddressResult{doneResult(threadID1), doneResult(threadID2)}}
	r := newRunner(gh, cl) // IsTTY returns false

	// No --all, no --thread, no TTY → defaults to addressing every thread.
	if err := r.Run(io.Discard, baseOpts("o/r#1")); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if cl.called.Load() != 2 {
		t.Errorf("Claude.Address called %d times, want 2 (no-TTY default to all)", cl.called.Load())
	}
}

func TestRun_InteractiveSelectsSubset(t *testing.T) {
	gh := &fakeGitHub{prBranch: "feat/x", threads: sampleThreads()}
	cl := &fakeClaude{results: []*report.AddressResult{doneResult(threadID2)}}
	r := newRunner(gh, cl)
	r.IsTTY = func() bool { return true }
	// Skip RT_1, address RT_2.
	r.In = strings.NewReader("n\ny\n")

	if err := r.Run(io.Discard, baseOpts("o/r#1")); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if cl.called.Load() != 1 {
		t.Fatalf("Claude.Address called %d times, want 1 (only RT_2 selected)", cl.called.Load())
	}
	if cl.ctxs[0].Threads[0].ID != threadID2 {
		t.Errorf("addressed %q, want RT_2", cl.ctxs[0].Threads[0].ID)
	}
}

func TestRun_DryRunSkipsClaude(t *testing.T) {
	gh := &fakeGitHub{prBranch: "feat/x", threads: sampleThreads()}
	cl := &fakeClaude{}
	r := newRunner(gh, cl)

	opts := baseOpts("o/r#1")
	opts.DryRun = true
	var buf bytes.Buffer
	if err := r.Run(&buf, opts); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if cl.called.Load() != 0 {
		t.Errorf("Claude.Address called %d times in dry-run, want 0", cl.called.Load())
	}
	if gh.pushCalls.Load() != 0 {
		t.Errorf("PushHead called %d times in dry-run, want 0", gh.pushCalls.Load())
	}
	out := buf.String()
	if !strings.Contains(out, "[dry-run]") || !strings.Contains(out, threadID1) {
		t.Errorf("dry-run output missing plan or threads: %s", out)
	}
}

func TestRun_PrintPromptWritesPromptAndSkipsClaude(t *testing.T) {
	gh := &fakeGitHub{prBranch: "feat/x", threads: sampleThreads()}
	cl := &fakeClaude{}
	r := newRunner(gh, cl)
	r.BuildPrompt = func(ctx Context) string {
		return "PROMPT threads=" + itoa(len(ctx.Threads)) + " first=" + ctx.Threads[0].ID
	}

	opts := baseOpts("o/r#1")
	opts.PrintPrompt = true
	var buf bytes.Buffer
	if err := r.Run(&buf, opts); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if cl.called.Load() != 0 {
		t.Errorf("Claude.Address called %d times in print-prompt mode, want 0", cl.called.Load())
	}
	if gh.pushCalls.Load() != 0 {
		t.Errorf("PushHead called %d times in print-prompt mode, want 0", gh.pushCalls.Load())
	}
	out := buf.String()
	// Per-thread mode renders the first thread's prompt.
	if !strings.Contains(out, "PROMPT threads=1 first=RT_1") {
		t.Errorf("unexpected print-prompt output: %q", out)
	}
	if !strings.HasSuffix(out, "\n") {
		t.Errorf("prompt output should end with a newline: %q", out)
	}
}

func TestRun_PrintPromptWithoutBuilderErrors(t *testing.T) {
	gh := &fakeGitHub{prBranch: "feat/x", threads: sampleThreads()}
	r := newRunner(gh, &fakeClaude{})
	// BuildPrompt left nil intentionally.
	opts := baseOpts("o/r#1")
	opts.PrintPrompt = true
	err := r.Run(io.Discard, opts)
	if err == nil || !strings.Contains(err.Error(), "prompt builder") {
		t.Fatalf("expected prompt-builder error, got %v", err)
	}
}

func TestRun_RequiresRefWithoutLocal(t *testing.T) {
	r := newRunner(&fakeGitHub{}, &fakeClaude{})
	err := r.Run(io.Discard, baseOpts(""))
	if err == nil || !strings.Contains(err.Error(), "PR reference is required") {
		t.Fatalf("expected a missing-ref error, got %v", err)
	}
}

func TestRun_LocalSkipsCloneAndSurvives(t *testing.T) {
	gh := &fakeGitHub{
		prBranch: "feat/x",
		prBase:   "main",
		cloneDir: t.TempDir(),
		threads:  sampleThreads()[:1],
	}
	cl := &fakeClaude{results: []*report.AddressResult{doneResult(threadID1)}}
	r := newRunner(gh, cl)

	opts := baseOpts("o/r#1")
	opts.All = true
	opts.Local = true
	if err := r.Run(io.Discard, opts); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if gh.localCalls.Load() != 1 {
		t.Errorf("FetchAndCheckoutLocal calls = %d, want 1", gh.localCalls.Load())
	}
	if gh.fetchCalls.Load() != 0 {
		t.Errorf("FetchAndCheckout (clone) calls = %d, want 0 in local mode", gh.fetchCalls.Load())
	}
	if !cl.ctxs[0].Local {
		t.Error("Claude context Local = false, want true in --local mode")
	}
}

func TestRun_FetchThreadsErrorIsFatal(t *testing.T) {
	gh := &fakeGitHub{prBranch: "feat/x", threadsErr: errors.New("graphql down")}
	r := newRunner(gh, &fakeClaude{})
	err := r.Run(io.Discard, baseOpts("o/r#1"))
	if err == nil || !strings.Contains(err.Error(), "graphql down") {
		t.Fatalf("expected a fatal fetch-threads error, got %v", err)
	}
}

// itoa avoids pulling strconv into the test for a single conversion.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
