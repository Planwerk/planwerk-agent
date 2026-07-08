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

	"github.com/planwerk/planwerk-agent/internal/attribution"
	"github.com/planwerk/planwerk-agent/internal/capture"
	"github.com/planwerk/planwerk-agent/internal/github"
	"github.com/planwerk/planwerk-agent/internal/patterns"
	"github.com/planwerk/planwerk-agent/internal/report"
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

type fakeAdversarialVerifier struct {
	called atomic.Int32
	base   string
	// result is returned on every call. results, when non-empty, scripts a
	// per-call sequence instead: call N (1-based) returns results[N-1], with the
	// last element repeating once the sequence is exhausted. This lets the review
	// loop tests drive iteration N (e.g. findings then a clean pass).
	result  *report.ReviewResult
	results []*report.ReviewResult
	err     error
}

func (f *fakeAdversarialVerifier) AdversarialReview(dir, baseBranch string) (*report.ReviewResult, error) {
	n := int(f.called.Add(1))
	f.base = baseBranch
	if f.err != nil {
		return nil, f.err
	}
	if len(f.results) > 0 {
		i := n - 1
		if i >= len(f.results) {
			i = len(f.results) - 1
		}
		return f.results[i], nil
	}
	return f.result, nil
}

type fakeSimplifyFinder struct {
	called atomic.Int32
	base   string
	result *report.ReviewResult
	err    error
}

func (f *fakeSimplifyFinder) SimplifyFindings(dir, baseBranch string) (*report.ReviewResult, error) {
	f.called.Add(1)
	f.base = baseBranch
	return f.result, f.err
}

type fakeSimplifyApplier struct {
	called atomic.Int32
	ctx    SimplifyApplyContext
	report string
	model  string
	err    error
}

func (f *fakeSimplifyApplier) ApplySimplifications(dir string, ctx SimplifyApplyContext) (string, string, error) {
	f.called.Add(1)
	f.ctx = ctx
	return f.report, f.model, f.err
}

type fakeReviewApplier struct {
	called atomic.Int32
	ctx    ReviewApplyContext
	report string
	model  string
	err    error
}

func (f *fakeReviewApplier) ApplyReview(dir string, ctx ReviewApplyContext) (string, string, error) {
	f.called.Add(1)
	f.ctx = ctx
	return f.report, f.model, f.err
}

type fakeCapturer struct {
	called atomic.Int32
	ctx    capture.CaptureContext
	result *capture.CaptureResult
	err    error
}

func (f *fakeCapturer) Capture(dir string, ctx capture.CaptureContext) (*capture.CaptureResult, error) {
	f.called.Add(1)
	f.ctx = ctx
	return f.result, f.err
}

// fakeCaptureWriter is an offline capture.WikiWriter: it records the clone
// target and the rendered pages handed to ApplyAdditions so the capture
// write-back tests can assert what would be pushed, without touching git.
type fakeCaptureWriter struct {
	cloneCalls atomic.Int32
	cloneRepo  string
	cloneRef   string
	applyCalls atomic.Int32
	applyFiles []patterns.WikiFile
	applyMsg   string
	applyErr   error
}

func (f *fakeCaptureWriter) Clone(repo, ref string) (string, string, func(), error) {
	f.cloneCalls.Add(1)
	f.cloneRepo, f.cloneRef = repo, ref
	return "/tmp/capture-wiki", "abc1234def", func() {}, nil
}

func (f *fakeCaptureWriter) ApplyAdditions(dir string, files []patterns.WikiFile, msg string) error {
	f.applyCalls.Add(1)
	f.applyFiles, f.applyMsg = files, msg
	return f.applyErr
}

type fakeFinalizer struct {
	called atomic.Int32
	dir    string
	ctx    FinalizeContext
	report string
	model  string
	err    error
}

func (f *fakeFinalizer) FinalizePR(dir string, ctx FinalizeContext) (string, string, error) {
	f.called.Add(1)
	f.dir = dir
	f.ctx = ctx
	return f.report, f.model, f.err
}

type fakePlanner struct {
	called atomic.Int32
	dir    string
	ctx    Context
	plan   string
	model  string
	err    error
}

func (f *fakePlanner) Plan(dir string, ctx Context) (string, string, error) {
	f.called.Add(1)
	f.dir = dir
	f.ctx = ctx
	return f.plan, f.model, f.err
}

func TestRun_PlanFeedsImplement(t *testing.T) {
	gh := &fakeGitHub{issue: sampleIssue(), cloneDir: t.TempDir()}
	cl := &fakeClaude{report: validImplReport}
	fp := &fakePlanner{plan: "## Implementation Plan (issue #42)\n\nSTATUS: PLAN_READY"}
	r := newRunner(gh, cl)
	r.Planner = fp

	var buf bytes.Buffer
	if err := r.Run(&buf, Options{IssueRef: "owner/repo#42"}); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if fp.called.Load() != 1 {
		t.Errorf("planner called %d times, want 1", fp.called.Load())
	}
	if fp.dir != gh.cloneDir {
		t.Errorf("planner ran in %q, want clone dir %q", fp.dir, gh.cloneDir)
	}
	if cl.ctx.Plan != fp.plan {
		t.Errorf("implement received Plan %q, want the planner's plan %q", cl.ctx.Plan, fp.plan)
	}
	if !strings.Contains(buf.String(), "Implementation plan:") || !strings.Contains(buf.String(), "PLAN_READY") {
		t.Errorf("plan not printed to output:\n%s", buf.String())
	}
	if gh.commentCalls.Load() != 2 {
		t.Fatalf("AddIssueComment called %d times, want 2 (plan then report)", gh.commentCalls.Load())
	}
	if !strings.Contains(gh.commentBodies[0], fp.plan) {
		t.Errorf("first posted comment %q does not contain the plan %q", gh.commentBodies[0], fp.plan)
	}
	if !strings.Contains(gh.commentBodies[0], planCommentFooter("")) {
		t.Errorf("first posted comment is missing the plan attribution footer:\n%s", gh.commentBodies[0])
	}
	if !strings.Contains(gh.commentBodies[1], cl.report) {
		t.Errorf("second posted comment %q does not contain the report %q", gh.commentBodies[1], cl.report)
	}
	if !strings.Contains(gh.commentBodies[1], reportCommentFooter("")) {
		t.Errorf("second posted comment is missing the report attribution footer:\n%s", gh.commentBodies[1])
	}
	if !strings.Contains(buf.String(), "Posted the implementation plan as a comment on issue #42") {
		t.Errorf("missing plan-comment confirmation in output:\n%s", buf.String())
	}
}

// TestRun_RelationsFeedPlanner locks that the Meta/Sub-Issue neighborhood
// fetched in Run reaches the planning session's context, so BuildPlanPrompt can
// render the Meta Issue and sibling Sub Issues.
func TestRun_RelationsFeedPlanner(t *testing.T) {
	gh := &fakeGitHub{
		issue:    sampleIssue(),
		cloneDir: t.TempDir(),
		relations: &github.IssueRelations{
			Parent:   &github.Issue{Number: 1, Title: "Meta", Body: "Meta body"},
			Siblings: []github.Issue{{Number: 7, Title: "Sibling", Body: "Sibling body", State: "open"}},
		},
	}
	cl := &fakeClaude{report: validImplReport}
	fp := &fakePlanner{plan: "## Implementation Plan (issue #42)\n\nSTATUS: PLAN_READY"}
	r := newRunner(gh, cl)
	r.Planner = fp

	var buf bytes.Buffer
	if err := r.Run(&buf, Options{IssueRef: "owner/repo#42"}); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if fp.ctx.MetaIssue == nil || fp.ctx.MetaIssue.Number != 1 {
		t.Fatalf("planner ctx.MetaIssue = %+v, want Meta Issue #1", fp.ctx.MetaIssue)
	}
	if len(fp.ctx.SiblingIssues) != 1 || fp.ctx.SiblingIssues[0].Number != 7 {
		t.Fatalf("planner ctx.SiblingIssues = %+v, want one sibling #7", fp.ctx.SiblingIssues)
	}
}

func TestRun_NoPlanCommentSkipsComment(t *testing.T) {
	gh := &fakeGitHub{issue: sampleIssue(), cloneDir: t.TempDir()}
	cl := &fakeClaude{report: validImplReport}
	fp := &fakePlanner{plan: "## Implementation Plan (issue #42)\n\nSTATUS: PLAN_READY"}
	r := newRunner(gh, cl)
	r.Planner = fp

	if err := r.Run(&bytes.Buffer{}, Options{IssueRef: "owner/repo#42", NoPlanComment: true}); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if gh.commentCalls.Load() != 1 {
		t.Fatalf("AddIssueComment called %d times, want 1 — --no-plan-comment skips only the plan, the report still posts", gh.commentCalls.Load())
	}
	if !strings.Contains(gh.commentBodies[0], reportCommentFooter("")) {
		t.Errorf("the posted comment should be the report, got:\n%s", gh.commentBodies[0])
	}
	if strings.Contains(gh.commentBodies[0], planCommentFooter("")) {
		t.Errorf("--no-plan-comment must suppress the plan comment, but it was posted:\n%s", gh.commentBodies[0])
	}
	if cl.ctx.Plan != fp.plan {
		t.Errorf("implement received Plan %q, want the planner's plan even when the comment is skipped", cl.ctx.Plan)
	}
}

func TestRun_PlanCommentFailureIsNonFatal(t *testing.T) {
	gh := &fakeGitHub{issue: sampleIssue(), cloneDir: t.TempDir(), commentErr: errors.New("github down")}
	cl := &fakeClaude{report: validImplReport}
	fp := &fakePlanner{plan: "## Implementation Plan (issue #42)\n\nSTATUS: PLAN_READY"}
	r := newRunner(gh, cl)
	r.Planner = fp

	var buf bytes.Buffer
	if err := r.Run(&buf, Options{IssueRef: "owner/repo#42"}); err != nil {
		t.Fatalf("Run returned %v, want nil despite the comment-post failure", err)
	}
	if cl.called.Load() != 1 {
		t.Errorf("implement called %d times, want 1 — a failed plan comment must not abort the run", cl.called.Load())
	}
	if !strings.Contains(buf.String(), "Could not post the implementation plan") {
		t.Errorf("expected a non-fatal warning about the failed comment post, got:\n%s", buf.String())
	}
}

func TestRun_PostsReportComment(t *testing.T) {
	gh := &fakeGitHub{issue: sampleIssue(), cloneDir: t.TempDir()}
	cl := &fakeClaude{report: validImplReport}
	r := newRunner(gh, cl)

	var buf bytes.Buffer
	if err := r.Run(&buf, Options{IssueRef: "owner/repo#42"}); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if gh.commentCalls.Load() != 1 {
		t.Fatalf("AddIssueComment called %d times, want 1", gh.commentCalls.Load())
	}
	if !strings.Contains(gh.commentBodies[0], cl.report) {
		t.Errorf("posted comment %q does not contain the report %q", gh.commentBodies[0], cl.report)
	}
	if !strings.Contains(gh.commentBodies[0], reportCommentFooter("")) {
		t.Errorf("posted comment is missing the attribution footer:\n%s", gh.commentBodies[0])
	}
	if !strings.Contains(buf.String(), "Posted the implementation report as a comment on issue #42") {
		t.Errorf("missing report-comment confirmation in output:\n%s", buf.String())
	}
}

func TestRun_ReportCommentFailureIsNonFatal(t *testing.T) {
	gh := &fakeGitHub{issue: sampleIssue(), cloneDir: t.TempDir(), commentErr: errors.New("github down")}
	cl := &fakeClaude{report: validImplReport}
	r := newRunner(gh, cl)

	var buf bytes.Buffer
	if err := r.Run(&buf, Options{IssueRef: "owner/repo#42"}); err != nil {
		t.Fatalf("Run returned %v, want nil despite the comment-post failure", err)
	}
	if cl.called.Load() != 1 {
		t.Errorf("implement called %d times, want 1", cl.called.Load())
	}
	if !strings.Contains(buf.String(), "Could not post the implementation report") {
		t.Errorf("expected a non-fatal warning about the failed report post, got:\n%s", buf.String())
	}
}

// TestRun_AbortsOnInvalidImplementReport locks the guard that a one-shot
// implement session must return a COMPLETE report (the mandated heading AND a
// terminal STATUS line). A session that yields mid-work — e.g. backgrounding its
// tests to be "notified" later, impossible in a headless run — returns prose
// with neither, and Run must treat that as a failed implementation: no report
// posted onto the issue, and no PR opened on a half-built branch.
func TestRun_AbortsOnInvalidImplementReport(t *testing.T) {
	cases := []struct {
		name   string
		report string
	}{
		{"empty", ""},
		{"prose with neither heading nor status", "Both the background go test job and the Monitor will notify me when the integration tests finish. Waiting for that result before committing Commit 2."},
		{"heading but no status line", "## Implementation Report (issue #42)\n\n### Commits\n- abc1234 wip"},
		{"status line but no heading", "Did the work.\n\nSTATUS: DONE"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gh := &fakeGitHub{issue: sampleIssue(), cloneDir: t.TempDir()}
			cl := &fakeClaude{report: tc.report}
			ff := &fakeFinalizer{report: defaultFinalizeReport}
			r := newRunner(gh, cl)
			r.Finalizer = ff

			err := r.Run(&bytes.Buffer{}, Options{IssueRef: "owner/repo#42"})
			if err == nil || !strings.Contains(err.Error(), "complete implementation report") {
				t.Fatalf("Run returned %v, want an incomplete-report error", err)
			}
			if cl.called.Load() != 1 {
				t.Errorf("implement called %d times, want 1", cl.called.Load())
			}
			if ff.called.Load() != 0 {
				t.Errorf("finalizer called %d times, want 0 — no PR on an unfinished implementation", ff.called.Load())
			}
			if gh.commentCalls.Load() != 0 {
				t.Errorf("AddIssueComment called %d times, want 0 — a non-report must not be posted as one", gh.commentCalls.Load())
			}
		})
	}
}

