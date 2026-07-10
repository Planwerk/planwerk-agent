package claude

import (
	"fmt"
	"log/slog"
	"strings"

	"golang.org/x/sync/errgroup"

	"github.com/planwerk/planwerk-agent/internal/implement"
	"github.com/planwerk/planwerk-agent/internal/patterns"
	"github.com/planwerk/planwerk-agent/internal/report"
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
	// NeverGate marks a specialist that must always run regardless of which
	// files the diff touches — a miss in these domains is too costly to gate.
	// It is set for security and data-migration. When true, Relevance is
	// ignored. See [Specialist.ShouldRun].
	NeverGate bool
	// Relevance selects which changed files make a gateable specialist worth
	// running. It is ignored when NeverGate is true. See [Specialist.ShouldRun].
	Relevance Relevance
}

// Relevance classifies which changed files a gateable specialist cares about.
// Adaptive gating uses it to skip a specialist whose relevant paths the diff
// never touches, cutting wall-clock and cost on small PRs.
type Relevance int

const (
	// RelevanceAnySource runs the specialist when any changed file is source
	// code (not documentation, configuration, or other non-code files). It is
	// the zero value, so a gateable specialist defaults to it.
	RelevanceAnySource Relevance = iota
	// RelevanceRoutes runs the specialist only when a changed file lives in a
	// routing or request-handler layer (api/, routes/, handlers/, controllers/).
	RelevanceRoutes
)

// Specialists is the registry of domain reviewers run by the fan-out. Security
// and data-migration are marked NeverGate because a missed vulnerability or a
// destructive migration is far more costly than the extra pass; the remaining
// specialists are adaptively gated by [Specialist.ShouldRun] so a PR that does
// not touch their relevant paths skips them.
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
		Key:       "testing",
		Relevance: RelevanceAnySource,
		Focus:     `Test coverage for new or changed behavior: untested new functions/branches, missing error-path and edge-case tests, assertions that check type/status but not side effects, and missing integration/E2E tests when the project already runs them for comparable features. Do not flag trivial getters/setters.`,
	},
	{
		Key: "performance",
		// Gates on any source change. Narrowing to repo-configured hot-loop
		// directories is a future refinement; any-source is the safe default
		// (it runs on every code change and only skips doc/config-only PRs).
		Relevance: RelevanceAnySource,
		Focus:     `N+1 queries and missing eager loading, unbounded allocations or result sets, missing pagination, hot-path work that should be cached or batched, accidental quadratic loops, and known-heavy dependencies pulled into a hot path.`,
	},
	{
		Key:       "api-contract",
		Relevance: RelevanceRoutes,
		Focus:     `Backward-compatibility of public interfaces: breaking changes to exported signatures, HTTP routes, request/response shapes, or serialized formats without versioning; removed or renamed fields; changed status codes or error formats; and enum/value additions not handled by every consumer.`,
	},
	{
		Key:       "maintainability",
		Relevance: RelevanceAnySource,
		Focus:     `Clarity and intent: dead code, misleading names, duplicated logic that should be factored, magic numbers that should be named constants, and missing documentation for new public APIs, CLI flags, or config options. Flag only what genuinely impairs a new reader — not style preferences.`,
	},
}

// SpecialistReviews runs the domain-specialist fan-out over the diff and returns
// the successful results keyed by specialist, so the implement command's
// review-and-fix pass can run the same specialists the review command does. It
// mirrors review's fan-out: each specialist is adaptively gated by
// Specialist.ShouldRun(changedFiles), the gated-in ones run concurrently, and a
// specialist whose pass fails is logged and dropped rather than sinking the
// rest. baseBranch scopes the diff; pats/maxPatterns ground each specialist in
// the review-pattern catalog. The error return is always nil — per-specialist
// failures are non-fatal — but exists so the seam can surface a fatal error in
// future without a signature change.
func (c *Client) SpecialistReviews(dir, baseBranch string, changedFiles []string, pats []patterns.Pattern, maxPatterns int) ([]implement.SpecialistResult, error) {
	return runSpecialistFanOut(changedFiles, func(sp Specialist) (*report.ReviewResult, error) {
		return c.SpecialistReview(dir, baseBranch, sp.Key, sp.Focus, pats, maxPatterns)
	}), nil
}

