package claude

import (
	"errors"
	"regexp"
	"strings"
	"testing"
)

// swapRunSession replaces the runSessionFn seam with a scripted fake for the
// duration of one test and records every invocation. Tests using it must not
// run in parallel — the seam is package-level, mirroring the streamSinkFn
// override pattern.
type sessionCall struct {
	spec   runSpec
	prompt string
}

func swapRunSession(t *testing.T, script func(call int, spec runSpec, prompt string) (string, string, error)) *[]sessionCall {
	t.Helper()
	var calls []sessionCall
	restore := runSessionFn
	runSessionFn = func(_ *Client, spec runSpec, prompt string) (string, string, error) {
		calls = append(calls, sessionCall{spec: spec, prompt: prompt})
		return script(len(calls), spec, prompt)
	}
	t.Cleanup(func() { runSessionFn = restore })
	return &calls
}

const (
	completeImplementReport = "## Implementation Report (issue #42)\n\nSTATUS: DONE"
	// testResolvedModel stands in for the exact model id the envelope reports.
	testResolvedModel = "claude-opus-4-8"
)

// TestRunWithCompletionNudge_CompleteFirstTry locks the happy path: a session
// that ends with its report runs exactly once — no resume turn, no changed
// output — and the invocation pins a session id so a nudge would have been
// possible.
func TestRunWithCompletionNudge_CompleteFirstTry(t *testing.T) {
	calls := swapRunSession(t, func(int, runSpec, string) (string, string, error) {
		return completeImplementReport, testResolvedModel, nil
	})

	out, model, err := NewClient().runWithCompletionNudge(runSpec{label: "implement"}, "do the thing", implementReportHeading, implementReportStatusChoices)
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
	calls := swapRunSession(t, func(call int, _ runSpec, _ string) (string, string, error) {
		if call == 1 {
			return "Status so far: the envtest run exceeded the foreground cap and is finishing in the background; I'll report once the notification lands.", testResolvedModel, nil
		}
		return completeImplementReport, testResolvedModel, nil
	})

	spec := runSpec{dir: "/work/clone", label: "implement", permissionMode: "auto", model: "opus", agentsJSON: `{"implementer":{}}`}
	out, _, err := NewClient().runWithCompletionNudge(spec, "do the thing", implementReportHeading, implementReportStatusChoices)
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
	calls := swapRunSession(t, func(call int, _ runSpec, _ string) (string, string, error) {
		return "still no report", "", nil
	})

	out, _, err := NewClient().runWithCompletionNudge(runSpec{label: "implement"}, "p", implementReportHeading, implementReportStatusChoices)
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
	wantErr := errors.New("claude (model opus): exit status 1: api error 429")
	calls := swapRunSession(t, func(int, runSpec, string) (string, string, error) {
		return "", "", wantErr
	})

	_, _, err := NewClient().runWithCompletionNudge(runSpec{label: "fix"}, "p", fixReportHeading, reportStatusChoices)
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want the run error unchanged", err)
	}
	if len(*calls) != 1 {
		t.Errorf("session ran %d times, want 1 (no nudge after a run error)", len(*calls))
	}
}

// TestRunWithCompletionNudge_NudgeErrorReturnsIncompleteOutput covers the
// nudge turn itself failing (e.g. the usage limit hit between turns): the
// previous incomplete output must survive with a nil error so the caller can
// persist the session's account instead of losing it behind the nudge's error.
func TestRunWithCompletionNudge_NudgeErrorReturnsIncompleteOutput(t *testing.T) {
	calls := swapRunSession(t, func(call int, _ runSpec, _ string) (string, string, error) {
		if call == 1 {
			return "partial account of the work", testResolvedModel, nil
		}
		return "", "", errors.New("api error 429")
	})

	out, model, err := NewClient().runWithCompletionNudge(runSpec{label: "implement"}, "p", implementReportHeading, implementReportStatusChoices)
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
