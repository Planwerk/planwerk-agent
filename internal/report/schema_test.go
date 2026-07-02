package report_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/planwerk/planwerk-agent/internal/draft"
	"github.com/planwerk/planwerk-agent/internal/propose"
	"github.com/planwerk/planwerk-agent/internal/report"
	"github.com/planwerk/planwerk-agent/internal/report/schema"
)

// compileSchema compiles a draft 2020-12 schema document from raw bytes.
func compileSchema(t *testing.T, name string, doc []byte) *jsonschema.Schema {
	t.Helper()
	parsed, err := jsonschema.UnmarshalJSON(bytes.NewReader(doc))
	if err != nil {
		t.Fatalf("unmarshal schema %s: %v", name, err)
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource(name, parsed); err != nil {
		t.Fatalf("add schema %s: %v", name, err)
	}
	sch, err := c.Compile(name)
	if err != nil {
		t.Fatalf("compile schema %s: %v", name, err)
	}
	return sch
}

// validate parses doc as JSON and validates it against sch. A doc that is not
// valid JSON fails the test outright; the returned error is the schema-level
// validation result for the caller to assert on.
func validate(t *testing.T, sch *jsonschema.Schema, doc []byte) error {
	t.Helper()
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(doc))
	if err != nil {
		t.Fatalf("instance is not valid JSON: %v", err)
	}
	return sch.Validate(inst)
}

// TestSchemasCompile guards against malformed schema JSON: both embedded
// documents must compile as valid draft 2020-12 schemas.
func TestSchemasCompile(t *testing.T) {
	compileSchema(t, "report-result.schema.json", schema.ReportResult)
	compileSchema(t, "proposal.schema.json", schema.Proposal)
	compileSchema(t, "rebase-analysis.schema.json", schema.RebaseAnalysis)
	compileSchema(t, "draft.schema.json", schema.Draft)
	compileSchema(t, "address-result.schema.json", schema.AddressResult)
	compileSchema(t, "structured-review.schema.json", schema.StructuredReview)
}

// TestFixturesValidateAgainstSchema validates every JSON fixture under
// testdata/schema/<dir> against its schema (acceptance criterion: every
// fixture validates). Keep all fixtures valid — TestInvalidDocumentRejected
// covers the negative cases with inline documents.
func TestFixturesValidateAgainstSchema(t *testing.T) {
	for _, tc := range []struct {
		dir string
		doc []byte
	}{
		{dir: "report-result", doc: schema.ReportResult},
		{dir: "proposal", doc: schema.Proposal},
		{dir: "rebase-analysis", doc: schema.RebaseAnalysis},
		{dir: "draft", doc: schema.Draft},
		{dir: "address-result", doc: schema.AddressResult},
		{dir: "structured-review", doc: schema.StructuredReview},
	} {
		t.Run(tc.dir, func(t *testing.T) {
			sch := compileSchema(t, tc.dir, tc.doc)
			glob := filepath.Join("testdata", "schema", tc.dir, "*.json")
			files, err := filepath.Glob(glob)
			if err != nil {
				t.Fatalf("glob %s: %v", glob, err)
			}
			if len(files) == 0 {
				t.Fatalf("no fixtures found under %s", glob)
			}
			for _, f := range files {
				t.Run(filepath.Base(f), func(t *testing.T) {
					data, err := os.ReadFile(f)
					if err != nil {
						t.Fatalf("read %s: %v", f, err)
					}
					if err := validate(t, sch, data); err != nil {
						t.Fatalf("fixture %s failed schema validation: %v", f, err)
					}
				})
			}
		})
	}
}

