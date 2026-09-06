package elaborate

import "fmt"

// BodyBudget is the size an elaborated body is written to, in characters.
// GitHub caps a body at github.MaxIssueBodyLen (65,536) and a body over the
// cap is split into continuation comments, so the budget is not what keeps a
// write from failing. It sits well under the cap for two reasons: the split
// should stay the exception, since a body in comments is one every reader
// has to reassemble; and the body is injected whole into every planning,
// implementation, and verification prompt that reads the issue, where at
// roughly 10,000 tokens it is already the largest single block. The
// elaboration prompt and the skill-side format
// (plugins/planwerk/shared/issue-format-plan.md) state the same number.
const BodyBudget = 40000

// sizeGap returns the gap an over-budget body earns in the reviewer refine
// loop, or "" when the body fits. It names the moves that shrink a plan
// without losing what it decides, so the refine round tightens rather than
// drops.
func sizeGap(body string) string {
	if len(body) <= BodyBudget {
		return ""
	}
	return fmt.Sprintf("Size — the body is %d characters against a budget of %d. Tighten it without dropping a decision, a criterion, a citation, or an edge case: state each fact once, in the section that owns it (the Description says what changes and why, a criterion says how to observe it); cite path:line instead of quoting code the implementer will open anyway; cut a boundary that only narrates code nothing changes to the one sentence the plan needs; give a rejected alternative one sentence under Non-Goals.", len(body), BodyBudget)
}
