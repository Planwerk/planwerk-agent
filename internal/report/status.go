package report

import (
	"slices"
	"strings"
)

// Terminal STATUS markers a session's structured report can end with. Every
// mutating session (implement, simplify-apply, review-apply, finalize, fix)
// closes its report with a "STATUS: <verdict>" line; the orchestrators key
// their control flow on it. The constants live here — in the leaf package both
// internal/claude and internal/implement already import — so the parser and
// its vocabulary exist exactly once instead of being duplicated per package
// (internal/fix keeps its own escalation-oriented parser, whose first-match
// semantics and Status type serve the fix loop specifically).
const (
	StatusDone             = "DONE"
	StatusDoneWithConcerns = "DONE_WITH_CONCERNS"
	StatusPartial          = "PARTIAL"
	StatusBlocked          = "BLOCKED"
	StatusNeedsContext     = "NEEDS_CONTEXT"
	// StatusPlanReady closes a planning session's plan rather than a report.
	// It is deliberately NOT in the default vocabulary: the implement guard
	// treats a missing verdict as "the session did not finish", and a plan
	// verdict must not satisfy it. Pass it to TerminalStatus explicitly where a
	// plan is being read.
	StatusPlanReady = "PLAN_READY"
)

// TerminalStatus returns the report's terminal STATUS verdict (DONE,
// DONE_WITH_CONCERNS, PARTIAL, BLOCKED, or NEEDS_CONTEXT), or "" when the
// report carries no recognized STATUS line — what a session that yielded
// mid-work returns.
//
// It scans line-anchored and lets the last standalone verdict win, so a
// mid-sentence mention of a status value ("the session emits STATUS: BLOCKED
// when stuck") is not mistaken for the verdict; it additionally tolerates the
// markdown decoration a model sometimes adds (a leading list marker or
// heading, surrounding bold/backticks) and a trailing reason after the verdict
// word.
//
// extra widens the recognized vocabulary for artifacts that close with a
// verdict a report never carries — a plan ends with StatusPlanReady. Widening
// it matters for more than recognizing that one word: the last verdict only
// wins among words the parser knows, so a plan that mentions BLOCKED before
// closing with an unrecognized PLAN_READY would otherwise read as BLOCKED.
func TerminalStatus(text string, extra ...string) string {
	verdict := ""
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimLeft(strings.TrimSpace(raw), "-*#> \t")
		if !strings.HasPrefix(strings.ToUpper(line), "STATUS:") {
			continue
		}
		fields := strings.Fields(strings.TrimSpace(line[len("STATUS:"):]))
		if len(fields) == 0 {
			continue
		}
		word := strings.ToUpper(strings.Trim(fields[0], "*`_"))
		switch word {
		case StatusDone, StatusDoneWithConcerns, StatusPartial, StatusBlocked, StatusNeedsContext:
			verdict = word
		default:
			if slices.Contains(extra, word) {
				verdict = word
			}
		}
	}
	return verdict
}
