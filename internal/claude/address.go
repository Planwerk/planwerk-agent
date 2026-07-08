package claude

import (
	"fmt"
	"strings"

	"github.com/planwerk/planwerk-agent/internal/address"
	"github.com/planwerk/planwerk-agent/internal/github"
	"github.com/planwerk/planwerk-agent/internal/report"
)

// Address runs a fresh auto-mode Claude Code session inside the checkout to
// incorporate the selected PR review threads as a follow-up commit, then returns
// the structured per-thread result. The session edits, verifies, and commits —
// but does NOT push; the orchestrator owns the push, mirroring how the rebase
// apply session leaves publishing to the orchestrator. It runs in auto mode so
// the session can edit files, run tests, and commit without an interactive
// confirmation. The decode shares decodeJSONWithRepair so a one-character JSON
// glitch does not fail the run.
func (c *Client) Address(dir string, ctx address.Context) (*report.AddressResult, error) {
	out, model, err := c.runClaudeAuto(dir, BuildAddressPrompt(ctx), "address")
	if err != nil {
		return nil, fmt.Errorf("running address: %w", err)
	}
	var result report.AddressResult
	if err := c.decodeJSONWithRepair(out, "structured address-result", &result); err != nil {
		return nil, err
	}
	result.Model = model
	return &result, nil
}

// BuildAddressPrompt assembles the prompt for one address session: the PR
// metadata, the selected review threads (each with its file:line, author, full
// comment chain, and the diff hunk it is anchored to), the pattern catalog, the
// commit instructions, and the structured JSON output shape. Exported so the
// address subcommand can render it without invoking Claude (--print-prompt).
func BuildAddressPrompt(ctx address.Context) string {
	var sb strings.Builder

	sb.WriteString(`You are a Staff Engineer addressing human review comments on a GitHub pull request.

`)
	sb.WriteString(baselineBehavioralPrinciples)
	sb.WriteString(outputLanguageBlock())
	sb.WriteString(`Apply these task-specific thinking patterns on top of the baseline above:
- "Address the reviewer's actual ask." — Read the whole comment chain, not just the first line. Do exactly what the reviewer requested; do not reinterpret it into a larger change.
- "Minimal-invasive change." — Make the smallest change that resolves the comment. No drive-by refactors, no reformatting unrelated code, no dependency bumps the comment does not call for.
- "Open the file at the anchored hunk; do not guess." — The diff hunk shows where the comment was left. Open the actual source at that path and line before editing. Never invent code shapes or line numbers.
- "Verify before committing." — Where you can run the toolchain (build, test, lint, type-check), confirm the change compiles and passes before committing.
- "If you cannot address it, say so." — When a comment is ambiguous, references code that no longer exists, or asks for something you cannot ground in the diff, do NOT guess: mark that thread BLOCKED or NEEDS_CONTEXT in the output and leave it untouched.
- "A reviewer can be wrong." — When you have concrete evidence the requested change would be incorrect (it would break behavior, contradict the issue, or reintroduce a bug), do NOT make the change and do NOT file the thread as BLOCKED: set its status to NEEDS_CONTEXT and state the disagreement with the file:line evidence in the summary, so a human decides.

`)

	fmt.Fprintf(&sb, "## Pull Request\n\n- Repository: %s\n- PR #%d: %s\n- Head branch: %s (committed to by you; the orchestrator pushes)\n",
		ctx.RepoFullName, ctx.PRNumber, ctx.PRTitle, ctx.HeadBranch)
	if ctx.BaseBranch != "" {
		fmt.Fprintf(&sb, "- Base branch: %s\n", ctx.BaseBranch)
	}
	sb.WriteString("\n")

	sb.WriteString("## Review threads to address\n\n")
	sb.WriteString(formatAddressThreads(ctx.Threads))
	sb.WriteString("\n")

	writePatternSection(&sb, ctx.Patterns, ctx.MaxPatterns,
		"These patterns are the catalog the project's review/audit tools share. Your change MUST stay consistent with them: do not address a comment in a way that would itself be flagged by a pattern below.")

	sb.WriteString(projectSkillsBlock(ctx.Skills))

	sb.WriteString("## What to do\n\n")
	sb.WriteString(`1. For each thread above, read the full comment chain and open the file at the anchored path and line.
2. Make the minimal change that addresses the reviewer's ask. If two threads share a root cause, fix it once.
3. Verify locally where you can run the toolchain. Re-read your diff and remove anything not required.
`)

	if ctx.OneCommitPerThread {
		fmt.Fprintf(&sb, `4. Stage your change and create ONE focused follow-up commit for this
   thread:

      git add -- <files for this thread>
      git commit -s \
        -m "<concise summary of the change>" \
        -m "Assisted-by: Claude"

   Wrap every commit-message line at 72 characters or fewer.
5. Do NOT push. The orchestrator pushes the follow-up commit to %s after
   this session, then replies to and (optionally) resolves the thread.
`, ctx.HeadBranch)
	} else {
		fmt.Fprintf(&sb, `4. Stage every change and create ONE aggregate follow-up commit covering
   all the threads above:

      git add -A
      git commit -s \
        -m "Address review comments" \
        -m "Threads: <comma-separated thread ids>" \
        -m "Assisted-by: Claude"

   Wrap every commit-message line at 72 characters or fewer.
5. Do NOT push. The orchestrator pushes the follow-up commit to %s after
   this session, then replies to and (optionally) resolves each thread.
`, ctx.HeadBranch)
	}

	sb.WriteString(`
6. Output ONLY valid JSON matching this exact schema as your final message (no markdown fences, no prose before or after):

{
  "threads": [
    {
      "thread_id": "the GraphQL thread id from the thread header above",
      "status": "DONE|DONE_WITH_CONCERNS|BLOCKED|NEEDS_CONTEXT",
      "summary": "one sentence on what you changed for this thread (or why you could not)",
      "files": ["path/to/file.go"]
    }
  ],
  "summary": "Overall: how the selected threads were addressed (2-4 sentences)",
  "status": "DONE|DONE_WITH_CONCERNS|BLOCKED|NEEDS_CONTEXT"
}

Field rules:
- Include one "threads" entry for EVERY thread above, in order, even when you could not address it.
- "status" per thread: DONE when addressed and verified; DONE_WITH_CONCERNS when changed but with a reservation; BLOCKED when you could not make progress; NEEDS_CONTEXT when only a human can supply what is missing — including a reasoned disagreement: leave the code untouched and put the disagreement and its evidence in the summary.
- "files" lists the repo-relative paths you touched for that thread; omit it when you changed nothing.
- The top-level "status" is the run's overall terminal status. The orchestrator stops and escalates on BLOCKED or NEEDS_CONTEXT.

` + commitTrailerBlock() + `## Hard rules

- Address ONLY the threads listed above. Do NOT touch unrelated code.
- Do NOT push. Do NOT force-push. The orchestrator publishes the branch separately.
` + noSkipHooksLine() + `- NEVER fabricate file paths or line numbers — open the file before claiming.
- If there is nothing to commit (every thread was BLOCKED/NEEDS_CONTEXT), do NOT create an empty commit; emit the JSON and stop.
`)

	return sb.String()
}

