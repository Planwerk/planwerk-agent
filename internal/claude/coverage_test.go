package claude

import (
	"errors"
	"testing"

	"github.com/planwerk/planwerk-agent/internal/report"
)

// TestCoverageDecode_FencedPayload exercises the decode contract CoverageMap
// relies on (issue #156, defect 3): a ```json-fenced coverage payload decodes
// into a populated CoverageResult without ever invoking the repair path, so a
// stray markdown fence no longer fails the whole coverage pass.
func TestCoverageDecode_FencedPayload(t *testing.T) {
	called := false
	restore := repairJSON
	repairJSON = func(*Client, string, error, string) (string, error) {
		called = true
		return "", errors.New("repair must not be called for a fenced-but-valid payload")
	}
	t.Cleanup(func() { repairJSON = restore })

	const payload = "```json\n{\"entries\":[{\"function\":\"Foo()\",\"file\":\"foo.go\",\"rating\":\"GAP\"}]}\n```"
	var result report.CoverageResult
	if err := (&Client{}).decodeJSONWithRepair(payload, "coverage map", &result); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if called {
		t.Error("repair was called for a fenced-but-valid payload")
	}
	if len(result.Entries) != 1 || result.Entries[0].Function != "Foo()" || result.Entries[0].Rating != "GAP" {
		t.Errorf("decoded %+v, want one GAP entry for Foo()", result)
	}
}

// TestCoverageDecode_MalformedRepaired covers the repair fallback the coverage
// pass now shares with every other structured pass: a truncated payload is
// repaired once and then decodes, so a one-character glitch no longer kills the
// pass.
func TestCoverageDecode_MalformedRepaired(t *testing.T) {
	restore := repairJSON
	repairJSON = func(_ *Client, _ string, parseErr error, _ string) (string, error) {
		if parseErr == nil {
			t.Error("repair should receive the original parse error")
		}
		return `{"entries":[{"function":"Bar()","file":"bar.go","rating":"★★★"}]}`, nil
	}
	t.Cleanup(func() { repairJSON = restore })

	var result report.CoverageResult
	if err := (&Client{}).decodeJSONWithRepair(`{"entries":[{"function":"Bar()","file":"bar.go","rating":"★★★"}`, "coverage map", &result); err != nil {
		t.Fatalf("expected repair to succeed, got: %v", err)
	}
	if len(result.Entries) != 1 || result.Entries[0].Function != "Bar()" {
		t.Errorf("decoded %+v after repair, want one entry for Bar()", result)
	}
}

// TestDecodeCoverage_PrependedObjectFailsLoud pins the fail-loud guard (issue
// #156, defect 2). When the model prepends an unrelated JSON object before the
// coverage payload, the two concatenated objects are not valid JSON, so
// decodeJSONWithRepair falls back to extractJSONValue, which returns the leading
// {"status":"ok"} object. That decodes into a CoverageResult with no entries;
// decodeCoverage must reject it loudly rather than return an empty coverage map.
func TestDecodeCoverage_PrependedObjectFailsLoud(t *testing.T) {
	called := false
	restore := repairJSON
	repairJSON = func(*Client, string, error, string) (string, error) {
		called = true
		return "", errors.New("repair should not run: the leading object decodes cleanly")
	}
	t.Cleanup(func() { repairJSON = restore })

	const payload = `{"status":"ok"}` + "\n" + `{"entries":[{"function":"Foo()","file":"foo.go","rating":"GAP"}]}`
	if _, err := (&Client{}).decodeCoverage(payload); err == nil {
		t.Fatal("decodeCoverage accepted a payload whose leading object has no entries; want a loud error")
	}
	if called {
		t.Error("repair was called; the guard should reject the entries-less decode, not the repair path")
	}
}

// TestDecodeCoverage_EmptyEntriesAccepted confirms the guard does not reject a
// legitimate empty coverage map: a diff with no changed functions yields
// {"entries": []}, which unmarshals to a non-nil empty slice.
func TestDecodeCoverage_EmptyEntriesAccepted(t *testing.T) {
	result, err := (&Client{}).decodeCoverage(`{"entries":[]}`)
	if err != nil {
		t.Fatalf("unexpected error for entries-present-but-empty payload: %v", err)
	}
	if result.Entries == nil || len(result.Entries) != 0 {
		t.Errorf("Entries = %#v, want non-nil empty slice", result.Entries)
	}
}
