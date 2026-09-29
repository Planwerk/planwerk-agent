package eval

import (
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"math"
	"os"
	"slices"
	"strings"
)

// The rule a run is judged by against a saved baseline (decision 106): at
// least MinCompareRuns runs per case on each side, pooled recall not below the
// baseline's, and pooled precision and severity accuracy at most their
// tolerance below it. floorSlack absorbs float rounding at a floor.
const (
	MinCompareRuns     = 3
	precisionTolerance = 0.10
	severityTolerance  = 0.10
	floorSlack         = 1e-9
)

// The verdicts Compare returns.
const (
	VerdictHeld      = "HELD"
	VerdictRegressed = "REGRESSED"
)

// Comparison is a candidate report judged against a baseline: the verdict, one
// check per judged metric, the recall by pass on both sides, each side's cost
// per run, and the tiers each side ran on. Cost is reported beside the verdict
// and never decides it.
type Comparison struct {
	Verdict        string      `json:"verdict"`
	Checks         []Check     `json:"checks"`
	Passes         []PassDelta `json:"recall_by_pass"`
	Baseline       RunCost     `json:"baseline"`
	Candidate      RunCost     `json:"candidate"`
	BaselineTiers  Tiers       `json:"baseline_tiers"`
	CandidateTiers Tiers       `json:"candidate_tiers"`
}

// Check is one metric's aggregate ratio on both sides and the floor the
// candidate must reach. When either ratio is undefined, Floor is nil and the
// check holds.
type Check struct {
	Metric    string   `json:"metric"`
	Baseline  *float64 `json:"baseline"`
	Candidate *float64 `json:"candidate"`
	Floor     *float64 `json:"floor"`
	Held      bool     `json:"held"`
}

// PassDelta is one pass's recall on both sides, nil on a side that credits
// the pass nothing.
type PassDelta struct {
	Pass      string   `json:"pass"`
	Baseline  *float64 `json:"baseline"`
	Candidate *float64 `json:"candidate"`
}

