package review

import (
	"fmt"
	"strings"

	"github.com/planwerk/planwerk-review/internal/claude"
	"github.com/planwerk/planwerk-review/internal/report"
)

// Pass labels recorded in Finding.ConfirmedBy so the merge can tell how many
// independent passes flagged the same issue.
const (
	passReview      = "review"
	passAdversarial = "adversarial"
	passCompliance  = "compliance"
)

// tagPass stamps every finding that has no provenance yet with the given pass
// label. It is idempotent: findings already carrying a label (because they
// originated in or were merged from another pass) are left untouched.
func tagPass(result *report.ReviewResult, pass string) {
	if result == nil {
		return
	}
	for i := range result.Findings {
		if len(result.Findings[i].ConfirmedBy) == 0 {
			result.Findings[i].ConfirmedBy = []string{pass}
		}
	}
}

var severityRank = map[report.Severity]int{
	report.SeverityBlocking: 0,
	report.SeverityCritical: 1,
	report.SeverityWarning:  2,
	report.SeverityInfo:     3,
}

// mergeResults combines findings from a primary review and a secondary pass
// (adversarial or compliance). It deduplicates by file+line+title. For a
// duplicate it keeps the higher-severity finding and, because two passes
// independently flagged the same issue, unions their ConfirmedBy provenance
// and boosts confidence one step (the first time the finding becomes
// multi-pass confirmed). Secondary-only findings are appended.
func mergeResults(primary, secondary *report.ReviewResult) *report.ReviewResult {
	if secondary == nil || len(secondary.Findings) == 0 {
		return primary
	}

	type key struct {
		file  string
		line  int
		title string
	}
	existing := make(map[key]int) // key -> index in primary.Findings
	for i, f := range primary.Findings {
		if f.File != "" {
			existing[key{f.File, f.Line, normalizeTitle(f.Title)}] = i
		}
	}

	for _, sf := range secondary.Findings {
		k := key{sf.File, sf.Line, normalizeTitle(sf.Title)}
		idx, found := existing[k]
		if !found || sf.File == "" {
			// New finding from the secondary pass.
			primary.Findings = append(primary.Findings, sf)
			continue
		}

		kept := primary.Findings[idx]

		// Cross-pass agreement: union provenance and, the first time the
		// finding crosses into multi-pass-confirmed, boost confidence one step.
		merged := unionPasses(kept.ConfirmedBy, sf.ConfirmedBy)
		confidence := kept.Confidence
		if distinctCount(kept.ConfirmedBy) < 2 && distinctCount(merged) >= 2 {
			confidence = boostConfidence(kept.Confidence)
		}

		existingRank, ok1 := severityRank[kept.Severity]
		newRank, ok2 := severityRank[sf.Severity]
		if ok1 && ok2 && newRank < existingRank {
			// Secondary finding is higher severity — adopt it, but preserve the
			// primary's enrichment and the merged provenance/confidence.
			sf.ID = kept.ID
			if sf.CodeSnippet == "" {
				sf.CodeSnippet = kept.CodeSnippet
			}
			if sf.SuggestedFix == "" {
				sf.SuggestedFix = kept.SuggestedFix
			}
			sf.RelatedTo = mergeRelated(sf.RelatedTo, kept.RelatedTo)
			sf.ConfirmedBy = merged
			sf.Confidence = confidence
			primary.Findings[idx] = sf
		} else {
			kept.ConfirmedBy = merged
			kept.Confidence = confidence
			primary.Findings[idx] = kept
		}
	}

	return primary
}

// appendSummaryNote appends a parenthetical note to a non-empty summary so the
// reader knows which extra passes contributed. Each caller adds its own note
// once, instead of mergeResults stamping the same suffix on every fold.
func appendSummaryNote(result *report.ReviewResult, note string) {
	if result != nil && result.Summary != "" {
		result.Summary += " (" + note + ")"
	}
}

// mergeSpecialists folds each specialist's findings into the primary review,
// tagging them with the specialist's pass label so cross-specialist agreement
// boosts confidence (via mergeResults). nil entries (failed specialists) are
// skipped. specialistResults is index-aligned with claude.Specialists.
func mergeSpecialists(primary *report.ReviewResult, specialistResults []*report.ReviewResult) *report.ReviewResult {
	merged := 0
	for i, sr := range specialistResults {
		if sr == nil || i >= len(claude.Specialists) {
			continue
		}
		tagPass(primary, passReview)
		tagPass(sr, "specialist:"+claude.Specialists[i].Key)
		primary = mergeResults(primary, sr)
		merged++
	}
	if merged > 0 {
		appendSummaryNote(primary, fmt.Sprintf("includes %d specialist pass(es)", merged))
	}
	return primary
}

// boostConfidence raises confidence one step (uncertain -> likely -> verified).
// verified and unset confidences are returned unchanged.
func boostConfidence(c report.Confidence) report.Confidence {
	switch c {
	case report.ConfidenceUncertain:
		return report.ConfidenceLikely
	case report.ConfidenceLikely:
		return report.ConfidenceVerified
	default:
		return c
	}
}

// unionPasses returns the union of two pass-label lists, preserving the order
// of a's entries followed by b's new entries.
func unionPasses(a, b []string) []string {
	seen := make(map[string]bool, len(a)+len(b))
	var out []string
	for _, s := range append(append([]string{}, a...), b...) {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// distinctCount counts the distinct non-empty entries in passes.
func distinctCount(passes []string) int {
	seen := make(map[string]bool, len(passes))
	for _, s := range passes {
		if s != "" {
			seen[s] = true
		}
	}
	return len(seen)
}

// mergeRelated returns base with any entries from extra that are not already
// present appended.
func mergeRelated(base, extra []string) []string {
	if len(extra) == 0 {
		return base
	}
	seen := make(map[string]bool, len(base))
	for _, r := range base {
		seen[r] = true
	}
	for _, r := range extra {
		if !seen[r] {
			base = append(base, r)
			seen[r] = true
		}
	}
	return base
}

// normalizeTitle lowercases and trims a title for dedup comparison.
func normalizeTitle(title string) string {
	return strings.ToLower(strings.TrimSpace(title))
}
