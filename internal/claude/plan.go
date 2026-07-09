package claude

import (
	"fmt"
	"strings"

	"github.com/planwerk/planwerk-agent/internal/implement"
	"github.com/planwerk/planwerk-agent/internal/patterns"
)

// Plan runs a read-only Claude Code session inside the given checkout to
// produce a detailed implementation plan for the elaborated GitHub issue
// described in ctx. The session grounds the issue in the actual code —
// verifying every cited file and symbol — but never edits anything; its
// only artifact is the plan text, which the subsequent implement session
// receives verbatim via Context.Plan.
//
// It runs on the dedicated planning model (PlanModel, default "fable") at the
// dedicated planning effort (PlanEffort, default "max") so the deepest
// reasoning happens where it steers the whole implementation, while the
// implement session stays on the default model and effort. Like every
// runClaude* call it is a fresh `claude -p` invocation, so plan and
// implement are two independent sessions by construction.
func (c *Client) Plan(dir string, ctx implement.Context) (string, string, error) {
	// Returned unwrapped: the sole caller (implement's runPlanning) already
	// prefixes "claude plan", and runClaudePlan names the model and the reason.
	out, model, err := c.runClaudePlan(dir, BuildPlanPrompt(ctx), "plan")
	if err != nil {
		return "", "", err
	}
	return sanitizePlan(out), model, nil
}

// planHeading is the heading the planning prompt mandates as the first line
// of every plan ("## Implementation Plan (issue #N)"). sanitizePlan anchors
// on it to drop any conversational preamble the model emits before the plan.
const planHeading = "## Implementation Plan"

// sanitizePlan normalizes the planning session's raw output into the plan
// artifact that is embedded in the implement prompt and posted onto the source
// issue as a comment, dropping any markdown fence or conversational preamble the
// model emits before the "## Implementation Plan" heading. See sanitizeReport.
func sanitizePlan(out string) string {
	return sanitizeReport(out, planHeading)
}

