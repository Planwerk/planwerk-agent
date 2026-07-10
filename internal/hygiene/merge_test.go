package hygiene

import (
	"testing"

	"github.com/planwerk/planwerk-agent/internal/report"
)

func TestMergeResults_NilAdversarial(t *testing.T) {
	primary := &report.ReviewResult{
		Findings: []report.Finding{{ID: "C-001", Severity: report.SeverityCritical, File: "main.go"}},
		Summary:  "Test",
	}
	result := MergeResults(primary, nil)
	if len(result.Findings) != 1 {
		t.Errorf("expected 1 finding, got %d", len(result.Findings))
	}
}

func TestMergeResults_EmptyAdversarial(t *testing.T) {
	primary := &report.ReviewResult{
		Findings: []report.Finding{{ID: "C-001", Severity: report.SeverityCritical, File: "main.go"}},
	}
	adv := &report.ReviewResult{}
	result := MergeResults(primary, adv)
	if len(result.Findings) != 1 {
		t.Errorf("expected 1 finding, got %d", len(result.Findings))
	}
}

func TestMergeResults_NewFindings(t *testing.T) {
	primary := &report.ReviewResult{
		Findings: []report.Finding{
			{ID: "C-001", Severity: report.SeverityCritical, File: "main.go", Line: 10},
		},
		Summary: "Primary review",
	}
	adv := &report.ReviewResult{
		Findings: []report.Finding{
			{Severity: report.SeverityWarning, File: "handler.go", Line: 20, Title: "SSRF vector"},
		},
	}

	result := MergeResults(primary, adv)
	if len(result.Findings) != 2 {
		t.Fatalf("expected 2 findings, got %d", len(result.Findings))
	}
	if result.Findings[1].Title != "SSRF vector" {
		t.Errorf("expected adversarial finding appended, got %q", result.Findings[1].Title)
	}
}

func TestMergeResults_DuplicateKeepsHigherSeverity(t *testing.T) {
	primary := &report.ReviewResult{
		Findings: []report.Finding{
			{ID: "W-001", Severity: report.SeverityWarning, File: "auth.go", Line: 42, Title: "Weak check"},
		},
	}
	adv := &report.ReviewResult{
		Findings: []report.Finding{
			{Severity: report.SeverityCritical, File: "auth.go", Line: 42, Title: "Weak check"},
		},
	}

	result := MergeResults(primary, adv)
	if len(result.Findings) != 1 {
		t.Fatalf("expected 1 finding (deduplicated), got %d", len(result.Findings))
	}
	if result.Findings[0].Severity != report.SeverityCritical {
		t.Errorf("expected higher severity CRITICAL, got %s", result.Findings[0].Severity)
	}
	if result.Findings[0].ID != "W-001" {
		t.Errorf("expected preserved ID W-001, got %s", result.Findings[0].ID)
	}
}

func TestMergeResults_DuplicateKeepsPrimaryWhenEqual(t *testing.T) {
	primary := &report.ReviewResult{
		Findings: []report.Finding{
			{ID: "C-001", Severity: report.SeverityCritical, File: "db.go", Line: 10, Title: "SQL injection"},
		},
	}
	adv := &report.ReviewResult{
		Findings: []report.Finding{
			{Severity: report.SeverityCritical, File: "db.go", Line: 10, Title: "SQL injection"},
		},
	}

	result := MergeResults(primary, adv)
	if len(result.Findings) != 1 {
		t.Fatalf("expected 1 finding (deduplicated), got %d", len(result.Findings))
	}
	// Same severity — primary should be kept
	if result.Findings[0].Title != "SQL injection" {
		t.Errorf("expected primary finding preserved, got %q", result.Findings[0].Title)
	}
}

func TestMergeResults_DifferentTitlesSameLocation(t *testing.T) {
	primary := &report.ReviewResult{
		Findings: []report.Finding{
			{ID: "W-001", Severity: report.SeverityWarning, File: "auth.go", Line: 42, Title: "Missing nil check"},
		},
	}
	adv := &report.ReviewResult{
		Findings: []report.Finding{
			{Severity: report.SeverityCritical, File: "auth.go", Line: 42, Title: "Auth bypass"},
		},
	}

	result := MergeResults(primary, adv)
	if len(result.Findings) != 2 {
		t.Fatalf("expected 2 findings (different titles), got %d", len(result.Findings))
	}
}

