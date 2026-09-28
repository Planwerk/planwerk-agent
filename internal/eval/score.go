package eval

import (
	"cmp"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/planwerk/planwerk-agent/internal/hygiene"
	"github.com/planwerk/planwerk-agent/internal/report"
)

// lineTolerance is the maximum absolute line distance for a predicted finding to
// match an expected one. Reviewers cite the symptom line, not always the exact
// seeded line, so a small window avoids penalizing a correct catch off by a line
// or two.
const lineTolerance = 3

// Score is the raw match tally for one case (or, aggregated, the whole corpus).
// The ratio methods return (value, defined): a ratio over a zero denominator is
// undefined and reported as such rather than as a misleading 0.
type Score struct {
	Clean           bool
	TP              int // expected findings matched by a prediction
	FP              int // predictions matching no expected finding
	FN              int // expected findings left unmatched
	SeverityMatches int // among TP, predictions whose severity equals the matched expected severity
	// FoundBy counts, per pass label, the expected findings that pass matched
	// (see ScoreCase); nil when no pass matched any.
	FoundBy map[string]int
}

// Precision is TP/(TP+FP); undefined when nothing was predicted.
func (s Score) Precision() (float64, bool) {
	den := s.TP + s.FP
	if den == 0 {
		return 0, false
	}
	return float64(s.TP) / float64(den), true
}

// Recall is TP/(TP+FN); undefined when nothing was expected (a clean case).
func (s Score) Recall() (float64, bool) {
	den := s.TP + s.FN
	if den == 0 {
		return 0, false
	}
	return float64(s.TP) / float64(den), true
}

// SeverityAccuracy is SeverityMatches/TP; undefined when nothing matched.
func (s Score) SeverityAccuracy() (float64, bool) {
	if s.TP == 0 {
		return 0, false
	}
	return float64(s.SeverityMatches) / float64(s.TP), true
}

// Add folds o into s, accumulating an aggregate over many cases.
func (s *Score) Add(o Score) {
	s.TP += o.TP
	s.FP += o.FP
	s.FN += o.FN
	s.SeverityMatches += o.SeverityMatches
	for pass, n := range o.FoundBy {
		if s.FoundBy == nil {
			s.FoundBy = make(map[string]int, len(o.FoundBy))
		}
		s.FoundBy[pass] += n
	}
}

// Scored pairs a case with the score pooled over its runs and the usage those
// runs spent.
type Scored struct {
	Case  Case
	Score Score
	Usage report.Usage
}

// ScoreRuns scores every run of c and pools them: the tallies add up, so every
// ratio of the result is a pooled ratio over the runs, and Usage is the sum of
// the runs' usage. No runs yield zero tallies with Clean copied from the case.
func ScoreRuns(c Case, runs []Run) Scored {
	sc := Scored{Case: c, Score: Score{Clean: c.Expected.Clean}}
	for _, r := range runs {
		sc.Score.Add(ScoreCase(c, r.Result))
		sc.Usage = sumUsage(sc.Usage, r.Usage)
	}
	return sc
}

// ScoreCase compares the predicted findings against the case's expected findings
// and returns the tally. Matching is greedy one-to-one from the expected side:
// each expected finding claims the first not-yet-claimed prediction that matches
// it (same file, line within tolerance, keyword present). Unclaimed predictions
// are false positives; unclaimed expected findings are false negatives.
//
// FoundBy credits each expected finding once to every pass in the union of
// ConfirmedBy over all predictions that match it, claimed or not; an empty
// ConfirmedBy counts as hygiene.PassReview. The credit answers only "did this
// pass see the bug": an unclaimed duplicate still counts as a false positive,
// and one prediction matching two expected findings credits its passes for
// both, so a pass's recall can exceed the pooled recall.
func ScoreCase(c Case, result report.ReviewResult) Score {
	preds := result.Findings
	claimed := make([]bool, len(preds))
	s := Score{Clean: c.Expected.Clean}

	for _, exp := range c.Expected.Findings {
		for pass := range passesMatching(preds, exp) {
			if s.FoundBy == nil {
				s.FoundBy = make(map[string]int)
			}
			s.FoundBy[pass]++
		}
		matchIdx := -1
		for i := range preds {
			if claimed[i] {
				continue
			}
			if matches(preds[i], exp) {
				matchIdx = i
				break
			}
		}
		if matchIdx < 0 {
			s.FN++
			continue
		}
		claimed[matchIdx] = true
		s.TP++
		if strings.EqualFold(strings.TrimSpace(string(preds[matchIdx].Severity)), strings.TrimSpace(exp.Severity)) {
			s.SeverityMatches++
		}
	}
	for i := range preds {
		if !claimed[i] {
			s.FP++
		}
	}
	return s
}

