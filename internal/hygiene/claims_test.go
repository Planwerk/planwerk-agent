package hygiene

import (
	"errors"
	"testing"

	"github.com/planwerk/planwerk-agent/internal/report"
)

// blockingAndWarning is the finding set the claim-verification tests run
// against: one BLOCKING finding (which the pass sends) and one WARNING (which
// it must not), so a Sent=1 count also proves the severity gate held.
func blockingAndWarning() *report.ReviewResult {
	return &report.ReviewResult{Findings: []report.Finding{
		{Severity: report.SeverityBlocking, Title: "nil deref", File: "a.go", Line: 4},
		{Severity: report.SeverityWarning, Title: "naming", File: "b.go", Line: 9},
	}}
}

// claimStats returns the recorded claim-gate stats, failing the test if the gate
// left no record (a nil Gates.Claim means the gate never ran).
func claimStats(t *testing.T, result *report.ReviewResult) report.ClaimGateStats {
	t.Helper()
	if result.Gates == nil || result.Gates.Claim == nil {
		t.Fatalf("claim gate stats not recorded: %+v", result.Gates)
	}
	return *result.Gates.Claim
}

// TestVerifyClaims_CountRefutations pins the demotion path: a refuted verdict
// demotes its finding, stamps it, and is counted, so the ratio the records carry
// is the real one.
func TestVerifyClaims_CountRefutations(t *testing.T) {
	verify := func(_ string, findings []report.Finding) ([]ClaimVerdict, error) {
		if len(findings) != 1 {
			t.Fatalf("sent %d findings, want 1 (WARNING must not be sent)", len(findings))
		}
		return []ClaimVerdict{{Index: 0, Verdict: "refuted", Evidence: "a.go:4", Reason: "guarded above"}}, nil
	}

	result := blockingAndWarning()
	VerifyClaims(result, t.TempDir(), verify)

	if want := (report.ClaimGateStats{Sent: 1, Verdicts: 1, Refuted: 1}); claimStats(t, result) != want {
		t.Errorf("stats = %+v, want %+v", claimStats(t, result), want)
	}
	if result.Findings[0].Confidence != report.ConfidenceUncertain {
		t.Errorf("refuted finding confidence = %q, want uncertain", result.Findings[0].Confidence)
	}
	if result.Findings[0].ClaimCheck != report.ClaimCheckRefuted {
		t.Errorf("claim check = %q, want refuted", result.Findings[0].ClaimCheck)
	}
	// The human-readable reason still lives in VerificationNote; the token does
	// not duplicate or replace it.
	if result.Findings[0].VerificationNote != "refuted: guarded above" {
		t.Errorf("verification note = %q, want the refutation reason", result.Findings[0].VerificationNote)
	}
	if result.Findings[1].ClaimCheck != "" || result.Findings[1].Confidence == report.ConfidenceUncertain {
		t.Errorf("WARNING finding must be neither sent nor stamped, got check=%q confidence=%q",
			result.Findings[1].ClaimCheck, result.Findings[1].Confidence)
	}
}

// TestVerifyClaims_CountAgreement is the no-op signal this instrumentation
// exists for: a pass that confirms everything records Refuted=0 with Sent>0 and
// stamps the finding confirmed — the previously unrecordable case. Zero
// refutations across many real runs means the verifier is agreeing rather than
// verifying, and the pass has stopped earning its tokens.
func TestVerifyClaims_CountAgreement(t *testing.T) {
	verify := func(_ string, _ []report.Finding) ([]ClaimVerdict, error) {
		return []ClaimVerdict{{Index: 0, Verdict: "confirmed", Evidence: "a.go:4"}}, nil
	}

	result := blockingAndWarning()
	VerifyClaims(result, t.TempDir(), verify)

	if want := (report.ClaimGateStats{Sent: 1, Verdicts: 1, Refuted: 0}); claimStats(t, result) != want {
		t.Errorf("stats = %+v, want %+v", claimStats(t, result), want)
	}
	if result.Findings[0].ClaimCheck != report.ClaimCheckConfirmed {
		t.Errorf("claim check = %q, want confirmed", result.Findings[0].ClaimCheck)
	}
	if result.Findings[0].Confidence == report.ConfidenceUncertain {
		t.Error("confirmed finding was demoted")
	}
}

