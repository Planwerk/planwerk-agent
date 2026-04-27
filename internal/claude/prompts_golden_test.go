package claude

import (
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/planwerk/planwerk-review/internal/audit"
	"github.com/planwerk/planwerk-review/internal/doccheck"
	"github.com/planwerk/planwerk-review/internal/patterns"
	"github.com/planwerk/planwerk-review/internal/planwerk"
	"github.com/planwerk/planwerk-review/internal/propose"
)

// updateGolden regenerates the prompt golden files under testdata/prompts/.
// Run `go test ./internal/claude -update` after an intentional prompt change.
var updateGolden = flag.Bool("update", false, "regenerate prompt golden files")

func assertGoldenPrompt(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", "prompts", name+".golden")
	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("creating golden dir: %v", err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("writing golden %s: %v", path, err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading golden %s: %v (run `go test ./internal/claude -update` to generate)", path, err)
	}
	if got != string(want) {
		t.Errorf("prompt %s differs from golden file %s.\nRun `go test ./internal/claude -update` if the change is intentional.\n\n--- want ---\n%s\n--- got ---\n%s", name, path, string(want), got)
	}
}

// goldenPatterns returns a deterministic pattern set covering both the
// technology and design-principle categories so FormatGroupedForPrompt
// emits both XML blocks in the snapshot.
func goldenPatterns() []patterns.Pattern {
	return []patterns.Pattern{
		{
			Name:          "Hardcoded secrets",
			ReviewArea:    "security",
			DetectionHint: "literal API keys or passwords in source",
			Severity:      "CRITICAL",
			Category:      "design-principle",
			Sources:       []patterns.Source{{Title: "OWASP ASVS", URL: "https://owasp.org/asvs"}},
			Body:          "## Rule\nSecrets MUST be loaded from environment variables or a secret manager.",
		},
		{
			Name:          "Missing context.Context parameter",
			ReviewArea:    "reliability",
			DetectionHint: "long-running Go functions without ctx",
			Severity:      "WARNING",
			Category:      "technology",
			AppliesWhen:   []string{"go"},
			Body:          "## Rule\nLong-running functions MUST accept a context.Context.",
		},
	}
}

func goldenReviewContext() ReviewContext {
	return ReviewContext{
		Patterns:    goldenPatterns(),
		MaxPatterns: 0,
		BaseBranch:  "develop",
		PRTitle:     "feat: add snapshot tests for prompt builders",
		PRBody:      "Adds golden-file tests for every prompt builder.\n\nFixes #3",
		Checklist:   "## Review Checklist\n- Verify prompt coverage\n- Verify golden files exist",
		CommitLog:   "abc1234 feat: add snapshot tests\ndef5678 chore: add -update flag",
		StaleDocs: []doccheck.StaleDocHint{
			{DocFile: "README.md", RelatedDirs: []string{"internal/claude", "internal/patterns"}},
		},
		NewFeatures: []doccheck.NewFeatureHint{
			{File: "internal/claude/prompts_golden_test.go", Description: "new test file"},
		},
		TodoContent: "- [ ] Add coverage map snapshot\n- [x] Add review prompt snapshot",
	}
}

func goldenAuditContext() audit.AuditContext {
	return audit.AuditContext{
		Patterns:    goldenPatterns(),
		MaxPatterns: 0,
		MaxFindings: 25,
		RepoName:    "planwerk/planwerk-review",
	}
}

func goldenAnalysisContext() propose.AnalysisContext {
	return propose.AnalysisContext{
		Patterns:    goldenPatterns(),
		MaxPatterns: 0,
		RepoName:    "planwerk/planwerk-review",
	}
}

func goldenFeature() *planwerk.Feature {
	return &planwerk.Feature{
		FeatureID:   "CC-0042",
		Title:       "Snapshot tests for prompt builders",
		Slug:        "prompt-snapshot-tests",
		Status:      "in-progress",
		Description: "Lock the prompt surface with golden files so drift shows up in PR diffs.",
		Stories: []planwerk.Story{
			{
				Title:    "Detect prompt drift",
				Role:     "maintainer",
				Want:     "failing tests when prompt text changes",
				SoThat:   "unintended prompt mutations cannot ship silently",
				Criteria: []string{"Golden file exists for every builder", "Tests fail when the prompt changes"},
			},
		},
		Requirements: []planwerk.Requirement{
			{
				ID:          "REQ-001",
				Description: "Snapshot tests MUST cover every prompt builder",
				Priority:    "SHALL",
				Rationale:   "Prompt mutations otherwise go unreviewed.",
				Scenarios: []planwerk.Scenario{
					{
						Name:    "Prompt text unchanged",
						When:    "the prompt builder is invoked with fixed input",
						Then:    "the output MUST equal the golden file byte-for-byte",
						AndThen: []string{"the test MUST pass"},
					},
				},
			},
		},
		Tasks: []planwerk.Task{
			{ID: "T1", Title: "Add golden helper", Description: "Write -update helper", Status: "done", Requirements: []string{"REQ-001"}},
		},
		TestSpecifications: []planwerk.TestSpecification{
			{
				TestFile:      "internal/claude/prompts_golden_test.go",
				TestFunction:  "TestBuildReviewPrompt_Golden",
				Story:         "Detect prompt drift",
				Expected:      "review prompt matches golden file",
				RequirementID: "REQ-001",
			},
		},
	}
}

func TestBuildReviewPrompt_Golden(t *testing.T) {
	assertGoldenPrompt(t, "review", buildReviewPrompt(goldenReviewContext()))
}

func TestBuildAuditPrompt_Golden(t *testing.T) {
	assertGoldenPrompt(t, "audit", buildAuditPrompt(goldenAuditContext()))
}

func TestBuildAnalysisPrompt_Golden(t *testing.T) {
	assertGoldenPrompt(t, "analysis", buildAnalysisPrompt(goldenAnalysisContext()))
}

// TestBuildAnalysisPrompt_NoPatterns locks the fallback shape used when no
// patterns are loaded: the prompt MUST still render, without the
// pattern-injection blocks.
func TestBuildAnalysisPrompt_NoPatterns(t *testing.T) {
	assertGoldenPrompt(t, "analysis_no_patterns", buildAnalysisPrompt(propose.AnalysisContext{}))
}

func TestBuildAdversarialPrompt_Golden(t *testing.T) {
	assertGoldenPrompt(t, "adversarial", buildAdversarialPrompt("develop"))
}

func TestBuildCompliancePrompt_Golden(t *testing.T) {
	assertGoldenPrompt(t, "compliance", buildCompliancePrompt("develop", goldenFeature()))
}

func TestBuildCoveragePrompt_Golden(t *testing.T) {
	assertGoldenPrompt(t, "coverage", buildCoveragePrompt("develop"))
}
