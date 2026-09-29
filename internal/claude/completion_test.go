package claude

import (
	"errors"
	"regexp"
	"strings"
	"testing"
)

// sessionCall is one session a scriptedClient ran: the spec and the prompt it
// was given.
type sessionCall struct {
	spec   runSpec
	prompt string
}

// scriptedClient returns a NewClient(opts...) whose sessions are answered by
// script instead of the claude CLI, with every invocation recorded in order.
// script receives the 1-based call number, so a test can answer the first turn
// and a resumed turn differently.
func scriptedClient(t *testing.T, script func(call int, spec runSpec, prompt string) (string, string, error), opts ...Option) (*Client, *[]sessionCall) {
	t.Helper()
	var calls []sessionCall
	c := NewClient(opts...)
	c.sessionFn = func(spec runSpec, prompt string) (string, string, error) {
		calls = append(calls, sessionCall{spec: spec, prompt: prompt})
		return script(len(calls), spec, prompt)
	}
	return c, &calls
}

const (
	completeImplementReport = "## Implementation Report (issue #42)\n\nSTATUS: DONE"
	// testResolvedModel stands in for the exact model id the envelope reports.
	testResolvedModel = "claude-opus-5-5"
)

// TestRunWithCompletionNudge_CompleteFirstTry locks the happy path: a session
// that ends with its report runs exactly once — no resume turn, no changed
// output — and the invocation pins a session id so a nudge would have been
// possible.
func TestRunWithCompletionNudge_CompleteFirstTry(t *testing.T) {
	t.Parallel()
	c, calls := scriptedClient(t, func(int, runSpec, string) (string, string, error) {
		return completeImplementReport, testResolvedModel, nil
	})

	out, model, err := c.runWithCompletionNudge(runSpec{label: "implement"}, "do the thing", implementReportHeading, implementReportStatusChoices)
	if err != nil {
		t.Fatalf("runWithCompletionNudge returned error: %v", err)
	}
	if out != completeImplementReport || model != testResolvedModel {
		t.Errorf("out=%q model=%q, want the session's own result", out, model)
	}
	if len(*calls) != 1 {
		t.Fatalf("session ran %d times, want 1 (no nudge for a complete report)", len(*calls))
	}
	first := (*calls)[0]
	if first.spec.resume {
		t.Error("first invocation must start a fresh session, not resume")
	}
	if !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(first.spec.sessionID) {
		t.Errorf("sessionID %q is not a v4 UUID; the CLI's --session-id requires one", first.spec.sessionID)
	}
}

// TestRunWithCompletionNudge_NudgeCompletes is the regression test for the
// reported failure: the implement session yielded to "wait" for a backgrounded
// test run and returned prose without the report. The runner must resume the
// SAME session (same id, resume set, same dir/model/agents) with the nudge
// prompt, and accept the report the resumed turn produces.
func TestRunWithCompletionNudge_NudgeCompletes(t *testing.T) {
	t.Parallel()
	c, calls := scriptedClient(t, func(call int, _ runSpec, _ string) (string, string, error) {
		if call == 1 {
			return "Status so far: the envtest run exceeded the foreground cap and is finishing in the background; I'll report once the notification lands.", testResolvedModel, nil
		}
		return completeImplementReport, testResolvedModel, nil
	})

	spec := runSpec{dir: "/work/clone", label: "implement", permissionMode: "auto", model: "opus", agentsJSON: `{"implementer":{}}`}
	out, _, err := c.runWithCompletionNudge(spec, "do the thing", implementReportHeading, implementReportStatusChoices)
	if err != nil {
		t.Fatalf("runWithCompletionNudge returned error: %v", err)
	}
	if out != completeImplementReport {
		t.Errorf("out = %q, want the resumed turn's report", out)
	}
	if len(*calls) != 2 {
		t.Fatalf("session ran %d times, want 2 (initial + one nudge)", len(*calls))
	}
	first, second := (*calls)[0], (*calls)[1]
	if !second.spec.resume {
		t.Error("nudge invocation must resume, not start fresh")
	}
	if second.spec.sessionID == "" || second.spec.sessionID != first.spec.sessionID {
		t.Errorf("nudge resumed session %q, want the pinned id %q", second.spec.sessionID, first.spec.sessionID)
	}
	if second.spec.dir != first.spec.dir || second.spec.model != first.spec.model || second.spec.agentsJSON != first.spec.agentsJSON || second.spec.permissionMode != first.spec.permissionMode {
		t.Errorf("nudge spec %+v drifted from the initial spec %+v", second.spec, first.spec)
	}
	for _, want := range []string{implementReportHeading, "KILLED", "FOREGROUND", "STATUS: <" + implementReportStatusChoices + ">"} {
		if !strings.Contains(second.prompt, want) {
			t.Errorf("nudge prompt does not contain %q:\n%s", want, second.prompt)
		}
	}
}

