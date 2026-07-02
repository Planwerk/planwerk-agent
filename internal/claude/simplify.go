package claude

import (
	"fmt"
	"strings"

	"github.com/planwerk/planwerk-agent/internal/implement"
	"github.com/planwerk/planwerk-agent/internal/patterns"
	"github.com/planwerk/planwerk-agent/internal/report"
)

// SimplifyFindings runs the read-only ponytail-style simplify pass using a fresh
// Claude context. It reviews the produced diff through a minimalist
// decision-ladder lens (YAGNI -> stdlib -> platform/framework native -> an
// already-present dependency -> a one-liner -> minimum new code) and returns a
// delete/collapse list of over-engineering as structured findings. baseBranch
// scopes the pass to changes relative to that branch. The finder never flags
// validation, error handling, security, accessibility, tests, or assertions —
// that hard guardrail is asserted in the prompt.
func (c *Client) SimplifyFindings(dir, baseBranch string) (*report.ReviewResult, error) {
	raw, model, err := c.runClaude(dir, buildSimplifyFindPrompt(baseBranch), "simplify")
	if err != nil {
		return nil, fmt.Errorf("running simplify pass: %w", err)
	}

	result, err := c.structureReview(raw)
	if err != nil {
		return nil, fmt.Errorf("structuring simplify pass: %w", err)
	}

	// Tag all findings as from the simplify pass.
	for i := range result.Findings {
		if result.Findings[i].Pattern == "" {
			result.Findings[i].Pattern = "simplify"
		}
	}

	assignIDs(result)
	result.Model = model
	return result, nil
}

func buildSimplifyFindPrompt(baseBranch string) string {
	if baseBranch == "" {
		baseBranch = DefaultBaseBranch
	}
	return `You are a Staff Engineer performing a ponytail-style simplify review.
Your job is to find over-engineering and unnecessary complexity an unattended
implementation session introduced — and produce a delete/collapse list, not a redesign.

` + diffScopeLines(baseBranch) + `Then focus your analysis ONLY on those files.

## The decision ladder
For every piece of complexity, ask whether a simpler rung of this ladder would do
the same job, and prefer it (top is simplest):
1. Do not build it at all (YAGNI) — the requirement does not actually need it.
2. The standard library.
3. A platform- or framework-native feature.
4. A dependency already present in the project.
5. A one-liner.
6. Only then: the minimum new code.

Apply the engineering principles the project already enforces: KISS, YAGNI,
MINIMAL, NO_OVERENGINEERING, NO_NIH. A finding is a concrete piece of complexity
the implementation does not need — name the rung of the ladder that replaces it.

Flag ONLY:
1. Speculative abstractions — interfaces with a single implementation, factories where a constructor suffices, generics or config knobs with one concrete use.
2. Reinvented wheels — hand-rolled code that the stdlib, the framework, or an already-present dependency already provides.
3. Dead or unreachable complexity — unused parameters kept "for the future", branches that cannot be taken, commented-out code.
4. Over-built control flow — layers of indirection, wrappers that only forward, defensive handling for impossible states.
5. Code that is simply longer than it needs to be — a 200-line solution that a senior engineer would write in 50.

` + simplifyFindGuardrailBlock() + `
For every finding you report:
- Quote the exact lines of over-engineered code from the diff.
- Name the decision-ladder rung that replaces it and describe the smaller code it collapses to.
- Use severity WARNING for clear over-engineering, INFO for smaller cleanups. Do not use BLOCKING or CRITICAL — nothing here is a bug.

DO NOT comment on:
- Bugs, security holes, or failure modes — that is the review pass's job, not this one.
- Code style, naming, or formatting.
- Anything whose removal would change observable behavior.

A clean diff is a valid outcome: if you find no over-engineering, return an empty findings array — do NOT manufacture a finding.

` + planwerkIgnoreLine() + communicationStyleBlock() + outputLanguageBlock() + findingLabelsBlock() + "/review"
}

// simplifyReportHeading is the heading every simplification report opens with.
// sanitizeSimplifyReport anchors on this prefix to drop any conversational
// preamble the model emits before the report.
const simplifyReportHeading = "## Simplification Report"

