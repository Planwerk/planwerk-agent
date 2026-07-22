package claude

import (
	"fmt"
	"strings"

	"github.com/planwerk/planwerk-agent/internal/implement"
)

// finalizeReportHeading is the heading every finalize report opens with.
// sanitizeReport anchors on this prefix to drop any conversational preamble the
// model emits before the report ("The branch is pushed. Final report:").
const finalizeReportHeading = "## Pull Request"

// FinalizePR runs a fresh Claude Code session inside the given checkout to open
// the draft pull request for the implemented + simplified + reviewed feature
// branch: it resolves the base branch and the change set from git, pushes the
// branch, and opens a draft PR whose description walks the reviewer through the
// commits and links the issue with the closing "Closes #N" keyword — finalize
// only ever runs for a complete implementation, since a PARTIAL run persists
// its branch and aborts before this step. It is the final step of an implement
// run — the implement, simplify, and review sessions deliberately do NOT push
// or open a PR, so the PR lands already simplified and self-reviewed.
//
// It runs in auto mode (--permission-mode auto) so the session can push the
// branch and run `gh pr create` without an interactive confirmation, while the
// auto-mode classifier still vets each action. When there is nothing to ship (no
// commits on the branch), the session opens no PR and says so in the report,
// returning no error — the same shape the implement session uses for an issue
// that turns out to be already implemented.
func (c *Client) FinalizePR(dir string, ctx implement.FinalizeContext) (string, string, error) {
	out, model, err := c.runClaudeAuto(dir, BuildFinalizePrompt(ctx), "finalize")
	if err != nil {
		return "", "", fmt.Errorf("running finalize: %w", err)
	}
	return sanitizeReport(out, finalizeReportHeading), model, nil
}

// BuildFinalizePrompt assembles the prompt for the finalize session that opens
// the draft pull request. It instructs the session to resolve the base branch
// and change set itself (so a non-"main" default branch and an empty change set
// are handled in the session), push the branch, and open the draft PR linking
// the issue with the closing "Closes #N" keyword — finalize only runs for a
// complete implementation, so the link always closes the issue on merge.
// Exported so the implement path can render the prompt without invoking Claude.
func BuildFinalizePrompt(ctx implement.FinalizeContext) string {
	issueNumber := ctx.IssueNumber
	var sb strings.Builder

	sb.WriteString(`You are a Staff Engineer opening the draft pull request for a feature branch that has already been implemented, simplified, and self-reviewed by earlier automated sessions. Your only job is to publish the branch and open the PR — do NOT edit code, fix findings, or change the commits.

`)
	sb.WriteString(baselineBehavioralPrinciples)
	sb.WriteString(outputLanguageBlock())

	fmt.Fprintf(&sb, "## Source Issue\n\n- Repository: %s\n- Issue #%d: %s\n\n", ctx.RepoFullName, issueNumber, ctx.IssueTitle)

	// Finalize only runs for a complete implementation (a PARTIAL run persists
	// its branch and aborts before this step), so the PR always links the issue
	// with the closing keyword.
	linkInstruction := `   - Link the issue with the GitHub closing keyword "Closes #` + fmt.Sprintf("%d", issueNumber) + `" on its own line, so GitHub auto-links the PR to the issue and closes it on merge. This is mandatory. Do NOT use a bare "Implements #` + fmt.Sprintf("%d", issueNumber) + `" mention — GitHub only recognizes the closing keywords (close/closes/closed, fix/fixes/fixed, resolve/resolves/resolved), so a plain reference does NOT create the linkage GitHub displays.`

	sb.WriteString(`## Workflow

Run these steps in order. Do not skip ahead.

1. DETERMINE the change set:
   - Resolve the base branch: run ` + "`git symbolic-ref --short refs/remotes/origin/HEAD`" + ` (it returns e.g. "origin/main"; the base is the part after "origin/"). Fall back to origin/main, then origin/master, if it is unset.
   - You are on the feature branch the implement session committed. Run ` + "`git log <base>..HEAD --oneline`" + ` and ` + "`git diff <base>...HEAD --stat`" + ` to see the commits and files that make up this change.
   - If there are NO commits between the base branch and HEAD, there is nothing to ship: do NOT push and do NOT open a pull request. Output the report below with an empty Pull Request section and STATUS: DONE, explaining that the branch carries no commits.
2. PUSH the branch to origin:

       git push -u origin HEAD

   Use a plain push — NEVER force-push. The branch is usually new on the remote; if an earlier interrupted run already pushed partial progress to it, your local commits are ahead of it and this is a fast-forward.
3. OPEN OR UPDATE THE DRAFT PULL REQUEST against the base branch. First check whether one already exists for this branch: run ` + "`gh pr list --head <branch> --json number,url,state`" + ` — an earlier interrupted run may already have opened one. If NONE exists, open a draft PR with ` + "`gh pr create --draft`" + `. If one ALREADY exists, do NOT open a second (the push above already updated its commits) — update its description with ` + "`gh pr edit`" + ` if it needs it, and report that PR's URL. The PR description must:
` + linkInstruction + `
   - Walk the reviewer through the change set in commit order, reading the actual commits and diff — not guessing from the issue.
   - Call out anything in the diff that diverged from the issue (and why), if you can tell from the commits.
4. OUTPUT the structured report below.

## Report (final output)

After opening the draft PR, output a report in this exact shape:

   ## Pull Request

   <verdict word, no "STATUS:" prefix> — <one sentence: the concrete outcome, per the Report Shape rules below>

   - URL: <draft PR URL, or "none — branch carries no commits">
   - Branch: <branch name>
   - Base: <base branch>
   ### Commits
   - <sha7> <subject>
   ### Status
   STATUS: <DONE | DONE_WITH_CONCERNS | BLOCKED | NEEDS_CONTEXT>
   (DONE = PR opened, or nothing to ship; DONE_WITH_CONCERNS = opened but with reservations a reviewer should see; BLOCKED = could not push or open the PR; NEEDS_CONTEXT = missing information only a human can supply.)
   Next: <on any verdict but DONE only: the single action a human takes next; omit this line on DONE>

` + reportShapeBlock("DONE") + attributionFooterBlock("Implemented by") + `## Hard rules

- NEVER edit code, amend commits, rebase, or change the branch's history — the diff is final. You only push and open the PR.
- NEVER force-push. A plain push is correct — a fast-forward when an earlier interrupted run already pushed this branch.
` + noSkipHooksLine() + `- NEVER open an empty pull request. If the branch carries no commits over the base, open nothing and report it.
- If pushing or opening the PR fails, STOP and report BLOCKED with the exact error — do not retry blindly or invent a workaround.
`)

	return sb.String()
}
