package claude

import (
	"fmt"
	"strings"

	"github.com/planwerk/planwerk-review/internal/implement"
	"github.com/planwerk/planwerk-review/internal/patterns"
	"github.com/planwerk/planwerk-review/internal/report"
)

// VerifyImplementation runs an independent verification pass over the change
// set an implement session just produced, checking it against the issue's
// Acceptance Criteria. It deliberately does NOT trust any implementation
// report: it diffs the feature branch and reads the actual committed code.
// Findings are returned for every criterion that is not fully satisfied.
func VerifyImplementation(dir, issueTitle, issueBody string) (*report.ReviewResult, error) {
	raw, err := runClaudeAuto(dir, buildVerifyImplementationPrompt(issueTitle, issueBody), "verify-implementation")
	if err != nil {
		return nil, fmt.Errorf("running implementation verification: %w", err)
	}
	result, err := structureReview(raw)
	if err != nil {
		return nil, fmt.Errorf("structuring implementation verification: %w", err)
	}
	for i := range result.Findings {
		if result.Findings[i].Pattern == "" {
			result.Findings[i].Pattern = "implementation-verification"
		}
	}
	assignIDs(result)
	return result, nil
}

func buildVerifyImplementationPrompt(issueTitle, issueBody string) string {
	var sb strings.Builder

	sb.WriteString(`You are a Senior Engineer independently verifying that a just-completed implementation satisfies its issue's Acceptance Criteria.

## CRITICAL: Do NOT trust the implementation
The session that wrote this code may have finished suspiciously quickly and its self-report may be optimistic, incomplete, or wrong. Ignore any claims of completion. Verify everything against the ACTUAL committed code.

## Determine the change set
You are inside a checkout currently on the implementation's feature branch.
- Find the base branch: run ` + "`git symbolic-ref refs/remotes/origin/HEAD`" + ` (fall back to origin/main, then origin/master).
- Run ` + "`git diff <base>...HEAD --stat`" + ` and ` + "`git log <base>..HEAD --oneline`" + ` to see what changed.
- Read the actual changed files. Do NOT judge from commit messages alone.

`)

	fmt.Fprintf(&sb, "## Source Issue: %s\n\n<issue-body>\n%s\n</issue-body>\n\n", issueTitle, strings.TrimSpace(issueBody))

	sb.WriteString(`## Your task

Extract EVERY Acceptance Criterion from the issue body. For each one:
1. Search the diff for the concrete code, test, or doc that satisfies it. Cite file:line.
2. Classify it: satisfied (evidence found), partial (some but not all), or missing (no evidence in the diff).
3. Report a finding for every criterion that is NOT fully satisfied. A criterion the implementer would claim is "done" but that you cannot verify in the actual diff is exactly the kind of finding this pass exists to catch.

## Severity

- BLOCKING: a core Acceptance Criterion is missing or contradicted by the implementation.
- CRITICAL: a criterion is only partially met in a way that breaks its stated goal.
- WARNING: a minor criterion gap, or missing tests/docs for an otherwise-implemented criterion.
- INFO: a positive deviation, or a cosmetic mismatch with the spec.

If EVERY criterion is fully satisfied with cited evidence, report an empty findings array.

`)

	sb.WriteString(communicationStyleBlock())
	sb.WriteString(outputLanguageBlock())

	sb.WriteString(`## Verification of Claims (mandatory)

- Cite the exact file:line for every "satisfied" judgment, or downgrade it to partial/missing.
- NEVER say "probably handled" or "likely tested" — find the code/test or call the criterion missing.
- Quote the relevant code (or state "No implementation found") as evidence for every finding.

## Finding Enrichment

For EVERY finding, include: the Acceptance Criterion it concerns (quote it in the problem), a code snippet (the satisfying/contradicting lines, or "No implementation found"), a concrete suggested fix, and a confidence level (verified | likely | uncertain).

IMPORTANT: Completely ignore changes in the .planwerk/ directory.

/review`)

	return sb.String()
}