// TestRendererOutputMatchesSchema is the drift guard the issue motivates: it
// renders populated results through the real renderers and validates the bytes
// against the schema. Because the schemas forbid additional properties, a new
// struct field without a matching schema entry fails this test.
func TestRendererOutputMatchesSchema(t *testing.T) {
	t.Run("review", func(t *testing.T) {
		sch := compileSchema(t, "report-result.schema.json", schema.ReportResult)
		rr := report.ReviewResult{
			Findings:       []report.Finding{populatedFinding()},
			Summary:        "a populated summary",
			Recommendation: "a populated recommendation",
		}
		var buf bytes.Buffer
		if err := report.NewRenderer(&buf).RenderJSON(rr, report.SeverityInfo, ""); err != nil {
			t.Fatalf("RenderJSON: %v", err)
		}
		if err := validate(t, sch, buf.Bytes()); err != nil {
			t.Fatalf("rendered review output does not match schema: %v\n%s", err, buf.String())
		}
	})

	t.Run("propose", func(t *testing.T) {
		sch := compileSchema(t, "proposal.schema.json", schema.Proposal)
		pr := propose.ProposalResult{
			RepositoryOverview: "a populated overview",
			Proposals:          []propose.Proposal{populatedProposal()},
		}
		var buf bytes.Buffer
		if err := propose.NewRenderer(&buf).RenderJSON(pr); err != nil {
			t.Fatalf("RenderJSON: %v", err)
		}
		if err := validate(t, sch, buf.Bytes()); err != nil {
			t.Fatalf("rendered propose output does not match schema: %v\n%s", err, buf.String())
		}
	})

	t.Run("rebase", func(t *testing.T) {
		sch := compileSchema(t, "rebase-analysis.schema.json", schema.RebaseAnalysis)
		var buf bytes.Buffer
		if err := report.NewRenderer(&buf).RenderRebaseAnalysisJSON(populatedRebaseAnalysis()); err != nil {
			t.Fatalf("RenderRebaseAnalysisJSON: %v", err)
		}
		if err := validate(t, sch, buf.Bytes()); err != nil {
			t.Fatalf("rendered rebase output does not match schema: %v\n%s", err, buf.String())
		}
	})

	t.Run("draft", func(t *testing.T) {
		sch := compileSchema(t, "draft.schema.json", schema.Draft)
		var buf bytes.Buffer
		if err := draft.NewRenderer(&buf).RenderJSON(populatedDraft()); err != nil {
			t.Fatalf("RenderJSON: %v", err)
		}
		if err := validate(t, sch, buf.Bytes()); err != nil {
			t.Fatalf("rendered draft output does not match schema: %v\n%s", err, buf.String())
		}
	})

	// AddressResult has no renderer — it is the address session's own
	// structured output — so the drift guard marshals a fully-populated value
	// directly. additionalProperties:false still catches a struct field that
	// the schema does not declare.
	t.Run("address", func(t *testing.T) {
		sch := compileSchema(t, "address-result.schema.json", schema.AddressResult)
		data, err := json.Marshal(populatedAddressResult())
		if err != nil {
			t.Fatalf("marshal AddressResult: %v", err)
		}
		if err := validate(t, sch, data); err != nil {
			t.Fatalf("marshaled address output does not match schema: %v\n%s", err, data)
		}
	})
}

// TestInvalidRebaseAnalysisRejected feeds inline documents that violate the
// rebase-analysis contract and asserts the validator rejects each.
func TestInvalidRebaseAnalysisRejected(t *testing.T) {
	sch := compileSchema(t, "rebase-analysis.schema.json", schema.RebaseAnalysis)
	for name, doc := range map[string]string{
		"bad kind enum":           `{"commits":[{"sha":"a","subject":"s","adjustments":[{"kind":"reworded","file":"f","detail":"d","action":"a"}]}],"summary":"","recommendation":""}`,
		"missing required action": `{"commits":[{"sha":"a","subject":"s","adjustments":[{"kind":"lint-rule","file":"f","detail":"d"}]}],"summary":"","recommendation":""}`,
		"unknown property":        `{"commits":null,"summary":"","recommendation":"","extra":true}`,
	} {
		t.Run(name, func(t *testing.T) {
			if err := validate(t, sch, []byte(doc)); err == nil {
				t.Fatalf("expected a validation error for %q, got nil", name)
			}
		})
	}
}