// passesMatching returns the union of ConfirmedBy over every prediction that
// matches exp. A prediction with no ConfirmedBy counts as the primary review:
// the pipeline stamps provenance only when a secondary pass ran.
func passesMatching(preds []report.Finding, exp ExpectedFinding) map[string]bool {
	passes := make(map[string]bool)
	for _, p := range preds {
		if !matches(p, exp) {
			continue
		}
		if len(p.ConfirmedBy) == 0 {
			passes[hygiene.PassReview] = true
			continue
		}
		for _, pass := range p.ConfirmedBy {
			passes[pass] = true
		}
	}
	return passes
}

// matches reports whether a predicted finding satisfies the match rule for an
// expected one: same file, line within lineTolerance, and at least one expected
// keyword present (case-insensitively) in the predicted title or problem.
func matches(pred report.Finding, exp ExpectedFinding) bool {
	if normPath(pred.File) != normPath(exp.File) {
		return false
	}
	if abs(pred.Line-exp.Line) > lineTolerance {
		return false
	}
	haystack := strings.ToLower(pred.Title + "\n" + pred.Problem)
	for _, kw := range exp.Keywords {
		kw = strings.ToLower(strings.TrimSpace(kw))
		if kw != "" && strings.Contains(haystack, kw) {
			return true
		}
	}
	return false
}