func TestMergeResults_PreservesEnrichment(t *testing.T) {
	primary := &report.ReviewResult{
		Findings: []report.Finding{
			{
				ID:           "W-001",
				Severity:     report.SeverityWarning,
				File:         "auth.go",
				Line:         42,
				Title:        "Weak check",
				CodeSnippet:  "if user != nil {",
				SuggestedFix: "if user != nil && user.IsActive() {",
			},
		},
	}
	adv := &report.ReviewResult{
		Findings: []report.Finding{
			{
				Severity: report.SeverityCritical,
				File:     "auth.go",
				Line:     42,
				Title:    "Weak check",
				// Adversarial has no code snippet or suggested fix
			},
		},
	}

	result := MergeResults(primary, adv)
	if len(result.Findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(result.Findings))
	}
	f := result.Findings[0]
	if f.Severity != report.SeverityCritical {
		t.Errorf("expected upgraded severity CRITICAL, got %s", f.Severity)
	}
	if f.CodeSnippet != "if user != nil {" {
		t.Errorf("expected preserved CodeSnippet from primary, got %q", f.CodeSnippet)
	}
	if f.SuggestedFix != "if user != nil && user.IsActive() {" {
		t.Errorf("expected preserved SuggestedFix from primary, got %q", f.SuggestedFix)
	}
}

func TestMergeResults_MergesRelatedTo(t *testing.T) {
	primary := &report.ReviewResult{
		Findings: []report.Finding{
			{
				ID:        "W-001",
				Severity:  report.SeverityWarning,
				File:      "auth.go",
				Line:      42,
				Title:     "Weak check",
				RelatedTo: []string{"C-002"},
			},
		},
	}
	adv := &report.ReviewResult{
		Findings: []report.Finding{
			{
				Severity:  report.SeverityCritical,
				File:      "auth.go",
				Line:      42,
				Title:     "Weak check",
				RelatedTo: []string{"C-003"},
			},
		},
	}

	result := MergeResults(primary, adv)
	if len(result.Findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(result.Findings))
	}
	related := result.Findings[0].RelatedTo
	if len(related) != 2 {
		t.Fatalf("expected 2 related_to entries, got %d: %v", len(related), related)
	}
	// Should contain both C-003 (from adversarial) and C-002 (merged from primary)
	has := map[string]bool{}
	for _, r := range related {
		has[r] = true
	}
	if !has["C-002"] || !has["C-003"] {
		t.Errorf("expected C-002 and C-003, got %v", related)
	}
}

func TestMergeResults_CrossPassBoostsConfidence(t *testing.T) {
	primary := &report.ReviewResult{
		Findings: []report.Finding{
			{ID: "C-001", Severity: report.SeverityCritical, File: "db.go", Line: 10, Title: "SQL injection", Confidence: report.ConfidenceLikely, ConfirmedBy: []string{PassReview}},
		},
	}
	adv := &report.ReviewResult{
		Findings: []report.Finding{
			{Severity: report.SeverityCritical, File: "db.go", Line: 10, Title: "SQL injection", ConfirmedBy: []string{PassAdversarial}},
		},
	}

	result := MergeResults(primary, adv)
	if len(result.Findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(result.Findings))
	}
	f := result.Findings[0]
	if f.Confidence != report.ConfidenceVerified {
		t.Errorf("expected confidence boosted likely->verified, got %q", f.Confidence)
	}
	if len(f.ConfirmedBy) != 2 {
		t.Errorf("expected 2 confirming passes, got %v", f.ConfirmedBy)
	}
}

func TestMergeResults_NoSecondBoostBeyondTwoPasses(t *testing.T) {
	// Already confirmed by two passes; a third confirming pass adds provenance
	// but must NOT boost confidence a second time.
	primary := &report.ReviewResult{
		Findings: []report.Finding{
			{ID: "C-001", Severity: report.SeverityCritical, File: "db.go", Line: 10, Title: "X", Confidence: report.ConfidenceLikely, ConfirmedBy: []string{PassReview, PassAdversarial}},
		},
	}
	comp := &report.ReviewResult{
		Findings: []report.Finding{
			{Severity: report.SeverityCritical, File: "db.go", Line: 10, Title: "X", ConfirmedBy: []string{PassCompliance}},
		},
	}

	f := MergeResults(primary, comp).Findings[0]
	if f.Confidence != report.ConfidenceLikely {
		t.Errorf("confidence must stay likely (no second boost), got %q", f.Confidence)
	}
	if len(f.ConfirmedBy) != 3 {
		t.Errorf("expected 3 confirming passes, got %v", f.ConfirmedBy)
	}
}

func TestMergeResults_FuzzyTitleAndLineMatch(t *testing.T) {
	// Independently worded passes rarely produce byte-identical titles or land
	// on the exact same line; the fuzzy matcher must still fold them together.
	primary := &report.ReviewResult{
		Findings: []report.Finding{
			{ID: "C-001", Severity: report.SeverityCritical, File: "svc.go", Line: 42,
				Title: "nil pointer dereference in Foo", Confidence: report.ConfidenceLikely, ConfirmedBy: []string{PassReview}},
		},
	}
	adv := &report.ReviewResult{
		Findings: []report.Finding{
			{Severity: report.SeverityCritical, File: "svc.go", Line: 44,
				Title: "nil dereference in Foo", ConfirmedBy: []string{PassAdversarial}},
		},
	}
	result := MergeResults(primary, adv)
	if len(result.Findings) != 1 {
		t.Fatalf("expected 1 merged finding, got %d", len(result.Findings))
	}
	f := result.Findings[0]
	if len(f.ConfirmedBy) != 2 {
		t.Errorf("expected 2 confirming passes, got %v", f.ConfirmedBy)
	}
	if f.Confidence != report.ConfidenceVerified {
		t.Errorf("expected confidence boosted likely->verified, got %q", f.Confidence)
	}
}

