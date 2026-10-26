package claude

import (
	"fmt"
	"log/slog"
	"strings"

	"golang.org/x/sync/errgroup"

	"github.com/planwerk/planwerk-agent/internal/diffscope"
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
	// Areas names the review-pattern areas this specialist's domain covers. The
	// prompt renders only those patterns from the catalog, because the rest are
	// ones the prompt already tells it not to act on: a specialist reviews ONLY
	// its domain, so a pattern outside it is text the pass is asked to ignore —
	// at, for a Go repository, ~2 KB of prompt each.
	//
	// An empty Areas renders the whole catalog. That is the correct value for a
	// specialist whose domain genuinely spans it (maintainability reads on
	// quality, architecture and documentation alike) and the safe default for one
	// added later: a new specialist behaves exactly as every specialist did
	// before this field existed until someone decides what it covers.
	Areas []string
	// Design marks the specialist that reads the impeccable design skill when
	// the operator loaded it: its reference documents, and the detector's
	// matches on the changed UI files as leads (decision 155). It is set for
	// ui alone.
	Design bool
}

// PassLabel returns the session label the specialist's pass runs under,
// "specialist-<key>": the run records the pass's usage and its
// report.PassStatus under it. The provenance label its findings carry in
// ConfirmedBy is hygiene.SpecialistPass, "specialist:<key>" (decision 135).
func (sp Specialist) PassLabel() string { return "specialist-" + sp.Key }

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
	// RelevanceUI runs the specialist only when a changed file is markup, a
	// stylesheet, a UI component, or a view template (isUIFile).
	RelevanceUI
)

// Specialists is the registry of domain reviewers run by the fan-out. Security
// and data-migration are marked NeverGate because a missed vulnerability or a
// destructive migration is far more costly than the extra pass; the remaining
// specialists are adaptively gated by [Specialist.ShouldRun] so a PR that does
// not touch their relevant paths skips them. ui comes last, so the six that
// preceded it keep their indices (decision 155).
//
// Each specialist's Areas lists the areas its domain actually reads on, and is
// read as an exclusion of the rest: it drops only areas plainly outside the
// domain. The catalog has no performance or data-migration area of its own, so
// those two are mapped to the areas their patterns are filed under (quality,
// reliability) rather than to an invented one — a mapping stretched to force a
// saving would drop the patterns the pass exists to apply, which is the one
// failure mode this must not have.
var Specialists = []Specialist{
	{
		Key:       "security",
		NeverGate: true,
		Areas:     []string{"security"},
		Focus:     `Injection (SQL/command/template), auth and authorization gaps, secrets committed to source, unsafe deserialization, SSRF, path traversal, missing input validation at trust boundaries, unsafe HTML/template rendering of user data, weak crypto or RNG, and LLM-output written to a sink without validation. For each finding, name the concrete attack vector.`,
	},
	{
		Key:       "data-migration",
		NeverGate: true,
		Areas:     []string{"reliability"},
		Focus:     `Schema migrations and data changes: irreversible or non-backward-compatible migrations, missing down/rollback paths, locking or long-running operations on large tables, default/NOT NULL additions without backfill, data loss from type narrowing or column drops, and ordering hazards between code deploy and migration apply.`,
	},
	{
		Key:       "testing",
		Relevance: RelevanceAnySource,
		Areas:     []string{"testing"},
		Focus:     `Test coverage for new or changed behavior: untested new functions/branches, missing error-path and edge-case tests, assertions that check type/status but not side effects, and missing integration/E2E tests when the project already runs them for comparable features. Do not flag trivial getters/setters.`,
	},
	{
		Key: "performance",
		// Gates on any source change. Narrowing to repo-configured hot-loop
		// directories is a future refinement; any-source is the safe default
		// (it runs on every code change and only skips doc/config-only PRs).
		Relevance: RelevanceAnySource,
		// The catalog has no performance area: its performance-bearing patterns
		// (goroutine leaks, synchronization primitives, slice/map idioms, resource
		// limits, the observability trio) are all filed under quality.
		Areas: []string{"quality"},
		Focus: `N+1 queries and missing eager loading, unbounded allocations or result sets, missing pagination, hot-path work that should be cached or batched, accidental quadratic loops, and known-heavy dependencies pulled into a hot path.`,
	},
	{
		Key:       "api-contract",
		Relevance: RelevanceRoutes,
		// REST design and the API conventions sit under architecture; the error
		// format and HTTP semantics patterns under quality.
		Areas: []string{"architecture", "quality"},
		Focus: `Backward-compatibility of public interfaces: breaking changes to exported signatures, HTTP routes, request/response shapes, or serialized formats without versioning; removed or renamed fields; changed status codes or error formats; and enum/value additions not handled by every consumer.`,
	},
	{
		Key:       "maintainability",
		Relevance: RelevanceAnySource,
		// The one genuinely cross-cutting domain: naming and dead code read on
		// quality, factoring on architecture, and missing docs for a new public
		// API on documentation. Only security, testing and reliability drop out.
		Areas: []string{"quality", "architecture", "documentation"},
		Focus: `Clarity and intent: dead code, misleading names, duplicated logic that should be factored, magic numbers that should be named constants, and missing documentation for new public APIs, CLI flags, or config options. Skip style preferences: formatting, and naming that follows the project's conventions.`,
	},
	{
		Key:       "ui",
		Relevance: RelevanceUI,
		// No embedded pattern carries this area, so the pass gets the whole
		// catalog until a project files UI patterns under it.
		Areas:  []string{"user-experience"},
		Design: true,
		Focus:  `What a person sees and operates. Markup that hides its meaning from assistive technology: a control without an accessible name, an image without alt text, a skipped heading level, a click handler on an element the keyboard cannot reach, focus lost or trapped after an action. Text or controls below WCAG 2.2 AA contrast, and touch targets under its minimum of 24 by 24 CSS pixels. States the change leaves unrendered: loading, empty, error, and long or translated text that overflows. Layouts that break at narrow widths or on zoom, and motion without a prefers-reduced-motion fallback. Colors, sizes, or spacing hard-coded where the project uses design tokens. UI copy that leaves the user unable to tell what happened or what to do next. Judge each from the source: the markup, the stylesheet values, the component's states. Judge visual taste only where the project's documented design decisions (a DESIGN.md, a PRODUCT.md, a brand guide) rule on it.`,
	},
}

