package eval

import (
	"encoding/json"
	"maps"
	"math"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/planwerk/planwerk-agent/internal/report"
)

// pf builds a predicted finding.
func pf(file string, line int, sev report.Severity, title, problem string) report.Finding {
	return report.Finding{File: file, Line: line, Severity: sev, Title: title, Problem: problem}
}

// pfBy builds a predicted finding carrying the given ConfirmedBy provenance.
func pfBy(file string, line int, sev report.Severity, title, problem string, passes ...string) report.Finding {
	f := pf(file, line, sev, title, problem)
	f.ConfirmedBy = passes
	return f
}

// pfSpan builds a predicted finding over the lines [line, lineEnd].
func pfSpan(file string, line, lineEnd int, sev report.Severity, title, problem string) report.Finding {
	f := pf(file, line, sev, title, problem)
	f.LineEnd = lineEnd
	return f
}

// ef builds an expected finding.
func ef(file string, line int, sev string, keywords ...string) ExpectedFinding {
	return ExpectedFinding{File: file, Line: line, Severity: sev, Keywords: keywords}
}

// efSpan builds an expected finding over the lines [line, lineEnd].
func efSpan(file string, line, lineEnd int, sev string, keywords ...string) ExpectedFinding {
	f := ef(file, line, sev, keywords...)
	f.LineEnd = lineEnd
	return f
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
		wantFoundBy        map[string]int
	}{
		{
			name:        "perfect match",
			c:           caseWith(false, ef("a.go", 10, "CRITICAL", "leak")),
			preds:       []report.Finding{pf("a.go", 10, report.SeverityCritical, "Goroutine leak", "leaks forever")},
			wantTP:      1,
			wantSevHit:  1,
			wantFoundBy: map[string]int{"review": 1},
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
			name:        "severity mismatch still true positive",
			c:           caseWith(false, ef("a.go", 10, "CRITICAL", "leak")),
			preds:       []report.Finding{pf("a.go", 10, report.SeverityWarning, "leak", "leaks")},
			wantTP:      1,
			wantSevHit:  0,
			wantFoundBy: map[string]int{"review": 1},
		},
		{
			name:        "line within tolerance matches",
			c:           caseWith(false, ef("a.go", 10, "WARNING", "leak")),
			preds:       []report.Finding{pf("a.go", 13, report.SeverityWarning, "leak", "leaks")},
			wantTP:      1,
			wantSevHit:  1,
			wantFoundBy: map[string]int{"review": 1},
		},
		{
			name:   "line beyond tolerance misses",
			c:      caseWith(false, ef("a.go", 10, "WARNING", "leak")),
			preds:  []report.Finding{pf("a.go", 14, report.SeverityWarning, "leak", "leaks")},
			wantFN: 1, wantFP: 1,
		},
		{
			// Under a rule reading only the start line, 10 is 10 lines off 20.
			name:        "a predicted range covering the expected line matches",
			c:           caseWith(false, ef("a.go", 20, "WARNING", "leak")),
			preds:       []report.Finding{pfSpan("a.go", 10, 30, report.SeverityWarning, "leak", "leaks")},
			wantTP:      1,
			wantSevHit:  1,
			wantFoundBy: map[string]int{"review": 1},
		},
		{
			name:        "a predicted range ending within tolerance before the expected line matches",
			c:           caseWith(false, ef("a.go", 20, "WARNING", "leak")),
			preds:       []report.Finding{pfSpan("a.go", 10, 17, report.SeverityWarning, "leak", "leaks")},
			wantTP:      1,
			wantSevHit:  1,
			wantFoundBy: map[string]int{"review": 1},
		},
		{
			name:   "a predicted range ending beyond tolerance misses",
			c:      caseWith(false, ef("a.go", 20, "WARNING", "leak")),
			preds:  []report.Finding{pfSpan("a.go", 10, 16, report.SeverityWarning, "leak", "leaks")},
			wantFN: 1, wantFP: 1,
		},
		{
			name:        "an expected range matches a prediction inside it",
			c:           caseWith(false, efSpan("a.go", 20, 30, "WARNING", "leak")),
			preds:       []report.Finding{pf("a.go", 25, report.SeverityWarning, "leak", "leaks")},
			wantTP:      1,
			wantSevHit:  1,
			wantFoundBy: map[string]int{"review": 1},
		},
		{
			name:        "a prediction within tolerance past the expected range's end matches",
			c:           caseWith(false, efSpan("a.go", 20, 30, "WARNING", "leak")),
			preds:       []report.Finding{pf("a.go", 33, report.SeverityWarning, "leak", "leaks")},
			wantTP:      1,
			wantSevHit:  1,
			wantFoundBy: map[string]int{"review": 1},
		},
		{
			name:   "a prediction beyond tolerance past the expected range's end misses",
			c:      caseWith(false, efSpan("a.go", 20, 30, "WARNING", "leak")),
			preds:  []report.Finding{pf("a.go", 34, report.SeverityWarning, "leak", "leaks")},
			wantFN: 1, wantFP: 1,
		},
		{
			name:   "a line_end before line stands for line alone",
			c:      caseWith(false, ef("a.go", 14, "WARNING", "leak")),
			preds:  []report.Finding{pfSpan("a.go", 10, 5, report.SeverityWarning, "leak", "leaks")},
			wantFN: 1, wantFP: 1,
		},
		{
			// Read as written, [10, 5] would end 7 lines before 12.
			name:        "a line_end before line still matches within tolerance of line",
			c:           caseWith(false, ef("a.go", 12, "WARNING", "leak")),
			preds:       []report.Finding{pfSpan("a.go", 10, 5, report.SeverityWarning, "leak", "leaks")},
			wantTP:      1,
			wantSevHit:  1,
			wantFoundBy: map[string]int{"review": 1},
		},
		{
			// Swapped into [5, 10], the range would reach 6.
			name:   "a line_end before line is not swapped into a range",
			c:      caseWith(false, ef("a.go", 6, "WARNING", "leak")),
			preds:  []report.Finding{pfSpan("a.go", 10, 5, report.SeverityWarning, "leak", "leaks")},
			wantFN: 1, wantFP: 1,
		},
		{
			// Line 0 means no location; [0, 40] would cover 30.
			name:   "an unlocated prediction ignores its line_end",
			c:      caseWith(false, ef("a.go", 30, "WARNING", "leak")),
			preds:  []report.Finding{pfSpan("a.go", 0, 40, report.SeverityWarning, "leak", "leaks")},
			wantFN: 1, wantFP: 1,
		},
		{
			name:        "keyword case-insensitive in problem",
			c:           caseWith(false, ef("a.go", 10, "BLOCKING", "Injection")),
			preds:       []report.Finding{pf("a.go", 10, report.SeverityBlocking, "SQL issue", "possible INJECTION via concat")},
			wantTP:      1,
			wantSevHit:  1,
			wantFoundBy: map[string]int{"review": 1},
		},
		{
			name: "one prediction cannot satisfy two expected (one-to-one)",
			c:    caseWith(false, ef("a.go", 10, "WARNING", "leak"), ef("a.go", 11, "WARNING", "leak")),
			preds: []report.Finding{
				pf("a.go", 10, report.SeverityWarning, "leak", "leaks"),
			},
			wantTP: 1, wantFN: 1, wantSevHit: 1,
			// The credit counts every match, claimed or not: the pass saw both.
			wantFoundBy: map[string]int{"review": 2},
		},
		{
			// Claiming the first match in order, the leak line would take the
			// wide range and leave the race line nothing.
			name: "a wide prediction gives way to a narrower one listed after it",
			c:    caseWith(false, ef("a.go", 20, "WARNING", "leak"), ef("a.go", 6, "WARNING", "race")),
			preds: []report.Finding{
				pfSpan("a.go", 5, 40, report.SeverityWarning, "race and leak", "both"),
				pf("a.go", 20, report.SeverityWarning, "leak", "leaks"),
			},
			wantTP: 2, wantSevHit: 2,
			wantFoundBy: map[string]int{"review": 2},
		},
		{
			name: "a wide prediction gives way to a narrower one listed before it",
			c:    caseWith(false, ef("a.go", 20, "WARNING", "leak"), ef("a.go", 6, "WARNING", "race")),
			preds: []report.Finding{
				pf("a.go", 20, report.SeverityWarning, "leak", "leaks"),
				pfSpan("a.go", 5, 40, report.SeverityWarning, "race and leak", "both"),
			},
			wantTP: 2, wantSevHit: 2,
			wantFoundBy: map[string]int{"review": 2},
		},
		{
			name: "a duplicate with the expected severity takes the claim",
			c:    caseWith(false, ef("a.go", 10, "CRITICAL", "leak")),
			preds: []report.Finding{
				pf("a.go", 10, report.SeverityWarning, "leak", "leaks"),
				pf("a.go", 11, report.SeverityCritical, "leak", "leaks"),
			},
			wantTP: 1, wantFP: 1, wantSevHit: 1,
			wantFoundBy: map[string]int{"review": 1},
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
			wantFoundBy: map[string]int{"review": 1},
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
		{
			name:        "a cross-pass match credits every confirming pass",
			c:           caseWith(false, ef("a.go", 10, "BLOCKING", "injection")),
			preds:       []report.Finding{pfBy("a.go", 10, report.SeverityBlocking, "SQL injection", "concat", "review", "specialist:security")},
			wantTP:      1,
			wantSevHit:  1,
			wantFoundBy: map[string]int{"review": 1, "specialist:security": 1},
		},
		{
			name: "an unclaimed duplicate is credited yet stays a false positive",
			c:    caseWith(false, ef("a.go", 10, "WARNING", "untested")),
			preds: []report.Finding{
				pfBy("a.go", 10, report.SeverityWarning, "untested branch", "no test", "review"),
				pfBy("a.go", 11, report.SeverityWarning, "untested branch", "no test", "specialist:testing"),
			},
			wantTP: 1, wantFP: 1, wantSevHit: 1,
			wantFoundBy: map[string]int{"review": 1, "specialist:testing": 1},
		},
		{
			name:        "an empty ConfirmedBy credits the primary review",
			c:           caseWith(false, ef("a.go", 10, "WARNING", "leak")),
			preds:       []report.Finding{pfBy("a.go", 10, report.SeverityWarning, "leak", "leaks", []string{}...)},
			wantTP:      1,
			wantSevHit:  1,
			wantFoundBy: map[string]int{"review": 1},
		},
		{
			name:   "nil findings credit no pass",
			c:      caseWith(false, ef("a.go", 10, "WARNING", "leak")),
			preds:  nil,
			wantFN: 1,
		},
		{
			name: "a clean case credits no pass",
			c:    caseWith(true),
			preds: []report.Finding{
				pfBy("a.go", 1, report.SeverityInfo, "x", "y", "review", "specialist:testing"),
				pfBy("b.go", 2, report.SeverityInfo, "x", "y", "adversarial"),
			},
			wantFP: 2,
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
			if !maps.Equal(s.FoundBy, tt.wantFoundBy) {
				t.Errorf("FoundBy = %v, want %v", s.FoundBy, tt.wantFoundBy)
			}
		})
	}
}

