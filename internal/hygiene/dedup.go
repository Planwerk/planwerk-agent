package hygiene

import (
	"log/slog"

	"github.com/planwerk/planwerk-agent/internal/report"
)

// DedupFn groups findings that describe the same underlying issue, returning
// index groups into the passed slice (never merged findings, so the model only
// classifies and never transcribes content). It backstops MergeResults' fuzzy
// matcher for findings that carry no file to anchor on. In production it is
// backed by claude.Client.DedupFindings; a nil DedupFn makes DedupFileless a
// no-op.
type DedupFn func(findings []report.Finding) ([][]int, error)

// DedupFileless folds cross-pass duplicate findings that carry no file — the
// ones MergeResults' fuzzy matcher cannot anchor. It sends only the file-less
// findings to dedup, which returns index groups, and folds each group in Go via
// mergeFindingPair so no finding content is transcribed by the model. Fewer than
// two file-less findings need no call. The pass is non-fatal: a nil dedup or a
// failed grouping call leaves the findings unmerged. A nil result is a no-op.
func DedupFileless(result *report.ReviewResult, dedup DedupFn) {
	if result == nil || dedup == nil {
		return
	}
	var fileless []int
	for i := range result.Findings {
		if result.Findings[i].File == "" {
			fileless = append(fileless, i)
		}
	}
	if len(fileless) < 2 {
		return
	}
	subset := make([]report.Finding, len(fileless))
	for j, idx := range fileless {
		subset[j] = result.Findings[idx]
	}
	groups, err := dedup(subset)
	if err != nil {
		slog.Warn("file-less finding dedup failed; keeping findings unmerged", "err", err)
		return
	}

	// Fold each group into its first member; mark the rest for removal. Group
	// indices are into subset, so map back to result.Findings via fileless.
	// claimed tracks every subset index the model has already assigned to a
	// group. The prompt asks it to place each index in at most one group, but
	// nothing enforces that: overlapping groups (e.g. [[0,1],[1,2]]) could make an
	// index that was merged-and-marked-for-removal become a later group's
	// keep-target, so its content merges only into a doomed finding and is then
	// pruned away. Claiming each index at most once keeps every distinct finding.
	remove := make(map[int]bool)
	claimed := make(map[int]bool)
	merged := 0
	for _, group := range groups {
		keepSub := -1
		for _, sub := range group {
			if sub < 0 || sub >= len(fileless) || claimed[sub] {
				continue // out-of-range or already claimed by an earlier group
			}
			claimed[sub] = true
			if keepSub == -1 {
				keepSub = sub
				continue
			}
			keepIdx, dupIdx := fileless[keepSub], fileless[sub]
			result.Findings[keepIdx] = mergeFindingPair(result.Findings[keepIdx], result.Findings[dupIdx])
			remove[dupIdx] = true
			merged++
		}
	}
	if merged == 0 {
		return
	}
	kept := result.Findings[:0]
	for i := range result.Findings {
		if !remove[i] {
			kept = append(kept, result.Findings[i])
		}
	}
	result.Findings = kept
	slog.Info("merged file-less duplicate findings via structure-tier fallback", "merged", merged)
}