// SpecialistReviews runs the domain-specialist fan-out over the diff and returns
// one entry per registered specialist, in registry order, so the implement
// command's review-and-fix pass can run the same specialists the review command
// does. It mirrors review's fan-out: each specialist is adaptively gated by
// Specialist.ShouldRun(changedFiles), the gated-in ones run concurrently, and a
// specialist whose pass fails is logged and dropped rather than sinking the
// rest. An entry's Result is nil when its specialist was gated out or failed,
// and its Status says which. baseBranch scopes the diff; pats/maxPatterns
// ground each specialist in the review-pattern catalog. The error return is
// always nil — per-specialist failures are non-fatal — but exists so the seam
// can surface a fatal error in future without a signature change.
func (c *Client) SpecialistReviews(dir, baseBranch string, changedFiles []string, pats []patterns.Pattern, maxPatterns int) ([]implement.SpecialistResult, error) {
	results, statuses := RunSpecialistFanOut(changedFiles, func(sp Specialist) (*report.ReviewResult, error) {
		return c.SpecialistReview(dir, baseBranch, sp, pats, maxPatterns)
	})
	out := make([]implement.SpecialistResult, len(Specialists))
	for i, sp := range Specialists {
		out[i] = implement.SpecialistResult{Key: sp.Key, Result: results[i], Status: statuses[i]}
	}
	return out, nil
}

