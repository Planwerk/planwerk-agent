package claude

import (
	"fmt"
	"strings"

	"github.com/planwerk/planwerk-agent/internal/implement"
	"github.com/planwerk/planwerk-agent/internal/patterns"
	"github.com/planwerk/planwerk-agent/internal/report"
)

// reviewReportHeading is the heading every review-apply report opens with.
// sanitizeReport anchors on this prefix to drop any conversational preamble the
// model emits before the report.
const reviewReportHeading = "## Review Report"

// ApplyReview runs a fresh Claude Code session inside the given checkout to
// resolve the review pass's findings and fold each fix into the commit it
// belongs to (git commit --fixup + git rebase --autosquash) on the local feature
// branch. It does NOT push: the pass runs before any pull request exists, and the
// finalize step opens the PR afterwards. It is the findings-driven analog of Fix:
// it runs in auto mode so the session can edit files, run tests, and commit
// without an interactive confirmation, while the auto-mode classifier still vets
// each action. The session runs under the completion nudge (runClaudeAutoReport):
// when it ends without its report, the same session is resumed to finish and
// report instead of the pass losing its work.
func (c *Client) ApplyReview(dir string, ctx implement.ReviewApplyContext) (string, string, error) {
	out, model, err := c.runClaudeAutoReport(dir, BuildReviewApplyPrompt(ctx), "review-apply", reviewReportHeading, reportStatusChoices)
	if err != nil {
		return "", "", fmt.Errorf("running review apply: %w", err)
	}
	return sanitizeReport(out, reviewReportHeading), model, nil
}

