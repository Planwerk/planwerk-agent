package eval

import (
	"errors"
	"maps"
	"slices"
	"testing"

	"github.com/planwerk/planwerk-agent/internal/report"
)

// judgeStub returns a SameIntentFunc that records the title of every
// prediction it is asked about and answers same for a title in same.
func judgeStub(calls *[]string, same ...string) SameIntentFunc {
	return func(pred report.Finding, exp ExpectedFinding) (bool, error) {
		*calls = append(*calls, pred.Title)
		return slices.Contains(same, pred.Title), nil
	}
}

func TestJudgeRun(t *testing.T) {
	c := aacrCase(refComment("store.go", 40, 44), refComment("store.go", 60, 61))
	preds := []report.Finding{
		pf("store.go", 42, report.SeverityCritical, "race", "the map write races"),
		pf("cache.go", 42, report.SeverityWarning, "elsewhere", "another file"),
		pfSpan("store.go", 44, 60, report.SeverityInfo, "wide", "spans both comments"),
		pf("store.go", 50, report.SeverityInfo, "between", "overlaps neither"),
	}

	t.Run("only overlapping pairs are judged, and the answers become the verdicts", func(t *testing.T) {
		var calls []string
		r := Run{Result: report.ReviewResult{Findings: preds}}
		tally, err := JudgeRun(c, &r, judgeStub(&calls, "race"))
		if err != nil {
			t.Fatalf("JudgeRun: %v", err)
		}
		if want := []string{"race", "wide", "wide"}; !slices.Equal(calls, want) {
			t.Errorf("judged %v, want %v in index order", calls, want)
		}
		if tally != (JudgeTally{Pairs: 3, Same: 1}) {
			t.Errorf("tally = %+v, want 3 pairs, 1 same", tally)
		}
		want := map[PairKey]bool{{Pred: 0, Exp: 0}: true, {Pred: 2, Exp: 0}: false, {Pred: 2, Exp: 1}: false}
		if !maps.Equal(r.Verdicts, want) {
			t.Errorf("Verdicts = %v, want %v", r.Verdicts, want)
		}
		if s := ScoreRun(c, r); s.TP != 1 || s.FN != 1 || s.FP != 3 {
			t.Errorf("judged score = {TP:%d FP:%d FN:%d}, want {1 3 1}", s.TP, s.FP, s.FN)
		}
	})

	t.Run("a run with no overlapping pair gets an empty, non-nil verdict map", func(t *testing.T) {
		var calls []string
		r := Run{Result: report.ReviewResult{Findings: preds[1:2]}}
		tally, err := JudgeRun(c, &r, judgeStub(&calls))
		if err != nil || tally != (JudgeTally{}) || len(calls) != 0 {
			t.Fatalf("JudgeRun = %+v, %v after %d calls, want a zero tally and no call", tally, err, len(calls))
		}
		if r.Verdicts == nil || len(r.Verdicts) != 0 {
			t.Errorf("Verdicts = %#v, want an empty map", r.Verdicts)
		}
	})

	t.Run("a judge error is returned and leaves the run unjudged", func(t *testing.T) {
		errJudge := errors.New("structure tier timed out")
		calls := 0
		r := Run{Result: report.ReviewResult{Findings: preds}}
		_, err := JudgeRun(c, &r, func(report.Finding, ExpectedFinding) (bool, error) {
			if calls++; calls == 2 {
				return false, errJudge
			}
			return true, nil
		})
		if !errors.Is(err, errJudge) {
			t.Errorf("err = %v, want the judge's error", err)
		}
		if r.Verdicts != nil {
			t.Errorf("Verdicts = %v, want nil so a retry starts clean", r.Verdicts)
		}
	})

	t.Run("a corpus case makes no call", func(t *testing.T) {
		var calls []string
		r := Run{Result: report.ReviewResult{Findings: preds}}
		tally, err := JudgeRun(caseWith(false, ef("store.go", 42, "CRITICAL", "race")), &r, judgeStub(&calls))
		if err != nil || tally != (JudgeTally{}) || len(calls) != 0 || r.Verdicts != nil {
			t.Errorf("JudgeRun = %+v, %v after %d calls with Verdicts %v, want nothing judged", tally, err, len(calls), r.Verdicts)
		}
	})
}