// RunSpecialistFanOut is the gate/dispatch/collect core the review and
// implement commands share. It runs call(sp) concurrently for every specialist
// whose ShouldRun(changedFiles) is true, skips the rest (adaptive gating), and
// drops a specialist whose call errors (logged with the full error, non-fatal).
// Both returned slices are indexed like Specialists. A nil result is a
// specialist that was gated out or failed, which the callers skip at merge, and
// its status says which: every status carries sp.PassLabel() and ran,
// gated-out, or failed with the error's first line (decision 135).
func RunSpecialistFanOut(changedFiles []string, call func(Specialist) (*report.ReviewResult, error)) ([]*report.ReviewResult, []report.PassStatus) {
	results := make([]*report.ReviewResult, len(Specialists))
	statuses := make([]report.PassStatus, len(Specialists))
	var g errgroup.Group
	running := 0
	for i, sp := range Specialists {
		if !sp.ShouldRun(changedFiles) {
			// Adaptive gating: the diff does not touch this specialist's relevant
			// paths, so running it would only add cost.
			slog.Info("skipping specialist; diff does not touch its relevant paths", "specialist", sp.Key)
			statuses[i] = report.PassStatus{Pass: sp.PassLabel(), Status: report.PassStatusGatedOut}
			continue
		}
		running++
		g.Go(func() error {
			// Each goroutine writes only its own index, and Wait orders the
			// writes before the caller reads them.
			res, err := call(sp)
			statuses[i] = report.NewPassStatus(sp.PassLabel(), err)
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
	return results, statuses
}

// SpecialistReview runs a single domain-focused review pass over the diff and
// returns its findings, tagged with the specialist's pattern. baseBranch scopes
// the review to changes relative to that branch. pats is the project
// review-pattern catalog (maxPatterns budgets how many are rendered), injected
// so the specialist applies the patterns that fall inside its domain — narrowed
// to sp.Areas when it declares them; an empty catalog leaves the prompt
// unchanged. The pass reads a diff it writes first (runDiffFinder), and its
// result lists in ExcludedFiles what that diff left out (decision 137). A
// Design specialist, when the operator loaded the impeccable skill, may also
// read the skill's reference documents, and its prompt carries them and the
// detector's leads (designGuidance, decision 155).
func (c *Client) SpecialistReview(dir, baseBranch string, sp Specialist, pats []patterns.Pattern, maxPatterns int) (*report.ReviewResult, error) {
	var readDirs []string
	if sp.Design && c.hasDesignKit {
		readDirs = []string{c.designKit.ReferenceDir()}
	}
	raw, model, excluded, err := c.runDiffFinderReading(dir, baseBranch, "", sp.PassLabel(), readDirs, func(d diffscope.Diff) string {
		design := ""
		if sp.Design {
			design = c.designGuidance(dir, baseBranch, d)
		}
		return specialistPrompt(baseBranch, d, sp, pats, maxPatterns, design)
	})
	if err != nil {
		return nil, fmt.Errorf("running %s specialist review: %w", sp.Key, err)
	}
	res, err := c.finishReview(raw, model, sp.Key+" specialist review", "specialist:"+sp.Key)
	if err != nil {
		return nil, err
	}
	res.ExcludedFiles = excluded
	return res, nil
}

// buildSpecialistPrompt renders the prompt of sp's pass over the diff d,
// without the design section a Design specialist carries at run time.
// ReviewPassPrompts sizes the dry run's specialist rows with it.
func buildSpecialistPrompt(baseBranch string, d diffscope.Diff, sp Specialist, pats []patterns.Pattern, maxPatterns int) string {
	return specialistPrompt(baseBranch, d, sp, pats, maxPatterns, "")
}

// specialistPrompt is buildSpecialistPrompt with design, the rendered
// designGuidanceBlock, after the pattern catalog; "" leaves it out.
func specialistPrompt(baseBranch string, d diffscope.Diff, sp Specialist, pats []patterns.Pattern, maxPatterns int, design string) string {
	if baseBranch == "" {
		baseBranch = DefaultBaseBranch
	}
	var sb strings.Builder

	fmt.Fprintf(&sb, "You are a %s specialist performing a focused code review. Review ONLY your domain.\n\n", sp.Key)
	sb.WriteString(diffScopeLines(baseBranch, d))
	fmt.Fprintf(&sb, `Then review the added and modified lines in those files. A finding's cause must be in the diff; its evidence may sit in unchanged code the change now affects (a consumer it breaks, a routine a new caller makes slow), so quote that file and line.

## Your domain (%s)
%s

If your domain has no issues in this diff, return an empty findings array.

`, sp.Key, sp.Focus)

	sb.WriteString(domainPatternCatalog("## Project review patterns\n\nApply the project review patterns below that fall inside your domain — they ground your pass in the same catalog a later review of this diff would apply. They do NOT widen your scope beyond the domain above.", pats, maxPatterns, sp.Areas))
	sb.WriteString(design)

	sb.WriteString(communicationStyleBlock())
	sb.WriteString(outputLanguageBlock())

	sb.WriteString(`## Finding Enrichment

For EVERY finding, include: a code snippet (the exact problematic lines from the diff) and a concrete suggested fix.

`)
	sb.WriteString(severityLadderBlock(scopeDiff))
	sb.WriteString(findingLabelsBlock())
	sb.WriteString(planwerkIgnoreLine())
	sb.WriteString(passSummaryLine())
	sb.WriteString(findingsOutputBlock())

	return sb.String()
}
