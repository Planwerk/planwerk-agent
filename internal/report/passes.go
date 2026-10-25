package report

import (
	"strings"
	"unicode/utf8"

	"github.com/planwerk/planwerk-agent/internal/redact"
)

// PassStatus records how one secondary pass a review requested ended. Pass is
// the session label the run records the pass's usage under (PassUsage.Pass:
// "adversarial", "coverage", "compliance", "specialist-security"), so a failed
// pass's cost and its outcome read under one name; it is not the "specialist:"
// provenance label findings carry in ConfirmedBy. Status is PassStatusRan,
// PassStatusGatedOut or PassStatusFailed, and Error is set for a failed pass
// only, by NewPassStatus (decision 135).
type PassStatus struct {
	Pass   string `json:"pass"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

// PassStatus.Status values. A pass that ran contributed its findings, possibly
// none. A gated-out pass is a specialist adaptive gating skipped because the
// diff touches none of its paths. A failed pass errored and contributed nothing.
const (
	PassStatusRan      = "ran"
	PassStatusGatedOut = "gated-out"
	PassStatusFailed   = "failed"
)

// maxPassErrorLen caps PassStatus.Error in bytes, the ellipsis included: the
// error is rendered in a PR comment every reader of the repository sees, while
// the full error stays in the log.
const maxPassErrorLen = 200

// passErrorEllipsis ends an error message shortPassError cut short.
const passErrorEllipsis = "…"

// passErrorFallback is the Error of a failed pass whose error has no text.
const passErrorFallback = "failed without an error message"

// NewPassStatus returns the status of a pass that returned err: ran for a nil
// err, otherwise failed with the error's first line, secrets redacted, cut to
// maxPassErrorLen bytes on a rune boundary.
func NewPassStatus(pass string, err error) PassStatus {
	if err == nil {
		return PassStatus{Pass: pass, Status: PassStatusRan}
	}
	return PassStatus{Pass: pass, Status: PassStatusFailed, Error: shortPassError(err)}
}

// shortPassError keeps the first line of err's message: a `claude -p` failure
// appends the session's stderr and the path of the saved raw output on later
// lines, which belong in the log. It redacts before it truncates, so a secret
// is never cut in half and half kept.
func shortPassError(err error) string {
	msg, _, _ := strings.Cut(err.Error(), "\n")
	msg = redact.Redact(strings.TrimSpace(msg)).Text
	if msg == "" {
		return passErrorFallback
	}
	if len(msg) <= maxPassErrorLen {
		return msg
	}
	cut := maxPassErrorLen - len(passErrorEllipsis)
	for cut > 0 && !utf8.RuneStart(msg[cut]) {
		cut--
	}
	return strings.TrimRight(msg[:cut], " ") + passErrorEllipsis
}

// FailedPasses returns the failed entries of r.Passes in recorded order, or nil
// when no pass failed. A result with a failed entry is never cached
// (decision 135).
func (r *ReviewResult) FailedPasses() []PassStatus {
	var failed []PassStatus
	for _, p := range r.Passes {
		if p.Status == PassStatusFailed {
			failed = append(failed, p)
		}
	}
	return failed
}
