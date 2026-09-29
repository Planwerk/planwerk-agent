package eval

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/planwerk/planwerk-agent/internal/report"
)

// ratio returns a pointer to v, for the optional ratios of a report.
func ratio(v float64) *float64 { return &v }

// comparableReport builds a report the rule can judge: three runs over case
// "a" with the given aggregate ratios.
func comparableReport(recall, precision, sevAcc *float64) Report {
	return Report{
		Config:    Config{Runs: 3},
		Cases:     []CaseScore{{Name: "a"}},
		Aggregate: CaseScore{Name: "AGGREGATE", Recall: recall, Precision: precision, SeverityAccuracy: sevAcc},
	}
}

func TestLoadReport(t *testing.T) {
	t.Run("a report written by RenderJSON loads back equal", func(t *testing.T) {
		scored := []Scored{
			{
				Case:  Case{Name: "sql-injection", Expected: Expected{Description: "seeded", Findings: []ExpectedFinding{ef("db.go", 11, "BLOCKING", "injection")}}},
				Score: Score{TP: 3, FP: 1, SeverityMatches: 2, FoundBy: map[string]int{"review": 3, "specialist:security": 2}},
				Usage: report.Usage{
					Calls: 9, InputTokens: 300, OutputTokens: 90, CacheReadTokens: 3000, CacheCreationTokens: 30, CostUSD: 1.25,
					Passes: []report.PassUsage{{Pass: "specialist-security", Calls: 3, InputTokens: 100, CostUSD: 0.5}},
				},
			},
			{Case: caseWith(true), Score: Score{Clean: true, FP: 2}},
		}
		want := BuildReport(scored, Config{Runs: 3, RunOptions: RunOptions{Specialists: true}, Tiers: sampleTiers()})
		cmp, err := Compare(want, want)
		if err != nil {
			t.Fatalf("Compare: %v", err)
		}
		want.Comparison = &cmp

		path := filepath.Join(t.TempDir(), "baseline.json")
		f, err := os.Create(path)
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		if err := RenderJSON(f, want); err != nil {
			t.Fatalf("RenderJSON: %v", err)
		}
		if err := f.Close(); err != nil {
			t.Fatalf("close: %v", err)
		}

		got, err := LoadReport(path)
		if err != nil {
			t.Fatalf("LoadReport: %v", err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("LoadReport =\n%+v\nwant\n%+v", got, want)
		}
	})

	t.Run("a baseline kept as bytes loads every field the rule reads", func(t *testing.T) {
		// testdata/baseline.json is a -json report checked in as written, so a
		// renamed JSON key fails here instead of loading as a nil ratio that
		// holds with no floor.
		got, err := LoadReport(filepath.Join("testdata", "baseline.json"))
		if err != nil {
			t.Fatalf("LoadReport: %v", err)
		}
		if want := (Config{Runs: 4, RunOptions: RunOptions{Specialists: true}}); got.Config != want {
			t.Errorf("Config = %+v, want %+v", got.Config, want)
		}
		if names := caseNames(got.Cases); !slices.Equal(names, []string{"sql-injection", "clean-refactor"}) {
			t.Errorf("cases = %v, want [sql-injection clean-refactor]", names)
		}
		agg := got.Aggregate
		for _, r := range []struct {
			metric string
			got    *float64
			want   float64
		}{
			{"recall", agg.Recall, 0.75},
			{"precision", agg.Precision, 0.6},
			{"severity_accuracy", agg.SeverityAccuracy, 2.0 / 3},
		} {
			if r.got == nil || math.Abs(*r.got-r.want) > 1e-9 {
				t.Errorf("aggregate %s = %v, want %v", r.metric, r.got, r.want)
			}
		}
		if want := (RunCost{PromptTokens: 1110, OutputTokens: 30, CostUSD: 0.375}); runCost(got) != want {
			t.Errorf("runCost = %+v, want %+v", runCost(got), want)
		}
		wantPasses := []PassRecall{{Pass: "review", Found: 3, Recall: ratio(0.75)}, {Pass: "specialist:security", Found: 1, Recall: ratio(0.25)}}
		if !reflect.DeepEqual(got.Passes, wantPasses) {
			t.Errorf("Passes = %+v, want %+v", got.Passes, wantPasses)
		}
	})

	t.Run("a baseline without tiers loads with none recorded", func(t *testing.T) {
		for name, content := range map[string]string{
			"no tiers key": `{"runs": 3, "cases": [], "aggregate": {}}`,
			"null tiers":   `{"runs": 3, "tiers": null, "cases": [], "aggregate": {}}`,
		} {
			path := filepath.Join(t.TempDir(), "baseline.json")
			writeFile(t, path, content)
			got, err := LoadReport(path)
			if err != nil {
				t.Fatalf("%s: LoadReport: %v", name, err)
			}
			if got.Tiers != (Tiers{}) {
				t.Errorf("%s: Tiers = %+v, want none recorded", name, got.Tiers)
			}
		}
	})

	t.Run("a missing path is a reading error", func(t *testing.T) {
		_, err := LoadReport(filepath.Join(t.TempDir(), "missing.json"))
		if err == nil || !strings.HasPrefix(err.Error(), "reading baseline") {
			t.Fatalf("err = %v, want a reading baseline error", err)
		}
		if !errors.Is(err, os.ErrNotExist) {
			t.Errorf("err = %v, want it to wrap os.ErrNotExist", err)
		}
	})

	for _, tt := range []struct {
		name, content string
		want          []string
	}{
		{"an empty file is a parsing error", "", []string{"parsing baseline", "unexpected end of JSON input"}},
		{"a report without runs is rejected", "{}", []string{"records no runs", "regenerate it with this harness"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "baseline.json")
			writeFile(t, path, tt.content)
			_, err := LoadReport(path)
			if err == nil {
				t.Fatal("LoadReport = nil error, want error")
			}
			for _, want := range tt.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("err = %q, want it to contain %q", err, want)
				}
			}
		})
	}
}

