package claude

import (
	"fmt"
	"strings"

	"github.com/planwerk/planwerk-agent/internal/fix"
)

// Fix runs a fresh Claude Code session inside the given checkout directory
// to repair the failing checks described in ctx. The session is responsible
// for applying minimal-invasive code changes, simplifying and self-reviewing
// the diff, then publishing it to the PR head branch. By default (ctx.Fixup)
// it folds each change into the branch commit it belongs to (git commit
// --fixup + git rebase --autosquash) and force-pushes with --force-with-lease;
// with --no-fixup (ctx.Fixup false) it appends a single on-top follow-up commit
// and pushes without rewriting history. The choice is independent of ctx.Local,
// which only controls whether the session runs in the user's own checkout or a
// throw-away temp-dir clone.
//
// runClaudeAuto already creates a fresh `claude -p` invocation per call, so each
// iteration of the fix loop runs in a brand-new Claude session by construction.
// It runs in auto mode (--permission-mode auto) so the session can edit files,
// run tests, commit, and push the repaired branch without an interactive
// confirmation — the same requirement the implement command has — while the
// auto-mode classifier still vets each action.
func (c *Client) Fix(dir string, ctx fix.Context) (string, string, error) {
	out, model, err := c.runClaudeAuto(dir, BuildFixPrompt(ctx), "fix")
	if err != nil {
		return "", "", fmt.Errorf("running fix: %w", err)
	}
	return sanitizeFixReport(out), model, nil
}

// fixReportHeading is the heading every fix report opens with. Both prompt
// variants mandate it: the orchestrator-driven prompt as "## Fix Report
// (iteration N)" and the bare prompt as "## Fix Report". sanitizeFixReport
// anchors on this prefix to drop any conversational preamble the model emits
// before the report ("The branch is published. Final report:").
const fixReportHeading = "## Fix Report"

// sanitizeFixReport strips a wrapping markdown fence and any preamble the model
// emits before the "## Fix Report" heading, so only the report itself reaches
// stdout and the PR comment. The report's "STATUS: ..." line survives because
// it always follows the heading. See sanitizeReport.
func sanitizeFixReport(out string) string {
	return sanitizeReport(out, fixReportHeading)
}

