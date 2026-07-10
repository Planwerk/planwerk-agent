package review

import (
	"fmt"

	"github.com/planwerk/planwerk-agent/internal/claude"
	"github.com/planwerk/planwerk-agent/internal/hygiene"
	"github.com/planwerk/planwerk-agent/internal/report"
)

// appendSummaryNote appends a parenthetical note to a non-empty summary so the
// reader knows which extra passes contributed. Each caller adds its own note
// once, instead of hygiene.MergeResults stamping the same suffix on every fold.
func appendSummaryNote(result *report.ReviewResult, note string) {
	if result != nil && result.Summary != "" {
		result.Summary += " (" + note + ")"
	}
}

// mergeSpecialists folds each specialist's findings into the primary review,
// tagging them with the specialist's pass label so cross-specialist agreement
// boosts confidence (via hygiene.MergeResults). nil entries (failed specialists)
// are skipped. specialistResults is index-aligned with claude.Specialists.
func mergeSpecialists(primary *report.ReviewResult, specialistResults []*report.ReviewResult) *report.ReviewResult {
	merged := 0
	for i, sr := range specialistResults {
		if sr == nil || i >= len(claude.Specialists) {
			continue
		}
		hygiene.TagPass(primary, hygiene.PassReview)
		hygiene.TagPass(sr, "specialist:"+claude.Specialists[i].Key)
		primary = hygiene.MergeResults(primary, sr)
		merged++
	}
	if merged > 0 {
		appendSummaryNote(primary, fmt.Sprintf("includes %d specialist pass(es)", merged))
	}
	return primary
}
