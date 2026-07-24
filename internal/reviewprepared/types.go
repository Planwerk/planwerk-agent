// Package reviewprepared analyzes Planwerk feature files in the "prepared"
// state, surfaces quality issues in the SPEC ITSELF, and (optionally) opens
// a pull request that rewrites each feature JSON with the improvements.
//
// Scope vs. neighbouring commands:
//   - review (PR diff) and audit (whole codebase) review CODE.
//   - gap-analysis compares COMPLETED feature specs to the codebase to find
//     spec items that were never implemented.
//   - review-prepared reviews the SPEC TEXT only. It does not look at the
//     codebase to judge whether something is implemented — for "prepared"
//     features implementation has not started, and that is expected. Its
//     entire job is to make the spec sharper, more verifiable, and more
//     internally consistent BEFORE an engineer starts coding against it.
//
// The codebase is cloned only for context (loading repo-local patterns,
// rendering implementation_notes that reference real files). Findings about
// the code state are intentionally out of scope here.
package reviewprepared

import (
	"encoding/json"
	"time"

	"github.com/planwerk/planwerk-agent/internal/patterns"
	"github.com/planwerk/planwerk-agent/internal/report"
)

// CommandReviewPrepared is the cache scope identifier for review-prepared
// entries. Mirrors the constants defined by gapanalysis / audit / review.
const CommandReviewPrepared = "review-prepared"

// PreparedStatus is the feature lifecycle status this command operates on.
// A feature with any other status is skipped — we do NOT review drafts in
// progress nor features whose implementation already shipped.
const PreparedStatus = "prepared"

// FindingCategory groups review findings by which slice of the spec the
// problem touches. Keeping these as enum-shaped strings makes the renderer
// table easy to read and lets downstream tools group/filter consistently.
type FindingCategory string

const (
	// CategoryStories covers user-story shape: role/want/so_that completeness,
	// criterion verifiability, missing edge cases.
	CategoryStories FindingCategory = "stories"
	// CategoryRequirements covers requirement shape: priority, rationale,
	// scenario coverage, traceability to stories.
	CategoryRequirements FindingCategory = "requirements"
	// CategoryTasks covers task ordering, granularity, and requirement linkage.
	CategoryTasks FindingCategory = "tasks"
	// CategoryTests covers TestSpecification coverage of every requirement.
	CategoryTests FindingCategory = "tests"
	// CategoryReviewCriteria covers the review_criteria block at the bottom
	// of the feature file.
	CategoryReviewCriteria FindingCategory = "review_criteria"
	// CategoryImplementationNotes covers implementation_notes depth and
	// concreteness (file paths, pitfalls, pattern references).
	CategoryImplementationNotes FindingCategory = "implementation_notes"
	// CategoryOther is a catch-all for cross-cutting findings (e.g. missing
	// summary, ambiguous slug).
	CategoryOther FindingCategory = "other"
)

// Confidence mirrors the confidence levels used by gap-analysis so renderers
// and downstream tooling can treat both result shapes uniformly.
type Confidence string

const (
	ConfidenceVerified  Confidence = "verified"
	ConfidenceLikely    Confidence = "likely"
	ConfidenceUncertain Confidence = "uncertain"
)

// Finding is a single quality issue Claude raised against one prepared
// feature file. Severity follows the project-wide report.Severity vocabulary
// so the renderer can reuse the same color/weighting rules.
type Finding struct {
	ID          string          `json:"id"`
	FeatureID   string          `json:"feature_id"`
	FeatureFile string          `json:"feature_file"`
	Category    FindingCategory `json:"category"`
	Severity    report.Severity `json:"severity"`
	Title       string          `json:"title"`
	Description string          `json:"description"`
	Suggestion  string          `json:"suggestion"`   // concrete edit recommended for the JSON file
	SpecPointer string          `json:"spec_pointer"` // e.g. "stories[2].criteria[1]" — the JSON-pointer-ish path
	Confidence  Confidence      `json:"confidence"`
}

// FeatureReview groups findings + improved JSON for one prepared feature.
type FeatureReview struct {
	FeatureID   string    `json:"feature_id"`
	FeatureFile string    `json:"feature_file"`
	Title       string    `json:"title"`
	Findings    []Finding `json:"findings"`
	Summary     string    `json:"summary"`
	// ImprovedJSON is the full rewritten content of the feature file, if
	// Claude produced one. Empty when the spec is already in good shape OR
	// when the analyze-only flow runs (no PR side-effect requested).
	//
	// json.RawMessage stores the value as raw JSON bytes so the round-trip
	// through the cache envelope (and through the structuring Claude call)
	// preserves the rewritten document without base64 wrapping or pretty-
	// reformatting. Callers writing to disk should re-encode through
	// json.Indent to keep the file diff-friendly.
	ImprovedJSON json.RawMessage `json:"improved_json,omitempty"`
}

// Result is the full review-prepared output across one or more features.
type Result struct {
	RepoFullName string          `json:"repo"`
	Features     []FeatureReview `json:"features"`
	Overview     string          `json:"overview"`
	// Model is the resolved Claude model id (e.g. "claude-opus-5") that
	// produced this result. It is threaded per-run to the attribution footer
	// and excluded from the serialized payload.
	Model string `json:"-"`
}

// Options configures the review-prepared pipeline.
type Options struct {
	RepoRef         string
	PatternDirs     []string
	NoRepoPatterns  bool
	NoLocalPatterns bool
	NoCache         bool
	Format          string // "markdown" or "json"
	Version         string
	MaxPatterns     int
	MinSeverity     report.Severity

	// FeatureID restricts the run to a single prepared feature by its
	// feature_id (e.g. "PX-0028"). FilePath restricts by file path or
	// basename under .planwerk/features/. Both empty = every prepared file.
	FeatureID string
	FilePath  string

	// CreatePR opens a GitHub pull request with the improved feature JSON
	// files. When false the command is read-only and only renders the
	// review report.
	CreatePR bool
	// PRBranch is the name of the branch to push improvements onto. Empty
	// falls back to a deterministic default ("planwerk-agent/improve-prepared-features").
	PRBranch string
	// PRBase is the base branch the PR opens against. Empty = repo default.
	PRBase string

	CacheMaxAge time.Duration
	Local       bool // operate on the current working directory instead of cloning
	Force       bool // with Local, skip the dirty-working-tree confirmation prompt
	// Remote configures how remote pattern URIs (--patterns github:..., git+...)
	// resolve into local directories; carries the --remote-patterns-ttl value.
	Remote patterns.RemoteOptions
}

// AllFindings flattens the per-feature buckets into a single slice in emit
// order. Convenience for renderers and severity counting.
func (r *Result) AllFindings() []Finding {
	if r == nil {
		return nil
	}
	total := 0
	for _, f := range r.Features {
		total += len(f.Findings)
	}
	out := make([]Finding, 0, total)
	for _, f := range r.Features {
		out = append(out, f.Findings...)
	}
	return out
}