func normPath(p string) string {
	return strings.TrimSpace(strings.ReplaceAll(p, "\\", "/"))
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// CaseScore is the JSON/text-facing view of one case's (or the aggregate's)
// score. Undefined ratios serialize as null and render as "n/a". Tallies and
// Usage are totals over all runs.
type CaseScore struct {
	Name             string         `json:"name"`
	Description      string         `json:"description,omitempty"`
	Clean            bool           `json:"clean"`
	TP               int            `json:"tp"`
	FP               int            `json:"fp"`
	FN               int            `json:"fn"`
	SeverityMatches  int            `json:"severity_matches"`
	Precision        *float64       `json:"precision"`
	Recall           *float64       `json:"recall"`
	SeverityAccuracy *float64       `json:"severity_accuracy"`
	FoundBy          map[string]int `json:"found_by,omitempty"`
	Usage            report.Usage   `json:"usage"`
}

// Config records how a report's runs were made: the runs per case and the
// passes each run added to the primary review.
type Config struct {
	Runs int `json:"runs"`
	RunOptions
}

// PassRecall is one pass's share of the pooled recall: Found expected findings
// credited to the pass (see ScoreCase) over the aggregate TP+FN. Recall is nil
// when that denominator is 0.
type PassRecall struct {
	Pass   string   `json:"pass"`
	Found  int      `json:"found"`
	Recall *float64 `json:"recall"`
}

// Report is the full scored corpus: the run configuration, per-case rows, the
// corpus-wide aggregate, and the recall credited to each pass.
type Report struct {
	Config
	Cases     []CaseScore  `json:"cases"`
	Aggregate CaseScore    `json:"aggregate"`
	Passes    []PassRecall `json:"passes"`
}

// BuildReport turns scored cases into the renderable report, computing the
// aggregate by summing the raw tallies (not by averaging per-case ratios, which
// would double-weight small cases) and the usage of every case.
func BuildReport(scored []Scored, cfg Config) Report {
	rep := Report{Config: cfg}
	var agg Score
	var usage report.Usage
	for _, sc := range scored {
		cs := toCaseScore(sc.Case.Name, sc.Case.Expected.Description, sc.Score)
		cs.Usage = sc.Usage
		rep.Cases = append(rep.Cases, cs)
		agg.Add(sc.Score)
		usage = sumUsage(usage, sc.Usage)
	}
	rep.Aggregate = toCaseScore("AGGREGATE", "", agg)
	rep.Aggregate.Usage = usage
	rep.Passes = passRecalls(agg)
	return rep
}

// passRecalls lists one PassRecall per pass agg credits, ordered by Found
// descending, then by name. It is never nil, so the JSON carries [] rather
// than null.
func passRecalls(agg Score) []PassRecall {
	passes := make([]PassRecall, 0, len(agg.FoundBy))
	expected := agg.TP + agg.FN
	for pass, found := range agg.FoundBy {
		pr := PassRecall{Pass: pass, Found: found}
		if expected > 0 {
			v := float64(found) / float64(expected)
			pr.Recall = &v
		}
		passes = append(passes, pr)
	}
	slices.SortFunc(passes, func(a, b PassRecall) int {
		return cmp.Or(cmp.Compare(b.Found, a.Found), cmp.Compare(a.Pass, b.Pass))
	})
	return passes
}

func toCaseScore(name, desc string, s Score) CaseScore {
	cs := CaseScore{
		Name:            name,
		Description:     desc,
		Clean:           s.Clean,
		TP:              s.TP,
		FP:              s.FP,
		FN:              s.FN,
		SeverityMatches: s.SeverityMatches,
		FoundBy:         s.FoundBy,
	}
	if v, ok := s.Precision(); ok {
		cs.Precision = &v
	}
	if v, ok := s.Recall(); ok {
		cs.Recall = &v
	}
	if v, ok := s.SeverityAccuracy(); ok {
		cs.SeverityAccuracy = &v
	}
	return cs
}

// RenderTable writes the report as an aligned text table. Undefined ratios show
// as "n/a"; a clean case's recall is always undefined and is noted in the
// footer. The table is followed by the run configuration, the recall by pass,
// and the cost per run, where every value is a total divided by the runs.
func RenderTable(w io.Writer, rep Report) {
	const header = "%-22s  %5s  %3s  %3s  %3s  %9s  %7s  %8s\n"
	const row = "%-22s  %5s  %3d  %3d  %3d  %9s  %7s  %8s\n"
	_, _ = fmt.Fprintf(w, header, "CASE", "CLEAN", "TP", "FP", "FN", "PRECISION", "RECALL", "SEV-ACC")
	for _, c := range rep.Cases {
		writeRow(w, row, c)
	}
	_, _ = fmt.Fprintln(w, strings.Repeat("-", 74))
	writeRow(w, row, rep.Aggregate)
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintln(w, "PRECISION = TP/(TP+FP), RECALL = TP/(TP+FN), SEV-ACC = severity matches/TP.")
	_, _ = fmt.Fprintln(w, "A clean case seeds no bug: recall is undefined (n/a) and every finding is a false positive.")

	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintf(w, "RUNS: %d per case (thorough: %s, specialists: %s)\n",
		rep.Runs, yesNo(rep.Thorough), yesNo(rep.Specialists))
	renderPassRecall(w, rep.Passes)
	renderCostPerRun(w, rep)
}

// labelWidth is the width of the label column that leads the RECALL BY PASS
// and COST PER RUN sections and the comparison.
const labelWidth = 28

// renderPassRecall writes the RECALL BY PASS section.
func renderPassRecall(w io.Writer, passes []PassRecall) {
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintln(w, "RECALL BY PASS")
	if len(passes) == 0 {
		_, _ = fmt.Fprintln(w, "(none)")
		return
	}
	_, _ = fmt.Fprintf(w, "%-*s  %5s  %7s\n", labelWidth, "PASS", "FOUND", "RECALL")
	for _, p := range passes {
		_, _ = fmt.Fprintf(w, "%-*s  %5d  %7s\n", labelWidth, truncate(p.Pass, labelWidth), p.Found, pct(p.Recall))
	}
}

// renderCostPerRun writes the COST PER RUN section: one row per case, the
// aggregate, then the aggregate's passes, each total divided by rep.Runs.
func renderCostPerRun(w io.Writer, rep Report) {
	const header = "%-*s  %6s  %13s  %13s  %8s\n"
	const row = "%-*s  %6.1f  %13.0f  %13.0f  %8s\n"
	runs := float64(max(rep.Runs, 1))
	costRow := func(name string, calls int, prompt, output int64, usd float64) {
		_, _ = fmt.Fprintf(w, row, labelWidth, truncate(name, labelWidth), float64(calls)/runs,
			float64(prompt)/runs, float64(output)/runs, fmt.Sprintf("$%.2f", usd/runs))
	}

	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintln(w, "COST PER RUN")
	head := fmt.Sprintf(header, labelWidth, "CASE", "CALLS", "PROMPT TOKENS", "OUTPUT TOKENS", "EST USD")
	_, _ = io.WriteString(w, head)
	for _, c := range rep.Cases {
		costRow(c.Name, c.Usage.Calls, promptTokens(c.Usage), c.Usage.OutputTokens, c.Usage.CostUSD)
	}
	// The rule is as wide as the header row, its newline aside.
	_, _ = fmt.Fprintln(w, strings.Repeat("-", len(head)-1))
	agg := rep.Aggregate.Usage
	costRow(rep.Aggregate.Name, agg.Calls, promptTokens(agg), agg.OutputTokens, agg.CostUSD)
	for _, p := range agg.Passes {
		costRow("  "+p.Pass, p.Calls, passPromptTokens(p), p.OutputTokens, p.CostUSD)
	}
}

func writeRow(w io.Writer, format string, c CaseScore) {
	_, _ = fmt.Fprintf(w, format, truncate(c.Name, 22), yesNo(c.Clean), c.TP, c.FP, c.FN,
		pct(c.Precision), pct(c.Recall), pct(c.SeverityAccuracy))
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// pct formats an optional ratio as a percentage, or "n/a" when undefined.
func pct(v *float64) string {
	if v == nil {
		return "n/a"
	}
	return fmt.Sprintf("%.0f%%", *v*100)
}

// RenderJSON writes the report as indented JSON. Undefined ratios serialize as
// null (see CaseScore's pointer fields).
func RenderJSON(w io.Writer, rep Report) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(rep)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