// BuildFixPrompt assembles the prompt for a single fix iteration. It includes
// the failing check names, summaries, and truncated logs so Claude can
// diagnose and patch the root cause without needing to re-fetch them.
// Exported so the fix subcommand can render the prompt without invoking
// Claude (--print-prompt mode).
func BuildFixPrompt(ctx fix.Context) string {
	var sb strings.Builder

	sb.WriteString(`You are a Staff Engineer fixing failing CI checks on a GitHub pull request.

`)
	sb.WriteString(baselineBehavioralPrinciples)
	sb.WriteString(outputLanguageBlock())
	sb.WriteString(fixThinkingPatterns())

	fmt.Fprintf(&sb, "## Pull Request\n\n- Repository: %s\n- PR #%d: %s\n- Head branch: %s (committed to and pushed by you)\n- Head SHA at start of this iteration: %s\n- Iteration: %d of %d (max)\n",
		ctx.RepoFullName, ctx.PRNumber, ctx.PRTitle, ctx.HeadBranch, ctx.HeadSHA, ctx.Iteration, ctx.MaxIterations)
	if ctx.Fixup && ctx.BaseBranch != "" {
		fmt.Fprintf(&sb, "- Base branch: %s — fold fixes into this branch's own commits, the range origin/%[1]s..HEAD\n", ctx.BaseBranch)
	}
	sb.WriteString("\n")

	writePatternSection(&sb, ctx.Patterns, ctx.MaxPatterns,
		"These patterns are the catalog the project's review/audit/elaborate tools share — including any project-specific patterns shipped under `.planwerk/review_patterns/` in this repository. The fix you push MUST stay consistent with them: do not introduce code or test changes that would itself be flagged by a pattern below. When the fix touches an area covered by a pattern, prefer the resolution the pattern endorses.")

	if ctx.Iteration > 1 {
		if ctx.Fixup {
			fmt.Fprintf(&sb, "NOTE: This is iteration %d. A previous iteration already folded fixes into this branch's commits and force-pushed, but checks are still failing. Before patching again, inspect what changed (e.g. `git log --oneline origin/%s..HEAD`, `git show <sha>`) and the failing logs below: if the SAME check is failing for the SAME reason, your previous approach did not work — change strategy or STOP and report instead of repeating it.\n\n", ctx.Iteration, ctx.BaseBranch)
		} else {
			fmt.Fprintf(&sb, "NOTE: This is iteration %d. A previous iteration already attempted a fix and pushed a commit, but checks are still failing. Before patching again, inspect the most recent commit on %s (e.g. `git log -1 -p`) and the failing logs below: if the SAME check is failing for the SAME reason, your previous approach did not work — change strategy or STOP and report instead of repeating it.\n\n", ctx.Iteration, ctx.HeadBranch)
		}
	}

	if len(ctx.FailedChecks) > 0 {
		sb.WriteString("## Failing Checks\n\n")
		for _, fc := range ctx.FailedChecks {
			fmt.Fprintf(&sb, "### %s — %s\n\n", fc.Name, fc.Conclusion)
			if fc.HTMLURL != "" {
				fmt.Fprintf(&sb, "- URL: %s\n", fc.HTMLURL)
			}
			if fc.OutputTitle != "" {
				fmt.Fprintf(&sb, "- Title: %s\n", fc.OutputTitle)
			}
			if fc.OutputSummary != "" {
				sb.WriteString("- Summary:\n\n```\n")
				sb.WriteString(strings.TrimSpace(fc.OutputSummary))
				sb.WriteString("\n```\n")
			}
			if fc.Logs != "" {
				sb.WriteString("- Failed-step logs (truncated to the last lines):\n\n```\n")
				sb.WriteString(tailLines(fc.Logs, 200))
				sb.WriteString("\n```\n")
			} else {
				sb.WriteString("- (No logs available — third-party check or logs expired.)\n")
			}
			sb.WriteString("\n")
		}
	}

	sb.WriteString(`## Diagnosis Workflow

Run these steps for EACH failing check above before editing any code:

1. CATEGORIZE the failure from the logs:
   - build/compile error (syntax, missing symbol, broken import)
   - test failure (assertion, panic, timeout)
   - lint / format finding (vet, golangci-lint, ruff, eslint, prettier)
   - type-check error (mypy, basedpyright, tsc, golangci-lint typecheck)
   - dependency / SBOM / security scan finding
   - infra / transient flake (network timeout, expired token, runner OOM)
2. LOCATE the offending code by opening the file at the cited path:line. Do not work from memory of what the log says — open the file.
3. UNDERSTAND THE INTENT: read surrounding code, the relevant test, and the PR title/body. Decide what the code SHOULD do.
4. CHOOSE A FIX STRATEGY:
   - Production code is wrong → fix production code; if no test caught the bug, add or extend one.
   - Test encodes outdated behavior → update the test, and explain in the report WHY the new expectation is correct.
   - Lint/format/type-check finding → apply the real fix (formatter, missing annotation, narrowed type). Suppression comments are forbidden unless they were already idiomatic in this file before this PR.
   - Flake / infra / unreachable secret → STOP and report. Do not commit a placebo fix.
5. APPLY the minimal change. If two failing checks share a single root cause, fix it once.
6. VERIFY LOCALLY: re-run the exact command that failed in CI (or the closest local equivalent — e.g. ` + "`go test ./internal/foo`, `pytest tests/test_x.py::test_y`, `golangci-lint run`, `tsc --noEmit`" + `). Capture the command and pass/fail in your final report. If the command cannot run in this environment, say so explicitly.
7. ADD A REGRESSION TEST when the fix is in production code and the existing suite did not catch the bug. Skip this step ONLY for: lint/format-only fixes, fixes inside test code itself, or fixes for failures that no unit/integration test could plausibly catch (e.g. SBOM signature, runtime infra config).

## What to do

1. Work through the diagnosis workflow above for every failing check.
`)

	if ctx.Fixup {
		fmt.Fprintf(&sb, "2. Fold each change into the commit it belongs to. This branch may carry more\n"+
			"   than one commit, and a fix for code that an earlier commit introduced\n"+
			"   belongs IN that commit — not in a new commit stacked on top.\n\n"+
			"   a. List the branch's own commits (oldest first):\n\n"+
			"      git log --oneline --reverse origin/%[1]s..HEAD\n\n"+
			"   b. For each distinct change, find the commit that introduced the code you\n"+
			"      are fixing — use `git blame <file>`, `git log -p -- <file>`, or `git log -S<symbol>`.\n"+
			"   c. Stage ONLY that change and record it as a fixup of its target commit:\n\n"+
			"      git add -- <files for this change>\n"+
			"      git commit --fixup=<target-sha>\n\n"+
			"      Repeat (c) for every change that maps to a different commit.\n"+
			"   d. Once every change is recorded as a fixup, fold them in non-interactively\n"+
			"      (no editor opens). Rebase against the merge-base so ONLY this branch's\n"+
			"      own commits are folded and the branch is never silently advanced onto a\n"+
			"      moved base:\n\n"+
			"      GIT_SEQUENCE_EDITOR=true git rebase -i --autosquash \"$(git merge-base origin/%[1]s HEAD)\"\n\n"+
			"   Create a NEW standalone commit ONLY when a change genuinely belongs to no\n"+
			"   existing commit on this branch (e.g. an entirely new file unrelated to any\n"+
			"   of them). That is the rare exception, not the default — and only then:\n\n"+
			"      git commit -s -m \"<concise summary>\" -m \"Failed checks: <comma-separated names>\" -m \"Assisted-by: Claude\"\n\n"+
			"3. Publish the rewritten branch:\n\n"+
			"      git push --force-with-lease origin HEAD:%[2]s\n\n"+
			"   The autosquash rebase rewrote the branch's commit SHAs, so a plain push is\n"+
			"   rejected. Use --force-with-lease (never plain --force): it publishes the\n"+
			"   fold while refusing to clobber commits you have not seen.\n\n",
			ctx.BaseBranch, ctx.HeadBranch)
	} else {
		fmt.Fprintf(&sb, "2. Stage your changes, create ONE follow-up commit using:\n\n"+
			"   git add -A\n"+
			"   git commit -s -m \"Fix failing CI checks (iteration %d)\" -m \"Failed checks: <comma-separated names>\" -m \"Assisted-by: Claude\"\n\n"+
			"3. Push to the PR head branch:\n\n"+
			"   git push origin HEAD:%s\n\n",
			ctx.Iteration, ctx.HeadBranch)
	}

	fmt.Fprintf(&sb, `4. After pushing, output a structured fix report in this exact shape:

   ## Fix Report (iteration %d)

   ### Per check
   - <check name>
     - Category: <build|test|lint|typecheck|deps|infra>
     - Root cause: <one sentence>
     - Fix: <files touched + one-sentence description of the change>
     - Local verification: <exact command run + pass/fail, OR "not reproducible in this environment — relying on CI">
     - Regression test: <added/extended test name, OR "n/a — <reason from the diagnosis workflow above>">
   ### Diff summary
   - Files: <comma-separated list>
   - Approx lines added/removed: <+N/-M>
`, ctx.Iteration)

	if ctx.Fixup {
		sb.WriteString("   - Commit strategy: <per change: \"folded into <sha> <subject>\" via fixup/autosquash, OR \"new commit — <why it belonged to no existing commit>\">\n")
	}

	sb.WriteString(`   ### Status
   STATUS: <DONE | DONE_WITH_CONCERNS | BLOCKED | NEEDS_CONTEXT>
   (DONE = all checks fixed and verified; DONE_WITH_CONCERNS = pushed but with reservations a human should see; BLOCKED = could not make progress; NEEDS_CONTEXT = missing information only a human can supply. The orchestrator reads this line and stops the loop on BLOCKED or NEEDS_CONTEXT.)

` + commitTrailerBlock() + `## Hard rules

`)

	if ctx.Fixup {
		fmt.Fprintf(&sb, "- Force-push ONLY with --force-with-lease, ONLY to the PR's own head branch (%[1]s), and ONLY to publish the autosquash rebase above. NEVER use plain --force. NEVER rebase, reorder, drop, or rewrite commits that already exist on the base branch (origin/%[2]s) — only this branch's own commits (origin/%[2]s..HEAD) may be folded.\n", ctx.HeadBranch, ctx.BaseBranch)
	} else {
		sb.WriteString("- NEVER force-push.\n")
	}

	sb.WriteString(`- PREFER to change only files on the failure surface. Reaching outside it is a last resort, reserved for the worst case where the failing check genuinely cannot be fixed any other way — then make the smallest out-of-scope change that works and call it out explicitly in the report. NEVER reach outside for convenience, drive-by cleanups, or unrelated improvements.
- NEVER skip, weaken, or suppress the failing check (see the "Do not cheat the check." pattern above for the explicit forbidden list).
`)
	sb.WriteString(noSkipHooksLine())
	sb.WriteString(`- NEVER bump dependencies that the failure log does not directly implicate.
- NEVER fabricate file paths, line numbers, or error messages — open the file before claiming.
- NEVER claim "fixed" without either local verification (step 6) or an explicit "not reproducible locally" note in the report.
- If you cannot diagnose a failure from the logs (truncation, infra flake, expired secret, third-party check without logs), STOP and explain — do not invent a fix.
- If there is nothing to commit after the fix attempt, do NOT create an empty commit; output the report and stop.
- It is OK to stop and report BLOCKED or NEEDS_CONTEXT. Bad work is worse than no work; escalating is not penalized. Emit the matching STATUS and do not push a placebo fix.
- If the same check is failing for the same root cause as the previous iteration, STOP and report — repeating the failed approach will not help.
`)

	return sb.String()
}

