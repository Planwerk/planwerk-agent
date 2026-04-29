package report

import (
	"encoding/json"
	"testing"
)

func TestParseSeverity(t *testing.T) {
	tests := []struct {
		input   string
		want    Severity
		wantErr bool
	}{
		{"BLOCKING", SeverityBlocking, false},
		{"blocking", SeverityBlocking, false},
		{"CRITICAL", SeverityCritical, false},
		{"critical", SeverityCritical, false},
		{"WARNING", SeverityWarning, false},
		{"warning", SeverityWarning, false},
		{"INFO", SeverityInfo, false},
		{"info", SeverityInfo, false},
		{"unknown", "", true},
		{"", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := ParseSeverity(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q", tt.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("ParseSeverity(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestDeriveFixClass(t *testing.T) {
	tests := []struct {
		input Actionability
		want  FixClass
	}{
		{ActionabilityAutoFix, FixClassAutoFix},
		{ActionabilityNeedsDiscussion, FixClassAsk},
		{ActionabilityArchitectural, FixClassAsk},
		{"", FixClassAsk},
		{"unknown", FixClassAsk},
	}

	for _, tt := range tests {
		t.Run(string(tt.input), func(t *testing.T) {
			got := DeriveFixClass(tt.input)
			if got != tt.want {
				t.Errorf("DeriveFixClass(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestSeverityUnmarshalJSON(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    Severity
		wantErr bool
	}{
		{"uppercase", `"CRITICAL"`, SeverityCritical, false},
		{"lowercase", `"warning"`, SeverityWarning, false},
		{"mixed case", `"Blocking"`, SeverityBlocking, false},
		{"with whitespace", `" info "`, SeverityInfo, false},
		{"unknown passes through", `"custom"`, Severity("CUSTOM"), false},
		{"invalid json", `123`, Severity(""), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got Severity
			err := json.Unmarshal([]byte(tt.input), &got)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("UnmarshalJSON(%s) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestFindingUnmarshalNormalizesSeverity(t *testing.T) {
	input := `{"severity": "critical", "title": "test", "problem": "p", "action": "a"}`
	var f Finding
	if err := json.Unmarshal([]byte(input), &f); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f.Severity != SeverityCritical {
		t.Errorf("Finding.Severity = %q, want %q", f.Severity, SeverityCritical)
	}
}

func TestNormalizeConfidence(t *testing.T) {
	tests := []struct {
		input string
		want  Confidence
	}{
		{"verified", ConfidenceVerified},
		{"VERIFIED", ConfidenceVerified},
		{"Verified", ConfidenceVerified},
		{"likely", ConfidenceLikely},
		{"LIKELY", ConfidenceLikely},
		{"uncertain", ConfidenceUncertain},
		{"UNCERTAIN", ConfidenceUncertain},
		{"  likely  ", ConfidenceLikely},
		{"unknown", ConfidenceUncertain},
		{"", ConfidenceUncertain},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := NormalizeConfidence(tt.input)
			if got != tt.want {
				t.Errorf("NormalizeConfidence(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestFindingUnmarshalWithNewFields(t *testing.T) {
	input := `{
		"severity": "critical",
		"title": "SQL injection",
		"file": "db.go",
		"line": 42,
		"line_end": 45,
		"confidence": "verified",
		"problem": "User input in query",
		"action": "Use parameterized query",
		"code_snippet": "db.Query(\"SELECT * FROM users WHERE id=\" + id)",
		"suggested_fix": "db.Query(\"SELECT * FROM users WHERE id=?\", id)",
		"related_to": ["Missing input validation"]
	}`
	var f Finding
	if err := json.Unmarshal([]byte(input), &f); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f.LineEnd != 45 {
		t.Errorf("LineEnd = %d, want 45", f.LineEnd)
	}
	if f.Confidence != "verified" {
		t.Errorf("Confidence = %q, want %q", f.Confidence, "verified")
	}
	if f.CodeSnippet == "" {
		t.Error("CodeSnippet should not be empty")
	}
	if f.SuggestedFix == "" {
		t.Error("SuggestedFix should not be empty")
	}
	if len(f.RelatedTo) != 1 || f.RelatedTo[0] != "Missing input validation" {
		t.Errorf("RelatedTo = %v, want [Missing input validation]", f.RelatedTo)
	}
}

func TestFindingUnmarshalBackwardCompat(t *testing.T) {
	input := `{"severity": "warning", "title": "test", "problem": "p", "action": "a"}`
	var f Finding
	if err := json.Unmarshal([]byte(input), &f); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f.LineEnd != 0 {
		t.Errorf("LineEnd should be zero value, got %d", f.LineEnd)
	}
	if f.Confidence != "" {
		t.Errorf("Confidence should be empty, got %q", f.Confidence)
	}
	if f.CodeSnippet != "" {
		t.Errorf("CodeSnippet should be empty, got %q", f.CodeSnippet)
	}
	if f.SuggestedFix != "" {
		t.Errorf("SuggestedFix should be empty, got %q", f.SuggestedFix)
	}
	if f.RelatedTo != nil {
		t.Errorf("RelatedTo should be nil, got %v", f.RelatedTo)
	}
}

func TestFindingUnmarshalFixOptions(t *testing.T) {
	input := `{
		"severity": "warning",
		"title": "Broad catch swallows specific failures",
		"file": "internal/foo/bar.go",
		"line": 12,
		"actionability": "needs-discussion",
		"problem": "catch-all hides distinct error classes",
		"action": "narrow or re-raise",
		"fix_options": [
			{"id": "A", "approach": "Catch specific types", "pros": "precise", "cons": "more code", "effort": "MED", "risk_if_skipped": "real bugs stay hidden"},
			{"id": "B", "approach": "Re-raise after logging", "pros": "loud failures", "cons": "caller must handle", "effort": "LOW", "risk_if_skipped": "silent state corruption"}
		],
		"recommended_option": "A",
		"recommendation_reasoning": "matches existing handlers in pkg/io"
	}`
	var f Finding
	if err := json.Unmarshal([]byte(input), &f); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := len(f.FixOptions); got != 2 {
		t.Fatalf("FixOptions length = %d, want 2", got)
	}
	if f.FixOptions[0].ID != "A" || f.FixOptions[0].Effort != "MED" {
		t.Errorf("FixOptions[0] = %+v, want id=A effort=MED", f.FixOptions[0])
	}
	if f.RecommendedOption != "A" {
		t.Errorf("RecommendedOption = %q, want A", f.RecommendedOption)
	}
	if f.RecommendationReasoning == "" {
		t.Error("RecommendationReasoning should not be empty")
	}
}

func TestMeetsMinimum(t *testing.T) {
	tests := []struct {
		sev  Severity
		min  Severity
		want bool
	}{
		{SeverityBlocking, SeverityInfo, true},
		{SeverityCritical, SeverityInfo, true},
		{SeverityWarning, SeverityInfo, true},
		{SeverityInfo, SeverityInfo, true},
		{SeverityInfo, SeverityWarning, false},
		{SeverityWarning, SeverityCritical, false},
		{SeverityBlocking, SeverityBlocking, true},
	}

	for _, tt := range tests {
		t.Run(string(tt.sev)+"_meets_"+string(tt.min), func(t *testing.T) {
			if got := tt.sev.MeetsMinimum(tt.min); got != tt.want {
				t.Errorf("%s.MeetsMinimum(%s) = %v, want %v", tt.sev, tt.min, got, tt.want)
			}
		})
	}
}
