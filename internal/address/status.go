package address

import (
	"strings"

	"github.com/planwerk/planwerk-agent/internal/report"
)

// Status is the machine-readable terminal status an address session reports in
// its structured output so the orchestrator can react deterministically. It
// mirrors the fix/implement status protocol.
type Status string

const (
	// StatusDone means the session addressed the work and verified it.
	StatusDone Status = "DONE"
	// StatusDoneWithConcerns means the work was committed but the session
	// flagged reservations worth a human's attention.
	StatusDoneWithConcerns Status = "DONE_WITH_CONCERNS"
	// StatusBlocked means the session could not make progress and should not be
	// retried as-is.
	StatusBlocked Status = "BLOCKED"
	// StatusNeedsContext means the session lacks information only a human can
	// supply.
	StatusNeedsContext Status = "NEEDS_CONTEXT"
	// StatusUnknown means the status field was empty or unrecognized.
	StatusUnknown Status = ""
)

// parseStatus normalizes a raw status string from the structured address output
// into a recognized Status, tolerating surrounding whitespace and case. It
// returns StatusUnknown for an empty or unrecognized value.
func parseStatus(raw string) Status {
	switch Status(strings.ToUpper(strings.TrimSpace(raw))) {
	case StatusDone:
		return StatusDone
	case StatusDoneWithConcerns:
		return StatusDoneWithConcerns
	case StatusBlocked:
		return StatusBlocked
	case StatusNeedsContext:
		return StatusNeedsContext
	}
	return StatusUnknown
}

// ShouldEscalate reports whether the status means the run must stop and hand off
// to a human rather than continue addressing more threads.
func (s Status) ShouldEscalate() bool {
	return s == StatusBlocked || s == StatusNeedsContext
}

// addressed reports whether the per-thread status means the thread's change was
// committed — and is therefore eligible for a reply and (under --resolve) for
// being marked resolved.
func (s Status) addressed() bool {
	return s == StatusDone || s == StatusDoneWithConcerns
}

// severity orders the statuses for overallStatus: a human-facing stop outranks
// a reservation, which outranks a clean result.
func (s Status) severity() int {
	switch s {
	case StatusBlocked:
		return 4
	case StatusNeedsContext:
		return 3
	case StatusDoneWithConcerns:
		return 2
	case StatusDone:
		return 1
	}
	return 0
}

// overallStatus is the run status the orchestrator acts on: the most severe of
// the session's top-level status and every per-thread status. Reading only the
// top-level field let a session that marked one thread NEEDS_CONTEXT but wrote
// DONE at the top, or wrote no top-level status at all, pass as a clean run.
func overallStatus(result *report.AddressResult) Status {
	if result == nil {
		return StatusUnknown
	}
	overall := parseStatus(result.Status)
	for _, t := range result.Threads {
		if st := parseStatus(t.Status); st.severity() > overall.severity() {
			overall = st
		}
	}
	return overall
}
