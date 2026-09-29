package eval

import (
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