func TestScoreAddFoundBy(t *testing.T) {
	var s Score
	s.Add(Score{TP: 1})
	if s.FoundBy != nil {
		t.Fatalf("FoundBy = %v after adding a score without credit, want nil", s.FoundBy)
	}
	s.Add(Score{TP: 1, FoundBy: map[string]int{"review": 1, "specialist:testing": 1}})
	s.Add(Score{TP: 1, FoundBy: map[string]int{"review": 1}})
	want := map[string]int{"review": 2, "specialist:testing": 1}
	if !maps.Equal(s.FoundBy, want) {
		t.Errorf("FoundBy = %v, want %v", s.FoundBy, want)
	}
	if s.TP != 3 {
		t.Errorf("TP = %d, want 3", s.TP)
	}
}

func TestScoreRatios(t *testing.T) {
	t.Run("perfect", func(t *testing.T) {
		s := Score{TP: 1, SeverityMatches: 1}
		p, ok := s.Precision()
		wantDefined(t, "precision", p, ok, true, 1.0)
		r, ok := s.Recall()
		wantDefined(t, "recall", r, ok, true, 1.0)
		f, ok := s.F1()
		wantDefined(t, "f1", f, ok, true, 1.0)
		a, ok := s.SeverityAccuracy()
		wantDefined(t, "sev-acc", a, ok, true, 1.0)
	})

	t.Run("partial fractions", func(t *testing.T) {
		s := Score{TP: 1, FP: 2, FN: 1, SeverityMatches: 0}
		p, ok := s.Precision()
		wantDefined(t, "precision", p, ok, true, 1.0/3.0)
		r, ok := s.Recall()
		wantDefined(t, "recall", r, ok, true, 0.5)
		// 2/(2+2+1), the harmonic mean of 1/3 and 1/2.
		f, ok := s.F1()
		wantDefined(t, "f1", f, ok, true, 0.4)
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
		if _, ok := s.F1(); ok {
			t.Error("f1 must be undefined for a clean case")
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
		if _, ok := s.F1(); ok {
			t.Error("f1 must be undefined with no expected findings")
		}
	})

	t.Run("nothing predicted on a seeded case: precision undefined, F1 zero", func(t *testing.T) {
		s := Score{FN: 1}
		if _, ok := s.Precision(); ok {
			t.Error("precision must be undefined with no predictions")
		}
		f, ok := s.F1()
		wantDefined(t, "f1", f, ok, true, 0.0)
	})
}

func TestBuildReportAggregate(t *testing.T) {
	scored := []Scored{
		{Case: caseWith(false, ef("a.go", 10, "WARNING", "leak")), Score: Score{TP: 1, SeverityMatches: 1}},
		{Case: caseWith(false, ef("b.go", 20, "CRITICAL", "nil")), Score: Score{FN: 1, FP: 3}},
		{Case: caseWith(true), Score: Score{Clean: true, FP: 1}},
	}

	rep := BuildReport(scored, Config{Runs: 1})
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
	// F1 = 2/(2+4+1) = 2/7: the clean case's false positive counts too.
	if agg.F1 == nil || math.Abs(*agg.F1-2.0/7) > 1e-9 {
		t.Errorf("aggregate f1 = %v, want 2/7", agg.F1)
	}
	if rep.Cases[2].F1 != nil {
		t.Errorf("clean case f1 = %v, want nil", *rep.Cases[2].F1)
	}
	if rep.Match != MatchRule {
		t.Errorf("Match = %q, want %q", rep.Match, MatchRule)
	}
}

func TestCaseScoreUndefinedRatiosAreNil(t *testing.T) {
	// A clean case with no predictions has every ratio undefined; toCaseScore
	// must leave the pointers nil so JSON serializes them as null / the table
	// prints "n/a".
	cs := toCaseScore("clean", "", Score{Clean: true})
	if cs.Precision != nil || cs.Recall != nil || cs.F1 != nil || cs.SeverityAccuracy != nil {
		t.Errorf("undefined ratios must be nil, got p=%v r=%v f1=%v s=%v", cs.Precision, cs.Recall, cs.F1, cs.SeverityAccuracy)
	}
}

func TestScoreRuns(t *testing.T) {
	c := caseWith(false, ef("a.go", 10, "WARNING", "leak"))
	hit := report.ReviewResult{Findings: []report.Finding{pf("a.go", 10, report.SeverityWarning, "leak", "leaks")}}
	usage := func(calls int, input int64) report.Usage {
		return report.Usage{
			Calls: calls, InputTokens: input,
			Passes: []report.PassUsage{{Pass: "review", Calls: calls, InputTokens: input}},
		}
	}

	t.Run("pools the tallies and sums the usage", func(t *testing.T) {
		sc := ScoreRuns(c, []Run{
			{Result: hit, Usage: usage(1, 100)},
			{Result: report.ReviewResult{}, Usage: usage(2, 200)},
			{Result: hit, Usage: usage(3, 300)},
		})
		if sc.Score.TP != 2 || sc.Score.FN != 1 || sc.Score.FP != 0 {
			t.Errorf("Score = {TP:%d FP:%d FN:%d}, want {2 0 1}", sc.Score.TP, sc.Score.FP, sc.Score.FN)
		}
		if !maps.Equal(sc.Score.FoundBy, map[string]int{"review": 2}) {
			t.Errorf("FoundBy = %v, want map[review:2]", sc.Score.FoundBy)
		}
		want := usage(6, 600)
		if !reflect.DeepEqual(sc.Usage, want) {
			t.Errorf("Usage = %+v, want %+v", sc.Usage, want)
		}
	})

	t.Run("no runs yield zero tallies and zero usage", func(t *testing.T) {
		for _, c := range []Case{c, caseWith(true)} {
			sc := ScoreRuns(c, nil)
			if want := (Score{Clean: c.Expected.Clean}); !reflect.DeepEqual(sc.Score, want) {
				t.Errorf("Score = %+v, want %+v", sc.Score, want)
			}
			if !reflect.DeepEqual(sc.Usage, report.Usage{}) {
				t.Errorf("Usage = %+v, want zero", sc.Usage)
			}
		}
	})
}

func TestBuildReportPasses(t *testing.T) {
	t.Run("recall per pass over the aggregate TP+FN, config copied", func(t *testing.T) {
		scored := []Scored{
			{Case: caseWith(false, ef("a.go", 1, "WARNING", "x")), Score: Score{TP: 5, FN: 1, FoundBy: map[string]int{"review": 5, "specialist:testing": 2}}},
			{Case: caseWith(false, ef("b.go", 1, "WARNING", "x")), Score: Score{TP: 3, FN: 1, FoundBy: map[string]int{"review": 3}}},
		}
		rep := BuildReport(scored, Config{Runs: 3, RunOptions: RunOptions{Thorough: true, Specialists: true}})
		if rep.Runs != 3 || !rep.Thorough || !rep.Specialists {
			t.Errorf("config = {Runs:%d Thorough:%v Specialists:%v}, want {3 true true}", rep.Runs, rep.Thorough, rep.Specialists)
		}
		if len(rep.Passes) != 2 {
			t.Fatalf("Passes = %+v, want 2 entries", rep.Passes)
		}
		for i, want := range []struct {
			pass   string
			found  int
			recall float64
		}{{"review", 8, 0.8}, {"specialist:testing", 2, 0.2}} {
			got := rep.Passes[i]
			if got.Pass != want.pass || got.Found != want.found || got.Recall == nil || math.Abs(*got.Recall-want.recall) > 1e-9 {
				t.Errorf("Passes[%d] = {%s %d %v}, want {%s %d %v}", i, got.Pass, got.Found, got.Recall, want.pass, want.found, want.recall)
			}
		}
		if !maps.Equal(rep.Cases[0].FoundBy, scored[0].Score.FoundBy) {
			t.Errorf("case FoundBy = %v, want %v", rep.Cases[0].FoundBy, scored[0].Score.FoundBy)
		}
	})

	t.Run("equal found ties break by name", func(t *testing.T) {
		scored := []Scored{{Case: caseWith(false, ef("a.go", 1, "WARNING", "x")), Score: Score{TP: 2, FoundBy: map[string]int{"specialist:testing": 1, "adversarial": 1}}}}
		rep := BuildReport(scored, Config{Runs: 1})
		if len(rep.Passes) != 2 || rep.Passes[0].Pass != "adversarial" || rep.Passes[1].Pass != "specialist:testing" {
			t.Errorf("Passes = %+v, want adversarial before specialist:testing", rep.Passes)
		}
	})
}

func TestBuildReportCleanOnly(t *testing.T) {
	rep := BuildReport([]Scored{{Case: caseWith(true), Score: Score{Clean: true, FP: 2}}}, Config{Runs: 1})
	if rep.Passes == nil || len(rep.Passes) != 0 {
		t.Errorf("Passes = %#v, want empty and non-nil", rep.Passes)
	}
	if rep.Aggregate.Recall != nil {
		t.Errorf("aggregate recall = %v, want nil", *rep.Aggregate.Recall)
	}
}

func TestBuildReportUsage(t *testing.T) {
	ua := report.Usage{Calls: 2, InputTokens: 10, CostUSD: 0.2, Passes: []report.PassUsage{{Pass: "review", Calls: 2, InputTokens: 10, CostUSD: 0.2}}}
	ub := report.Usage{Calls: 1, OutputTokens: 4, CostUSD: 0.5, Passes: []report.PassUsage{{Pass: "specialist-security", Calls: 1, OutputTokens: 4, CostUSD: 0.5}}}
	scored := []Scored{
		{Case: caseWith(false, ef("a.go", 1, "WARNING", "x")), Score: Score{TP: 1}, Usage: ua},
		{Case: caseWith(true), Score: Score{Clean: true}, Usage: ub},
	}
	rep := BuildReport(scored, Config{Runs: 1})
	if !reflect.DeepEqual(rep.Cases[0].Usage, ua) || !reflect.DeepEqual(rep.Cases[1].Usage, ub) {
		t.Errorf("case usage = %+v / %+v, want %+v / %+v", rep.Cases[0].Usage, rep.Cases[1].Usage, ua, ub)
	}
	if want := sumUsage(ua, ub); !reflect.DeepEqual(rep.Aggregate.Usage, want) {
		t.Errorf("aggregate usage = %+v, want %+v", rep.Aggregate.Usage, want)
	}
}

// sampleTiers is a fully recorded Tiers whose finder effort differs from the
// main tier's, and sampleTiersLine its rendering.
func sampleTiers() Tiers {
	return Tiers{ClaudeModel: "opus", ClaudeEffort: "xhigh", FinderModel: "opus", FinderEffort: "high", StructureModel: "sonnet", StructureEffort: "xhigh"}
}

const sampleTiersLine = "claude opus/xhigh, finder opus/high, structure sonnet/xhigh"

func TestTiersString(t *testing.T) {
	tests := []struct {
		name  string
		tiers Tiers
		want  string
	}{
		{"the zero value is unrecorded", Tiers{}, "(unrecorded)"},
		{"a full value names every tier", sampleTiers(), sampleTiersLine},
		{"a finder tool list follows the tiers", func() Tiers { tr := sampleTiers(); tr.FinderTools = "Grep,Read"; return tr }(), sampleTiersLine + ", finder tools Grep,Read"},
		{"the plugins come last", func() Tiers { tr := sampleTiers(); tr.FinderTools = "Read"; tr.Plugins = "impeccable@4.7.1"; return tr }(), sampleTiersLine + ", finder tools Read, plugins impeccable@4.7.1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.tiers.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRenderTableSections(t *testing.T) {
	scored := []Scored{{
		Case:  Case{Name: "priced", Expected: Expected{Findings: []ExpectedFinding{ef("a.go", 1, "WARNING", "x")}}},
		Score: Score{TP: 3, SeverityMatches: 3, FoundBy: map[string]int{"review": 3}},
		Usage: report.Usage{
			Calls: 6, InputTokens: 1000, CacheReadTokens: 1500, CacheCreationTokens: 500, OutputTokens: 300, CostUSD: 0.3,
			Passes: []report.PassUsage{{Pass: "review", Calls: 6, InputTokens: 1000, CacheReadTokens: 1500, CacheCreationTokens: 500, OutputTokens: 300, CostUSD: 0.3}},
		},
	}}
	var buf strings.Builder
	RenderTable(&buf, BuildReport(scored, Config{Runs: 3, RunOptions: RunOptions{Thorough: true}, Tiers: sampleTiers()}))
	out := buf.String()

	const runsLine = "RUNS: 3 per case (thorough: yes, specialists: no)"
	for _, want := range []string{"CASE ", runsLine, "TIERS: " + sampleTiersLine, "RECALL BY PASS", "COST PER RUN"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if _, after, ok := strings.Cut(out, runsLine+"\n"); !ok || !strings.HasPrefix(after, "TIERS: "+sampleTiersLine+"\n") {
		t.Errorf("the line after %q is not the TIERS line:\n%s", runsLine, out)
	}

	var scoped strings.Builder
	RenderTable(&scoped, BuildReport(scored, Config{Runs: 3, RunOptions: RunOptions{Thorough: true, ScopePatterns: true}, Tiers: sampleTiers()}))
	if want := "RUNS: 3 per case (thorough: yes, specialists: no, scope-patterns: yes)\n"; !strings.Contains(scoped.String(), want) {
		t.Errorf("a -scope-patterns run lacks %q:\n%s", want, scoped.String())
	}

	// The F1 column sits between RECALL and SEV-ACC, and the rule under the
	// case rows is as wide as the header.
	head := lineStarting(t, out, "CASE ")
	if !strings.Contains(head, "  F1  ") {
		t.Errorf("header = %q, want an F1 column", head)
	}
	if f := strings.Fields(lineStarting(t, out, "priced ")); !slices.Equal(f, []string{"priced", "no", "3", "0", "0", "100%", "100%", "100%", "100%"}) {
		t.Errorf("case row = %v, want [priced no 3 0 0 100%% 100%% 100%% 100%%]\n%s", f, out)
	}
	if rule := lineStarting(t, out, "---"); len(rule) != len(head) {
		t.Errorf("rule is %d wide, header %d:\n%s", len(rule), len(head), out)
	}
	if !strings.Contains(out, "F1 = 2TP/(2TP+FP+FN)") {
		t.Errorf("footer lacks the F1 formula:\n%s", out)
	}

	_, cost, ok := strings.Cut(out, "COST PER RUN")
	if !ok {
		t.Fatalf("no COST PER RUN section:\n%s", out)
	}
	var row []string
	for _, line := range strings.Split(cost, "\n") {
		if f := strings.Fields(line); len(f) > 0 && f[0] == "priced" {
			row = f
		}
	}
	// 3000 prompt tokens over 3 runs: 1000 per run; calls 2.0, output 100, $0.10.
	if want := []string{"priced", "2.0", "1000", "100", "$0.10"}; !slices.Equal(row, want) {
		t.Errorf("cost row = %v, want %v\n%s", row, want, out)
	}
}

func TestRenderTableUnrecordedTiers(t *testing.T) {
	var buf strings.Builder
	RenderTable(&buf, BuildReport([]Scored{{Case: caseWith(true), Score: Score{Clean: true}}}, Config{Runs: 1}))
	if !strings.Contains(buf.String(), "\nTIERS: (unrecorded)\n") {
		t.Errorf("a report with no tiers lacks TIERS: (unrecorded):\n%s", buf.String())
	}
}

func TestRenderTableNoPasses(t *testing.T) {
	var buf strings.Builder
	RenderTable(&buf, BuildReport([]Scored{{Case: caseWith(true), Score: Score{Clean: true}}}, Config{Runs: 1}))
	_, recall, _ := strings.Cut(buf.String(), "RECALL BY PASS\n")
	if !strings.HasPrefix(recall, "(none)\n") {
		t.Errorf("RECALL BY PASS with no credit = %q, want (none)", recall)
	}
}

// aacrCase builds an AACR-Bench case expecting the given reference comments.
func aacrCase(findings ...ExpectedFinding) Case {
	src := &AACRSource{URL: "https://github.com/acme/widget/pull/7", Owner: "acme", Repo: "widget", Number: 7}
	return Case{Name: "widget-7", Expected: Expected{Description: src.URL, Findings: findings}, AACR: src}
}

// refComment builds an AACR-Bench reference comment over [from, to], with no
// severity and no keywords, as LoadAACR builds one.
func refComment(file string, from, to int) ExpectedFinding {
	return ExpectedFinding{File: file, Line: from, LineEnd: to, Comment: "the map is written after Unlock"}
}

func TestScoreRunAACR(t *testing.T) {
	// No prediction below shares a word with the reference comment.
	unrelated := func(file string, line, lineEnd int) report.Finding {
		return pfSpan(file, line, lineEnd, report.SeverityWarning, "Shadowed variable", "x is declared twice")
	}
	tests := []struct {
		name                   string
		c                      Case
		preds                  []report.Finding
		verdicts               map[PairKey]bool
		wantTP, wantFP, wantFN int
	}{
		{name: "overlapping ranges match without a keyword", c: aacrCase(refComment("store.go", 40, 44)),
			preds: []report.Finding{unrelated("store.go", 44, 50)}, wantTP: 1},
		{name: "a prediction inside the range matches", c: aacrCase(refComment("store.go", 40, 44)),
			preds: []report.Finding{unrelated("store.go", 42, 0)}, wantTP: 1},
		{name: "an adjacent range does not overlap", c: aacrCase(refComment("store.go", 40, 44)),
			preds: []report.Finding{unrelated("store.go", 45, 50)}, wantFP: 1, wantFN: 1},
		{name: "a line before the range does not match, unlike the corpus window", c: aacrCase(refComment("store.go", 40, 44)),
			preds: []report.Finding{unrelated("store.go", 39, 0)}, wantFP: 1, wantFN: 1},
		{name: "another file does not match", c: aacrCase(refComment("store.go", 40, 44)),
			preds: []report.Finding{unrelated("cache.go", 42, 0)}, wantFP: 1, wantFN: 1},
		{name: "an unlocated prediction does not match", c: aacrCase(refComment("store.go", 1, 44)),
			preds: []report.Finding{unrelated("store.go", 0, 44)}, wantFP: 1, wantFN: 1},
		{name: "line_end widens the prediction onto the range", c: aacrCase(refComment("store.go", 40, 44)),
			preds: []report.Finding{unrelated("store.go", 30, 40)}, wantTP: 1},
		{name: "nil verdicts match on lines alone", c: aacrCase(refComment("store.go", 40, 44), refComment("store.go", 60, 61)),
			preds: []report.Finding{unrelated("store.go", 41, 0), unrelated("store.go", 60, 0)}, wantTP: 2},
		{name: "a pair judged another concern does not match", c: aacrCase(refComment("store.go", 40, 44)),
			preds: []report.Finding{unrelated("store.go", 41, 0)}, verdicts: map[PairKey]bool{{Pred: 0, Exp: 0}: false}, wantFP: 1, wantFN: 1},
		{name: "a pair judged the same concern matches", c: aacrCase(refComment("store.go", 40, 44)),
			preds: []report.Finding{unrelated("store.go", 41, 0)}, verdicts: map[PairKey]bool{{Pred: 0, Exp: 0}: true}, wantTP: 1},
		{name: "the judged-same prediction takes the claim", c: aacrCase(refComment("store.go", 40, 44)),
			preds:    []report.Finding{unrelated("store.go", 41, 0), unrelated("store.go", 43, 0)},
			verdicts: map[PairKey]bool{{Pred: 0, Exp: 0}: false, {Pred: 1, Exp: 0}: true}, wantTP: 1, wantFP: 1},
		{name: "an empty verdict map matches nothing", c: aacrCase(refComment("store.go", 40, 44)),
			preds: []report.Finding{unrelated("store.go", 41, 0)}, verdicts: map[PairKey]bool{}, wantFP: 1, wantFN: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := ScoreRun(tt.c, Run{Result: report.ReviewResult{Findings: tt.preds}, Verdicts: tt.verdicts})
			if s.TP != tt.wantTP || s.FP != tt.wantFP || s.FN != tt.wantFN {
				t.Errorf("Score = {TP:%d FP:%d FN:%d}, want {TP:%d FP:%d FN:%d}", s.TP, s.FP, s.FN, tt.wantTP, tt.wantFP, tt.wantFN)
			}
			if s.SeverityMatches != 0 || s.SeverityUnscored != s.TP {
				t.Errorf("SeverityMatches/SeverityUnscored = %d/%d, want 0/%d: a reference comment names no severity", s.SeverityMatches, s.SeverityUnscored, s.TP)
			}
		})
	}

	t.Run("a corpus case is scored by ScoreCase", func(t *testing.T) {
		c := caseWith(false, ef("a.go", 10, "WARNING", "leak"))
		r := Run{Result: report.ReviewResult{Findings: []report.Finding{pf("a.go", 12, report.SeverityWarning, "leak", "leaks")}},
			Verdicts: map[PairKey]bool{}}
		if got, want := ScoreRun(c, r), ScoreCase(c, r.Result); !reflect.DeepEqual(got, want) {
			t.Errorf("ScoreRun = %+v, want ScoreCase's %+v", got, want)
		}
	})

	t.Run("ScoreRuns scores each run by its own verdicts", func(t *testing.T) {
		c := aacrCase(refComment("store.go", 40, 44))
		hit := report.ReviewResult{Findings: []report.Finding{unrelated("store.go", 41, 0)}}
		sc := ScoreRuns(c, []Run{{Result: hit}, {Result: hit, Verdicts: map[PairKey]bool{{Pred: 0, Exp: 0}: false}}})
		if sc.Score.TP != 1 || sc.Score.FP != 1 || sc.Score.FN != 1 {
			t.Errorf("Score = {TP:%d FP:%d FN:%d}, want {1 1 1}", sc.Score.TP, sc.Score.FP, sc.Score.FN)
		}
	})
}

func TestScoreRunAACRSeverityUndefined(t *testing.T) {
	c := aacrCase(refComment("store.go", 40, 44))
	s := ScoreRun(c, Run{Result: report.ReviewResult{Findings: []report.Finding{pf("store.go", 42, report.SeverityCritical, "x", "y")}}})
	if s.TP != 1 {
		t.Fatalf("TP = %d, want 1", s.TP)
	}
	if _, ok := s.SeverityAccuracy(); ok {
		t.Error("severity accuracy is defined, want it undefined: no reference comment names a severity")
	}
	if cs := toCaseScore("widget-7", "", s); cs.SeverityAccuracy != nil {
		t.Errorf("severity_accuracy = %v, want null", *cs.SeverityAccuracy)
	}

	// Mixed with a corpus tally, only the TP that name a severity count.
	var agg Score
	agg.Add(s)
	agg.Add(Score{TP: 2, SeverityMatches: 1})
	a, ok := agg.SeverityAccuracy()
	wantDefined(t, "pooled sev-acc", a, ok, true, 0.5)
}

func TestBuildReportAACR(t *testing.T) {
	withSource := func(c Case, lang, ctx string, left int) Case {
		src := *c.AACR
		src.Language, src.Context, src.LeftExcluded = lang, ctx, left
		c.AACR = &src
		return c
	}
	scored := []Scored{
		{Case: withSource(aacrCase(refComment("store.go", 40, 44)), "Go", "Diff Level", 1), Score: Score{TP: 1, SeverityUnscored: 1}},
		{Case: withSource(aacrCase(refComment("cache.go", 8, 9)), "Python", "Repo Level", 2), Score: Score{FN: 1, FP: 1}},
	}
	aacr := &AACRConfig{Export: "dataset.json", SHA256: strings.Repeat("ab", 32), Sample: 2, Candidates: 196}

	rep := BuildReport(scored, Config{Runs: 1, AACR: aacr})
	if rep.Match != MatchLine {
		t.Errorf("Match = %q, want %q", rep.Match, MatchLine)
	}
	if c := rep.Cases[1]; c.URL != "https://github.com/acme/widget/pull/7" || c.Language != "Python" || c.Context != "Repo Level" || c.LeftExcluded != 2 {
		t.Errorf("case = %+v, want its URL, Python, Repo Level and 2 left-side comments", c)
	}
	if rep.Aggregate.LeftExcluded != 3 {
		t.Errorf("aggregate left_excluded = %d, want 3", rep.Aggregate.LeftExcluded)
	}
	if rep.Aggregate.SeverityAccuracy != nil {
		t.Errorf("aggregate severity_accuracy = %v, want null", *rep.Aggregate.SeverityAccuracy)
	}

	judged := *aacr
	judged.Judge = true
	rep = BuildReport(scored, Config{Runs: 1, AACR: &judged})
	if rep.Match != MatchLineJudge {
		t.Errorf("judged Match = %q, want %q", rep.Match, MatchLineJudge)
	}
	rep.Judge = &JudgeSummary{Pairs: 4, Same: 3, Usage: report.Usage{Calls: 4, OutputTokens: 80}}

	var buf strings.Builder
	if err := RenderJSON(&buf, rep); err != nil {
		t.Fatalf("RenderJSON: %v", err)
	}
	var got struct {
		Match string                     `json:"match"`
		AACR  map[string]json.RawMessage `json:"aacr"`
		Judge struct {
			Pairs int          `json:"pairs"`
			Same  int          `json:"same"`
			Usage report.Usage `json:"usage"`
		} `json:"judge"`
		Cases []map[string]json.RawMessage `json:"cases"`
	}
	if err := json.Unmarshal([]byte(buf.String()), &got); err != nil {
		t.Fatalf("decoding the report: %v", err)
	}
	if got.Match != MatchLineJudge {
		t.Errorf("match = %q, want %q", got.Match, MatchLineJudge)
	}
	keys := slices.Sorted(maps.Keys(got.AACR))
	if want := []string{"candidates", "export", "sample", "sha256"}; !slices.Equal(keys, want) {
		t.Errorf("aacr keys = %v, want %v: the rule lives in the top-level match", keys, want)
	}
	if got.Judge.Pairs != 4 || got.Judge.Same != 3 || got.Judge.Usage.Calls != 4 {
		t.Errorf("judge = %+v, want 4 pairs, 3 same, 4 calls", got.Judge)
	}
	for _, key := range []string{"url", "language", "context", "left_excluded"} {
		if _, ok := got.Cases[0][key]; !ok {
			t.Errorf("case lacks %q:\n%s", key, buf.String())
		}
	}

	t.Run("a corpus report carries none of the AACR-Bench keys", func(t *testing.T) {
		var buf strings.Builder
		rep := BuildReport([]Scored{{Case: caseWith(false, ef("a.go", 1, "WARNING", "x")), Score: Score{TP: 1, SeverityMatches: 1}}}, Config{Runs: 1})
		if err := RenderJSON(&buf, rep); err != nil {
			t.Fatalf("RenderJSON: %v", err)
		}
		for _, key := range []string{`"aacr"`, `"judge"`, `"url"`, `"language"`, `"context"`, `"left_excluded"`} {
			if strings.Contains(buf.String(), key) {
				t.Errorf("corpus report holds %s:\n%s", key, buf.String())
			}
		}
		if rep.Match != MatchRule {
			t.Errorf("Match = %q, want %q", rep.Match, MatchRule)
		}
	})
}

func TestRenderTableAACR(t *testing.T) {
	scored := []Scored{{
		Case:  aacrCase(refComment("store.go", 40, 44)),
		Score: Score{TP: 1, FP: 2, SeverityUnscored: 1},
	}}
	scored[0].Case.AACR.LeftExcluded = 2
	cfg := Config{Runs: 1, Tiers: sampleTiers(), AACR: &AACRConfig{Export: "dataset.json", SHA256: "0804505f0a474765ce2840c832cfeaa6", Sample: 1, Candidates: 196, Judge: true}}
	rep := BuildReport(scored, cfg)
	rep.Judge = &JudgeSummary{Pairs: 3, Same: 1, Usage: report.Usage{Calls: 3, InputTokens: 900, CacheReadTokens: 100, OutputTokens: 60, CostUSD: 0.04}}

	var buf strings.Builder
	RenderTable(&buf, rep)
	out := buf.String()

	const runsLine = "RUNS: 1 per case (thorough: no, specialists: no)\n"
	const benchLines = "AACR-BENCH: 1 of 196 pull requests from dataset.json (sha256 0804505f0a47), match: line+judge (range overlap and a same-concern judgement)\n" +
		"LEFT SIDE: 2 verified comments on deleted lines not scored\n" +
		"TIERS: " + sampleTiersLine + "\n"
	if !strings.Contains(out, runsLine+benchLines) {
		t.Errorf("output lacks the AACR-BENCH and LEFT SIDE lines between RUNS and TIERS:\n%s", out)
	}
	if !strings.Contains(out, "AACR-Bench ground truth is incomplete: a finding no annotator wrote counts as FP, so precision is a lower bound.\n") {
		t.Errorf("output lacks the lower-bound note:\n%s", out)
	}
	if strings.Contains(out, "A clean case seeds no bug") {
		t.Errorf("output keeps the clean-case note:\n%s", out)
	}
	if f := strings.Fields(lineStarting(t, out, "widget-7 ")); !slices.Equal(f, []string{"widget-7", "no", "1", "2", "0", "33%", "100%", "50%", "n/a"}) {
		t.Errorf("case row = %v, want severity accuracy n/a", f)
	}
	if _, after, _ := strings.Cut(out, "COST PER RUN"); !strings.HasSuffix(after, "\nJUDGE: 3 pairs, 1 same concern; 3 calls, 1000 prompt tokens, 60 output tokens, $0.04 (total)\n") {
		t.Errorf("output does not end with the JUDGE line after COST PER RUN:\n%s", out)
	}

	t.Run("an unjudged run names the line rule and prints no JUDGE line", func(t *testing.T) {
		cfg := cfg
		aacr := *cfg.AACR
		aacr.Judge = false
		cfg.AACR = &aacr
		var buf strings.Builder
		RenderTable(&buf, BuildReport(scored, cfg))
		if !strings.Contains(buf.String(), "), match: line (range overlap)\n") || strings.Contains(buf.String(), "JUDGE:") {
			t.Errorf("output =\n%s\nwant match: line (range overlap) and no JUDGE line", buf.String())
		}
	})
}