// BuildPlanPrompt assembles the prompt for the read-only planning session
// that precedes an implement session. The full issue body is embedded
// inline, mirroring BuildImplementPrompt. Exported so the implement
// subcommand can render the prompt without invoking Claude
// (--print-plan-prompt mode).
func BuildPlanPrompt(ctx implement.Context) string {
	var sb strings.Builder

	sb.WriteString(`You are a Staff Engineer producing a detailed implementation plan for an elaborated GitHub issue, inside a fresh checkout of the target repository. A SEPARATE implementation session — with no memory of this one — will receive your plan verbatim and execute it IN FULL, delivering the entire plan as a single pull request. The plan must therefore be self-contained, concrete, and grounded in the actual code: name exact paths and symbols, never "the usual place".

`)
	sb.WriteString(baselineBehavioralPrinciples)
	sb.WriteString(outputLanguageBlock())
	sb.WriteString(codebaseDesignBlock())
	sb.WriteString(`Apply these task-specific thinking patterns on top of the baseline above:
- "Read the issue first, in full." — Acceptance Criteria, Non-Goals, Affected Areas, References, and any Work breakdown. Do NOT start planning before you have read every section.
- "Verify the ground truth." — For every file, symbol, package, or migration the issue cites, open the file and confirm it exists and matches the description. Record what you found; the implementer relies on it.
- "Plan EVERY work package — the whole issue is the contract." — When the issue decomposes the work into multiple parts — ` + workBreakdownDefinition() + ` — the plan must cover ALL of them, and each part's own deliverables (its unit / integration / e2e tests and docs). A plan that addresses only the first part or two is incomplete, not "smaller". The implementer executes one issue to completion; a plan that silently drops later work packages sets it up to do the same.
- "Smallest change set that satisfies every Acceptance Criterion." — "Smallest" governs HOW each part is built — no speculative scope, no drive-by refactors, no renames the issue did not ask for — NOT HOW MANY of the issue's listed parts you plan. Dropping a listed work package is not "smaller"; it is incomplete.
- "Name exact paths and symbols." — The implementer must never have to guess which file or function you mean.
- "Tests and docs are part of the plan." — Plan the unit/integration/E2E tests and the documentation updates alongside the code, matching how thoroughly the project itself tests and documents.
- "Surface risks instead of hiding them." — An honest open question beats a confident guess.

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

	renderIssueRelations(&sb, ctx.MetaIssue, ctx.SiblingIssues, ctx.ChildIssues)

	if len(ctx.Patterns) > 0 {
		sb.WriteString("## Project Review Patterns to Honor\n\n")
		sb.WriteString("These patterns are the catalog the project's review/audit/elaborate tools share — including any project-specific patterns shipped under `.planwerk/review_patterns/` in this repository. Treat them as binding constraints on the planned change set: when a pattern covers an area the plan touches, plan the resolution the pattern endorses.\n\n")
		sb.WriteString("<review-patterns>\n")
		sb.WriteString(patterns.FormatGroupedForPrompt(ctx.Patterns, ctx.MaxPatterns))
		sb.WriteString("</review-patterns>\n\n")
	}

	// Project memory from the repo's GitHub Wiki (no-op when the wiki carries
	// no memory pages)
	sb.WriteString(projectMemoryBlock(ctx.Memory))

	sb.WriteString(`## Planning Workflow

Run these steps in order. Do not skip ahead.

1. READ the issue body in full. Extract Acceptance Criteria, Non-Goals, Affected Areas, References, and — when present — the Work breakdown (every work package / work item / numbered or lettered part, with each part's own deliverables) into your working notes. Extract the user stories from the issue body's **User Stories** section when the elaborated issue supplies them; otherwise derive them yourself, subject to the same proportionality rule — generate exactly as many stories as the issue REQUIRES, emit none for purely mechanical work (refactors, CI fixes, dependency bumps, rebases), and never invent a synthetic persona.
2. WALK the repository to ground the issue in reality:
   - Open the README, top-level layout, and any package the issue mentions.
   - For every file the issue cites, open it and confirm it still exists at (or near) the cited path.
   - Identify the project's test conventions (unit, integration, E2E) and where tests live.
   - Identify the project's documentation conventions (README, docs/, CHANGELOG, generated API docs).
3. DESIGN the smallest change set that satisfies every Acceptance Criterion AND covers every work package the issue lists, file by file. OVER-SCOPE GATE: over-scope is work the issue did NOT ask for — a new top-level package, or files the issue never mentions, that you would add to force something to work. Delivering a new package, files, or layers the issue EXPLICITLY lists (in its Work breakdown, Affected Areas, or Acceptance Criteria) is REQUIRED scope, never over-scope — plan it in full. Only when the smallest correct change set still exceeds the issue's stated blast radius is that a signal the issue is underspecified: record the over-scope under "Risks & Open Questions" and prefer STATUS: NEEDS_CONTEXT over PLAN_READY.
4. SEQUENCE the work into small, reviewable commits. Wrap every planned commit subject at 72 characters or fewer.
5. WRITE the plan in the exact output format below. Output ONLY the plan — no preamble, no commentary around it.

## Implementation Plan (final output)

   ## Implementation Plan (issue #` + fmt.Sprintf("%d", ctx.IssueNumber) + `)

   ### Summary
   - <2-4 sentences: the chosen approach and why it is the smallest one that satisfies every Acceptance Criterion AND covers every work package the issue lists>
   ### Ground-Truth Notes
   - <file or symbol the issue cites> — <confirmed | moved to <path> | missing>, <one line on what you actually found>
   ### Work Breakdown Coverage
   - <work package / work item, verbatim title or number> — covered by <the Change Set, Commit Sequence, Test Plan, and Documentation Plan entries below that deliver it, including its own tests and docs>. List EVERY package the issue breaks the work into; the plan is not PLAN_READY until each one is covered here. Write "None — the issue is a single undivided change" when the issue has no multi-part breakdown.
   ### User Stories
   - As a <role>, I want <want>, so that <so_that> — <extracted from the issue | derived>. Write "None — purely mechanical work" (no synthetic persona) for refactors, CI fixes, or dependency bumps that serve no distinct persona.
   ### Change Set
   - <path> — <create | modify | delete>: <what changes and why, naming the exact symbols involved; reference the user story or work package it serves when present>
   ### Commit Sequence
   1. <imperative subject, 72 chars or fewer> — <files touched; one line on the why for the commit body>
   ### Test Plan
   - <test file / function to add or extend> — <behavior it locks down; reference the user story it verifies when stories are present>
   ### Documentation Plan
   - <doc file / section> — <what to update; "none" only if the change has no user-visible behavior>
   ### Verification Commands
   - <exact commands the implementer must run locally (test suite, lint, vet, …)>
   ### Risks & Open Questions
   - <one bullet per risk or open question; "none" if there are none>
   ### Status
   STATUS: <PLAN_READY | BLOCKED | NEEDS_CONTEXT>
   (PLAN_READY = the plan is executable as written and covers every work package the issue lists; BLOCKED = the issue cannot be implemented as specified — explain why under Risks; NEEDS_CONTEXT = the issue is underspecified and a human must clarify before implementation.)

## Hard rules

- This is a PLANNING session. NEVER edit files, create branches, run formatters or code generators, commit, push, or open PRs. Inspecting the repository with read-only commands is fine; modifying the working tree is not.
- NEVER fabricate file paths, symbol names, or migration numbers — open the file before citing it.
- NEVER plan scope the issue did not ask for. Refactors, renames, dependency bumps, formatter sweeps — out of scope unless explicitly listed in Affected Areas.
- NEVER plan anything the issue's Non-Goals list excludes.
- NEVER write a bare #<number> for an enumeration — acceptance criteria, user stories, steps, or options. GitHub auto-links every #<number> in a posted comment to the issue or PR of that number in the target repo, so "AC #1" silently links to issue 1 and adds a spurious back-reference to its timeline. Write the number without the hash: "AC 1", "Story 2", "Step 3". Reserve #<number> strictly for genuine issue/PR cross-references — the source issue, and the Meta, sibling, child, and linked-PR numbers the Meta / Sub-Issue Context section asks you to reference.
- Plan every work package the issue lists, with its own tests and docs — a plan that covers only the first part or two is incomplete, not "smaller". A new package, files, or layers the issue EXPLICITLY lists are required scope: plan them in full, never defer them as out of scope.
- The plan is executed by ONE implementation session and lands as exactly ONE pull request. NEVER structure it as multiple PRs or phased deliveries, NEVER write delivery notes like "one commit ≈ one PR", and NEVER defer a listed work package to a follow-up issue or PR. When the issue body itself carries such a delivery-splitting note, override it: plan the whole issue as one PR. The Commit Sequence is the order of commits WITHIN that single PR.
- If the smallest correct change set still exceeds the blast radius the issue implies — a new top-level package, or files the issue never asked for — do NOT plan the bigger change as if it were asked for: record the over-scope under Risks & Open Questions and prefer STATUS: NEEDS_CONTEXT so a human supplies the missing context first.
- If the issue is wrong (a cited file does not exist; an Acceptance Criterion is unreachable; the Non-Goals contradict the Description), do NOT invent scope around it: record the contradiction under Risks & Open Questions and set STATUS: BLOCKED or NEEDS_CONTEXT.
- It is OK to stop at BLOCKED or NEEDS_CONTEXT. A wrong plan is worse than no plan; escalating is not penalized.
`)

	return sb.String()
}
