package claude

import (
	"strings"
	"testing"
)

// The implement skill reviews its own plan before the author sees it (decision
// 178), against the checks the command's plan reviewer runs in a fresh session
// (decision 158). The plugin cannot read the binary's prompts, so the skill
// carries its own copy of the nine checks and of what counts as a gap; the
// test below fails when that copy drifts from what buildPlanReviewPrompt
// renders, the way shared_simplify_test.go pins the simplify pass.

// planReviewMarkers are the phrases the plan-review prompt and the skill must
// both carry: at least one per check, and the two sentences that bound what a
// gap is. Each is kept short so a rewording elsewhere in either text passes
// while a dropped check fails.
var planReviewMarkers = []string{
	// The stance: a plan is not correct because it reads well.
	"because it looks thorough",
	// The nine checks, in the prompt's order.
	"Criterion coverage",
	"Work-package coverage",
	"lettered workstreams",
	"Over-scope",
	"a new top-level package, a file the issue never implies, a refactor, rename, or dependency bump",
	"plans work the issue's Non-Goals exclude",
	"Single delivery",
	"is scoping, not deferral",
	"Ground truth",
	"every path it creates does not exist yet",
	"Domain coverage",
	"A domain the change does not touch is never a gap",
	"names its test file or function and the error or edge path it exercises",
	"every command exists in the repository's own tooling",
	// What a gap is.
	"A single citation that does not hold is a gap",
	"Minor wording and stylistic preferences are not gaps",
}

// TestImplementSkillMatchesThePlanReviewPrompt fails when the plan-review
// prompt or the implement skill stops carrying a marker of the plan review.
func TestImplementSkillMatchesThePlanReviewPrompt(t *testing.T) {
	prompt := flow(buildPlanReviewPrompt(goldenImplementContext(), planReviewTestPlan))
	skill := flow(readSharedDoc(t, implementSkillDoc))
	for _, marker := range planReviewMarkers {
		m := flow(marker)
		if !strings.Contains(prompt, m) {
			t.Errorf("the plan-review prompt no longer carries %q; change the marker and the implement skill together", marker)
		}
		if !strings.Contains(skill, m) {
			t.Errorf("%s does not carry %q; the skill's plan review drifted from the plan-review prompt", implementSkillDoc, marker)
		}
	}
}
