package report

import "testing"

// TestTerminalStatus locks the shared STATUS-line parser every mutating
// session's report gate keys on: line-anchored scanning, last standalone
// verdict wins, markdown decoration and a trailing reason tolerated, and —
// crucially — an empty result for a session that yielded mid-work without a
// report, which is what the completion gates detect.
func TestTerminalStatus(t *testing.T) {
	const header = "## Implementation Report (issue #42)\n\n"
	cases := []struct {
		name string
		text string
		want string
	}{
		{"done", header + "STATUS: DONE", StatusDone},
		{"done with concerns", header + "STATUS: DONE_WITH_CONCERNS", StatusDoneWithConcerns},
		{"partial", header + "STATUS: PARTIAL", StatusPartial},
		{"blocked", header + "STATUS: BLOCKED", StatusBlocked},
		{"needs context", header + "STATUS: NEEDS_CONTEXT", StatusNeedsContext},
		{"no status line", header + "### Commits\n- abc1234 wip", ""},
		{"empty", "", ""},
		{"yielded mid-work blurb", "Waiting for the background test job before committing Commit 2.", ""},
		{"bold decoration", "**STATUS: DONE**", StatusDone},
		{"list marker", "- STATUS: BLOCKED", StatusBlocked},
		{"trailing reason after verdict", "STATUS: DONE_WITH_CONCERNS — flaky test left skipped", StatusDoneWithConcerns},
		{"terminal verdict wins over earlier line", "STATUS: BLOCKED\n\n(revised)\n\nSTATUS: DONE", StatusDone},
		{"format spec line is not a verdict", "STATUS: <DONE | DONE_WITH_CONCERNS | PARTIAL | BLOCKED | NEEDS_CONTEXT>", ""},
		{"unrecognized value ignored", "STATUS: MAYBE", ""},
		{"bare status prefix without value", "STATUS:", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := TerminalStatus(tc.text); got != tc.want {
				t.Errorf("TerminalStatus() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestTerminalStatus_ExtraVocabulary covers the widening the plan path needs:
// PLAN_READY is not a report verdict, so it must not satisfy the implement
// report guard, but a plan that closes with it must not read as whatever it
// mentioned earlier either.
func TestTerminalStatus_ExtraVocabulary(t *testing.T) {
	// An earlier standalone verdict followed by the plan's own closing line.
	plan := "STATUS: BLOCKED\n\nOn reflection the blocker cleared.\n\n**STATUS: PLAN_READY**"

	if got := TerminalStatus(plan); got != StatusBlocked {
		t.Errorf("TerminalStatus(plan) = %q, want %q — PLAN_READY is not a report verdict", got, StatusBlocked)
	}
	if got := TerminalStatus(plan, StatusPlanReady); got != StatusPlanReady {
		t.Errorf("TerminalStatus(plan, StatusPlanReady) = %q, want %q", got, StatusPlanReady)
	}
	if got := TerminalStatus("STATUS: DONE", StatusPlanReady); got != StatusDone {
		t.Errorf("extra vocabulary must not displace the report verdicts, got %q", got)
	}
	if got := TerminalStatus("STATUS: SOMETHING_ELSE", StatusPlanReady); got != "" {
		t.Errorf("unknown verdict = %q, want \"\"", got)
	}
}