// TestInvalidStructuredReviewRejected feeds inline documents that violate the
// structured-review wire contract and asserts the validator rejects each.
func TestInvalidStructuredReviewRejected(t *testing.T) {
	sch := compileSchema(t, "structured-review.schema.json", schema.StructuredReview)
	for name, doc := range map[string]string{
		"off-enum severity":     `{"findings":[{"id":"","severity":"SEV","title":"t","file":"f","problem":"p","action":"a"}],"summary":"","recommendation":"","source_finding_count":1}`,
		"missing source count":  `{"findings":null,"summary":"","recommendation":""}`,
		"top-level extra field": `{"findings":null,"summary":"","recommendation":"","source_finding_count":0,"extra":true}`,
		"finding extra field":   `{"findings":[{"id":"","severity":"INFO","title":"t","file":"f","problem":"p","action":"a","confirmed_by":["review"]}],"summary":"","recommendation":"","source_finding_count":1}`,
	} {
		t.Run(name, func(t *testing.T) {
			if err := validate(t, sch, []byte(doc)); err == nil {
				t.Fatalf("expected a validation error for %q, got nil", name)
			}
		})
	}
}

// TestInvalidAddressResultRejected feeds inline documents that violate the
// address-result contract and asserts the validator rejects each.
func TestInvalidAddressResultRejected(t *testing.T) {
	sch := compileSchema(t, "address-result.schema.json", schema.AddressResult)
	for name, doc := range map[string]string{
		"bad status enum":            `{"threads":null,"summary":"","status":"FINISHED"}`,
		"bad thread status enum":     `{"threads":[{"thread_id":"t","status":"OK","summary":"s"}],"summary":"","status":"DONE"}`,
		"missing required thread_id": `{"threads":[{"status":"DONE","summary":"s"}],"summary":"","status":"DONE"}`,
		"unknown property":           `{"threads":null,"summary":"","status":"DONE","extra":true}`,
	} {
		t.Run(name, func(t *testing.T) {
			if err := validate(t, sch, []byte(doc)); err == nil {
				t.Fatalf("expected a validation error for %q, got nil", name)
			}
		})
	}
}

// TestInvalidDocumentRejected feeds inline (not testdata) documents that
// violate the contract and asserts the validator rejects each, so the negative
// path is exercised without weakening "every fixture validates".
func TestInvalidDocumentRejected(t *testing.T) {
	sch := compileSchema(t, "report-result.schema.json", schema.ReportResult)
	for name, doc := range map[string]string{
		"bad severity enum":       `{"findings":[{"id":"X-1","severity":"FATAL","title":"t","file":"f","problem":"p","action":"a"}],"summary":"","recommendation":""}`,
		"missing required action": `{"findings":[{"id":"X-1","severity":"INFO","title":"t","file":"f","problem":"p"}],"summary":"","recommendation":""}`,
		"unknown property":        `{"findings":null,"summary":"","recommendation":"","extra":true}`,
	} {
		t.Run(name, func(t *testing.T) {
			if err := validate(t, sch, []byte(doc)); err == nil {
				t.Fatalf("expected a validation error for %q, got nil", name)
			}
		})
	}
}

// populatedFinding returns a Finding with every field set so the drift guard
// exercises the full schema surface. Confidence is verified so the renderer
// keeps it in its severity bucket rather than the unverified section.
func populatedFinding() report.Finding {
	return report.Finding{
		ID:            "C-001",
		Severity:      report.SeverityCritical,
		Title:         "SQL injection in user query",
		File:          "db/users.go",
		Line:          87,
		LineEnd:       92,
		Pattern:       "Injection",
		Actionability: report.ActionabilityNeedsDiscussion,
		FixClass:      report.FixClassAsk,
		Confidence:    report.ConfidenceVerified,
		Problem:       "User input is concatenated into a SQL query.",
		Action:        "Use a parameterized query.",
		CodeSnippet:   "db.Query(\"... \" + id)",
		SuggestedFix:  "db.Query(\"... WHERE id = ?\", id)",
		FixOptions: []report.FixOption{
			{
				ID:            "A",
				Approach:      "Use prepared statements.",
				Pros:          "Eliminates the injection.",
				Cons:          "Touches every query.",
				Effort:        "MED",
				RiskIfSkipped: "Database compromise.",
			},
		},
		RecommendedOption:       "A",
		RecommendationReasoning: "Prepared statements are the standard fix.",
		RelatedTo:               []string{"B-001"},
		ConfirmedBy:             []string{"review", "adversarial"},
		VerificationNote:        "refuted: the input is already parameterized at db/users.go:80",
	}
}

