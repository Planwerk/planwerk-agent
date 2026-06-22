package claude

import (
	"errors"
	"strings"
	"testing"

	"github.com/planwerk/planwerk-review/internal/report"
)

type sample struct {
	A int    `json:"a"`
	B string `json:"b"`
}

func TestDecodeJSONWithRepair_ValidNoRepair(t *testing.T) {
	// The common case: valid JSON decodes without ever invoking the repair path.
	called := false
	restore := repairJSON
	repairJSON = func(*Client, string, error, string) (string, error) {
		called = true
		return "", errors.New("repair must not be called for valid JSON")
	}
	t.Cleanup(func() { repairJSON = restore })

	var got sample
	if err := (&Client{}).decodeJSONWithRepair("```json\n{\"a\":5,\"b\":\"x\"}\n```", "test", &got); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if called {
		t.Error("repair was called for valid JSON")
	}
	if got.A != 5 || got.B != "x" {
		t.Errorf("decoded %+v, want {A:5 B:x}", got)
	}
}

func TestDecodeJSONWithRepair_RepairsMalformed(t *testing.T) {
	restore := repairJSON
	repairJSON = func(_ *Client, malformed string, parseErr error, label string) (string, error) {
		if parseErr == nil {
			t.Error("repair should receive the original parse error")
		}
		return `{"a":7,"b":"fixed"}`, nil
	}
	t.Cleanup(func() { repairJSON = restore })

	var got sample
	if err := (&Client{}).decodeJSONWithRepair(`{"a":7,"b":"fixed"`, "test", &got); err != nil {
		t.Fatalf("expected repair to succeed, got: %v", err)
	}
	if got.A != 7 || got.B != "fixed" {
		t.Errorf("decoded %+v after repair, want {A:7 B:fixed}", got)
	}
}

func TestDecodeJSONWithRepair_RepairCallFails(t *testing.T) {
	restore := repairJSON
	repairJSON = func(*Client, string, error, string) (string, error) {
		return "", errors.New("claude unavailable")
	}
	t.Cleanup(func() { repairJSON = restore })

	var got sample
	if err := (&Client{}).decodeJSONWithRepair(`not json`, "test", &got); err == nil {
		t.Error("expected an error when repair fails")
	}
}

func TestDecodeJSONWithRepair_RepairStillInvalid(t *testing.T) {
	restore := repairJSON
	repairJSON = func(*Client, string, error, string) (string, error) {
		return `still { not json`, nil
	}
	t.Cleanup(func() { repairJSON = restore })

	var got sample
	if err := (&Client{}).decodeJSONWithRepair(`bad`, "test", &got); err == nil {
		t.Error("expected an error when repaired output is still invalid")
	}
}

// A model that prepends a sentence before a fenced JSON block is recovered
// locally — the embedded value is extracted, so no repair call is needed.
func TestDecodeJSONWithRepair_PreambleNoRepair(t *testing.T) {
	called := false
	restore := repairJSON
	repairJSON = func(*Client, string, error, string) (string, error) {
		called = true
		return "", errors.New("repair must not be called when the value is recoverable")
	}
	t.Cleanup(func() { repairJSON = restore })

	preamble := "Removing the prose preamble yields valid JSON:\n\n```json\n{\"a\":5,\"b\":\"x\"}\n```"
	var got sample
	if err := (&Client{}).decodeJSONWithRepair(preamble, "test", &got); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if called {
		t.Error("repair was called for a recoverable preamble")
	}
	if got.A != 5 || got.B != "x" {
		t.Errorf("decoded %+v, want {A:5 B:x}", got)
	}
}

// The exact production failure: the initial parse fails, the repair call's
// output itself carries a prose preamble before the fenced JSON ("The error is
// caused by the prose preamble ... Removing it yields valid JSON:"). The retry
// parse must still recover the embedded object instead of choking on 'T'.
func TestDecodeJSONWithRepair_RetryPreamble(t *testing.T) {
	restore := repairJSON
	repairJSON = func(*Client, string, error, string) (string, error) {
		return "The error is caused by the prose preamble before the JSON object. " +
			"Removing it yields valid JSON:\n\n```json\n{\"a\":7,\"b\":\"fixed\"}\n```", nil
	}
	t.Cleanup(func() { repairJSON = restore })

	var got sample
	if err := (&Client{}).decodeJSONWithRepair(`{"a":7,"b":"fixed"`, "test", &got); err != nil {
		t.Fatalf("expected the retry preamble to be recovered, got: %v", err)
	}
	if got.A != 7 || got.B != "fixed" {
		t.Errorf("decoded %+v after retry, want {A:7 B:fixed}", got)
	}
}

