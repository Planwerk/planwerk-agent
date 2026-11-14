package implement

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/planwerk/planwerk-agent/internal/github"
	"github.com/planwerk/planwerk-agent/internal/github/githubtest"
)

// TestIssueFingerprint locks what counts as the same issue for a posted plan:
// the edits that change nothing there is to plan keep the fingerprint, and
// every other edit to the title or the body changes it.
func TestIssueFingerprint(t *testing.T) {
	const title = "Add foo widget"
	const body = "## Description\n\nFoo does X.\n\n## Acceptance Criteria\n\n- [ ] foo() returns X\n- [ ] bar() is gone\n"
	want := issueFingerprint(title, body)
	if len(want) != 64 {
		t.Fatalf("issueFingerprint() = %q, want 64 hex characters", want)
	}

	for _, tc := range []struct {
		name  string
		title string
		body  string
		same  bool
	}{
		{"CRLF line endings", title, strings.ReplaceAll(body, "\n", "\r\n"), true},
		{"trailing whitespace on a line and at the end", title, strings.ReplaceAll(body, "Foo does X.", "Foo does X.  \t") + "\n\n", true},
		{"surrounding whitespace in the title", "  " + title + "\n", body, true},
		{"a ticked criterion", title, strings.Replace(body, "- [ ] foo()", "- [x] foo()", 1), true},
		{"a criterion ticked with a capital X", title, strings.Replace(body, "- [ ] bar()", "- [X] bar()", 1), true},
		{"a reworded criterion", title, strings.Replace(body, "foo() returns X", "foo() returns Y", 1), false},
		{"an added criterion", title, body + "- [ ] baz() exists\n", false},
		{"a changed title", "Add bar widget", body, false},
		{"a bracketed x that is no checkbox", title, strings.Replace(body, "Foo does X.", "Foo does [x].", 1), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := issueFingerprint(tc.title, tc.body); (got == want) != tc.same {
				t.Errorf("issueFingerprint() same as the original = %v, want %v", got == want, tc.same)
			}
		})
	}
}

// TestPlannedBodyFingerprint locks where the fingerprint is read from: the
// line under the footer, never a marker the plan text itself quotes.
func TestPlannedBodyFingerprint(t *testing.T) {
	fp := issueFingerprint("t", "b")
	other := issueFingerprint("t", "other")
	const plan = "## Implementation Plan (issue #42)\n\nSTATUS: PLAN_READY"
	quoting := plan + "\n\n" + fmt.Sprintf(plannedBodyMarkerFmt, other)

	for _, tc := range []struct {
		name    string
		comment string
		want    string
	}{
		{"a plan posted with a fingerprint", formatPlanComment(plan, "m", fp), fp},
		{"a plan an older build posted", formatPlanComment(plan, "m", ""), ""},
		{"a plan that quotes the marker and records none", formatPlanComment(quoting, "m", ""), ""},
		{"a plan that quotes the marker and records its own", formatPlanComment(quoting, "m", fp), fp},
		{"a comment that is no plan", fmt.Sprintf(plannedBodyMarkerFmt, fp), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := plannedBodyFingerprint(tc.comment); got != tc.want {
				t.Errorf("plannedBodyFingerprint() = %q, want %q", got, tc.want)
			}
		})
	}

	// The marker never opens the comment, where a continuation marker would be
	// looked for, and the plan is fed on without it.
	comment := formatPlanComment(plan, "m", fp)
	if !strings.HasPrefix(comment, planHeading) {
		t.Errorf("the plan comment no longer opens with its heading: %q", comment[:40])
	}
	if got, plannedFor := mostRecentPlanComment([]github.IssueComment{{Body: comment}}); got != plan || plannedFor != fp {
		t.Errorf("mostRecentPlanComment() = (%q, %q), want (%q, %q)", got, plannedFor, plan, fp)
	}
}

// TestRun_PostedPlanRecordsTheIssueItWasPlannedFor locks the write side: a
// fresh plan's comment carries the fingerprint of the issue the run fetched.
func TestRun_PostedPlanRecordsTheIssueItWasPlannedFor(t *testing.T) {
	issue := sampleIssue()
	gh := &githubtest.Fake{Issue: issue, Dir: t.TempDir()}
	r := newRunner(gh, &fakeClaude{report: validImplReport})
	r.Planner = &fakePlanner{plan: "## Implementation Plan (issue #42)\n\nfresh\n\nSTATUS: PLAN_READY"}

	if err := r.Run(&bytes.Buffer{}, Options{IssueRef: "owner/repo#42"}); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	comments := gh.Comments()
	if len(comments) == 0 {
		t.Fatal("no comment was posted, want the plan")
	}
	want := fmt.Sprintf(plannedBodyMarkerFmt, issueFingerprint(issue.Title, issue.Body))
	if !strings.HasSuffix(strings.TrimSpace(comments[0]), want) {
		t.Errorf("the plan comment does not end with %s:\n%s", want, comments[0])
	}
}

