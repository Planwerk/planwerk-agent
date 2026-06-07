package implement

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

type fakeVerifier struct {
	called atomic.Int32
	result *report.ReviewResult
	err    error
}

func (f *fakeVerifier) VerifyImplementation(dir, issueTitle, issueBody string) (*report.ReviewResult, error) {
	f.called.Add(1)
	return f.result, f.err
}

func TestRun_VerifyReportsUnmetCriteria(t *testing.T) {
	gh := &fakeGitHub{issue: sampleIssue(), cloneDir: t.TempDir()}
	cl := &fakeClaude{report: "PR opened"}
	fv := &fakeVerifier{result: &report.ReviewResult{
		Findings: []report.Finding{
			{Severity: report.SeverityCritical, Title: "foo() not implemented", File: "foo.go", Problem: "no foo() in diff"},
		},
	}}
	r := newRunner(gh, cl)
	r.Verifier = fv

	var buf bytes.Buffer
	if err := r.Run(&buf, Options{IssueRef: "owner/repo#42", Verify: true}); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if fv.called.Load() != 1 {
		t.Errorf("verifier called %d times, want 1", fv.called.Load())
	}
	out := buf.String()
	if !strings.Contains(out, "unmet criterion finding") || !strings.Contains(out, "foo() not implemented") {
		t.Errorf("missing verification findings in output:\n%s", out)
	}
}

func TestRun_VerifyCleanPass(t *testing.T) {
	gh := &fakeGitHub{issue: sampleIssue(), cloneDir: t.TempDir()}
	cl := &fakeClaude{report: "PR opened"}
	fv := &fakeVerifier{result: &report.ReviewResult{}}
	r := newRunner(gh, cl)
	r.Verifier = fv

	var buf bytes.Buffer
	if err := r.Run(&buf, Options{IssueRef: "owner/repo#42", Verify: true}); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if !strings.Contains(buf.String(), "all Acceptance Criteria satisfied") {
		t.Errorf("expected clean-pass message, got:\n%s", buf.String())
	}
}

func TestRun_VerifyDisabledByDefault(t *testing.T) {
	gh := &fakeGitHub{issue: sampleIssue(), cloneDir: t.TempDir()}
	cl := &fakeClaude{report: "PR opened"}
	fv := &fakeVerifier{}
	r := newRunner(gh, cl)
	r.Verifier = fv

	if err := r.Run(&bytes.Buffer{}, Options{IssueRef: "owner/repo#42"}); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if fv.called.Load() != 0 {
		t.Errorf("verifier called %d times, want 0 when --verify is off", fv.called.Load())
	}
}

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

// barePromptRunner returns an implement.Runner whose GitHub fake clones
// into a throwaway dir so PrintBarePrompt can run detect.Technologies +
// patterns.LoadFiltered without hitting the network.
func barePromptRunner(t *testing.T) (*Runner, *fakeGitHub) {
	t.Helper()
	gh := &fakeGitHub{cloneDir: t.TempDir()}
	r := newRunner(gh, &fakeClaude{})
	return r, gh
}

func TestPrintBarePrompt_WritesPromptForRef(t *testing.T) {
	r, _ := barePromptRunner(t)
	build := func(ctx BareContext) string {
		return fmt.Sprintf("BARE repo=%s issue=%d", ctx.RepoFullName, ctx.IssueNumber)
	}
	var buf bytes.Buffer
	if err := r.PrintBarePrompt(&buf, Options{IssueRef: "https://github.com/owner/repo/issues/42"}, build); err != nil {
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
	r, _ := barePromptRunner(t)
	var got BareContext
	build := func(ctx BareContext) string {
		got = ctx
		return "ok"
	}
	if err := r.PrintBarePrompt(io.Discard, Options{IssueRef: "owner/repo#7"}, build); err != nil {
		t.Fatalf("PrintBarePrompt returned %v, want nil", err)
	}
	if got.RepoFullName != "owner/repo" || got.IssueNumber != 7 {
		t.Errorf("builder got repo=%q issue=%d, want owner/repo / 7", got.RepoFullName, got.IssueNumber)
	}
}

func TestPrintBarePrompt_RejectsBadRef(t *testing.T) {
	r, _ := barePromptRunner(t)
	err := r.PrintBarePrompt(io.Discard, Options{IssueRef: "not-a-ref"}, func(BareContext) string { return "" })
	if err == nil || !strings.Contains(err.Error(), "parsing issue ref") {
		t.Fatalf("expected parsing error, got %v", err)
	}
}

func TestPrintBarePrompt_RequiresBuilder(t *testing.T) {
	r, _ := barePromptRunner(t)
	err := r.PrintBarePrompt(io.Discard, Options{IssueRef: "owner/repo#1"}, nil)
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
		IssueRef:        "owner/repo#7",
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
	entry := got.PatternCatalog[0]
	if entry.URL != "" || entry.LocalPath != "" {
		t.Errorf("expected no URL/LocalPath for --patterns entry, got URL=%q LocalPath=%q", entry.URL, entry.LocalPath)
	}
	if entry.OriginNote == "" {
		t.Errorf("expected OriginNote for --patterns entry, got empty")
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

func TestRun_PassesPatternsToClaude(t *testing.T) {
	// Use an in-process patterns dir so LoadFiltered returns at least one
	// pattern, proving the wiring from Options through to Claude.Implement
	// actually carries patterns into the prompt context.
	patternsDir := t.TempDir()
	patternFile := patternsDir + "/sample.md"
	const patternBody = `# Review Pattern: Sample wiring check
**Review-Area**: meta
**Detection-Hint**: anything
**Severity**: WARNING

## Rule
Wired patterns must reach the implement Context.
`
	if err := os.WriteFile(patternFile, []byte(patternBody), 0o644); err != nil {
		t.Fatalf("seeding pattern file: %v", err)
	}

	gh := &fakeGitHub{issue: sampleIssue(), cloneDir: t.TempDir()}
	cl := &fakeClaude{report: "ok"}
	r := newRunner(gh, cl)

	opts := Options{
		IssueRef:        "owner/repo#42",
		PatternDirs:     []string{patternsDir},
		NoLocalPatterns: true,
		NoRepoPatterns:  true,
	}
	if err := r.Run(io.Discard, opts); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if got := len(cl.ctx.Patterns); got != 1 {
		t.Fatalf("Claude got %d patterns, want 1 (wiring broken)", got)
	}
	if cl.ctx.Patterns[0].Name != "Sample wiring check" {
		t.Errorf("Claude got pattern name %q, want %q", cl.ctx.Patterns[0].Name, "Sample wiring check")
	}
}