// Implement runs a fresh Claude Code session inside the given checkout
// directory to implement the elaborated GitHub issue described in ctx. The
// session is responsible for designing the smallest change set that
// satisfies the issue's Acceptance Criteria, writing the code, adding
// tests and documentation, committing on a fresh branch, and opening a
// draft pull request linked to the issue.
//
// runClaudeAuto already creates a fresh `claude -p` invocation per call, so
// every implement call runs in a brand-new Claude session by construction.
// It runs in auto mode (--permission-mode auto) so the session can edit
// files, run tests, commit, push the branch, and open the draft PR without
// an interactive confirmation, while the auto-mode classifier still vets
// each action.
func Implement(dir string, ctx implement.Context) (string, error) {
	out, err := runClaudeAuto(dir, BuildImplementPrompt(ctx), "implement")
	if err != nil {
		return "", fmt.Errorf("running implement: %w", err)
	}
	return sanitizeImplementationReport(out), nil
}

// implementReportHeading is the heading every implementation report opens with
// ("## Implementation Report (issue #N)"). sanitizeImplementationReport anchors
// on this prefix to drop any conversational preamble the model emits before the
// report ("The branch is published. Final report:").
const implementReportHeading = "## Implementation Report"

// sanitizeImplementationReport strips a wrapping markdown fence and any preamble
// the model emits before the "## Implementation Report" heading, so only the
// report itself reaches stdout and the issue comment. The report's "STATUS: ..."
// line survives because it always follows the heading. See sanitizeReport.
func sanitizeImplementationReport(out string) string {
	return sanitizeReport(out, implementReportHeading)
}