func TestExtractJSONValue(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain object", `{"a":1}`, `{"a":1}`},
		{"prose preamble + fence", "note:\n```json\n{\"a\":1}\n```", `{"a":1}`},
		{"trailing commentary", `{"a":1}` + "\nThat is the answer.", `{"a":1}`},
		{"brace inside string", `{"k":"a}b{c"}`, `{"k":"a}b{c"}`},
		{"escaped quote inside string", `{"k":"he said \"hi}\""}`, `{"k":"he said \"hi}\""}`},
		{"array of objects", `prefix [{"a":1},{"b":2}] suffix`, `[{"a":1},{"b":2}]`},
		{"no json value", `just prose, nothing structured`, `just prose, nothing structured`},
		{"unbalanced left untouched", `{"a":1`, `{"a":1`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := extractJSONValue(tc.in); got != tc.want {
				t.Errorf("extractJSONValue(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func validFinding() report.Finding {
	return report.Finding{
		Title:      "Missing error wrapping",
		Severity:   report.SeverityWarning,
		Confidence: report.ConfidenceVerified,
		Problem:    "p",
		Action:     "a",
	}
}

func TestRepairInvalidReview_ValidNoRepair(t *testing.T) {
	// A schema-valid review must not trigger a repair call.
	restore := repairInvalidJSON
	repairInvalidJSON = func(*Client, string, error, string) (string, error) {
		t.Error("repair must not be called for a valid review")
		return "", errors.New("repair must not be called")
	}
	t.Cleanup(func() { repairInvalidJSON = restore })

	result := &report.ReviewResult{Findings: []report.Finding{validFinding()}}
	if err := (&Client{}).repairInvalidReview(result); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRepairInvalidReview_RepairsInvalid(t *testing.T) {
	// An empty-title finding is repaired rather than normalized into a default.
	restore := repairInvalidJSON
	repairInvalidJSON = func(_ *Client, _ string, validationErr error, _ string) (string, error) {
		if validationErr == nil {
			t.Error("repair should receive the validation error")
		}
		return `{"findings":[{"severity":"WARNING","title":"Repaired title","confidence":"verified","problem":"p","action":"a"}],"summary":"s","recommendation":"r"}`, nil
	}
	t.Cleanup(func() { repairInvalidJSON = restore })

	bad := validFinding()
	bad.Title = ""
	result := &report.ReviewResult{Findings: []report.Finding{bad}}
	if err := (&Client{}).repairInvalidReview(result); err != nil {
		t.Fatalf("expected repair to succeed, got: %v", err)
	}
	if len(result.Findings) != 1 || result.Findings[0].Title != "Repaired title" {
		t.Errorf("result was not replaced by the repaired review: %+v", result.Findings)
	}
}

func TestRepairInvalidReview_StillInvalid(t *testing.T) {
	// One bounded round: a repair that returns still-invalid data fails loudly.
	restore := repairInvalidJSON
	repairInvalidJSON = func(*Client, string, error, string) (string, error) {
		return `{"findings":[{"severity":"WARNING","title":"","confidence":"verified","problem":"p","action":"a"}]}`, nil
	}
	t.Cleanup(func() { repairInvalidJSON = restore })

	bad := validFinding()
	bad.Severity = "FATAL"
	result := &report.ReviewResult{Findings: []report.Finding{bad}}
	err := (&Client{}).repairInvalidReview(result)
	if err == nil {
		t.Fatal("expected an error when the repaired review is still invalid")
	}
	if !strings.Contains(err.Error(), "still invalid") {
		t.Errorf("error should describe the bounded-repair failure, got: %v", err)
	}
}

func TestRepairInvalidReview_RepairCallFails(t *testing.T) {
	restore := repairInvalidJSON
	repairInvalidJSON = func(*Client, string, error, string) (string, error) {
		return "", errors.New("claude unavailable")
	}
	t.Cleanup(func() { repairInvalidJSON = restore })

	bad := validFinding()
	bad.Confidence = "sure"
	result := &report.ReviewResult{Findings: []report.Finding{bad}}
	err := (&Client{}).repairInvalidReview(result)
	if err == nil {
		t.Fatal("expected an error when the repair call fails")
	}
	if !strings.Contains(err.Error(), "claude unavailable") {
		t.Errorf("error should wrap the repair-call failure, got: %v", err)
	}
}