// TestRunWithCompletionNudge_GivesUpAfterBoundedNudges documents the bound: a
// session that never reports gets maxCompletionNudges resumed turns, then its
// last output is returned WITHOUT an error — the caller's own report gate
// decides what an incomplete output means (the implement orchestrator persists
// it as resumable progress).
func TestRunWithCompletionNudge_GivesUpAfterBoundedNudges(t *testing.T) {
	t.Parallel()
	c, calls := scriptedClient(t, func(call int, _ runSpec, _ string) (string, string, error) {
		return "still no report", "", nil
	})

	out, _, err := c.runWithCompletionNudge(runSpec{label: "implement"}, "p", implementReportHeading, implementReportStatusChoices)
	if err != nil {
		t.Fatalf("runWithCompletionNudge returned error: %v", err)
	}
	if out != "still no report" {
		t.Errorf("out = %q, want the last incomplete output for the caller's gate", out)
	}
	if want := 1 + maxCompletionNudges; len(*calls) != want {
		t.Errorf("session ran %d times, want %d (initial + %d nudges)", len(*calls), want, maxCompletionNudges)
	}
}

// TestRunWithCompletionNudge_RunErrorPropagates keeps genuine run failures (a
// hit rate limit, an exhausted turn budget) out of the nudge path: a follow-up
// turn cannot fix an API failure, so the error returns unchanged after one
// invocation.
func TestRunWithCompletionNudge_RunErrorPropagates(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("claude (model opus): exit status 1: api error 429")
	c, calls := scriptedClient(t, func(int, runSpec, string) (string, string, error) {
		return "", "", wantErr
	})

	_, _, err := c.runWithCompletionNudge(runSpec{label: "fix"}, "p", fixReportHeading, reportStatusChoices)
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want the run error unchanged", err)
	}
	if len(*calls) != 1 {
		t.Fatalf("session ran %d times, want 1 (no nudge after a run error)", len(*calls))
	}
	if (*calls)[0].spec.resume {
		t.Error("the only invocation resumed a session; a run error must not reach the nudge")
	}
}

// TestRunWithCompletionNudge_NudgeErrorReturnsIncompleteOutput covers the
// nudge turn itself failing (e.g. the usage limit hit between turns): the
// previous incomplete output must survive with a nil error so the caller can
// persist the session's account instead of losing it behind the nudge's error.
func TestRunWithCompletionNudge_NudgeErrorReturnsIncompleteOutput(t *testing.T) {
	t.Parallel()
	c, calls := scriptedClient(t, func(call int, _ runSpec, _ string) (string, string, error) {
		if call == 1 {
			return "partial account of the work", testResolvedModel, nil
		}
		return "", "", errors.New("api error 429")
	})

	out, model, err := c.runWithCompletionNudge(runSpec{label: "implement"}, "p", implementReportHeading, implementReportStatusChoices)
	if err != nil {
		t.Fatalf("runWithCompletionNudge returned error: %v", err)
	}
	if out != "partial account of the work" || model != testResolvedModel {
		t.Errorf("out=%q model=%q, want the pre-nudge output preserved", out, model)
	}
	if len(*calls) != 2 {
		t.Errorf("session ran %d times, want 2 (the failed nudge ends the loop)", len(*calls))
	}
}

// TestImplement_CarriesTheStatusContractInTheSystemPrompt is the regression
// test for a session that lost its verdict definitions to compaction: the
// implement session gets ImplementSystemPrompt on its first turn, and the
// nudge turn that resumes a session ending without its report gets it again.
func TestImplement_CarriesTheStatusContractInTheSystemPrompt(t *testing.T) {
	t.Parallel()
	c, calls := scriptedClient(t, func(call int, _ runSpec, _ string) (string, string, error) {
		if call == 1 {
			return "Every package is committed; the e2e suites only run in CI.", testResolvedModel, nil
		}
		return completeImplementReport, testResolvedModel, nil
	})

	if _, _, err := c.Implement("", goldenImplementContext()); err != nil {
		t.Fatalf("Implement returned error: %v", err)
	}
	if len(*calls) != 2 {
		t.Fatalf("session ran %d times, want 2 (initial + one nudge)", len(*calls))
	}
	for i, call := range *calls {
		if call.spec.appendSystemPrompt != ImplementSystemPrompt() {
			t.Errorf("call %d: appendSystemPrompt = %q, want ImplementSystemPrompt()", i+1, call.spec.appendSystemPrompt)
		}
	}
	if first, second := (*calls)[0], (*calls)[1]; !second.spec.resume || second.spec.sessionID != first.spec.sessionID {
		t.Errorf("nudge turn resume=%v session=%q, want a resume of %q", second.spec.resume, second.spec.sessionID, first.spec.sessionID)
	}
}

// TestTerminalReportComplete locks the gate the nudge keys on: heading AND
// terminal STATUS line, so a yielded-mid-work blurb, a bare status without the
// heading, and a heading without a verdict all fail.
func TestTerminalReportComplete(t *testing.T) {
	complete := terminalReportComplete(implementReportHeading)
	cases := []struct {
		name string
		out  string
		want bool
	}{
		{"complete report", completeImplementReport, true},
		{"partial verdict is complete", "## Implementation Report (issue #7)\n\nSTATUS: PARTIAL — envtest outstanding", true},
		{"yielded mid-work blurb", "Waiting for the background test job before committing.", false},
		{"heading without status", "## Implementation Report (issue #7)\n\n### Commits\n- abc1234 wip", false},
		{"status without heading", "All done.\n\nSTATUS: DONE", false},
		{"empty", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := complete(tc.out); got != tc.want {
				t.Errorf("terminalReportComplete(%q) = %v, want %v", tc.out, got, tc.want)
			}
		})
	}
}
