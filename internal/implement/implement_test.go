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

	"github.com/planwerk/planwerk-review/internal/attribution"
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

type fakeAdversarialVerifier struct {
	called atomic.Int32
	base   string
	result *report.ReviewResult
	err    error
}

func (f *fakeAdversarialVerifier) AdversarialReview(dir, baseBranch string) (*report.ReviewResult, error) {
	f.called.Add(1)
	f.base = baseBranch
	return f.result, f.err
}

type fakePlanner struct {
	called atomic.Int32
	dir    string
	plan   string
	err    error
}

func (f *fakePlanner) Plan(dir string, ctx Context) (string, error) {
	f.called.Add(1)
	f.dir = dir
	return f.plan, f.err
}

func TestRun_PlanFeedsImplement(t *testing.T) {
	gh := &fakeGitHub{issue: sampleIssue(), cloneDir: t.TempDir()}
	cl := &fakeClaude{report: "PR opened"}
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
	if !strings.Contains(gh.commentBodies[0], planCommentFooter()) {
		t.Errorf("first posted comment is missing the plan attribution footer:\n%s", gh.commentBodies[0])
	}
	if !strings.Contains(gh.commentBodies[1], cl.report) {
		t.Errorf("second posted comment %q does not contain the report %q", gh.commentBodies[1], cl.report)
	}
	if !strings.Contains(gh.commentBodies[1], reportCommentFooter()) {
		t.Errorf("second posted comment is missing the report attribution footer:\n%s", gh.commentBodies[1])
	}
	if !strings.Contains(buf.String(), "Posted the implementation plan as a comment on issue #42") {
		t.Errorf("missing plan-comment confirmation in output:\n%s", buf.String())
	}
}

func TestRun_NoPlanCommentSkipsComment(t *testing.T) {
	gh := &fakeGitHub{issue: sampleIssue(), cloneDir: t.TempDir()}
	cl := &fakeClaude{report: "PR opened"}
	fp := &fakePlanner{plan: "## Implementation Plan (issue #42)\n\nSTATUS: PLAN_READY"}
	r := newRunner(gh, cl)
	r.Planner = fp

	if err := r.Run(&bytes.Buffer{}, Options{IssueRef: "owner/repo#42", NoPlanComment: true}); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if gh.commentCalls.Load() != 1 {
		t.Fatalf("AddIssueComment called %d times, want 1 — --no-plan-comment skips only the plan, the report still posts", gh.commentCalls.Load())
	}
	if !strings.Contains(gh.commentBodies[0], reportCommentFooter()) {
		t.Errorf("the posted comment should be the report, got:\n%s", gh.commentBodies[0])
	}
	if strings.Contains(gh.commentBodies[0], planCommentFooter()) {
		t.Errorf("--no-plan-comment must suppress the plan comment, but it was posted:\n%s", gh.commentBodies[0])
	}
	if cl.ctx.Plan != fp.plan {
		t.Errorf("implement received Plan %q, want the planner's plan even when the comment is skipped", cl.ctx.Plan)
	}
}

func TestRun_PlanCommentFailureIsNonFatal(t *testing.T) {
	gh := &fakeGitHub{issue: sampleIssue(), cloneDir: t.TempDir(), commentErr: errors.New("github down")}
	cl := &fakeClaude{report: "PR opened"}
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
	cl := &fakeClaude{report: "## Implementation Report (issue #42)\n\nPR opened"}
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
	if !strings.Contains(gh.commentBodies[0], reportCommentFooter()) {
		t.Errorf("posted comment is missing the attribution footer:\n%s", gh.commentBodies[0])
	}
	if !strings.Contains(buf.String(), "Posted the implementation report as a comment on issue #42") {
		t.Errorf("missing report-comment confirmation in output:\n%s", buf.String())
	}
}

