package implement

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/planwerk/planwerk-review/internal/github"
)

// fakeGitHub is a scripted GitHubClient. The test sets the canned issue and
// optional clone error; both calls record their invocation count so each
// test can assert exactly which steps the runner reached.
type fakeGitHub struct {
	issue    *github.Issue
	issueErr error

	cloneDir   string
	cloneErr   error
	getCalls   atomic.Int32
	cloneCalls atomic.Int32
}

func (f *fakeGitHub) GetIssue(owner, name string, number int) (*github.Issue, error) {
	f.getCalls.Add(1)
	if f.issueErr != nil {
		return nil, f.issueErr
	}
	iss := *f.issue
	iss.Owner = owner
	iss.Name = name
	iss.Number = number
	return &iss, nil
}

func (f *fakeGitHub) CloneRepo(ref string) (*github.Repo, error) {
	f.cloneCalls.Add(1)
	if f.cloneErr != nil {
		return nil, f.cloneErr
	}
	owner, name, err := github.ParseRepoRef(ref)
	if err != nil {
		return nil, err
	}
	return &github.Repo{Owner: owner, Name: name, Dir: f.cloneDir}, nil
}

type fakeClaude struct {
	called atomic.Int32
	dir    string
	ctx    Context
	report string
	err    error
}

func (f *fakeClaude) Implement(dir string, ctx Context) (string, error) {
	f.called.Add(1)
	f.dir = dir
	f.ctx = ctx
	return f.report, f.err
}

func sampleIssue() *github.Issue {
	return &github.Issue{
		Title: "Add foo widget",
		Body:  "## Description\n\nFoo widget does X.\n\n## Acceptance Criteria\n- foo()\n",
		URL:   "https://github.com/owner/repo/issues/42",
		State: "open",
	}
}

func newRunner(gh *fakeGitHub, cl *fakeClaude) *Runner {
	return &Runner{Claude: cl, GitHub: gh}
}

func TestRun_HappyPath(t *testing.T) {
	gh := &fakeGitHub{issue: sampleIssue(), cloneDir: "/tmp/clone"}
	cl := &fakeClaude{report: "PR opened"}
	r := newRunner(gh, cl)

	var buf bytes.Buffer
	if err := r.Run(&buf, Options{IssueRef: "owner/repo#42"}); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if cl.called.Load() != 1 {
		t.Errorf("Claude.Implement called %d times, want 1", cl.called.Load())
	}
	if cl.dir != "/tmp/clone" {
		t.Errorf("Claude got dir %q, want /tmp/clone", cl.dir)
	}
	if cl.ctx.IssueNumber != 42 || cl.ctx.RepoFullName != "owner/repo" {
		t.Errorf("Claude got ctx %+v, want #42 in owner/repo", cl.ctx)
	}
	if cl.ctx.IssueTitle != "Add foo widget" {
		t.Errorf("Claude got title %q, want %q", cl.ctx.IssueTitle, "Add foo widget")
	}
	if !strings.Contains(buf.String(), "Claude implementation report") {
		t.Errorf("missing report header: %s", buf.String())
	}
	if !strings.Contains(buf.String(), "PR opened") {
		t.Errorf("missing report body: %s", buf.String())
	}
}

func TestRun_DryRunSkipsCloneAndClaude(t *testing.T) {
	gh := &fakeGitHub{issue: sampleIssue()}
	cl := &fakeClaude{}
	r := newRunner(gh, cl)

	var buf bytes.Buffer
	if err := r.Run(&buf, Options{IssueRef: "owner/repo#42", DryRun: true}); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if gh.cloneCalls.Load() != 0 {
		t.Errorf("CloneRepo called %d times in dry-run, want 0", gh.cloneCalls.Load())
	}
	if cl.called.Load() != 0 {
		t.Errorf("Claude.Implement called %d times in dry-run, want 0", cl.called.Load())
	}
	if !strings.Contains(buf.String(), "[dry-run]") {
		t.Errorf("missing dry-run notice: %s", buf.String())
	}
}

func TestRun_PrintPromptWritesPromptAndSkipsClone(t *testing.T) {
	gh := &fakeGitHub{issue: sampleIssue()}
	cl := &fakeClaude{}
	r := newRunner(gh, cl)
	r.BuildPrompt = func(ctx Context) string {
		return fmt.Sprintf("PROMPT issue=%s#%d title=%q body-len=%d",
			ctx.RepoFullName, ctx.IssueNumber, ctx.IssueTitle, len(ctx.IssueBody))
	}

	var buf bytes.Buffer
	if err := r.Run(&buf, Options{IssueRef: "owner/repo#42", PrintPrompt: true}); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if gh.cloneCalls.Load() != 0 {
		t.Errorf("CloneRepo called %d times in print-prompt mode, want 0", gh.cloneCalls.Load())
	}
	if cl.called.Load() != 0 {
		t.Errorf("Claude.Implement called %d times in print-prompt mode, want 0", cl.called.Load())
	}
	out := buf.String()
	if !strings.HasPrefix(out, `PROMPT issue=owner/repo#42 title="Add foo widget"`) {
		t.Errorf("expected rendered prompt on stdout, got: %q", out)
	}
	if !strings.HasSuffix(out, "\n") {
		t.Errorf("prompt output should end with a newline, got: %q", out)
	}
}

