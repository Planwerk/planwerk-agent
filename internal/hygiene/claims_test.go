package hygiene

import (
	"errors"
	"testing"

	"github.com/planwerk/planwerk-agent/internal/report"
)

// blockingAndWarning is the finding set the claim-verification tests run
// against: one BLOCKING finding (which the pass sends) and one WARNING (which
// it must not), so a stats count of Sent=1 also proves the severity gate held.
func blockingAndWarning() *report.ReviewResult {
	return &report.ReviewResult{Findings: []report.Finding{
		{Severity: report.SeverityBlocking, Title: "nil deref", File: "a.go", Line: 4},
		{Severity: report.SeverityWarning, Title: "naming", File: "b.go", Line: 9},
	}}
}

// TestVerifyClaims_CountRefutations pins the demotion path: a refuted verdict
// demotes its finding and is counted, so the ratio the caller logs is the real
// one.
func TestVerifyClaims_CountRefutations(t *testing.T) {
	verify := func(_ string, findings []report.Finding) ([]ClaimVerdict, error) {
		if len(findings) != 1 {
			t.Fatalf("sent %d findings, want 1 (WARNING must not be sent)", len(findings))
		}
		return []ClaimVerdict{{Index: 0, Verdict: "refuted", Evidence: "a.go:4", Reason: "guarded above"}}, nil
	}

	result := blockingAndWarning()
	got := VerifyClaims(result, t.TempDir(), verify)

	if want := (ClaimStats{Sent: 1, Verdicts: 1, Refuted: 1}); got != want {
		t.Errorf("stats = %+v, want %+v", got, want)
	}
	if result.Findings[0].Confidence != report.ConfidenceUncertain {
		t.Errorf("refuted finding confidence = %q, want uncertain", result.Findings[0].Confidence)
	}
	if result.Findings[0].VerificationNote != "refuted: guarded above" {
		t.Errorf("verification note = %q, want the refutation reason", result.Findings[0].VerificationNote)
	}
	if result.Findings[1].Confidence == report.ConfidenceUncertain {
		t.Error("WARNING finding was demoted; it is never sent to the verifier")
	}
}

// TestVerifyClaims_CountAgreement is the no-op signal this instrumentation
// exists for: a pass that confirms everything reports Refuted=0 with Sent>0.
// Zero refutations across many real runs means the verifier is agreeing rather
// than verifying, and the pass has stopped earning its tokens.
func TestVerifyClaims_CountAgreement(t *testing.T) {
	verify := func(_ string, _ []report.Finding) ([]ClaimVerdict, error) {
		return []ClaimVerdict{{Index: 0, Verdict: "confirmed", Evidence: "a.go:4"}}, nil
	}

	result := blockingAndWarning()
	got := VerifyClaims(result, t.TempDir(), verify)

	if want := (ClaimStats{Sent: 1, Verdicts: 1, Refuted: 0}); got != want {
		t.Errorf("stats = %+v, want %+v", got, want)
	}
	if result.Findings[0].Confidence == report.ConfidenceUncertain {
		t.Error("confirmed finding was demoted")
	}
}

// TestVerifyClaims_OnFailure keeps the fail-open path honest: a failed call
// still reports what it sent, so a run whose verifier never answered cannot be
// mistaken for a run that verified and found nothing.
func TestVerifyClaims_OnFailure(t *testing.T) {
	verify := func(_ string, _ []report.Finding) ([]ClaimVerdict, error) {
		return nil, errors.New("claude exploded")
	}

	got := VerifyClaims(blockingAndWarning(), t.TempDir(), verify)

	if want := (ClaimStats{Sent: 1}); got != want {
		t.Errorf("stats = %+v, want %+v", got, want)
	}
}

// TestVerifyClaims_NoSelectedFindings covers the empty branch: with nothing
// severe enough to verify, the pass makes no call and reports zeroes.
func TestVerifyClaims_NoSelectedFindings(t *testing.T) {
	verify := func(_ string, _ []report.Finding) ([]ClaimVerdict, error) {
		t.Fatal("verifier called with no BLOCKING/CRITICAL finding")
		return nil, nil
	}

	result := &report.ReviewResult{Findings: []report.Finding{
		{Severity: report.SeverityWarning, Title: "naming", File: "b.go", Line: 9},
	}}
	if got := VerifyClaims(result, t.TempDir(), verify); got != (ClaimStats{}) {
		t.Errorf("stats = %+v, want zero", got)
	}
}

// TestVerifyClaims_NilFn documents the no-op: a nil verify seam skips the pass
// entirely and reports zeroes, without demoting any finding.
func TestVerifyClaims_NilFn(t *testing.T) {
	result := blockingAndWarning()
	if got := VerifyClaims(result, t.TempDir(), nil); got != (ClaimStats{}) {
		t.Errorf("stats = %+v, want zero for a nil verify fn", got)
	}
	if result.Findings[0].Confidence == report.ConfidenceUncertain {
		t.Error("a nil verify fn must not demote any finding")
	}
}

// TestVerifyClaims_OutOfRangeIndexIgnored keeps the fail-open contract: a
// verdict whose index is outside the sent batch is ignored rather than panics
// or demotes the wrong finding.
func TestVerifyClaims_OutOfRangeIndexIgnored(t *testing.T) {
	verify := func(_ string, _ []report.Finding) ([]ClaimVerdict, error) {
		return []ClaimVerdict{{Index: 7, Verdict: "refuted", Reason: "nonsense"}}, nil
	}
	result := blockingAndWarning()
	got := VerifyClaims(result, t.TempDir(), verify)
	if got.Refuted != 0 {
		t.Errorf("out-of-range verdict must not demote, got Refuted=%d", got.Refuted)
	}
	if result.Findings[0].Confidence == report.ConfidenceUncertain {
		t.Error("BLOCKING finding demoted by an out-of-range verdict")
	}
}
