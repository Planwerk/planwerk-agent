package hygiene

import (
	"log/slog"
	"strings"

	"github.com/planwerk/planwerk-agent/internal/report"
)

// ClaimVerdict is one entry of the claim-verification pass's output: the model's
// judgment of whether a single finding's CLAIM holds against the checkout.
// Index refers to the finding's position in the batch VerifyClaimsFn was given.
// Verdict is "confirmed" or "refuted"; Evidence quotes the file:line the model
// grounded its judgment in; Reason explains a refutation.
type ClaimVerdict struct {
	Index    int    `json:"index"`
	Verdict  string `json:"verdict"`
	Evidence string `json:"evidence"`
	Reason   string `json:"reason"`
}

// VerifyClaimsFn re-checks each finding's CLAIM against the checkout at dir,
// returning one verdict per finding it judged (keyed by index into the batch).
// In production it is backed by claude.Client.VerifyFindingClaims; a nil
// VerifyClaimsFn makes VerifyClaims a no-op.
type VerifyClaimsFn func(dir string, findings []report.Finding) ([]ClaimVerdict, error)

// VerifyClaims re-checks each BLOCKING/CRITICAL finding's claim against the
// checkout at dir. It batches every such finding into one verify call; for each
// verdict the verifier refutes it demotes the finding to uncertain confidence
// and attaches the refutation as a VerificationNote (which routes it to the
// Unverified section). WARNING/INFO findings are never sent — the snippet gate
// already covers them and verifying them is not worth the cost. The pass is
// fail-open: a nil verify, a failed call, a missing verdict, or an out-of-range
// index leaves the finding's confidence unchanged.
//
// It records what it did on the result rather than returning it: every sent
// finding is stamped with a per-finding ClaimCheck token (confirmed, refuted,
// or no-verdict for the fail-open cases), and the run-level counts land in
// result.Gates.Claim. A nil Gates.Claim means the gate never ran; the counts
// there — and the refuted/sent ratio they carry — are the pass's own no-op
// signal (see report.ClaimGateStats), now surviving in the cached result and
// the data block instead of dying with the log line.
func VerifyClaims(result *report.ReviewResult, dir string, verify VerifyClaimsFn) {
	if result == nil || verify == nil {
		return
	}
	var selectedIdx []int
	var selected []report.Finding
	for i := range result.Findings {
		if sev := result.Findings[i].Severity; sev == report.SeverityBlocking || sev == report.SeverityCritical {
			selectedIdx = append(selectedIdx, i)
			selected = append(selected, result.Findings[i])
		}
	}
	// Record that the gate ran even when nothing was eligible: a nil Gates.Claim
	// means "never ran", Sent=0 means "ran, nothing severe enough to verify".
	stats := &report.ClaimGateStats{Sent: len(selected)}
	ensureGates(result).Claim = stats
	if len(selected) == 0 {
		return
	}
	// Stamp every sent finding no-verdict up front; a verdict overwrites it, and
	// the fail-open paths (a failed call, a missing verdict) leave it as the
	// record that the gate ran but returned nothing for this finding.
	for _, fi := range selectedIdx {
		result.Findings[fi].ClaimCheck = report.ClaimCheckNoVerdict
	}
	verdicts, err := verify(dir, selected)
	if err != nil {
		slog.Warn("claim verification failed; publishing findings unchanged", "err", err)
		return
	}
	stats.Verdicts = len(verdicts) // raw count, out-of-range verdicts included
	demoted := 0
	for _, v := range verdicts {
		if v.Index < 0 || v.Index >= len(selectedIdx) {
			continue // ignore an out-of-range index the model may return
		}
		fi := selectedIdx[v.Index]
		if !strings.EqualFold(strings.TrimSpace(v.Verdict), "refuted") {
			// The gate's decision is binary; an off-enum verdict that lets the
			// finding through records as a confirmation.
			result.Findings[fi].ClaimCheck = report.ClaimCheckConfirmed
			continue
		}
		reason := strings.TrimSpace(v.Reason)
		if reason == "" {
			reason = strings.TrimSpace(v.Evidence)
		}
		if reason == "" {
			reason = "no supporting evidence found in the checkout"
		}
		result.Findings[fi].Confidence = report.ConfidenceUncertain
		result.Findings[fi].VerificationNote = "refuted: " + reason
		result.Findings[fi].ClaimCheck = report.ClaimCheckRefuted
		demoted++
	}
	stats.Refuted = demoted
	slog.Info("claim verification complete",
		"sent", stats.Sent, "verdicts", stats.Verdicts, "refuted", stats.Refuted)
}
