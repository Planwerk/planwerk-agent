package claude

import (
	"fmt"
	"strings"

	"github.com/planwerk/planwerk-review/internal/report"
)

// Specialist is one domain reviewer in the fan-out. Each runs an independent,
// narrowly focused review pass; findings the same location triggers across
// specialists are merged and confidence-boosted by the review pipeline.
type Specialist struct {
	// Key is the short domain identifier, used as the finding pattern tag and
	// the cross-pass provenance label.
	Key string
	// Focus is the domain-specific checklist injected into the prompt.
	Focus string
	// NeverGate marks a specialist that must always run even once adaptive
	// gating (skipping low-yield specialists) is added — a miss in these
	// domains is too costly. It is reserved for that future gating; today every
	// specialist runs when the fan-out is enabled.
	NeverGate bool
}

// Specialists is the registry of domain reviewers run by the fan-out. Security
// and data-migration are marked NeverGate because a missed vulnerability or a
// destructive migration is far more costly than the extra pass.
var Specialists = []Specialist{
	{
		Key:       "security",
		NeverGate: true,
		Focus:     `Injection (SQL/command/template), auth and authorization gaps, secrets committed to source, unsafe deserialization, SSRF, path traversal, missing input validation at trust boundaries, unsafe HTML/template rendering of user data, weak crypto or RNG, and LLM-output written to a sink without validation. For each finding, name the concrete attack vector.`,
	},
	{
		Key:       "data-migration",
		NeverGate: true,
		Focus:     `Schema migrations and data changes: irreversible or non-backward-compatible migrations, missing down/rollback paths, locking or long-running operations on large tables, default/NOT NULL additions without backfill, data loss from type narrowing or column drops, and ordering hazards between code deploy and migration apply.`,
	},
	{
		Key:   "testing",
		Focus: `Test coverage for new or changed behavior: untested new functions/branches, missing error-path and edge-case tests, assertions that check type/status but not side effects, and missing integration/E2E tests when the project already runs them for comparable features. Do not flag trivial getters/setters.`,
	},
	{
		Key:   "performance",
		Focus: `N+1 queries and missing eager loading, unbounded allocations or result sets, missing pagination, hot-path work that should be cached or batched, accidental quadratic loops, and known-heavy dependencies pulled into a hot path.`,
	},
	{
		Key:   "api-contract",
		Focus: `Backward-compatibility of public interfaces: breaking changes to exported signatures, HTTP routes, request/response shapes, or serialized formats without versioning; removed or renamed fields; changed status codes or error formats; and enum/value additions not handled by every consumer.`,
	},
	{
		Key:   "maintainability",
		Focus: `Clarity and intent: dead code, misleading names, duplicated logic that should be factored, magic numbers that should be named constants, and missing documentation for new public APIs, CLI flags, or config options. Flag only what genuinely impairs a new reader — not style preferences.`,
	},
}

// SpecialistReview runs a single domain-focused review pass over the diff and
// returns its findings, tagged with the specialist's pattern. baseBranch scopes
// the review to changes relative to that branch.
func SpecialistReview(dir, baseBranch, key, focus string) (*report.ReviewResult, error) {
	raw, err := runClaude(dir, buildSpecialistPrompt(baseBranch, key, focus), "specialist-"+key)
	if err != nil {
		return nil, fmt.Errorf("running %s specialist review: %w", key, err)
	}
	result, err := structureReview(raw)
	if err != nil {
		return nil, fmt.Errorf("structuring %s specialist review: %w", key, err)
	}
	for i := range result.Findings {
		if result.Findings[i].Pattern == "" {
			result.Findings[i].Pattern = "specialist:" + key
		}
	}
	assignIDs(result)
	return result, nil
}

func buildSpecialistPrompt(baseBranch, key, focus string) string {
	if baseBranch == "" {
		baseBranch = DefaultBaseBranch
	}
	var sb strings.Builder

	fmt.Fprintf(&sb, `You are a %s specialist performing a focused code review. Review ONLY your domain; another reviewer covers everything else, so do not dilute your pass with out-of-domain nits.

SCOPE: only files changed in the current branch compared to origin/%s.
First run: git diff origin/%s --name-only
Then review ONLY the added/modified lines in those files.

## Your domain (%s)
%s

Report nothing outside this domain. If your domain has no issues in this diff, return an empty findings array.

`, key, baseBranch, baseBranch, key, focus)

	sb.WriteString(communicationStyleBlock())

	sb.WriteString(`## Finding Enrichment

For EVERY finding, include: a code snippet (the exact problematic lines from the diff), a concrete suggested fix, and a confidence level — "verified" (visible in the diff with certainty), "likely" (strong evidence, depends on wider context), or "uncertain" (needs investigation). Quote the triggering line verbatim; if you cannot, set confidence to "uncertain".

IMPORTANT: Completely ignore all changes in the .planwerk/ directory.

/review`)

	return sb.String()
}
