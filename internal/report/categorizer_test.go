package report

import "testing"

func TestCategorize(t *testing.T) {
	findings := []Finding{
		{Severity: SeverityBlocking, Title: "b1"},
		{Severity: SeverityCritical, Title: "c1"},
		{Severity: SeverityCritical, Title: "c2"},
		{Severity: SeverityWarning, Title: "w1"},
		{Severity: SeverityInfo, Title: "i1"},
		{Severity: SeverityInfo, Title: "i2"},
	}

	cf := Categorize(findings, SeverityInfo, "")
	if len(cf.Blocking) != 1 {
		t.Errorf("Blocking = %d, want 1", len(cf.Blocking))
	}
	if len(cf.Critical) != 2 {
		t.Errorf("Critical = %d, want 2", len(cf.Critical))
	}
	if len(cf.Warning) != 1 {
		t.Errorf("Warning = %d, want 1", len(cf.Warning))
	}
	if len(cf.Info) != 2 {
		t.Errorf("Info = %d, want 2", len(cf.Info))
	}
	if cf.Total() != 6 {
		t.Errorf("Total = %d, want 6", cf.Total())
	}
}

func TestCategorize_MinSeverity(t *testing.T) {
	findings := []Finding{
		{Severity: SeverityBlocking, Title: "b1"},
		{Severity: SeverityWarning, Title: "w1"},
		{Severity: SeverityInfo, Title: "i1"},
	}

	cf := Categorize(findings, SeverityWarning, "")
	if len(cf.Blocking) != 1 {
		t.Errorf("Blocking = %d, want 1", len(cf.Blocking))
	}
	if len(cf.Warning) != 1 {
		t.Errorf("Warning = %d, want 1", len(cf.Warning))
	}
	if len(cf.Info) != 0 {
		t.Errorf("Info = %d, want 0 (filtered by min severity)", len(cf.Info))
	}
}

func TestCategorize_ConfidenceOrdering(t *testing.T) {
	findings := []Finding{
		{Severity: SeverityCritical, Title: "uncertain", Confidence: ConfidenceUncertain},
		{Severity: SeverityCritical, Title: "verified", Confidence: ConfidenceVerified},
		{Severity: SeverityCritical, Title: "likely", Confidence: ConfidenceLikely},
	}
	cf := Categorize(findings, SeverityInfo, "")
	if len(cf.Critical) != 3 {
		t.Fatalf("Critical = %d, want 3", len(cf.Critical))
	}
	want := []string{"verified", "likely", "uncertain"}
	for i, w := range want {
		if cf.Critical[i].Title != w {
			t.Errorf("Critical[%d].Title = %q, want %q", i, cf.Critical[i].Title, w)
		}
	}
}

func TestCategorize_UnverifiedRouting(t *testing.T) {
	findings := []Finding{
		// Uncertain WARNING/INFO are demoted to Unverified.
		{Severity: SeverityWarning, Title: "w-uncertain", Confidence: ConfidenceUncertain},
		{Severity: SeverityInfo, Title: "i-uncertain", Confidence: ConfidenceUncertain},
		// Uncertain BLOCKING/CRITICAL stay in their buckets — too important to bury.
		{Severity: SeverityCritical, Title: "c-uncertain", Confidence: ConfidenceUncertain},
		// Verified WARNING stays in Warning.
		{Severity: SeverityWarning, Title: "w-verified", Confidence: ConfidenceVerified},
	}
	cf := Categorize(findings, SeverityInfo, "")
	if len(cf.Unverified) != 2 {
		t.Errorf("Unverified = %d, want 2", len(cf.Unverified))
	}
	if len(cf.Critical) != 1 {
		t.Errorf("Critical = %d, want 1 (uncertain critical stays)", len(cf.Critical))
	}
	if len(cf.Warning) != 1 || cf.Warning[0].Title != "w-verified" {
		t.Errorf("Warning = %v, want only w-verified", cf.Warning)
	}
	if cf.Total() != 4 {
		t.Errorf("Total = %d, want 4", cf.Total())
	}
}

func TestCategorize_RefutedCriticalDemoted(t *testing.T) {
	findings := []Finding{
		// A refuted CRITICAL (uncertain + a VerificationNote) is demoted to
		// Unverified — the counter-evidence outweighs the "never bury a critical"
		// rule.
		{Severity: SeverityCritical, Title: "refuted", Confidence: ConfidenceUncertain, VerificationNote: "refuted: guarded above"},
		// A merely-uncertain CRITICAL with no note stays in Critical.
		{Severity: SeverityCritical, Title: "unverifiable", Confidence: ConfidenceUncertain},
	}
	cf := Categorize(findings, SeverityInfo, "")
	if len(cf.Unverified) != 1 || cf.Unverified[0].Title != "refuted" {
		t.Errorf("Unverified = %v, want only the refuted finding", cf.Unverified)
	}
	if len(cf.Critical) != 1 || cf.Critical[0].Title != "unverifiable" {
		t.Errorf("Critical = %v, want only the unverifiable finding", cf.Critical)
	}
}

func TestCategorize_MinConfidence(t *testing.T) {
	findings := []Finding{
		{Severity: SeverityCritical, Title: "verified", Confidence: ConfidenceVerified},
		{Severity: SeverityCritical, Title: "likely", Confidence: ConfidenceLikely},
		{Severity: SeverityCritical, Title: "uncertain", Confidence: ConfidenceUncertain},
	}
	// likely threshold drops uncertain but keeps verified + likely.
	cf := Categorize(findings, SeverityInfo, ConfidenceLikely)
	if len(cf.Critical) != 2 {
		t.Errorf("Critical = %d, want 2 (uncertain filtered)", len(cf.Critical))
	}
	if cf.Total() != 2 {
		t.Errorf("Total = %d, want 2", cf.Total())
	}
}

func TestHasBlockersOrCritical(t *testing.T) {
	empty := CategorizedFindings{}
	if empty.HasBlockersOrCritical() {
		t.Error("empty should not have blockers")
	}

	withBlocking := CategorizedFindings{Blocking: []Finding{{}}}
	if !withBlocking.HasBlockersOrCritical() {
		t.Error("should have blockers when Blocking is non-empty")
	}

	withCritical := CategorizedFindings{Critical: []Finding{{}}}
	if !withCritical.HasBlockersOrCritical() {
		t.Error("should have blockers when Critical is non-empty")
	}

	warningsOnly := CategorizedFindings{Warning: []Finding{{}}}
	if warningsOnly.HasBlockersOrCritical() {
		t.Error("warnings-only should not have blockers")
	}
}
