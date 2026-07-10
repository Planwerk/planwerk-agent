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

// ClaimStats records what one claim-verification pass did: how many findings it
// sent, how many verdicts came back, and how many of those refuted a finding.
// The refuted/sent ratio is the pass's own no-op signal. The verifier is asked
// to confirm unless it finds quoted counter-evidence, and it is handed the
// finding's claim along with the code — two nudges toward agreement — so a pass
// that refutes nothing across many runs is not verifying, it is agreeing, and
// belongs sharpened or deleted rather than left to look productive. The counts
// are logged on every run so that case is visible instead of silent.
type ClaimStats struct {
	Sent     int
	Verdicts int
	Refuted  int
}

// VerifyClaims re-checks each BLOCKING/CRITICAL finding's claim against the
// checkout at dir. It batches every such finding into one verify call; for each
// verdict the verifier refutes it demotes the finding to uncertain confidence
// and attaches the refutation as a VerificationNote (which routes it to the
// Unverified section). WARNING/INFO findings are never sent — the snippet gate
// already covers them and verifying them is not worth the cost. The pass is
// fail-open: a nil verify, a failed call, a missing verdict, or an out-of-range
// index leaves the finding unchanged. The returned stats are the pass's own
// measurement; the caller needs no other use for them.
func VerifyClaims(result *report.ReviewResult, dir string, verify VerifyClaimsFn) ClaimStats {
	if result == nil || verify == nil {
		return ClaimStats{}
	}
	var selectedIdx []int
	var selected []report.Finding
	for i := range result.Findings {
		if sev := result.Findings[i].Severity; sev == report.SeverityBlocking || sev == report.SeverityCritical {
			selectedIdx = append(selectedIdx, i)
			selected = append(selected, result.Findings[i])
		}
	}
	if len(selected) == 0 {
		return ClaimStats{}
	}
	verdicts, err := verify(dir, selected)
	if err != nil {
		slog.Warn("claim verification failed; publishing findings unchanged", "err", err)
		return ClaimStats{Sent: len(selected)}
	}
	demoted := 0
	for _, v := range verdicts {
		if v.Index < 0 || v.Index >= len(selectedIdx) {
			continue // ignore an out-of-range index the model may return
		}
		if !strings.EqualFold(strings.TrimSpace(v.Verdict), "refuted") {
			continue
		}
		reason := strings.TrimSpace(v.Reason)
		if reason == "" {
			reason = strings.TrimSpace(v.Evidence)
		}
		if reason == "" {
			reason = "no supporting evidence found in the checkout"
		}
		fi := selectedIdx[v.Index]
		result.Findings[fi].Confidence = report.ConfidenceUncertain
		result.Findings[fi].VerificationNote = "refuted: " + reason
		demoted++
	}
	stats := ClaimStats{Sent: len(selected), Verdicts: len(verdicts), Refuted: demoted}
	slog.Info("claim verification complete",
		"sent", stats.Sent, "verdicts", stats.Verdicts, "refuted", stats.Refuted)
	return stats
}
