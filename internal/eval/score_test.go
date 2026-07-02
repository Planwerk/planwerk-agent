package eval

import (
	"math"
	"testing"

	"github.com/planwerk/planwerk-agent/internal/report"
)

// pf builds a predicted finding.
func pf(file string, line int, sev report.Severity, title, problem string) report.Finding {
	return report.Finding{File: file, Line: line, Severity: sev, Title: title, Problem: problem}
}

// ef builds an expected finding.
func ef(file string, line int, sev string, keywords ...string) ExpectedFinding {
	return ExpectedFinding{File: file, Line: line, Severity: sev, Keywords: keywords}
}

// caseWith builds a Case whose Expected has the given clean flag and findings.
func caseWith(clean bool, findings ...ExpectedFinding) Case {
	return Case{Name: "t", Expected: Expected{Clean: clean, Findings: findings}}
}

// wantDefined asserts a ratio method returned defined==want, and when defined,
// that its value is approximately v.
func wantDefined(t *testing.T, label string, got float64, ok, wantOK bool, v float64) {
	t.Helper()
	if ok != wantOK {
		t.Errorf("%s defined = %v, want %v", label, ok, wantOK)
		return
	}
	if ok && math.Abs(got-v) > 1e-9 {
		t.Errorf("%s = %v, want %v", label, got, v)
	}
}

func TestScoreCase(t *testing.T) {
	tests := []struct {
		name               string
		c                  Case
		preds              []report.Finding
		wantTP, wantFP     int
		wantFN, wantSevHit int
	}{
		{
			name:       "perfect match",
			c:          caseWith(false, ef("a.go", 10, "CRITICAL", "leak")),
			preds:      []report.Finding{pf("a.go", 10, report.SeverityCritical, "Goroutine leak", "leaks forever")},
			wantTP:     1,
			wantSevHit: 1,
		},
		{
			name:   "wrong file is a miss",
			c:      caseWith(false, ef("a.go", 10, "CRITICAL", "leak")),
			preds:  []report.Finding{pf("b.go", 10, report.SeverityCritical, "leak", "leaks")},
			wantFN: 1, wantFP: 1,
		},
		{
			name:   "keyword absent is a miss",
			c:      caseWith(false, ef("a.go", 10, "CRITICAL", "injection")),
			preds:  []report.Finding{pf("a.go", 10, report.SeverityCritical, "style nit", "rename this")},
			wantFN: 1, wantFP: 1,
		},
		{
			name:       "severity mismatch still true positive",
			c:          caseWith(false, ef("a.go", 10, "CRITICAL", "leak")),
			preds:      []report.Finding{pf("a.go", 10, report.SeverityWarning, "leak", "leaks")},
			wantTP:     1,
			wantSevHit: 0,
		},
		{
			name:       "line within tolerance matches",
			c:          caseWith(false, ef("a.go", 10, "WARNING", "leak")),
			preds:      []report.Finding{pf("a.go", 13, report.SeverityWarning, "leak", "leaks")},
			wantTP:     1,
			wantSevHit: 1,
		},
		{
			name:   "line beyond tolerance misses",
			c:      caseWith(false, ef("a.go", 10, "WARNING", "leak")),
			preds:  []report.Finding{pf("a.go", 14, report.SeverityWarning, "leak", "leaks")},
			wantFN: 1, wantFP: 1,
		},
		{
			name:       "keyword case-insensitive in problem",
			c:          caseWith(false, ef("a.go", 10, "BLOCKING", "Injection")),
			preds:      []report.Finding{pf("a.go", 10, report.SeverityBlocking, "SQL issue", "possible INJECTION via concat")},
			wantTP:     1,
			wantSevHit: 1,
		},
		{
			name: "one prediction cannot satisfy two expected (one-to-one)",
			c:    caseWith(false, ef("a.go", 10, "WARNING", "leak"), ef("a.go", 11, "WARNING", "leak")),
			preds: []report.Finding{
				pf("a.go", 10, report.SeverityWarning, "leak", "leaks"),
			},
			wantTP: 1, wantFN: 1, wantSevHit: 1,
		},
		{
			name: "partial recall and precision",
			c:    caseWith(false, ef("a.go", 10, "WARNING", "leak"), ef("b.go", 20, "CRITICAL", "nil map")),
			preds: []report.Finding{
				pf("a.go", 10, report.SeverityWarning, "leak", "leaks"),
				pf("c.go", 1, report.SeverityInfo, "nit", "spacing"),
				pf("d.go", 2, report.SeverityInfo, "nit", "naming"),
			},
			wantTP: 1, wantFP: 2, wantFN: 1, wantSevHit: 1,
		},
		{
			name:   "clean case with false positives",
			c:      caseWith(true),
			preds:  []report.Finding{pf("a.go", 1, report.SeverityInfo, "x", "y"), pf("b.go", 2, report.SeverityInfo, "x", "y")},
			wantFP: 2,
		},
		{
			name: "clean case with no findings",
			c:    caseWith(true),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := ScoreCase(tt.c, report.ReviewResult{Findings: tt.preds})
			if s.TP != tt.wantTP || s.FP != tt.wantFP || s.FN != tt.wantFN || s.SeverityMatches != tt.wantSevHit {
				t.Fatalf("Score = {TP:%d FP:%d FN:%d Sev:%d}, want {TP:%d FP:%d FN:%d Sev:%d}",
					s.TP, s.FP, s.FN, s.SeverityMatches, tt.wantTP, tt.wantFP, tt.wantFN, tt.wantSevHit)
			}
			if s.Clean != tt.c.Expected.Clean {
				t.Errorf("Clean = %v, want %v", s.Clean, tt.c.Expected.Clean)
			}
		})
	}
}

