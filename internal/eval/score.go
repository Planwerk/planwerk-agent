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

// lineTolerance is the largest gap, in lines, between a predicted finding's
// line range and an expected one's for the two to match. Reviewers cite the
// symptom line, not always the exact seeded line, so a small window avoids
// penalizing a correct catch off by a line or two.
const lineTolerance = 3

// MatchRule names the rule ScoreCase tallies by: matches, and the one-to-one
// pairing over it (pairFindings). Change the value whenever either changes. A
// report records it, and Comparable refuses a baseline scored under another
// rule, because the same predictions tally differently under each (decision
// 146).
const MatchRule = "range-overlap"

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

// F1 is 2TP/(2TP+FP+FN), the harmonic mean of precision and recall over the
// pooled tallies; undefined when nothing was expected, like Recall, and 0 when
// something was expected and nothing matched.
func (s Score) F1() (float64, bool) {
	if s.TP+s.FN == 0 {
		return 0, false
	}
	return float64(2*s.TP) / float64(2*s.TP+s.FP+s.FN), true
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
// and returns the tally. A prediction is a candidate for an expected finding
// when it matches it (same file, line ranges within tolerance, keyword present),
// and pairFindings pairs the two sides one to one so that as many expected
// findings as possible claim a candidate. TP, FP and FN therefore do not depend
// on the order the findings come in, and a match rule that admits more
// candidates never lowers TP. Where an expected finding has a choice, the
// pairing tries the candidates with its severity first. Unclaimed predictions
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
	exps := c.Expected.Findings
	s := Score{Clean: c.Expected.Clean}

	candidates := make([][]int, len(exps))
	for e, exp := range exps {
		for pass := range passesMatching(preds, exp) {
			if s.FoundBy == nil {
				s.FoundBy = make(map[string]int)
			}
			s.FoundBy[pass]++
		}
		var otherSeverity []int
		for i := range preds {
			if !matches(preds[i], exp) {
				continue
			}
			if sameSeverity(preds[i], exp) {
				candidates[e] = append(candidates[e], i)
			} else {
				otherSeverity = append(otherSeverity, i)
			}
		}
		candidates[e] = append(candidates[e], otherSeverity...)
	}

	for i, e := range pairFindings(len(preds), candidates) {
		if e < 0 {
			s.FP++
			continue
		}
		s.TP++
		if sameSeverity(preds[i], exps[e]) {
			s.SeverityMatches++
		}
	}
	s.FN = len(exps) - s.TP
	return s
}

// sameSeverity reports whether a prediction carries the expected finding's
// severity, ignoring case and surrounding space.
func sameSeverity(pred report.Finding, exp ExpectedFinding) bool {
	return strings.EqualFold(strings.TrimSpace(string(pred.Severity)), strings.TrimSpace(exp.Severity))
}

// pairFindings pairs each expected finding with at most one of its candidate
// predictions, and each of the nPreds predictions with at most one expected
// finding, so that as many expected findings as possible are paired: a maximum
// bipartite matching by augmenting paths (Kuhn's algorithm). candidates[e] lists
// the predictions expected finding e may claim, in the order it tries them. It
// returns, per prediction, the index of the expected finding that claimed it,
// or -1.
func pairFindings(nPreds int, candidates [][]int) []int {
	claimedBy := make([]int, nPreds)
	for i := range claimedBy {
		claimedBy[i] = -1
	}
	for e := range candidates {
		augment(e, candidates, claimedBy, make([]bool, nPreds))
	}
	return claimedBy
}