func TestComparable(t *testing.T) {
	base := Report{
		Config:    Config{Runs: 3, RunOptions: RunOptions{Thorough: true}},
		Cases:     []CaseScore{{Name: "b"}, {Name: "a"}},
		Aggregate: CaseScore{Recall: ratio(1)},
	}
	cfg := Config{Runs: 3, RunOptions: RunOptions{Thorough: true}}

	tests := []struct {
		name     string
		baseline Report
		cfg      Config
		cases    []string
		want     []string // substrings of the error; nil means comparable
	}{
		{"same set in another order is comparable", base, cfg, []string{"b", "a"}, nil},
		{"one baseline run is too few", Report{Config: Config{Runs: 1, RunOptions: RunOptions{Thorough: true}}, Cases: base.Cases}, cfg, []string{"a", "b"},
			[]string{"at least 3 runs per side", "(baseline 1, this run 3)", "re-run with -runs 3"}},
		{"one candidate run is too few", base, Config{Runs: 1, RunOptions: RunOptions{Thorough: true}}, []string{"a", "b"},
			[]string{"at least 3 runs per side", "(baseline 3, this run 1)"}},
		{"thorough mismatch", base, Config{Runs: 3}, []string{"a", "b"},
			[]string{"baseline ran with thorough=true specialists=false", "this run with thorough=false specialists=false", "compare like with like"}},
		{"specialists mismatch", base, Config{Runs: 3, RunOptions: RunOptions{Thorough: true, Specialists: true}}, []string{"a", "b"},
			[]string{"baseline ran with thorough=true specialists=false", "this run with thorough=true specialists=true", "compare like with like"}},
		{"different case sets", base, cfg, []string{"c", "a"},
			[]string{"baseline covers cases [a b], this run covers [a c]", "compare like with like"}},
		{"only clean cases leave no check that could fail", Report{Config: base.Config, Cases: []CaseScore{{Name: "a", Clean: true}}}, cfg, []string{"a"},
			[]string{"baseline has no recall", "its cases [a] seed no bug", "include a case that seeds one"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cases := slices.Clone(tt.cases)
			err := Comparable(tt.baseline, tt.cfg, cases)
			if !slices.Equal(cases, tt.cases) {
				t.Errorf("Comparable reordered the caller's cases: %v, want %v", cases, tt.cases)
			}
			if tt.want == nil {
				if err != nil {
					t.Fatalf("Comparable = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatal("Comparable = nil, want an error")
			}
			for _, want := range tt.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("err = %q, want it to contain %q", err, want)
				}
			}
		})
	}
}