// BuildBareFixPrompt assembles a self-contained fix prompt that does NOT
// embed a pre-fetched failing-check analysis. It is meant to be copy-pasted
// into a manual Claude Code session that is ALREADY running inside a
// checkout of the PR's head branch — no `gh pr checkout`, no working-tree
// setup. That session discovers the failing checks itself, fixes the code,
// and pushes a follow-up commit.
//
// The orchestrator-driven prompt (BuildFixPrompt) is preferred when this
// tool is driving the loop, because it can hand Claude the failing logs
// directly. The bare variant trades that convenience for portability:
// the manual session works from the PR reference plus its own checkout.
//
// The orchestrator clones the target repo at prompt-build time so this
// prompt can ship with the detected technology tags AND the tech-filtered
// review-pattern catalog inlined — the manual Claude session does not need
// access to planwerk-agent or its pattern dirs.
func BuildBareFixPrompt(ctx fix.BareContext) string {
	var sb strings.Builder

	sb.WriteString(`You are a Staff Engineer fixing failing CI checks on a GitHub pull request.

`)
	sb.WriteString(baselineBehavioralPrinciples)
	sb.WriteString(outputLanguageBlock())
	sb.WriteString(fixThinkingPatterns())

	fmt.Fprintf(&sb, "## Pull Request\n\n- Repository: %s\n- PR #%d\n\n",
		ctx.RepoFullName, ctx.PRNumber)

	if len(ctx.TechTags) > 0 {
		fmt.Fprintf(&sb, "Detected technologies in the target repo (used to filter the pattern catalog below): %s\n\n",
			strings.Join(ctx.TechTags, ", "))
	}

	sb.WriteString("You are already running inside a checkout of this PR's head branch. Do NOT re-checkout, do NOT clone. Operate on the working tree you have. You run as a one-shot session: discover the failing checks yourself, fix them, publish the fix, and report.\n\n")

	sb.WriteString(renderBareCatalog(ctx.PatternCatalog, ctx.HasRepoLocalRefs))

	fmt.Fprintf(&sb, `## Discover failing checks

Do NOT guess what is failing. Use the GitHub CLI to enumerate the PR's check runs and pull the failed-step logs.

1. List the checks for the PR:

`+"```"+`
gh pr checks %d --repo %s
`+"```"+`

2. For each check reported as "fail" / "failure", record its name, conclusion, and the link to the run / job.

3. For each Actions-backed failing check, fetch the failed-step logs:

`+"```"+`
gh run view <run-id> --repo %s --log-failed
`+"```"+`

   The failure tail is what matters — CI logs cluster errors at the end. Read to the bottom of each log.

4. For third-party checks (no Actions run id), you cannot pull logs from the CLI. Open the check URL to investigate. If the cause cannot be diagnosed from the visible signal, STOP and report — do not invent a fix.

`, ctx.PRNumber, ctx.RepoFullName, ctx.RepoFullName)

	sb.WriteString(`## Diagnosis Workflow

Run these steps for EACH failing check before editing any code:

1. CATEGORIZE the failure from the logs:
   - build/compile error (syntax, missing symbol, broken import)
   - test failure (assertion, panic, timeout)
   - lint / format finding (vet, golangci-lint, ruff, eslint, prettier)
   - type-check error (mypy, basedpyright, tsc, golangci-lint typecheck)
   - dependency / SBOM / security scan finding
   - infra / transient flake (network timeout, expired token, runner OOM)
2. LOCATE the offending code by opening the file at the cited path:line. Do not work from memory of what the log says — open the file.
3. UNDERSTAND THE INTENT: read surrounding code, the relevant test, and the PR title/body. Decide what the code SHOULD do.
4. CHOOSE A FIX STRATEGY:
   - Production code is wrong → fix production code; if no test caught the bug, add or extend one.
   - Test encodes outdated behavior → update the test, and explain in the report WHY the new expectation is correct.
   - Lint/format/type-check finding → apply the real fix (formatter, missing annotation, narrowed type). Suppression comments are forbidden unless they were already idiomatic in this file before this PR.
   - Flake / infra / unreachable secret → STOP and report. Do not commit a placebo fix.
5. APPLY the minimal change. If two failing checks share a single root cause, fix it once.
6. VERIFY LOCALLY: re-run the exact command that failed in CI (or the closest local equivalent — e.g. ` + "`go test ./internal/foo`, `pytest tests/test_x.py::test_y`, `golangci-lint run`, `tsc --noEmit`" + `). Capture the command and pass/fail in your final report. If the command cannot run in this environment, say so explicitly.
7. ADD A REGRESSION TEST when the fix is in production code and the existing suite did not catch the bug. Skip this step ONLY for: lint/format-only fixes, fixes inside test code itself, or fixes for failures that no unit/integration test could plausibly catch (e.g. SBOM signature, runtime infra config).

## What to do

1. Run the discovery steps above to enumerate failing checks and pull their logs.
2. Run the diagnosis workflow for every failing check.
3. Apply the minimal change(s).
4. Verify locally where possible. Re-read your diff. Remove anything not required.
5. Self-review the diff against the original PR scope. Keep the fix inside it unless reaching outside is the only way to make the failing check pass — and if you must, confine the out-of-scope change to the minimum and call it out in the report.
`)

	if ctx.Fixup {
		fmt.Fprintf(&sb, `6. Determine the PR's base branch so you can bound the fold to this branch's
   own commits, then fetch it so origin/<base> exists:

   gh pr view %d --repo %s --json baseRefName -q .baseRefName
   git fetch origin <base>

   Use the printed name wherever <base> appears below.
7. Fold each change into the commit it belongs to. This branch may carry more
   than one commit, and a fix for code that an earlier commit introduced
   belongs IN that commit — not in a new commit stacked on top.

   a. List the branch's own commits (oldest first):

      git log --oneline --reverse origin/<base>..HEAD

   b. For each distinct change, find the commit that introduced the code you
      are fixing (git blame <file>, git log -p -- <file>, or git log -S<symbol>).
   c. Stage ONLY that change and record it as a fixup of its target commit:

      git add -- <files for this change>
      git commit --fixup=<target-sha>

      Repeat (c) for every change that maps to a different commit.
   d. Once every change is recorded as a fixup, fold them in non-interactively
      (no editor opens), bounded to the merge-base so ONLY this branch's own
      commits are folded:

      GIT_SEQUENCE_EDITOR=true git rebase -i --autosquash "$(git merge-base origin/<base> HEAD)"

   Create a NEW standalone commit ONLY when a change genuinely belongs to no
   existing commit on this branch (e.g. an entirely new file unrelated to any
   of them). That is the rare exception, not the default — and only then:

      git commit -s -m "<concise summary>" -m "Failed checks: <comma-separated names>" -m "Assisted-by: Claude"

8. Publish the rewritten branch:

      git push --force-with-lease origin HEAD

   The autosquash rebase rewrote the branch's commit SHAs, so a plain push is
   rejected. Use --force-with-lease (never plain --force): it publishes the fold
   while refusing to clobber commits you have not seen.

`, ctx.PRNumber, ctx.RepoFullName)
	} else {
		sb.WriteString("6. Stage your changes and create ONE follow-up commit:\n\n" +
			"   git add -A\n" +
			"   git commit -s -m \"Fix failing CI checks\" -m \"Failed checks: <comma-separated names>\" -m \"Assisted-by: Claude\"\n\n" +
			"7. Push back to the PR's head branch:\n\n" +
			"   git push origin HEAD\n\n")
	}

	sb.WriteString(`After pushing, output a structured fix report in this exact shape:

   ## Fix Report

   ### Per check
   - <check name>
     - Category: <build|test|lint|typecheck|deps|infra>
     - Root cause: <one sentence>
     - Fix: <files touched + one-sentence description of the change>
     - Local verification: <exact command run + pass/fail, OR "not reproducible in this environment — relying on CI">
     - Regression test: <added/extended test name, OR "n/a — <reason from the diagnosis workflow above>">
   ### Diff summary
   - Files: <comma-separated list>
   - Approx lines added/removed: <+N/-M>
`)

	if ctx.Fixup {
		sb.WriteString("   - Commit strategy: <per change: \"folded into <sha> <subject>\" via fixup/autosquash, OR \"new commit — <why it belonged to no existing commit>\">\n")
	}

	sb.WriteString(`   ### Status
   STATUS: <DONE | DONE_WITH_CONCERNS | BLOCKED | NEEDS_CONTEXT>
   (DONE = all checks fixed and verified; DONE_WITH_CONCERNS = pushed but with reservations a human should see; BLOCKED = could not make progress; NEEDS_CONTEXT = missing information only a human can supply. The orchestrator reads this line and stops the loop on BLOCKED or NEEDS_CONTEXT.)

` + commitTrailerBlock() + `## Hard rules

`)

	if ctx.Fixup {
		sb.WriteString("- Force-push ONLY with --force-with-lease, ONLY to the PR's own head branch, and ONLY to publish the autosquash rebase above. NEVER use plain --force. NEVER rebase, reorder, drop, or rewrite commits that already exist on the base branch — only this branch's own commits (origin/<base>..HEAD) may be folded.\n")
	} else {
		sb.WriteString("- NEVER force-push.\n")
	}

	sb.WriteString(`- PREFER to change only files on the failure surface. Reaching outside it is a last resort, reserved for the worst case where the failing check genuinely cannot be fixed any other way — then make the smallest out-of-scope change that works and call it out explicitly in the report. NEVER reach outside for convenience, drive-by cleanups, or unrelated improvements.
- NEVER skip, weaken, or suppress the failing check (see the "Do not cheat the check." pattern above for the explicit forbidden list).
`)
	sb.WriteString(noSkipHooksLine())
	sb.WriteString(`- NEVER bump dependencies that the failure log does not directly implicate.
- NEVER fabricate file paths, line numbers, or error messages — open the file before claiming.
- NEVER claim "fixed" without either local verification (step 6) or an explicit "not reproducible locally" note in the report.
- If you cannot diagnose a failure from the logs (truncation, infra flake, expired secret, third-party check without logs), STOP and explain — do not invent a fix.
- If there is nothing to commit after the fix attempt, do NOT create an empty commit; output the report and stop.
- It is OK to stop and report BLOCKED or NEEDS_CONTEXT. Bad work is worse than no work; escalating is not penalized. Emit the matching STATUS and do not push a placebo fix.
`)

	return sb.String()
}

// tailLines returns the last n lines of s. CI logs are often huge — keeping
// only the trailing lines preserves the failure tail (where Go test panics,
// linter errors, and shell exit codes land) while staying inside the prompt
// budget.
func tailLines(s string, n int) string {
	if n <= 0 {
		return ""
	}
	lines := strings.Split(s, "\n")
	if len(lines) <= n {
		return s
	}
	return strings.Join(lines[len(lines)-n:], "\n")
}
