package claude

import (
	"encoding/json"
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
// runClaudeImplement already creates a fresh `claude -p` invocation per call,
// so every implement call runs in a brand-new Claude session by construction.
// It runs in auto mode (--permission-mode auto) so the session can edit
// files, run tests, and commit without an interactive confirmation, while the
// auto-mode classifier still vets each action. The session runs on the
// --implement-model override when one is set, and on the shared --claude-model
// otherwise; the surrounding passes always stay on --claude-model.
//
// When ctx.WorkerModel is set the session runs in orchestrator mode: the
// prompt instructs it to delegate every work package to the "implementer"
// subagent defined via the CLI's --agents flag (see implementAgentsJSON),
// which runs on ctx.WorkerModel. Subagents inherit the parent's auto
// permission mode, so the workers can edit, test, and commit under the same
// classifier. The returned model then names the worker model — the model that
// wrote the code — rather than the orchestrating session's model, so the
// report's attribution footer credits the implementing model (the workers'
// own commit trailers already carry their exact model id).
func (c *Client) Implement(dir string, ctx implement.Context) (string, string, error) {
	agentsJSON, err := implementAgentsJSON(ctx)
	if err != nil {
		return "", "", err
	}
	out, model, err := c.runClaudeImplement(dir, BuildImplementPrompt(ctx), "implement", agentsJSON)
	if err != nil {
		return "", "", fmt.Errorf("running implement: %w", err)
	}
	return sanitizeImplementationReport(out), implementAttributionModel(ctx, model), nil
}

// implementAttributionModel resolves which model the implement run's artifacts
// are attributed to (the report comment footer). In orchestrator mode that is
// the worker model — the implementer subagents wrote the code, so the
// attribution names the implementing model, not the orchestrating one that
// only reviewed and verified. In single-session mode it stays the resolved
// session model the envelope reported, exactly as before orchestrator mode
// existed. The worker attribution carries the configured value (an alias like
// "opus", or an exact id when the operator passed one) because the envelope
// only ever resolves the session's own model; the workers' commit trailers
// name their exact model id regardless (see commitTrailerBlock).
func implementAttributionModel(ctx implement.Context, sessionModel string) string {
	if ctx.WorkerModel != "" {
		return ctx.WorkerModel
	}
	return sessionModel
}

// implementerAgentName is the subagent name the orchestrated implement session
// delegates its work packages to. It appears in two coupled places: the
// --agents definition implementAgentsJSON emits, and the orchestration
// instructions BuildImplementPrompt renders — the constant keeps them from
// drifting apart.
const implementerAgentName = "implementer"

// agentDefinition is the JSON shape of one inline subagent definition inside
// the object Claude Code's --agents flag accepts (the same fields as a
// .claude/agents/*.md frontmatter): what the agent is for, its system prompt,
// and the model/effort it runs on. Tools are deliberately not restricted — the
// worker inherits the full toolset (Edit, Write, Bash, …) and the parent
// session's auto permission mode, so the auto classifier keeps vetting its
// actions.
type agentDefinition struct {
	Description string `json:"description"`
	Prompt      string `json:"prompt"`
	Model       string `json:"model,omitempty"`
	Effort      string `json:"effort,omitempty"`
}

// implementAgentsJSON renders the --agents value for an orchestrated implement
// session: a JSON object defining the implementer subagent on ctx.WorkerModel
// at ctx.WorkerEffort (falling back to DefaultImplementWorkerEffort). It
// returns "" when ctx.WorkerModel is empty — the single-session mode, where no
// subagent is defined and the runner omits the --agents flag entirely, keeping
// the historical invocation byte-for-byte unchanged.
func implementAgentsJSON(ctx implement.Context) (string, error) {
	if ctx.WorkerModel == "" {
		return "", nil
	}
	effort := ctx.WorkerEffort
	if effort == "" {
		effort = DefaultImplementWorkerEffort
	}
	raw, err := json.Marshal(map[string]agentDefinition{
		implementerAgentName: {
			Description: "Implements exactly one delegated work package of a GitHub issue end-to-end — code, tests, docs — committed on the current feature branch.",
			Prompt:      buildImplementerAgentPrompt(),
			Model:       ctx.WorkerModel,
			Effort:      effort,
		},
	})
	if err != nil {
		return "", fmt.Errorf("encoding implementer agent definition: %w", err)
	}
	return string(raw), nil
}

// buildImplementerAgentPrompt assembles the system prompt of the implementer
// subagent an orchestrated implement session delegates its work packages to.
// The worker shares the orchestrator's checkout but NOT its context — it sees
// neither the issue nor the plan nor the orchestrator's prompt — so everything
// it must honor unconditionally (the baseline principles, the commit-trailer
// convention, the hard rules) is repeated here, while everything task-specific
// arrives in the per-delegation brief the orchestrator writes. The prompt is
// static by design: a deterministic --agents value keeps the orchestrated
// invocation reproducible and lets a golden test lock it.
func buildImplementerAgentPrompt() string {
	var sb strings.Builder
	sb.WriteString(`You are a Staff Engineer implementing exactly ONE work package of a larger GitHub issue, inside a checkout of the target repository. An orchestrator session delegated this package to you; the task brief you received is your entire contract — you cannot see the orchestrator's context, the issue, or the plan, so treat the brief as authoritative and complete. If the brief is missing something you need, say so in your result instead of guessing.

`)
	sb.WriteString(baselineBehavioralPrinciples)
	sb.WriteString(outputLanguageBlock())
	sb.WriteString(`## Rules for this delegation

- Implement ONLY what the brief asks for: the named work package with its code, tests, and documentation. Anything beyond it — refactors, renames, drive-by fixes — is out of scope; mention it in your result instead of doing it.
- You are ALREADY on the correct feature branch. NEVER create or switch branches, NEVER reset, rebase, revert, or amend existing commits, and NEVER push or open a pull request — the orchestrator owns the branch and the delivery.
- Mirror the repository's existing conventions: layout, naming, error handling, logging, test style.
- Tests are part of the package: add unit tests for new logic, each exercising at least one error or edge path — not the happy path only — and integration/E2E tests when the project runs them for comparable features.
- Documentation is part of the package: update README, CHANGELOG, doc comments, or CLI help for every user-visible change the brief covers.
- Run tests, linters, and builds in the FOREGROUND and wait for them to finish; never background a command and return while it runs — your result must carry the real exit status. If a command outlives the Bash tool's foreground time limit, background it and poll its output until it exits before returning.
- Commit your finished work in small, reviewable commits with clean imperative messages (subject and body wrapped at 72 characters), and leave the working tree CLEAN before you return — an uncommitted change is invisible to the orchestrator and lost work.

`)
	sb.WriteString(commitTrailerBlock())
	sb.WriteString(`## Hard rules

` + noSkipHooksLine() + `- NEVER weaken, skip, or delete tests to go green; fix the root cause.
- NEVER widen types to Any/interface{}/unknown to silence the type-checker.
- NEVER suppress lint findings with // nolint, # noqa, # type: ignore, @ts-ignore, etc. unless that suppression is already idiomatic in the same file.
- NEVER fabricate file paths, symbol names, or migration numbers — open the file before claiming.
- If the brief contradicts the repository (a cited file does not exist, a criterion is unreachable), STOP and report the contradiction in your result instead of inventing code around it.

## Result (what you report back)

End with a summary the orchestrator can verify without trusting you:
- the commits you made (sha7 + subject),
- the files you changed,
- the exact verification commands you ran with their pass/fail status,
- any deviation from the brief, with rationale,
- anything you noticed but deliberately left alone.
`)
	return sb.String()
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
	// orchestrated switches the prompt into orchestrator mode: the session
	// delegates every work package to the implementer subagent (defined on
	// ctx.WorkerModel via --agents, see implementAgentsJSON) instead of writing
	// the code itself. The flag shapes the prompt in three coupled places — the
	// orchestration section, workflow step 5, and one hard rule — all gated on
	// this one condition.
	orchestrated := ctx.WorkerModel != ""

	sb.WriteString(`You are a Staff Engineer implementing an elaborated GitHub issue end-to-end inside a fresh checkout of the target repository. The issue body below is the definition of done — treat its Acceptance Criteria as a contract, and treat the WHOLE issue as one unit of work: when the issue breaks the work into several work packages, implementing it means implementing EVERY package, not the first one and a stop.

`)
	sb.WriteString(baselineBehavioralPrinciples)
	sb.WriteString(outputLanguageBlock())
	sb.WriteString(`You implement and commit the change on a feature branch; you do NOT open a pull request. After you finish, automated simplify and review passes run over your diff, and only then is the pull request opened — so leave the branch committed and report, nothing more.

This is a single, non-interactive, one-shot session: there is NO next turn, no human to hand work back to, and nothing re-invokes you after you stop. Do everything to completion now, within this one response — read the issue, edit, run the tests in the FOREGROUND and wait for them to finish, commit every change, then output the report as the last thing you do. NEVER launch a long-running command (a test run, a build) in the background and then yield to "wait" for it or to be "notified" when it finishes: when this session ends the backgrounded job is killed, its result never arrives, and the work it gated — the next commit, the fix it would have informed — never happens. When a single command genuinely outlives the Bash tool's foreground time limit, run it in the background and POLL its output from within this same turn — check it repeatedly until the command exits — instead of ending the turn: polling is how you wait; yielding is how the result is lost. NEVER defer a step to later ("I'll commit once the tests pass", "waiting for the run to complete before committing"): anything left unfinished when you stop is finished never.

`)
	if orchestrated {
		sb.WriteString(orchestrationBlock())
	}
	sb.WriteString(`Apply these task-specific thinking patterns on top of the baseline above:
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
- "Note it, don't fix it." — When you notice something worth improving that the issue did not ask for, write it down for the report's "Noticed but not touching" section and leave the code alone.

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

	sb.WriteString(projectSkillsBlock(ctx.Skills))
	sb.WriteString(styleGuideBlock(ctx.StyleGuidePath))

	hasPlan := strings.TrimSpace(ctx.Plan) != ""
	if hasPlan {
		sb.WriteString("## Implementation Plan (from the planning session)\n\n")
		sb.WriteString("A dedicated read-only planning session already grounded this issue in the repository and produced the plan below. Treat it as the default route: adopt its change set, commit sequence, test plan, and documentation plan. Re-verify its Ground-Truth Notes as you work — when the repository contradicts the plan, deviate as narrowly as possible and record the deviation (with rationale) under \"Deviations from the issue\" in your report.\n\n")
		sb.WriteString("<implementation-plan>\n")
		sb.WriteString(strings.TrimSpace(ctx.Plan))
		sb.WriteString("\n</implementation-plan>\n\n")
	}

	hasResume := ctx.Resume != nil && len(ctx.Resume.Commits) > 0
	if hasResume {
		sb.WriteString(renderResumeSection(ctx.Resume))
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
	if hasResume {
		fmt.Fprintf(&sb, "4. STAY on the existing feature branch `%s` you are already checked out on — an earlier run created it (see \"Resuming a partial implementation\" above). Do NOT create a new branch and do NOT reset, rebase, or amend it.\n", ctx.Resume.Branch)
	} else {
		fmt.Fprintf(&sb, "4. CREATE a fresh feature branch off the current default branch. Use a short, descriptive branch name that MUST begin with \"implement/issue-%d-\" (e.g. \"implement/issue-%d-<slug>\") — the orchestrator keys on this prefix to find and resume the branch if this session is interrupted before it finishes.\n", ctx.IssueNumber, ctx.IssueNumber)
	}
	if orchestrated {
		sb.WriteString(`5. IMPLEMENT the change set package by package, through the ` + "`" + implementerAgentName + "`" + ` agent (see "Orchestrated implementation" above):
   - Delegate one work package per task with a self-contained brief; the worker matches existing conventions, adds the package's tests and docs, and commits in small, reviewable steps.
   - After each worker returns, verify its commits and run the tests in the foreground yourself; dispatch follow-up tasks for every gap, and only then delegate the next package.
`)
	} else {
		sb.WriteString(`5. IMPLEMENT the change set:
   - Match existing layout, naming, error handling, and logging conventions.
   - Add unit tests for new logic, and make every new test exercise at least one error or edge path — not the happy path only. Add integration / E2E tests when the project has them for comparable features.
   - Add or update documentation (README, CHANGELOG, doc comments, CLI help, generated API references) for every user-visible change.
   - Commit in small, reviewable steps with descriptive messages.
`)
	}
	sb.WriteString(`6. VERIFY LOCALLY before you hand off:
   - Run every command in the FOREGROUND and wait for it to finish before the next step — never background a test or build run and move on. You need its real exit status in hand to commit and to fill in the report; a backgrounded run's result never reaches this one-shot session. If a command outlives the Bash tool's foreground time limit, background it and poll its output within this same turn until it exits.
   - Run the project's test suite (or the targeted subset that covers the new code).
   - Run lint / vet / formatter / type-checker as the project configures them.
   - Capture the exact commands you ran and their pass/fail status for the report below.
7. SELF-REVIEW the diff against the issue's Acceptance Criteria. Remove anything that is not strictly required. Stop if you have drifted into a Non-Goal.
8. STOP after committing on the feature branch. Do NOT push and do NOT open a pull request — automated simplify and review passes run over your diff next, and a dedicated finalize step opens the draft PR (linking the issue with "Closes #` + fmt.Sprintf("%d", ctx.IssueNumber) + `") once they are done. Leave the branch checked out with your commits on it.
9. OUTPUT the structured implementation report below.

## Implementation Report (final output)

ALWAYS end the session with this report — it is mandatory and is the last thing you output, even if you stopped early or hit a circuit breaker below. A session that ends without it (a bare summary, a "waiting for the tests to finish" note, anything missing the heading and a terminal STATUS line) is treated by the orchestrator as a failed, unfinished implementation: the run is aborted and no pull request is opened. After committing on the feature branch, output a report in this exact shape:

   ## Implementation Report (issue #` + fmt.Sprintf("%d", ctx.IssueNumber) + `)

   <verdict word, no "STATUS:" prefix> — <one sentence: the concrete outcome, per the Report Shape rules below>

   ### Work Breakdown Coverage
   - <work package / work item, verbatim title or number> — <done | partial | not started> — <evidence: the commits, files, and tests that deliver it, including its own tests and docs>
   - (List EVERY work package the issue breaks the work into. Write "None — the issue is a single undivided change" when the issue has no multi-part breakdown. STATUS: DONE is only legitimate when every package here is "done".)
   ### Acceptance Criteria
   - <criterion verbatim>
     - Status: <satisfied | partial>
     - Evidence: <file:lines that satisfy it, or the test that exercises it — cite the edge or error test, not a happy-path one, when a new test covers the criterion>
   ### Commits
   - <sha7> <subject>
   ### Local verification
   - <exact command> — <pass | fail | skipped: reason>
   ### Branch
   - <branch name> (committed, not pushed — the finalize step opens the PR)
   ### Deviations from the issue
   - <one bullet per deviation, with rationale; "none" if there are no deviations>
   ### Noticed but not touching
   - <the out-of-scope thing you saw> — <where: file:line> — <why it is out of scope: not in Affected Areas, excluded by a Non-Goal>
   - (Write "none" when you saw nothing outside the issue's scope. This section is for observations you deliberately left alone — a bug next door, a stale doc, a refactor the issue never asked for — so a reviewer can tell a disciplined omission from an oversight. NEVER park a work package or an Acceptance Criterion here: anything the issue asks for is in scope and is implemented, not noted.)
   ### Status
   STATUS: <DONE | DONE_WITH_CONCERNS | PARTIAL | BLOCKED | NEEDS_CONTEXT>
   (DONE = EVERY work package implemented and tested on the feature branch, every Acceptance Criterion satisfied — no package left partial or not started; DONE_WITH_CONCERNS = every package likewise complete, but with reservations a reviewer should see; PARTIAL = at least one work package is unfinished because a circuit breaker below genuinely interrupted the work — NEVER a scoping choice; BLOCKED = could not implement, nothing shippable; NEEDS_CONTEXT = the issue is underspecified and a human must clarify.)
   Next: <on any verdict but DONE only: the single action a human takes next — e.g. on PARTIAL "rerun implement on branch <branch>"; omit this line on DONE>
   Do NOT report DONE or DONE_WITH_CONCERNS when any work package is partial or not started — that is exactly the false "this closes the issue" signal this report exists to prevent. A complete subset of a multi-package issue is PARTIAL, not DONE. On PARTIAL the orchestrator opens NO pull request: it keeps the branch so a follow-up run resumes it and finishes the remaining packages — the single pull request (linking "Closes #` + fmt.Sprintf("%d", ctx.IssueNumber) + `") opens only once every package is done.

## Circuit breakers — stop instead of thrashing

You run fully autonomously, with no human in the loop and a bounded budget, so a thrash loop burns the whole budget before anyone notices. STOP and output the report the moment you detect any of these conditions — do not push through them:
- Fighting the test suite: the same test (or set of tests) keeps failing across repeated, distinct fix attempts and you are not converging. NEVER weaken, skip, or delete the test to go green — that masks the defect instead of fixing it; stop instead.
- Ballooning scope: the change set is growing past the plan and the issue's implied blast radius — new top-level packages or files the issue never asked for — to force something to work. Implementing the work packages the issue EXPLICITLY lists (including a new package or files it names) is required scope, NOT ballooning; this breaker is only for scope the issue never asked for.
- Reverting in circles: you have reverted and rewritten the same code more than once without converging on a working change.

When you hit a circuit breaker, halt immediately and emit STATUS: PARTIAL when a partial but reviewable change already exists — at least one work package is done and committed but others remain (commit what you have on the branch; no pull request is opened for it, and a follow-up run resumes the branch to finish the rest), STATUS: DONE_WITH_CONCERNS when every work package is in fact complete but you have reservations a reviewer should see, or STATUS: BLOCKED when nothing shippable was produced. A stopped run that explains why is worth far more than an exhausted budget. The circuit breakers are the ONLY legitimate route to PARTIAL.

` + reportShapeBlock("DONE") + commitTrailerBlock() + attributionFooterBlock("Implemented by") + implementRationalizationsBlock() + `## Hard rules

` + noSkipHooksLine() + `- NEVER weaken or delete tests to make the suite green; fix the root cause.
- NEVER widen types to Any/interface{}/unknown to silence the type-checker.
- NEVER suppress lint findings with // nolint, # noqa, # type: ignore, @ts-ignore, etc. unless that suppression is already idiomatic in the same file.
- NEVER add scope the issue did not ask for. Refactors, renames, dependency bumps, formatter sweeps — out of scope unless explicitly listed in Affected Areas.
- NEVER do anything the issue's Non-Goals list excludes.
- NEVER fabricate file paths, symbol names, or migration numbers — open the file before claiming.
- NEVER stop after a subset of the issue's work packages and report DONE. Implement every package the issue lists; only when a circuit breaker genuinely interrupts you, report PARTIAL (not DONE / DONE_WITH_CONCERNS) so a follow-up run resumes the branch and finishes the rest.
- NEVER split the issue's delivery or pre-emptively descope it. The whole issue lands as exactly ONE pull request. Never propose follow-up issues or PRs as a substitute for implementing listed scope.
- NEVER push or force-push, and do NOT open a pull request — the finalize step does that after the simplify and review passes. Your job ends at committing on the branch.
- NEVER background a command and stop to wait for its result, and NEVER defer work to "after" something finishes — this one-shot session has no later turn. Run tests and builds in the foreground to completion (polling a backgrounded run within the turn only when it outlives the foreground time limit), commit, then output the report, all within this single response.
- If the issue is wrong (a cited file does not exist; an Acceptance Criterion is unreachable; the Non-Goals contradict the Description), STOP and post a clarifying comment on the issue instead of inventing scope. Output the report explaining what you did NOT do and why.
- If there is nothing to commit (the issue turns out to already be implemented), do NOT create an empty commit; output the report explaining what you found.
- It is OK to stop and report BLOCKED or NEEDS_CONTEXT. Bad work is worse than no work; escalating is not penalized. Emit the matching STATUS instead of inventing scope or shipping a half-built change.
`)
	if orchestrated {
		sb.WriteString("- NEVER create or edit a file yourself in this orchestrated session — every code change is delivered by an `" + implementerAgentName + "` delegation, and every gap your verification finds goes back to one as a follow-up task.\n")
	}

	return sb.String()
}

// orchestrationBlock returns the "## Orchestrated implementation" section
// BuildImplementPrompt renders when ctx.WorkerModel is set. It flips the
// session's role from implementer to orchestrator: the session keeps the whole
// issue in view, delegates every work package to the implementer subagent
// (defined via --agents on the worker model), and verifies each delivered
// package against the actual diff before moving on — the delegate → verify →
// follow-up loop that keeps the strongest model on oversight while the worker
// model writes the code. Sequential delegation is mandated because the workers
// commit on one shared feature branch; parallel workers would race each other
// on the git index.
func orchestrationBlock() string {
	name := "`" + implementerAgentName + "`"
	return `## Orchestrated implementation — delegate the code to ` + name + ` subagents

This session runs in ORCHESTRATOR mode: your job is to keep the WHOLE issue in view, delegate the code work, and verify every delivered piece — not to write the code yourself. A dedicated ` + name + ` agent is defined for this session, running on a model dedicated to implementation work; every code change goes through it. The one-shot rules above still bind you — everything must finish within this session — but the code-writing happens through delegations, not your own edits.

- NEVER create or edit files yourself. Delegate every work package to the ` + name + ` agent via the Task tool. You may — and must — run the read-only commands (git log, git diff, reading files) and the verification commands (tests, linters, builds) yourself.
- Delegate ONE work package at a time, in the plan's commit-sequence order. The workers commit on the shared feature branch, so parallel workers would race each other on the git index; sequential delegation also lets each package build on the one before it.
- Write each delegation brief SELF-CONTAINED: the worker shares your checkout but NOT your context — it sees neither the issue, nor the plan, nor this prompt. Include the work package's description verbatim, its Acceptance Criteria, the files the plan names for it, the test and documentation expectations, and every review pattern or project skill that constrains it. A brief that says "see the issue" delivers nothing.
- Instruct every worker to commit its finished work on the current branch and to leave the working tree clean before returning.
- VERIFY every worker's result yourself before moving on: read the commits it made (git log, git diff), run the tests in the FOREGROUND, and check the package's Acceptance Criteria against the actual diff. Do NOT take the worker's summary at its word.
- When your verification finds gaps, dispatch a follow-up ` + name + ` task carrying your concrete findings — the file:line, the failing command's output, the unmet criterion — instead of fixing the code yourself. Repeat until the package genuinely passes, then move to the next one.
- If a worker returns with uncommitted changes or a broken intermediate state, dispatch a follow-up task to finish and commit — never patch the tree yourself.
- The Implementation Report below stays YOURS: fill it in from your own verification — the commits and test runs you checked — not from the workers' summaries.

`
}

// renderResumeSection builds the prompt block for a resuming implement session:
// it tells the session it is already on the feature branch an earlier aborted run
// left behind, lists the commits already on it, and instructs it to build on that
// work — reconciling it against the plan and finishing only what remains — rather
// than recreating the branch and redoing committed work. When the orchestrator
// recovered the stopped session's last account (a progress note or partial
// report from the issue), it is embedded too, so the resuming session starts
// from what was already implemented and verified instead of re-deriving it.
// Called only when ctx.Resume carries commits.
func renderResumeSection(rc *implement.ResumeContext) string {
	var sb strings.Builder
	sb.WriteString("## Resuming a partial implementation\n\n")
	fmt.Fprintf(&sb, "An earlier run for this issue stopped before finishing (most likely it hit its session/usage limit). You are ALREADY checked out on the feature branch it left behind — `%s` — with the commits it already made on it:\n\n", rc.Branch)
	for _, c := range rc.Commits {
		sha := c.SHA
		if len(sha) > 7 {
			sha = sha[:7]
		}
		fmt.Fprintf(&sb, "- %s %s\n", sha, c.Subject)
	}
	if prior := strings.TrimSpace(rc.PriorReport); prior != "" {
		sb.WriteString(`
The stopped session's final account is preserved below. Treat it as your map, not as truth: it records what that session had already implemented and verified — commits made, verification commands that already passed, and what was still outstanding when it stopped. Focus your work on the outstanding part, re-verify the account's claims cheaply (git log, re-run a check only where doubt exists) instead of redoing every verification from scratch, and never contradict the actual repository state in its favor.

<previous-session-account>
` + prior + `
</previous-session-account>
`)
	}
	sb.WriteString(`
Continue that work — do NOT start over:
- Do NOT create a new branch, and do NOT reset, rebase, revert, or amend the commits already on this branch. They are completed work; build on top of them.
- Reconcile the commits above against the plan's Commit Sequence and Work Breakdown: work out which commits and work packages are already delivered and which remain. Open the files an existing commit changed to confirm it does what its subject claims; only if one is clearly broken or incomplete should you correct it in a NEW follow-up commit — never by rewriting history.
- Then implement ONLY the remaining commits and work packages, on this same branch, committing as you go.
- In the Work Breakdown Coverage and Commits sections of your report, include the already-present commits and mark the packages they deliver "done", with those commits as evidence — exactly as if you had made them this session.

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
- "Tests are part of the change." — Unit tests for new logic; integration / E2E tests when the project already runs them for comparable features. Every new test must exercise at least one error or edge path (empty/zero-length, nil/absent, an upstream error), not the happy path only. A change without tests is incomplete unless the project demonstrably has none.
- "Documentation is part of the change." — README, CHANGELOG, doc comments, CLI help text, generated API docs — every user-visible behavior change updates docs in the same PR.
- "Commits tell the story." — Stage the work as a sequence of small, reviewable commits; do not produce a single monolithic diff. Write each commit message cleanly: a concise, imperative subject line and, when the change needs it, a body that explains the why. Wrap EVERY line — subject and body alike — at 72 characters or fewer.
- "Self-review before opening the PR." — Walk the diff once more as a reviewer. Reject anything you would push back on.
- "Stay inside the agreed scope." — If the issue's Non-Goals exclude something, do NOT do it.
- "Note it, don't fix it." — When you notice something worth improving that the issue did not ask for, write it down for the report's "Noticed but not touching" section and leave the code alone.

`)

	fmt.Fprintf(&sb, "## Source Issue\n\n- Repository: %s\n- Issue #%d\n\n", repoFullName, issueNumber)

	if len(ctx.TechTags) > 0 {
		fmt.Fprintf(&sb, "Detected technologies in the target repo (used to filter the pattern catalog below): %s\n\n",
			strings.Join(ctx.TechTags, ", "))
	}

	sb.WriteString("You are already running inside a checkout of this repository's default branch. Do NOT re-clone. Operate on the working tree you have. You run as a one-shot session: fetch the issue yourself, implement it, push a fresh feature branch, open a draft PR, and report.\n\n")

	sb.WriteString(renderBareCatalog(ctx.PatternCatalog, ctx.HasRepoLocalRefs))

	sb.WriteString(projectSkillsBlock(ctx.Skills))
	sb.WriteString(styleGuideBlock(ctx.StyleGuidePath))

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
4. CREATE a fresh feature branch off the current default branch. Use a short, descriptive branch name that MUST begin with "implement/issue-` + fmt.Sprintf("%d", issueNumber) + `-" (e.g. "implement/issue-` + fmt.Sprintf("%d", issueNumber) + `-<slug>") so the branch is unambiguously identifiable as this issue's implementation.
5. IMPLEMENT the change set:
   - Match existing layout, naming, error handling, and logging conventions.
   - Add unit tests for new logic, and make every new test exercise at least one error or edge path — not the happy path only. Add integration / E2E tests when the project has them for comparable features.
   - Add or update documentation (README, CHANGELOG, doc comments, CLI help, generated API references) for every user-visible change.
   - Commit in small, reviewable steps with descriptive messages.
6. VERIFY LOCALLY before opening the PR:
   - Run every command in the FOREGROUND and wait for it to finish before the next step — never background a test or build run and move on. You need its real exit status in hand to commit and to fill in the report; a backgrounded run's result never reaches this one-shot session. If a command outlives the Bash tool's foreground time limit, background it and poll its output within this same turn until it exits.
   - Run the project's test suite (or the targeted subset that covers the new code).
   - Run lint / vet / formatter / type-checker as the project configures them.
   - Capture the exact commands you ran and their pass/fail status for the report below.
7. SELF-REVIEW the diff against the issue's Acceptance Criteria. Remove anything that is not strictly required. Stop if you have drifted into a Non-Goal.
8. PUSH the branch and — ONLY when you implemented the WHOLE issue — OPEN A DRAFT PULL REQUEST linked to issue #` + fmt.Sprintf("%d", issueNumber) + `:
   - If you implemented EVERY work package and satisfied every Acceptance Criterion (a DONE / DONE_WITH_CONCERNS implementation): link with the GitHub closing keyword "Closes #` + fmt.Sprintf("%d", issueNumber) + `" on its own line, so GitHub auto-links the PR and closes the issue on merge. Do NOT use a bare "Implements #` + fmt.Sprintf("%d", issueNumber) + `" mention — GitHub only recognizes the closing keywords (close/closes/closed, fix/fixes/fixed, resolve/resolves/resolved), so a plain reference does NOT create the linkage GitHub displays.
   - If ANY work package is unfinished (a PARTIAL implementation): push the branch so the committed work persists, but do NOT open a pull request — a partial PR splits the issue's delivery, and the issue lands as exactly ONE complete PR. Report PARTIAL below so the operator reruns the implementation on this same branch to finish the remaining packages.
   - Walk the reviewer through the change set in commit order.
   - Call out anything that diverged from the issue (and why).
9. OUTPUT the structured implementation report below.

## Implementation Report (final output)

ALWAYS end the session with this report — even if you stopped early or hit a circuit breaker below. After pushing the branch and opening the draft PR, output a report in this exact shape:

   ## Implementation Report (issue #` + fmt.Sprintf("%d", issueNumber) + `)

   <verdict word, no "STATUS:" prefix> — <one sentence: the concrete outcome, per the Report Shape rules below>

   ### Work Breakdown Coverage
   - <work package / work item, verbatim title or number> — <done | partial | not started> — <evidence: the commits, files, and tests that deliver it, including its own tests and docs>
   - (List EVERY work package the issue breaks the work into. Write "None — the issue is a single undivided change" when the issue has no multi-part breakdown. STATUS: DONE is only legitimate when every package here is "done".)
   ### Acceptance Criteria
   - <criterion verbatim>
     - Status: <satisfied | partial>
     - Evidence: <file:lines that satisfy it, or the test that exercises it — cite the edge or error test, not a happy-path one, when a new test covers the criterion — or "see PR description">
   ### Commits
   - <sha7> <subject>
   ### Local verification
   - <exact command> — <pass | fail | skipped: reason>
   ### Pull Request
   - URL: <draft PR URL, or "none — PARTIAL, branch pushed for a follow-up session">
   - Branch: <branch name>
   ### Deviations from the issue
   - <one bullet per deviation, with rationale; "none" if there are no deviations>
   ### Noticed but not touching
   - <the out-of-scope thing you saw> — <where: file:line> — <why it is out of scope: not in Affected Areas, excluded by a Non-Goal>
   - (Write "none" when you saw nothing outside the issue's scope. This section is for observations you deliberately left alone — a bug next door, a stale doc, a refactor the issue never asked for — so a reviewer can tell a disciplined omission from an oversight. NEVER park a work package or an Acceptance Criterion here: anything the issue asks for is in scope and is implemented, not noted.)
   ### Status
   STATUS: <DONE | DONE_WITH_CONCERNS | PARTIAL | BLOCKED | NEEDS_CONTEXT>
   (DONE = EVERY work package implemented and tested, every Acceptance Criterion satisfied, and the PR opened with a "Closes #` + fmt.Sprintf("%d", issueNumber) + `" link; DONE_WITH_CONCERNS = every package likewise complete and the closing PR opened, but with reservations a reviewer should see; PARTIAL = at least one work package is unfinished because a circuit breaker below genuinely interrupted the work — NEVER a scoping choice; the branch is pushed but NO pull request is opened, and a follow-up session on this branch finishes the rest; BLOCKED = could not implement, nothing shippable; NEEDS_CONTEXT = the issue is underspecified and a human must clarify.)
   Next: <on any verdict but DONE only: the single action a human takes next — e.g. on PARTIAL "rerun implement on branch <branch>"; omit this line on DONE>
   Do NOT report DONE or DONE_WITH_CONCERNS when any work package is partial or not started — a complete subset of a multi-package issue is PARTIAL, and PARTIAL opens no pull request.

## Circuit breakers — stop instead of thrashing

You run fully autonomously, with no human in the loop and a bounded budget, so a thrash loop burns the whole budget before anyone notices. STOP and output the report the moment you detect any of these conditions — do not push through them:
- Fighting the test suite: the same test (or set of tests) keeps failing across repeated, distinct fix attempts and you are not converging. NEVER weaken, skip, or delete the test to go green — that masks the defect instead of fixing it; stop instead.
- Ballooning scope: the change set is growing past the plan and the issue's implied blast radius — new top-level packages or files the issue never asked for — to force something to work. Implementing the work packages the issue EXPLICITLY lists (including a new package or files it names) is required scope, NOT ballooning; this breaker is only for scope the issue never asked for.
- Reverting in circles: you have reverted and rewritten the same code more than once without converging on a working change.

When you hit a circuit breaker, halt immediately and emit STATUS: PARTIAL when a partial but reviewable change already exists — at least one work package is done and committed but others remain (push what you have but open NO pull request; a follow-up session on the same branch finishes the rest), STATUS: DONE_WITH_CONCERNS when every work package is in fact complete but you have reservations a reviewer should see, or STATUS: BLOCKED when nothing shippable was produced. A stopped run that explains why is worth far more than an exhausted budget. The circuit breakers are the ONLY legitimate route to PARTIAL.

` + reportShapeBlock("DONE") + commitTrailerBlock() + attributionFooterBlock("Implemented by") + implementRationalizationsBlock() + `## Hard rules

` + noSkipHooksLine() + `- NEVER weaken or delete tests to make the suite green; fix the root cause.
- NEVER widen types to Any/interface{}/unknown to silence the type-checker.
- NEVER suppress lint findings with // nolint, # noqa, # type: ignore, @ts-ignore, etc. unless that suppression is already idiomatic in the same file.
- NEVER add scope the issue did not ask for. Refactors, renames, dependency bumps, formatter sweeps — out of scope unless explicitly listed in Affected Areas.
- NEVER do anything the issue's Non-Goals list excludes.
- NEVER fabricate file paths, symbol names, or migration numbers — open the file before claiming.
- NEVER stop after a subset of the issue's work packages and report DONE, and NEVER open a pull request for partial work — the issue lands as exactly ONE complete PR. Implement every package the issue lists; only when a circuit breaker genuinely interrupts you, push the branch, open no PR, and report PARTIAL so a follow-up session finishes the rest.
- NEVER split the issue's delivery or pre-emptively descope it. Never propose follow-up issues or PRs as a substitute for implementing listed scope.
- NEVER force-push.
- NEVER background a command and stop to wait for its result, and NEVER defer work to "after" something finishes — this one-shot session has no later turn. Run tests and builds in the foreground to completion (polling a backgrounded run within the turn only when it outlives the foreground time limit), commit, push, open the PR, then output the report, all within this single response.
- If the issue is wrong (a cited file does not exist; an Acceptance Criterion is unreachable; the Non-Goals contradict the Description), STOP and post a clarifying comment on the issue instead of inventing scope. Output the report explaining what you did NOT do and why.
- If there is nothing to commit (the issue turns out to already be implemented), do NOT open an empty PR; output the report explaining what you found.
- It is OK to stop and report BLOCKED or NEEDS_CONTEXT. Bad work is worse than no work; escalating is not penalized. Emit the matching STATUS instead of inventing scope or shipping a half-built change.
`)

	return sb.String()
}