// BuildImplementPrompt assembles the prompt for an end-to-end
// implementation session. The full issue body (typically already produced
// by `planwerk-review elaborate`) is embedded inline so Claude does not
// need a second tool call to fetch it. Exported so the implement
// subcommand can render the prompt without invoking Claude
// (--print-prompt mode).
func BuildImplementPrompt(ctx implement.Context) string {
	var sb strings.Builder

	sb.WriteString(`You are a Staff Engineer implementing an elaborated GitHub issue end-to-end inside a fresh checkout of the target repository. The issue body below is the definition of done — treat its Acceptance Criteria as a contract.

`)
	sb.WriteString(baselineBehavioralPrinciples)
	sb.WriteString(outputLanguageBlock())
	sb.WriteString(`Apply these task-specific thinking patterns on top of the baseline above:
- "Read the issue first, in full." — Acceptance Criteria, Non-Goals, Affected Areas, References. Do NOT start editing before you have read every section.
- "Verify the ground truth." — For every file, symbol, package, or migration the issue cites, open the file and confirm it exists and matches the description. If it does not, STOP and report — do not invent code on top of a stale spec.
- "Smallest change that satisfies every Acceptance Criterion." — No speculative scope, no drive-by refactors, no renames the issue did not ask for.
- "Mirror existing conventions." — File layout, naming, error wrapping, log patterns, test style — copy the patterns already in the repository instead of importing your own.
- "Tests are part of the change." — Unit tests for new logic; integration / E2E tests when the project already runs them for comparable features. Every new test must exercise at least one error or edge path (empty/zero-length, nil/absent, an upstream error), not the happy path only. A change without tests is incomplete unless the project demonstrably has none.
- "Documentation is part of the change." — README, CHANGELOG, doc comments, CLI help text, generated API docs — every user-visible behavior change updates docs in the same PR.
- "Commits tell the story." — Stage the work as a sequence of small, reviewable commits; do not produce a single monolithic diff. Write each commit message cleanly: a concise, imperative subject line and, when the change needs it, a body that explains the why. Wrap EVERY line — subject and body alike — at 72 characters or fewer.
- "Self-review before opening the PR." — Walk the diff once more as a reviewer. Reject anything you would push back on.
- "Stay inside the agreed scope." — If the issue's Non-Goals exclude something, do NOT do it.

`)

	fmt.Fprintf(&sb, "## Source Issue\n\n- Repository: %s\n- Issue #%d: %s\n",
		ctx.RepoFullName, ctx.IssueNumber, ctx.IssueTitle)
	if ctx.IssueURL != "" {
		fmt.Fprintf(&sb, "- URL: %s\n", ctx.IssueURL)
	}
	if ctx.IssueState != "" {
		fmt.Fprintf(&sb, "- State: %s\n", ctx.IssueState)
	}
	sb.WriteString("\n<issue-body>\n")
	sb.WriteString(strings.TrimSpace(ctx.IssueBody))
	sb.WriteString("\n</issue-body>\n\n")

	if len(ctx.Patterns) > 0 {
		sb.WriteString("## Project Review Patterns to Honor\n\n")
		sb.WriteString("These patterns are the catalog the project's review/audit/elaborate tools share — including any project-specific patterns shipped under `.planwerk/review_patterns/` in this repository. Treat them as binding constraints on the implementation: every commit you push MUST stay consistent with them. When the change touches an area covered by a pattern, prefer the resolution the pattern endorses.\n\n")
		sb.WriteString("<review-patterns>\n")
		sb.WriteString(patterns.FormatGroupedForPrompt(ctx.Patterns, ctx.MaxPatterns))
		sb.WriteString("</review-patterns>\n\n")
	}

	hasPlan := strings.TrimSpace(ctx.Plan) != ""
	if hasPlan {
		sb.WriteString("## Implementation Plan (from the planning session)\n\n")
		sb.WriteString("A dedicated read-only planning session already grounded this issue in the repository and produced the plan below. Treat it as the default route: adopt its change set, commit sequence, test plan, and documentation plan. Re-verify its Ground-Truth Notes as you work — when the repository contradicts the plan, deviate as narrowly as possible and record the deviation (with rationale) under \"Deviations from the issue\" in your report.\n\n")
		sb.WriteString("<implementation-plan>\n")
		sb.WriteString(strings.TrimSpace(ctx.Plan))
		sb.WriteString("\n</implementation-plan>\n\n")
	}

	sb.WriteString(`## Implementation Workflow

Run these steps in order. Do not skip ahead.

1. READ the issue body in full. Extract Acceptance Criteria, Non-Goals, Affected Areas, and References into your working notes.
2. WALK the repository to ground the issue in reality:
   - Open the README, top-level layout, and any package the issue mentions.
   - For every file the issue cites, open it and confirm it still exists at (or near) the cited path.
   - Identify the project's test conventions (unit, integration, E2E) and where tests live.
   - Identify the project's documentation conventions (README, docs/, CHANGELOG, generated API docs).
`)
	if hasPlan {
		sb.WriteString(`3. VALIDATE the provided implementation plan against what you found in steps 1-2. Adopt its change set and commit sequence; refine them only where the repository contradicts the plan, and note every deviation for the final report.
`)
	} else {
		sb.WriteString(`3. PLAN the smallest change set that satisfies every Acceptance Criterion. Sketch the commit sequence before editing — keep each commit small and reviewable.
`)
	}
	sb.WriteString(`4. CREATE a fresh feature branch off the current default branch. Use a short, descriptive branch name derived from the issue (e.g. "implement/issue-` + fmt.Sprintf("%d", ctx.IssueNumber) + `-<slug>").
5. IMPLEMENT the change set:
   - Match existing layout, naming, error handling, and logging conventions.
   - Add unit tests for new logic, and make every new test exercise at least one error or edge path (empty/zero-length, nil/absent, an upstream error) — not the happy path only. Add integration / E2E tests when the project has them for comparable features.
   - Add or update documentation (README, CHANGELOG, doc comments, CLI help, generated API references) for every user-visible change.
   - Commit in small, reviewable steps with descriptive messages.
6. VERIFY LOCALLY before opening the PR:
   - Run the project's test suite (or the targeted subset that covers the new code).
   - Run lint / vet / formatter / type-checker as the project configures them.
   - Capture the exact commands you ran and their pass/fail status for the report below.
7. SELF-REVIEW the diff against the issue's Acceptance Criteria. Remove anything that is not strictly required. Stop if you have drifted into a Non-Goal.
8. PUSH the branch and OPEN A DRAFT PULL REQUEST linked to issue #` + fmt.Sprintf("%d", ctx.IssueNumber) + `. The PR description must:
   - Link the issue with the GitHub closing keyword "Closes #` + fmt.Sprintf("%d", ctx.IssueNumber) + `" on its own line, so GitHub auto-links the PR to the issue and closes it on merge. This is mandatory. Do NOT use a bare "Implements #` + fmt.Sprintf("%d", ctx.IssueNumber) + `" mention — GitHub only recognizes the closing keywords (close/closes/closed, fix/fixes/fixed, resolve/resolves/resolved), so a plain reference does NOT create the linkage GitHub displays.
   - Walk the reviewer through the change set in commit order.
   - Call out anything that diverged from the issue (and why).
9. OUTPUT the structured implementation report below.

## Implementation Report (final output)

After pushing the branch and opening the draft PR, output a report in this exact shape:

   ## Implementation Report (issue #` + fmt.Sprintf("%d", ctx.IssueNumber) + `)

   ### Acceptance Criteria
   - <criterion verbatim>
     - Status: <satisfied | partial | deferred>
     - Evidence: <file:lines that satisfy it, or the test that exercises it — cite the edge or error test, not a happy-path one, when a new test covers the criterion; or "see PR description">
   ### Commits
   - <sha7> <subject>
   ### Local verification
   - <exact command> — <pass | fail | skipped: reason>
   ### Pull Request
   - URL: <draft PR URL>
   - Branch: <branch name>
   ### Deviations from the issue
   - <one bullet per deviation, with rationale; "none" if there are no deviations>
   ### Status
   STATUS: <DONE | DONE_WITH_CONCERNS | BLOCKED | NEEDS_CONTEXT>
   (DONE = implemented, tested, and PR opened; DONE_WITH_CONCERNS = opened but with reservations a reviewer should see; BLOCKED = could not implement; NEEDS_CONTEXT = the issue is underspecified and a human must clarify.)

## Circuit breakers — stop instead of thrashing

You run fully autonomously, with no human in the loop and a bounded budget, so a thrash loop burns the whole budget before anyone notices. STOP and output the report the moment you detect any of these conditions — do not push through them:
- Fighting the test suite: the same test (or set of tests) keeps failing across repeated, distinct fix attempts and you are not converging. NEVER weaken, skip, or delete the test to go green — that masks the defect instead of fixing it; stop instead.
- Ballooning scope: the change set is growing past the plan and the issue's implied blast radius — new top-level packages or files the issue never asked for — to force something to work.
- Reverting in circles: you have reverted and rewritten the same code more than once without converging on a working change.

When you hit a circuit breaker, halt immediately and emit STATUS: DONE_WITH_CONCERNS when a partial but reviewable change already exists (commit it, push the branch, and open the draft PR so a human can take it from there), or STATUS: BLOCKED when nothing shippable was produced. A stopped run that explains why is worth far more than an exhausted budget.

` + commitTrailerBlock() + attributionFooterBlock() + `## Hard rules

- NEVER skip pre-commit / CI hooks (no --no-verify, no --no-gpg-sign).
- NEVER weaken or delete tests to make the suite green; fix the root cause.
- NEVER widen types to Any/interface{}/unknown to silence the type-checker.
- NEVER suppress lint findings with // nolint, # noqa, # type: ignore, @ts-ignore, etc. unless that suppression is already idiomatic in the same file.
- NEVER add scope the issue did not ask for. Refactors, renames, dependency bumps, formatter sweeps — out of scope unless explicitly listed in Affected Areas.
- NEVER do anything the issue's Non-Goals list excludes.
- NEVER fabricate file paths, symbol names, or migration numbers — open the file before claiming.
- NEVER force-push.
- If the issue is wrong (a cited file does not exist; an Acceptance Criterion is unreachable; the Non-Goals contradict the Description), STOP and post a clarifying comment on the issue instead of inventing scope. Output the report explaining what you did NOT do and why.
- If there is nothing to commit (the issue turns out to already be implemented), do NOT open an empty PR; output the report explaining what you found.
- It is OK to stop and report BLOCKED or NEEDS_CONTEXT. Bad work is worse than no work; escalating is not penalized. Emit the matching STATUS instead of inventing scope or shipping a half-built change.
`)

	return sb.String()
}

