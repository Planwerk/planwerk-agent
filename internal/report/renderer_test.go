package report

import (
	"bytes"
	"strings"
	"testing"
)

// TestRenderMarkdown_WikiProvenance verifies the review header carries the
// resolved wiki commit when one was used, and omits the line otherwise so
// wiki-less reviews render unchanged.
func TestRenderMarkdown_WikiProvenance(t *testing.T) {
	pr := PRInfo{Owner: "acme", Repo: "widgets", Number: 7, Title: "PR"}

	t.Run("renders the wiki line when a commit was recorded", func(t *testing.T) {
		result := ReviewResult{
			Summary:    "ok",
			WikiRepo:   "acme/widgets",
			WikiCommit: "abc1234def5678",
		}
		var buf bytes.Buffer
		NewRenderer(&buf).RenderMarkdown(result, pr, SeverityInfo, "", "v1")
		if !strings.Contains(buf.String(), "> Wiki: acme/widgets.wiki @ abc1234\n") {
			t.Errorf("expected the abbreviated wiki provenance line, got:\n%s", buf.String())
		}
	})

	t.Run("omits the wiki line when no commit was recorded", func(t *testing.T) {
		var buf bytes.Buffer
		NewRenderer(&buf).RenderMarkdown(ReviewResult{Summary: "ok"}, pr, SeverityInfo, "", "v1")
		if strings.Contains(buf.String(), "Wiki:") {
			t.Errorf("wiki-less review must not render a Wiki line, got:\n%s", buf.String())
		}
	})
}

// TestRenderMarkdown_ClaimCheck verifies a refuted finding renders its
// verification note as a Claim check line, and that a finding without one omits
// the line entirely.
func TestRenderMarkdown_ClaimCheck(t *testing.T) {
	pr := PRInfo{Owner: "acme", Repo: "widgets", Number: 7, Title: "PR"}

	t.Run("renders the claim check line for a refuted finding", func(t *testing.T) {
		result := ReviewResult{
			Findings: []Finding{{
				ID: "C-001", Severity: SeverityCritical, Title: "Nil deref",
				Confidence: ConfidenceUncertain, Problem: "p", Action: "a",
				VerificationNote: "refuted: guarded at line 50",
			}},
		}
		var buf bytes.Buffer
		NewRenderer(&buf).RenderMarkdown(result, pr, SeverityInfo, "", "v1")
		if !strings.Contains(buf.String(), "**Claim check**: refuted: guarded at line 50") {
			t.Errorf("expected the claim check line, got:\n%s", buf.String())
		}
	})

	t.Run("omits the claim check line without a note", func(t *testing.T) {
		result := ReviewResult{
			Findings: []Finding{{
				ID: "C-001", Severity: SeverityCritical, Title: "Nil deref",
				Confidence: ConfidenceVerified, Problem: "p", Action: "a",
			}},
		}
		var buf bytes.Buffer
		NewRenderer(&buf).RenderMarkdown(result, pr, SeverityInfo, "", "v1")
		if strings.Contains(buf.String(), "Claim check") {
			t.Errorf("finding without a note must not render a claim check line, got:\n%s", buf.String())
		}
	})
}

// TestRenderMarkdown_SnippetCheck verifies a snippet-demoted finding renders its
// recorded reason as a Snippet check line, while a passed or unstamped finding
// omits the line — a demoted finding must explain itself wherever it renders.
func TestRenderMarkdown_SnippetCheck(t *testing.T) {
	pr := PRInfo{Owner: "acme", Repo: "widgets", Number: 7, Title: "PR"}

	t.Run("renders the snippet check line for a demoted finding", func(t *testing.T) {
		result := ReviewResult{
			Findings: []Finding{{
				ID: "W-001", Severity: SeverityWarning, Title: "unquoted claim",
				Confidence: ConfidenceUncertain, Problem: "p", Action: "a",
				SnippetCheck: "demoted: quoted code not found in the changed files",
			}},
		}
		var buf bytes.Buffer
		NewRenderer(&buf).RenderMarkdown(result, pr, SeverityInfo, "", "v1")
		if !strings.Contains(buf.String(), "**Snippet check**: demoted: quoted code not found in the changed files") {
			t.Errorf("expected the snippet check line, got:\n%s", buf.String())
		}
	})

	t.Run("omits the snippet check line for a passed finding", func(t *testing.T) {
		result := ReviewResult{
			Findings: []Finding{{
				ID: "C-001", Severity: SeverityCritical, Title: "real bug",
				Confidence: ConfidenceVerified, Problem: "p", Action: "a",
				SnippetCheck: SnippetCheckPassed,
			}},
		}
		var buf bytes.Buffer
		NewRenderer(&buf).RenderMarkdown(result, pr, SeverityInfo, "", "v1")
		if strings.Contains(buf.String(), "Snippet check") {
			t.Errorf("a passed finding must not render a snippet check line, got:\n%s", buf.String())
		}
	})
}