func TestCompare(t *testing.T) {
	tests := []struct {
		name          string
		base, cand    Report
		wantVerdict   string
		wantHeld      map[string]bool
		wantNilFloors []string
	}{
		{
			name:        "identical reports hold",
			base:        comparableReport(ratio(0.9), ratio(0.8), ratio(0.7)),
			cand:        comparableReport(ratio(0.9), ratio(0.8), ratio(0.7)),
			wantVerdict: VerdictHeld,
			wantHeld:    map[string]bool{"recall": true, "precision": true, "severity_accuracy": true},
		},
		{
			name:        "any recall drop regresses",
			base:        comparableReport(ratio(0.93), ratio(0.9), ratio(0.9)),
			cand:        comparableReport(ratio(0.90), ratio(0.9), ratio(0.9)),
			wantVerdict: VerdictRegressed,
			wantHeld:    map[string]bool{"recall": false, "precision": true, "severity_accuracy": true},
		},
		{
			name:        "precision 5 points down holds",
			base:        comparableReport(ratio(1), ratio(0.90), ratio(1)),
			cand:        comparableReport(ratio(1), ratio(0.85), ratio(1)),
			wantVerdict: VerdictHeld,
			wantHeld:    map[string]bool{"recall": true, "precision": true, "severity_accuracy": true},
		},
		{
			name:        "precision exactly 10 points down holds",
			base:        comparableReport(ratio(1), ratio(0.90), ratio(1)),
			cand:        comparableReport(ratio(1), ratio(0.80), ratio(1)),
			wantVerdict: VerdictHeld,
			wantHeld:    map[string]bool{"recall": true, "precision": true, "severity_accuracy": true},
		},
		{
			name:        "precision 11 points down regresses",
			base:        comparableReport(ratio(1), ratio(0.90), ratio(1)),
			cand:        comparableReport(ratio(1), ratio(0.79), ratio(1)),
			wantVerdict: VerdictRegressed,
			wantHeld:    map[string]bool{"recall": true, "precision": false, "severity_accuracy": true},
		},
		{
			name:        "severity accuracy 5 points down holds",
			base:        comparableReport(ratio(1), ratio(1), ratio(0.90)),
			cand:        comparableReport(ratio(1), ratio(1), ratio(0.85)),
			wantVerdict: VerdictHeld,
			wantHeld:    map[string]bool{"recall": true, "precision": true, "severity_accuracy": true},
		},
		{
			name:        "severity accuracy exactly 10 points down holds",
			base:        comparableReport(ratio(1), ratio(1), ratio(0.90)),
			cand:        comparableReport(ratio(1), ratio(1), ratio(0.80)),
			wantVerdict: VerdictHeld,
			wantHeld:    map[string]bool{"recall": true, "precision": true, "severity_accuracy": true},
		},
		{
			name:        "severity accuracy 11 points down regresses",
			base:        comparableReport(ratio(1), ratio(1), ratio(0.90)),
			cand:        comparableReport(ratio(1), ratio(1), ratio(0.79)),
			wantVerdict: VerdictRegressed,
			wantHeld:    map[string]bool{"recall": true, "precision": true, "severity_accuracy": false},
		},
		{
			name:          "precision undefined on the baseline holds with no floor",
			base:          comparableReport(ratio(1), nil, ratio(1)),
			cand:          comparableReport(ratio(1), ratio(0.1), ratio(1)),
			wantVerdict:   VerdictHeld,
			wantHeld:      map[string]bool{"recall": true, "precision": true, "severity_accuracy": true},
			wantNilFloors: []string{"precision"},
		},
		{
			name:          "precision undefined on the candidate holds with no floor",
			base:          comparableReport(ratio(1), ratio(0.9), ratio(1)),
			cand:          comparableReport(ratio(1), nil, ratio(1)),
			wantVerdict:   VerdictHeld,
			wantHeld:      map[string]bool{"recall": true, "precision": true, "severity_accuracy": true},
			wantNilFloors: []string{"precision"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Compare(tt.base, tt.cand)
			if err != nil {
				t.Fatalf("Compare: %v", err)
			}
			if got.Verdict != tt.wantVerdict {
				t.Errorf("Verdict = %s, want %s", got.Verdict, tt.wantVerdict)
			}
			if len(got.Checks) != len(tt.wantHeld) {
				t.Fatalf("Checks = %+v, want %d", got.Checks, len(tt.wantHeld))
			}
			for _, c := range got.Checks {
				want, ok := tt.wantHeld[c.Metric]
				if !ok {
					t.Errorf("unexpected check %q", c.Metric)
					continue
				}
				if c.Held != want {
					t.Errorf("%s held = %v, want %v", c.Metric, c.Held, want)
				}
				if slices.Contains(tt.wantNilFloors, c.Metric) != (c.Floor == nil) {
					t.Errorf("%s floor = %v, want nil: %v", c.Metric, c.Floor, slices.Contains(tt.wantNilFloors, c.Metric))
				}
			}
		})
	}
}

