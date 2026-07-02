package eval

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

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
}

// Scored pairs a case with the score computed for its review result.
type Scored struct {
	Case  Case
	Score Score
}

// ScoreCase compares the predicted findings against the case's expected findings
// and returns the tally. Matching is greedy one-to-one from the expected side:
// each expected finding claims the first not-yet-claimed prediction that matches
// it (same file, line within tolerance, keyword present). Unclaimed predictions
// are false positives; unclaimed expected findings are false negatives.
func ScoreCase(c Case, result report.ReviewResult) Score {
	preds := result.Findings
	claimed := make([]bool, len(preds))
	s := Score{Clean: c.Expected.Clean}

	for _, exp := range c.Expected.Findings {
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
// score. Undefined ratios serialize as null and render as "n/a".
type CaseScore struct {
	Name             string   `json:"name"`
	Description      string   `json:"description,omitempty"`
	Clean            bool     `json:"clean"`
	TP               int      `json:"tp"`
	FP               int      `json:"fp"`
	FN               int      `json:"fn"`
	SeverityMatches  int      `json:"severity_matches"`
	Precision        *float64 `json:"precision"`
	Recall           *float64 `json:"recall"`
	SeverityAccuracy *float64 `json:"severity_accuracy"`
}

// Report is the full scored corpus: per-case rows plus the corpus-wide aggregate.
type Report struct {
	Cases     []CaseScore `json:"cases"`
	Aggregate CaseScore   `json:"aggregate"`
}

// BuildReport turns scored cases into the renderable report, computing the
// aggregate by summing the raw tallies (not by averaging per-case ratios, which
// would double-weight small cases).
func BuildReport(scored []Scored) Report {
	var rep Report
	var agg Score
	for _, sc := range scored {
		rep.Cases = append(rep.Cases, toCaseScore(sc.Case.Name, sc.Case.Expected.Description, sc.Score))
		agg.Add(sc.Score)
	}
	rep.Aggregate = toCaseScore("AGGREGATE", "", agg)
	return rep
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
// footer.
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
}

func writeRow(w io.Writer, format string, c CaseScore) {
	clean := "no"
	if c.Clean {
		clean = "yes"
	}
	_, _ = fmt.Fprintf(w, format, truncate(c.Name, 22), clean, c.TP, c.FP, c.FN,
		pct(c.Precision), pct(c.Recall), pct(c.SeverityAccuracy))
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
