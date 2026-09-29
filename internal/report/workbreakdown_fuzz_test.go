package report

import (
	"slices"
	"testing"
)

func FuzzWorkBreakdownStates(f *testing.F) {
	f.Add(coverageReport("- 1. Parser — done — abc1234\n- 2. Runner — partial — def5678\n- 3. Docs — not started — nothing yet\n"))
	f.Add(coverageReport("- **WP1: Foo** — **done** — abc1234\n- 2. Bar - Done - def5678\n- Baz – not started – (nothing yet)\n"))
	f.Add(coverageReport("- None — the issue is a single undivided change\n"))
	f.Add(coverageReport("- (List EVERY work package.)\n  - A — done\n"))
	f.Add("### Work Breakdown Coverage\n")
	f.Add("### Work Breakdown Coverage\n- ")
	f.Add("### Work Breakdown Coverage\n- none")
	f.Add("")

	f.Fuzz(func(t *testing.T, in string) {
		states := WorkBreakdownStates(in)
		if states != nil && len(states) == 0 {
			t.Fatalf("non-nil empty states for %q; no entries must read as nil", in)
		}
		for _, s := range states {
			if !slices.Contains([]string{WorkPackageDone, WorkPackagePartial, WorkPackageNotStarted, ""}, s) {
				t.Fatalf("state %q for %q is outside the vocabulary", s, in)
			}
		}
		allDone := len(states) > 0 && !slices.ContainsFunc(states, func(s string) bool { return s != WorkPackageDone })
		if got := WorkBreakdownComplete(in); got != allDone {
			t.Fatalf("WorkBreakdownComplete(%q) = %v, want %v for states %v", in, got, allDone, states)
		}
	})
}