// RunCost is a side's aggregate usage divided by its runs.
type RunCost struct {
	PromptTokens int64   `json:"prompt_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	CostUSD      float64 `json:"est_cost_usd"`
}

// LoadReport reads a report written by -json. A report without runs cannot be
// compared and is rejected: that is what a report from before the run count
// decodes to.
func LoadReport(path string) (Report, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Report{}, fmt.Errorf("reading baseline %s: %w", path, err)
	}
	var rep Report
	if err := json.Unmarshal(data, &rep); err != nil {
		return Report{}, fmt.Errorf("parsing baseline %s: %w", path, err)
	}
	if rep.Runs < 1 {
		return Report{}, fmt.Errorf("baseline %s records no runs; regenerate it with this harness", path)
	}
	return rep, nil
}

// Comparable returns why baseline cannot be compared with a run made under cfg
// over the named cases, or nil when it can. Both sides need MinCompareRuns
// runs, the same passes, and the same set of cases in any order, and the
// baseline needs a recall: over clean cases alone no check can fail. main calls
// it before the first case runs, so a mismatch spends no tokens.
func Comparable(baseline Report, cfg Config, cases []string) error {
	if baseline.Runs < MinCompareRuns || cfg.Runs < MinCompareRuns {
		return fmt.Errorf("a comparison needs at least %d runs per side (baseline %d, this run %d); re-run with -runs %d",
			MinCompareRuns, baseline.Runs, cfg.Runs, MinCompareRuns)
	}
	if baseline.Thorough != cfg.Thorough || baseline.Specialists != cfg.Specialists {
		return fmt.Errorf("baseline ran with thorough=%t specialists=%t, this run with thorough=%t specialists=%t; compare like with like",
			baseline.Thorough, baseline.Specialists, cfg.Thorough, cfg.Specialists)
	}
	base := nameSet(caseNames(baseline.Cases))
	cand := nameSet(cases)
	if !slices.Equal(base, cand) {
		return fmt.Errorf("baseline covers cases %v, this run covers %v; compare like with like", base, cand)
	}
	if baseline.Aggregate.Recall == nil {
		return fmt.Errorf("baseline has no recall: its cases %v seed no bug, so no check could fail; include a case that seeds one", base)
	}
	return nil
}

// Compare judges candidate against baseline under the rule on the aggregate
// row: recall no lower than the baseline's, precision and severity accuracy at
// most their tolerance lower. It returns Comparable's error when the reports
// cannot be compared.
func Compare(baseline, candidate Report) (Comparison, error) {
	if err := Comparable(baseline, candidate.Config, caseNames(candidate.Cases)); err != nil {
		return Comparison{}, err
	}
	b, c := baseline.Aggregate, candidate.Aggregate
	out := Comparison{
		Verdict: VerdictHeld,
		Checks: []Check{
			check("recall", b.Recall, c.Recall, 0),
			check("precision", b.Precision, c.Precision, precisionTolerance),
			check("severity_accuracy", b.SeverityAccuracy, c.SeverityAccuracy, severityTolerance),
		},
		Passes:         passDeltas(baseline.Passes, candidate.Passes),
		Baseline:       runCost(baseline),
		Candidate:      runCost(candidate),
		BaselineTiers:  baseline.Tiers,
		CandidateTiers: candidate.Tiers,
	}
	for _, ch := range out.Checks {
		if !ch.Held {
			out.Verdict = VerdictRegressed
		}
	}
	return out, nil
}

// check builds the Check for one metric with the floor tolerance below the
// baseline.
func check(metric string, base, cand *float64, tolerance float64) Check {
	c := Check{Metric: metric, Baseline: base, Candidate: cand, Held: true}
	if base == nil || cand == nil {
		return c
	}
	floor := *base - tolerance
	c.Floor = &floor
	c.Held = *cand >= floor-floorSlack
	return c
}

// passDeltas pairs both sides' recall by pass over the union of their labels,
// ordered by name.
func passDeltas(base, cand []PassRecall) []PassDelta {
	byPass := make(map[string]PassDelta, len(base)+len(cand))
	for _, p := range base {
		d := byPass[p.Pass]
		d.Pass, d.Baseline = p.Pass, p.Recall
		byPass[p.Pass] = d
	}
	for _, p := range cand {
		d := byPass[p.Pass]
		d.Pass, d.Candidate = p.Pass, p.Recall
		byPass[p.Pass] = d
	}
	return slices.SortedFunc(maps.Values(byPass), func(a, b PassDelta) int {
		return strings.Compare(a.Pass, b.Pass)
	})
}

// runCost divides rep's aggregate usage by its runs.
func runCost(rep Report) RunCost {
	u := rep.Aggregate.Usage
	return perRun(promptTokens(u), u.OutputTokens, u.CostUSD, rep.Runs)
}

// perRun divides usage totals by runs, rounding the tokens to the nearest
// token. The COST PER RUN table and the comparison both divide through it, so
// they print the same value.
func perRun(prompt, output int64, usd float64, runs int) RunCost {
	n := float64(max(runs, 1))
	return RunCost{
		PromptTokens: int64(math.Round(float64(prompt) / n)),
		OutputTokens: int64(math.Round(float64(output) / n)),
		CostUSD:      usd / n,
	}
}

func caseNames(cases []CaseScore) []string {
	names := make([]string, 0, len(cases))
	for _, c := range cases {
		names = append(names, c.Name)
	}
	return names
}

// nameSet returns names sorted with duplicates removed, leaving names itself
// untouched.
func nameSet(names []string) []string {
	return slices.Compact(slices.Sorted(slices.Values(names)))
}

// RenderComparison writes the verdict, the BASELINE TIERS and CANDIDATE TIERS
// lines, one row per check, the recall by pass on both sides, and both sides'
// cost per run with the relative change. Ratios carry one decimal so a
// candidate a fraction of a point off its floor reads differently from it.
func RenderComparison(w io.Writer, cmp Comparison) {
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintf(w, "VERDICT: %s\n", cmp.Verdict)
	_, _ = fmt.Fprintf(w, "%-*s  %s\n", labelWidth, "BASELINE TIERS", cmp.BaselineTiers)
	_, _ = fmt.Fprintf(w, "%-*s  %s\n", labelWidth, "CANDIDATE TIERS", cmp.CandidateTiers)

	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintf(w, "%-*s  %9s  %9s  %9s  %s\n", labelWidth, "METRIC", "BASELINE", "CANDIDATE", "FLOOR", "RESULT")
	for _, c := range cmp.Checks {
		result := "held"
		switch {
		case c.Floor == nil:
			result = notApplicable
		case !c.Held:
			result = VerdictRegressed
		}
		_, _ = fmt.Fprintf(w, "%-*s  %9s  %9s  %9s  %s\n", labelWidth, c.Metric, pct1(c.Baseline), pct1(c.Candidate), pct1(c.Floor), result)
	}

	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintf(w, "%-*s  %9s  %9s  %10s\n", labelWidth, "PASS RECALL", "BASELINE", "CANDIDATE", "DELTA")
	for _, p := range cmp.Passes {
		delta := notApplicable
		if p.Baseline != nil && p.Candidate != nil {
			delta = fmt.Sprintf("%+.1f pts", (*p.Candidate-*p.Baseline)*100)
		}
		_, _ = fmt.Fprintf(w, "%-*s  %9s  %9s  %10s\n", labelWidth, truncate(p.Pass, labelWidth), pct1(p.Baseline), pct1(p.Candidate), delta)
	}

	b, c := cmp.Baseline, cmp.Candidate
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintf(w, "%-*s  %13s  %13s  %8s\n", labelWidth, "COST PER RUN", "BASELINE", "CANDIDATE", "CHANGE")
	_, _ = fmt.Fprintf(w, "%-*s  %13d  %13d  %8s\n", labelWidth, "prompt tokens", b.PromptTokens, c.PromptTokens,
		relChange(float64(b.PromptTokens), float64(c.PromptTokens)))
	_, _ = fmt.Fprintf(w, "%-*s  %13d  %13d  %8s\n", labelWidth, "output tokens", b.OutputTokens, c.OutputTokens,
		relChange(float64(b.OutputTokens), float64(c.OutputTokens)))
	_, _ = fmt.Fprintf(w, "%-*s  %13s  %13s  %8s\n", labelWidth, "est USD", fmt.Sprintf("$%.2f", b.CostUSD), fmt.Sprintf("$%.2f", c.CostUSD),
		relChange(b.CostUSD, c.CostUSD))
}

// pct1 formats an optional ratio as a percentage with one decimal, or "n/a".
func pct1(v *float64) string {
	if v == nil {
		return notApplicable
	}
	return fmt.Sprintf("%.1f%%", *v*100)
}

// relChange formats the change from base to cand relative to base, or "n/a"
// when base is 0.
func relChange(base, cand float64) string {
	if base == 0 {
		return notApplicable
	}
	return fmt.Sprintf("%+.1f%%", (cand-base)/base*100)
}
