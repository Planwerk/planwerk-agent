package review

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/planwerk/planwerk-agent/internal/claude"
	"github.com/planwerk/planwerk-agent/internal/report"
)

// dedupLineTolerance is the ±line slack allowed when two findings are matched as
// the same issue: independently worded passes routinely land a line or two apart
// on the same defect. dedupTitleSimilarity is the minimum Jaccard token overlap
// (0-1) two titles must share to be treated as the same finding when their
// file+line already overlap. Both are heuristics locked by the merge tests and
// tuned via the eval harness.
const (
	dedupLineTolerance   = 3
	dedupTitleSimilarity = 0.5
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
// (adversarial or compliance). It deduplicates fuzzily — same file, overlapping
// line range (±dedupLineTolerance), and similar titles — because two
// independently worded passes almost never produce byte-identical titles, so an
// exact-key match would ship the same defect twice and never fire the
// multi-pass-confirmation confidence boost. For a duplicate it keeps the
// higher-severity finding and unions their ConfirmedBy provenance, boosting
// confidence one step the first time the finding becomes multi-pass confirmed.
// Secondary-only findings (and every file-less finding, which the fuzzy matcher
// cannot anchor) are appended; file-less duplicates are reconciled separately by
// the structure-tier dedup fallback.
func mergeResults(primary, secondary *report.ReviewResult) *report.ReviewResult {
	if secondary == nil || len(secondary.Findings) == 0 {
		return primary
	}

	// Match only against the findings primary held on entry, so two secondary
	// findings never fold into each other mid-merge (that would depend on
	// iteration order).
	origLen := len(primary.Findings)
	for _, sf := range secondary.Findings {
		if idx := findMatchIndex(primary.Findings[:origLen], sf); idx >= 0 {
			primary.Findings[idx] = mergeFindingPair(primary.Findings[idx], sf)
			continue
		}
		primary.Findings = append(primary.Findings, sf)
	}

	return primary
}

// findMatchIndex returns the index in existing of the best fuzzy match for sf,
// or -1 when none qualifies. A candidate qualifies only when it shares sf's
// non-empty file, its line range overlaps within dedupLineTolerance, and its
// title token-similarity is at least dedupTitleSimilarity. Among qualifying
// candidates the highest title similarity wins, ties broken by the closest line.
// A finding with no file cannot be anchored, so it never matches.
func findMatchIndex(existing []report.Finding, sf report.Finding) int {
	if sf.File == "" {
		return -1
	}
	best := -1
	bestScore := -1.0
	bestLineDist := 0
	for i := range existing {
		cand := existing[i]
		if cand.File != sf.File || !linesOverlap(cand, sf, dedupLineTolerance) {
			continue
		}
		score := titleSimilarity(cand.Title, sf.Title)
		if score < dedupTitleSimilarity {
			continue
		}
		dist := lineDistance(cand, sf)
		if score > bestScore || (score == bestScore && dist < bestLineDist) {
			best, bestScore, bestLineDist = i, score, dist
		}
	}
	return best
}

// mergeFindingPair folds dup into kept: it unions their ConfirmedBy provenance
// and, the first time the finding crosses into multi-pass-confirmed (≥2 distinct
// passes), boosts confidence one step. When dup carries a higher severity it is
// adopted while preserving kept's ID and enrichment; otherwise kept is retained.
func mergeFindingPair(kept, dup report.Finding) report.Finding {
	merged := unionPasses(kept.ConfirmedBy, dup.ConfirmedBy)
	confidence := kept.Confidence
	if distinctCount(kept.ConfirmedBy) < 2 && distinctCount(merged) >= 2 {
		confidence = boostConfidence(kept.Confidence)
	}

	existingRank, ok1 := severityRank[kept.Severity]
	newRank, ok2 := severityRank[dup.Severity]
	if ok1 && ok2 && newRank < existingRank {
		// Duplicate is higher severity — adopt it, but preserve the kept
		// finding's ID, enrichment, and the merged provenance/confidence.
		dup.ID = kept.ID
		if dup.CodeSnippet == "" {
			dup.CodeSnippet = kept.CodeSnippet
		}
		if dup.SuggestedFix == "" {
			dup.SuggestedFix = kept.SuggestedFix
		}
		dup.RelatedTo = mergeRelated(dup.RelatedTo, kept.RelatedTo)
		dup.ConfirmedBy = merged
		dup.Confidence = confidence
		return dup
	}
	kept.ConfirmedBy = merged
	kept.Confidence = confidence
	return kept
}

// linesOverlap reports whether a's and b's line ranges intersect once each is
// expanded by tolerance. When neither side carries a line the decision is left
// to title similarity (returns true); when exactly one side is located the two
// cannot be the same site (returns false).
func linesOverlap(a, b report.Finding, tolerance int) bool {
	aHas, bHas := a.Line > 0, b.Line > 0
	if !aHas && !bHas {
		return true // no line info on either side; let title similarity decide
	}
	if aHas != bHas {
		return false // one side located, the other not — cannot be the same site
	}
	aStart, aEnd := lineRange(a)
	bStart, bEnd := lineRange(b)
	return aStart-tolerance <= bEnd && bStart <= aEnd+tolerance
}

// lineRange returns the finding's [start, end] line span, clamping a missing or
// smaller LineEnd up to the start line.
func lineRange(f report.Finding) (int, int) {
	end := f.LineEnd
	if end < f.Line {
		end = f.Line
	}
	return f.Line, end
}

// lineDistance returns the absolute distance between the two findings' start
// lines, used only to break ties between equally similar candidates.
func lineDistance(a, b report.Finding) int {
	if d := a.Line - b.Line; d >= 0 {
		return d
	} else {
		return -d
	}
}

// titleSimilarity returns the Jaccard overlap (0-1) of a's and b's lowercased
// alphanumeric token sets, with an exact-match fast path returning 1.0. It is
// the fuzzy-title signal that lets two reworded phrasings of the same defect
// merge.
func titleSimilarity(a, b string) float64 {
	if normalizeTitle(a) == normalizeTitle(b) {
		return 1.0
	}
	at, bt := titleTokens(a), titleTokens(b)
	if len(at) == 0 || len(bt) == 0 {
		return 0
	}
	inter := 0
	for t := range at {
		if bt[t] {
			inter++
		}
	}
	union := len(at) + len(bt) - inter
	if union == 0 {
		return 0
	}
	return float64(inter) / float64(union)
}

// titleTokens splits title into a set of lowercased alphanumeric tokens.
func titleTokens(title string) map[string]bool {
	fields := strings.FieldsFunc(strings.ToLower(title), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})
	set := make(map[string]bool, len(fields))
	for _, f := range fields {
		set[f] = true
	}
	return set
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
