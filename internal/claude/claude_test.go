package claude

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/planwerk/planwerk-agent/internal/doccheck"
	"github.com/planwerk/planwerk-agent/internal/report"
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

func TestNewClient_DefaultsFromConsts(t *testing.T) {
	c := NewClient()
	if c.timeout != DefaultClaudeTimeout {
		t.Errorf("timeout = %s, want default %s", c.timeout, DefaultClaudeTimeout)
	}
	if c.model != DefaultClaudeModel {
		t.Errorf("model = %q, want default %q", c.model, DefaultClaudeModel)
	}
	if c.planModel != DefaultPlanModel {
		t.Errorf("planModel = %q, want default %q", c.planModel, DefaultPlanModel)
	}
	if c.effort != DefaultClaudeEffort {
		t.Errorf("effort = %q, want default %q", c.effort, DefaultClaudeEffort)
	}
	if c.planEffort != DefaultPlanEffort {
		t.Errorf("planEffort = %q, want default %q", c.planEffort, DefaultPlanEffort)
	}
	if c.showOutput {
		t.Errorf("showOutput = true, want default false")
	}
}

func TestNewClient_AppliesOptions(t *testing.T) {
	c := NewClient(
		WithTimeout(42*time.Minute),
		WithModel("fable"),
		WithPlanModel("opus"),
		WithEffort("max"),
		WithPlanEffort("high"),
		WithShowOutput(true),
	)
	if c.timeout != 42*time.Minute {
		t.Errorf("timeout = %s, want 42m0s", c.timeout)
	}
	if c.model != "fable" {
		t.Errorf("model = %q, want \"fable\"", c.model)
	}
	if c.planModel != "opus" {
		t.Errorf("planModel = %q, want \"opus\"", c.planModel)
	}
	if c.effort != "max" {
		t.Errorf("effort = %q, want \"max\"", c.effort)
	}
	if c.planEffort != "high" {
		t.Errorf("planEffort = %q, want \"high\"", c.planEffort)
	}
	if !c.showOutput {
		t.Errorf("showOutput = false, want true")
	}
}

