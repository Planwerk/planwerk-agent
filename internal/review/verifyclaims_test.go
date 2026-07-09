package review

import (
	"errors"
	"testing"

	"github.com/planwerk/planwerk-agent/internal/claude"
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

// TestVerifyClaimsStatsCountRefutations pins the demotion path: a refuted
// verdict demotes its finding and is counted, so the ratio the caller logs is
// the real one.
func TestVerifyClaimsStatsCountRefutations(t *testing.T) {
	r := &Runner{Claude: &configurableClaude{
		verifyClaims: func(_ string, findings []report.Finding) ([]claude.ClaimVerdict, error) {
			if len(findings) != 1 {
				t.Fatalf("sent %d findings, want 1 (WARNING must not be sent)", len(findings))
			}
			return []claude.ClaimVerdict{{Index: 0, Verdict: "refuted", Evidence: "a.go:4", Reason: "guarded above"}}, nil
		},
	}}

	result := blockingAndWarning()
	got := r.verifyClaims(result, t.TempDir())

	if want := (claimStats{Sent: 1, Verdicts: 1, Refuted: 1}); got != want {
		t.Errorf("stats = %+v, want %+v", got, want)
	}
	if result.Findings[0].Confidence != report.ConfidenceUncertain {
		t.Errorf("refuted finding confidence = %q, want uncertain", result.Findings[0].Confidence)
	}
	if result.Findings[1].Confidence == report.ConfidenceUncertain {
		t.Error("WARNING finding was demoted; it is never sent to the verifier")
	}
}

// TestVerifyClaimsStatsCountAgreement is the no-op signal this instrumentation
// exists for: a pass that confirms everything reports Refuted=0 with Sent>0.
// Zero refutations across many real runs means the verifier is agreeing rather
// than verifying, and the pass has stopped earning its tokens.
func TestVerifyClaimsStatsCountAgreement(t *testing.T) {
	r := &Runner{Claude: &configurableClaude{
		verifyClaims: func(_ string, _ []report.Finding) ([]claude.ClaimVerdict, error) {
			return []claude.ClaimVerdict{{Index: 0, Verdict: "confirmed", Evidence: "a.go:4"}}, nil
		},
	}}

	result := blockingAndWarning()
	got := r.verifyClaims(result, t.TempDir())

	if want := (claimStats{Sent: 1, Verdicts: 1, Refuted: 0}); got != want {
		t.Errorf("stats = %+v, want %+v", got, want)
	}
	if result.Findings[0].Confidence == report.ConfidenceUncertain {
		t.Error("confirmed finding was demoted")
	}
}

// TestVerifyClaimsStatsOnFailure keeps the fail-open path honest: a failed call
// still reports what it sent, so a run whose verifier never answered cannot be
// mistaken for a run that verified and found nothing.
func TestVerifyClaimsStatsOnFailure(t *testing.T) {
	r := &Runner{Claude: &configurableClaude{
		verifyClaims: func(_ string, _ []report.Finding) ([]claude.ClaimVerdict, error) {
			return nil, errors.New("claude exploded")
		},
	}}

	got := r.verifyClaims(blockingAndWarning(), t.TempDir())

	if want := (claimStats{Sent: 1}); got != want {
		t.Errorf("stats = %+v, want %+v", got, want)
	}
}

// TestVerifyClaimsNoSelectedFindings covers the empty branch: with nothing
// severe enough to verify, the pass makes no call and reports zeroes.
func TestVerifyClaimsNoSelectedFindings(t *testing.T) {
	claudeMock := &configurableClaude{
		verifyClaims: func(_ string, _ []report.Finding) ([]claude.ClaimVerdict, error) {
			t.Fatal("verifier called with no BLOCKING/CRITICAL finding")
			return nil, nil
		},
	}
	r := &Runner{Claude: claudeMock}

	result := &report.ReviewResult{Findings: []report.Finding{
		{Severity: report.SeverityWarning, Title: "naming", File: "b.go", Line: 9},
	}}
	if got := r.verifyClaims(result, t.TempDir()); got != (claimStats{}) {
		t.Errorf("stats = %+v, want zero", got)
	}
}