// populatedRebaseAnalysis returns a RebaseAnalysis with every field set so the
// drift guard exercises the full rebase-analysis schema surface: a commit with
// a fully-populated adjustment and a commit with none.
func populatedRebaseAnalysis() report.RebaseAnalysis {
	return report.RebaseAnalysis{
		Commits: []report.CommitAnalysis{
			{
				SHA:     "a1b2c3d4e5f60718293a4b5c6d7e8f9012345678",
				Subject: "Add the parser entry point",
				Adjustments: []report.Adjustment{
					{
						Kind:        "changed-signature",
						File:        "internal/parse/parser.go",
						Detail:      "Upstream changed the Parse signature; this commit calls the old one.",
						Action:      "Thread ctx through and pass it to Parse.",
						UpstreamRef: "9f8e7d6 Thread context through the parser",
						Confidence:  "verified",
					},
				},
			},
			{
				SHA:         "b2c3d4e5f60718293a4b5c6d7e8f901234567890",
				Subject:     "Wire the parser into the CLI",
				Adjustments: nil,
			},
		},
		Summary:        "Two commits replayed cleanly; the first needs a signature adjustment.",
		Recommendation: "Apply the adjustment before pushing.",
	}
}

// populatedAddressResult returns an AddressResult with every field set so the
// drift guard exercises the full address-result schema surface: a thread with
// files and a thread without (Files omitted).
func populatedAddressResult() report.AddressResult {
	return report.AddressResult{
		Threads: []report.AddressedThread{
			{
				ThreadID: "PRRT_kwDOAbc123",
				Status:   "DONE",
				Summary:  "Renamed the helper and updated its callers as the reviewer asked.",
				Files:    []string{"internal/foo/bar.go", "internal/foo/bar_test.go"},
			},
			{
				ThreadID: "PRRT_kwDOAbc456",
				Status:   "DONE_WITH_CONCERNS",
				Summary:  "Applied the guard, but flagged an edge case for a follow-up.",
			},
		},
		Summary: "Addressed both threads; one carries a reservation.",
		Status:  "DONE_WITH_CONCERNS",
	}
}

// populatedDraft returns a draft.Result with every field set so the drift
// guard exercises the full draft schema surface. Scope is a valid enum member.
func populatedDraft() draft.Result {
	return draft.Result{
		Title:       "Add a dark mode toggle to the settings page",
		Description: "Add a toggle that switches the interface to a dark palette.",
		Motivation:  "Night-time users want a theme that does not strain the eyes.",
		Scope:       "Medium",
		Body:        draft.BuildIssueBody(&draft.Result{Title: "Add a dark mode toggle to the settings page", Description: "Add a toggle that switches the interface to a dark palette.", Motivation: "Night-time users want a theme that does not strain the eyes.", Scope: "Medium"}),
	}
}

// populatedProposal returns a Proposal with every field set so the drift guard
// exercises the full proposal schema surface.
func populatedProposal() propose.Proposal {
	return propose.Proposal{
		ID:                 "P-001",
		Priority:           "HIGH",
		Category:           "testing",
		Title:              "Add JSON schema contract tests",
		Description:        "Validate JSON output against a declared schema.",
		Motivation:         "Catch silent output regressions.",
		Scope:              "Medium",
		AffectedAreas:      []string{"internal/report/schema"},
		AcceptanceCriteria: []string{"Schema files exist.", "Fixtures validate."},
	}
}
