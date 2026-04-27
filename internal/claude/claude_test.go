package claude

import (
	"fmt"
	"strings"
	"testing"

	"github.com/planwerk/planwerk-review/internal/doccheck"
	"github.com/planwerk/planwerk-review/internal/report"
)

func TestStripMarkdownFences(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "no fences",
			input: `{"findings": []}`,
			want:  `{"findings": []}`,
		},
		{
			name:  "json fences",
			input: "```json\n{\"findings\": []}\n```",
			want:  `{"findings": []}`,
		},
		{
			name:  "plain fences",
			input: "```\n{\"findings\": []}\n```",
			want:  `{"findings": []}`,
		},
		{
			name:  "with surrounding whitespace",
			input: "  \n```json\n{\"findings\": []}\n```\n  ",
			want:  `{"findings": []}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripMarkdownFences(tt.input)
			if got != tt.want {
				t.Errorf("stripMarkdownFences() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBuildReviewPrompt_ScopeCoversAllCommits(t *testing.T) {
	prompt := buildReviewPrompt(ReviewContext{BaseBranch: "develop"})
	checks := []string{
		"Review Scope (MANDATORY)",
		"every commit between origin/develop and HEAD",
		"git diff origin/develop...HEAD",
		"git log origin/develop..HEAD --oneline",
		"Do NOT restrict the review to HEAD alone",
	}
	for _, check := range checks {
		if !strings.Contains(prompt, check) {
			t.Errorf("review prompt should pin /review to the full multi-commit PR diff; missing %q", check)
		}
	}
}

func TestBuildReviewPrompt_ScopeFallsBackToDefaultBranch(t *testing.T) {
	prompt := buildReviewPrompt(ReviewContext{})
	wanted := "every commit between origin/" + DefaultBaseBranch + " and HEAD"
	if !strings.Contains(prompt, wanted) {
		t.Errorf("review prompt should fall back to the default base branch when none is set; missing %q", wanted)
	}
}

func TestBuildReviewPrompt_PersonaIncludesTestPattern(t *testing.T) {
	prompt := buildReviewPrompt(ReviewContext{})
	if !strings.Contains(prompt, "Where are the tests?") {
		t.Error("Staff Engineer persona should include test-related thinking pattern")
	}
}

func TestBuildReviewPrompt_PersonaIncludesDocPattern(t *testing.T) {
	prompt := buildReviewPrompt(ReviewContext{})
	if !strings.Contains(prompt, "Would I find this in the docs?") {
		t.Error("Staff Engineer persona should include doc-related thinking pattern")
	}
}

func TestBuildReviewPrompt_ContainsTestVerification(t *testing.T) {
	prompt := buildReviewPrompt(ReviewContext{
		Checklist: "## Review Checklist\n- item",
	})
	if !strings.Contains(prompt, "Test & Documentation Verification") {
		t.Error("prompt should contain Test & Documentation Verification section")
	}
	if !strings.Contains(prompt, "Missing Tests:") {
		t.Error("prompt should instruct Claude to flag missing tests")
	}
}

func TestBuildReviewPrompt_ContainsDocVerification(t *testing.T) {
	prompt := buildReviewPrompt(ReviewContext{
		Checklist: "## Review Checklist\n- item",
	})
	if !strings.Contains(prompt, "Documentation Completeness") {
		t.Error("prompt should contain documentation completeness check")
	}
	if !strings.Contains(prompt, "Missing Documentation:") {
		t.Error("prompt should instruct Claude to flag missing documentation")
	}
}

func TestBuildReviewPrompt_SuppressionsClarified(t *testing.T) {
	prompt := buildReviewPrompt(ReviewContext{})
	if !strings.Contains(prompt, "trivial getters/setters") {
		t.Error("suppressions should still mention trivial getters/setters")
	}
	if !strings.Contains(prompt, "does NOT suppress missing tests") {
		t.Error("suppressions should clarify they do not suppress missing tests for functions with logic")
	}
	if !strings.Contains(prompt, "does NOT suppress missing documentation") {
		t.Error("suppressions should clarify they do not suppress missing docs for public APIs")
	}
}

func TestBuildReviewPrompt_NewFeatureHints(t *testing.T) {
	prompt := buildReviewPrompt(ReviewContext{
		NewFeatures: []doccheck.NewFeatureHint{
			{File: "cmd/newtool/main.go", Description: "new file added"},
		},
	})
	if !strings.Contains(prompt, "New Feature Documentation Hints") {
		t.Error("prompt should contain new feature documentation hints when present")
	}
	if !strings.Contains(prompt, "cmd/newtool/main.go") {
		t.Error("prompt should include the new file path")
	}
}

func TestBuildReviewPrompt_NoNewFeatureHints(t *testing.T) {
	prompt := buildReviewPrompt(ReviewContext{})
	if strings.Contains(prompt, "New Feature Documentation Hints") {
		t.Error("prompt should NOT contain new feature hints section when no new features")
	}
}

func TestBuildReviewPrompt_SuppressesUnchangedCode(t *testing.T) {
	prompt := buildReviewPrompt(ReviewContext{})
	if !strings.Contains(prompt, "not changed in this diff") {
		t.Error("suppressions should include rule against commenting on unchanged code")
	}
}

func TestBuildReviewPrompt_ContainsSummaryInstructions(t *testing.T) {
	prompt := buildReviewPrompt(ReviewContext{})
	if !strings.Contains(prompt, "Review Summary") {
		t.Error("prompt should contain Review Summary section")
	}
	if !strings.Contains(prompt, "does well") {
		t.Error("prompt should instruct to mention what PR does well")
	}
}

func TestBuildReviewPrompt_SuggestionFormattingRules(t *testing.T) {
	prompt := buildReviewPrompt(ReviewContext{})
	if !strings.Contains(prompt, "NO markdown fences") {
		t.Error("prompt should specify no markdown fences in suggested fixes")
	}
	if !strings.Contains(prompt, "exact indentation from the original file") {
		t.Error("prompt should require exact indentation in suggested fixes")
	}
}

func TestBuildReviewPrompt_ContainsFindingEnrichment(t *testing.T) {
	prompt := buildReviewPrompt(ReviewContext{})
	checks := []string{
		"Finding Enrichment",
		"Code Snippet",
		"Suggested Fix",
		"Confidence Level",
		"Related Findings",
	}
	for _, check := range checks {
		if !strings.Contains(prompt, check) {
			t.Errorf("prompt should contain %q", check)
		}
	}
}

func TestBuildReviewPrompt_IncludesMaxFindings(t *testing.T) {
	prompt := buildReviewPrompt(ReviewContext{MaxFindings: 25})
	if !strings.Contains(prompt, "at most 25") {
		t.Error("review prompt should surface MaxFindings cap when set")
	}
}

func TestBuildReviewPrompt_OmitsMaxFindingsWhenZero(t *testing.T) {
	prompt := buildReviewPrompt(ReviewContext{})
	if strings.Contains(prompt, "Finding Budget") {
		t.Error("review prompt should omit the finding budget section when MaxFindings <= 0")
	}
}

func TestBuildStructurePrompt_ContainsNewFields(t *testing.T) {
	prompt := buildStructurePrompt("test review output")
	checks := []string{
		`"code_snippet"`,
		`"suggested_fix"`,
		`"line_end"`,
		`"confidence"`,
		`"related_to"`,
		"Confidence levels:",
		"Field rules:",
	}
	for _, check := range checks {
		if !strings.Contains(prompt, check) {
			t.Errorf("structure prompt should contain %q", check)
		}
	}
}

func TestAssignIDs(t *testing.T) {
	result := &report.ReviewResult{
		Findings: []report.Finding{
			{Severity: "blocking"},
			{Severity: "critical"},
			{Severity: "critical"},
			{Severity: "warning"},
			{Severity: "info"},
		},
	}

	assignIDs(result)

	expected := []struct {
		id       string
		severity report.Severity
	}{
		{"B-001", report.SeverityBlocking},
		{"C-001", report.SeverityCritical},
		{"C-002", report.SeverityCritical},
		{"W-001", report.SeverityWarning},
		{"I-001", report.SeverityInfo},
	}

	for i, exp := range expected {
		f := result.Findings[i]
		if f.ID != exp.id {
			t.Errorf("finding[%d].ID = %q, want %q", i, f.ID, exp.id)
		}
		if f.Severity != exp.severity {
			t.Errorf("finding[%d].Severity = %q, want %q", i, f.Severity, exp.severity)
		}
	}
}

func TestAssignIDs_NormalizesConfidence(t *testing.T) {
	result := &report.ReviewResult{
		Findings: []report.Finding{
			{Severity: "critical", Confidence: "VERIFIED"},
			{Severity: "warning", Confidence: "Likely"},
			{Severity: "info", Confidence: "unknown"},
		},
	}

	assignIDs(result)

	if result.Findings[0].Confidence != report.ConfidenceVerified {
		t.Errorf("finding[0].Confidence = %q, want %q", result.Findings[0].Confidence, report.ConfidenceVerified)
	}
	if result.Findings[1].Confidence != report.ConfidenceLikely {
		t.Errorf("finding[1].Confidence = %q, want %q", result.Findings[1].Confidence, report.ConfidenceLikely)
	}
	if result.Findings[2].Confidence != report.ConfidenceUncertain {
		t.Errorf("finding[2].Confidence = %q, want %q", result.Findings[2].Confidence, report.ConfidenceUncertain)
	}
}

func TestAssignIDs_ResolvesRelatedTo(t *testing.T) {
	result := &report.ReviewResult{
		Findings: []report.Finding{
			{Severity: "critical", Title: "SQL injection", RelatedTo: []string{"Missing input validation"}},
			{Severity: "warning", Title: "Missing input validation", RelatedTo: []string{"SQL injection"}},
		},
	}

	assignIDs(result)

	// First finding should reference the ID of the second
	if result.Findings[0].RelatedTo[0] != "W-001" {
		t.Errorf("finding[0].RelatedTo[0] = %q, want %q", result.Findings[0].RelatedTo[0], "W-001")
	}
	// Second finding should reference the ID of the first
	if result.Findings[1].RelatedTo[0] != "C-001" {
		t.Errorf("finding[1].RelatedTo[0] = %q, want %q", result.Findings[1].RelatedTo[0], "C-001")
	}
}

func TestBuildRepairPrompt_ContainsErrorAndJSON(t *testing.T) {
	malformed := `{"findings":[{"id":""},"id":""]}`
	err := fmt.Errorf("invalid character ':' after array element")
	prompt := buildRepairPrompt(malformed, err)

	if !strings.Contains(prompt, "invalid character") {
		t.Error("repair prompt should include the parse error")
	}
	if !strings.Contains(prompt, malformed) {
		t.Error("repair prompt should include the malformed JSON")
	}
	if !strings.Contains(prompt, "Fix the JSON") {
		t.Error("repair prompt should ask Claude to fix the JSON")
	}
}
