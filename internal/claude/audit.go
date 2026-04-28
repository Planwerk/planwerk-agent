package claude

import (
	"fmt"
	"strings"

	"github.com/planwerk/planwerk-review/internal/audit"
	"github.com/planwerk/planwerk-review/internal/patterns"
	"github.com/planwerk/planwerk-review/internal/report"
)

// Audit performs a full-codebase audit against loaded review patterns.
// It runs two Claude calls:
//  1. Audit the codebase against all patterns and emit an unstructured finding list.
//  2. Structure the output into JSON matching report.ReviewResult.
func Audit(dir string, ctx audit.AuditContext) (*report.ReviewResult, error) {
	rawAudit, err := runClaude(dir, buildAuditPrompt(ctx), "audit")
	if err != nil {
		return nil, fmt.Errorf("running audit: %w", err)
	}

	result, err := structureReview(rawAudit)
	if err != nil {
		return nil, fmt.Errorf("structuring audit output: %w", err)
	}

	assignIDs(result)
	return result, nil
}

// buildAuditPrompt constructs a prompt that applies all loaded review patterns
// to the entire codebase and reports concrete improvement findings.
func buildAuditPrompt(ctx audit.AuditContext) string {
	var sb strings.Builder

	// Staff Engineer persona (same cognitive frame as /review, applied to the whole codebase)
	sb.WriteString(`You are a Staff Engineer performing a comprehensive codebase audit. Apply these thinking patterns:
- "What happens at 10x scale?" — Consider load, data volume, and concurrent users
- "What's the blast radius?" — If this code fails, what else breaks?
- "What happens at 3am?" — Is the error path clear? Will oncall understand the logs?
- "Would a new team member understand this?" — Is the intent clear from the code?
- "Where are the tests?" — Does every behavior have a test?
- "Would I find this in the docs?" — Can a new user/developer discover this feature or API from the documentation?

`)

	if ctx.RepoName != "" {
		fmt.Fprintf(&sb, "Repository: %s\n\n", ctx.RepoName)
	}

	// Review patterns — grouped by category, severity-budgeted
	sb.WriteString("## Review Patterns to Apply\n\n")
	sb.WriteString("Apply EVERY pattern below to the whole codebase. For each concrete violation you find, emit a finding and set the \"pattern\" field to the pattern name.\n\n")
	sb.WriteString("<review-patterns>\n")
	sb.WriteString(patterns.FormatGroupedForPrompt(ctx.Patterns, ctx.MaxPatterns))
	sb.WriteString("</review-patterns>\n\n")

	// Audit methodology
	sb.WriteString(`## Audit Methodology

This is NOT a pull-request review — there is no diff. Audit the ENTIRE current state of the codebase.

1. First, walk the repository structure to understand the project (language, layout, entry points, tests, docs).
2. For EACH review pattern above, scan the codebase for violations. Reference the pattern by name in the "pattern" field of each finding.
3. Beyond the patterns, also report any CRITICAL or BLOCKING issues you encounter (security holes, data-loss risks, missing error handling on hot paths) even if no pattern covers them.
4. Cite concrete file paths and line numbers for every finding. If you cannot cite a line, do not report it.
5. Group duplicate violations: if the same pattern is violated in many places, pick the 3-5 most representative instances and list the remaining files in the "action" field rather than creating dozens of near-identical findings.

`)

	// Test & Documentation verification
	sb.WriteString(`## Test & Documentation Completeness

Audit the project's existing test and documentation coverage:

1. Identify ALL test conventions the project uses: unit tests, integration tests, E2E tests (e2e/, chainsaw/, .chainsaw/, chainsaw-test.yaml, kuttl, Helm chart tests).
2. For core features with significant logic, check whether matching tests exist. If a feature lacks tests while comparable features are tested, flag as WARNING titled "Missing Tests: <feature/file>".
3. If the project has E2E tests for some features but not others, flag as WARNING titled "Missing E2E Tests: <feature>".
4. For every public API, CLI flag, configuration option, or user-facing feature, check whether it is documented (README, CHANGELOG, doc comments). Flag undocumented items as WARNING titled "Missing Documentation: <item>" or "Undocumented Flag/Config: <name>".
5. Do NOT flag missing tests for trivial getters/setters or missing docs for private/internal functions.

## Documentation Structure & Quality

Apply the ` + "`Documentation Structure (Diátaxis)`" + ` review pattern to every documentation-like file (` + "`*.md`, `*.rst`, `*.adoc`" + `, anything under ` + "`docs/`, `README*`, `CHANGELOG*`" + `) AND every in-code documentation comment (godoc, docstrings, JSDoc, rustdoc):

1. For each page with a clear intended Diátaxis mode (Tutorial / How-To / Reference / Explanation), flag drift between claimed mode and actual content as WARNING titled "Diátaxis Drift: <page> mixes <claimed-mode> with <actual-mode>".
2. Flag doc-comments that paraphrase the code instead of explaining intent (WHY) as INFO titled "Comment Restates Code: <file>:<line>".
3. Flag stale code examples in documentation (signatures, flags, defaults that no longer match the source) as CRITICAL titled "Stale Doc Example: <file>:<approx line>".
4. Flag removed or renamed public APIs, CLI flags, or config keys without an explicit deprecation block as WARNING titled "Missing Deprecation Notice: <name>".
5. Flag terminology drift (one concept named two different ways across the docs) as INFO titled "Terminology Drift: <term-A>/<term-B>".
6. Flag broken internal anchors and obviously dead external links as INFO titled "Broken Doc Link: <file>".

`)

	// Dependency freshness
	sb.WriteString(`## Dependency Freshness

Scan declared dependencies (go.mod, package.json, requirements.txt, pyproject.toml, Cargo.toml, pom.xml, GitHub Actions workflow files, Dockerfiles, Helm Chart.yaml):

- Flag deprecated dependencies as CRITICAL titled "Deprecated Dependency: <name>".
- Flag unmaintained (archived/abandoned) dependencies as CRITICAL titled "Unmaintained Dependency: <name>".
- Flag significantly outdated versions as WARNING titled "Outdated Dependency: <name> uses <version>, latest is <latest>".
- Include the recommended replacement or current version in the action.

`)

	// Suppressions
	sb.WriteString(`## Suppressions — DO NOT flag these

- TODO/FIXME comments that reference an issue tracker (e.g. TODO(#123))
- Missing tests for trivial getters/setters, simple delegation methods, or configuration constants
- Import ordering or formatting differences (handled by formatters)
- Variable naming that follows the project's existing conventions
- Missing documentation on unexported/private functions or internal implementation details
- Minor style preferences that don't affect correctness or readability
- Redundancy that aids readability (defense in depth)
- Consistency-only suggestions with no correctness impact
- Speculative "consider using X library" suggestions when the current approach works — this does NOT suppress flagging deprecated, unmaintained, or severely outdated dependencies

`)

	// Anti-hallucination rules
	sb.WriteString(`## Verification of Claims

These rules are MANDATORY. Violating them produces a misleading audit.

- NEVER say "this is probably tested" — name the specific test file and test function, or flag as "test coverage unknown".
- NEVER say "this is handled elsewhere" — cite the exact file and line that handles it, or say "not verified".
- NEVER assume error handling exists unless you can see it in the code.
- If uncertain, set confidence to "uncertain" and prefix the problem description with "UNVERIFIED:".
- Every finding MUST cite a concrete file path. Line numbers are required unless the finding concerns a missing file (e.g. "no CHANGELOG.md").

`)

	// Anti-sycophancy
	sb.WriteString(`## Communication Style

Be direct and decisive. Do NOT hedge:
- Do NOT write "you might want to consider..." — state what IS wrong
- Do NOT write "this could potentially cause..." — state what WILL happen
- Take a clear position on every finding. If something is fine, do not mention it at all.

`)

	// Finding enrichment
	sb.WriteString(`## Finding Enrichment

For EVERY finding you report, you MUST include:

1. **Code Snippet**: Quote the exact 3-5 lines of problematic code. Preserve original indentation.
2. **Suggested Fix**: For auto-fix findings, provide the EXACT replacement code (no markdown fences, no comments, preserve indentation, no placeholders). For needs-discussion and architectural findings, describe the fix approach concretely.
3. **Line Range**: Specify start and end line when the finding spans multiple lines.
4. **Confidence Level**: "verified" (visible in code), "likely" (strong evidence, depends on wider context), or "uncertain" (requires further investigation).
5. **Related Findings**: Reference related findings by their exact title.
6. **Pattern**: If the finding violates one of the review patterns above, set "pattern" to the exact pattern name.

`)

	// Finding limit
	if ctx.MaxFindings > 0 {
		fmt.Fprintf(&sb, "## Finding Budget\n\nReport at most %d findings. Prioritize BLOCKING > CRITICAL > WARNING > INFO. If more exist, keep the highest-severity and most representative ones.\n\n", ctx.MaxFindings)
	}

	// Summary instructions
	sb.WriteString(`## Audit Summary

At the end of your audit, write a brief overall summary (3-5 sentences) that:
1. States the overall health of the codebase.
2. Highlights the most important findings (top themes, not a list of every issue).
3. Names the 1-3 highest-leverage improvements the team should tackle first.
Keep it balanced and constructive.

`)

	sb.WriteString("Now perform the audit. When you are done, emit a comprehensive list of findings with the enrichment fields above, followed by the audit summary.\n")

	return sb.String()
}
