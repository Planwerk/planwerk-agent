package claude

import (
	"fmt"
	"strings"

	"github.com/planwerk/planwerk-agent/internal/implement"
	"github.com/planwerk/planwerk-agent/internal/patterns"
	"github.com/planwerk/planwerk-agent/internal/report"
)

// VerifyImplementation runs an independent verification pass over the change
// set an implement session just produced, checking it against the issue's
// Acceptance Criteria. It deliberately does NOT trust any implementation
// report: it diffs the feature branch and reads the actual committed code.
// Findings are returned for every criterion that is not fully satisfied.
func (c *Client) VerifyImplementation(dir, issueTitle, issueBody string) (*report.ReviewResult, error) {
	raw, model, err := c.runClaudeAuto(dir, buildVerifyImplementationPrompt(issueTitle, issueBody), "verify-implementation")
	if err != nil {
		return nil, fmt.Errorf("running implementation verification: %w", err)
	}
	result, err := c.structureReview(raw)
	if err != nil {
		return nil, fmt.Errorf("structuring implementation verification: %w", err)
	}
	for i := range result.Findings {
		if result.Findings[i].Pattern == "" {
			result.Findings[i].Pattern = "implementation-verification"
		}
	}
	assignIDs(result)
	result.Model = model
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

First, if the issue decomposes the work into multiple parts — ` + workBreakdownDefinition() + ` — enumerate EVERY part. For each, search the diff for the code, tests, and docs that deliver it. A listed work package with no implementation in the diff is a BLOCKING finding: a multi-part issue is not done until every part is, and a single-package subset shipped as if it closes the issue is exactly what this pass exists to catch.

Then extract EVERY Acceptance Criterion from the issue body. For each one:
1. Search the diff for the concrete code, test, or doc that satisfies it. Cite file:line.
2. Classify it: satisfied (evidence found), partial (some but not all), or missing (no evidence in the diff).
3. Report a finding for every criterion that is NOT fully satisfied.

## Severity

- BLOCKING: a core Acceptance Criterion is missing or contradicted by the implementation, or a listed work package is entirely absent from the diff.
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

For EVERY finding, include: the Acceptance Criterion it concerns (quote it in the problem), a code snippet (the satisfying/contradicting lines, or "No implementation found"), and a concrete suggested fix.

`)
	sb.WriteString(findingLabelsBlock())
	sb.WriteString(planwerkIgnoreLine())
	sb.WriteString("/review")

	return sb.String()
}

// Implement runs a fresh Claude Code session inside the given checkout
// directory to implement the elaborated GitHub issue described in ctx. The
// session is responsible for designing the smallest change set that
// satisfies the issue's Acceptance Criteria, writing the code, adding
// tests and documentation, and committing on a fresh branch. It does NOT
// open a pull request: the orchestrator runs the simplify and review passes
// over the committed diff first, then a dedicated finalize session opens the
// draft PR so it lands already simplified and self-reviewed.
//
// runClaudeAuto already creates a fresh `claude -p` invocation per call, so
// every implement call runs in a brand-new Claude session by construction.
// It runs in auto mode (--permission-mode auto) so the session can edit
// files, run tests, and commit without an interactive confirmation, while the
// auto-mode classifier still vets each action.
func (c *Client) Implement(dir string, ctx implement.Context) (string, string, error) {
	out, model, err := c.runClaudeAuto(dir, BuildImplementPrompt(ctx), "implement")
	if err != nil {
		return "", "", fmt.Errorf("running implement: %w", err)
	}
	return sanitizeImplementationReport(out), model, nil
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
// by `planwerk-agent elaborate`) is embedded inline so Claude does not
// need a second tool call to fetch it. Exported so the implement
// subcommand can render the prompt without invoking Claude
// (--print-prompt mode).
func BuildImplementPrompt(ctx implement.Context) string {
	var sb strings.Builder

	sb.WriteString(`You are a Staff Engineer implementing an elaborated GitHub issue end-to-end inside a fresh checkout of the target repository. The issue body below is the definition of done — treat its Acceptance Criteria as a contract, and treat the WHOLE issue as one unit of work: when the issue breaks the work into several work packages, implementing it means implementing EVERY package, not the first one and a stop.

`)
	sb.WriteString(baselineBehavioralPrinciples)
	sb.WriteString(outputLanguageBlock())
	sb.WriteString(`You implement and commit the change on a feature branch; you do NOT open a pull request. After you finish, automated simplify and review passes run over your diff, and only then is the pull request opened — so leave the branch committed and report, nothing more.

This is a single, non-interactive, one-shot session: there is NO next turn, no human to hand work back to, and nothing re-invokes you after you stop. Do everything to completion now, within this one response — read the issue, edit, run the tests in the FOREGROUND and wait for them to finish, commit every change, then output the report as the last thing you do. NEVER launch a long-running command (a test run, a build) in the background and then yield to "wait" for it or to be "notified" when it finishes: when this session ends the backgrounded job is killed, its result never arrives, and the work it gated — the next commit, the fix it would have informed — never happens. NEVER defer a step to later ("I'll commit once the tests pass", "waiting for the run to complete before committing"): anything left unfinished when you stop is finished never.

Apply these task-specific thinking patterns on top of the baseline above:
- "Read the issue first, in full." — Acceptance Criteria, Non-Goals, Affected Areas, References. Do NOT start editing before you have read every section.
- "Verify the ground truth." — For every file, symbol, package, or migration the issue cites, open the file and confirm it exists and matches the description. If it does not, STOP and report — do not invent code on top of a stale spec.
- "Implement EVERY work package — the whole issue is the contract." — When the issue decomposes the work into multiple parts — ` + workBreakdownDefinition() + ` — you must implement ALL of them in this session, each with its own deliverables (the unit / integration / e2e tests and docs the package calls for). Implementing only the first package or two and stopping is an INCOMPLETE implementation, not a "smaller" one. There is no later session to pick up the rest: whatever you leave unimplemented stays unimplemented.
- "Smallest change that satisfies every Acceptance Criterion." — "Smallest" governs HOW each part is built — no speculative scope, no drive-by refactors, no renames the issue did not ask for — NOT HOW MANY of the issue's listed parts you implement.
- "Mirror existing conventions." — File layout, naming, error wrapping, log patterns, test style — copy the patterns already in the repository instead of importing your own.
- "Tests are part of the change." — Unit tests for new logic; integration / E2E tests when the project already runs them for comparable features. Every new test must exercise at least one error or edge path (empty/zero-length, nil/absent, an upstream error), not the happy path only. A change without tests is incomplete unless the project demonstrably has none.
- "Documentation is part of the change." — README, CHANGELOG, doc comments, CLI help text, generated API docs — every user-visible behavior change updates docs in the same change set.
- "Commits tell the story." — Stage the work as a sequence of small, reviewable commits; do not produce a single monolithic diff. Write each commit message cleanly: a concise, imperative subject line and, when the change needs it, a body that explains the why. Wrap EVERY line — subject and body alike — at 72 characters or fewer.
- "Self-review before you hand off." — Walk the diff once more as a reviewer. Reject anything you would push back on.
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

1. READ the issue body in full. Extract Acceptance Criteria, Non-Goals, Affected Areas, References, and — when present — the Work breakdown (every work package / work item / numbered or lettered part, with each part's own deliverables) into your working notes. The set of work packages is your checklist: none is done until all are done.
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
   - Add unit tests for new logic, and make every new test exercise at least one error or edge path — not the happy path only. Add integration / E2E tests when the project has them for comparable features.
   - Add or update documentation (README, CHANGELOG, doc comments, CLI help, generated API references) for every user-visible change.
   - Commit in small, reviewable steps with descriptive messages.
6. VERIFY LOCALLY before you hand off:
   - Run every command in the FOREGROUND and wait for it to finish before the next step — never background a test or build run and move on. You need its real exit status in hand to commit and to fill in the report; a backgrounded run's result never reaches this one-shot session.
   - Run the project's test suite (or the targeted subset that covers the new code).
   - Run lint / vet / formatter / type-checker as the project configures them.
   - Capture the exact commands you ran and their pass/fail status for the report below.
7. SELF-REVIEW the diff against the issue's Acceptance Criteria. Remove anything that is not strictly required. Stop if you have drifted into a Non-Goal.
8. STOP after committing on the feature branch. Do NOT push and do NOT open a pull request — automated simplify and review passes run over your diff next, and a dedicated finalize step opens the draft PR (linking the issue with "Closes #` + fmt.Sprintf("%d", ctx.IssueNumber) + `") once they are done. Leave the branch checked out with your commits on it.
9. OUTPUT the structured implementation report below.

## Implementation Report (final output)

ALWAYS end the session with this report — it is mandatory and is the last thing you output, even if you stopped early or hit a circuit breaker below. A session that ends without it (a bare summary, a "waiting for the tests to finish" note, anything missing the heading and a terminal STATUS line) is treated by the orchestrator as a failed, unfinished implementation: the run is aborted and no pull request is opened. After committing on the feature branch, output a report in this exact shape:

   ## Implementation Report (issue #` + fmt.Sprintf("%d", ctx.IssueNumber) + `)

   ### Work Breakdown Coverage
   - <work package / work item, verbatim title or number> — <done | partial | not started> — <evidence: the commits, files, and tests that deliver it, including its own tests and docs>
   - (List EVERY work package the issue breaks the work into. Write "None — the issue is a single undivided change" when the issue has no multi-part breakdown. STATUS: DONE is only legitimate when every package here is "done".)
   ### Acceptance Criteria
   - <criterion verbatim>
     - Status: <satisfied | partial | deferred>
     - Evidence: <file:lines that satisfy it, or the test that exercises it — cite the edge or error test, not a happy-path one, when a new test covers the criterion>
   ### Commits
   - <sha7> <subject>
   ### Local verification
   - <exact command> — <pass | fail | skipped: reason>
   ### Branch
   - <branch name> (committed, not pushed — the finalize step opens the PR)
   ### Deviations from the issue
   - <one bullet per deviation, with rationale; "none" if there are no deviations>
   ### Status
   STATUS: <DONE | DONE_WITH_CONCERNS | PARTIAL | BLOCKED | NEEDS_CONTEXT>
   (DONE = EVERY work package implemented and tested on the feature branch, every Acceptance Criterion satisfied — no package left partial or not started; DONE_WITH_CONCERNS = every package likewise complete, but with reservations a reviewer should see; PARTIAL = a reviewable subset is committed but at least one work package is unfinished — used ONLY when you genuinely could not finish them all in this session, never as a shortcut; BLOCKED = could not implement, nothing shippable; NEEDS_CONTEXT = the issue is underspecified and a human must clarify.)
   Do NOT report DONE or DONE_WITH_CONCERNS when any work package is partial or not started — that is exactly the false "this closes the issue" signal this report exists to prevent. A complete subset of a multi-package issue is PARTIAL, not DONE. On PARTIAL the orchestrator opens the pull request with a non-closing "Refs #` + fmt.Sprintf("%d", ctx.IssueNumber) + `" link so the issue stays open for the remaining packages; on DONE / DONE_WITH_CONCERNS it links "Closes #` + fmt.Sprintf("%d", ctx.IssueNumber) + `".

## Circuit breakers — stop instead of thrashing

You run fully autonomously, with no human in the loop and a bounded budget, so a thrash loop burns the whole budget before anyone notices. STOP and output the report the moment you detect any of these conditions — do not push through them:
- Fighting the test suite: the same test (or set of tests) keeps failing across repeated, distinct fix attempts and you are not converging. NEVER weaken, skip, or delete the test to go green — that masks the defect instead of fixing it; stop instead.
- Ballooning scope: the change set is growing past the plan and the issue's implied blast radius — new top-level packages or files the issue never asked for — to force something to work. Implementing the work packages the issue EXPLICITLY lists (including a new package or files it names) is required scope, NOT ballooning; this breaker is only for scope the issue never asked for.
- Reverting in circles: you have reverted and rewritten the same code more than once without converging on a working change.

When you hit a circuit breaker, halt immediately and emit STATUS: PARTIAL when a partial but reviewable change already exists — at least one work package is done and committed but others remain (commit what you have on the branch so the issue stays open and a follow-up run can finish the rest), STATUS: DONE_WITH_CONCERNS when every work package is in fact complete but you have reservations a reviewer should see, or STATUS: BLOCKED when nothing shippable was produced. A stopped run that explains why is worth far more than an exhausted budget.

` + commitTrailerBlock() + attributionFooterBlock("Implemented by") + `## Hard rules

` + noSkipHooksLine() + `- NEVER weaken or delete tests to make the suite green; fix the root cause.
- NEVER widen types to Any/interface{}/unknown to silence the type-checker.
- NEVER suppress lint findings with // nolint, # noqa, # type: ignore, @ts-ignore, etc. unless that suppression is already idiomatic in the same file.
- NEVER add scope the issue did not ask for. Refactors, renames, dependency bumps, formatter sweeps — out of scope unless explicitly listed in Affected Areas.
- NEVER do anything the issue's Non-Goals list excludes.
- NEVER fabricate file paths, symbol names, or migration numbers — open the file before claiming.
- NEVER stop after a subset of the issue's work packages and report DONE. Implement every package the issue lists; if you genuinely cannot finish them all, report PARTIAL (not DONE / DONE_WITH_CONCERNS) so the issue stays open for the rest.
- NEVER push or force-push, and do NOT open a pull request — the finalize step does that after the simplify and review passes. Your job ends at committing on the branch.
- NEVER background a command and stop to wait for its result, and NEVER defer work to "after" something finishes — this one-shot session has no later turn. Run tests and builds in the foreground to completion, commit, then output the report, all within this single response.
- If the issue is wrong (a cited file does not exist; an Acceptance Criterion is unreachable; the Non-Goals contradict the Description), STOP and post a clarifying comment on the issue instead of inventing scope. Output the report explaining what you did NOT do and why.
- If there is nothing to commit (the issue turns out to already be implemented), do NOT create an empty commit; output the report explaining what you found.
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
// access to planwerk-agent or its pattern dirs.
func BuildBareImplementPrompt(ctx implement.BareContext) string {
	repoFullName := ctx.RepoFullName
	issueNumber := ctx.IssueNumber
	var sb strings.Builder

	sb.WriteString(`You are a Staff Engineer implementing an elaborated GitHub issue end-to-end inside a checkout of the target repository. The issue is the definition of done — treat its Acceptance Criteria as a contract, and treat the WHOLE issue as one unit of work: when the issue breaks the work into several work packages, implementing it means implementing EVERY package, not the first one and a stop.

`)
	sb.WriteString(baselineBehavioralPrinciples)
	sb.WriteString(outputLanguageBlock())
	sb.WriteString(`Apply these task-specific thinking patterns on top of the baseline above:
- "Read the issue first, in full." — Acceptance Criteria, Non-Goals, Affected Areas, References. Do NOT start editing before you have read every section.
- "Verify the ground truth." — For every file, symbol, package, or migration the issue cites, open the file and confirm it exists and matches the description. If it does not, STOP and report — do not invent code on top of a stale spec.
- "Implement EVERY work package — the whole issue is the contract." — When the issue decomposes the work into multiple parts — ` + workBreakdownDefinition() + ` — you must implement ALL of them in this session, each with its own deliverables (the unit / integration / e2e tests and docs the package calls for). Implementing only the first package or two and stopping is an INCOMPLETE implementation, not a "smaller" one.
- "Smallest change that satisfies every Acceptance Criterion." — "Smallest" governs HOW each part is built — no speculative scope, no drive-by refactors, no renames the issue did not ask for — NOT HOW MANY of the issue's listed parts you implement. Skipping a listed work package is not "smaller"; it is unfinished.
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

1. READ the issue body in full (from the gh fetch above). Extract Acceptance Criteria, Non-Goals, Affected Areas, References, and — when present — the Work breakdown (every work package / work item / numbered or lettered part, with each part's own deliverables) into your working notes. The set of work packages is your checklist: none is done until all are done.
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
   - Run every command in the FOREGROUND and wait for it to finish before the next step — never background a test or build run and move on. You need its real exit status in hand to commit and to fill in the report; a backgrounded run's result never reaches this one-shot session.
   - Run the project's test suite (or the targeted subset that covers the new code).
   - Run lint / vet / formatter / type-checker as the project configures them.
   - Capture the exact commands you ran and their pass/fail status for the report below.
7. SELF-REVIEW the diff against the issue's Acceptance Criteria. Remove anything that is not strictly required. Stop if you have drifted into a Non-Goal.
8. PUSH the branch and OPEN A DRAFT PULL REQUEST linked to issue #` + fmt.Sprintf("%d", issueNumber) + `. How you link the issue depends on whether you implemented the WHOLE issue:
   - If you implemented EVERY work package and satisfied every Acceptance Criterion (a DONE / DONE_WITH_CONCERNS implementation): link with the GitHub closing keyword "Closes #` + fmt.Sprintf("%d", issueNumber) + `" on its own line, so GitHub auto-links the PR and closes the issue on merge. Do NOT use a bare "Implements #` + fmt.Sprintf("%d", issueNumber) + `" mention — GitHub only recognizes the closing keywords (close/closes/closed, fix/fixes/fixed, resolve/resolves/resolved), so a plain reference does NOT create the linkage GitHub displays.
   - If you left ANY work package unfinished (a PARTIAL implementation): link with a NON-closing reference "Refs #` + fmt.Sprintf("%d", issueNumber) + `" instead — NEVER a closing keyword — so the issue stays OPEN for the remaining packages and merging this PR does not falsely close it. Add a "Work packages" section to the PR body listing which packages this branch delivers and which remain, and state plainly that the issue stays open for the rest.
   - Walk the reviewer through the change set in commit order.
   - Call out anything that diverged from the issue (and why).
9. OUTPUT the structured implementation report below.

## Implementation Report (final output)

After pushing the branch and opening the draft PR, output a report in this exact shape:

   ## Implementation Report (issue #` + fmt.Sprintf("%d", issueNumber) + `)

   ### Work Breakdown Coverage
   - <work package / work item, verbatim title or number> — <done | partial | not started> — <evidence: the commits, files, and tests that deliver it, including its own tests and docs>
   - (List EVERY work package the issue breaks the work into. Write "None — the issue is a single undivided change" when the issue has no multi-part breakdown. STATUS: DONE is only legitimate when every package here is "done".)
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
   STATUS: <DONE | DONE_WITH_CONCERNS | PARTIAL | BLOCKED | NEEDS_CONTEXT>
   (DONE = EVERY work package implemented and tested, every Acceptance Criterion satisfied, and the PR opened with a "Closes #` + fmt.Sprintf("%d", issueNumber) + `" link; DONE_WITH_CONCERNS = every package likewise complete and the closing PR opened, but with reservations a reviewer should see; PARTIAL = a reviewable subset is committed and the PR opened with a non-closing "Refs #` + fmt.Sprintf("%d", issueNumber) + `" link because at least one work package is unfinished — the issue stays open; BLOCKED = could not implement, nothing shippable; NEEDS_CONTEXT = the issue is underspecified and a human must clarify.)
   Do NOT report DONE or DONE_WITH_CONCERNS when any work package is partial or not started — a complete subset of a multi-package issue is PARTIAL, and its PR must link with "Refs #` + fmt.Sprintf("%d", issueNumber) + `", never a closing keyword.

` + commitTrailerBlock() + attributionFooterBlock("Implemented by") + `## Hard rules

` + noSkipHooksLine() + `- NEVER weaken or delete tests to make the suite green; fix the root cause.
- NEVER widen types to Any/interface{}/unknown to silence the type-checker.
- NEVER suppress lint findings with // nolint, # noqa, # type: ignore, @ts-ignore, etc. unless that suppression is already idiomatic in the same file.
- NEVER add scope the issue did not ask for. Refactors, renames, dependency bumps, formatter sweeps — out of scope unless explicitly listed in Affected Areas.
- NEVER do anything the issue's Non-Goals list excludes.
- NEVER fabricate file paths, symbol names, or migration numbers — open the file before claiming.
- NEVER stop after a subset of the issue's work packages and report DONE, and NEVER open a "Closes #` + fmt.Sprintf("%d", issueNumber) + `" PR for partial work. Implement every package the issue lists; if you genuinely cannot finish them all, report PARTIAL and link the PR with "Refs #` + fmt.Sprintf("%d", issueNumber) + `" so the issue stays open for the rest.
- NEVER force-push.
- NEVER background a command and stop to wait for its result, and NEVER defer work to "after" something finishes — this one-shot session has no later turn. Run tests and builds in the foreground to completion, commit, push, open the PR, then output the report, all within this single response.
- If the issue is wrong (a cited file does not exist; an Acceptance Criterion is unreachable; the Non-Goals contradict the Description), STOP and post a clarifying comment on the issue instead of inventing scope. Output the report explaining what you did NOT do and why.
- If there is nothing to commit (the issue turns out to already be implemented), do NOT open an empty PR; output the report explaining what you found.
- It is OK to stop and report BLOCKED or NEEDS_CONTEXT. Bad work is worse than no work; escalating is not penalized. Emit the matching STATUS instead of inventing scope or shipping a half-built change.
`)

	return sb.String()
}