// BuildBareImplementPrompt assembles a self-contained implement prompt
// that does NOT embed the issue body. It is meant to be copy-pasted into
// a manual Claude Code session that is ALREADY running inside a checkout
// of the target repository — no clone, no working-tree setup. That session
// fetches the issue itself with the gh CLI and then implements it.
//
// The orchestrator-driven prompt (BuildImplementPrompt) is preferred when
// this tool is driving the session, because it can hand Claude the issue
// body inline. The bare variant trades that convenience for portability:
// the manual session works from the issue reference plus its own checkout.
//
// The orchestrator clones the target repo at prompt-build time so this
// prompt can ship with the detected technology tags AND the tech-filtered
// review-pattern catalog inlined — the manual Claude session does not need
// access to planwerk-review or its pattern dirs.
func BuildBareImplementPrompt(ctx implement.BareContext) string {
	repoFullName := ctx.RepoFullName
	issueNumber := ctx.IssueNumber
	var sb strings.Builder

	sb.WriteString(`You are a Staff Engineer implementing an elaborated GitHub issue end-to-end inside a checkout of the target repository. The issue is the definition of done — treat its Acceptance Criteria as a contract.

`)
	sb.WriteString(baselineBehavioralPrinciples)
	sb.WriteString(outputLanguageBlock())
	sb.WriteString(`Apply these task-specific thinking patterns on top of the baseline above:
- "Read the issue first, in full." — Acceptance Criteria, Non-Goals, Affected Areas, References. Do NOT start editing before you have read every section.
- "Verify the ground truth." — For every file, symbol, package, or migration the issue cites, open the file and confirm it exists and matches the description. If it does not, STOP and report — do not invent code on top of a stale spec.
- "Smallest change that satisfies every Acceptance Criterion." — No speculative scope, no drive-by refactors, no renames the issue did not ask for.
- "Mirror existing conventions." — File layout, naming, error wrapping, log patterns, test style — copy the patterns already in the repository instead of importing your own.
- "Tests are part of the change." — Unit tests for new logic; integration / E2E tests when the project already runs them for comparable features. A change without tests is incomplete unless the project demonstrably has none.
- "Documentation is part of the change." — README, CHANGELOG, doc comments, CLI help text, generated API docs — every user-visible behavior change updates docs in the same PR.
- "Commits tell the story." — Stage the work as a sequence of small, reviewable commits; do not produce a single monolithic diff. Write each commit message cleanly: a concise, imperative subject line and, when the change needs it, a body that explains the why. Wrap EVERY line — subject and body alike — at 72 characters or fewer.
- "Self-review before opening the PR." — Walk the diff once more as a reviewer. Reject anything you would push back on.
- "Stay inside the agreed scope." — If the issue's Non-Goals exclude something, do NOT do it.

`)

	fmt.Fprintf(&sb, "## Source Issue\n\n- Repository: %s\n- Issue #%d\n\n", repoFullName, issueNumber)

	if len(ctx.TechTags) > 0 {
		fmt.Fprintf(&sb, "Detected technologies in the target repo (used to filter the pattern catalog below): %s\n\n",
			strings.Join(ctx.TechTags, ", "))
	}

	sb.WriteString("You are already running inside a checkout of this repository's default branch. Do NOT re-clone. Operate on the working tree you have. You run as a one-shot session: fetch the issue yourself, implement it, push a fresh feature branch, open a draft PR, and report.\n\n")

	sb.WriteString(renderBareCatalog(ctx.PatternCatalog, ctx.HasRepoLocalRefs))

	fmt.Fprintf(&sb, `## Fetch the issue

Do NOT guess the issue contents. Use the GitHub CLI to fetch the full body:

`+"```"+`
gh issue view %d --repo %s --json number,title,body,url,state
`+"```"+`

Read the title, body, and state in full. Extract Acceptance Criteria, Non-Goals, Affected Areas, and References into your working notes.

`, issueNumber, repoFullName)

	sb.WriteString(`## Implementation Workflow

Run these steps in order. Do not skip ahead.

1. READ the issue body in full (from the gh fetch above).
2. WALK the repository to ground the issue in reality:
   - Open the README, top-level layout, and any package the issue mentions.
   - For every file the issue cites, open it and confirm it still exists at (or near) the cited path.
   - Identify the project's test conventions (unit, integration, E2E) and where tests live.
   - Identify the project's documentation conventions (README, docs/, CHANGELOG, generated API docs).
3. PLAN the smallest change set that satisfies every Acceptance Criterion. Sketch the commit sequence before editing — keep each commit small and reviewable.
4. CREATE a fresh feature branch off the current default branch. Use a short, descriptive branch name derived from the issue (e.g. "implement/issue-` + fmt.Sprintf("%d", issueNumber) + `-<slug>").
5. IMPLEMENT the change set:
   - Match existing layout, naming, error handling, and logging conventions.
   - Add unit tests for new logic. Add integration / E2E tests when the project has them for comparable features.
   - Add or update documentation (README, CHANGELOG, doc comments, CLI help, generated API references) for every user-visible change.
   - Commit in small, reviewable steps with descriptive messages.
6. VERIFY LOCALLY before opening the PR:
   - Run the project's test suite (or the targeted subset that covers the new code).
   - Run lint / vet / formatter / type-checker as the project configures them.
   - Capture the exact commands you ran and their pass/fail status for the report below.
7. SELF-REVIEW the diff against the issue's Acceptance Criteria. Remove anything that is not strictly required. Stop if you have drifted into a Non-Goal.
8. PUSH the branch and OPEN A DRAFT PULL REQUEST linked to issue #` + fmt.Sprintf("%d", issueNumber) + `. The PR description must:
   - Link the issue with the GitHub closing keyword "Closes #` + fmt.Sprintf("%d", issueNumber) + `" on its own line, so GitHub auto-links the PR to the issue and closes it on merge. This is mandatory. Do NOT use a bare "Implements #` + fmt.Sprintf("%d", issueNumber) + `" mention — GitHub only recognizes the closing keywords (close/closes/closed, fix/fixes/fixed, resolve/resolves/resolved), so a plain reference does NOT create the linkage GitHub displays.
   - Walk the reviewer through the change set in commit order.
   - Call out anything that diverged from the issue (and why).
9. OUTPUT the structured implementation report below.

## Implementation Report (final output)

After pushing the branch and opening the draft PR, output a report in this exact shape:

   ## Implementation Report (issue #` + fmt.Sprintf("%d", issueNumber) + `)

   ### Acceptance Criteria
   - <criterion verbatim>
     - Status: <satisfied | partial | deferred>
     - Evidence: <file:lines that satisfy it, or test that exercises it, or "see PR description">
   ### Commits
   - <sha7> <subject>
   ### Local verification
   - <exact command> — <pass | fail | skipped: reason>
   ### Pull Request
   - URL: <draft PR URL>
   - Branch: <branch name>
   ### Deviations from the issue
   - <one bullet per deviation, with rationale; "none" if there are no deviations>
   ### Status
   STATUS: <DONE | DONE_WITH_CONCERNS | BLOCKED | NEEDS_CONTEXT>
   (DONE = implemented, tested, and PR opened; DONE_WITH_CONCERNS = opened but with reservations a reviewer should see; BLOCKED = could not implement; NEEDS_CONTEXT = the issue is underspecified and a human must clarify.)

` + commitTrailerBlock() + attributionFooterBlock() + `## Hard rules

- NEVER skip pre-commit / CI hooks (no --no-verify, no --no-gpg-sign).
- NEVER weaken or delete tests to make the suite green; fix the root cause.
- NEVER widen types to Any/interface{}/unknown to silence the type-checker.
- NEVER suppress lint findings with // nolint, # noqa, # type: ignore, @ts-ignore, etc. unless that suppression is already idiomatic in the same file.
- NEVER add scope the issue did not ask for. Refactors, renames, dependency bumps, formatter sweeps — out of scope unless explicitly listed in Affected Areas.
- NEVER do anything the issue's Non-Goals list excludes.
- NEVER fabricate file paths, symbol names, or migration numbers — open the file before claiming.
- NEVER force-push.
- If the issue is wrong (a cited file does not exist; an Acceptance Criterion is unreachable; the Non-Goals contradict the Description), STOP and post a clarifying comment on the issue instead of inventing scope. Output the report explaining what you did NOT do and why.
- If there is nothing to commit (the issue turns out to already be implemented), do NOT open an empty PR; output the report explaining what you found.
- It is OK to stop and report BLOCKED or NEEDS_CONTEXT. Bad work is worse than no work; escalating is not penalized. Emit the matching STATUS instead of inventing scope or shipping a half-built change.
`)

	return sb.String()
}
