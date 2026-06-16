package address

import "strings"

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