func TestRun_PrintPromptWithoutBuilderErrors(t *testing.T) {
	gh := &fakeGitHub{issue: sampleIssue()}
	cl := &fakeClaude{}
	r := newRunner(gh, cl) // BuildPrompt nil

	err := r.Run(io.Discard, Options{IssueRef: "owner/repo#42", PrintPrompt: true})
	if err == nil || !strings.Contains(err.Error(), "prompt builder") {
		t.Fatalf("expected prompt-builder error, got %v", err)
	}
}

func TestRun_RejectsBadIssueRef(t *testing.T) {
	r := newRunner(&fakeGitHub{}, &fakeClaude{})
	err := r.Run(io.Discard, Options{IssueRef: "not-a-ref"})
	if err == nil || !strings.Contains(err.Error(), "parsing issue ref") {
		t.Fatalf("expected parsing error, got %v", err)
	}
}

func TestRun_PropagatesIssueFetchError(t *testing.T) {
	gh := &fakeGitHub{issueErr: errors.New("boom")}
	r := newRunner(gh, &fakeClaude{})

	err := r.Run(io.Discard, Options{IssueRef: "owner/repo#1"})
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected wrapped 'boom' error, got %v", err)
	}
}

func TestRun_PropagatesCloneError(t *testing.T) {
	gh := &fakeGitHub{issue: sampleIssue(), cloneErr: errors.New("clone failed")}
	cl := &fakeClaude{}
	r := newRunner(gh, cl)

	err := r.Run(io.Discard, Options{IssueRef: "owner/repo#1"})
	if err == nil || !strings.Contains(err.Error(), "clone failed") {
		t.Fatalf("expected wrapped clone error, got %v", err)
	}
	if cl.called.Load() != 0 {
		t.Errorf("Claude.Implement called despite clone failure")
	}
}

func TestRun_PropagatesClaudeError(t *testing.T) {
	gh := &fakeGitHub{issue: sampleIssue(), cloneDir: "/tmp/x"}
	cl := &fakeClaude{err: errors.New("kaboom")}
	r := newRunner(gh, cl)

	err := r.Run(io.Discard, Options{IssueRef: "owner/repo#1"})
	if err == nil || !strings.Contains(err.Error(), "kaboom") {
		t.Fatalf("expected wrapped 'kaboom' error, got %v", err)
	}
}

func TestPrintBarePrompt_WritesPromptForRef(t *testing.T) {
	build := func(repo string, n int) string {
		return fmt.Sprintf("BARE repo=%s issue=%d", repo, n)
	}
	var buf bytes.Buffer
	if err := PrintBarePrompt(&buf, "https://github.com/owner/repo/issues/42", build); err != nil {
		t.Fatalf("PrintBarePrompt returned %v, want nil", err)
	}
	out := buf.String()
	if !strings.HasPrefix(out, "BARE repo=owner/repo issue=42") {
		t.Errorf("expected rendered bare prompt with parsed ref, got: %q", out)
	}
	if !strings.HasSuffix(out, "\n") {
		t.Errorf("output should end with a newline, got: %q", out)
	}
}

func TestPrintBarePrompt_AcceptsShortForm(t *testing.T) {
	var got struct {
		repo string
		num  int
	}
	build := func(repo string, n int) string {
		got.repo, got.num = repo, n
		return "ok"
	}
	if err := PrintBarePrompt(io.Discard, "owner/repo#7", build); err != nil {
		t.Fatalf("PrintBarePrompt returned %v, want nil", err)
	}
	if got.repo != "owner/repo" || got.num != 7 {
		t.Errorf("builder got repo=%q issue=%d, want owner/repo / 7", got.repo, got.num)
	}
}

func TestPrintBarePrompt_RejectsBadRef(t *testing.T) {
	err := PrintBarePrompt(io.Discard, "not-a-ref", func(string, int) string { return "" })
	if err == nil || !strings.Contains(err.Error(), "parsing issue ref") {
		t.Fatalf("expected parsing error, got %v", err)
	}
}

func TestPrintBarePrompt_RequiresBuilder(t *testing.T) {
	err := PrintBarePrompt(io.Discard, "owner/repo#1", nil)
	if err == nil || !strings.Contains(err.Error(), "prompt builder") {
		t.Fatalf("expected builder-required error, got %v", err)
	}
}
