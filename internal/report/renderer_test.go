package report

import (
	"strings"
	"testing"
)

// TestRenderDataBlock_IncludesUsage verifies the per-Run Claude usage totals are
// embedded in the data block as a machine-readable object (issue #46, AC#3) and
// that adding the field leaves ParseDataBlock's SHA+findings round-trip intact.
func TestRenderDataBlock_IncludesUsage(t *testing.T) {
	full := ReviewResult{Findings: []Finding{
		{ID: "C-001", Severity: SeverityCritical, Title: "SQLi", File: "db.go", Line: 10},
	}}
	usage := Usage{
		Calls:               6,
		InputTokens:         13400,
		OutputTokens:        4200,
		CacheReadTokens:     15626,
		CacheCreationTokens: 2464,
		CostUSD:             0.42,
	}

	block := RenderDataBlock(full, "abc123", usage)

	for _, want := range []string{
		`"usage":{`,
		`"calls":6`,
		`"input_tokens":13400`,
		`"output_tokens":4200`,
		`"cache_read_input_tokens":15626`,
		`"cache_creation_input_tokens":2464`,
		`"est_cost_usd":0.42`,
	} {
		if !strings.Contains(block, want) {
			t.Errorf("data block missing %q; got %q", want, block)
		}
	}

	// The added usage field must not disturb the existing SHA+findings parse.
	sha, findings, ok := ParseDataBlock(block)
	if !ok {
		t.Fatal("expected a parseable data block")
	}
	if sha != "abc123" {
		t.Errorf("sha = %q, want abc123", sha)
	}
	if len(findings) != 1 || findings[0].Title != "SQLi" {
		t.Errorf("findings = %+v, want one SQLi finding", findings)
	}
}

// TestRenderDataBlock_ZeroUsage documents the empty-Run shape: a data block
// rendered with no Claude calls still carries a well-formed zero usage object,
// so a CI consumer can always read the field rather than handling its absence.
func TestRenderDataBlock_ZeroUsage(t *testing.T) {
	block := RenderDataBlock(ReviewResult{}, "deadbeef", Usage{})
	if !strings.Contains(block, `"usage":{"calls":0`) {
		t.Errorf("zero-usage data block should still embed a usage object; got %q", block)
	}
}
