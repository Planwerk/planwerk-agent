package capture

import (
	"strings"

	"github.com/planwerk/planwerk-review/internal/patterns"
	"github.com/planwerk/planwerk-review/internal/report"
)

// CandidateFindings is the quality pre-filter applied before the proposal pass:
// it keeps only findings whose Pattern does NOT name a loaded catalog entry —
// i.e. findings that matched no existing review pattern, so they are candidates
// for a NEW one. A finding whose Pattern names an existing catalog pattern is
// already covered; proposing a pattern for it would manufacture the redundancy
// the sync pass exists to clean, so it is dropped.
//
// It deliberately does NOT apply the meta-issue's "ConfirmedBy >= 2" half of the
// gate. The implement review pass runs AdversarialReview directly, which stamps
// an empty Pattern with "adversarial-review" and never runs the multi-pass merge
// that populates ConfirmedBy — so that half would select zero findings and make
// pattern capture a guaranteed no-op. The recurring/generalizable judgment is
// delegated to the proposal prompt instead; because the posture is propose-only,
// a looser pre-filter only lengthens a human-reviewed suggestion list, never an
// unreviewed write.
func CandidateFindings(findings []report.Finding, catalog []patterns.Pattern) []report.Finding {
	known := make(map[string]bool, len(catalog))
	for _, p := range catalog {
		if name := normalizePatternName(p.Name); name != "" {
			known[name] = true
		}
	}
	var candidates []report.Finding
	for _, f := range findings {
		if known[normalizePatternName(f.Pattern)] {
			continue
		}
		candidates = append(candidates, f)
	}
	return candidates
}

// normalizePatternName folds a pattern name to a case- and whitespace-insensitive
// key so a finding's Pattern matches a catalog entry's Name regardless of casing.
func normalizePatternName(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}