// TestNewClient_OptionsIgnoreBadValues pins the guard semantics carried over
// from the former package-level setters: a non-positive timeout or an empty
// model/effort string is ignored so a misconfigured flag cannot disable the
// timeout or select an empty model.
func TestNewClient_OptionsIgnoreBadValues(t *testing.T) {
	c := NewClient(
		WithTimeout(0),
		WithTimeout(-5*time.Minute),
		WithModel(""),
		WithPlanModel(""),
		WithEffort(""),
		WithPlanEffort(""),
	)
	if c.timeout != DefaultClaudeTimeout {
		t.Errorf("non-positive WithTimeout must be ignored; timeout = %s, want %s", c.timeout, DefaultClaudeTimeout)
	}
	if c.model != DefaultClaudeModel {
		t.Errorf("empty WithModel must be ignored; model = %q, want %q", c.model, DefaultClaudeModel)
	}
	if c.planModel != DefaultPlanModel {
		t.Errorf("empty WithPlanModel must be ignored; planModel = %q, want %q", c.planModel, DefaultPlanModel)
	}
	if c.effort != DefaultClaudeEffort {
		t.Errorf("empty WithEffort must be ignored; effort = %q, want %q", c.effort, DefaultClaudeEffort)
	}
	if c.planEffort != DefaultPlanEffort {
		t.Errorf("empty WithPlanEffort must be ignored; planEffort = %q, want %q", c.planEffort, DefaultPlanEffort)
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

func TestSanitizeFixReport(t *testing.T) {
	const report = "## Fix Report (iteration 1)\n\n### Per check\n- build\n### Status\nSTATUS: DONE"

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "already clean is unchanged",
			input: report,
			want:  report,
		},
		{
			name:  "the branch is published preamble is dropped",
			input: "The branch is published. Final report:\n\n" + report,
			want:  report,
		},
		{
			name:  "multi-line preamble is dropped",
			input: "Pushed the follow-up commit.\nVerified the suite is green.\nFinal report:\n\n" + report,
			want:  report,
		},
		{
			name:  "wrapping markdown fence is stripped",
			input: "```markdown\n" + report + "\n```",
			want:  report,
		},
		{
			name:  "bare-prompt heading without iteration is anchored",
			input: "Done. Here is the report:\n\n## Fix Report\n\n### Status\nSTATUS: DONE",
			want:  "## Fix Report\n\n### Status\nSTATUS: DONE",
		},
		{
			name:  "no heading is returned trimmed but otherwise intact",
			input: "\n  STATUS: BLOCKED — the failure is an infra flake.  \n",
			want:  "STATUS: BLOCKED — the failure is an infra flake.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sanitizeFixReport(tt.input); got != tt.want {
				t.Errorf("sanitizeFixReport() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSanitizeGlossary(t *testing.T) {
	const doc = "# Billing\n\nThe billing context.\n\n## Language\n\n**Invoice**: a finalized statement.\n_Avoid_: bill"

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "already clean is unchanged",
			input: doc,
			want:  doc,
		},
		{
			name:  "chatty preamble before the top-level heading is dropped",
			input: "Here is the CONTEXT.md for the repo:\n\n" + doc,
			want:  doc,
		},
		{
			name:  "wrapping markdown fence is stripped",
			input: "```markdown\n" + doc + "\n```",
			want:  doc,
		},
		{
			name: "a ## subheading is not mistaken for the top-level anchor",
			// No "# " heading at all: sanitizeReport must return the de-fenced
			// text intact rather than anchoring on "## Language".
			input: "## Language\n\n**Invoice**: a finalized statement.",
			want:  "## Language\n\n**Invoice**: a finalized statement.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sanitizeGlossary(tt.input); got != tt.want {
				t.Errorf("sanitizeGlossary() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSanitizeImplementationReport(t *testing.T) {
	const report = "## Implementation Report (issue #7)\n\n### Acceptance Criteria\n- Did the thing.\n### Status\nSTATUS: DONE"

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "already clean is unchanged",
			input: report,
			want:  report,
		},
		{
			name:  "the branch is published preamble is dropped",
			input: "The branch is published. Final report:\n\n" + report,
			want:  report,
		},
		{
			name:  "wrapping markdown fence is stripped",
			input: "```markdown\n" + report + "\n```",
			want:  report,
		},
		{
			name:  "no heading is returned trimmed but otherwise intact",
			input: "\n  STATUS: NEEDS_CONTEXT — the issue is underspecified.  \n",
			want:  "STATUS: NEEDS_CONTEXT — the issue is underspecified.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sanitizeImplementationReport(tt.input); got != tt.want {
				t.Errorf("sanitizeImplementationReport() = %q, want %q", got, tt.want)
			}
		})
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

func TestBuildValidationRepairPrompt_ContainsErrorAndJSON(t *testing.T) {
	invalid := `{"findings":[{"severity":"WARNING","title":"","confidence":"verified"}]}`
	err := fmt.Errorf("finding 0 (%q): title is empty", "")
	prompt := buildValidationRepairPrompt(invalid, err)

	if !strings.Contains(prompt, "title is empty") {
		t.Error("validation repair prompt should include the validation error")
	}
	if !strings.Contains(prompt, invalid) {
		t.Error("validation repair prompt should include the invalid JSON")
	}
	if !strings.Contains(prompt, "Output ONLY the corrected JSON") {
		t.Error("validation repair prompt should ask for corrected JSON only")
	}
}

// TestBuildPlanPrompt_ContainsOverScopeGate locks the over-scope gate (issue
// #89, I2): a plan whose change set exceeds the issue's implied blast radius
// must escalate for context rather than plan the bigger change. The anchor
// survives golden regeneration so the gate cannot be dropped silently.
func TestBuildPlanPrompt_ContainsOverScopeGate(t *testing.T) {
	prompt := BuildPlanPrompt(goldenImplementContext())
	for _, want := range []string{"OVER-SCOPE GATE", "blast radius", "STATUS: NEEDS_CONTEXT"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("plan prompt should record over-scope and escalate for context; missing %q", want)
		}
	}
}

// TestBuildPlanPrompt_ContainsAutolinkHygiene locks the autolink-hygiene rule
// (issue #149): the plan prompt must forbid bare #<number> enumerations like
// "AC #1" — GitHub auto-links them to unrelated issues in the target repo — and
// reserve #<number> for genuine cross-references. The anchors survive golden
// regeneration so the rule cannot be dropped silently.
func TestBuildPlanPrompt_ContainsAutolinkHygiene(t *testing.T) {
	prompt := BuildPlanPrompt(goldenImplementContext())
	for _, want := range []string{"auto-links every #<number>", "AC 1", "genuine issue/PR cross-references"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("plan prompt should forbid bare #<number> enumerations; missing %q", want)
		}
	}
}

