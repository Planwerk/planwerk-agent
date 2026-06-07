package claude

import (
	"fmt"
	"strings"

	"github.com/planwerk/planwerk-review/internal/patterns"
	"github.com/planwerk/planwerk-review/internal/propose"
)

// Propose invokes Claude to analyze the codebase and generate feature proposals.
// It runs two Claude calls:
//  1. Deep analysis of the codebase, grounded in the loaded review patterns.
//  2. Structuring the analysis into JSON proposals.
func Propose(dir string, ctx propose.AnalysisContext) (*propose.ProposalResult, error) {
	rawAnalysis, err := runAnalysis(dir, ctx)
	if err != nil {
		return nil, fmt.Errorf("running analysis: %w", err)
	}

	result, err := structureProposals(rawAnalysis)
	if err != nil {
		return nil, fmt.Errorf("structuring proposals: %w", err)
	}

	assignProposalIDs(result)
	return result, nil
}

func runAnalysis(dir string, ctx propose.AnalysisContext) (string, error) {
	return runClaude(dir, buildAnalysisPrompt(ctx), "analysis")
}

// buildAnalysisPrompt constructs the deep-analysis prompt. When patterns are
// supplied it injects the grouped pattern catalog so proposals are grounded in
// the same rules audit and review apply, matching buildAuditPrompt /
// buildReviewPrompt.
func buildAnalysisPrompt(ctx propose.AnalysisContext) string {
	var sb strings.Builder

	sb.WriteString(`You are a senior software architect performing a comprehensive codebase analysis. Your goal is to deeply understand this project and generate concrete, actionable feature proposals.

`)

	if ctx.RepoName != "" {
		fmt.Fprintf(&sb, "Repository: %s\n\n", ctx.RepoName)
	}

	if len(ctx.Patterns) > 0 {
		sb.WriteString("## Review Patterns to Ground Proposals In\n\n")
		sb.WriteString("The patterns below are the same catalog used by /review and /audit. Use them as a lens when proposing features or improvements: when a proposal addresses a pattern (closes a gap, hardens against a violation, or extends coverage) reference the pattern by name in the proposal description so reviewers can trace the rationale back to the catalog.\n\n")
		sb.WriteString("<review-patterns>\n")
		sb.WriteString(patterns.FormatGroupedForPrompt(ctx.Patterns, ctx.MaxPatterns))
		sb.WriteString("</review-patterns>\n\n")
	}

	sb.WriteString(`Analyze the entire codebase systematically:

1. **Architecture & Structure**: Understand the overall architecture, module structure, dependencies, and design patterns used.
2. **Code Quality**: Identify areas where code quality could be improved — missing tests, error handling gaps, inconsistencies.
3. **Feature Gaps**: Identify missing features that would make the project more complete, useful, or production-ready.
4. **Developer Experience**: Look for improvements to DX — better CLI output, configuration, documentation, tooling.
5. **Performance & Scalability**: Identify potential bottlenecks or areas where performance could be improved.
6. **Security**: Look for security hardening opportunities.
7. **Testing**: Identify gaps in test coverage and testing strategy.
8. **CI/CD & Operations**: Look for improvements to build, release, and deployment processes.

For each area, think about:
- What exists today and what is missing?
- What would a production-ready version of this project need?
- What would make the biggest impact for users?
- What is achievable with reasonable effort?

Be specific and concrete in your analysis. Reference actual files, functions, and code patterns you observe.
Provide a detailed, structured analysis covering all the areas above.

IMPORTANT: Do NOT just list generic software improvements. Your proposals must be specific to THIS codebase and grounded in what you actually observe in the code.`)

	if len(ctx.Patterns) > 0 {
		sb.WriteString("\n\nWhen a proposal is motivated by a review pattern above, name that pattern in the proposal's description or motivation so the trail from pattern catalog to proposed work is explicit.")
	}

	sb.WriteString("\n\n")
	sb.WriteString(proseStyleBlock())

	return sb.String()
}

func structureProposals(rawAnalysis string) (*propose.ProposalResult, error) {
	text, err := runClaude("", buildProposalStructurePrompt(rawAnalysis), "proposals")
	if err != nil {
		return nil, err
	}
	var result propose.ProposalResult
	if err := decodeJSONWithRepair(text, "structured proposals", &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func buildProposalStructurePrompt(rawAnalysis string) string {
	return `Convert the following codebase analysis into structured JSON feature proposals. Extract every concrete, actionable proposal mentioned.

Output ONLY valid JSON matching this exact schema (no markdown fences, no surrounding text):

{
  "repository_overview": "A concise summary of what this repository is, its tech stack, architecture, and current state of maturity (3-5 sentences).",
  "proposals": [
    {
      "id": "",
      "priority": "HIGH|MEDIUM|LOW",
      "category": "feature|improvement|refactoring|testing|documentation|security|performance",
      "title": "Short, descriptive title suitable as GitHub issue title",
      "description": "Detailed description of what should be implemented or changed. Be specific about the technical approach.",
      "motivation": "Why this proposal matters. What problem does it solve? What value does it add?",
      "scope": "Small|Medium|Large",
      "affected_areas": ["path/to/relevant/file.go", "package/name", "subsystem"],
      "acceptance_criteria": ["Criterion 1", "Criterion 2"]
    }
  ]
}

Priority levels:
- HIGH: Critical for production readiness, security, or core functionality — should be addressed soon
- MEDIUM: Valuable improvements that enhance quality, DX, or capabilities — plan for next iterations
- LOW: Nice-to-have improvements, minor enhancements — consider when time allows

Categories:
- feature: New user-facing functionality
- improvement: Enhancement to existing functionality
- refactoring: Internal code quality improvement
- testing: Test coverage or test infrastructure
- documentation: Documentation improvements
- security: Security hardening
- performance: Performance optimization

Scope:
- Small: < 1 day of work, single file or function changes
- Medium: 1-3 days of work, multiple files or a new module
- Large: > 3 days of work, significant new functionality or architectural changes

Leave the "id" field as an empty string — it will be assigned automatically.
Each proposal should be specific and actionable, referencing actual code areas.
Generate between 5 and 20 proposals, depending on the size and complexity of the codebase.

<analysis-output>
` + rawAnalysis + `
</analysis-output>`
}

func assignProposalIDs(result *propose.ProposalResult) {
	counters := map[string]int{
		"HIGH":   0,
		"MEDIUM": 0,
		"LOW":    0,
	}
	prefixes := map[string]string{
		"HIGH":   "H",
		"MEDIUM": "M",
		"LOW":    "L",
	}

	for i := range result.Proposals {
		prio := strings.ToUpper(result.Proposals[i].Priority)
		result.Proposals[i].Priority = prio
		counters[prio]++
		prefix := prefixes[prio]
		if prefix == "" {
			prefix = "X"
		}
		result.Proposals[i].ID = fmt.Sprintf("%s-%03d", prefix, counters[prio])
	}
}
