package claude

import (
	"fmt"

	"github.com/planwerk/planwerk-review/internal/doccheck"
	"github.com/planwerk/planwerk-review/internal/patterns"
	"github.com/planwerk/planwerk-review/internal/report"
)

// DefaultBaseBranch is the fallback base branch name when none is specified.
const DefaultBaseBranch = "main"

// ReviewContext holds all context needed to build the review prompt.
type ReviewContext struct {
	Patterns    []patterns.Pattern
	MaxPatterns int    // max patterns to inject; <= 0 disables truncation
	MaxFindings int    // cap on findings Claude returns; <= 0 disables cap
	BaseBranch  string // PR base branch; empty falls back to DefaultBaseBranch
	PRTitle     string
	PRBody      string
	Checklist   string                    // external checklist content (empty = use built-in)
	CommitLog   string                    // git log output for scope drift detection
	StaleDocs   []doccheck.StaleDocHint   // documentation files that may need updating
	NewFeatures []doccheck.NewFeatureHint // new files that may need documentation
	TodoContent string                    // content of TODOS.md if present
}

// Review invokes `claude /review` in the given directory and returns structured findings.
// It runs two Claude calls:
//  1. `claude /review` to get the unstructured review output
//  2. `claude -p` to structure the output into JSON
func Review(dir string, ctx ReviewContext) (*report.ReviewResult, error) {
	// Step 1: Run /review
	rawReview, err := runReview(dir, ctx)
	if err != nil {
		return nil, fmt.Errorf("running /review: %w", err)
	}

	// Step 2: Structure the output into JSON
	result, err := structureReview(rawReview)
	if err != nil {
		return nil, fmt.Errorf("structuring review output: %w", err)
	}

	assignIDs(result)
	return result, nil
}

// runReview invokes `claude -p` with a prompt that includes patterns and the /review command.
func runReview(dir string, rctx ReviewContext) (string, error) {
	return runClaude(dir, buildReviewPrompt(rctx), "review")
}