// ApplySimplifications runs a fresh Claude Code session inside the given checkout
// to apply the simplify pass's findings and fold each into the commit it belongs
// to (git commit --fixup + git rebase --autosquash) on the local feature branch.
// It does NOT push: the pass runs before any pull request exists, and the
// finalize step opens the PR afterwards. It is the findings-driven analog of Fix:
// it runs in auto mode so the session can edit files, run tests, and commit
// without an interactive confirmation, while the auto-mode classifier still vets
// each action.
func (c *Client) ApplySimplifications(dir string, ctx implement.SimplifyApplyContext) (string, string, error) {
	out, model, err := c.runClaudeAuto(dir, BuildSimplifyApplyPrompt(ctx), "simplify-apply")
	if err != nil {
		return "", "", fmt.Errorf("running simplify apply: %w", err)
	}
	return sanitizeReport(out, simplifyReportHeading), model, nil
}

// BuildSimplifyApplyPrompt assembles the prompt for the simplify-apply session.
// It renders the findings as a delete-list, embeds the pattern catalog, repeats
// the hard guardrail, and folds each change into the commit it belongs to via
// fixup/autosquash — without pushing, since no pull request exists yet.
// Exported so the simplify path can render the prompt without invoking Claude.
func BuildSimplifyApplyPrompt(ctx implement.SimplifyApplyContext) string {
	var sb strings.Builder

	sb.WriteString(`You are a Staff Engineer simplifying a just-implemented feature branch: removing the over-engineering and unnecessary complexity a prior implementation session introduced, WITHOUT changing behavior. No pull request exists yet — you fold your simplifications into the branch's local commits, and a later finalize step opens the PR once this and the review pass are done.

`)
	sb.WriteString(baselineBehavioralPrinciples)
	sb.WriteString(outputLanguageBlock())
	sb.WriteString(`Apply these task-specific thinking patterns on top of the baseline above:
- "Decision ladder." — For every piece of complexity, prefer the simplest rung that still does the job: not building it (YAGNI) -> the standard library -> a platform/framework-native feature -> a dependency already present -> a one-liner -> only then the minimum new code. Collapse toward the top.
- "Delete, do not redesign." — Simplification removes accidental complexity. It is NOT a refactor, an API redesign, or a behavior change. If a change alters observable behavior, it is out of scope — leave it.
- "Each removal folds into the commit that introduced it." — A simplification to code an earlier commit added belongs IN that commit, not in a new commit stacked on top.
` + selfReviewPatternLine() + `
`)

	fmt.Fprintf(&sb, "## Branch\n\n- Repository: %s\n- Base branch: %s — fold simplifications into this branch's own commits, the range origin/%[2]s..HEAD\n- You are on the feature branch the implement session committed. No PR exists yet; do NOT push or open one.\n\n",
		ctx.RepoFullName, ctx.BaseBranch)

	sb.WriteString("## Simplifications to apply\n\n")
	sb.WriteString("These are the over-engineering findings from the read-only simplify pass. Apply each one — remove or collapse the complexity to the simpler form named — unless doing so would change behavior or touch the guardrail areas below, in which case skip it and say so in the report.\n\n")
	sb.WriteString(renderSimplifyFindings(ctx.Findings))
	sb.WriteString("\n")

	if len(ctx.Patterns) > 0 {
		sb.WriteString("## Project Review Patterns to Honor\n\n")
		sb.WriteString("These patterns are the catalog the project's review/audit/elaborate tools share — including any project-specific patterns shipped under `.planwerk/review_patterns/` in this repository. The simplified result you push MUST stay consistent with them. When a simplification touches an area covered by a pattern, prefer the resolution the pattern endorses.\n\n")
		sb.WriteString("<review-patterns>\n")
		sb.WriteString(patterns.FormatGroupedForPrompt(ctx.Patterns, ctx.MaxPatterns))
		sb.WriteString("</review-patterns>\n\n")
	}

	sb.WriteString(simplifyApplyGuardrailBlock() + `
## What to do

1. For each simplification above, confirm it removes accidental complexity only and changes no observable behavior. Skip any that would touch the guardrail areas.
2. Apply the change — delete or collapse the code to the simpler form.
3. Verify locally: build the project and run the tests (or the targeted subset covering the touched code). Capture the exact commands and pass/fail in the report. If a command cannot run in this environment, say so explicitly.
`)

	sb.WriteString(foldSteps(ctx.BaseBranch, 4))

	sb.WriteString(`5. After folding, output a structured simplification report in this exact shape:

   ## Simplification Report

   ### Applied
   - <finding title> — <what was removed/collapsed and to what> (folded into <sha> <subject>)
   ### Skipped
   - <finding title> — <why: would change behavior, touches a guardrail area, or no longer present>
   ### Diff summary
   - Files: <comma-separated list>
   - Approx lines added/removed: <+N/-M>
   ### Status
   STATUS: <DONE | DONE_WITH_CONCERNS | BLOCKED | NEEDS_CONTEXT>
   (DONE = simplifications applied and verified; DONE_WITH_CONCERNS = folded but with reservations a human should see; BLOCKED = could not make progress; NEEDS_CONTEXT = missing information only a human can supply. The orchestrator reads this line and stops the pass on BLOCKED or NEEDS_CONTEXT.)

` + commitTrailerBlock() + `## Hard rules

`)
	sb.WriteString(foldDisciplineRule(ctx.BaseBranch))
	sb.WriteString(`- NEVER change observable behavior. This pass removes complexity; it is not a refactor or a redesign.
`)
	sb.WriteString(noSkipHooksLine())
	sb.WriteString(`- NEVER fabricate file paths, line numbers, or symbols — open the file before claiming.
- If a finding no longer applies, would change behavior, or touches a guardrail area, SKIP it and record why — do not force it.
- If there is nothing to simplify after review, do NOT create an empty commit; output the report with an empty Applied list and stop.
- It is OK to stop and report BLOCKED or NEEDS_CONTEXT. Bad work is worse than no work; escalating is not penalized.
`)

	return sb.String()
}