// BuildBareAddressPrompt assembles a portable, self-contained address prompt
// that is copy-pasted into a manual Claude session already running inside a
// checkout of the PR head. The session fetches the unresolved review threads
// itself, addresses them as follow-up commits, pushes, and optionally replies
// to and resolves each thread. The pattern catalog is inlined so the session
// needs no access to planwerk-agent.
func BuildBareAddressPrompt(ctx address.BareContext) string {
	var sb strings.Builder

	sb.WriteString(`You are a Staff Engineer addressing human review comments on a GitHub pull request.

`)
	sb.WriteString(baselineBehavioralPrinciples)
	sb.WriteString(outputLanguageBlock())
	sb.WriteString(`Apply these task-specific thinking patterns on top of the baseline above:
- "Address the reviewer's actual ask." — Read the whole comment chain, not just the first line. Do exactly what the reviewer requested; do not reinterpret it into a larger change.
- "Minimal-invasive change." — Make the smallest change that resolves each comment. No drive-by refactors, no reformatting unrelated code, no dependency bumps the comment does not call for.
- "Open the file at the anchored hunk; do not guess." — Open the actual source at the path and line the comment is anchored to before editing.
- "Verify before committing." — Where you can run the toolchain, confirm the change compiles and passes before committing.
- "If you cannot address it, say so." — When a comment is ambiguous or references code that no longer exists, do NOT guess: leave it untouched and report it.
- "A reviewer can be wrong." — When you have concrete evidence the requested change would be incorrect (it would break behavior, contradict the issue, or reintroduce a bug), do NOT make the change and do NOT file the thread as BLOCKED: set its status to NEEDS_CONTEXT and state the disagreement with the file:line evidence in the summary, so a human decides.

`)

	fmt.Fprintf(&sb, "## Pull Request\n\n- Repository: %s\n- PR #%d\n\n", ctx.RepoFullName, ctx.PRNumber)

	if len(ctx.TechTags) > 0 {
		fmt.Fprintf(&sb, "Detected technologies in the target repo (used to filter the pattern catalog below): %s\n\n",
			strings.Join(ctx.TechTags, ", "))
	}

	sb.WriteString("You are already running inside a checkout of this PR's head branch. Do NOT re-checkout, do NOT clone. Operate on the working tree you have.\n\n")

	sb.WriteString(renderBareCatalog(ctx.PatternCatalog, ctx.HasRepoLocalRefs))

	fmt.Fprintf(&sb, `## Fetch the review threads

Do NOT guess what the reviewers asked. Use the GitHub CLI to pull the PR's unresolved review threads, each with its file, line, comment chain, and the diff hunk it is anchored to:

`+"```"+`
gh api graphql -F owner=<owner> -F name=<repo> -F number=%d -f query='
query($owner: String!, $name: String!, $number: Int!) {
  repository(owner: $owner, name: $name) {
    pullRequest(number: $number) {
      reviewThreads(first: 100) {
        nodes {
          id isResolved isOutdated
          comments(first: 100) { nodes { author { login } body path line diffHunk } }
        }
      }
    }
  }
}'
`+"```"+`

Skip threads that are already resolved (isResolved: true) and any thread whose first comment is one of planwerk-agent's own inline findings.

## What to do

1. For each unresolved thread, read the full comment chain and open the file at the anchored path and line.
2. Make the minimal change that addresses the reviewer's ask. If two threads share a root cause, fix it once.
3. Verify locally where you can run the toolchain. Re-read your diff and remove anything not required.
4. Commit the change(s) as follow-up commits (one per thread keeps the mapping comment to commit legible). Wrap every commit-message line at 72 characters or fewer:

   git add -- <files for this thread>
   git commit -s -m "<concise summary>" -m "Assisted-by: Claude"

5. Push the follow-up commits to the PR head branch:

   git push origin HEAD

6. For each addressed thread, optionally reply summarizing what changed (addPullRequestReviewThreadReply) and, only if asked, resolve it (resolveReviewThread). Replying and resolving are best-effort — a GitHub failure must not undo the pushed commit.
7. Output an address report in this exact shape:

   ## Address Report

   ### Threads
   - <file:line or thread id> — STATUS: <DONE | DONE_WITH_CONCERNS | BLOCKED | NEEDS_CONTEXT> — <one sentence: what changed, or why not>

   Include one entry for EVERY unresolved thread you fetched, in the order fetched, even when you could not address it. Per-thread STATUS: DONE when addressed and verified; DONE_WITH_CONCERNS when changed but with a reservation; BLOCKED when you could not make progress; NEEDS_CONTEXT when only a human can supply what is missing — including a reasoned disagreement: leave the code untouched and put the disagreement and its evidence in the summary.

   ### Summary

   Overall: how the selected threads were addressed (2-4 sentences).

   STATUS: <DONE | DONE_WITH_CONCERNS | BLOCKED | NEEDS_CONTEXT>

`, ctx.PRNumber)

	sb.WriteString(commitTrailerBlock())
	sb.WriteString(attributionFooterBlock("Addressed by"))

	sb.WriteString(`## Hard rules

- Address ONLY the reviewers' comments. Do NOT touch unrelated code.
- Do NOT force-push. Follow-up commits push cleanly with a plain ` + "`git push origin HEAD`" + `.
`)
	sb.WriteString(noSkipHooksLine())
	sb.WriteString(`- NEVER fabricate file paths or line numbers — open the file before claiming.
- If a comment is ambiguous or references code that no longer exists, leave it untouched and report it — do not guess.
- If there is nothing to commit (every thread was BLOCKED/NEEDS_CONTEXT), do NOT create an empty commit and do NOT push; output the report and stop.
`)

	return sb.String()
}