// BuildReviewApplyPrompt assembles the prompt for the review-apply session. It
// renders the findings as a fix-list, embeds the pattern catalog, and folds each
// fix into the commit it belongs to via fixup/autosquash — without pushing, since
// no pull request exists yet. Unlike the simplify pass, it carries no test-file
// guardrail: review fixes are allowed — and expected — to add regression tests.
// Exported so the review path can render the prompt without invoking Claude.
func BuildReviewApplyPrompt(ctx implement.ReviewApplyContext) string {
	var sb strings.Builder

	sb.WriteString(`You are a Staff Engineer resolving the review findings on a just-implemented feature branch: applying the fixes a read-only review pass surfaced and folding each one into the commit that introduced the issue. No pull request exists yet — you fold your fixes into the branch's local commits, and a later finalize step opens the PR once this pass is done.

`)
	sb.WriteString(baselineBehavioralPrinciples)
	sb.WriteString(outputLanguageBlock())
	sb.WriteString(`Apply these task-specific thinking patterns on top of the baseline above:
- "Find the root cause." — Each finding names a symptom; fix the broken invariant in the code under it, not just the surface. Open the cited file before editing — never patch from the finding text alone.
- "Minimal-invasive change." — Touch the smallest surface area that resolves each finding. No drive-by refactors, no reformatting unrelated code, no dependency bumps the finding does not implicate.
- "Regression guard." — When the fix is in production code and the existing tests did not catch the issue, add or extend a test that fails before your fix and passes after. This pass IS allowed to add tests.
` + selfReviewPatternLine() + `
`)

	fmt.Fprintf(&sb, "## Branch\n\n- Repository: %s\n- Base branch: %s — fold fixes into this branch's own commits, the range origin/%[2]s..HEAD\n- You are on the feature branch the implement session committed. No PR exists yet; do NOT push or open one.\n\n",
		ctx.RepoFullName, ctx.BaseBranch)

	sb.WriteString("## Review findings to resolve\n\n")
	sb.WriteString("These are the findings from the read-only review pass over the produced diff. Resolve each one — fix the root cause — unless it is a false positive or no longer applies to the current diff, in which case skip it and say so in the report.\n\n")
	sb.WriteString(renderReviewFindings(ctx.Findings))
	sb.WriteString("\n")

	if len(ctx.Patterns) > 0 {
		sb.WriteString("## Project Review Patterns to Honor\n\n")
		sb.WriteString("These patterns are the catalog the project's review/audit/elaborate tools share — including any project-specific patterns shipped under `.planwerk/review_patterns/` in this repository. The fixed result you push MUST stay consistent with them: do not introduce code or test changes that would themselves be flagged by a pattern below. When a fix touches an area covered by a pattern, prefer the resolution the pattern endorses.\n\n")
		sb.WriteString("<review-patterns>\n")
		sb.WriteString(patterns.FormatGroupedForPrompt(ctx.Patterns, ctx.MaxPatterns))
		sb.WriteString("</review-patterns>\n\n")
	}

	sb.WriteString(`## What to do

1. For each finding above, confirm it is a real issue worth fixing. If a finding is a false positive or no longer applies to the current diff, skip it and record why in the report.
2. Fix the root cause with the minimal change that resolves the finding. Open the cited file before editing; do not patch the symptom or reach into unrelated cleanups.
3. Add a regression test when the fix is in production code and the existing suite did not catch the issue — a test that fails before your fix and passes after. Skip this only for fixes inside test code itself or fixes no unit/integration test could plausibly catch.
4. Verify locally: build the project and run the tests (or the targeted subset covering the touched code). Capture the exact commands and pass/fail in the report. If a command cannot run in this environment, say so explicitly.
`)

	sb.WriteString(foldSteps(ctx.BaseBranch, 5))

	sb.WriteString(`6. After folding, output a structured review report in this exact shape:

   ## Review Report

   ### Resolved
   - <finding title> — <root cause and the fix> (folded into <sha> <subject>)
   ### Skipped
   - <finding title> — <why: false positive, no longer present, or out of scope for this pass>
   ### Diff summary
   - Files: <comma-separated list>
   - Approx lines added/removed: <+N/-M>
   ### Status
   STATUS: <DONE | DONE_WITH_CONCERNS | BLOCKED | NEEDS_CONTEXT>
   (DONE = findings resolved and verified; DONE_WITH_CONCERNS = folded but with reservations a human should see; BLOCKED = could not make progress; NEEDS_CONTEXT = missing information only a human can supply. The orchestrator reads this line and stops the pass on BLOCKED or NEEDS_CONTEXT.)

` + commitTrailerBlock() + `## Hard rules

`)
	sb.WriteString(foldDisciplineRule(ctx.BaseBranch))
	sb.WriteString(`- NEVER silence a finding instead of fixing it: no deleting or weakening tests, assertions, or required checks; no suppression comments that were not already idiomatic in the file; no widening types to Any/interface{}/unknown.
`)
	sb.WriteString(noSkipHooksLine())
	sb.WriteString(`- NEVER fabricate file paths, line numbers, or symbols — open the file before claiming.
- PREFER to change only files the branch already touches. Reaching outside it is a last resort; make the smallest out-of-scope change that resolves the finding and call it out in the report.
- If a finding is a false positive or no longer applies, SKIP it and record why — do not invent a change to satisfy it.
- If there is nothing to fix after review, do NOT create an empty commit; output the report with an empty Resolved list and stop.
- It is OK to stop and report BLOCKED or NEEDS_CONTEXT. Bad work is worse than no work; escalating is not penalized.
`)

	return sb.String()
}

// renderReviewFindings renders the review findings as a numbered fix-list for
// the apply prompt: title, severity, file, the problem, the suggested fix (or
// the recommended action), and the quoted code.
func renderReviewFindings(findings []report.Finding) string {
	if len(findings) == 0 {
		return "(No findings.)\n"
	}
	var sb strings.Builder
	for i, f := range findings {
		fmt.Fprintf(&sb, "%d. %s", i+1, f.Title)
		if f.Severity != "" {
			fmt.Fprintf(&sb, " [%s]", f.Severity)
		}
		if f.File != "" {
			fmt.Fprintf(&sb, " (%s)", f.File)
		}
		sb.WriteString("\n")
		if f.Problem != "" {
			fmt.Fprintf(&sb, "   - Problem: %s\n", f.Problem)
		}
		if fix := f.SuggestedFix; fix != "" {
			fmt.Fprintf(&sb, "   - Suggested fix: %s\n", fix)
		} else if f.Action != "" {
			fmt.Fprintf(&sb, "   - Suggested fix: %s\n", f.Action)
		}
		if f.CodeSnippet != "" {
			fmt.Fprintf(&sb, "   - Code:\n\n```\n%s\n```\n", strings.TrimRight(f.CodeSnippet, "\n"))
		}
	}
	return sb.String()
}
