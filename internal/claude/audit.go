package claude

import (
	"fmt"
	"strings"

	"github.com/planwerk/planwerk-agent/internal/audit"
	"github.com/planwerk/planwerk-agent/internal/patterns"
	"github.com/planwerk/planwerk-agent/internal/report"
)

// Audit performs a full-codebase audit against loaded review patterns. One
// session audits the codebase and emits its findings as JSON under
// --json-schema with schema.FinderOutput, and finishReview decodes that output
// (decision 109).
func (c *Client) Audit(dir string, ctx audit.AuditContext) (*report.ReviewResult, error) {
	rawAudit, model, err := c.runClaudeFindings(dir, buildAuditPrompt(ctx), "audit", ctx.Memory, ctx.Brain)
	if err != nil {
		return nil, fmt.Errorf("running audit: %w", err)
	}

	return c.finishReview(rawAudit, model, "audit output", "")
}

// buildAuditPrompt constructs a prompt that applies all loaded review patterns
// to the entire codebase and reports concrete improvement findings.
func buildAuditPrompt(ctx audit.AuditContext) string {
	var sb strings.Builder

	// The scope leads: auditing the wrong thing makes every later instruction
	// moot (see "Information hierarchy" in docs/explanation/prompt-design.md).
	sb.WriteString("You are a Staff Engineer performing a codebase audit. This is NOT a pull-request review — there is no diff. Audit the ENTIRE current state of the codebase.\n\n")

	if ctx.RepoName != "" {
		fmt.Fprintf(&sb, "Repository: %s\n\n", ctx.RepoName)
	}

	// Staff Engineer persona (same cognitive frame as the diff review, applied to the whole codebase)
	sb.WriteString(`Apply these thinking patterns:
- "What happens at 10x scale?" — Consider load, data volume, and concurrent users
- "What's the blast radius?" — If this code fails, what else breaks?
- "What happens at 3am?" — Is the error path clear? Will oncall understand the logs?
- "Would a new team member understand this?" — Is the intent clear from the code?
- "Where are the tests?" — Does every behavior have a test?
- "Would I find this in the docs?" — Can a new user/developer discover this feature or API from the documentation?

`)

	// Review patterns — grouped by category, severity-budgeted
	sb.WriteString("## Review Patterns to Apply\n\n")
	sb.WriteString("For each concrete violation you find, emit a finding.\n\n")
	sb.WriteString("<review-patterns>\n")
	sb.WriteString(patterns.FormatGroupedForPrompt(ctx.Patterns, ctx.MaxPatterns))
	sb.WriteString("</review-patterns>\n\n")

	// Project memory from the repo's GitHub Wiki (no-op when the wiki carries
	// no memory pages)
	sb.WriteString(projectMemoryBlock(ctx.Memory))
	sb.WriteString(brainSearchBlock(ctx.Brain))

	// Audit methodology
	sb.WriteString(`## Audit Methodology

1. For EACH review pattern above, scan the codebase for violations.
2. Beyond the patterns, report every defect you find that could cause incorrect behavior, data loss, a security exposure, or a test failure, at the severity the ladder below assigns, even if no pattern covers it.
3. Cite a concrete file path and line for every finding, following the citation rule under "Verification of Claims" below. When you cannot pin a finding to a line, report it with Confidence uncertain and the "UNVERIFIED:" prefix; never invent a location. A missing file is the one exception that rule names: the finding cites the path where the file belongs, with no line, and the search that found nothing in its problem.
4. Group duplicate violations: if the same pattern is violated in many places, pick the 3-5 most representative instances and list the remaining files in the "action" field rather than creating dozens of near-identical findings.

`)

	// Test & Documentation verification
	sb.WriteString(`## Test & Documentation Completeness

Audit the project's existing test and documentation coverage:

1. Identify ALL test conventions the project uses: unit tests, integration tests, E2E tests (e2e/, chainsaw/, .chainsaw/, chainsaw-test.yaml, kuttl, Helm chart tests).
2. For core features with significant logic, check whether matching tests exist. If a feature lacks tests while comparable features are tested, flag as WARNING titled "Missing Tests: <feature/file>".
3. If the project has E2E tests for some features but not others, flag as WARNING titled "Missing E2E Tests: <feature>".
4. For every public API, CLI flag, configuration option, or user-facing feature, check whether it is documented (README, CHANGELOG, doc comments). Flag undocumented items as WARNING titled "Missing Documentation: <item>" or "Undocumented Flag/Config: <name>".

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

These apply the Severity Ladder to dependencies, and the ladder governs where they differ: a deprecated or unmaintained dependency is CRITICAL because it receives no fixes, so a defect in it is one the project cannot patch; an outdated version is WARNING because the fixes it lacks sit on paths the project may not use.
- Flag deprecated dependencies as CRITICAL titled "Deprecated Dependency: <name>".
- Flag unmaintained (archived/abandoned) dependencies as CRITICAL titled "Unmaintained Dependency: <name>".
- Flag significantly outdated versions as WARNING titled "Outdated Dependency: <name> uses <version>, latest is <latest>".
- Include the recommended replacement or current version in the action.

`)

	// Suppressions (shared with review; codebase scope omits diff-only bullets)
	sb.WriteString(suppressionsBlock(scopeCodebase))

	// Anti-hallucination rules
	sb.WriteString(`## Verification of Claims

These rules exist because a reader acts on what the audit claims, and a claim the code does not back misleads them.

- NEVER say "this is probably tested" — name the specific test file and test function, or flag as "test coverage unknown".
- NEVER say "this is handled elsewhere" — cite the exact file and line that handles it, or say "not verified".
- NEVER assume error handling exists unless you can see it in the code.
- If you are not sure a finding is real, report it anyway: state the claim plainly, prefix its problem with "UNVERIFIED:", and set its Confidence to uncertain. An uncertain finding is filed in a separate section, so reporting one costs the reader little and withholding one can cost them the defect.
- Every finding MUST cite a concrete file path. Line numbers are required unless the finding concerns a missing file (e.g. "no CHANGELOG.md").

`)

	// Anti-sycophancy (shared with review/adversarial/compliance)
	sb.WriteString(communicationStyleBlock())
	sb.WriteString(outputLanguageBlock())
	sb.WriteString(codebaseDesignBlock())

	// Finding enrichment
	sb.WriteString(`## Finding Enrichment

For EVERY finding you report, you MUST include:

1. **Code Snippet**: Quote the exact 3-5 lines of problematic code. Preserve original indentation.
2. **Suggested Fix**: For auto-fix findings, provide the EXACT replacement code (no markdown fences, no comments, preserve indentation, no placeholders). For needs-discussion and architectural findings, describe the fix approach concretely.
3. **Line Range**: Specify start and end line when the finding spans multiple lines.
4. **Related Findings**: Reference related findings by their exact title.
5. **Pattern**: If the finding violates one of the review patterns above, set "pattern" to the exact pattern name.

`)
	sb.WriteString(severityLadderBlock(scopeCodebase))
	sb.WriteString(findingLabelsBlock())

	// Finding limit
	sb.WriteString(findingBudgetBlock(ctx.MaxFindings))

	// Summary instructions
	sb.WriteString(`## Audit Summary

In the ` + "`summary`" + ` field, write a brief overall summary (3-5 sentences) that:
1. States the overall health of the codebase.
2. Highlights the most important findings (top themes, not a list of every issue).
3. Names the 1-3 highest-leverage improvements the team should tackle first.

Leave the ` + "`recommendation`" + ` field as the empty string.

`)

	sb.WriteString("Now perform the audit. When you are done, emit the JSON object described under Output below. If a pattern yields no violations, report nothing for it rather than inventing one; if the whole codebase is clean, emit an empty findings list and say so in the summary.\n\n")
	sb.WriteString(findingsOutputBlock())

	return sb.String()
}