// TestRun_AbortsOnImplementEscalation locks that a BLOCKED / NEEDS_CONTEXT
// implementation report stops the run before the simplify/review/finalize
// passes: the session could not finish the work, so no PR is opened — but the
// report IS posted so the human who must intervene sees it on the issue.
func TestRun_AbortsOnImplementEscalation(t *testing.T) {
	for _, status := range []string{"BLOCKED", "NEEDS_CONTEXT"} {
		t.Run(status, func(t *testing.T) {
			gh := &fakeGitHub{issue: sampleIssue(), cloneDir: t.TempDir()}
			cl := &fakeClaude{report: "## Implementation Report (issue #42)\n\nSTATUS: " + status}
			ff := &fakeFinalizer{report: defaultFinalizeReport}
			r := newRunner(gh, cl)
			r.Finalizer = ff

			var buf bytes.Buffer
			err := r.Run(&buf, Options{IssueRef: "owner/repo#42"})
			if err == nil || !strings.Contains(err.Error(), status) {
				t.Fatalf("Run returned %v, want a %s escalation error", err, status)
			}
			if ff.called.Load() != 0 {
				t.Errorf("finalizer called %d times, want 0 after a %s report", ff.called.Load(), status)
			}
			if gh.commentCalls.Load() != 1 {
				t.Errorf("AddIssueComment called %d times, want 1 — an escalated report must still post", gh.commentCalls.Load())
			}
			if !strings.Contains(buf.String(), "STATUS: "+status) {
				t.Errorf("escalating report not printed for review:\n%s", buf.String())
			}
		})
	}
}

// TestRun_ResumeDetectedFeedsImplement locks that a feature branch left by an
// earlier aborted run is detected and threaded into the implement context: the
// session is told which commits are already present so it continues from there.
func TestRun_ResumeDetectedFeedsImplement(t *testing.T) {
	commits := []github.Commit{
		{SHA: "abc1234def", Subject: "Add foo scaffolding"},
		{SHA: "0011223344", Subject: "Wire foo into the CLI"},
	}
	gh := &fakeGitHub{
		issue:       sampleIssue(),
		cloneDir:    t.TempDir(),
		resumeState: &github.ResumeState{Branch: "implement/issue-42-foo", Commits: commits},
	}
	cl := &fakeClaude{report: validImplReport}
	r := newRunner(gh, cl)

	var buf bytes.Buffer
	if err := r.Run(&buf, Options{IssueRef: "owner/repo#42"}); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if gh.resumeCalls.Load() != 1 {
		t.Errorf("PrepareResume called %d times, want 1", gh.resumeCalls.Load())
	}
	if cl.ctx.Resume == nil {
		t.Fatal("implement ctx.Resume is nil, want the resumed branch/commits")
	}
	if cl.ctx.Resume.Branch != "implement/issue-42-foo" {
		t.Errorf("resume branch = %q, want implement/issue-42-foo", cl.ctx.Resume.Branch)
	}
	if len(cl.ctx.Resume.Commits) != 2 {
		t.Errorf("resume carried %d commits, want 2", len(cl.ctx.Resume.Commits))
	}
	if !strings.Contains(buf.String(), "Resuming on branch implement/issue-42-foo") {
		t.Errorf("missing resume notice in output:\n%s", buf.String())
	}
}

// TestRun_ResumeDetectionErrorIsNonFatal locks that a failure to look for a
// resumable branch never aborts the run: it degrades to a normal fresh
// implementation with ctx.Resume left nil.
func TestRun_ResumeDetectionErrorIsNonFatal(t *testing.T) {
	gh := &fakeGitHub{
		issue:     sampleIssue(),
		cloneDir:  t.TempDir(),
		resumeErr: errors.New("git down"),
	}
	cl := &fakeClaude{report: validImplReport}
	r := newRunner(gh, cl)

	if err := r.Run(&bytes.Buffer{}, Options{IssueRef: "owner/repo#42"}); err != nil {
		t.Fatalf("Run returned %v, want nil (resume detection is best-effort)", err)
	}
	if cl.ctx.Resume != nil {
		t.Errorf("ctx.Resume = %+v, want nil after a detection error", cl.ctx.Resume)
	}
}

// TestRun_NoResumeSkipsDetectionAndPush locks that --no-resume disables both the
// pre-implement resume detection and the post-abort partial-progress push.
func TestRun_NoResumeSkipsDetectionAndPush(t *testing.T) {
	gh := &fakeGitHub{
		issue:         sampleIssue(),
		cloneDir:      t.TempDir(),
		resumeState:   &github.ResumeState{Branch: "implement/issue-42-foo", Commits: []github.Commit{{SHA: "abc1234", Subject: "wip"}}},
		progressState: &github.ResumeState{Branch: "implement/issue-42-foo", Commits: []github.Commit{{SHA: "abc1234", Subject: "wip"}}},
	}
	cl := &fakeClaude{report: "prose without a report heading or status"}
	r := newRunner(gh, cl)

	err := r.Run(&bytes.Buffer{}, Options{IssueRef: "owner/repo#42", NoResume: true})
	if err == nil || !strings.Contains(err.Error(), "complete implementation report") {
		t.Fatalf("Run returned %v, want an incomplete-report error", err)
	}
	if gh.resumeCalls.Load() != 0 {
		t.Errorf("PrepareResume called %d times, want 0 under --no-resume", gh.resumeCalls.Load())
	}
	if cl.ctx.Resume != nil {
		t.Errorf("ctx.Resume = %+v, want nil under --no-resume", cl.ctx.Resume)
	}
	if gh.pushBranchCalls.Load() != 0 {
		t.Errorf("PushBranch called %d times, want 0 under --no-resume", gh.pushBranchCalls.Load())
	}
}

// TestRun_PersistsPartialProgressOnAbortInCloneMode locks that when a clone-mode
// run aborts with an incomplete report, the commits the session did make are
// pushed to origin so a later run can resume — and the abort error is still
// returned.
func TestRun_PersistsPartialProgressOnAbortInCloneMode(t *testing.T) {
	gh := &fakeGitHub{
		issue:         sampleIssue(),
		cloneDir:      t.TempDir(),
		progressState: &github.ResumeState{Branch: "implement/issue-42-foo", Commits: []github.Commit{{SHA: "abc1234", Subject: "wip"}}},
	}
	cl := &fakeClaude{report: "prose without a report heading or status"}
	r := newRunner(gh, cl)

	var buf bytes.Buffer
	err := r.Run(&buf, Options{IssueRef: "owner/repo#42"})
	if err == nil || !strings.Contains(err.Error(), "complete implementation report") {
		t.Fatalf("Run returned %v, want an incomplete-report error", err)
	}
	if gh.pushBranchCalls.Load() != 1 {
		t.Fatalf("PushBranch called %d times, want 1", gh.pushBranchCalls.Load())
	}
	if gh.pushBranchArg != "implement/issue-42-foo" {
		t.Errorf("pushed branch %q, want implement/issue-42-foo", gh.pushBranchArg)
	}
	if !strings.Contains(buf.String(), "Pushed partial progress to origin/implement/issue-42-foo") {
		t.Errorf("missing partial-progress notice in output:\n%s", buf.String())
	}
}

// TestRun_LocalAbortDoesNotPushPartialProgress locks that in --local mode the
// partial branch is left in the user's checkout rather than pushed to origin.
func TestRun_LocalAbortDoesNotPushPartialProgress(t *testing.T) {
	gh := &fakeGitHub{
		issue:         sampleIssue(),
		cloneDir:      t.TempDir(),
		progressState: &github.ResumeState{Branch: "implement/issue-42-foo", Commits: []github.Commit{{SHA: "abc1234", Subject: "wip"}}},
	}
	cl := &fakeClaude{report: "prose without a report heading or status"}
	r := newRunner(gh, cl)

	var buf bytes.Buffer
	err := r.Run(&buf, Options{IssueRef: "owner/repo#42", Local: true})
	if err == nil || !strings.Contains(err.Error(), "complete implementation report") {
		t.Fatalf("Run returned %v, want an incomplete-report error", err)
	}
	if gh.pushBranchCalls.Load() != 0 {
		t.Errorf("PushBranch called %d times, want 0 in --local mode", gh.pushBranchCalls.Load())
	}
	if !strings.Contains(buf.String(), "local branch implement/issue-42-foo") {
		t.Errorf("missing local-branch resume hint in output:\n%s", buf.String())
	}
}

func TestRun_NoReportCommentSkipsReportComment(t *testing.T) {
	gh := &fakeGitHub{issue: sampleIssue(), cloneDir: t.TempDir()}
	cl := &fakeClaude{report: validImplReport}
	r := newRunner(gh, cl)

	if err := r.Run(&bytes.Buffer{}, Options{IssueRef: "owner/repo#42", NoReportComment: true}); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if gh.commentCalls.Load() != 0 {
		t.Errorf("AddIssueComment called %d times, want 0 with --no-report-comment", gh.commentCalls.Load())
	}
	if cl.called.Load() != 1 {
		t.Errorf("implement called %d times, want 1 — the report is still produced, just not posted", cl.called.Load())
	}
}

func TestRun_NoPlanSkipsPlanner(t *testing.T) {
	gh := &fakeGitHub{issue: sampleIssue(), cloneDir: t.TempDir()}
	cl := &fakeClaude{report: validImplReport}
	fp := &fakePlanner{plan: "unused"}
	r := newRunner(gh, cl)
	r.Planner = fp

	if err := r.Run(&bytes.Buffer{}, Options{IssueRef: "owner/repo#42", NoPlan: true}); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if fp.called.Load() != 0 {
		t.Errorf("planner called %d times, want 0 when --no-plan is set", fp.called.Load())
	}
	if cl.ctx.Plan != "" {
		t.Errorf("implement received Plan %q, want empty", cl.ctx.Plan)
	}
}

func TestRun_NilPlannerImplementsDirectly(t *testing.T) {
	gh := &fakeGitHub{issue: sampleIssue(), cloneDir: t.TempDir()}
	cl := &fakeClaude{report: validImplReport}
	r := newRunner(gh, cl)

	if err := r.Run(&bytes.Buffer{}, Options{IssueRef: "owner/repo#42"}); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if cl.called.Load() != 1 {
		t.Errorf("implement called %d times, want 1", cl.called.Load())
	}
	if cl.ctx.Plan != "" {
		t.Errorf("implement received Plan %q, want empty", cl.ctx.Plan)
	}
}

func TestRun_PlanErrorAbortsBeforeImplement(t *testing.T) {
	gh := &fakeGitHub{issue: sampleIssue(), cloneDir: t.TempDir()}
	cl := &fakeClaude{report: "unused"}
	fp := &fakePlanner{err: errors.New("claude exploded")}
	r := newRunner(gh, cl)
	r.Planner = fp

	err := r.Run(&bytes.Buffer{}, Options{IssueRef: "owner/repo#42"})
	if err == nil || !strings.Contains(err.Error(), "claude plan") {
		t.Fatalf("Run returned %v, want claude plan error", err)
	}
	if cl.called.Load() != 0 {
		t.Errorf("implement called %d times, want 0 after plan failure", cl.called.Load())
	}
}

func TestRun_PlanEscalationAbortsBeforeImplement(t *testing.T) {
	for _, status := range []string{"BLOCKED", "NEEDS_CONTEXT"} {
		t.Run(status, func(t *testing.T) {
			gh := &fakeGitHub{issue: sampleIssue(), cloneDir: t.TempDir()}
			cl := &fakeClaude{report: "unused"}
			fp := &fakePlanner{plan: "## Implementation Plan (issue #42)\n\nSTATUS: " + status}
			r := newRunner(gh, cl)
			r.Planner = fp

			var buf bytes.Buffer
			err := r.Run(&buf, Options{IssueRef: "owner/repo#42"})
			if err == nil || !strings.Contains(err.Error(), status) {
				t.Fatalf("Run returned %v, want %s escalation error", err, status)
			}
			if cl.called.Load() != 0 {
				t.Errorf("implement called %d times, want 0 after %s plan", cl.called.Load(), status)
			}
			if !strings.Contains(buf.String(), "STATUS: "+status) {
				t.Errorf("escalating plan not printed for review:\n%s", buf.String())
			}
			if gh.commentCalls.Load() != 1 {
				t.Errorf("AddIssueComment called %d times, want 1 — an escalated plan must still land on the issue", gh.commentCalls.Load())
			}
		})
	}
}

