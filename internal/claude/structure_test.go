package claude

import (
	"errors"
	"testing"
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