func TestCompareFloors(t *testing.T) {
	got, err := Compare(comparableReport(ratio(0.93), ratio(0.9), ratio(0.8)), comparableReport(ratio(0.93), ratio(0.9), ratio(0.8)))
	if err != nil {
		t.Fatalf("Compare: %v", err)
	}
	want := map[string]float64{"recall": 0.93, "precision": 0.8, "severity_accuracy": 0.7}
	for _, c := range got.Checks {
		if c.Floor == nil || math.Abs(*c.Floor-want[c.Metric]) > 1e-9 {
			t.Errorf("%s floor = %v, want %v", c.Metric, c.Floor, want[c.Metric])
		}
	}
}

func TestCompareIncomparable(t *testing.T) {
	cand := comparableReport(ratio(1), ratio(1), ratio(1))
	cand.Runs = 1
	if _, err := Compare(comparableReport(ratio(1), ratio(1), ratio(1)), cand); err == nil || !strings.Contains(err.Error(), "at least 3 runs per side") {
		t.Errorf("Compare with one candidate run = %v, want a runs error", err)
	}
}

func TestComparePassesAndCost(t *testing.T) {
	base := comparableReport(ratio(1), ratio(1), ratio(1))
	base.Passes = []PassRecall{{Pass: "review", Found: 8, Recall: ratio(0.8)}, {Pass: "specialist:testing", Found: 2, Recall: ratio(0.2)}}
	base.Aggregate.Usage = report.Usage{InputTokens: 10000, CacheReadTokens: 15000, CacheCreationTokens: 5000, OutputTokens: 3000, CostUSD: 3}
	cand := comparableReport(ratio(1), ratio(1), ratio(1))
	cand.Runs = 4
	cand.Passes = []PassRecall{{Pass: "review", Found: 7, Recall: ratio(0.7)}, {Pass: "adversarial", Found: 1, Recall: ratio(0.1)}}
	cand.Aggregate.Usage = report.Usage{InputTokens: 20000, OutputTokens: 400, CostUSD: 2}

	got, err := Compare(base, cand)
	if err != nil {
		t.Fatalf("Compare: %v", err)
	}
	wantPasses := []PassDelta{
		{Pass: "adversarial", Candidate: ratio(0.1)},
		{Pass: "review", Baseline: ratio(0.8), Candidate: ratio(0.7)},
		{Pass: "specialist:testing", Baseline: ratio(0.2)},
	}
	if !reflect.DeepEqual(got.Passes, wantPasses) {
		t.Errorf("Passes = %+v, want %+v", got.Passes, wantPasses)
	}
	// 30 000 prompt tokens over 3 runs is 10 000 per run.
	if want := (RunCost{PromptTokens: 10000, OutputTokens: 1000, CostUSD: 1}); got.Baseline != want {
		t.Errorf("Baseline cost = %+v, want %+v", got.Baseline, want)
	}
	if want := (RunCost{PromptTokens: 5000, OutputTokens: 100, CostUSD: 0.5}); got.Candidate != want {
		t.Errorf("Candidate cost = %+v, want %+v", got.Candidate, want)
	}
}

