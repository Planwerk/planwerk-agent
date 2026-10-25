package eval

import (
	"fmt"

	"github.com/planwerk/planwerk-agent/internal/report"
)

// SameIntentFunc reports whether a predicted finding raises the same concern
// as the reference comment of exp (ExpectedFinding.Comment). planwerk-eval
// wires it to claude.Client.JudgeSameIntent.
type SameIntentFunc func(pred report.Finding, exp ExpectedFinding) (bool, error)

// PairKey names one pair of a run by the prediction's index into the run's
// findings and the expected finding's index into the case's.
type PairKey struct{ Pred, Exp int }

// JudgeTally counts the pairs JudgeRun judged and those judged the same
// concern.
type JudgeTally struct{ Pairs, Same int }

// JudgeRun asks judge about every pair of an AACR-Bench run whose line ranges
// overlap (overlapsRange), in index order, and stores the answers in
// r.Verdicts, which ScoreRun reads: a pair then matches only when the judge
// called it the same concern (decision 147). Verdicts is assigned only once
// every call succeeded, and is never nil then, even with no pair to judge, so
// a judged run is never scored as a line-only one. On the first error JudgeRun
// returns it and leaves r untouched, so a retry starts clean. A corpus case
// is not judged: JudgeRun makes no call and returns a zero tally.
func JudgeRun(c Case, r *Run, judge SameIntentFunc) (JudgeTally, error) {
	if c.AACR == nil {
		return JudgeTally{}, nil
	}
	var t JudgeTally
	verdicts := make(map[PairKey]bool)
	for i, pred := range r.Result.Findings {
		for e, exp := range c.Expected.Findings {
			if !overlapsRange(pred, exp) {
				continue
			}
			same, err := judge(pred, exp)
			if err != nil {
				return JudgeTally{}, fmt.Errorf("case %s: judging finding %d against reference comment %d: %w", c.Name, i, e, err)
			}
			verdicts[PairKey{Pred: i, Exp: e}] = same
			t.Pairs++
			if same {
				t.Same++
			}
		}
	}
	r.Verdicts = verdicts
	return t, nil
}
