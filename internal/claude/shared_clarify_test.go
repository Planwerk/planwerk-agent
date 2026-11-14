package claude

import (
	"strings"
	"testing"

	"github.com/planwerk/planwerk-agent/internal/implement"
)

// The clarify skill reads two artifacts the commands post on an issue, the
// implementation plan and the implementation report, and finds its questions
// by the names of their sections and verdicts. Those names are written here,
// in the plan and implement prompts, so the two are free to drift; they may
// not: a section the planner renames is a source of questions the skill no
// longer opens. This test pins the names the skill reads to the prompts that
// produce them, the way shared_review_test.go pins the review skill's ladder.
const (
	clarifySkillDoc = "../../plugins/planwerk/skills/clarify/SKILL.md"
	sharedGitHubDoc = "../../plugins/planwerk/shared/github.md"
)

// TestClarifySkillReadsThePlanAsThePlannerWritesIt fails when the plan prompt
// stops emitting a section or verdict the clarify skill names, or the skill
// stops naming one.
func TestClarifySkillReadsThePlanAsThePlannerWritesIt(t *testing.T) {
	prompt := BuildPlanPrompt(implement.Context{RepoFullName: "o/r", IssueNumber: 7, IssueTitle: "t", IssueBody: "b"})
	skill := readSharedDoc(t, clarifySkillDoc)
	for _, name := range []string{
		"### Ground-Truth Notes",
		"### Assumptions",
		"### Risks & Open Questions",
		"### Status",
		"`STATUS:`",
		"`Next:`",
		"`PLAN_READY`",
		"`NEEDS_CONTEXT`",
		"`BLOCKED`",
	} {
		emitted := strings.Trim(name, "`")
		if !strings.Contains(prompt, emitted) {
			t.Errorf("the plan prompt no longer emits %q, which the clarify skill reads; change the two together", emitted)
		}
		if !strings.Contains(skill, name) {
			t.Errorf("%s no longer names %s, which the plan carries", clarifySkillDoc, name)
		}
	}
	if !strings.Contains(prompt, "on BLOCKED or NEEDS_CONTEXT only: the single action a human takes next") {
		t.Error("the plan prompt no longer puts the Next: line on BLOCKED and NEEDS_CONTEXT, where the clarify skill reads it")
	}
}

// TestClarifySkillReadsTheReportAsTheImplementSessionWritesIt fails when the
// implement prompt stops emitting what the clarify skill reads out of a posted
// implementation report, or when the shared document stops listing the
// verdicts the report can end in.
func TestClarifySkillReadsTheReportAsTheImplementSessionWritesIt(t *testing.T) {
	prompt := BuildImplementPrompt(implement.Context{RepoFullName: "o/r", IssueNumber: 7, IssueTitle: "t", IssueBody: "b"})
	skill := readSharedDoc(t, clarifySkillDoc)
	for _, name := range []string{"### Deviations from the issue", "`missing`", "`partial`", "`Next:`"} {
		emitted := strings.Trim(name, "`")
		if !strings.Contains(prompt, emitted) {
			t.Errorf("the implement prompt no longer emits %q, which the clarify skill reads; change the two together", emitted)
		}
		if !strings.Contains(skill, name) {
			t.Errorf("%s no longer names %s, which the implementation report carries", clarifySkillDoc, name)
		}
	}
	if !strings.Contains(prompt, "Status: <satisfied | unproven | partial | missing>") {
		t.Error("the implement prompt's Acceptance Criteria statuses changed; the clarify skill reads `missing` and `partial` out of them")
	}

	// The verdicts the shared document lists are the ones the report's STATUS
	// line offers, in the prompt's order.
	const verdicts = "STATUS: <DONE | DONE_WITH_CONCERNS | PARTIAL | BLOCKED | NEEDS_CONTEXT>"
	if !strings.Contains(prompt, verdicts) {
		t.Fatalf("the implement prompt no longer offers %q; change this test and %s with it", verdicts, sharedGitHubDoc)
	}
	doc := flow(readSharedDoc(t, sharedGitHubDoc))
	want := flow("ends in `STATUS: DONE`, `DONE_WITH_CONCERNS`, `PARTIAL`, `BLOCKED`, or `NEEDS_CONTEXT`")
	if !strings.Contains(doc, want) {
		t.Errorf("%s does not list the report's verdicts as %q", sharedGitHubDoc, want)
	}
	if !strings.Contains(prompt, implementReportHeading) || !strings.Contains(readSharedDoc(t, sharedGitHubDoc), "`"+implementReportHeading+" (issue #N)`") {
		t.Errorf("%s does not name the report by its heading %q", sharedGitHubDoc, implementReportHeading)
	}
}