// formatAddressThreads renders the selected review threads for the prompt: each
// thread's id, anchored file:line, resolved status, the full comment chain, and
// the diff hunk the comment is anchored to.
func formatAddressThreads(threads []github.ReviewThread) string {
	if len(threads) == 0 {
		return "(none)\n"
	}
	var sb strings.Builder
	for _, t := range threads {
		loc := t.Path
		if t.Line > 0 {
			loc = fmt.Sprintf("%s:%d", t.Path, t.Line)
		}
		state := "unresolved"
		if t.IsResolved {
			state = "resolved"
		}
		if t.IsOutdated {
			state += ", outdated"
		}
		fmt.Fprintf(&sb, "### Thread %s — %s (%s)\n\n", t.ID, loc, state)
		sb.WriteString("Comment chain:\n\n")
		for _, c := range t.Comments {
			author := c.Author
			if author == "" {
				author = "(unknown)"
			}
			fmt.Fprintf(&sb, "- **%s**: %s\n", author, strings.TrimSpace(c.Body))
		}
		if t.DiffHunk != "" {
			sb.WriteString("\nDiff hunk the comment is anchored to:\n\n```\n")
			sb.WriteString(strings.TrimRight(t.DiffHunk, "\n"))
			sb.WriteString("\n```\n")
		}
		sb.WriteString("\n")
	}
	return sb.String()
}