// assertContainsDesignVocabulary checks that a built prompt carries the shared
// design-vocabulary block (issue #114): the block itself confirms the wiring,
// and the seven pinned terms plus the component/service/boundary prohibition are
// the intent that must survive golden regeneration.
func assertContainsDesignVocabulary(t *testing.T, builder, prompt string) {
	t.Helper()
	if !strings.Contains(prompt, codebaseDesignBlock()) {
		t.Errorf("%s prompt should embed the shared design-vocabulary block", builder)
	}
	for _, term := range []string{"**module**", "**interface**", "**depth**", "**seam**", "**adapter**", "**leverage**", "**locality**"} {
		if !strings.Contains(prompt, term) {
			t.Errorf("%s prompt design vocabulary missing pinned term %q", builder, term)
		}
	}
	for _, forbidden := range []string{`"component"`, `"service"`, `"boundary"`} {
		if !strings.Contains(prompt, forbidden) {
			t.Errorf("%s prompt should forbid drift into %s", builder, forbidden)
		}
	}
}

// TestBuildPlanPrompt_ContainsDesignVocabulary locks the design vocabulary into
// the plan prompt so plan, propose, and audit all speak one architecture
// vocabulary (issue #114).
func TestBuildPlanPrompt_ContainsDesignVocabulary(t *testing.T) {
	assertContainsDesignVocabulary(t, "plan", BuildPlanPrompt(goldenImplementContext()))
}

// TestBuildAuditPrompt_ContainsDesignVocabulary locks the design vocabulary into
// the audit prompt (issue #114).
func TestBuildAuditPrompt_ContainsDesignVocabulary(t *testing.T) {
	assertContainsDesignVocabulary(t, "audit", buildAuditPrompt(goldenAuditContext()))
}

// TestBuildAnalysisPrompt_ContainsDesignVocabulary locks the design vocabulary
// into the propose analysis prompt (issue #114). The block is independent of the
// pattern catalog — the analysis_no_patterns golden proves it renders even with
// no patterns loaded.
func TestBuildAnalysisPrompt_ContainsDesignVocabulary(t *testing.T) {
	assertContainsDesignVocabulary(t, "analysis", buildAnalysisPrompt(goldenAnalysisContext()))
}

// TestBuildImplementPrompt_ContainsCircuitBreakers locks the circuit-breaker
// stop conditions (issue #89, I1): the auto-mode implement session must halt
// and emit PARTIAL (partial but reviewable), DONE_WITH_CONCERNS (complete with
// reservations), or BLOCKED instead of grinding through a thrash loop. The bare
// prompt (human-supervised) is intentionally left without them.
func TestBuildImplementPrompt_ContainsCircuitBreakers(t *testing.T) {
	prompt := BuildImplementPrompt(goldenImplementContext())
	for _, want := range []string{"Circuit breakers", "Fighting the test suite", "Ballooning scope", "Reverting in circles", "STATUS: PARTIAL", "STATUS: DONE_WITH_CONCERNS"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("implement prompt should name the circuit-breaker stop conditions; missing %q", want)
		}
	}
}

// TestBuildImplementPrompt_RequiresEdgeOrErrorTest locks the test-quality bar
// (issue #89, I4): every new test must exercise at least one error or edge
// path, not the happy path only, and the report's acceptance-criteria evidence
// must cite that edge or error test.
func TestBuildImplementPrompt_RequiresEdgeOrErrorTest(t *testing.T) {
	prompt := BuildImplementPrompt(goldenImplementContext())
	if !strings.Contains(prompt, "error or edge path") {
		t.Error("implement prompt should require every new test to exercise an error or edge path")
	}
	if !strings.Contains(prompt, "cite the edge or error test") {
		t.Error("implement report evidence should cite the edge or error test, not a happy-path one")
	}
}
