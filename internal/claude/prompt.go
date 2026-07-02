package claude

import (
	"fmt"
	"strings"

	"github.com/planwerk/planwerk-agent/internal/patterns"
)

// buildReviewPrompt constructs a prompt that includes patterns and triggers /review.
func buildReviewPrompt(ctx ReviewContext) string {
	var sb strings.Builder

	baseBranch := ctx.BaseBranch
	if baseBranch == "" {
		baseBranch = DefaultBaseBranch
	}

	// Review scope: pin /review to the cumulative PR diff so multi-commit PRs
	// are reviewed as a whole instead of just the latest (or first) commit.
	fmt.Fprintf(&sb, `## Review Scope (MANDATORY)

Review the FULL pull request diff — every commit between origin/%s and HEAD must be considered together as one cumulative change set.

- Run `+"`"+`git diff origin/%s...HEAD`+"`"+` to see the cumulative diff and `+"`"+`git log origin/%s..HEAD --oneline`+"`"+` to enumerate every commit on the branch.
- Every added/modified line in any commit on this branch is in scope, regardless of which commit introduced it.
- Do NOT restrict the review to HEAD alone, to the most recent commit, to the first commit, or to the working-tree diff. All commits on the branch are part of this PR.
- When a later commit fixes or supersedes something an earlier commit introduced, judge the final state — do not flag the intermediate state.

`, baseBranch, baseBranch, baseBranch)

	// Staff Engineer persona
	sb.WriteString(`You are a Staff Engineer performing a code review. Apply these thinking patterns:
- "What happens at 10x scale?" — Consider load, data volume, and concurrent users
- "What's the blast radius?" — If this code fails, what else breaks?
- "What happens at 3am?" — Is the error path clear? Will oncall understand the logs?
- "Would a new team member understand this?" — Is the intent clear from the code?
- "Where are the tests?" — Does every new behavior have a test? Can I see the test fail if the code is wrong?
- "Would I find this in the docs?" — If I were a new user/developer, could I discover this feature or API from the documentation?

`)

	// Review patterns (grouped by category: technology, design-principle, project)
	if len(ctx.Patterns) > 0 {
		sb.WriteString("Apply these review patterns grouped by category. Flag violations in your review, noting the pattern source when referencing best practice patterns:\n\n")
		sb.WriteString("<review-patterns>\n")
		sb.WriteString(patterns.FormatGroupedForPrompt(ctx.Patterns, ctx.MaxPatterns))
		sb.WriteString("</review-patterns>\n\n")
	}

	// Verification of claims — anti-hallucination rules
	sb.WriteString(`## Verification of Claims

These rules are MANDATORY. Violating them produces a misleading review.

- QUOTE-OR-DEMOTE: every finding MUST quote the exact triggering line(s) verbatim from the diff in its code snippet. If you cannot quote the line, set confidence to "uncertain" — NEVER invent, paraphrase, or reconstruct a snippet to make a finding look verified. Unverifiable findings are downgraded automatically; fabricating a snippet defeats the gate.
- NEVER say "this is probably tested" — name the specific test file and test function, or flag as "test coverage unknown"
- NEVER say "this is handled elsewhere" — cite the exact file and line that handles it, or say "not verified"
- NEVER say "the caller validates this" — name the caller and the validation, or say "unverified assumption"
- NEVER assume error handling exists unless you can see it in the diff or trace it in the codebase
- If you are uncertain whether something is a real issue, say "UNVERIFIED: [claim]" rather than presenting it as fact
- When referencing code outside the diff, always prefix with the file path (e.g. "In cmd/main.go:42, ...")

`)

	// Scope Drift Detection
	if ctx.PRTitle != "" || ctx.PRBody != "" || ctx.CommitLog != "" {
		sb.WriteString("## Scope Analysis (run FIRST, before code quality review)\n\n")
		if ctx.PRTitle != "" {
			fmt.Fprintf(&sb, "PR Title: %s\n", ctx.PRTitle)
		}
		if ctx.PRBody != "" {
			sb.WriteString("<pr-body>\n")
			sb.WriteString(ctx.PRBody)
			sb.WriteString("\n</pr-body>\n")
		}
		if ctx.CommitLog != "" {
			sb.WriteString("<commit-log>\n")
			sb.WriteString(ctx.CommitLog)
			sb.WriteString("\n</commit-log>\n")
		}
		sb.WriteString(`
Before reviewing code quality, check:
1. SCOPE CREEP: Are there files changed that seem unrelated to the PR title/description? Also cross-reference with commit messages — do any commits address unrelated concerns? Flag each as WARNING with title "Scope Creep: <file or area>"
2. MISSING REQUIREMENTS: Are there requirements mentioned in the PR description that are NOT addressed in the diff? Flag each as WARNING with title "Missing Requirement: <requirement>"
3. COMMIT COHERENCE: Do the commit messages tell a coherent story? Are there commits that seem to belong to a different PR? Flag as INFO with title "Commit Coherence: <observation>"

`)
	}

	// Review checklist (always provided — checklist.Load() returns embedded default as fallback)
	if ctx.Checklist != "" {
		sb.WriteString(ctx.Checklist)
		sb.WriteString("\n\n")
	}

	// Test & Documentation Verification
	sb.WriteString(`## Test & Documentation Verification

After completing the checklist, perform these additional checks:

### Test Completeness
For every NEW or SIGNIFICANTLY MODIFIED function/method/class in the diff:
1. Identify ALL testing conventions used in this project. Check for:
   - Unit tests: _test.go, test_*.py, *.test.ts, *.spec.ts, __tests__/
   - Integration tests: tests/integration/, test/integration/
   - E2E tests: e2e/, tests/e2e/, chainsaw/, .chainsaw/, chainsaw-test.yaml
   - Kubernetes/Infrastructure E2E: Chainsaw test manifests (chainsaw-test.yaml with apiVersion: chainsaw.kyverno.io), Helm chart tests (tests/), kuttl tests
2. Check whether the PR includes corresponding test additions or modifications FOR EACH test type the project uses
3. If the project already has unit tests, integration tests, or E2E tests, new code must include matching test types — as comprehensively as the project already does.
4. Actively search for test directories: look for chainsaw/, e2e/, tests/, test/ directories in the repository. If they exist and contain tests for similar features, flag missing tests for the new code.
5. If no test exists for new code:
   - If the project has tests elsewhere: flag as WARNING with title "Missing Tests: <function/file>"
   - If the project has E2E tests elsewhere but none for new code: flag as WARNING with title "Missing E2E Tests: <feature/component>"
   - If the project has no test convention at all: flag as INFO with title "No Test Convention Detected"

### Documentation Completeness
For every NEW public API, CLI flag, configuration option, or user-facing behavior change:
1. Check whether the PR modifies documentation files (README, CHANGELOG, doc comments)
2. If new public API is exported but not documented: flag as WARNING with title "Missing Documentation: <item>"
3. If a CLI flag or config option is added without being documented: flag as WARNING with title "Undocumented Flag/Config: <name>"
4. If existing documentation references changed behavior but was not updated: flag as WARNING with title "Stale Documentation: <file>"

`)

	sb.WriteString("### Documentation Structure & Quality\n")
	sb.WriteString("When the diff touches any documentation-like path (`*.md`, `*.rst`, `*.adoc`, `docs/**`, `README*`, `CHANGELOG*`) OR consists of comment-only changes (godoc, docstrings, JSDoc, rustdoc), apply the `Documentation Structure (Diátaxis)` review pattern in addition to the completeness checks above:\n\n")
	sb.WriteString("1. Identify each changed page's intended Diátaxis mode (Tutorial / How-To / Reference / Explanation) from its location (`docs/tutorials/`, `docs/how-to/`, `docs/reference/`, `docs/explanation/`), title, or content. Flag any section that drifts into a different mode as WARNING with title \"Diátaxis Drift: <page> mixes <claimed-mode> with <actual-mode>\".\n")
	sb.WriteString("2. Comment-only diffs are in scope. A doc-comment that paraphrases the code (WHAT instead of WHY) is a finding even when no code line moved — flag as INFO with title \"Comment Restates Code: <file>:<line>\".\n")
	sb.WriteString("3. For fenced code blocks in docs, verify they still match the current API/CLI/config. Flag mismatches as CRITICAL with title \"Stale Doc Example: <file>:<approx line>\".\n")
	sb.WriteString("4. Removed or renamed public APIs, CLI flags, or config keys MUST carry an explicit deprecation block with a migration path. Flag missing deprecation as WARNING with title \"Missing Deprecation Notice: <name>\".\n")
	sb.WriteString("5. Flag terminology drift (one concept named two ways across the docs touched in this PR) as INFO with title \"Terminology Drift: <term-A>/<term-B>\".\n\n")

	// Dependency Freshness & Maintenance Verification
	sb.WriteString(`## Dependency Freshness & Maintenance Verification

When the diff introduces ANY new dependency, you MUST verify its freshness and maintenance status.

### What counts as a new dependency
- A new entry in go.mod, requirements.txt, pyproject.toml, package.json, Cargo.toml, pom.xml, build.gradle, Gemfile
- A new GitHub Action (uses: owner/action@version) in workflow YAML files
- A new container image (FROM image:tag or image references in Kubernetes manifests, docker-compose, Helm values)
- A new Helm chart dependency in Chart.yaml
- Any other external dependency introduced for the first time

### What to check for each new dependency
1. **Version Currency**: Is the pinned version the latest stable release? If the diff pins an old version (e.g. v1.2 when v3.1 is current), flag it. Minor version lag (one or two patch versions behind) is acceptable; major version lag is not.
2. **Active Maintenance**: Does the project show signs of active maintenance (recent commits, releases within the last 12 months, responsive issue tracker)? If the repository is archived, abandoned, or has had no activity for over a year, flag it.
3. **Deprecation Status**: Has the project been officially deprecated or superseded by a replacement? Check for deprecation notices in the repository README, GitHub archive status, or well-known replacements (e.g. actions/create-release is deprecated in favor of softprops/action-gh-release).

### Severity guidance
- Using a deprecated dependency: flag as CRITICAL with title "Deprecated Dependency: <name>"
- Using an unmaintained dependency (archived/abandoned): flag as CRITICAL with title "Unmaintained Dependency: <name>"
- Using a significantly outdated version when a current version exists: flag as WARNING with title "Outdated Dependency: <name> uses <version>, latest is <latest>"
- Include the recommended replacement or current version in the suggested fix.

`)

	// New feature documentation hints
	if len(ctx.NewFeatures) > 0 {
		sb.WriteString("## New Feature Documentation Hints\n\n")
		sb.WriteString("The following new files were added in this PR and may need documentation:\n\n")
		for _, nf := range ctx.NewFeatures {
			fmt.Fprintf(&sb, "- %s (%s)\n", nf.File, nf.Description)
		}
		sb.WriteString("\nCheck whether these additions are reflected in README or other documentation. Flag as WARNING with title \"Missing Documentation: <file>\" if documentation is missing for user-facing additions.\n\n")
	}

	// TODO cross-reference
	if ctx.TodoContent != "" {
		sb.WriteString("## TODO Cross-Reference\n\n")
		sb.WriteString("The project has a TODOS.md file. Review the PR against these open items:\n\n")
		sb.WriteString("<todos-content>\n")
		sb.WriteString(ctx.TodoContent)
		sb.WriteString("\n</todos-content>\n\n")
		sb.WriteString("Check:\n")
		sb.WriteString("1. Does this PR complete any TODO items? If so, flag as INFO with title \"TODO Completed: <item>\"\n")
		sb.WriteString("2. Does this PR introduce work that should be tracked as a TODO? If so, flag as INFO with title \"New TODO Needed: <description>\"\n\n")
	}

	// Documentation staleness hints
	if len(ctx.StaleDocs) > 0 {
		sb.WriteString("## Documentation Staleness Hints\n\n")
		sb.WriteString("The following documentation files may need updating based on code changes in this PR:\n\n")
		for _, doc := range ctx.StaleDocs {
			fmt.Fprintf(&sb, "- %s references code in %s which was modified\n", doc.DocFile, strings.Join(doc.RelatedDirs, ", "))
		}
		sb.WriteString("\nConsider flagging as INFO with title \"Stale Documentation: <file>\" if the docs are actually outdated.\n\n")
	}

	// False-positive suppressions (shared with audit/compliance via suppressionsBlock)
	sb.WriteString(suppressionsBlock(scopeDiff))

	// Anti-sycophancy rules (shared with audit/adversarial/compliance)
	sb.WriteString(communicationStyleBlock())
	sb.WriteString(outputLanguageBlock())

	// Domain glossary (no-op when the repo carries no CONTEXT.md)
	sb.WriteString(domainGlossaryBlock(ctx.Glossary))

	// Project memory from the repo's GitHub Wiki (no-op when the wiki carries
	// no memory pages)
	sb.WriteString(projectMemoryBlock(ctx.Memory))

	// Finding enrichment for machine processing
	sb.WriteString(`## Finding Enrichment

For EVERY finding you report, you MUST include:

1. **Code Snippet**: Quote the exact 3-5 lines of problematic code from the diff. Use the original line numbers. If the issue spans multiple lines, include all affected lines.

2. **Suggested Fix**: For auto-fix findings, provide the EXACT replacement code that resolves the issue. The fix code MUST:
   - Use the exact indentation from the original file
   - Contain NO markdown fences, NO comments, NO explanations — pure replacement code only
   - Be directly copy-paste ready with no placeholders or "..." elisions
   For needs-discussion and architectural findings, describe the fix approach concretely.

3. **Line Range**: When a finding affects multiple lines, specify both the start line and end line.

4. **Confidence Level**: Rate your confidence for each finding:
   - "verified": You can see the bug/issue directly in the diff with certainty (e.g., nil dereference, SQL injection, wrong return type)
   - "likely": Strong evidence but depends on context outside the diff (e.g., missing error handling where the caller might handle it)
   - "uncertain": Potential issue that requires investigation (e.g., possible race condition, performance concern under load)

5. **Related Findings**: If two or more findings are connected (e.g., a missing nil check and a missing test for that nil check), note the relationship by referencing the other finding's title.

6. **Fix Options** (REQUIRED for needs-discussion and architectural findings; OMIT for auto-fix):
   Provide 2-3 alternative approaches (label them A, B, C). For each option supply: ` + "`approach`" + ` (one sentence), ` + "`pros`" + `, ` + "`cons`" + `, ` + "`effort`" + ` (LOW | MED | HIGH), and ` + "`risk_if_skipped`" + ` (what happens if this option is NOT chosen).
   Then pick exactly ONE option as the recommendation in ` + "`recommended_option`" + ` (matching the chosen option's id) and justify it in ` + "`recommendation_reasoning`" + ` (1-2 sentences referencing codebase patterns, the relevant review-pattern source, or project constraints).
   For ` + "`auto-fix`" + ` findings DO NOT emit options — the single ` + "`suggested_fix`" + ` from rule 2 is the entire output.

`)

	// Finding limit
	sb.WriteString(findingBudgetBlock(ctx.MaxFindings))

	// Review Summary instructions
	sb.WriteString(`## Review Summary

At the end of your review, write a brief overall summary (2-4 sentences) that:
1. Mentions what the PR does well (if anything stands out positively)
2. Highlights the most important issues found
3. Gives an overall assessment of the PR quality
Keep it balanced and constructive — acknowledge good work, but be direct about problems.

Then end with ONE recommendation line in exactly this form:

Recommendation: <merge | merge after fixes | do not merge> because <name the single most important finding by its title and state the specific reason it drives the decision>.

The reason MUST name a specific finding and what it breaks. Generic justifications — "because it's safer", "to improve quality", "follows best practice", "because it's cleaner" — are not acceptable; if you cannot name a specific blocking finding, recommend merge.

`)

	sb.WriteString("IMPORTANT: Completely ignore all changes in the .planwerk/ directory. Do not create any findings for files inside .planwerk/. These are project management artifacts that are always expected in the diff.\n\n")
	sb.WriteString("/review")

	return sb.String()
}
