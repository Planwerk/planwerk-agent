package fix

import "strings"

// Status is the machine-readable terminal status a fix or implement session
// reports so the orchestrator can react deterministically instead of parsing
// prose. It mirrors the subagent status protocol from the superpowers
// reference.
type Status string

const (
	// StatusDone means the session finished the work and verified it.
	StatusDone Status = "DONE"
	// StatusDoneWithConcerns means the work was pushed but the session flagged
	// reservations worth a human's attention.
	StatusDoneWithConcerns Status = "DONE_WITH_CONCERNS"
	// StatusBlocked means the session could not make progress (e.g. an infra
	// flake or an undiagnosable failure) and should not be retried as-is.
	StatusBlocked Status = "BLOCKED"
	// StatusNeedsContext means the session lacks information it cannot recover
	// on its own and a human must supply it.
	StatusNeedsContext Status = "NEEDS_CONTEXT"
	// StatusUnknown means no status line was found in the report.
	StatusUnknown Status = ""
)

// ShouldEscalate reports whether the status means the loop must stop and hand
// off to a human rather than spend another iteration.
func (s Status) ShouldEscalate() bool {
	return s == StatusBlocked || s == StatusNeedsContext
}

// parseStatus scans a fix/implement report for a "STATUS: <value>" line and
// returns the recognized terminal status, tolerating markdown decoration
// (bold, list markers, headings). It returns StatusUnknown when no recognized
// status line is present.
func parseStatus(report string) Status {
	for _, raw := range strings.Split(report, "\n") {
		line := strings.TrimLeft(strings.TrimSpace(raw), "-*#> \t")
		if !strings.HasPrefix(strings.ToUpper(line), "STATUS:") {
			continue
		}
		val := strings.Trim(strings.TrimSpace(line[len("STATUS:"):]), "*`_ \t")
		switch Status(strings.ToUpper(val)) {
		case StatusDone:
			return StatusDone
		case StatusDoneWithConcerns:
			return StatusDoneWithConcerns
		case StatusBlocked:
			return StatusBlocked
		case StatusNeedsContext:
			return StatusNeedsContext
		}
	}
	return StatusUnknown
}
