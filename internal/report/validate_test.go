package report

import (
	"strings"
	"testing"
)

func TestFindingValidate(t *testing.T) {
	// valid is a finding that satisfies every rule; each case mutates one field.
	valid := func() Finding {
		return Finding{
			Title:      "Missing error wrapping",
			Severity:   SeverityWarning,
			Confidence: ConfidenceVerified,
		}
	}

	tests := []struct {
		name    string
		mutate  func(*Finding)
		wantErr string // substring expected in the error; "" means no error
	}{
		{"valid", func(*Finding) {}, ""},
		{"empty title", func(f *Finding) { f.Title = "" }, "title is empty"},
		{"whitespace title", func(f *Finding) { f.Title = "   " }, "title is empty"},
		{"missing severity", func(f *Finding) { f.Severity = "" }, "severity"},
		{"invalid severity", func(f *Finding) { f.Severity = "FATAL" }, "severity"},
		{"missing confidence", func(f *Finding) { f.Confidence = "" }, "confidence"},
		{"invalid confidence", func(f *Finding) { f.Confidence = "sure" }, "confidence"},
		{"mixed-case confidence", func(f *Finding) { f.Confidence = "Verified" }, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := valid()
			tt.mutate(&f)
			err := f.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want substring %q", err, tt.wantErr)
			}
		})
	}
}

func TestReviewResultValidate(t *testing.T) {
	good := Finding{Title: "ok", Severity: SeverityInfo, Confidence: ConfidenceLikely}

	t.Run("nil findings", func(t *testing.T) {
		var r ReviewResult
		if err := r.Validate(); err != nil {
			t.Fatalf("empty result should be valid, got: %v", err)
		}
	})

	t.Run("empty findings", func(t *testing.T) {
		r := ReviewResult{Findings: []Finding{}}
		if err := r.Validate(); err != nil {
			t.Fatalf("empty findings should be valid, got: %v", err)
		}
	})

	t.Run("surfaces offending index and title", func(t *testing.T) {
		r := ReviewResult{Findings: []Finding{
			good,
			{Title: "", Severity: SeverityInfo, Confidence: ConfidenceLikely},
		}}
		err := r.Validate()
		if err == nil {
			t.Fatal("expected error for a result with an invalid second finding")
		}
		if !strings.Contains(err.Error(), "finding 1") {
			t.Errorf("error should name the offending index, got: %v", err)
		}
		if !strings.Contains(err.Error(), "title is empty") {
			t.Errorf("error should wrap the finding violation, got: %v", err)
		}
	})
}