// augment looks for a prediction expected finding e can claim: an unclaimed
// candidate, or a claimed one whose claimant can move on to another candidate
// of its own. seen marks the predictions this search has tried. It reports
// whether e was paired, and on success updates claimedBy along the path.
func augment(e int, candidates [][]int, claimedBy []int, seen []bool) bool {
	for _, i := range candidates[e] {
		if seen[i] {
			continue
		}
		seen[i] = true
		if claimedBy[i] < 0 || augment(claimedBy[i], candidates, claimedBy, seen) {
			claimedBy[i] = e
			return true
		}
	}
	return false
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
// expected one: same file, the predicted line range within lineTolerance of the
// expected one (see lineSpan), and at least one expected keyword present
// (case-insensitively) in the predicted title or problem.
func matches(pred report.Finding, exp ExpectedFinding) bool {
	if normPath(pred.File) != normPath(exp.File) {
		return false
	}
	ps, pe := lineSpan(pred.Line, pred.LineEnd)
	es, ee := lineSpan(exp.Line, exp.LineEnd)
	if ps > ee+lineTolerance || pe < es-lineTolerance {
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

// lineSpan returns the inclusive range [line, lineEnd], where a lineEnd of 0 or
// one before line stands for line alone. A line of 0 or less marks a finding
// with no location, as elsewhere in the pipeline, so its lineEnd is ignored and
// the span is that line alone.
func lineSpan(line, lineEnd int) (start, end int) {
	if line <= 0 {
		return line, line
	}
	return line, max(line, lineEnd)
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
	F1               *float64       `json:"f1"`
	SeverityAccuracy *float64       `json:"severity_accuracy"`
	FoundBy          map[string]int `json:"found_by,omitempty"`
	Usage            report.Usage   `json:"usage"`
}

// Config records how a report's runs were made: the runs per case, the passes
// each run added to the primary review, and the tiers the runs resolved to.
type Config struct {
	Runs int `json:"runs"`
	RunOptions
	Tiers Tiers `json:"tiers"`
}

// Tiers records the model and effort each tier of a run resolved to, so a
// report says what it measured. Comparable never reads it: a candidate
// finder tier against the baseline's is the comparison the tier
// experiment exists for.
type Tiers struct {
	ClaudeModel     string `json:"claude_model"`
	ClaudeEffort    string `json:"claude_effort"`
	FinderModel     string `json:"finder_model"`
	FinderEffort    string `json:"finder_effort"`
	StructureModel  string `json:"structure_model"`
	StructureEffort string `json:"structure_effort"`
}

// String renders the tiers as "claude <model>/<effort>, finder
// <model>/<effort>, structure <model>/<effort>", or "(unrecorded)" for the zero
// value a report written before the tiers were recorded decodes to.
func (t Tiers) String() string {
	if t == (Tiers{}) {
		return unrecorded
	}
	return fmt.Sprintf("claude %s/%s, finder %s/%s, structure %s/%s",
		t.ClaudeModel, t.ClaudeEffort, t.FinderModel, t.FinderEffort, t.StructureModel, t.StructureEffort)
}

// PassRecall is one pass's share of the pooled recall: Found expected findings
// credited to the pass (see ScoreCase) over the aggregate TP+FN. Recall is nil
// when that denominator is 0.
type PassRecall struct {
	Pass   string   `json:"pass"`
	Found  int      `json:"found"`
	Recall *float64 `json:"recall"`
}

// Report is the full scored corpus: the run configuration, the match rule its
// findings were scored by (MatchRule), per-case rows, the corpus-wide
// aggregate, the recall credited to each pass, and, when the run was compared
// against a baseline, the comparison.
type Report struct {
	Config
	Match      string       `json:"match"`
	Cases      []CaseScore  `json:"cases"`
	Aggregate  CaseScore    `json:"aggregate"`
	Passes     []PassRecall `json:"recall_by_pass"`
	Comparison *Comparison  `json:"comparison,omitempty"`
}

// BuildReport turns scored cases into the renderable report, computing the
// aggregate by summing the raw tallies (not by averaging per-case ratios, which
// would double-weight small cases) and the usage of every case.
func BuildReport(scored []Scored, cfg Config) Report {
	rep := Report{Config: cfg, Match: MatchRule}
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
	if v, ok := s.F1(); ok {
		cs.F1 = &v
	}
	if v, ok := s.SeverityAccuracy(); ok {
		cs.SeverityAccuracy = &v
	}
	return cs
}

// RenderTable writes the report as an aligned text table. Undefined ratios show
// as "n/a"; a clean case's recall and F1 are always undefined, as the footer
// notes. The table is followed by the run configuration and its tiers, the
// recall by pass, and the cost per run, where every value is a total divided by
// the runs.
func RenderTable(w io.Writer, rep Report) {
	const header = "%-22s  %5s  %3s  %3s  %3s  %9s  %7s  %4s  %8s\n"
	const row = "%-22s  %5s  %3d  %3d  %3d  %9s  %7s  %4s  %8s\n"
	head := fmt.Sprintf(header, "CASE", "CLEAN", "TP", "FP", "FN", "PRECISION", "RECALL", "F1", "SEV-ACC")
	_, _ = io.WriteString(w, head)
	for _, c := range rep.Cases {
		writeRow(w, row, c)
	}
	// The rule is as wide as the header row, its newline aside.
	_, _ = fmt.Fprintln(w, strings.Repeat("-", len(head)-1))
	writeRow(w, row, rep.Aggregate)
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintln(w, "PRECISION = TP/(TP+FP), RECALL = TP/(TP+FN), SEV-ACC = severity matches/TP.")
	_, _ = fmt.Fprintln(w, "F1 = 2TP/(2TP+FP+FN), the harmonic mean of PRECISION and RECALL.")
	_, _ = fmt.Fprintln(w, "A clean case seeds no bug: recall and F1 are undefined (n/a) and every finding is a false positive.")

	_, _ = fmt.Fprintln(w)
	scope := ""
	if rep.ScopePatterns {
		scope = ", scope-patterns: yes"
	}
	_, _ = fmt.Fprintf(w, "RUNS: %d per case (thorough: %s, specialists: %s%s)\n",
		rep.Runs, yesNo(rep.Thorough), yesNo(rep.Specialists), scope)
	_, _ = fmt.Fprintf(w, "TIERS: %s\n", rep.Tiers)
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
	const row = "%-*s  %6.1f  %13d  %13d  %8s\n"
	runs := float64(max(rep.Runs, 1))
	costRow := func(name string, calls int, prompt, output int64, usd float64) {
		c := perRun(prompt, output, usd, rep.Runs)
		_, _ = fmt.Fprintf(w, row, labelWidth, truncate(name, labelWidth), float64(calls)/runs,
			c.PromptTokens, c.OutputTokens, fmt.Sprintf("$%.2f", c.CostUSD))
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
		pct(c.Precision), pct(c.Recall), pct(c.F1), pct(c.SeverityAccuracy))
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// notApplicable marks an undefined ratio or a change with no base in the
// rendered report.
const notApplicable = "n/a"

// unrecorded names a value that a report written before the value was recorded
// decodes to empty: the tiers in the rendered report, the match rule in
// Comparable's error.
const unrecorded = "(unrecorded)"

// pct formats an optional ratio as a percentage, or "n/a" when undefined.
func pct(v *float64) string {
	if v == nil {
		return notApplicable
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