// TestVerifyClaims_OnFailure keeps the fail-open path honest: a failed call
// still records what it sent and leaves the finding stamped no-verdict, so a run
// whose verifier never answered cannot be mistaken for one that verified and
// found nothing.
func TestVerifyClaims_OnFailure(t *testing.T) {
	verify := func(_ string, _ []report.Finding) ([]ClaimVerdict, error) {
		return nil, errors.New("claude exploded")
	}

	result := blockingAndWarning()
	VerifyClaims(result, t.TempDir(), verify)

	if want := (report.ClaimGateStats{Sent: 1}); claimStats(t, result) != want {
		t.Errorf("stats = %+v, want %+v", claimStats(t, result), want)
	}
	if result.Findings[0].ClaimCheck != report.ClaimCheckNoVerdict {
		t.Errorf("failed-call finding must stay no-verdict, got %q", result.Findings[0].ClaimCheck)
	}
}

// TestVerifyClaims_NoSelectedFindings covers the empty branch: with nothing
// severe enough to verify, the pass makes no call but still records that it ran
// (Sent=0) — distinct from the nil Gates.Claim of a pass that never ran.
func TestVerifyClaims_NoSelectedFindings(t *testing.T) {
	verify := func(_ string, _ []report.Finding) ([]ClaimVerdict, error) {
		t.Fatal("verifier called with no BLOCKING/CRITICAL finding")
		return nil, nil
	}

	result := &report.ReviewResult{Findings: []report.Finding{
		{Severity: report.SeverityWarning, Title: "naming", File: "b.go", Line: 9},
	}}
	VerifyClaims(result, t.TempDir(), verify)
	if got := claimStats(t, result); got != (report.ClaimGateStats{}) {
		t.Errorf("stats = %+v, want zero (ran, nothing eligible)", got)
	}
}

// TestVerifyClaims_NilFn documents the no-op: a nil verify seam skips the pass
// entirely — no record (nil Gates), no demotion, no stamp.
func TestVerifyClaims_NilFn(t *testing.T) {
	result := blockingAndWarning()
	VerifyClaims(result, t.TempDir(), nil)
	if result.Gates != nil {
		t.Errorf("a nil verify fn must not record stats, got %+v", result.Gates)
	}
	if result.Findings[0].Confidence == report.ConfidenceUncertain || result.Findings[0].ClaimCheck != "" {
		t.Error("a nil verify fn must not demote or stamp any finding")
	}
}

// TestVerifyClaims_OutOfRangeIndexIgnored keeps the fail-open contract: a
// verdict whose index is outside the sent batch is ignored rather than panics or
// demotes the wrong finding, and the raw verdict count is still recorded.
func TestVerifyClaims_OutOfRangeIndexIgnored(t *testing.T) {
	verify := func(_ string, _ []report.Finding) ([]ClaimVerdict, error) {
		return []ClaimVerdict{{Index: 7, Verdict: "refuted", Reason: "nonsense"}}, nil
	}
	result := blockingAndWarning()
	VerifyClaims(result, t.TempDir(), verify)
	if want := (report.ClaimGateStats{Sent: 1, Verdicts: 1, Refuted: 0}); claimStats(t, result) != want {
		t.Errorf("stats = %+v, want %+v", claimStats(t, result), want)
	}
	if result.Findings[0].Confidence == report.ConfidenceUncertain {
		t.Error("BLOCKING finding demoted by an out-of-range verdict")
	}
	if result.Findings[0].ClaimCheck != report.ClaimCheckNoVerdict {
		t.Errorf("finding uncovered by any valid verdict must stay no-verdict, got %q", result.Findings[0].ClaimCheck)
	}
}

// TestVerifyClaims_PartialVerdictsLeaveNoVerdict covers a batch the verifier
// only partly answered: the covered finding is stamped, the uncovered one keeps
// its no-verdict record so the gap is visible rather than silently confirmed.
func TestVerifyClaims_PartialVerdictsLeaveNoVerdict(t *testing.T) {
	verify := func(_ string, _ []report.Finding) ([]ClaimVerdict, error) {
		return []ClaimVerdict{{Index: 0, Verdict: "confirmed"}}, nil
	}
	result := &report.ReviewResult{Findings: []report.Finding{
		{Severity: report.SeverityBlocking, Title: "one", File: "a.go", Line: 1},
		{Severity: report.SeverityCritical, Title: "two", File: "b.go", Line: 2},
	}}
	VerifyClaims(result, t.TempDir(), verify)
	if want := (report.ClaimGateStats{Sent: 2, Verdicts: 1, Refuted: 0}); claimStats(t, result) != want {
		t.Errorf("stats = %+v, want %+v", claimStats(t, result), want)
	}
	if result.Findings[0].ClaimCheck != report.ClaimCheckConfirmed {
		t.Errorf("covered finding = %q, want confirmed", result.Findings[0].ClaimCheck)
	}
	if result.Findings[1].ClaimCheck != report.ClaimCheckNoVerdict {
		t.Errorf("uncovered finding = %q, want no-verdict", result.Findings[1].ClaimCheck)
	}
}
