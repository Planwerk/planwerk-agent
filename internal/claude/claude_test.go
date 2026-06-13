package claude

import (
	"fmt"
	"strings"
	"testing"
	"time"

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
		`"fix_options"`,
		`"recommended_option"`,
		`"recommendation_reasoning"`,
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

func TestAssignIDs_StripsFixOptionsFromAutoFix(t *testing.T) {
	result := &report.ReviewResult{
		Findings: []report.Finding{
			{
				Severity:                "warning",
				Title:                   "auto-fix carrying stray options",
				Actionability:           "auto-fix",
				FixOptions:              []report.FixOption{{ID: "A", Approach: "x"}},
				RecommendedOption:       "A",
				RecommendationReasoning: "stale",
			},
		},
	}
	assignIDs(result)
	f := result.Findings[0]
	if f.FixOptions != nil {
		t.Errorf("auto-fix finding should drop FixOptions, got %+v", f.FixOptions)
	}
	if f.RecommendedOption != "" || f.RecommendationReasoning != "" {
		t.Errorf("auto-fix finding should clear RecommendedOption/Reasoning, got %q / %q",
			f.RecommendedOption, f.RecommendationReasoning)
	}
}

func TestAssignIDs_DropsRecommendedOptionWhenIDMissing(t *testing.T) {
	result := &report.ReviewResult{
		Findings: []report.Finding{
			{
				Severity:                "warning",
				Title:                   "broad catch",
				Actionability:           "needs-discussion",
				FixOptions:              []report.FixOption{{ID: "A"}, {ID: "B"}},
				RecommendedOption:       "C", // not in the option set
				RecommendationReasoning: "should be dropped",
			},
			{
				Severity:                "warning",
				Title:                   "broad catch (valid)",
				Actionability:           "needs-discussion",
				FixOptions:              []report.FixOption{{ID: "A"}, {ID: "B"}},
				RecommendedOption:       "b", // case-insensitive match
				RecommendationReasoning: "kept",
			},
		},
	}
	assignIDs(result)
	if result.Findings[0].RecommendedOption != "" {
		t.Errorf("invalid recommended_option should be cleared, got %q", result.Findings[0].RecommendedOption)
	}
	if result.Findings[0].RecommendationReasoning != "" {
		t.Errorf("reasoning should be cleared when recommended_option is dropped")
	}
	if result.Findings[1].RecommendedOption != "b" {
		t.Errorf("valid case-insensitive recommended_option should be kept, got %q", result.Findings[1].RecommendedOption)
	}
	if result.Findings[1].RecommendationReasoning != "kept" {
		t.Errorf("reasoning should be kept alongside valid recommended_option")
	}
}

func TestSetTimeout_TogglesAndRestores(t *testing.T) {
	if Timeout() != DefaultClaudeTimeout {
		t.Fatalf("precondition: Timeout() = %s, want default %s", Timeout(), DefaultClaudeTimeout)
	}

	restore := SetTimeout(42 * time.Minute)
	if Timeout() != 42*time.Minute {
		t.Errorf("Timeout() = %s after SetTimeout(42m), want 42m0s", Timeout())
	}
	restore()
	if Timeout() != DefaultClaudeTimeout {
		t.Errorf("Timeout() = %s after restore, want default %s", Timeout(), DefaultClaudeTimeout)
	}
}

func TestSetTimeout_IgnoresNonPositive(t *testing.T) {
	prev := Timeout()
	t.Cleanup(SetTimeout(prev))

	SetTimeout(0)
	if Timeout() != prev {
		t.Errorf("SetTimeout(0) must be ignored; Timeout() = %s, want %s", Timeout(), prev)
	}
	SetTimeout(-5 * time.Minute)
	if Timeout() != prev {
		t.Errorf("SetTimeout(-5m) must be ignored; Timeout() = %s, want %s", Timeout(), prev)
	}
}

func TestSetShowOutput_TogglesAndRestores(t *testing.T) {
	if ShowOutput() {
		t.Fatalf("precondition: ShowOutput() should default to false")
	}
	restore := SetShowOutput(true)
	if !ShowOutput() {
		t.Errorf("ShowOutput() should be true after SetShowOutput(true)")
	}
	restore()
	if ShowOutput() {
		t.Errorf("ShowOutput() should be false after restore")
	}
}

func TestSetModel_TogglesAndRestores(t *testing.T) {
	if Model() != DefaultClaudeModel {
		t.Fatalf("precondition: Model() = %q, want default %q", Model(), DefaultClaudeModel)
	}

	restore := SetModel("fable")
	if Model() != "fable" {
		t.Errorf("Model() = %q after SetModel(\"fable\"), want \"fable\"", Model())
	}
	restore()
	if Model() != DefaultClaudeModel {
		t.Errorf("Model() = %q after restore, want default %q", Model(), DefaultClaudeModel)
	}
}

func TestSetModel_IgnoresEmpty(t *testing.T) {
	prev := Model()
	t.Cleanup(SetModel(prev))

	SetModel("")
	if Model() != prev {
		t.Errorf("SetModel(\"\") must be ignored; Model() = %q, want %q", Model(), prev)
	}
}

func TestSanitizePlan(t *testing.T) {
	const plan = "## Implementation Plan (issue #7)\n\n### Summary\n- Do the thing.\n### Status\nSTATUS: PLAN_READY"

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "already clean is unchanged",
			input: plan,
			want:  plan,
		},
		{
			name:  "single-line preamble is dropped",
			input: "I have confirmed all ground truth. Writing the plan.\n\n" + plan,
			want:  plan,
		},
		{
			name:  "multi-line preamble is dropped",
			input: "Let me work through this.\nI have confirmed all ground truth.\nWriting the plan now:\n\n" + plan,
			want:  plan,
		},
		{
			name:  "wrapping markdown fence is stripped",
			input: "```markdown\n" + plan + "\n```",
			want:  plan,
		},
		{
			name:  "blocked status survives preamble stripping",
			input: "Here is what I found.\n\n## Implementation Plan (issue #7)\n\n### Status\nSTATUS: BLOCKED",
			want:  "## Implementation Plan (issue #7)\n\n### Status\nSTATUS: BLOCKED",
		},
		{
			name:  "no heading is returned trimmed but otherwise intact",
			input: "\n  STATUS: NEEDS_CONTEXT — the issue is underspecified.  \n",
			want:  "STATUS: NEEDS_CONTEXT — the issue is underspecified.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sanitizePlan(tt.input); got != tt.want {
				t.Errorf("sanitizePlan() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSanitizePlan_PreservesEscalationMarker(t *testing.T) {
	// The preamble strip must not break planEscalation: a BLOCKED/NEEDS_CONTEXT
	// marker buried after a preamble must still be detectable downstream.
	input := "Writing the plan.\n\n## Implementation Plan (issue #1)\n### Status\nSTATUS: BLOCKED"
	got := sanitizePlan(input)
	if !strings.Contains(got, "STATUS: BLOCKED") {
		t.Errorf("sanitizePlan() dropped the escalation marker: %q", got)
	}
	if strings.Contains(got, "Writing the plan.") {
		t.Errorf("sanitizePlan() left preamble in: %q", got)
	}
}

func TestSetPlanModel_TogglesAndRestores(t *testing.T) {
	if PlanModel() != DefaultPlanModel {
		t.Fatalf("precondition: PlanModel() = %q, want default %q", PlanModel(), DefaultPlanModel)
	}

	restore := SetPlanModel("opus")
	if PlanModel() != "opus" {
		t.Errorf("PlanModel() = %q after SetPlanModel(\"opus\"), want \"opus\"", PlanModel())
	}
	restore()
	if PlanModel() != DefaultPlanModel {
		t.Errorf("PlanModel() = %q after restore, want default %q", PlanModel(), DefaultPlanModel)
	}
}

func TestSetPlanModel_IgnoresEmpty(t *testing.T) {
	prev := PlanModel()
	t.Cleanup(SetPlanModel(prev))

	SetPlanModel("")
	if PlanModel() != prev {
		t.Errorf("SetPlanModel(\"\") must be ignored; PlanModel() = %q, want %q", PlanModel(), prev)
	}
}

func TestSetPlanEffort_TogglesAndRestores(t *testing.T) {
	if PlanEffort() != DefaultPlanEffort {
		t.Fatalf("precondition: PlanEffort() = %q, want default %q", PlanEffort(), DefaultPlanEffort)
	}

	restore := SetPlanEffort("high")
	if PlanEffort() != "high" {
		t.Errorf("PlanEffort() = %q after SetPlanEffort(\"high\"), want \"high\"", PlanEffort())
	}
	restore()
	if PlanEffort() != DefaultPlanEffort {
		t.Errorf("PlanEffort() = %q after restore, want default %q", PlanEffort(), DefaultPlanEffort)
	}
}

func TestSetPlanEffort_IgnoresEmpty(t *testing.T) {
	prev := PlanEffort()
	t.Cleanup(SetPlanEffort(prev))

	SetPlanEffort("")
	if PlanEffort() != prev {
		t.Errorf("SetPlanEffort(\"\") must be ignored; PlanEffort() = %q, want %q", PlanEffort(), prev)
	}
}

func TestSetEffort_TogglesAndRestores(t *testing.T) {
	if Effort() != DefaultClaudeEffort {
		t.Fatalf("precondition: Effort() = %q, want default %q", Effort(), DefaultClaudeEffort)
	}

	restore := SetEffort("max")
	if Effort() != "max" {
		t.Errorf("Effort() = %q after SetEffort(\"max\"), want \"max\"", Effort())
	}
	restore()
	if Effort() != DefaultClaudeEffort {
		t.Errorf("Effort() = %q after restore, want default %q", Effort(), DefaultClaudeEffort)
	}
}

func TestSetEffort_IgnoresEmpty(t *testing.T) {
	prev := Effort()
	t.Cleanup(SetEffort(prev))

	SetEffort("")
	if Effort() != prev {
		t.Errorf("SetEffort(\"\") must be ignored; Effort() = %q, want %q", Effort(), prev)
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