// TestRenderDataBlock_IncludesGates verifies the run-level gate records ride the
// machine-readable data block when set, are omitted when nil, and that adding
// them leaves the existing SHA+findings round-trip (now carrying the per-finding
// records) intact.
func TestRenderDataBlock_IncludesGates(t *testing.T) {
	t.Run("present when set", func(t *testing.T) {
		full := ReviewResult{
			Findings: []Finding{
				{ID: "C-001", Severity: SeverityCritical, Title: "SQLi", File: "db.go", Line: 10, ClaimCheck: ClaimCheckRefuted, SnippetCheck: SnippetCheckPassed},
			},
			Gates: &GateStats{
				Snippet: &SnippetGateStats{Examined: 4, Demoted: 1},
				Claim:   &ClaimGateStats{Sent: 2, Verdicts: 2, Refuted: 1},
			},
		}
		block := RenderDataBlock(full, "sha-gates", Usage{})
		for _, want := range []string{
			`"gates":{`,
			`"snippet":{"examined":4,"demoted":1}`,
			`"claim":{"sent":2,"verdicts":2,"refuted":1}`,
			`"claim_check":"refuted"`,
		} {
			if !strings.Contains(block, want) {
				t.Errorf("data block missing %q; got %q", want, block)
			}
		}
		// The added gates field must not disturb the existing SHA+findings parse.
		sha, findings, ok := ParseDataBlock(block)
		if !ok {
			t.Fatal("expected a parseable data block")
		}
		if sha != "sha-gates" {
			t.Errorf("sha = %q, want sha-gates", sha)
		}
		if len(findings) != 1 || findings[0].ClaimCheck != ClaimCheckRefuted {
			t.Errorf("findings = %+v, want one finding carrying its claim_check", findings)
		}
	})

	t.Run("fail-open shape stays visible", func(t *testing.T) {
		// A gate that ran but returned no verdicts records sent>0, verdicts=0 —
		// the fail-open case must survive to the data block, not vanish.
		full := ReviewResult{Gates: &GateStats{Claim: &ClaimGateStats{Sent: 1}}}
		block := RenderDataBlock(full, "def456", Usage{})
		if !strings.Contains(block, `"claim":{"sent":1,"verdicts":0,"refuted":0}`) {
			t.Errorf("fail-open claim stats missing from data block; got %q", block)
		}
	})

	t.Run("omitted when nil", func(t *testing.T) {
		block := RenderDataBlock(ReviewResult{}, "plainsha", Usage{})
		if strings.Contains(block, `"gates"`) {
			t.Errorf("data block should omit gates when unset; got %q", block)
		}
	})
}

// TestRenderAuditMarkdown_WikiProvenance verifies the audit header carries the
// resolved wiki commit when one was used.
func TestRenderAuditMarkdown_WikiProvenance(t *testing.T) {
	result := ReviewResult{Summary: "ok", WikiRepo: "acme/widgets", WikiCommit: "0123456789abcdef"}
	var buf bytes.Buffer
	NewRenderer(&buf).RenderAuditMarkdown(result, RepoInfo{Owner: "acme", Name: "widgets"}, SeverityInfo, "", "v1")
	if !strings.Contains(buf.String(), "> Wiki: acme/widgets.wiki @ 0123456\n") {
		t.Errorf("expected the abbreviated wiki provenance line in the audit header, got:\n%s", buf.String())
	}
}

// TestRenderDataBlock_WikiCommit verifies the resolved wiki commit is attached to
// the machine-readable data block and that the field is omitted when absent.
func TestRenderDataBlock_WikiCommit(t *testing.T) {
	t.Run("present when set", func(t *testing.T) {
		block := RenderDataBlock(ReviewResult{WikiCommit: "abc1234"}, "headsha", Usage{})
		if !strings.Contains(block, `"wiki_commit":"abc1234"`) {
			t.Errorf("data block missing wiki_commit; got %q", block)
		}
	})

	t.Run("omitted when empty", func(t *testing.T) {
		block := RenderDataBlock(ReviewResult{}, "headsha", Usage{})
		if strings.Contains(block, "wiki_commit") {
			t.Errorf("data block should omit wiki_commit when unset; got %q", block)
		}
	})
}

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
