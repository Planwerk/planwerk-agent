// Package gapanalysis compares completed Planwerk feature files against the
// actual repository state and reports gaps — acceptance criteria, scenarios,
// test specifications, or tasks marked complete in the spec but not yet
// implemented in the code.
package gapanalysis

import (
	"time"

	"github.com/planwerk/planwerk-review/internal/report"
)

// CommandGapAnalysis is the cache scope identifier for gap-analysis entries.
const CommandGapAnalysis = "gap-analysis"

// GapType classifies the kind of incompleteness reported.
type GapType string

const (
	GapMissingCriterion GapType = "missing_criterion" // story acceptance criterion has no visible implementation
	GapMissingScenario  GapType = "missing_scenario"  // requirement scenario (When/Then) not honored by code
	GapMissingTest      GapType = "missing_test"      // planned TestSpecification has no matching test function
	GapMissingTask      GapType = "missing_task"      // task marked completed but description not evident in code
)

// Confidence mirrors report.Finding confidence levels but is duplicated here so
// the gap result is independently serializable.
type Confidence string

const (
	ConfidenceVerified  Confidence = "verified"
	ConfidenceLikely    Confidence = "likely"
	ConfidenceUncertain Confidence = "uncertain"
)

// Gap is a single incompleteness reported against a completed feature file.
type Gap struct {
	ID          string          `json:"id"`
	FeatureID   string          `json:"feature_id"`
	FeatureFile string          `json:"feature_file"` // basename of the feature JSON, for traceability
	Type        GapType         `json:"type"`
	Severity    report.Severity `json:"severity"`
	Title       string          `json:"title"`
	Description string          `json:"description"`
	Evidence    string          `json:"evidence"`     // concrete file/line references the model used
	Source      string          `json:"source"`       // the spec text that this gap maps to (criterion, scenario, etc.)
	Confidence  Confidence      `json:"confidence"`
	Suggested   IssueSuggestion `json:"suggested_issue"`
}

// IssueSuggestion is the model's suggested GitHub issue title and body for a
// gap. The interactive flow may post this verbatim.
type IssueSuggestion struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

// FeatureGaps is the per-feature group emitted by Claude.
type FeatureGaps struct {
	FeatureID   string `json:"feature_id"`
	FeatureFile string `json:"feature_file"`
	Title       string `json:"title"` // feature title, copied from the spec for context
	Gaps        []Gap  `json:"gaps"`
	Summary     string `json:"summary"` // 1-3 sentence overall verdict for this feature
}

// Result is the full gap-analysis output across one or more features.
type Result struct {
	RepoFullName string        `json:"repo"`
	Features     []FeatureGaps `json:"features"`
	Overview     string        `json:"overview"`
}

// Options configures the gap-analysis pipeline.
type Options struct {
	RepoRef         string
	PatternDirs     []string
	NoRepoPatterns  bool
	NoLocalPatterns bool
	NoCache         bool
	Format          string // "markdown" or "json"
	Version         string
	MaxPatterns     int

	// Filter selects which completed/*.json files to analyze. Both empty
	// means "all files in .planwerk/completed/". FeatureID matches against
	// the parsed feature_id field; FilePath is an absolute or
	// repo-relative path to a single feature file.
	FeatureID string
	FilePath  string

	CreateIssues  bool
	NoIssueDedupe bool
	CacheMaxAge   time.Duration
	Local         bool // operate on the current working directory instead of cloning
	Force         bool // with Local, skip the dirty-working-tree confirmation prompt
}

// AllGaps flattens the per-feature buckets back into a single slice in the
// emit order, useful for renderers and the interactive flow.
func (r *Result) AllGaps() []Gap {
	if r == nil {
		return nil
	}
	total := 0
	for _, f := range r.Features {
		total += len(f.Gaps)
	}
	out := make([]Gap, 0, total)
	for _, f := range r.Features {
		out = append(out, f.Gaps...)
	}
	return out
}