func TestRun_ReportCommentFailureIsNonFatal(t *testing.T) {
	gh := &fakeGitHub{issue: sampleIssue(), cloneDir: t.TempDir(), commentErr: errors.New("github down")}
	cl := &fakeClaude{report: "PR opened"}
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

func TestRun_EmptyReportSkipsReportComment(t *testing.T) {
	gh := &fakeGitHub{issue: sampleIssue(), cloneDir: t.TempDir()}
	cl := &fakeClaude{report: ""}
	r := newRunner(gh, cl)

	if err := r.Run(&bytes.Buffer{}, Options{IssueRef: "owner/repo#42"}); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if gh.commentCalls.Load() != 0 {
		t.Errorf("AddIssueComment called %d times, want 0 — an empty report posts no comment", gh.commentCalls.Load())
	}
}

func TestRun_NoReportCommentSkipsReportComment(t *testing.T) {
	gh := &fakeGitHub{issue: sampleIssue(), cloneDir: t.TempDir()}
	cl := &fakeClaude{report: "PR opened"}
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
	cl := &fakeClaude{report: "PR opened"}
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
	cl := &fakeClaude{report: "PR opened"}
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

func TestStripPlanCommentFooter(t *testing.T) {
	const plan = "## Implementation Plan (issue #42)\n\n### Summary\n- do the thing\n\nSTATUS: PLAN_READY"
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"round-trips a formatted plan comment", formatPlanComment(plan), plan},
		{"no footer returned trimmed", "  " + plan + "  ", plan},
		{"trailing separator and whitespace stripped", plan + "\n\n---\n\n" + planCommentFooter() + "\n\n", plan},
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
	reportComment := "## Implementation Report (issue #42)\n\ndone\n\n---\n\n" + reportCommentFooter() + "\n"

	cases := []struct {
		name string
		in   []github.IssueComment
		want string
	}{
		{
			name: "picks the last comment carrying both markers",
			in: []github.IssueComment{
				{Body: formatPlanComment(planA)},
				{Body: formatPlanComment(planB)},
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
			in:   []github.IssueComment{{Body: "random text\n\n---\n\n" + planCommentFooter() + "\n"}},
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
				{Body: formatPlanComment(planA)},
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

	// The current run resolved a different model; detection must not depend on it.
	attribution.SetModel("claude-opus-4-8")
	t.Cleanup(func() { attribution.SetModel("") })

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
		comments: []github.IssueComment{{Body: formatPlanComment(plan)}},
	}
	cl := &fakeClaude{report: "PR opened"}
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
	if strings.Contains(gh.commentBodies[0], planCommentFooter()) {
		t.Errorf("reuse must not re-post the plan, but a plan comment was posted:\n%s", gh.commentBodies[0])
	}
	if !strings.Contains(gh.commentBodies[0], reportCommentFooter()) {
		t.Errorf("the single posted comment should be the report, got:\n%s", gh.commentBodies[0])
	}
}

func TestRun_NoPlanReuseForcesFreshPlan(t *testing.T) {
	const posted = "## Implementation Plan (issue #42)\n\nstale\n\nSTATUS: PLAN_READY"
	gh := &fakeGitHub{
		issue:    sampleIssue(),
		cloneDir: t.TempDir(),
		comments: []github.IssueComment{{Body: formatPlanComment(posted)}},
	}
	cl := &fakeClaude{report: "PR opened"}
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
				comments: []github.IssueComment{{Body: formatPlanComment(plan)}},
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
		comments: []github.IssueComment{{Body: formatPlanComment(posted)}},
	}
	cl := &fakeClaude{report: "PR opened"}
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

func TestRun_VerifyAdversarialReportsFindings(t *testing.T) {
	gh := &fakeGitHub{issue: sampleIssue(), cloneDir: t.TempDir()}
	cl := &fakeClaude{report: "PR opened"}
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
	cl := &fakeClaude{report: "PR opened"}
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
	cl := &fakeClaude{report: "PR opened"}
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

// fakeGitHub is a scripted GitHubClient. The test sets the canned issue and
// optional clone error; both calls record their invocation count so each
// test can assert exactly which steps the runner reached.
type fakeGitHub struct {
	issue    *github.Issue
	issueErr error

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

func TestRun_LocalNoClone(t *testing.T) {
	dir := t.TempDir()
	gh := &fakeGitHub{issue: sampleIssue(), cloneDir: dir}
	cl := &fakeClaude{report: "PR opened"}
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
