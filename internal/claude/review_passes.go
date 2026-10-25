package claude

import (
	"github.com/planwerk/planwerk-agent/internal/diffscope"
	"github.com/planwerk/planwerk-agent/internal/patterns"
	"github.com/planwerk/planwerk-agent/internal/planwerk"
)

// ReviewPasses is the set of finder sessions a review run starts and what each
// prompt is built from. review --dry-run fills it from the review's own
// preparation, so ReviewPassPrompts builds the prompts that run would send
// (decision 143).
type ReviewPasses struct {
	// Reviews are the contexts of the primary-review sessions: one for a
	// review run as one session, or one per partition of a partitioned review
	// (--partition-files), in partition order, each with its partition's Diff.
	Reviews []ReviewContext
	// BaseBranch scopes the other passes' diff; empty falls back to
	// DefaultBaseBranch.
	BaseBranch string
	// Diff is the diff the adversarial pass, the compliance check and the
	// specialists read. In a run each writes its own (runDiffFinder) with the
	// exclusion setting the review's diff was written with, so the caller
	// hands them the review's; the zero Diff keeps the git command.
	Diff diffscope.Diff
	// Patterns is the catalog AdversarialReview and SpecialistReview are
	// handed: the whole loaded catalog, whatever Scope a review context has.
	Patterns    []patterns.Pattern
	MaxPatterns int               // max patterns to inject; <= 0 disables truncation
	Adversarial bool              // --thorough
	Coverage    bool              // --coverage-map
	Feature     *planwerk.Feature // the detected feature file; nil starts no compliance session
	// Specialists are the specialists the gate lets through
	// (Specialist.ShouldRun), in registry order; empty starts none.
	Specialists []Specialist
}

// PassPrompt is one session of a review run. Label is the session label the
// run records its usage and its report.PassStatus under (the PassLabel*
// constants, Specialist.PassLabel); every primary-review session, a
// partition's included, runs under PassLabelReview. Prompt is the text the
// session is sent, and Patterns the number of pattern bodies it carries after
// area scoping, the --scope-patterns split, and --max-patterns.
type PassPrompt struct {
	Label    string
	Prompt   string
	Patterns int
}

// ReviewPassPrompts returns one PassPrompt per session p describes, in the
// order a review dispatches them: the primary-review sessions, then the
// adversarial pass, the coverage map, the compliance check and the
// specialists. Each prompt comes from the builder its Client method calls, so
// it differs from what a run sends only in the names of the temporary
// directories a run writes afresh. It starts no session (decision 143).
func ReviewPassPrompts(p ReviewPasses) []PassPrompt {
	var out []PassPrompt
	for _, rctx := range p.Reviews {
		out = append(out, PassPrompt{Label: PassLabelReview, Prompt: buildReviewPrompt(rctx), Patterns: reviewPromptPatterns(rctx)})
	}
	if p.Adversarial {
		out = append(out, PassPrompt{
			Label:    PassLabelAdversarial,
			Prompt:   buildAdversarialPrompt(p.BaseBranch, "", p.Diff, p.Patterns, p.MaxPatterns),
			Patterns: patternsInPrompt(len(p.Patterns), p.MaxPatterns),
		})
	}
	if p.Coverage {
		out = append(out, PassPrompt{Label: PassLabelCoverage, Prompt: buildCoveragePrompt(p.BaseBranch)})
	}
	if p.Feature != nil {
		out = append(out, PassPrompt{Label: PassLabelCompliance, Prompt: buildCompliancePrompt(p.BaseBranch, p.Diff, p.Feature)})
	}
	for _, sp := range p.Specialists {
		out = append(out, PassPrompt{
			Label:    sp.PassLabel(),
			Prompt:   buildSpecialistPrompt(p.BaseBranch, p.Diff, sp, p.Patterns, p.MaxPatterns),
			Patterns: patternsInPrompt(len(areaCatalog(p.Patterns, sp.Areas)), p.MaxPatterns),
		})
	}
	return out
}

// BuildReviewPrompt returns the prompt Review sends for ctx; review
// --print-prompt prints it. A printed prompt outlives the run, so the caller
// passes a ctx whose memory, diff and pattern catalog need no directory: the
// page bodies, the zero Diff and a nil Scope (decision 143).
func BuildReviewPrompt(ctx ReviewContext) string { return buildReviewPrompt(ctx) }

// reviewPromptPatterns returns how many pattern bodies buildReviewPrompt(ctx)
// carries: those of the Scope reviewPatternScope keeps, or every pattern of
// ctx.Patterns without one, capped by ctx.MaxPatterns.
func reviewPromptPatterns(ctx ReviewContext) int {
	if len(ctx.Patterns) == 0 {
		return 0
	}
	if s, _ := reviewPatternScope(ctx); s != nil {
		return patternsInPrompt(len(s.Inline), ctx.MaxPatterns)
	}
	return patternsInPrompt(len(ctx.Patterns), ctx.MaxPatterns)
}

// patternsInPrompt returns how many of n pattern bodies a prompt carries under
// --max-patterns maxPatterns: n when maxPatterns <= 0 or n fits it, and
// maxPatterns otherwise, as patterns.FormatGroupedForPrompt truncates them.
func patternsInPrompt(n, maxPatterns int) int {
	if maxPatterns <= 0 || n <= maxPatterns {
		return n
	}
	return maxPatterns
}