// runSpecialistFanOut is the gate/dispatch/collect core of SpecialistReviews,
// factored out from the Claude call so it is unit-testable without the binary.
// It runs call(sp) concurrently for every specialist whose ShouldRun(changedFiles)
// is true, skips the rest (adaptive gating), drops a specialist whose call
// errors (logged, non-fatal), and returns the survivors in registry order.
func runSpecialistFanOut(changedFiles []string, call func(Specialist) (*report.ReviewResult, error)) []implement.SpecialistResult {
	results := make([]*report.ReviewResult, len(Specialists))
	var g errgroup.Group
	running := 0
	for i, sp := range Specialists {
		if !sp.ShouldRun(changedFiles) {
			// Adaptive gating: the diff does not touch this specialist's relevant
			// paths, so running it would only add cost.
			slog.Info("skipping specialist; diff does not touch its relevant paths", "specialist", sp.Key)
			continue
		}
		running++
		g.Go(func() error {
			res, err := call(sp)
			if err != nil {
				// A failed specialist must not sink the whole fan-out.
				slog.Warn("specialist review failed", "specialist", sp.Key, "err", err)
				return nil
			}
			results[i] = res
			return nil
		})
	}
	slog.Info("running specialist review fan-out", "running", running, "registered", len(Specialists))
	// The callbacks never return an error, so Wait cannot fail; the error return
	// is discarded deliberately.
	_ = g.Wait()

	var out []implement.SpecialistResult
	for i, sp := range Specialists {
		if results[i] != nil {
			out = append(out, implement.SpecialistResult{Key: sp.Key, Result: results[i]})
		}
	}
	return out
}

// SpecialistReview runs a single domain-focused review pass over the diff and
// returns its findings, tagged with the specialist's pattern. baseBranch scopes
// the review to changes relative to that branch. pats is the project
// review-pattern catalog (maxPatterns budgets how many are rendered), injected
// so the specialist applies the patterns that fall inside its domain; an empty
// catalog leaves the prompt unchanged.
func (c *Client) SpecialistReview(dir, baseBranch, key, focus string, pats []patterns.Pattern, maxPatterns int) (*report.ReviewResult, error) {
	raw, model, err := c.runClaude(dir, buildSpecialistPrompt(baseBranch, key, focus, pats, maxPatterns), "specialist-"+key)
	if err != nil {
		return nil, fmt.Errorf("running %s specialist review: %w", key, err)
	}
	result, err := c.structureReview(raw)
	if err != nil {
		return nil, fmt.Errorf("structuring %s specialist review: %w", key, err)
	}
	for i := range result.Findings {
		if result.Findings[i].Pattern == "" {
			result.Findings[i].Pattern = "specialist:" + key
		}
	}
	assignIDs(result)
	result.Model = model
	return result, nil
}

func buildSpecialistPrompt(baseBranch, key, focus string, pats []patterns.Pattern, maxPatterns int) string {
	if baseBranch == "" {
		baseBranch = DefaultBaseBranch
	}
	var sb strings.Builder

	fmt.Fprintf(&sb, "You are a %s specialist performing a focused code review. Review ONLY your domain.\n\n", key)
	sb.WriteString(diffScopeLines(baseBranch))
	fmt.Fprintf(&sb, `Then review ONLY the added/modified lines in those files.

## Your domain (%s)
%s

If your domain has no issues in this diff, return an empty findings array.

`, key, focus)

	sb.WriteString(finderPatternCatalog("## Project review patterns\n\nApply the project review patterns below that fall inside your domain — they ground your pass in the same catalog a later review of this diff would apply. They do NOT widen your scope beyond the domain above.", pats, maxPatterns))

	sb.WriteString(communicationStyleBlock())
	sb.WriteString(outputLanguageBlock())

	sb.WriteString(`## Finding Enrichment

For EVERY finding, include: a code snippet (the exact problematic lines from the diff) and a concrete suggested fix. Quote the triggering line verbatim; if you cannot, set confidence to "uncertain".

`)
	sb.WriteString(severityLadderBlock(scopeDiff))
	sb.WriteString(findingLabelsBlock())
	sb.WriteString(planwerkIgnoreLine())
	sb.WriteString("/review")

	return sb.String()
}
