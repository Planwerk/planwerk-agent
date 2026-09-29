package report

import (
	"slices"
	"testing"
)

// coverageReport wraps entries in an implementation report whose Work
// Breakdown Coverage section is followed by an Acceptance Criteria section.
func coverageReport(entries string) string {
	return "## Implementation Report (issue #42)\n\nDONE — shipped.\n\n### Work Breakdown Coverage\n" + entries + "### Acceptance Criteria\n- Golden file exists — done\n  - Status: satisfied\n### Status\nSTATUS: DONE\n"
}

// TestWorkBreakdownStates locks the reader the implement orchestrator keys on
// when a PARTIAL report contradicts its own coverage list: states in order,
// decoration tolerated, evidence continuations and the other sections ignored,
// and nil whenever the section names no package.
func TestWorkBreakdownStates(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		text string
		want []string
	}{
		{"three states in order", coverageReport("- 1. Parser — done — abc1234\n- 2. Runner — partial — def5678\n- 3. Docs — not started — nothing yet\n"), []string{"done", "partial", "not started"}},
		{"no heading", "## Implementation Report (issue #42)\n\n### Acceptance Criteria\n- A — done\n\nSTATUS: PARTIAL\n", nil},
		{"heading directly followed by the next section", coverageReport(""), nil},
		{"single-change sentinel", coverageReport("- None — the issue is a single undivided change\n"), nil},
		{"decorated sentinel", coverageReport("- **None** — the issue is a single undivided change\n"), nil},
		{"echoed instruction line", coverageReport("- (List EVERY work package the issue breaks the work into.)\n- A — done — abc1234\n"), []string{"done"}},
		{"decorated entries", coverageReport("- **WP1: Foo** — **done** — abc1234\n- 2. Bar - Done - def5678\n- Baz – not started – (nothing yet)\n"), []string{"done", "done", "not started"}},
		{"indented evidence bullet contributes nothing", coverageReport("- A — done — abc1234\n  - B — partial — the evidence names a partial helper\n    - C — not started\n"), []string{"done"}},
		{"unrecognized state", coverageReport("- Qux — finished — abc1234\n"), []string{""}},
		{"unrecognized state does not fall through to the evidence", coverageReport("- Helm chart — in progress — templates half-done\n"), []string{""}},
		{"state word inside the title", coverageReport("- Mark-as-done button — not started — nothing yet\n- WP2: done-callback retries — partial — def5678\n- WP3: Done marker handling — partial — abc1234\n"), []string{"not started", "partial", "partial"}},
		{"parenthesized package numbers", coverageReport("- (a) Parser — done — abc1234\n- (b) Renderer — not started — (nothing yet)\n"), []string{"done", "not started"}},
		{"titles starting with None", coverageReport("- None handling in config loader — partial — abc1234\n- None-handling in the loader — partial — def5678\n"), []string{"partial", "partial"}},
		{"state after a colon with evidence starting with done", coverageReport("- WP1 — done — abc1234\n- WP2: partial — done: the parser; the renderer is not started\n"), []string{"done", "partial"}},
		{"title starting with Done before the state", coverageReport("- WP1 — done — abc1234\n- WP3 — Done verdict for CI-only criteria — not started\n"), []string{"done", "not started"}},
		{"title starting with Done before a state with a note", coverageReport("- WP1 — done — abc1234\n- WP3 — Done verdict for CI-only criteria — not started (no commits yet)\n- WP4 — Done marker — partial, renderer missing\n"), []string{"done", "not started", "partial"}},
		{"shell comment in a fenced block stays in the section", coverageReport("- WP1 — done — verified with:\n  ```bash\n  # run the suite\n  make test\n  ```\n- WP2 — not started — nothing yet\n"), []string{"done", "not started"}},
		{"heading-like line inside a tilde fence that quotes a backtick fence", coverageReport("- WP1 — done — report sample:\n  ~~~markdown\n  ```bash\n  # run the suite\n  ```\n  ~~~\n- WP2 — not started — nothing yet\n"), []string{"done", "not started"}},
		{"line led by inline code opens no fence", coverageReport("- WP1 — done — verified with:\n  ```make test``` passes; its script:\n  ```bash\n  # run the suite\n  ```\n- WP2 — not started — nothing yet\n"), []string{"done", "not started"}},
		{"hash text and a sub-heading stay in the section", coverageReport("- WP1 Parser — done — a1b2c3d\n  #812's retry helper is reused here\n#### Remaining\n- WP3 Helm chart — not started — nothing yet\n"), []string{"done", "not started"}},
		{"star bullet with one space of indentation", coverageReport(" * A: done\n"), []string{"done"}},
		{"acceptance criteria bullet is not counted", coverageReport("- A — partial — abc1234\n"), []string{"partial"}},
		{"lower-case bold heading", "## **work breakdown coverage**\n- A — done — abc1234\n", []string{"done"}},
		{"tab-indented bullet is no entry", coverageReport("\t- A — done — abc1234\n"), nil},
		{"empty text", "", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := WorkBreakdownStates(tc.text); !slices.Equal(got, tc.want) || (got == nil) != (tc.want == nil) {
				t.Errorf("WorkBreakdownStates() = %#v, want %#v", got, tc.want)
			}
		})
	}
}

// TestWorkBreakdownComplete covers the one question the orchestrator asks:
// at least one package, and every package done.
func TestWorkBreakdownComplete(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		text string
		want bool
	}{
		{"two done entries", coverageReport("- A — done — abc1234\n- B — done — def5678\n"), true},
		{"done plus partial", coverageReport("- A — done — abc1234\n- B — partial — def5678\n"), false},
		{"partial state with evidence starting with done", coverageReport("- A — done — abc1234\n- B: partial — done: the parser\n"), false},
		{"single unrecognized entry", coverageReport("- A — finished — abc1234\n"), false},
		{"no packages", coverageReport("- None — the issue is a single undivided change\n"), false},
		{"empty text", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := WorkBreakdownComplete(tc.text); got != tc.want {
				t.Errorf("WorkBreakdownComplete() = %v, want %v", got, tc.want)
			}
		})
	}
}