// renderSimplifyFindings renders the simplify findings as a numbered delete-list
// for the apply prompt: title, file, the complexity to remove, the simpler form
// to collapse to, and the quoted code.
func renderSimplifyFindings(findings []report.Finding) string {
	if len(findings) == 0 {
		return "(No findings.)\n"
	}
	var sb strings.Builder
	for i, f := range findings {
		fmt.Fprintf(&sb, "%d. %s", i+1, f.Title)
		if f.File != "" {
			fmt.Fprintf(&sb, " (%s)", f.File)
		}
		sb.WriteString("\n")
		if f.Problem != "" {
			fmt.Fprintf(&sb, "   - Complexity: %s\n", f.Problem)
		}
		if f.Action != "" {
			fmt.Fprintf(&sb, "   - Collapse to: %s\n", f.Action)
		}
		if f.CodeSnippet != "" {
			fmt.Fprintf(&sb, "   - Code:\n\n```\n%s\n```\n", strings.TrimRight(f.CodeSnippet, "\n"))
		}
	}
	return sb.String()
}

// foldSteps renders the fold-via-autosquash instructions shared by the
// findings-driven apply prompts: each change is folded into the branch commit it
// belongs to (git commit --fixup + git rebase --autosquash bounded to the
// merge-base). It does NOT push — these passes run on the local feature branch
// before any pull request exists; the finalize step opens the PR afterwards.
// baseBranch bounds the rebase to the branch's own commits; foldStep is the step
// number so the caller can place the block within its own numbered workflow.
func foldSteps(baseBranch string, foldStep int) string {
	return fmt.Sprintf(`%[1]d. Fold each change into the commit it belongs to. This branch may carry more
   than one commit, and a removal of code that an earlier commit introduced
   belongs IN that commit — not in a new commit stacked on top.

   a. List the branch's own commits (oldest first):

      git log --oneline --reverse origin/%[2]s..HEAD

   b. For each distinct change, find the commit that introduced the code you
      are simplifying — use `+"`git blame <file>`, `git log -p -- <file>`, or `git log -S<symbol>`"+`.
   c. Stage ONLY that change and record it as a fixup of its target commit:

      git add -- <files for this change>
      git commit --fixup=<target-sha>

      Repeat (c) for every change that maps to a different commit.
   d. Once every change is recorded as a fixup, fold them in non-interactively
      (no editor opens). Rebase against the merge-base so ONLY this branch's
      own commits are folded and the branch is never silently advanced onto a
      moved base:

      GIT_SEQUENCE_EDITOR=true git rebase -i --autosquash "$(git merge-base origin/%[2]s HEAD)"

   Do NOT push and do NOT open a pull request. Leave the rewritten commits on the
   local branch — the finalize step opens the PR once the simplify and review
   passes are done.

`, foldStep, baseBranch)
}
