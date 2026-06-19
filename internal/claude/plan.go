package claude

import (
	"fmt"
	"strings"

	"github.com/planwerk/planwerk-review/internal/implement"
	"github.com/planwerk/planwerk-review/internal/patterns"
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
func (c *Client) Plan(dir string, ctx implement.Context) (string, error) {
	out, err := c.runClaudePlan(dir, BuildPlanPrompt(ctx), "plan")
	if err != nil {
		return "", fmt.Errorf("running plan: %w", err)
	}
	return sanitizePlan(out), nil
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

	sb.WriteString(`You are a Staff Engineer producing a detailed implementation plan for an elaborated GitHub issue, inside a fresh checkout of the target repository. A SEPARATE implementation session — with no memory of this one — will receive your plan verbatim and execute it. The plan must therefore be self-contained, concrete, and grounded in the actual code: name exact paths and symbols, never "the usual place".

`)
	sb.WriteString(baselineBehavioralPrinciples)
	sb.WriteString(outputLanguageBlock())
	sb.WriteString(`Apply these task-specific thinking patterns on top of the baseline above:
- "Read the issue first, in full." — Acceptance Criteria, Non-Goals, Affected Areas, References. Do NOT start planning before you have read every section.
- "Verify the ground truth." — For every file, symbol, package, or migration the issue cites, open the file and confirm it exists and matches the description. Record what you found; the implementer relies on it.
- "Smallest change set that satisfies every Acceptance Criterion." — No speculative scope, no drive-by refactors, no renames the issue did not ask for.
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

	sb.WriteString(`## Planning Workflow

Run these steps in order. Do not skip ahead.

1. READ the issue body in full. Extract Acceptance Criteria, Non-Goals, Affected Areas, and References into your working notes.
2. WALK the repository to ground the issue in reality:
   - Open the README, top-level layout, and any package the issue mentions.
   - For every file the issue cites, open it and confirm it still exists at (or near) the cited path.
   - Identify the project's test conventions (unit, integration, E2E) and where tests live.
   - Identify the project's documentation conventions (README, docs/, CHANGELOG, generated API docs).
3. DESIGN the smallest change set that satisfies every Acceptance Criterion, file by file. OVER-SCOPE GATE: when the change set exceeds the blast radius the issue implies — a new top-level package, or files the issue never asked for — that is a signal the issue is underspecified, not a license to plan the bigger change. Record the over-scope under "Risks & Open Questions" and prefer STATUS: NEEDS_CONTEXT (escalate for more context before any code is written) over PLAN_READY.
4. SEQUENCE the work into small, reviewable commits. Wrap every planned commit subject at 72 characters or fewer.
5. WRITE the plan in the exact output format below. Output ONLY the plan — no preamble, no commentary around it.

## Implementation Plan (final output)

   ## Implementation Plan (issue #` + fmt.Sprintf("%d", ctx.IssueNumber) + `)

   ### Summary
   - <2-4 sentences: the chosen approach and why it is the smallest one that satisfies every Acceptance Criterion>
   ### Ground-Truth Notes
   - <file or symbol the issue cites> — <confirmed | moved to <path> | missing>, <one line on what you actually found>
   ### Change Set
   - <path> — <create | modify | delete>: <what changes and why, naming the exact symbols involved>
   ### Commit Sequence
   1. <imperative subject, 72 chars or fewer> — <files touched; one line on the why for the commit body>
   ### Test Plan
   - <test file / function to add or extend> — <behavior it locks down>
   ### Documentation Plan
   - <doc file / section> — <what to update; "none" only if the change has no user-visible behavior>
   ### Verification Commands
   - <exact commands the implementer must run locally (test suite, lint, vet, …)>
   ### Risks & Open Questions
   - <one bullet per risk or open question; "none" if there are none>
   ### Status
   STATUS: <PLAN_READY | BLOCKED | NEEDS_CONTEXT>
   (PLAN_READY = the plan is executable as written; BLOCKED = the issue cannot be implemented as specified — explain why under Risks; NEEDS_CONTEXT = the issue is underspecified and a human must clarify before implementation.)

## Hard rules

- This is a PLANNING session. NEVER edit files, create branches, run formatters or code generators, commit, push, or open PRs. Inspecting the repository with read-only commands is fine; modifying the working tree is not.
- NEVER fabricate file paths, symbol names, or migration numbers — open the file before citing it.
- NEVER plan scope the issue did not ask for. Refactors, renames, dependency bumps, formatter sweeps — out of scope unless explicitly listed in Affected Areas.
- NEVER plan anything the issue's Non-Goals list excludes.
- If the smallest correct change set still exceeds the blast radius the issue implies — a new top-level package, or files the issue never asked for — do NOT plan the bigger change as if it were asked for: record the over-scope under Risks & Open Questions and prefer STATUS: NEEDS_CONTEXT so a human supplies the missing context first.
- If the issue is wrong (a cited file does not exist; an Acceptance Criterion is unreachable; the Non-Goals contradict the Description), do NOT invent scope around it: record the contradiction under Risks & Open Questions and set STATUS: BLOCKED or NEEDS_CONTEXT.
- It is OK to stop at BLOCKED or NEEDS_CONTEXT. A wrong plan is worse than no plan; escalating is not penalized.
`)

	return sb.String()
}
