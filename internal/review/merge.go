package review

import (
	"github.com/planwerk/planwerk-agent/internal/claude"
	"github.com/planwerk/planwerk-agent/internal/hygiene"
	"github.com/planwerk/planwerk-agent/internal/report"
)

// mergeSpecialists folds each specialist's findings into the primary review,
// tagging them with the specialist's provenance label so cross-specialist
// agreement boosts confidence (via hygiene.MergeResults). specialistResults is
// index-aligned with claude.Specialists. A nil entry is a specialist that was
// gated out or failed; it is skipped here, and ReviewResult.Passes records
// which of the two it was.
func mergeSpecialists(primary *report.ReviewResult, specialistResults []*report.ReviewResult) *report.ReviewResult {
	for i, sr := range specialistResults {
		if sr == nil || i >= len(claude.Specialists) {
			continue
		}
		hygiene.TagPass(primary, hygiene.PassReview)
		hygiene.TagPass(sr, hygiene.SpecialistPass(claude.Specialists[i].Key))
		primary = hygiene.MergeResults(primary, sr)
	}
	return primary
}