func TestPlanEscalation(t *testing.T) {
	// A plan whose subject IS the BLOCKED/NEEDS_CONTEXT status values (issue
	// #89 hardens the implement session's stop conditions) mentions those
	// markers mid-sentence and inside backticks while still ending in a
	// PLAN_READY verdict. The escalation check must read the terminal verdict,
	// not any "STATUS: BLOCKED" substring in the body.
	const issue89Plan = "## Implementation Plan (issue #89)\n\n" +
		"### Change Set\n" +
		"- instruct the session to halt and emit `STATUS: DONE_WITH_CONCERNS` " +
		"or `STATUS: BLOCKED` (nothing shippable), reusing the existing " +
		"`STATUS: NEEDS_CONTEXT` path.\n\n" +
		"### Status\nSTATUS: PLAN_READY"

	cases := []struct {
		name string
		plan string
		want string
	}{
		{"ready", "## Plan\n\nSTATUS: PLAN_READY", ""},
		{"blocked", "## Plan\n\nSTATUS: BLOCKED", "BLOCKED"},
		{"needs context", "## Plan\n\nSTATUS: NEEDS_CONTEXT", "NEEDS_CONTEXT"},
		{"free-form without marker", "## Plan\n\n- do the thing", ""},
		{"issue #89 mentions markers but is ready", issue89Plan, ""},
		{"mid-sentence mention is not a verdict", "The session emits STATUS: BLOCKED when stuck.\n\nSTATUS: PLAN_READY", ""},
		{"inline-code line is not a verdict", "`STATUS: BLOCKED`\n\nSTATUS: PLAN_READY", ""},
		{"format spec line is not a verdict", "STATUS: <PLAN_READY | BLOCKED | NEEDS_CONTEXT>", ""},
		{"terminal verdict wins over earlier line", "STATUS: PLAN_READY\n\n(revised)\n\nSTATUS: BLOCKED", "BLOCKED"},
		{"verdict with trailing reason", "STATUS: NEEDS_CONTEXT — missing the auth config", "NEEDS_CONTEXT"},
		{"indented verdict still counts", "## Plan\n\n   STATUS: BLOCKED", "BLOCKED"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := planEscalation(tc.plan); got != tc.want {
				t.Errorf("planEscalation() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestImplementReportStatus(t *testing.T) {
	const header = "## Implementation Report (issue #42)\n\n"
	cases := []struct {
		name   string
		report string
		want   string
	}{
		{"done", header + "STATUS: DONE", "DONE"},
		{"done with concerns", header + "STATUS: DONE_WITH_CONCERNS", "DONE_WITH_CONCERNS"},
		{"partial", header + "STATUS: PARTIAL", "PARTIAL"},
		{"blocked", header + "STATUS: BLOCKED", "BLOCKED"},
		{"needs context", header + "STATUS: NEEDS_CONTEXT", "NEEDS_CONTEXT"},
		{"no status line", header + "### Commits\n- abc1234 wip", ""},
		{"empty", "", ""},
		{"yielded mid-work blurb", "Waiting for the background test job before committing Commit 2.", ""},
		{"bold decoration", "**STATUS: DONE**", "DONE"},
		{"list marker", "- STATUS: BLOCKED", "BLOCKED"},
		{"trailing reason after verdict", "STATUS: DONE_WITH_CONCERNS — flaky test left skipped", "DONE_WITH_CONCERNS"},
		{"terminal verdict wins over earlier line", "STATUS: BLOCKED\n\n(revised)\n\nSTATUS: DONE", "DONE"},
		{"unrecognized value ignored", "STATUS: MAYBE", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := implementReportStatus(tc.report); got != tc.want {
				t.Errorf("implementReportStatus() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestStripPlanCommentFooter(t *testing.T) {
	const plan = "## Implementation Plan (issue #42)\n\n### Summary\n- do the thing\n\nSTATUS: PLAN_READY"
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"round-trips a formatted plan comment", formatPlanComment(plan, ""), plan},
		{"no footer returned trimmed", "  " + plan + "  ", plan},
		{"trailing separator and whitespace stripped", plan + "\n\n---\n\n" + planCommentFooter("") + "\n\n", plan},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := stripPlanCommentFooter(tc.in); got != tc.want {
				t.Errorf("stripPlanCommentFooter() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestMostRecentPlanComment(t *testing.T) {
	const planA = "## Implementation Plan (issue #42)\n\nfirst plan\n\nSTATUS: PLAN_READY"
	const planB = "## Implementation Plan (issue #42)\n\nsecond plan\n\nSTATUS: PLAN_READY"
	reportComment := "## Implementation Report (issue #42)\n\ndone\n\n---\n\n" + reportCommentFooter("") + "\n"

	cases := []struct {
		name string
		in   []github.IssueComment
		want string
	}{
		{
			name: "picks the last comment carrying both markers",
			in: []github.IssueComment{
				{Body: formatPlanComment(planA, "")},
				{Body: formatPlanComment(planB, "")},
			},
			want: planB,
		},
		{
			name: "ignores a plan heading without the footer",
			in:   []github.IssueComment{{Body: planA}},
			want: "",
		},
		{
			name: "ignores the footer without the plan heading",
			in:   []github.IssueComment{{Body: "random text\n\n---\n\n" + planCommentFooter("") + "\n"}},
			want: "",
		},
		{
			name: "ignores a report comment",
			in:   []github.IssueComment{{Body: reportComment}},
			want: "",
		},
		{
			name: "skips a later report to find the earlier plan",
			in: []github.IssueComment{
				{Body: formatPlanComment(planA, "")},
				{Body: reportComment},
			},
			want: planA,
		},
		{
			name: "no comments yields empty",
			in:   nil,
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := mostRecentPlanComment(tc.in); got != tc.want {
				t.Errorf("mostRecentPlanComment() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestMostRecentPlanComment_SurvivesModelChange locks in the reason the
// detection keys on the model-independent planCommentMarker rather than the full
// footer: a plan posted under one model id must still be found and stripped when
// a later run resolves a different model.
func TestMostRecentPlanComment_SurvivesModelChange(t *testing.T) {
	const plan = "## Implementation Plan (issue #42)\n\nplanned under another model\n\nSTATUS: PLAN_READY"
	// A plan comment an earlier run posted under a different model id.
	postedUnderFable := plan + "\n\n---\n\n_Implementation plan generated by " +
		attribution.Link + " implement with Claude:claude-fable-5_\n"

	// Detection keys on the model-independent marker, so a plan posted under one
	// model id is still found and stripped regardless of the current run's model.
	got := mostRecentPlanComment([]github.IssueComment{{Body: postedUnderFable}})
	if got != plan {
		t.Errorf("mostRecentPlanComment() = %q, want the plan stripped of its footer %q", got, plan)
	}
	if stripped := stripPlanCommentFooter(postedUnderFable); stripped != plan {
		t.Errorf("stripPlanCommentFooter() = %q, want %q", stripped, plan)
	}
}

func TestRun_ReusesExistingPlan(t *testing.T) {
	const plan = "## Implementation Plan (issue #42)\n\nreuse me\n\nSTATUS: PLAN_READY"
	gh := &fakeGitHub{
		issue:    sampleIssue(),
		cloneDir: t.TempDir(),
		comments: []github.IssueComment{{Body: formatPlanComment(plan, "")}},
	}
	cl := &fakeClaude{report: validImplReport}
	fp := &fakePlanner{plan: "FRESH PLAN should not be used"}
	r := newRunner(gh, cl)
	r.Planner = fp

	var buf bytes.Buffer
	if err := r.Run(&buf, Options{IssueRef: "owner/repo#42"}); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if fp.called.Load() != 0 {
		t.Errorf("planner called %d times, want 0 — a posted plan should be reused", fp.called.Load())
	}
	if gh.listCalls.Load() != 1 {
		t.Errorf("ListIssueComments called %d times, want 1", gh.listCalls.Load())
	}
	if cl.ctx.Plan != plan {
		t.Errorf("implement received Plan %q, want the reused plan %q", cl.ctx.Plan, plan)
	}
	if !strings.Contains(buf.String(), "Reusing the implementation plan already posted on issue #42") {
		t.Errorf("missing plan-reuse notice in output:\n%s", buf.String())
	}
	if gh.commentCalls.Load() != 1 {
		t.Fatalf("AddIssueComment called %d times, want 1 — only the report, no duplicate plan", gh.commentCalls.Load())
	}
	if strings.Contains(gh.commentBodies[0], planCommentFooter("")) {
		t.Errorf("reuse must not re-post the plan, but a plan comment was posted:\n%s", gh.commentBodies[0])
	}
	if !strings.Contains(gh.commentBodies[0], reportCommentFooter("")) {
		t.Errorf("the single posted comment should be the report, got:\n%s", gh.commentBodies[0])
	}
}

func TestRun_NoPlanReuseForcesFreshPlan(t *testing.T) {
	const posted = "## Implementation Plan (issue #42)\n\nstale\n\nSTATUS: PLAN_READY"
	gh := &fakeGitHub{
		issue:    sampleIssue(),
		cloneDir: t.TempDir(),
		comments: []github.IssueComment{{Body: formatPlanComment(posted, "")}},
	}
	cl := &fakeClaude{report: validImplReport}
	fp := &fakePlanner{plan: "## Implementation Plan (issue #42)\n\nfresh\n\nSTATUS: PLAN_READY"}
	r := newRunner(gh, cl)
	r.Planner = fp

	if err := r.Run(&bytes.Buffer{}, Options{IssueRef: "owner/repo#42", NoPlanReuse: true}); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if fp.called.Load() != 1 {
		t.Errorf("planner called %d times, want 1 — --no-plan-reuse forces a fresh session", fp.called.Load())
	}
	if gh.listCalls.Load() != 0 {
		t.Errorf("ListIssueComments called %d times, want 0 — --no-plan-reuse skips the lookup", gh.listCalls.Load())
	}
	if cl.ctx.Plan != fp.plan {
		t.Errorf("implement received Plan %q, want the fresh plan %q", cl.ctx.Plan, fp.plan)
	}
	if gh.commentCalls.Load() != 2 {
		t.Errorf("AddIssueComment called %d times, want 2 (fresh plan then report)", gh.commentCalls.Load())
	}
}

func TestRun_ReusedPlanEscalationAborts(t *testing.T) {
	for _, status := range []string{"BLOCKED", "NEEDS_CONTEXT"} {
		t.Run(status, func(t *testing.T) {
			plan := "## Implementation Plan (issue #42)\n\nnope\n\nSTATUS: " + status
			gh := &fakeGitHub{
				issue:    sampleIssue(),
				cloneDir: t.TempDir(),
				comments: []github.IssueComment{{Body: formatPlanComment(plan, "")}},
			}
			cl := &fakeClaude{report: "unused"}
			fp := &fakePlanner{plan: "unused"}
			r := newRunner(gh, cl)
			r.Planner = fp

			var buf bytes.Buffer
			err := r.Run(&buf, Options{IssueRef: "owner/repo#42"})
			if err == nil || !strings.Contains(err.Error(), status) {
				t.Fatalf("Run returned %v, want %s escalation error", err, status)
			}
			if fp.called.Load() != 0 {
				t.Errorf("planner called %d times, want 0 — reuse short-circuits planning", fp.called.Load())
			}
			if cl.called.Load() != 0 {
				t.Errorf("implement called %d times, want 0 after a reused %s plan", cl.called.Load(), status)
			}
			if !strings.Contains(buf.String(), "STATUS: "+status) {
				t.Errorf("reused escalating plan not printed for review:\n%s", buf.String())
			}
			if gh.commentCalls.Load() != 0 {
				t.Errorf("AddIssueComment called %d times, want 0 — the plan is already on the issue", gh.commentCalls.Load())
			}
		})
	}
}

func TestRun_NoPlanBeatsReuse(t *testing.T) {
	const posted = "## Implementation Plan (issue #42)\n\nreusable\n\nSTATUS: PLAN_READY"
	gh := &fakeGitHub{
		issue:    sampleIssue(),
		cloneDir: t.TempDir(),
		comments: []github.IssueComment{{Body: formatPlanComment(posted, "")}},
	}
	cl := &fakeClaude{report: validImplReport}
	fp := &fakePlanner{plan: "unused"}
	r := newRunner(gh, cl)
	r.Planner = fp

	if err := r.Run(&bytes.Buffer{}, Options{IssueRef: "owner/repo#42", NoPlan: true}); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if fp.called.Load() != 0 {
		t.Errorf("planner called %d times, want 0 with --no-plan", fp.called.Load())
	}
	if gh.listCalls.Load() != 0 {
		t.Errorf("ListIssueComments called %d times, want 0 — --no-plan short-circuits reuse", gh.listCalls.Load())
	}
	if cl.ctx.Plan != "" {
		t.Errorf("implement received Plan %q, want empty — --no-plan implements without a plan", cl.ctx.Plan)
	}
	if cl.called.Load() != 1 {
		t.Errorf("implement called %d times, want 1", cl.called.Load())
	}
}

func TestRun_ReuseCommentFetchFailureAborts(t *testing.T) {
	gh := &fakeGitHub{
		issue:       sampleIssue(),
		cloneDir:    t.TempDir(),
		commentsErr: errors.New("github down"),
	}
	cl := &fakeClaude{report: "unused"}
	fp := &fakePlanner{plan: "unused"}
	r := newRunner(gh, cl)
	r.Planner = fp

	err := r.Run(&bytes.Buffer{}, Options{IssueRef: "owner/repo#42"})
	if err == nil || !strings.Contains(err.Error(), "reading issue comments") {
		t.Fatalf("Run returned %v, want a comment-fetch error", err)
	}
	if fp.called.Load() != 0 {
		t.Errorf("planner called %d times, want 0 — the fetch failure aborts before planning", fp.called.Load())
	}
	if cl.called.Load() != 0 {
		t.Errorf("implement called %d times, want 0 after the abort", cl.called.Load())
	}
}

func TestRun_PrintPlanPrompt(t *testing.T) {
	gh := &fakeGitHub{issue: sampleIssue(), cloneDir: t.TempDir()}
	cl := &fakeClaude{report: "unused"}
	fp := &fakePlanner{plan: "unused"}
	r := newRunner(gh, cl)
	r.Planner = fp
	r.BuildPlanPrompt = func(ctx Context) string {
		return fmt.Sprintf("PLAN PROMPT for #%d: %s", ctx.IssueNumber, ctx.IssueTitle)
	}

	var buf bytes.Buffer
	if err := r.Run(&buf, Options{IssueRef: "owner/repo#42", PrintPlanPrompt: true}); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if !strings.Contains(buf.String(), "PLAN PROMPT for #42") {
		t.Errorf("plan prompt not rendered:\n%s", buf.String())
	}
	if gh.cloneCalls.Load() != 0 {
		t.Errorf("clone called %d times, want 0 in --print-plan-prompt mode", gh.cloneCalls.Load())
	}
	if fp.called.Load() != 0 || cl.called.Load() != 0 {
		t.Errorf("planner/implement called (%d/%d), want 0/0 in --print-plan-prompt mode",
			fp.called.Load(), cl.called.Load())
	}
}

func TestRun_PrintPlanPromptRequiresBuilder(t *testing.T) {
	r := newRunner(&fakeGitHub{issue: sampleIssue()}, &fakeClaude{})

	err := r.Run(&bytes.Buffer{}, Options{IssueRef: "owner/repo#42", PrintPlanPrompt: true})
	if err == nil || !strings.Contains(err.Error(), "--print-plan-prompt requires a plan prompt builder") {
		t.Fatalf("Run returned %v, want missing plan prompt builder error", err)
	}
}

func TestRun_VerifyReportsUnmetCriteria(t *testing.T) {
	gh := &fakeGitHub{issue: sampleIssue(), cloneDir: t.TempDir()}
	cl := &fakeClaude{report: validImplReport}
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
	cl := &fakeClaude{report: validImplReport}
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
	cl := &fakeClaude{report: validImplReport}
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

func TestRun_VerifyAdversarialReportsFindings(t *testing.T) {
	gh := &fakeGitHub{issue: sampleIssue(), cloneDir: t.TempDir()}
	cl := &fakeClaude{report: validImplReport}
	av := &fakeAdversarialVerifier{result: &report.ReviewResult{
		Findings: []report.Finding{
			{Severity: report.SeverityCritical, Title: "SQL injection in new query", File: "db.go", Problem: "unescaped user input"},
		},
	}}
	r := newRunner(gh, cl)
	r.AdversarialVerifier = av

	var buf bytes.Buffer
	if err := r.Run(&buf, Options{IssueRef: "owner/repo#42", VerifyAdversarial: true}); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if av.called.Load() != 1 {
		t.Errorf("adversarial verifier called %d times, want 1", av.called.Load())
	}
	if av.base != "" {
		t.Errorf("adversarial verifier got base %q, want empty so it falls back to the default branch", av.base)
	}
	out := buf.String()
	if !strings.Contains(out, "red-teaming the produced diff") || !strings.Contains(out, "SQL injection in new query") {
		t.Errorf("missing adversarial findings in output:\n%s", out)
	}
}

func TestRun_VerifyAdversarialCleanPass(t *testing.T) {
	gh := &fakeGitHub{issue: sampleIssue(), cloneDir: t.TempDir()}
	cl := &fakeClaude{report: validImplReport}
	av := &fakeAdversarialVerifier{result: &report.ReviewResult{}}
	r := newRunner(gh, cl)
	r.AdversarialVerifier = av

	var buf bytes.Buffer
	if err := r.Run(&buf, Options{IssueRef: "owner/repo#42", VerifyAdversarial: true}); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if !strings.Contains(buf.String(), "no introduced bugs found") {
		t.Errorf("expected adversarial clean-pass message, got:\n%s", buf.String())
	}
}

func TestRun_VerifyAdversarialDisabledByDefault(t *testing.T) {
	gh := &fakeGitHub{issue: sampleIssue(), cloneDir: t.TempDir()}
	cl := &fakeClaude{report: validImplReport}
	av := &fakeAdversarialVerifier{}
	r := newRunner(gh, cl)
	r.AdversarialVerifier = av

	if err := r.Run(&bytes.Buffer{}, Options{IssueRef: "owner/repo#42"}); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if av.called.Load() != 0 {
		t.Errorf("adversarial verifier called %d times, want 0 when --verify-adversarial is off", av.called.Load())
	}
}

// oneCriterionFinding is the acceptance-criteria gap the --verify pass surfaces:
// a criterion the diff does not satisfy. The review-feedback tests thread it
// through to the applier.
func oneCriterionFinding() *report.ReviewResult {
	return &report.ReviewResult{Findings: []report.Finding{
		{Severity: report.SeverityCritical, Title: "foo() not implemented", File: "foo.go", Problem: "no foo() in diff"},
	}}
}

// verifyApplyRunner wires the acceptance-criteria verifier and a review applier
// onto a fresh Runner. The adversarial finder is left nil, so the default-on
// review-and-fix pass stays disabled and only the --verify feedback path touches
// the applier — isolating Part A of the loop-closing work.
func verifyApplyRunner(gh *fakeGitHub, cl *fakeClaude, fv *fakeVerifier, ra *fakeReviewApplier) *Runner {
	ensureValidReport(cl)
	r := newRunner(gh, cl)
	r.Verifier = fv
	r.ReviewApplier = ra
	return r
}

// TestRun_VerifyFeedsUnmetCriteriaToApplier proves the --verify pass no longer
// just renders: when it finds unmet criteria and an applier is wired, exactly
// those findings are fed into the same ReviewApplier the review pass uses, and
// the apply report is printed — while the render output still appears.
func TestRun_VerifyFeedsUnmetCriteriaToApplier(t *testing.T) {
	gh := &fakeGitHub{
		issue:     sampleIssue(),
		cloneDir:  t.TempDir(),
		branchRef: &github.BranchRef{BaseBranch: "main", HeadBranch: "feat/x"},
	}
	cl := &fakeClaude{report: validImplReport}
	fv := &fakeVerifier{result: oneCriterionFinding()}
	ra := &fakeReviewApplier{report: "## Review Report\n\nSTATUS: DONE"}
	r := verifyApplyRunner(gh, cl, fv, ra)

	var buf bytes.Buffer
	if err := r.Run(&buf, Options{IssueRef: "owner/repo#42", Verify: true, NoReportComment: true}); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if fv.called.Load() != 1 {
		t.Errorf("verifier called %d times, want 1", fv.called.Load())
	}
	if ra.called.Load() != 1 {
		t.Fatalf("applier called %d times, want 1 — the unmet-criteria findings feed the applier", ra.called.Load())
	}
	if len(ra.ctx.Findings) != 1 || ra.ctx.Findings[0].File != "foo.go" {
		t.Errorf("applier got findings %+v, want exactly the verifier's unmet-criteria finding", ra.ctx.Findings)
	}
	if ra.ctx.BaseBranch != reviewTestBase {
		t.Errorf("applier got base %q, want main threaded from CurrentBranchRef", ra.ctx.BaseBranch)
	}
	out := buf.String()
	if !strings.Contains(out, "unmet criterion finding") {
		t.Errorf("the render output must still appear alongside the apply:\n%s", out)
	}
	if !strings.Contains(out, "Verification fixes report:") {
		t.Errorf("missing the apply report in output:\n%s", out)
	}
}

// TestRun_VerifyCleanPassSkipsApplier proves a clean --verify pass (no unmet
// criteria) never touches the applier even when one is wired — the render-only
// no-op path.
func TestRun_VerifyCleanPassSkipsApplier(t *testing.T) {
	gh := &fakeGitHub{
		issue:     sampleIssue(),
		cloneDir:  t.TempDir(),
		branchRef: &github.BranchRef{BaseBranch: "main", HeadBranch: "feat/x"},
	}
	cl := &fakeClaude{report: validImplReport}
	fv := &fakeVerifier{result: &report.ReviewResult{}}
	ra := &fakeReviewApplier{report: "unused"}
	r := verifyApplyRunner(gh, cl, fv, ra)

	var buf bytes.Buffer
	if err := r.Run(&buf, Options{IssueRef: "owner/repo#42", Verify: true, NoReportComment: true}); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if ra.called.Load() != 0 {
		t.Errorf("applier called %d times, want 0 — a clean verify pass applies nothing", ra.called.Load())
	}
	if !strings.Contains(buf.String(), "all Acceptance Criteria satisfied") {
		t.Errorf("expected the clean-pass message, got:\n%s", buf.String())
	}
}

// TestRun_VerifyApplyErrorIsNonFatal proves a failure applying the verification
// findings is surfaced but never aborts the run — the PR still opens.
func TestRun_VerifyApplyErrorIsNonFatal(t *testing.T) {
	gh := &fakeGitHub{
		issue:     sampleIssue(),
		cloneDir:  t.TempDir(),
		branchRef: &github.BranchRef{BaseBranch: "main", HeadBranch: "feat/x"},
	}
	cl := &fakeClaude{report: validImplReport}
	fv := &fakeVerifier{result: oneCriterionFinding()}
	ra := &fakeReviewApplier{err: errors.New("apply exploded")}
	r := verifyApplyRunner(gh, cl, fv, ra)
	ff := &fakeFinalizer{report: defaultFinalizeReport}
	r.Finalizer = ff

	var buf bytes.Buffer
	if err := r.Run(&buf, Options{IssueRef: "owner/repo#42", Verify: true, NoReportComment: true}); err != nil {
		t.Fatalf("Run returned %v, want nil despite the apply error", err)
	}
	if ra.called.Load() != 1 {
		t.Errorf("applier called %d times, want 1", ra.called.Load())
	}
	if ff.called.Load() != 1 {
		t.Errorf("finalizer called %d times, want 1 — a verify-apply error must not block the PR", ff.called.Load())
	}
	if !strings.Contains(buf.String(), "Verification fixes could not run") {
		t.Errorf("expected a non-fatal warning about the apply failure, got:\n%s", buf.String())
	}
}

// TestRun_VerifyApplyBranchRefErrorSkips proves the verify-feedback apply degrades
// cleanly when the base branch cannot be resolved: it renders, notes the skip,
// and never calls the applier.
func TestRun_VerifyApplyBranchRefErrorSkips(t *testing.T) {
	gh := &fakeGitHub{issue: sampleIssue(), cloneDir: t.TempDir(), branchRefErr: errors.New("no origin/HEAD")}
	cl := &fakeClaude{report: validImplReport}
	fv := &fakeVerifier{result: oneCriterionFinding()}
	ra := &fakeReviewApplier{report: "unused"}
	r := verifyApplyRunner(gh, cl, fv, ra)

	var buf bytes.Buffer
	if err := r.Run(&buf, Options{IssueRef: "owner/repo#42", Verify: true, NoReportComment: true}); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if ra.called.Load() != 0 {
		t.Errorf("applier called %d times, want 0 when the base branch cannot be resolved", ra.called.Load())
	}
	if !strings.Contains(buf.String(), "could not resolve the base branch") {
		t.Errorf("missing the branch-resolution skip note in output:\n%s", buf.String())
	}
}

// TestRun_VerifyRenderOnlyWithoutApplier proves the pre-loop behavior is intact:
// with no applier wired, --verify renders its findings and never attempts a fix.
func TestRun_VerifyRenderOnlyWithoutApplier(t *testing.T) {
	gh := &fakeGitHub{
		issue:     sampleIssue(),
		cloneDir:  t.TempDir(),
		branchRef: &github.BranchRef{BaseBranch: "main", HeadBranch: "feat/x"},
	}
	cl := &fakeClaude{report: validImplReport}
	fv := &fakeVerifier{result: oneCriterionFinding()}
	r := newRunner(gh, cl)
	r.Verifier = fv // no ReviewApplier wired

	var buf bytes.Buffer
	if err := r.Run(&buf, Options{IssueRef: "owner/repo#42", Verify: true, NoReportComment: true}); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	out := buf.String()
	if !strings.Contains(out, "unmet criterion finding") || !strings.Contains(out, "foo() not implemented") {
		t.Errorf("render-only path must still show the findings:\n%s", out)
	}
	if strings.Contains(out, "Verification fixes report:") {
		t.Errorf("render-only path must not run any apply:\n%s", out)
	}
}

// ensureValidReport gives a pass test's implement fake a well-formed report
// when it has none, so Run's implement guard (which aborts on a missing or
// incomplete report) does not short-circuit the simplify/review pass the test
// actually exercises. Tests that assert on the implement report itself set
// cl.report explicitly and keep it.
func ensureValidReport(cl *fakeClaude) {
	if cl.report == "" {
		cl.report = validImplReport
	}
}

// simplifyRunner wires the simplify deps onto a fresh Runner alongside the
// shared GitHub/Claude fakes, so each simplify test reads as just its
// finder/applier setup.
func simplifyRunner(gh *fakeGitHub, cl *fakeClaude, sf *fakeSimplifyFinder, sa *fakeSimplifyApplier) *Runner {
	ensureValidReport(cl)
	r := newRunner(gh, cl)
	r.Simplifier = sf
	r.SimplifyApplier = sa
	return r
}

func oneSimplifyFinding(file string) *report.ReviewResult {
	return &report.ReviewResult{Findings: []report.Finding{
		{Severity: report.SeverityWarning, Title: "Single-impl interface", File: file, Problem: "over-engineered", Action: "drop it"},
	}}
}

func TestRun_SimplifyDefaultOnAppliesFindings(t *testing.T) {
	gh := &fakeGitHub{
		issue:     sampleIssue(),
		cloneDir:  t.TempDir(),
		branchRef: &github.BranchRef{BaseBranch: "main", HeadBranch: "feat/x"},
	}
	cl := &fakeClaude{} // helper fills a valid report; --no-report-comment keeps the only comment the simplify report
	sf := &fakeSimplifyFinder{result: oneSimplifyFinding("internal/foo/foo.go")}
	sa := &fakeSimplifyApplier{report: "## Simplification Report\n\nSTATUS: DONE"}
	r := simplifyRunner(gh, cl, sf, sa)

	var buf bytes.Buffer
	if err := r.Run(&buf, Options{IssueRef: "owner/repo#42", NoReportComment: true}); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if sf.called.Load() != 1 {
		t.Errorf("finder called %d times, want 1 — the simplify pass is on by default", sf.called.Load())
	}
	if sf.base != "main" {
		t.Errorf("finder got base %q, want main from CurrentBranchRef", sf.base)
	}
	if sa.called.Load() != 1 {
		t.Fatalf("applier called %d times, want 1", sa.called.Load())
	}
	if sa.ctx.BaseBranch != "main" {
		t.Errorf("applier got ctx %+v, want base main threaded from CurrentBranchRef", sa.ctx)
	}
	if len(sa.ctx.Findings) != 1 || sa.ctx.Findings[0].File != "internal/foo/foo.go" {
		t.Errorf("applier got findings %+v, want the single kept finding", sa.ctx.Findings)
	}
	if gh.commentCalls.Load() != 1 {
		t.Fatalf("AddIssueComment called %d times, want 1 (the simplify report)", gh.commentCalls.Load())
	}
	if !strings.Contains(gh.commentBodies[0], "## Simplification Report") {
		t.Errorf("issue comment does not carry the report:\n%s", gh.commentBodies[0])
	}
	if !strings.Contains(gh.commentBodies[0], simplifyCommentFooter("")) {
		t.Errorf("issue comment is missing the attribution footer:\n%s", gh.commentBodies[0])
	}
	if !strings.Contains(buf.String(), "Posted the simplification report as a comment on issue #42") {
		t.Errorf("missing simplify-comment confirmation in output:\n%s", buf.String())
	}
}

func TestRun_NoSimplifySkipsPass(t *testing.T) {
	gh := &fakeGitHub{
		issue:     sampleIssue(),
		cloneDir:  t.TempDir(),
		branchRef: &github.BranchRef{BaseBranch: "main", HeadBranch: "feat/x"},
	}
	cl := &fakeClaude{}
	sf := &fakeSimplifyFinder{result: oneSimplifyFinding("internal/foo/foo.go")}
	sa := &fakeSimplifyApplier{report: "## Simplification Report\n\nSTATUS: DONE"}
	r := simplifyRunner(gh, cl, sf, sa)

	if err := r.Run(&bytes.Buffer{}, Options{IssueRef: "owner/repo#42", NoSimplify: true, NoReportComment: true}); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if sf.called.Load() != 0 || sa.called.Load() != 0 {
		t.Errorf("finder/applier called (%d/%d), want 0/0 with --no-simplify", sf.called.Load(), sa.called.Load())
	}
	if gh.commentCalls.Load() != 0 {
		t.Errorf("AddIssueComment called %d times, want 0 with --no-simplify (--no-report-comment suppresses the implement report)", gh.commentCalls.Load())
	}
}

func TestRun_SimplifyNoFindingsNoOp(t *testing.T) {
	gh := &fakeGitHub{
		issue:     sampleIssue(),
		cloneDir:  t.TempDir(),
		branchRef: &github.BranchRef{BaseBranch: "main", HeadBranch: "feat/x"},
	}
	cl := &fakeClaude{}
	sf := &fakeSimplifyFinder{result: &report.ReviewResult{}}
	sa := &fakeSimplifyApplier{report: "unused"}
	r := simplifyRunner(gh, cl, sf, sa)

	var buf bytes.Buffer
	if err := r.Run(&buf, Options{IssueRef: "owner/repo#42", NoReportComment: true}); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if sf.called.Load() != 1 {
		t.Errorf("finder called %d times, want 1", sf.called.Load())
	}
	if sa.called.Load() != 0 {
		t.Errorf("applier called %d times, want 0 — no findings is a clean no-op", sa.called.Load())
	}
	if gh.commentCalls.Load() != 0 {
		t.Errorf("AddIssueComment called %d times, want 0 — no findings posts no issue comment", gh.commentCalls.Load())
	}
	if !strings.Contains(buf.String(), "Nothing to simplify.") {
		t.Errorf("missing the no-op note in output:\n%s", buf.String())
	}
}

func TestRun_SimplifyGuardrailRejectsTestFinding(t *testing.T) {
	gh := &fakeGitHub{
		issue:     sampleIssue(),
		cloneDir:  t.TempDir(),
		branchRef: &github.BranchRef{BaseBranch: "main", HeadBranch: "feat/x"},
	}
	cl := &fakeClaude{}
	sf := &fakeSimplifyFinder{result: &report.ReviewResult{Findings: []report.Finding{
		{Severity: report.SeverityWarning, Title: "Redundant assertion", File: "internal/foo/foo_test.go", Problem: "x", Action: "drop"},
		{Severity: report.SeverityWarning, Title: "Single-impl interface", File: "internal/foo/foo.go", Problem: "y", Action: "drop"},
	}}}
	sa := &fakeSimplifyApplier{report: "## Simplification Report\n\nSTATUS: DONE"}
	r := simplifyRunner(gh, cl, sf, sa)

	var buf bytes.Buffer
	if err := r.Run(&buf, Options{IssueRef: "owner/repo#42"}); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if sa.called.Load() != 1 {
		t.Fatalf("applier called %d times, want 1", sa.called.Load())
	}
	if len(sa.ctx.Findings) != 1 || sa.ctx.Findings[0].File != "internal/foo/foo.go" {
		t.Errorf("applier got findings %+v, want only the non-test finding — the test-file finding must be rejected", sa.ctx.Findings)
	}
	if !strings.Contains(buf.String(), "rejected 1 finding") {
		t.Errorf("missing the guardrail-rejection note in output:\n%s", buf.String())
	}
}

func TestRun_SimplifyEscalationStops(t *testing.T) {
	for _, status := range []string{"BLOCKED", "NEEDS_CONTEXT"} {
		t.Run(status, func(t *testing.T) {
			gh := &fakeGitHub{
				issue:     sampleIssue(),
				cloneDir:  t.TempDir(),
				branchRef: &github.BranchRef{BaseBranch: "main", HeadBranch: "feat/x"},
			}
			cl := &fakeClaude{}
			sf := &fakeSimplifyFinder{result: oneSimplifyFinding("internal/foo/foo.go")}
			sa := &fakeSimplifyApplier{report: "## Simplification Report\n\nSTATUS: " + status}
			r := simplifyRunner(gh, cl, sf, sa)

			var buf bytes.Buffer
			if err := r.Run(&buf, Options{IssueRef: "owner/repo#42", NoReportComment: true}); err != nil {
				t.Fatalf("Run returned %v, want nil — the simplify pass is non-fatal", err)
			}
			if gh.commentCalls.Load() != 1 {
				t.Errorf("AddIssueComment called %d times, want 1 — an escalated report must still post", gh.commentCalls.Load())
			}
			if !strings.Contains(buf.String(), "Claude reported "+status+" — stopping the simplify pass") {
				t.Errorf("missing the escalation/stop note in output:\n%s", buf.String())
			}
		})
	}
}

func TestRun_SimplifyCommentFailureIsNonFatal(t *testing.T) {
	gh := &fakeGitHub{
		issue:      sampleIssue(),
		cloneDir:   t.TempDir(),
		branchRef:  &github.BranchRef{BaseBranch: "main", HeadBranch: "feat/x"},
		commentErr: errors.New("github down"),
	}
	cl := &fakeClaude{}
	sf := &fakeSimplifyFinder{result: oneSimplifyFinding("internal/foo/foo.go")}
	sa := &fakeSimplifyApplier{report: "## Simplification Report\n\nSTATUS: DONE"}
	r := simplifyRunner(gh, cl, sf, sa)

	var buf bytes.Buffer
	if err := r.Run(&buf, Options{IssueRef: "owner/repo#42"}); err != nil {
		t.Fatalf("Run returned %v, want nil despite the issue-comment failure", err)
	}
	if sa.called.Load() != 1 {
		t.Errorf("applier called %d times, want 1 — a failed comment post must not abort the run", sa.called.Load())
	}
	if !strings.Contains(buf.String(), "Could not post the simplification report") {
		t.Errorf("expected a non-fatal warning about the failed comment post, got:\n%s", buf.String())
	}
}

func TestRun_SimplifyBranchRefErrorSkips(t *testing.T) {
	// branchRefErr is set, so CurrentBranchRef errors: the base branch cannot be
	// resolved and the pass skips cleanly without running the finder/applier.
	gh := &fakeGitHub{issue: sampleIssue(), cloneDir: t.TempDir(), branchRefErr: errors.New("no origin/HEAD")}
	cl := &fakeClaude{}
	sf := &fakeSimplifyFinder{result: oneSimplifyFinding("internal/foo/foo.go")}
	sa := &fakeSimplifyApplier{report: "unused"}
	r := simplifyRunner(gh, cl, sf, sa)

	var buf bytes.Buffer
	if err := r.Run(&buf, Options{IssueRef: "owner/repo#42"}); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if gh.branchRefCalls.Load() == 0 {
		t.Errorf("CurrentBranchRef called %d times, want at least 1", gh.branchRefCalls.Load())
	}
	if sf.called.Load() != 0 || sa.called.Load() != 0 {
		t.Errorf("finder/applier called (%d/%d), want 0/0 when the base branch cannot be resolved", sf.called.Load(), sa.called.Load())
	}
	if !strings.Contains(buf.String(), "could not resolve the base branch") {
		t.Errorf("missing the branch-resolution skip note in output:\n%s", buf.String())
	}
}

func TestRun_SimplifyFinderErrorIsNonFatal(t *testing.T) {
	gh := &fakeGitHub{
		issue:     sampleIssue(),
		cloneDir:  t.TempDir(),
		branchRef: &github.BranchRef{BaseBranch: "main", HeadBranch: "feat/x"},
	}
	cl := &fakeClaude{}
	sf := &fakeSimplifyFinder{err: errors.New("finder exploded")}
	sa := &fakeSimplifyApplier{report: "unused"}
	r := simplifyRunner(gh, cl, sf, sa)

	var buf bytes.Buffer
	if err := r.Run(&buf, Options{IssueRef: "owner/repo#42"}); err != nil {
		t.Fatalf("Run returned %v, want nil despite the finder error", err)
	}
	if sa.called.Load() != 0 {
		t.Errorf("applier called %d times, want 0 after a finder error", sa.called.Load())
	}
	if !strings.Contains(buf.String(), "Simplify pass could not run") {
		t.Errorf("expected a non-fatal warning about the finder failure, got:\n%s", buf.String())
	}
}

func TestKeepSimplifyFindings(t *testing.T) {
	cases := []struct {
		name     string
		file     string
		rejected bool
	}{
		{"go test file", "internal/foo/foo_test.go", true},
		{"python test file", "pkg/foo_test.py", true},
		{"js test file", "src/foo.test.ts", true},
		{"js spec file", "src/foo.spec.js", true},
		{"tests directory", "tests/integration/foo.go", true},
		{"testdata directory", "internal/foo/testdata/x.json", true},
		{"nested test directory", "internal/test/helper.go", true},
		{"production file is kept", "internal/foo/foo.go", false},
		{"empty file is kept", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := []report.Finding{{Title: "x", File: tc.file}}
			kept, rejected := keepSimplifyFindings(in)
			if tc.rejected {
				if len(rejected) != 1 || len(kept) != 0 {
					t.Errorf("file %q: kept=%d rejected=%d, want it rejected", tc.file, len(kept), len(rejected))
				}
			} else {
				if len(kept) != 1 || len(rejected) != 0 {
					t.Errorf("file %q: kept=%d rejected=%d, want it kept", tc.file, len(kept), len(rejected))
				}
			}
		})
	}
}

// reviewRunner wires the review-and-fix deps onto a fresh Runner alongside the
// shared GitHub/Claude fakes: the adversarial verifier doubles as the finder and
// the review applier folds the fixes. It deliberately leaves the simplify deps
// unset, so each review test reads as just its finder/applier setup.
func reviewRunner(gh *fakeGitHub, cl *fakeClaude, av *fakeAdversarialVerifier, ra *fakeReviewApplier) *Runner {
	ensureValidReport(cl)
	r := newRunner(gh, cl)
	r.AdversarialVerifier = av
	r.ReviewApplier = ra
	return r
}

func oneReviewFinding(file string) *report.ReviewResult {
	return &report.ReviewResult{Findings: []report.Finding{
		{Severity: report.SeverityCritical, Title: "SQL injection in new query", File: file, Problem: "unescaped user input", SuggestedFix: "use a parameterized query"},
	}}
}

// oneThenCleanReview scripts the review finder to surface one finding on the
// first pass and a clean pass on the next, so the bounded review-and-fix loop
// applies the fix once and then converges — the realistic single-fix path the
// pre-loop tests exercised before the loop existed.
func oneThenCleanReview(file string) []*report.ReviewResult {
	return []*report.ReviewResult{oneReviewFinding(file), {}}
}

// twoReviewFindings is a two-finding round the loop folds into a single apply,
// so the accumulation and multi-finding assertions have distinct files to check.
func twoReviewFindings() *report.ReviewResult {
	return &report.ReviewResult{Findings: []report.Finding{
		{Severity: report.SeverityCritical, Title: "SQL injection in new query", File: "internal/foo/foo.go", Problem: "unescaped user input"},
		{Severity: report.SeverityWarning, Title: "possible nil dereference", File: "internal/bar/bar.go", Problem: "missing nil check"},
	}}
}

// Shared literals for the review-pass assertions, extracted so the repeated
// base-branch and production-file strings stay under the goconst threshold.
const (
	reviewTestBase     = "main"
	reviewTestProdFile = "internal/foo/foo.go"
)

func TestRun_ReviewDefaultOnAppliesFindings(t *testing.T) {
	gh := &fakeGitHub{
		issue:     sampleIssue(),
		cloneDir:  t.TempDir(),
		branchRef: &github.BranchRef{BaseBranch: "main", HeadBranch: "feat/x"},
	}
	cl := &fakeClaude{} // helper fills a valid report; --no-report-comment keeps the only comment the review report
	av := &fakeAdversarialVerifier{results: oneThenCleanReview(reviewTestProdFile)}
	ra := &fakeReviewApplier{report: "## Review Report\n\nSTATUS: DONE"}
	r := reviewRunner(gh, cl, av, ra)

	var buf bytes.Buffer
	if err := r.Run(&buf, Options{IssueRef: "owner/repo#42", NoReportComment: true}); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if av.called.Load() != 2 {
		t.Errorf("finder called %d times, want 2 — the loop applies then re-reviews to confirm it converged", av.called.Load())
	}
	if av.base != reviewTestBase {
		t.Errorf("finder got base %q, want main from CurrentBranchRef", av.base)
	}
	if ra.called.Load() != 1 {
		t.Fatalf("applier called %d times, want 1 — the second review came back clean", ra.called.Load())
	}
	if ra.ctx.BaseBranch != reviewTestBase {
		t.Errorf("applier got ctx %+v, want base main threaded from CurrentBranchRef", ra.ctx)
	}
	if len(ra.ctx.Findings) != 1 || ra.ctx.Findings[0].File != reviewTestProdFile {
		t.Errorf("applier got findings %+v, want the single finding from the finder", ra.ctx.Findings)
	}
	if gh.commentCalls.Load() != 1 {
		t.Fatalf("AddIssueComment called %d times, want 1 (the review report)", gh.commentCalls.Load())
	}
	if !strings.Contains(gh.commentBodies[0], "## Review Report") {
		t.Errorf("issue comment does not carry the report:\n%s", gh.commentBodies[0])
	}
	if !strings.Contains(gh.commentBodies[0], reviewCommentFooter("")) {
		t.Errorf("issue comment is missing the attribution footer:\n%s", gh.commentBodies[0])
	}
	if !strings.Contains(buf.String(), "Posted the review report as a comment on issue #42") {
		t.Errorf("missing review-comment confirmation in output:\n%s", buf.String())
	}
}

func TestRun_NoReviewSkipsPass(t *testing.T) {
	gh := &fakeGitHub{
		issue:     sampleIssue(),
		cloneDir:  t.TempDir(),
		branchRef: &github.BranchRef{BaseBranch: "main", HeadBranch: "feat/x"},
	}
	cl := &fakeClaude{}
	av := &fakeAdversarialVerifier{result: oneReviewFinding(reviewTestProdFile)}
	ra := &fakeReviewApplier{report: "## Review Report\n\nSTATUS: DONE"}
	r := reviewRunner(gh, cl, av, ra)

	if err := r.Run(&bytes.Buffer{}, Options{IssueRef: "owner/repo#42", NoReview: true, NoReportComment: true}); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if av.called.Load() != 0 || ra.called.Load() != 0 {
		t.Errorf("finder/applier called (%d/%d), want 0/0 with --no-review", av.called.Load(), ra.called.Load())
	}
	if gh.commentCalls.Load() != 0 {
		t.Errorf("AddIssueComment called %d times, want 0 with --no-review (--no-report-comment suppresses the implement report)", gh.commentCalls.Load())
	}
}

func TestRun_ReviewNoFindingsNoOp(t *testing.T) {
	gh := &fakeGitHub{
		issue:     sampleIssue(),
		cloneDir:  t.TempDir(),
		branchRef: &github.BranchRef{BaseBranch: "main", HeadBranch: "feat/x"},
	}
	cl := &fakeClaude{}
	av := &fakeAdversarialVerifier{result: &report.ReviewResult{}}
	ra := &fakeReviewApplier{report: "unused"}
	r := reviewRunner(gh, cl, av, ra)

	var buf bytes.Buffer
	if err := r.Run(&buf, Options{IssueRef: "owner/repo#42", NoReportComment: true}); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if av.called.Load() != 1 {
		t.Errorf("finder called %d times, want 1", av.called.Load())
	}
	if ra.called.Load() != 0 {
		t.Errorf("applier called %d times, want 0 — no findings is a clean no-op", ra.called.Load())
	}
	if gh.commentCalls.Load() != 0 {
		t.Errorf("AddIssueComment called %d times, want 0 — no findings posts no issue comment", gh.commentCalls.Load())
	}
	if !strings.Contains(buf.String(), "Review found nothing to fix.") {
		t.Errorf("missing the no-op note in output:\n%s", buf.String())
	}
}

func TestRun_ReviewEscalationStops(t *testing.T) {
	for _, status := range []string{"BLOCKED", "NEEDS_CONTEXT"} {
		t.Run(status, func(t *testing.T) {
			gh := &fakeGitHub{
				issue:     sampleIssue(),
				cloneDir:  t.TempDir(),
				branchRef: &github.BranchRef{BaseBranch: "main", HeadBranch: "feat/x"},
			}
			cl := &fakeClaude{}
			av := &fakeAdversarialVerifier{result: oneReviewFinding(reviewTestProdFile)}
			ra := &fakeReviewApplier{report: "## Review Report\n\nSTATUS: " + status}
			r := reviewRunner(gh, cl, av, ra)

			var buf bytes.Buffer
			if err := r.Run(&buf, Options{IssueRef: "owner/repo#42", NoReportComment: true}); err != nil {
				t.Fatalf("Run returned %v, want nil — the review pass is non-fatal", err)
			}
			// The finder returns findings on every call, so had the escalation not
			// stopped the loop it would run the full iteration budget. Exactly one
			// finder + one apply proves the escalated apply stopped it after the
			// first round.
			if av.called.Load() != 1 || ra.called.Load() != 1 {
				t.Errorf("finder/applier called (%d/%d), want 1/1 — an escalation stops the loop after the first apply", av.called.Load(), ra.called.Load())
			}
			if gh.commentCalls.Load() != 1 {
				t.Errorf("AddIssueComment called %d times, want 1 — an escalated report must still post", gh.commentCalls.Load())
			}
			if !strings.Contains(buf.String(), "Claude reported "+status+" — stopping the review pass") {
				t.Errorf("missing the escalation/stop note in output:\n%s", buf.String())
			}
		})
	}
}

func TestRun_ReviewCommentFailureIsNonFatal(t *testing.T) {
	gh := &fakeGitHub{
		issue:      sampleIssue(),
		cloneDir:   t.TempDir(),
		branchRef:  &github.BranchRef{BaseBranch: "main", HeadBranch: "feat/x"},
		commentErr: errors.New("github down"),
	}
	cl := &fakeClaude{}
	av := &fakeAdversarialVerifier{results: oneThenCleanReview(reviewTestProdFile)}
	ra := &fakeReviewApplier{report: "## Review Report\n\nSTATUS: DONE"}
	r := reviewRunner(gh, cl, av, ra)

	var buf bytes.Buffer
	if err := r.Run(&buf, Options{IssueRef: "owner/repo#42"}); err != nil {
		t.Fatalf("Run returned %v, want nil despite the issue-comment failure", err)
	}
	if ra.called.Load() != 1 {
		t.Errorf("applier called %d times, want 1 — a failed comment post must not abort the run", ra.called.Load())
	}
	if !strings.Contains(buf.String(), "Could not post the review report") {
		t.Errorf("expected a non-fatal warning about the failed comment post, got:\n%s", buf.String())
	}
}

func TestRun_ReviewBranchRefErrorSkips(t *testing.T) {
	// branchRefErr is set, so CurrentBranchRef errors: the base branch cannot be
	// resolved and the pass skips cleanly without running the finder/applier.
	gh := &fakeGitHub{issue: sampleIssue(), cloneDir: t.TempDir(), branchRefErr: errors.New("no origin/HEAD")}
	cl := &fakeClaude{}
	av := &fakeAdversarialVerifier{result: oneReviewFinding(reviewTestProdFile)}
	ra := &fakeReviewApplier{report: "unused"}
	r := reviewRunner(gh, cl, av, ra)

	var buf bytes.Buffer
	if err := r.Run(&buf, Options{IssueRef: "owner/repo#42"}); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if gh.branchRefCalls.Load() == 0 {
		t.Errorf("CurrentBranchRef called %d times, want at least 1", gh.branchRefCalls.Load())
	}
	if av.called.Load() != 0 || ra.called.Load() != 0 {
		t.Errorf("finder/applier called (%d/%d), want 0/0 when the base branch cannot be resolved", av.called.Load(), ra.called.Load())
	}
	if !strings.Contains(buf.String(), "could not resolve the base branch") {
		t.Errorf("missing the branch-resolution skip note in output:\n%s", buf.String())
	}
}

func TestRun_ReviewFinderErrorIsNonFatal(t *testing.T) {
	gh := &fakeGitHub{
		issue:     sampleIssue(),
		cloneDir:  t.TempDir(),
		branchRef: &github.BranchRef{BaseBranch: "main", HeadBranch: "feat/x"},
	}
	cl := &fakeClaude{}
	av := &fakeAdversarialVerifier{err: errors.New("finder exploded")}
	ra := &fakeReviewApplier{report: "unused"}
	r := reviewRunner(gh, cl, av, ra)

	var buf bytes.Buffer
	if err := r.Run(&buf, Options{IssueRef: "owner/repo#42"}); err != nil {
		t.Fatalf("Run returned %v, want nil despite the finder error", err)
	}
	if ra.called.Load() != 0 {
		t.Errorf("applier called %d times, want 0 after a finder error", ra.called.Load())
	}
	if !strings.Contains(buf.String(), "Review pass could not run") {
		t.Errorf("expected a non-fatal warning about the finder failure, got:\n%s", buf.String())
	}
}

// TestRun_ReviewLoopConvergesAfterApply drives the bounded loop's clean-exit
// branch: a first round of findings is applied, then the re-review comes back
// clean, so the loop stops after one apply and two finder calls — folding both
// findings of the round into the single apply.
func TestRun_ReviewLoopConvergesAfterApply(t *testing.T) {
	gh := &fakeGitHub{
		issue:     sampleIssue(),
		cloneDir:  t.TempDir(),
		branchRef: &github.BranchRef{BaseBranch: "main", HeadBranch: "feat/x"},
	}
	cl := &fakeClaude{}
	av := &fakeAdversarialVerifier{results: []*report.ReviewResult{twoReviewFindings(), {}}}
	ra := &fakeReviewApplier{report: "## Review Report\n\nSTATUS: DONE"}
	r := reviewRunner(gh, cl, av, ra)

	var buf bytes.Buffer
	if err := r.Run(&buf, Options{IssueRef: "owner/repo#42", NoReportComment: true}); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if av.called.Load() != 2 {
		t.Errorf("finder called %d times, want 2 — apply once, then re-review to confirm clean", av.called.Load())
	}
	if ra.called.Load() != 1 {
		t.Fatalf("applier called %d times, want 1 — the second review found nothing", ra.called.Load())
	}
	if len(ra.ctx.Findings) != 2 {
		t.Errorf("applier got %d findings, want 2 — both findings of the round in one apply", len(ra.ctx.Findings))
	}
	if !strings.Contains(buf.String(), "Review pass converged: no further findings.") {
		t.Errorf("missing the converged note in output:\n%s", buf.String())
	}
}

// TestRun_ReviewLoopExhaustsBudget drives the budget-exhaustion branch: a finder
// that keeps reporting findings is applied exactly defaultMaxReviewIterations
// times, then the loop stops and surfaces the unresolved findings.
func TestRun_ReviewLoopExhaustsBudget(t *testing.T) {
	gh := &fakeGitHub{
		issue:     sampleIssue(),
		cloneDir:  t.TempDir(),
		branchRef: &github.BranchRef{BaseBranch: "main", HeadBranch: "feat/x"},
	}
	cl := &fakeClaude{}
	av := &fakeAdversarialVerifier{result: oneReviewFinding(reviewTestProdFile)} // never converges
	ra := &fakeReviewApplier{report: "## Review Report\n\nSTATUS: DONE"}
	r := reviewRunner(gh, cl, av, ra)

	var buf bytes.Buffer
	if err := r.Run(&buf, Options{IssueRef: "owner/repo#42", NoReportComment: true}); err != nil {
		t.Fatalf("Run returned %v, want nil — the review pass is non-fatal", err)
	}
	if av.called.Load() != defaultMaxReviewIterations || ra.called.Load() != defaultMaxReviewIterations {
		t.Errorf("finder/applier called (%d/%d), want %d/%d — the loop runs the full budget", av.called.Load(), ra.called.Load(), defaultMaxReviewIterations, defaultMaxReviewIterations)
	}
	if !strings.Contains(buf.String(), "stopped after 3 iteration(s) with findings still unresolved") {
		t.Errorf("missing the unresolved-findings budget note in output:\n%s", buf.String())
	}
}

// TestRun_ReviewLoopMaxIterationsOne proves opts.MaxReviewIterations is honored:
// a cap of 1 applies exactly once and then stops with the unresolved note, even
// though the finder still reports findings.
func TestRun_ReviewLoopMaxIterationsOne(t *testing.T) {
	gh := &fakeGitHub{
		issue:     sampleIssue(),
		cloneDir:  t.TempDir(),
		branchRef: &github.BranchRef{BaseBranch: "main", HeadBranch: "feat/x"},
	}
	cl := &fakeClaude{}
	av := &fakeAdversarialVerifier{result: oneReviewFinding(reviewTestProdFile)} // never converges
	ra := &fakeReviewApplier{report: "## Review Report\n\nSTATUS: DONE"}
	r := reviewRunner(gh, cl, av, ra)

	var buf bytes.Buffer
	if err := r.Run(&buf, Options{IssueRef: "owner/repo#42", MaxReviewIterations: 1, NoReportComment: true}); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if av.called.Load() != 1 || ra.called.Load() != 1 {
		t.Errorf("finder/applier called (%d/%d), want 1/1 — the cap of 1 stops after a single apply", av.called.Load(), ra.called.Load())
	}
	if !strings.Contains(buf.String(), "stopped after 1 iteration(s) with findings still unresolved") {
		t.Errorf("missing the budget note for a 1-iteration cap in output:\n%s", buf.String())
	}
}

// TestRun_ReviewLoopAccumulatesFindingsForCapture proves the findings from every
// loop round reach the capture pass: two rounds of findings (2 then 1) followed
// by a clean pass hand capture all three findings, not just the last round's.
func TestRun_ReviewLoopAccumulatesFindingsForCapture(t *testing.T) {
	gh := &fakeGitHub{
		issue:     sampleIssue(),
		cloneDir:  t.TempDir(),
		branchRef: &github.BranchRef{BaseBranch: "main", HeadBranch: "feat/x"},
	}
	cl := &fakeClaude{}
	av := &fakeAdversarialVerifier{results: []*report.ReviewResult{twoReviewFindings(), oneReviewFinding(reviewTestProdFile), {}}}
	ra := &fakeReviewApplier{report: "## Review Report\n\nSTATUS: DONE"}
	cp := &fakeCapturer{result: onePatternProposal()}
	r := captureRunner(t, gh, cl, av, ra, cp)

	var buf bytes.Buffer
	if err := r.Run(&buf, Options{IssueRef: "owner/repo#42", NoReportComment: true}); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if av.called.Load() != 3 || ra.called.Load() != 2 {
		t.Errorf("finder/applier called (%d/%d), want 3/2 — two apply rounds then a clean re-review", av.called.Load(), ra.called.Load())
	}
	if cp.called.Load() != 1 {
		t.Fatalf("capturer called %d times, want 1", cp.called.Load())
	}
	if len(cp.ctx.Findings) != 3 {
		t.Errorf("capturer got %d findings, want 3 accumulated across both apply rounds", len(cp.ctx.Findings))
	}
}

// captureRunner wires the capture pass onto a fresh Runner alongside the review
// deps and a ResolveWiki seam that resolves to a temp wiki dir (so the capture
// gate's wiki.Dir != "" check passes without cloning a real wiki). The review
// deps are wired so the pass produces findings that flow into capture; tests
// that exercise the memory-only path leave NoReview set.
func captureRunner(t *testing.T, gh *fakeGitHub, cl *fakeClaude, av *fakeAdversarialVerifier, ra *fakeReviewApplier, cp *fakeCapturer) *Runner {
	t.Helper()
	ensureValidReport(cl)
	r := newRunner(gh, cl)
	r.AdversarialVerifier = av
	r.ReviewApplier = ra
	r.Capturer = cp
	wikiDir := t.TempDir()
	r.ResolveWiki = func(_, _ string, _ patterns.WikiOptions, _ patterns.RemoteOptions) patterns.ResolvedWiki {
		return patterns.ResolvedWiki{Repo: "owner/repo.wiki", CommitSHA: "abc1234def", Dir: wikiDir}
	}
	return r
}

func onePatternProposal() *capture.CaptureResult {
	return &capture.CaptureResult{
		Patterns: []capture.ProposedPage{
			{
				Path:      "review_patterns/escape-untrusted-fences.md",
				Kind:      capture.KindPattern,
				Title:     "Escape untrusted fences",
				Body:      "# Review Pattern: Escape untrusted fences\n\n## What to check\n...",
				Rationale: "The fence-escaping fix recurs across builders.",
			},
		},
	}
}

func TestRun_CaptureProposesAndPostsComment(t *testing.T) {
	gh := &fakeGitHub{
		issue:     sampleIssue(),
		cloneDir:  t.TempDir(),
		branchRef: &github.BranchRef{BaseBranch: "main", HeadBranch: "feat/x"},
	}
	cl := &fakeClaude{}
	av := &fakeAdversarialVerifier{results: oneThenCleanReview(reviewTestProdFile)}
	ra := &fakeReviewApplier{report: "## Review Report\n\nSTATUS: DONE"}
	cp := &fakeCapturer{result: onePatternProposal()}
	r := captureRunner(t, gh, cl, av, ra, cp)

	var buf bytes.Buffer
	// --no-report-comment leaves only the review and capture comments, so the
	// comment count and bodies isolate the capture pass.
	if err := r.Run(&buf, Options{IssueRef: "owner/repo#42", NoReportComment: true}); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if cp.called.Load() != 1 {
		t.Fatalf("capturer called %d times, want 1 — the capture pass is on by default with a wiki", cp.called.Load())
	}
	// Capture received the review finding as a candidate (review ran first) and
	// the implementation report — proving it runs after the implement and review
	// passes.
	if len(cp.ctx.Findings) != 1 || cp.ctx.Findings[0].File != reviewTestProdFile {
		t.Errorf("capturer got findings %+v, want the single review finding as a candidate", cp.ctx.Findings)
	}
	if cp.ctx.IssueNumber != 42 || cp.ctx.RepoName != testRepoFullName {
		t.Errorf("capturer got ctx repo=%q issue=%d, want owner/repo / 42", cp.ctx.RepoName, cp.ctx.IssueNumber)
	}
	if cp.ctx.ImplementReport == "" {
		t.Errorf("capturer got an empty implementation report, want the implement session's report")
	}
	if gh.commentCalls.Load() != 2 {
		t.Fatalf("AddIssueComment called %d times, want 2 (review then capture)", gh.commentCalls.Load())
	}
	captureFooter := "_Capture proposals generated by " + attribution.Tool() + " implement " + attribution.AssistantWith("") + "_"
	if !strings.Contains(gh.commentBodies[1], captureFooter) {
		t.Errorf("the capture comment is missing its attribution footer:\n%s", gh.commentBodies[1])
	}
	if !strings.Contains(gh.commentBodies[1], "review_patterns/escape-untrusted-fences.md") {
		t.Errorf("the capture comment does not carry the proposal:\n%s", gh.commentBodies[1])
	}
	if !strings.Contains(buf.String(), "Captured knowledge proposals:") {
		t.Errorf("missing the capture proposals on stdout:\n%s", buf.String())
	}
}

func TestRun_CaptureErrorIsNonFatal(t *testing.T) {
	gh := &fakeGitHub{
		issue:     sampleIssue(),
		cloneDir:  t.TempDir(),
		branchRef: &github.BranchRef{BaseBranch: "main", HeadBranch: "feat/x"},
	}
	cl := &fakeClaude{}
	av := &fakeAdversarialVerifier{result: oneReviewFinding(reviewTestProdFile)}
	ra := &fakeReviewApplier{report: "## Review Report\n\nSTATUS: DONE"}
	cp := &fakeCapturer{err: errors.New("capture exploded")}
	r := captureRunner(t, gh, cl, av, ra, cp)
	ff := &fakeFinalizer{report: defaultFinalizeReport}
	r.Finalizer = ff

	var buf bytes.Buffer
	if err := r.Run(&buf, Options{IssueRef: "owner/repo#42", NoReportComment: true}); err != nil {
		t.Fatalf("Run returned %v, want nil despite the capture error", err)
	}
	if cp.called.Load() != 1 {
		t.Errorf("capturer called %d times, want 1", cp.called.Load())
	}
	if ff.called.Load() != 1 {
		t.Errorf("finalizer called %d times, want 1 — a capture error must not block the PR", ff.called.Load())
	}
	if !strings.Contains(buf.String(), "Capture pass could not run") {
		t.Errorf("expected a non-fatal warning about the capture failure, got:\n%s", buf.String())
	}
}

// TestRun_CaptureNilResultIsNonFatal proves a capturer that returns (nil, nil)
// — which the ClaudeCapturer contract permits — is skipped gracefully instead of
// panicking on a nil dereference after the implementation and PR work are done.
func TestRun_CaptureNilResultIsNonFatal(t *testing.T) {
	gh := &fakeGitHub{
		issue:     sampleIssue(),
		cloneDir:  t.TempDir(),
		branchRef: &github.BranchRef{BaseBranch: "main", HeadBranch: "feat/x"},
	}
	cl := &fakeClaude{}
	av := &fakeAdversarialVerifier{result: oneReviewFinding(reviewTestProdFile)}
	ra := &fakeReviewApplier{report: "## Review Report\n\nSTATUS: DONE"}
	cp := &fakeCapturer{result: nil}
	r := captureRunner(t, gh, cl, av, ra, cp)
	ff := &fakeFinalizer{report: defaultFinalizeReport}
	r.Finalizer = ff

	var buf bytes.Buffer
	if err := r.Run(&buf, Options{IssueRef: "owner/repo#42", NoReportComment: true}); err != nil {
		t.Fatalf("Run returned %v, want nil despite the nil capture result", err)
	}
	if cp.called.Load() != 1 {
		t.Errorf("capturer called %d times, want 1", cp.called.Load())
	}
	if ff.called.Load() != 1 {
		t.Errorf("finalizer called %d times, want 1 — a nil capture result must not block the PR", ff.called.Load())
	}
	if !strings.Contains(buf.String(), "Capture proposed no new review patterns or memory pages.") {
		t.Errorf("expected the no-proposals note for a nil capture result, got:\n%s", buf.String())
	}
}

func TestRun_CaptureSkippedWithoutWiki(t *testing.T) {
	gh := &fakeGitHub{
		issue:     sampleIssue(),
		cloneDir:  t.TempDir(),
		branchRef: &github.BranchRef{BaseBranch: "main", HeadBranch: "feat/x"},
	}
	cl := &fakeClaude{}
	av := &fakeAdversarialVerifier{result: oneReviewFinding(reviewTestProdFile)}
	ra := &fakeReviewApplier{report: "## Review Report\n\nSTATUS: DONE"}
	cp := &fakeCapturer{result: onePatternProposal()}
	r := captureRunner(t, gh, cl, av, ra, cp)
	// A wiki that did not resolve (no --wiki): capture has nowhere to propose to.
	r.ResolveWiki = func(_, _ string, _ patterns.WikiOptions, _ patterns.RemoteOptions) patterns.ResolvedWiki {
		return patterns.ResolvedWiki{}
	}

	if err := r.Run(&bytes.Buffer{}, Options{IssueRef: "owner/repo#42", NoReportComment: true}); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if cp.called.Load() != 0 {
		t.Errorf("capturer called %d times, want 0 — capture is skipped without a resolved wiki", cp.called.Load())
	}
}

func TestRun_CaptureSkippedByNoCapture(t *testing.T) {
	gh := &fakeGitHub{
		issue:     sampleIssue(),
		cloneDir:  t.TempDir(),
		branchRef: &github.BranchRef{BaseBranch: "main", HeadBranch: "feat/x"},
	}
	cl := &fakeClaude{}
	av := &fakeAdversarialVerifier{result: oneReviewFinding(reviewTestProdFile)}
	ra := &fakeReviewApplier{report: "## Review Report\n\nSTATUS: DONE"}
	cp := &fakeCapturer{result: onePatternProposal()}
	r := captureRunner(t, gh, cl, av, ra, cp)

	if err := r.Run(&bytes.Buffer{}, Options{IssueRef: "owner/repo#42", NoCapture: true, NoReportComment: true}); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if cp.called.Load() != 0 {
		t.Errorf("capturer called %d times, want 0 with --no-capture", cp.called.Load())
	}
}

// TestRun_CaptureRunsMemoryOnlyWithoutReview proves capture still runs when
// --no-review left no findings: it proposes memory pages from the plan and report
// alone, receiving an empty candidate list.
func TestRun_CaptureRunsMemoryOnlyWithoutReview(t *testing.T) {
	gh := &fakeGitHub{
		issue:     sampleIssue(),
		cloneDir:  t.TempDir(),
		branchRef: &github.BranchRef{BaseBranch: "main", HeadBranch: "feat/x"},
	}
	cl := &fakeClaude{}
	av := &fakeAdversarialVerifier{result: oneReviewFinding(reviewTestProdFile)}
	ra := &fakeReviewApplier{report: "unused"}
	cp := &fakeCapturer{result: &capture.CaptureResult{
		Memory: []capture.ProposedPage{
			{Path: "memory/capture-is-propose-only.md", Kind: capture.KindMemory, Title: "Propose only", Body: "Capture never pushes."},
		},
	}}
	r := captureRunner(t, gh, cl, av, ra, cp)

	var buf bytes.Buffer
	if err := r.Run(&buf, Options{IssueRef: "owner/repo#42", NoReview: true, NoReportComment: true}); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if av.called.Load() != 0 {
		t.Errorf("review finder called %d times, want 0 with --no-review", av.called.Load())
	}
	if cp.called.Load() != 1 {
		t.Fatalf("capturer called %d times, want 1 — capture runs memory-only without review", cp.called.Load())
	}
	if len(cp.ctx.Findings) != 0 {
		t.Errorf("capturer got findings %+v, want none when review was skipped", cp.ctx.Findings)
	}
	if gh.commentCalls.Load() != 1 {
		t.Errorf("AddIssueComment called %d times, want 1 (the capture comment only)", gh.commentCalls.Load())
	}
}

// TestRun_CaptureWritePushesAcceptedPages proves --capture-wiki (with --yes for
// the non-interactive run) pushes the accepted proposal pages: the write-back
// renders each page with its provenance marker and hands it to the writer, after
// the proposal comment is posted and before finalize opens the PR.
func TestRun_CaptureWritePushesAcceptedPages(t *testing.T) {
	gh := &fakeGitHub{
		issue:     sampleIssue(),
		cloneDir:  t.TempDir(),
		branchRef: &github.BranchRef{BaseBranch: "main", HeadBranch: "feat/x"},
	}
	cl := &fakeClaude{}
	av := &fakeAdversarialVerifier{result: oneReviewFinding(reviewTestProdFile)}
	ra := &fakeReviewApplier{report: "## Review Report\n\nSTATUS: DONE"}
	cp := &fakeCapturer{result: onePatternProposal()}
	r := captureRunner(t, gh, cl, av, ra, cp)
	cw := &fakeCaptureWriter{}
	r.CaptureWriter = cw
	ff := &fakeFinalizer{report: defaultFinalizeReport}
	r.Finalizer = ff

	var buf bytes.Buffer
	if err := r.Run(&buf, Options{IssueRef: "owner/repo#42", CaptureWiki: true, Yes: true, NoReportComment: true}); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if cw.applyCalls.Load() != 1 {
		t.Fatalf("ApplyAdditions called %d times, want 1 under --capture-wiki", cw.applyCalls.Load())
	}
	if len(cw.applyFiles) != 1 || cw.applyFiles[0].Path != "review_patterns/escape-untrusted-fences.md" {
		t.Errorf("wrote files %+v, want the one accepted pattern page", cw.applyFiles)
	}
	prov := capture.Provenance{Repo: testRepoFullName, Issue: 42}
	if !strings.HasPrefix(cw.applyFiles[0].Content, prov.Marker()) {
		t.Errorf("written page must carry the provenance marker, got:\n%s", cw.applyFiles[0].Content)
	}
	if cw.cloneRepo != "owner/repo.wiki" {
		t.Errorf("Clone got repo %q, want the resolved wiki repo", cw.cloneRepo)
	}
	if ff.called.Load() != 1 {
		t.Errorf("finalizer called %d times, want 1 — the write-back precedes finalize", ff.called.Load())
	}
}

// TestRun_CaptureDefaultWritesNothing locks the central gate: a default run
// (no --capture-wiki) stays propose-only — the proposals are posted but the
// writer is never touched.
func TestRun_CaptureDefaultWritesNothing(t *testing.T) {
	gh := &fakeGitHub{
		issue:     sampleIssue(),
		cloneDir:  t.TempDir(),
		branchRef: &github.BranchRef{BaseBranch: "main", HeadBranch: "feat/x"},
	}
	cl := &fakeClaude{}
	av := &fakeAdversarialVerifier{result: oneReviewFinding(reviewTestProdFile)}
	ra := &fakeReviewApplier{report: "## Review Report\n\nSTATUS: DONE"}
	cp := &fakeCapturer{result: onePatternProposal()}
	r := captureRunner(t, gh, cl, av, ra, cp)
	cw := &fakeCaptureWriter{}
	r.CaptureWriter = cw

	if err := r.Run(&bytes.Buffer{}, Options{IssueRef: "owner/repo#42", NoReportComment: true}); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if cp.called.Load() != 1 {
		t.Errorf("capturer called %d times, want 1 — capture still proposes by default", cp.called.Load())
	}
	if cw.cloneCalls.Load() != 0 || cw.applyCalls.Load() != 0 {
		t.Errorf("a default run must write nothing: clone=%d apply=%d", cw.cloneCalls.Load(), cw.applyCalls.Load())
	}
}

// TestRun_CaptureWriteRefusesNonTTYWithoutYes proves the non-TTY guard is
// non-fatal in implement: --capture-wiki without --yes on a non-TTY refuses to
// write, surfaces the refusal, and lets the run finish (the PR still opens) —
// unlike sync, where pruning is the deliverable and the refusal is fatal.
func TestRun_CaptureWriteRefusesNonTTYWithoutYes(t *testing.T) {
	gh := &fakeGitHub{
		issue:     sampleIssue(),
		cloneDir:  t.TempDir(),
		branchRef: &github.BranchRef{BaseBranch: "main", HeadBranch: "feat/x"},
	}
	cl := &fakeClaude{}
	av := &fakeAdversarialVerifier{result: oneReviewFinding(reviewTestProdFile)}
	ra := &fakeReviewApplier{report: "## Review Report\n\nSTATUS: DONE"}
	cp := &fakeCapturer{result: onePatternProposal()}
	r := captureRunner(t, gh, cl, av, ra, cp)
	cw := &fakeCaptureWriter{}
	r.CaptureWriter = cw
	r.IsTTY = func() bool { return false }
	r.In = strings.NewReader("")
	ff := &fakeFinalizer{report: defaultFinalizeReport}
	r.Finalizer = ff

	var buf bytes.Buffer
	if err := r.Run(&buf, Options{IssueRef: "owner/repo#42", CaptureWiki: true, NoReportComment: true}); err != nil {
		t.Fatalf("Run returned %v, want nil — a non-TTY refusal must be non-fatal", err)
	}
	if cw.applyCalls.Load() != 0 {
		t.Errorf("nothing must be written when the non-TTY guard refuses, apply=%d", cw.applyCalls.Load())
	}
	if ff.called.Load() != 1 {
		t.Errorf("finalizer called %d times, want 1 — a refused write must not block the PR", ff.called.Load())
	}
	if !strings.Contains(buf.String(), "Capture write-back could not run") {
		t.Errorf("the refusal should be surfaced on stdout, got:\n%s", buf.String())
	}
}

func TestRun_FinalizeOpensPR(t *testing.T) {
	gh := &fakeGitHub{issue: sampleIssue(), cloneDir: t.TempDir()}
	cl := &fakeClaude{report: validImplReport}
	ff := &fakeFinalizer{report: "## Pull Request\n\n- URL: https://github.com/owner/repo/pull/9\n\nSTATUS: DONE"}
	r := newRunner(gh, cl)
	r.Finalizer = ff

	var buf bytes.Buffer
	if err := r.Run(&buf, Options{IssueRef: "owner/repo#42"}); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if ff.called.Load() != 1 {
		t.Fatalf("finalizer called %d times, want 1 — the PR is opened last", ff.called.Load())
	}
	if ff.dir != gh.cloneDir {
		t.Errorf("finalizer ran in %q, want clone dir %q", ff.dir, gh.cloneDir)
	}
	if ff.ctx.RepoFullName != testRepoFullName || ff.ctx.IssueNumber != 42 || ff.ctx.IssueTitle != "Add foo widget" {
		t.Errorf("finalizer got ctx %+v, want repo owner/repo issue #42 titled \"Add foo widget\"", ff.ctx)
	}
	if !strings.Contains(buf.String(), "Pull request:") || !strings.Contains(buf.String(), "pull/9") {
		t.Errorf("missing the finalize report in output:\n%s", buf.String())
	}
}

// TestRun_FinalizeClosingByStatus locks how the implement report's terminal
// STATUS drives the issue link the finalize session uses. A complete
// implementation (DONE / DONE_WITH_CONCERNS) opens a closing PR
// (FinalizeContext.Closing == true → "Closes #N"); a PARTIAL one — a reviewable
// subset that did not finish every work package — still opens a PR but a
// non-closing one (Closing == false → "Refs #N") so the issue stays open. None of
// these abort the run.
func TestRun_FinalizeClosingByStatus(t *testing.T) {
	cases := []struct {
		status      string
		wantClosing bool
	}{
		{"DONE", true},
		{"DONE_WITH_CONCERNS", true},
		{"PARTIAL", false},
	}
	for _, tc := range cases {
		t.Run(tc.status, func(t *testing.T) {
			gh := &fakeGitHub{issue: sampleIssue(), cloneDir: t.TempDir()}
			cl := &fakeClaude{report: "## Implementation Report (issue #42)\n\nSTATUS: " + tc.status}
			ff := &fakeFinalizer{report: defaultFinalizeReport}
			r := newRunner(gh, cl)
			r.Finalizer = ff

			if err := r.Run(&bytes.Buffer{}, Options{IssueRef: "owner/repo#42"}); err != nil {
				t.Fatalf("Run returned %v, want nil for a %s report", err, tc.status)
			}
			if ff.called.Load() != 1 {
				t.Fatalf("finalizer called %d times, want 1 — a %s report still opens a PR", ff.called.Load(), tc.status)
			}
			if ff.ctx.Closing != tc.wantClosing {
				t.Errorf("finalizer got Closing=%v for STATUS: %s, want %v", ff.ctx.Closing, tc.status, tc.wantClosing)
			}
		})
	}
}

func TestRun_FinalizeErrorIsFatal(t *testing.T) {
	gh := &fakeGitHub{issue: sampleIssue(), cloneDir: t.TempDir()}
	cl := &fakeClaude{report: validImplReport}
	ff := &fakeFinalizer{err: errors.New("gh pr create failed")}
	r := newRunner(gh, cl)
	r.Finalizer = ff

	var buf bytes.Buffer
	err := r.Run(&buf, Options{IssueRef: "owner/repo#42"})
	if err == nil || !strings.Contains(err.Error(), "opening the pull request") {
		t.Fatalf("Run returned %v, want a fatal opening-the-pull-request error", err)
	}
	if ff.called.Load() != 1 {
		t.Errorf("finalizer called %d times, want 1", ff.called.Load())
	}
	if !strings.Contains(buf.String(), "opening the pull request failed") {
		t.Errorf("missing the finalize-failure note in output:\n%s", buf.String())
	}
}

// fakeGitHub is a scripted GitHubClient. The test sets the canned issue and
// optional clone error; both calls record their invocation count so each
// test can assert exactly which steps the runner reached.
type fakeGitHub struct {
	issue    *github.Issue
	issueErr error

	relations    *github.IssueRelations
	relationsErr error

	comments    []github.IssueComment
	commentsErr error
	listCalls   atomic.Int32

	cloneDir        string
	cloneErr        error
	getCalls        atomic.Int32
	cloneCalls      atomic.Int32
	cloneLocalCalls atomic.Int32

	commentErr    error
	commentCalls  atomic.Int32
	commentBodies []string

	branchRef      *github.BranchRef
	branchRefErr   error
	branchRefCalls atomic.Int32

	// resumeState/resumeErr drive PrepareResume (pre-implement resume detection);
	// progressState/progressErr drive CurrentFeatureProgress (post-abort partial
	// progress); pushBranch* record PushBranch (the clone-mode partial push).
	resumeState *github.ResumeState
	resumeErr   error
	resumeCalls atomic.Int32

	progressState *github.ResumeState
	progressErr   error
	progressCalls atomic.Int32

	pushBranchArg   string
	pushBranchErr   error
	pushBranchCalls atomic.Int32
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

// GetIssueRelations returns the canned Meta/Sub-Issue neighborhood, defaulting
// to "no relations" (the issue stands alone) so tests that do not exercise the
// Meta/sibling path need not set it.
func (f *fakeGitHub) GetIssueRelations(_, _ string, _ int) (*github.IssueRelations, error) {
	if f.relationsErr != nil {
		return nil, f.relationsErr
	}
	if f.relations == nil {
		return &github.IssueRelations{}, nil
	}
	return f.relations, nil
}

// ListIssueComments returns the canned comments (oldest-first, as gh does),
// unless commentsErr is set to simulate a GitHub failure. The default zero
// value (nil comments, nil error) means "no comments", so plan-reuse finds
// nothing and the existing planning tests fall through to a fresh session.
func (f *fakeGitHub) ListIssueComments(_, _ string, _ int) ([]github.IssueComment, error) {
	f.listCalls.Add(1)
	if f.commentsErr != nil {
		return nil, f.commentsErr
	}
	return f.comments, nil
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

// AddIssueComment records each posted comment body in order (plan then report)
// and returns a canned URL, unless commentErr is set to simulate a GitHub
// failure. Calls are sequential within a run, so a plain append is safe.
func (f *fakeGitHub) AddIssueComment(owner, name string, number int, body string) (string, error) {
	f.commentCalls.Add(1)
	if f.commentErr != nil {
		return "", f.commentErr
	}
	f.commentBodies = append(f.commentBodies, body)
	return fmt.Sprintf("https://github.com/%s/%s/issues/%d#issuecomment-1", owner, name, number), nil
}

// CurrentBranchRef returns the canned branch ref for the simplify and review
// passes (so they can scope their diff against the base branch), unless
// branchRefErr is set to simulate a git failure, in which case the passes skip
// cleanly. Production CurrentBranchRef never returns (nil, nil); tests that drive
// a pass to completion set branchRef, and tests that exercise the skip path set
// branchRefErr.
func (f *fakeGitHub) CurrentBranchRef(_ string) (*github.BranchRef, error) {
	f.branchRefCalls.Add(1)
	if f.branchRefErr != nil {
		return nil, f.branchRefErr
	}
	return f.branchRef, nil
}

// CloneRepoLocal mirrors github.UseLocalRepo: it returns a Local repo so
// Cleanup is a no-op.
func (f *fakeGitHub) CloneRepoLocal(ref string, _ github.LocalOptions) (*github.Repo, error) {
	f.cloneLocalCalls.Add(1)
	if f.cloneErr != nil {
		return nil, f.cloneErr
	}
	owner, name, err := github.ParseRepoRef(ref)
	if err != nil {
		return nil, err
	}
	return &github.Repo{Owner: owner, Name: name, Dir: f.cloneDir, Local: true}, nil
}

// PrepareResume returns the canned resumable branch (nil by default, so a run
// implements fresh) and records the call so tests can assert detection ran.
func (f *fakeGitHub) PrepareResume(_ string, _ int) (*github.ResumeState, error) {
	f.resumeCalls.Add(1)
	return f.resumeState, f.resumeErr
}

// CurrentFeatureProgress returns the canned partial-progress state (nil by
// default, so persistPartialProgress finds nothing to push).
func (f *fakeGitHub) CurrentFeatureProgress(_ string) (*github.ResumeState, error) {
	f.progressCalls.Add(1)
	return f.progressState, f.progressErr
}

// PushBranch records the branch a clone-mode abort pushed to preserve partial
// progress, returning pushBranchErr to exercise the best-effort failure path.
func (f *fakeGitHub) PushBranch(_, branch string) error {
	f.pushBranchCalls.Add(1)
	f.pushBranchArg = branch
	return f.pushBranchErr
}

type fakeClaude struct {
	called atomic.Int32
	dir    string
	ctx    Context
	report string
	model  string
	err    error
}

func (f *fakeClaude) Implement(dir string, ctx Context) (string, string, error) {
	f.called.Add(1)
	f.dir = dir
	f.ctx = ctx
	return f.report, f.model, f.err
}

// testRepoFullName is the "owner/repo" slug the tests parse out of the
// "owner/repo#42" issue refs; resolved-context assertions compare against it.
const testRepoFullName = "owner/repo"

func sampleIssue() *github.Issue {
	return &github.Issue{
		Title: "Add foo widget",
		Body:  "## Description\n\nFoo widget does X.\n\n## Acceptance Criteria\n- foo()\n",
		URL:   "https://github.com/owner/repo/issues/42",
		State: "open",
	}
}

// defaultFinalizeReport is the canned PR report the default finalizer returns,
// so every full Run exercises the finalize step (which opens the PR last)
// without each test having to wire its own finalizer.
const defaultFinalizeReport = "## Pull Request\n\n- URL: https://github.com/owner/repo/pull/9\n- Branch: implement/issue-42\n\nSTATUS: DONE"

// validImplReport is a minimal but well-formed implementation report — the
// mandated heading plus a terminal STATUS line — that Run's implement guard
// accepts. Tests that drive a successful Run past the implement step use it so
// the guard does not (correctly) abort on a missing or incomplete report.
const validImplReport = "## Implementation Report (issue #42)\n\nSTATUS: DONE"

// newRunner wires a default no-op finalizer so the full Run path — which now ends
// by opening the PR — completes in tests that do not exercise finalize directly.
// Finalize-specific tests replace r.Finalizer with their own fake.
func newRunner(gh *fakeGitHub, cl *fakeClaude) *Runner {
	return &Runner{Claude: cl, GitHub: gh, Finalizer: &fakeFinalizer{report: defaultFinalizeReport}}
}

func TestRun_HappyPath(t *testing.T) {
	gh := &fakeGitHub{issue: sampleIssue(), cloneDir: "/tmp/clone"}
	cl := &fakeClaude{report: validImplReport}
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
	if cl.ctx.IssueNumber != 42 || cl.ctx.RepoFullName != testRepoFullName {
		t.Errorf("Claude got ctx %+v, want #42 in owner/repo", cl.ctx)
	}
	if cl.ctx.IssueTitle != "Add foo widget" {
		t.Errorf("Claude got title %q, want %q", cl.ctx.IssueTitle, "Add foo widget")
	}
	if !strings.Contains(buf.String(), "Claude implementation report") {
		t.Errorf("missing report header: %s", buf.String())
	}
	if !strings.Contains(buf.String(), "STATUS: DONE") {
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
	if gh.commentCalls.Load() != 0 {
		t.Errorf("AddIssueComment called %d times, want 0 — a hard Implement error yields no report to post", gh.commentCalls.Load())
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
	if got.RepoFullName != testRepoFullName || got.IssueNumber != 7 {
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
	cl := &fakeClaude{report: validImplReport}
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

func TestRun_LocalNoClone(t *testing.T) {
	dir := t.TempDir()
	gh := &fakeGitHub{issue: sampleIssue(), cloneDir: dir}
	cl := &fakeClaude{report: validImplReport}
	r := newRunner(gh, cl)

	var buf bytes.Buffer
	if err := r.Run(&buf, Options{IssueRef: "owner/repo#42", Local: true}); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if gh.cloneCalls.Load() != 0 {
		t.Errorf("CloneRepo (temp-dir clone) calls = %d, want 0 in local mode", gh.cloneCalls.Load())
	}
	if gh.cloneLocalCalls.Load() != 1 {
		t.Errorf("CloneRepoLocal calls = %d, want 1", gh.cloneLocalCalls.Load())
	}
	if cl.called.Load() != 1 {
		t.Errorf("Claude.Implement called %d times, want 1", cl.called.Load())
	}
	if cl.dir != dir {
		t.Errorf("Claude got dir %q, want the local checkout %q", cl.dir, dir)
	}
	if !strings.Contains(buf.String(), "feature branch") {
		t.Errorf("expected the branch-left-on note on stdout, got:\n%s", buf.String())
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("local checkout must survive the run: %v", err)
	}
}

func TestPrintBarePrompt_LocalNoClone(t *testing.T) {
	gh := &fakeGitHub{cloneDir: t.TempDir()}
	r := newRunner(gh, &fakeClaude{})
	build := func(ctx BareContext) string {
		return fmt.Sprintf("BARE repo=%s issue=%d", ctx.RepoFullName, ctx.IssueNumber)
	}
	var buf bytes.Buffer
	if err := r.PrintBarePrompt(&buf, Options{IssueRef: "owner/repo#7", Local: true}, build); err != nil {
		t.Fatalf("PrintBarePrompt returned %v, want nil", err)
	}
	if gh.cloneCalls.Load() != 0 {
		t.Errorf("CloneRepo calls = %d, want 0 in local mode", gh.cloneCalls.Load())
	}
	if gh.cloneLocalCalls.Load() != 1 {
		t.Errorf("CloneRepoLocal calls = %d, want 1", gh.cloneLocalCalls.Load())
	}
	if !strings.HasPrefix(buf.String(), "BARE repo=owner/repo issue=7") {
		t.Errorf("unexpected bare prompt output: %q", buf.String())
	}
}