func TestFindMatchIndex(t *testing.T) {
	tests := []struct {
		name     string
		existing []report.Finding
		sf       report.Finding
		want     int
	}{
		{
			name:     "different file never matches",
			existing: []report.Finding{{File: "a.go", Line: 10, Title: "leak"}},
			sf:       report.Finding{File: "b.go", Line: 10, Title: "leak"},
			want:     -1,
		},
		{
			name:     "same file distant lines never matches",
			existing: []report.Finding{{File: "a.go", Line: 42, Title: "off by one"}},
			sf:       report.Finding{File: "a.go", Line: 90, Title: "off by one"},
			want:     -1,
		},
		{
			name:     "low token overlap never matches",
			existing: []report.Finding{{File: "a.go", Line: 10, Title: "missing nil check"}},
			sf:       report.Finding{File: "a.go", Line: 10, Title: "auth bypass vector"},
			want:     -1,
		},
		{
			name:     "both line-less falls back to title similarity",
			existing: []report.Finding{{File: "a.go", Title: "goroutine leak in worker"}},
			sf:       report.Finding{File: "a.go", Title: "leak of goroutine in worker"},
			want:     0,
		},
		{
			name:     "one-sided line does not match",
			existing: []report.Finding{{File: "a.go", Line: 42, Title: "goroutine leak"}},
			sf:       report.Finding{File: "a.go", Line: 0, Title: "goroutine leak"},
			want:     -1,
		},
		{
			name:     "file-less secondary never matches",
			existing: []report.Finding{{File: "a.go", Line: 10, Title: "leak"}},
			sf:       report.Finding{File: "", Line: 10, Title: "leak"},
			want:     -1,
		},
		{
			name: "multiple candidates picks closest line among equal similarity",
			existing: []report.Finding{
				{File: "a.go", Line: 40, Title: "nil deref"},
				{File: "a.go", Line: 43, Title: "nil deref"},
			},
			sf:   report.Finding{File: "a.go", Line: 44, Title: "nil deref"},
			want: 1,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := findMatchIndex(tc.existing, tc.sf); got != tc.want {
				t.Errorf("findMatchIndex = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestMergeFindingPair_PreservesSemantics(t *testing.T) {
	kept := report.Finding{
		ID: "W-001", Severity: report.SeverityWarning, File: "a.go", Line: 10,
		Title: "weak check", CodeSnippet: "x := 1", SuggestedFix: "x := 2",
		ConfirmedBy: []string{PassReview},
	}
	dup := report.Finding{
		Severity: report.SeverityCritical, File: "a.go", Line: 10, Title: "weak check",
		ConfirmedBy: []string{PassAdversarial},
	}
	got := mergeFindingPair(kept, dup)
	if got.Severity != report.SeverityCritical {
		t.Errorf("higher severity must be adopted, got %s", got.Severity)
	}
	if got.ID != "W-001" {
		t.Errorf("kept ID must be preserved, got %s", got.ID)
	}
	if got.CodeSnippet != "x := 1" || got.SuggestedFix != "x := 2" {
		t.Errorf("enrichment must be preserved, got snippet=%q fix=%q", got.CodeSnippet, got.SuggestedFix)
	}
	if len(got.ConfirmedBy) != 2 {
		t.Errorf("expected 2 confirming passes, got %v", got.ConfirmedBy)
	}
}

func TestTagPass_OnlyFillsEmpty(t *testing.T) {
	r := &report.ReviewResult{
		Findings: []report.Finding{
			{Title: "fresh"},
			{Title: "already", ConfirmedBy: []string{PassAdversarial}},
		},
	}
	TagPass(r, PassReview)
	if got := r.Findings[0].ConfirmedBy; len(got) != 1 || got[0] != PassReview {
		t.Errorf("fresh finding = %v, want [review]", got)
	}
	if got := r.Findings[1].ConfirmedBy; len(got) != 1 || got[0] != PassAdversarial {
		t.Errorf("tagged finding must be untouched, got %v", got)
	}
}

func TestTagPass_NilResult(t *testing.T) {
	// A nil result is a documented no-op; it must not panic.
	TagPass(nil, PassReview)
}

func TestMergeResults_DoesNotMutateSummary(t *testing.T) {
	primary := &report.ReviewResult{
		Summary: "Clean review",
	}
	adv := &report.ReviewResult{
		Findings: []report.Finding{
			{Severity: report.SeverityInfo, File: "util.go", Line: 1},
		},
	}

	// The summary note is the caller's responsibility (review.appendSummaryNote),
	// so a single fold must leave the summary untouched.
	result := MergeResults(primary, adv)
	if result.Summary != "Clean review" {
		t.Errorf("MergeResults must not mutate summary, got %q", result.Summary)
	}
}