func TestScoreRatios(t *testing.T) {
	t.Run("perfect", func(t *testing.T) {
		s := Score{TP: 1, SeverityMatches: 1}
		p, ok := s.Precision()
		wantDefined(t, "precision", p, ok, true, 1.0)
		r, ok := s.Recall()
		wantDefined(t, "recall", r, ok, true, 1.0)
		a, ok := s.SeverityAccuracy()
		wantDefined(t, "sev-acc", a, ok, true, 1.0)
	})

	t.Run("partial fractions", func(t *testing.T) {
		s := Score{TP: 1, FP: 2, FN: 1, SeverityMatches: 0}
		p, ok := s.Precision()
		wantDefined(t, "precision", p, ok, true, 1.0/3.0)
		r, ok := s.Recall()
		wantDefined(t, "recall", r, ok, true, 0.5)
		a, ok := s.SeverityAccuracy()
		wantDefined(t, "sev-acc", a, ok, true, 0.0)
	})

	t.Run("clean case: recall undefined, precision from FP", func(t *testing.T) {
		s := Score{Clean: true, FP: 2}
		p, ok := s.Precision()
		wantDefined(t, "precision", p, ok, true, 0.0)
		_, ok = s.Recall()
		if ok {
			t.Error("recall must be undefined for a clean case")
		}
		_, ok = s.SeverityAccuracy()
		if ok {
			t.Error("severity accuracy must be undefined when TP == 0")
		}
	})

	t.Run("no predictions and no expected: all undefined", func(t *testing.T) {
		s := Score{Clean: true}
		if _, ok := s.Precision(); ok {
			t.Error("precision must be undefined with no predictions")
		}
		if _, ok := s.Recall(); ok {
			t.Error("recall must be undefined with no expected findings")
		}
	})
}

func TestBuildReportAggregate(t *testing.T) {
	scored := []Scored{
		{Case: caseWith(false, ef("a.go", 10, "WARNING", "leak")), Score: Score{TP: 1, SeverityMatches: 1}},
		{Case: caseWith(false, ef("b.go", 20, "CRITICAL", "nil")), Score: Score{FN: 1, FP: 3}},
		{Case: caseWith(true), Score: Score{Clean: true, FP: 1}},
	}

	rep := BuildReport(scored)
	if len(rep.Cases) != 3 {
		t.Fatalf("cases = %d, want 3", len(rep.Cases))
	}

	agg := rep.Aggregate
	// TP=1, FP=4, FN=1, SevMatches=1 across the three cases.
	if agg.TP != 1 || agg.FP != 4 || agg.FN != 1 || agg.SeverityMatches != 1 {
		t.Fatalf("aggregate = {TP:%d FP:%d FN:%d Sev:%d}, want {1 4 1 1}",
			agg.TP, agg.FP, agg.FN, agg.SeverityMatches)
	}
	// Precision = 1/(1+4) = 0.2; Recall = 1/(1+1) = 0.5.
	if agg.Precision == nil || math.Abs(*agg.Precision-0.2) > 1e-9 {
		t.Errorf("aggregate precision = %v, want 0.2", agg.Precision)
	}
	if agg.Recall == nil || math.Abs(*agg.Recall-0.5) > 1e-9 {
		t.Errorf("aggregate recall = %v, want 0.5", agg.Recall)
	}
	if agg.SeverityAccuracy == nil || math.Abs(*agg.SeverityAccuracy-1.0) > 1e-9 {
		t.Errorf("aggregate sev-acc = %v, want 1.0", agg.SeverityAccuracy)
	}
}

func TestCaseScoreUndefinedRatiosAreNil(t *testing.T) {
	// A clean case with no predictions has every ratio undefined; toCaseScore
	// must leave the pointers nil so JSON serializes them as null / the table
	// prints "n/a".
	cs := toCaseScore("clean", "", Score{Clean: true})
	if cs.Precision != nil || cs.Recall != nil || cs.SeverityAccuracy != nil {
		t.Errorf("undefined ratios must be nil, got p=%v r=%v s=%v", cs.Precision, cs.Recall, cs.SeverityAccuracy)
	}
}