func TestRenderComparison(t *testing.T) {
	cmp, err := Compare(comparableReport(ratio(0.9), nil, ratio(0.8)), comparableReport(ratio(0.9), ratio(0.5), ratio(0.8)))
	if err != nil {
		t.Fatalf("Compare: %v", err)
	}
	cmp.Baseline = RunCost{PromptTokens: 1000, OutputTokens: 0, CostUSD: 2}
	cmp.Candidate = RunCost{PromptTokens: 750, OutputTokens: 10, CostUSD: 2.5}
	cmp.Passes = []PassDelta{{Pass: "review", Baseline: ratio(0.8), Candidate: ratio(0.75)}}

	var buf strings.Builder
	RenderComparison(&buf, cmp)
	out := buf.String()
	lineOf := func(prefix string) string {
		t.Helper()
		return lineStarting(t, out, prefix)
	}

	if !strings.Contains(out, "VERDICT: HELD") {
		t.Errorf("output lacks VERDICT: HELD:\n%s", out)
	}
	if f := strings.Fields(lineOf("precision ")); f[len(f)-1] != "n/a" {
		t.Errorf("undefined precision line = %q, want it to end in n/a", lineOf("precision "))
	}
	if f := strings.Fields(lineOf("recall ")); f[len(f)-1] != "held" || f[1] != "90.0%" {
		t.Errorf("recall line = %q, want 90.0%% held", lineOf("recall "))
	}
	if !strings.Contains(lineOf("review "), "-5.0 pts") {
		t.Errorf("review delta line = %q, want -5.0 pts", lineOf("review "))
	}
	if !strings.HasSuffix(lineOf("prompt tokens "), "-25.0%") {
		t.Errorf("prompt tokens line = %q, want -25.0%%", lineOf("prompt tokens "))
	}
	if !strings.HasSuffix(lineOf("output tokens "), "n/a") {
		t.Errorf("output tokens line = %q, want n/a for a zero baseline", lineOf("output tokens "))
	}
	if !strings.HasSuffix(lineOf("est USD "), "+25.0%") {
		t.Errorf("est USD line = %q, want +25.0%%", lineOf("est USD "))
	}
}

func TestRenderComparisonRegressed(t *testing.T) {
	cand := comparableReport(ratio(0.90), ratio(0.9), ratio(0.9))
	cand.Passes = []PassRecall{{Pass: "adversarial", Found: 1, Recall: ratio(0.1)}}
	cmp, err := Compare(comparableReport(ratio(0.93), ratio(0.9), ratio(0.9)), cand)
	if err != nil {
		t.Fatalf("Compare: %v", err)
	}

	var buf strings.Builder
	RenderComparison(&buf, cmp)
	out := buf.String()

	if !strings.Contains(out, "VERDICT: REGRESSED") {
		t.Errorf("output lacks VERDICT: REGRESSED:\n%s", out)
	}
	if f := strings.Fields(lineStarting(t, out, "recall ")); f[len(f)-1] != VerdictRegressed {
		t.Errorf("recall line = %q, want it to end in REGRESSED", lineStarting(t, out, "recall "))
	}
	if f := strings.Fields(lineStarting(t, out, "adversarial ")); f[len(f)-1] != notApplicable {
		t.Errorf("one-sided pass line = %q, want its delta n/a", lineStarting(t, out, "adversarial "))
	}
}

// TestCostPerRunAgrees locks that the COST PER RUN table and the comparison
// divide a total by the runs the same way.
func TestCostPerRunAgrees(t *testing.T) {
	rep := comparableReport(ratio(1), ratio(1), ratio(1))
	rep.Aggregate.Usage = report.Usage{InputTokens: 4120001, OutputTokens: 2}
	cmp, err := Compare(rep, rep)
	if err != nil {
		t.Fatalf("Compare: %v", err)
	}
	// 4 120 001 / 3 = 1 373 333.67 and 2 / 3 = 0.67, both rounded up.
	if cmp.Candidate.PromptTokens != 1373334 || cmp.Candidate.OutputTokens != 1 {
		t.Errorf("comparison cost = %+v, want 1373334 prompt and 1 output tokens", cmp.Candidate)
	}
	var buf strings.Builder
	RenderTable(&buf, rep)
	if !strings.Contains(buf.String(), " 1373334 ") {
		t.Errorf("cost table lacks 1373334 prompt tokens per run:\n%s", buf.String())
	}
}

// lineStarting returns the first line of out that starts with prefix.
func lineStarting(t *testing.T, out, prefix string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, prefix) {
			return line
		}
	}
	t.Fatalf("no line starting with %q:\n%s", prefix, out)
	return ""
}
