package claude

import (
	"strings"
	"testing"

	"github.com/planwerk/planwerk-agent/internal/implement"
	"github.com/planwerk/planwerk-agent/internal/report"
)

// TestReviewApplyPrompt_CarriesActionabilityAndSkipRule: the apply session sees
// each finding's actionability, and the rule that turns an architectural one
// into a skip.
func TestReviewApplyPrompt_CarriesActionabilityAndSkipRule(t *testing.T) {
	got := BuildReviewApplyPrompt(implement.ReviewApplyContext{
		RepoFullName: "o/r",
		BaseBranch:   "main",
		Findings: []report.Finding{{
			Title:         "Wrong layer",
			Severity:      report.SeverityCritical,
			Actionability: report.ActionabilityArchitectural,
			CodeSnippet:   "x := ```y```",
		}},
	})
	if !strings.Contains(got, "1. Wrong layer [CRITICAL, architectural]") {
		t.Error("the finding list does not carry the actionability label")
	}
	if !strings.Contains(got, `list it under Skipped as "needs discussion"`) {
		t.Error("the prompt has no rule for architectural findings")
	}
	if !strings.Contains(got, "``````\nx := ```y```\n``````") {
		t.Error("a snippet containing backticks must get a longer fence (fenceSnippet)")
	}
}

// TestReviewApplyPrompt_NamesVerificationGapsAsCriteria: findings from the
// independent verification are described as unmet criteria, not review defects.
func TestReviewApplyPrompt_NamesVerificationGapsAsCriteria(t *testing.T) {
	got := BuildReviewApplyPrompt(implement.ReviewApplyContext{
		RepoFullName: "o/r",
		BaseBranch:   "main",
		Source:       implement.ReviewApplySourceVerification,
	})
	if !strings.Contains(got, "an independent verification found between the issue's Acceptance Criteria and the produced diff") {
		t.Error("verification findings are not described as unmet criteria")
	}
}
