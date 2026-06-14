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
	repairJSON = func(string, error, string) (string, error) {
		called = true
		return "", errors.New("repair must not be called for valid JSON")
	}
	t.Cleanup(func() { repairJSON = restore })

	var got sample
	if err := decodeJSONWithRepair("```json\n{\"a\":5,\"b\":\"x\"}\n```", "test", &got); err != nil {
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
	repairJSON = func(malformed string, parseErr error, label string) (string, error) {
		if parseErr == nil {
			t.Error("repair should receive the original parse error")
		}
		return `{"a":7,"b":"fixed"}`, nil
	}
	t.Cleanup(func() { repairJSON = restore })

	var got sample
	if err := decodeJSONWithRepair(`{"a":7,"b":"fixed"`, "test", &got); err != nil {
		t.Fatalf("expected repair to succeed, got: %v", err)
	}
	if got.A != 7 || got.B != "fixed" {
		t.Errorf("decoded %+v after repair, want {A:7 B:fixed}", got)
	}
}

func TestDecodeJSONWithRepair_RepairCallFails(t *testing.T) {
	restore := repairJSON
	repairJSON = func(string, error, string) (string, error) {
		return "", errors.New("claude unavailable")
	}
	t.Cleanup(func() { repairJSON = restore })

	var got sample
	if err := decodeJSONWithRepair(`not json`, "test", &got); err == nil {
		t.Error("expected an error when repair fails")
	}
}

func TestDecodeJSONWithRepair_RepairStillInvalid(t *testing.T) {
	restore := repairJSON
	repairJSON = func(string, error, string) (string, error) {
		return `still { not json`, nil
	}
	t.Cleanup(func() { repairJSON = restore })

	var got sample
	if err := decodeJSONWithRepair(`bad`, "test", &got); err == nil {
		t.Error("expected an error when repaired output is still invalid")
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
	repairInvalidJSON = func(string, error, string) (string, error) {
		t.Error("repair must not be called for a valid review")
		return "", errors.New("repair must not be called")
	}
	t.Cleanup(func() { repairInvalidJSON = restore })

	result := &report.ReviewResult{Findings: []report.Finding{validFinding()}}
	if err := repairInvalidReview(result); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRepairInvalidReview_RepairsInvalid(t *testing.T) {
	// An empty-title finding is repaired rather than normalized into a default.
	restore := repairInvalidJSON
	repairInvalidJSON = func(_ string, validationErr error, _ string) (string, error) {
		if validationErr == nil {
			t.Error("repair should receive the validation error")
		}
		return `{"findings":[{"severity":"WARNING","title":"Repaired title","confidence":"verified","problem":"p","action":"a"}],"summary":"s","recommendation":"r"}`, nil
	}
	t.Cleanup(func() { repairInvalidJSON = restore })

	bad := validFinding()
	bad.Title = ""
	result := &report.ReviewResult{Findings: []report.Finding{bad}}
	if err := repairInvalidReview(result); err != nil {
		t.Fatalf("expected repair to succeed, got: %v", err)
	}
	if len(result.Findings) != 1 || result.Findings[0].Title != "Repaired title" {
		t.Errorf("result was not replaced by the repaired review: %+v", result.Findings)
	}
}

func TestRepairInvalidReview_StillInvalid(t *testing.T) {
	// One bounded round: a repair that returns still-invalid data fails loudly.
	restore := repairInvalidJSON
	repairInvalidJSON = func(string, error, string) (string, error) {
		return `{"findings":[{"severity":"WARNING","title":"","confidence":"verified","problem":"p","action":"a"}]}`, nil
	}
	t.Cleanup(func() { repairInvalidJSON = restore })

	bad := validFinding()
	bad.Severity = "FATAL"
	result := &report.ReviewResult{Findings: []report.Finding{bad}}
	err := repairInvalidReview(result)
	if err == nil {
		t.Fatal("expected an error when the repaired review is still invalid")
	}
	if !strings.Contains(err.Error(), "still invalid") {
		t.Errorf("error should describe the bounded-repair failure, got: %v", err)
	}
}

func TestRepairInvalidReview_RepairCallFails(t *testing.T) {
	restore := repairInvalidJSON
	repairInvalidJSON = func(string, error, string) (string, error) {
		return "", errors.New("claude unavailable")
	}
	t.Cleanup(func() { repairInvalidJSON = restore })

	bad := validFinding()
	bad.Confidence = "sure"
	result := &report.ReviewResult{Findings: []report.Finding{bad}}
	err := repairInvalidReview(result)
	if err == nil {
		t.Fatal("expected an error when the repair call fails")
	}
	if !strings.Contains(err.Error(), "claude unavailable") {
		t.Errorf("error should wrap the repair-call failure, got: %v", err)
	}
}
