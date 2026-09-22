package address

import (
	"testing"

	"github.com/planwerk/planwerk-agent/internal/report"
)

// TestOverallStatus_TakesTheMostSevere: a thread marked NEEDS_CONTEXT under a
// top-level DONE, or a result with no top-level status, still escalates.
func TestOverallStatus_TakesTheMostSevere(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   report.AddressResult
		want Status
	}{
		{"clean", report.AddressResult{Status: "DONE", Threads: []report.AddressedThread{{Status: "DONE"}}}, StatusDone},
		{"thread outranks top level", report.AddressResult{Status: "DONE", Threads: []report.AddressedThread{{Status: "DONE"}, {Status: "NEEDS_CONTEXT"}}}, StatusNeedsContext},
		{"missing top level", report.AddressResult{Threads: []report.AddressedThread{{Status: "blocked"}}}, StatusBlocked},
		{"nothing known", report.AddressResult{}, StatusUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := overallStatus(&tc.in); got != tc.want {
				t.Errorf("overallStatus = %q, want %q", got, tc.want)
			}
		})
	}
}
