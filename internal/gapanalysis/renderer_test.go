package gapanalysis

import (
	"bytes"
	"strings"
	"testing"

	"github.com/planwerk/planwerk-review/internal/report"
)

func sampleResult() *Result {
	return &Result{
		RepoFullName: "acme/widgets",
		Overview:     "One feature has a missing test.",
		Features: []FeatureGaps{
			{
				FeatureID:   "CC-0042",
				FeatureFile: "CC-0042-thing.json",
				Title:       "The Thing",
				Summary:     "Mostly done; one test missing.",
				Gaps: []Gap{
					{
						ID:          "W-001",
						FeatureID:   "CC-0042",
						FeatureFile: "CC-0042-thing.json",
						Type:        GapMissingTest,
						Severity:    report.SeverityWarning,
						Title:       "Missing test for thing handler",
						Description: "TestSpecification declared TestHandlerErrors in handler_test.go, no such test exists.",
						Evidence:    "grep TestHandlerErrors handler_test.go: no match",
						Source:      "TestHandlerErrors in handler_test.go [REQ-2]",
						Confidence:  ConfidenceVerified,
						Suggested: IssueSuggestion{
							Title: "Add TestHandlerErrors for CC-0042",
							Body:  "Body text",
						},
					},
				},
			},
		},
	}
}

func TestRenderMarkdown_ContainsKeySections(t *testing.T) {
	var buf bytes.Buffer
	RenderMarkdown(&buf, sampleResult(), "v1.2.3")
	out := buf.String()

	wants := []string{
		"# Gap Analysis: acme/widgets",
		"v1.2.3",
		"## Gaps Overview",
		"CC-0042",
		"missing_test",
		"Add TestHandlerErrors for CC-0042",
		"## CC-0042 — The Thing",
	}
	for _, w := range wants {
		if !strings.Contains(out, w) {
			t.Errorf("output missing %q\n----\n%s", w, out)
		}
	}
}

func TestRenderMarkdown_NoGaps(t *testing.T) {
	res := &Result{
		RepoFullName: "acme/widgets",
		Features: []FeatureGaps{
			{FeatureID: "CC-0001", Title: "Done", Summary: "fully implemented"},
		},
	}
	var buf bytes.Buffer
	RenderMarkdown(&buf, res, "")
	out := buf.String()
	if !strings.Contains(out, "All completed features appear fully implemented") {
		t.Errorf("expected fully-implemented message, got:\n%s", out)
	}
	if strings.Contains(out, "Gaps Overview") {
		t.Errorf("table must not appear when there are no gaps:\n%s", out)
	}
}

func TestRenderJSON_RoundTrip(t *testing.T) {
	in := sampleResult()
	var buf bytes.Buffer
	if err := RenderJSON(&buf, in); err != nil {
		t.Fatalf("RenderJSON: %v", err)
	}
	if !strings.Contains(buf.String(), `"feature_id": "CC-0042"`) {
		t.Errorf("JSON missing feature_id, got:\n%s", buf.String())
	}
}

func TestAllGaps_Order(t *testing.T) {
	res := &Result{Features: []FeatureGaps{
		{FeatureID: "A", Gaps: []Gap{{ID: "1"}, {ID: "2"}}},
		{FeatureID: "B", Gaps: []Gap{{ID: "3"}}},
	}}
	got := res.AllGaps()
	if len(got) != 3 {
		t.Fatalf("got %d gaps, want 3", len(got))
	}
	if got[0].ID != "1" || got[1].ID != "2" || got[2].ID != "3" {
		t.Errorf("order wrong: %+v", got)
	}
}

func TestBuildIssueBody_FallbackContents(t *testing.T) {
	g := Gap{
		ID:          "W-005",
		FeatureID:   "CC-0007",
		FeatureFile: "CC-0007-foo.json",
		Type:        GapMissingCriterion,
		Severity:    report.SeverityWarning,
		Title:       "Acceptance criterion not implemented",
		Description: "The login flow rejects empty passwords, but no such guard exists.",
		Evidence:    "auth/login.go: no password length check",
		Source:      "User can not submit an empty password",
		Confidence:  ConfidenceLikely,
	}
	body := buildIssueBody(g)
	for _, want := range []string{"Severity", "WARNING", "missing_criterion", "From the Planwerk spec", "auth/login.go"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q:\n%s", want, body)
		}
	}
}