// TestRun_PlanForTheSameIssueIsReused locks that a fingerprint that still
// matches changes nothing about reuse, a ticked criterion included.
func TestRun_PlanForTheSameIssueIsReused(t *testing.T) {
	const plan = "## Implementation Plan (issue #42)\n\nreuse me\n\nSTATUS: PLAN_READY"
	issue := sampleIssue()
	issue.Body = "## Acceptance Criteria\n\n- [ ] foo()\n"
	plannedFor := issueFingerprint(issue.Title, issue.Body)
	issue.Body = "## Acceptance Criteria\r\n\r\n- [x] foo()\r\n"
	gh := &githubtest.Fake{
		Issue:         issue,
		Dir:           t.TempDir(),
		IssueComments: []github.IssueComment{{Body: formatPlanComment(plan, "m", plannedFor)}},
	}
	cl := &fakeClaude{report: validImplReport}
	fp := &fakePlanner{plan: "FRESH PLAN should not be used"}
	r := newRunner(gh, cl)
	r.Planner = fp

	if err := r.Run(&bytes.Buffer{}, Options{IssueRef: "owner/repo#42"}); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if fp.called.Load() != 0 {
		t.Errorf("planner called %d times, want 0: the issue reads as it did when the plan was posted", fp.called.Load())
	}
	if cl.ctx.Plan != plan {
		t.Errorf("implement received Plan %q, want the reused plan %q", cl.ctx.Plan, plan)
	}
}

// TestRun_PlanForAnEarlierBodyIsPlannedAfresh locks the read side: a posted
// plan whose fingerprint no longer matches the issue is passed over, whatever
// its verdict, and the fresh plan is posted with the new fingerprint. The
// NEEDS_CONTEXT case is the run after a clarify pass rewrote the body.
func TestRun_PlanForAnEarlierBodyIsPlannedAfresh(t *testing.T) {
	for _, status := range []string{"PLAN_READY", "NEEDS_CONTEXT", "BLOCKED"} {
		t.Run(status, func(t *testing.T) {
			issue := sampleIssue()
			stale := formatPlanComment("## Implementation Plan (issue #42)\n\nstale\n\nSTATUS: "+status, "m", issueFingerprint(issue.Title, "the body before it was clarified"))
			gh := &githubtest.Fake{
				Issue:         issue,
				Dir:           t.TempDir(),
				IssueComments: []github.IssueComment{{Body: stale}},
			}
			cl := &fakeClaude{report: validImplReport}
			fp := &fakePlanner{plan: "## Implementation Plan (issue #42)\n\nfresh\n\nSTATUS: PLAN_READY"}
			r := newRunner(gh, cl)
			r.Planner = fp

			var buf bytes.Buffer
			if err := r.Run(&buf, Options{IssueRef: "owner/repo#42"}); err != nil {
				t.Fatalf("Run returned %v, want nil", err)
			}
			if fp.called.Load() != 1 {
				t.Errorf("planner called %d times, want 1: the posted plan was written against another body", fp.called.Load())
			}
			if cl.ctx.Plan != fp.plan {
				t.Errorf("implement received Plan %q, want the fresh plan %q", cl.ctx.Plan, fp.plan)
			}
			if !strings.Contains(buf.String(), "was written against an earlier title or body of the issue; planning afresh") {
				t.Errorf("the run does not say why it planned afresh:\n%s", buf.String())
			}
			if strings.Contains(buf.String(), "Reusing the implementation plan") {
				t.Errorf("the run announced a reuse it did not do:\n%s", buf.String())
			}
			want := fmt.Sprintf(plannedBodyMarkerFmt, issueFingerprint(issue.Title, issue.Body))
			if comments := gh.Comments(); len(comments) == 0 || !strings.Contains(comments[0], want) {
				t.Errorf("the fresh plan was not posted with the fingerprint of the issue as it reads now (%s): %v", want, comments)
			}
		})
	}
}

// TestRun_EscalatedPlanForTheSameIssueAborts locks that an unchanged issue
// still stops at its posted BLOCKED or NEEDS_CONTEXT plan, and that the abort
// names the way out a fingerprinted plan has: change the issue, then rerun.
func TestRun_EscalatedPlanForTheSameIssueAborts(t *testing.T) {
	for _, status := range []string{"BLOCKED", "NEEDS_CONTEXT"} {
		t.Run(status, func(t *testing.T) {
			issue := sampleIssue()
			posted := formatPlanComment("## Implementation Plan (issue #42)\n\nnope\n\nSTATUS: "+status, "m", issueFingerprint(issue.Title, issue.Body))
			gh := &githubtest.Fake{
				Issue:         issue,
				Dir:           t.TempDir(),
				IssueComments: []github.IssueComment{{Body: posted}},
			}
			cl := &fakeClaude{report: "unused"}
			fp := &fakePlanner{plan: "unused"}
			r := newRunner(gh, cl)
			r.Planner = fp

			err := r.Run(&bytes.Buffer{}, Options{IssueRef: "owner/repo#42"})
			if err == nil || !strings.Contains(err.Error(), status) {
				t.Fatalf("Run returned %v, want the %s escalation error", err, status)
			}
			if !strings.Contains(err.Error(), "a run plans afresh once the issue body has changed") {
				t.Errorf("the abort does not name the way out: %v", err)
			}
			if strings.Contains(err.Error(), "--no-plan-reuse") {
				t.Errorf("the abort still asks for --no-plan-reuse, which a fingerprinted plan does not need: %v", err)
			}
			if fp.called.Load() != 0 || cl.called.Load() != 0 {
				t.Errorf("planner called %d times and implement %d, want 0 and 0", fp.called.Load(), cl.called.Load())
			}
		})
	}
}
