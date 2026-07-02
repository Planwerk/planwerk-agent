package claude

import (
	"strings"
	"testing"

	"github.com/planwerk/planwerk-agent/internal/audit"
	"github.com/planwerk/planwerk-agent/internal/patterns"
)

func TestBuildAuditPrompt_IncludesPersona(t *testing.T) {
	prompt := buildAuditPrompt(audit.AuditContext{})
	if !strings.Contains(prompt, "Staff Engineer") {
		t.Error("audit prompt should invoke Staff Engineer persona")
	}
	if !strings.Contains(prompt, "Where are the tests?") {
		t.Error("audit prompt should include test cognitive pattern")
	}
}

func TestBuildAuditPrompt_IncludesPatterns(t *testing.T) {
	ctx := audit.AuditContext{
		Patterns: []patterns.Pattern{
			{Name: "Hardcoded secrets", ReviewArea: "security", DetectionHint: "literal secret strings", Severity: "CRITICAL"},
		},
	}
	prompt := buildAuditPrompt(ctx)
	if !strings.Contains(prompt, "Review Patterns to Apply") {
		t.Error("audit prompt should contain patterns section")
	}
	if !strings.Contains(prompt, "Hardcoded secrets") {
		t.Error("audit prompt should embed loaded patterns by name")
	}
	if !strings.Contains(prompt, "<review-patterns>") {
		t.Error("audit prompt should wrap patterns in XML tags")
	}
}

func TestBuildAuditPrompt_IncludesRepoName(t *testing.T) {
	prompt := buildAuditPrompt(audit.AuditContext{RepoName: "acme/widget"})
	if !strings.Contains(prompt, "Repository: acme/widget") {
		t.Error("audit prompt should include repository name when provided")
	}
}

func TestBuildAuditPrompt_ExplicitlyFullCodebase(t *testing.T) {
	prompt := buildAuditPrompt(audit.AuditContext{})
	if !strings.Contains(prompt, "NOT a pull-request review") {
		t.Error("audit prompt should explicitly state this is not a PR review")
	}
	if !strings.Contains(prompt, "there is no diff") {
		t.Error("audit prompt should make clear there is no diff to review")
	}
	if !strings.Contains(prompt, "ENTIRE current state") {
		t.Error("audit prompt should instruct to audit the ENTIRE current state")
	}
}

func TestBuildAuditPrompt_IncludesTestAndDocChecks(t *testing.T) {
	prompt := buildAuditPrompt(audit.AuditContext{})
	checks := []string{
		"Test & Documentation Completeness",
		"Missing Tests:",
		"Missing E2E Tests:",
		"Missing Documentation:",
		"chainsaw",
	}
	for _, c := range checks {
		if !strings.Contains(prompt, c) {
			t.Errorf("audit prompt should contain %q", c)
		}
	}
}

func TestBuildAuditPrompt_IncludesDependencyFreshness(t *testing.T) {
	prompt := buildAuditPrompt(audit.AuditContext{})
	if !strings.Contains(prompt, "Dependency Freshness") {
		t.Error("audit prompt should contain dependency freshness section")
	}
	if !strings.Contains(prompt, "Deprecated Dependency:") {
		t.Error("audit prompt should instruct to flag deprecated dependencies")
	}
}

func TestBuildAuditPrompt_IncludesMaxFindings(t *testing.T) {
	prompt := buildAuditPrompt(audit.AuditContext{MaxFindings: 25})
	if !strings.Contains(prompt, "at most 25") {
		t.Error("audit prompt should surface MaxFindings cap when set")
	}
}

func TestBuildAuditPrompt_OmitsMaxFindingsWhenZero(t *testing.T) {
	prompt := buildAuditPrompt(audit.AuditContext{})
	if strings.Contains(prompt, "Finding Budget") {
		t.Error("audit prompt should omit the finding budget section when MaxFindings <= 0")
	}
}

func TestBuildAuditPrompt_IncludesEnrichmentRules(t *testing.T) {
	prompt := buildAuditPrompt(audit.AuditContext{})
	for _, c := range []string{"Code Snippet", "Suggested Fix", "Finding Labels", "**Confidence**", "Related Findings", "Pattern"} {
		if !strings.Contains(prompt, c) {
			t.Errorf("audit prompt should include enrichment field %q", c)
		}
	}
}
