package claude

import (
	"fmt"
	"strings"

	"github.com/planwerk/planwerk-review/internal/fix"
	"github.com/planwerk/planwerk-review/internal/patterns"
)

// Fix runs a fresh Claude Code session inside the given checkout directory
// to repair the failing checks described in ctx. The session is responsible
// for applying minimal-invasive code changes, simplifying and self-reviewing
// the diff, then creating a follow-up commit and pushing it to the PR head
// branch.
//
// runClaude already creates a fresh `claude -p` invocation per call, so each
// iteration of the fix loop runs in a brand-new Claude session by construction.
func Fix(dir string, ctx fix.Context) (string, error) {
	out, err := runClaude(dir, BuildFixPrompt(ctx), "fix")
	if err != nil {
		return "", fmt.Errorf("running fix: %w", err)
	}
	return strings.TrimSpace(out), nil
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
	sb.WriteString(`Apply these task-specific thinking patterns on top of the baseline above:
- "Diagnose before patching." — Read every failing log to the bottom. Classify the failure category (build/compile, test, lint/format, type-check, dependency/security scan, infra/flake) BEFORE editing any file.
- "Find the root cause." — A failing assertion is a symptom; the broken invariant in the code under test is the cause. Fix the cause, not the symptom.
- "Reproduce, then verify." — When the failing command can be re-run in this checkout (test, lint, build, type-check), run it locally to reproduce the failure FIRST, then run it again after your edits to confirm the fix BEFORE pushing.
- "Open the file, do not guess." — When a log cites a file:line, open the actual source. Never invent code shapes, error messages, or line numbers from the log alone.
- "Do not cheat the check." — Never disable, skip, or weaken a check to make it pass. Forbidden: t.Skip / pytest.skip / xit / xdescribe added solely to bypass; // nolint, # noqa, # type: ignore, @ts-ignore, @SuppressWarnings added solely to silence; widening types to Any/interface{}/unknown to silence type-checkers; deleting or relaxing assertions; deleting test cases; pinning to an older dependency to dodge a security finding; --no-verify on commits.
- "Minimal-invasive change." — Touch the smallest surface area that resolves each failure. No drive-by refactors, no reformatting unrelated code, no dependency bumps that are not directly implicated.
- "Regression guard." — If the broken behavior is in production code and existing tests did not catch it, extend or add a test that fails before your fix and passes after.
- "Simplify the diff." — Re-read your own diff and remove anything not strictly required. Prefer fewer lines, fewer files, fewer abstractions.
- "Self-review before committing." — Walk through the diff once more as the reviewer. Reject anything you would push back on.
- "Stay inside the PR." — The PR has a stated intent (title + body). Your fix must serve it. Do not change unrelated files.

`)

	fmt.Fprintf(&sb, "## Pull Request\n\n- Repository: %s\n- PR #%d: %s\n- Head branch: %s (committed to and pushed by you)\n- Head SHA at start of this iteration: %s\n- Iteration: %d of %d (max)\n\n",
		ctx.RepoFullName, ctx.PRNumber, ctx.PRTitle, ctx.HeadBranch, ctx.HeadSHA, ctx.Iteration, ctx.MaxIterations)

	if len(ctx.Patterns) > 0 {
		sb.WriteString("## Project Review Patterns to Honor\n\n")
		sb.WriteString("These patterns are the catalog the project's review/audit/elaborate tools share — including any project-specific patterns shipped under `.planwerk/review_patterns/` in this repository. The fix you push MUST stay consistent with them: do not introduce code or test changes that would itself be flagged by a pattern below. When the fix touches an area covered by a pattern, prefer the resolution the pattern endorses.\n\n")
		sb.WriteString("<review-patterns>\n")
		sb.WriteString(patterns.FormatGroupedForPrompt(ctx.Patterns, ctx.MaxPatterns))
		sb.WriteString("</review-patterns>\n\n")
	}

	if ctx.Iteration > 1 {
		fmt.Fprintf(&sb, "NOTE: This is iteration %d. A previous iteration already attempted a fix and pushed a commit, but checks are still failing. Before patching again, inspect the most recent commit on %s (e.g. `git log -1 -p`) and the failing logs below: if the SAME check is failing for the SAME reason, your previous approach did not work — change strategy or STOP and report instead of repeating it.\n\n", ctx.Iteration, ctx.HeadBranch)
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

1. Run the diagnosis workflow for every failing check.
2. Apply the minimal change(s).
3. Verify locally where possible. Re-read your diff. Remove anything not required.
4. Self-review the diff against the original PR scope. Stop if your fix is drifting outside it.
5. Stage your changes, create ONE follow-up commit using:

   git add -A
   git commit -m "Fix failing CI checks (iteration ` + fmt.Sprintf("%d", ctx.Iteration) + `)" -m "Failed checks: <comma-separated names>" --trailer "Co-Authored-By: planwerk-review fix <noreply@planwerk>"

6. Push to the PR head branch:

   git push origin HEAD:` + ctx.HeadBranch + `

7. After pushing, output a structured fix report in this exact shape:

   ## Fix Report (iteration ` + fmt.Sprintf("%d", ctx.Iteration) + `)

   ### Per check
   - <check name>
     - Category: <build|test|lint|typecheck|deps|infra>
     - Root cause: <one sentence>
     - Fix: <files touched + one-sentence description of the change>
     - Local verification: <exact command run + pass/fail, OR "not reproducible in this environment — relying on CI">
     - Regression test: <added/extended test name, OR "n/a — <reason from step 7 above>">
   ### Diff summary
   - Files: <comma-separated list>
   - Approx lines added/removed: <+N/-M>
   ### Status
   STATUS: <DONE | DONE_WITH_CONCERNS | BLOCKED | NEEDS_CONTEXT>
   (DONE = all checks fixed and verified; DONE_WITH_CONCERNS = pushed but with reservations a human should see; BLOCKED = could not make progress; NEEDS_CONTEXT = missing information only a human can supply. The orchestrator reads this line and stops the loop on BLOCKED or NEEDS_CONTEXT.)

## Hard rules

- NEVER force-push.
- NEVER change files outside the failure surface. If a fix would require it, STOP and explain instead of committing.
- NEVER skip, weaken, or suppress the failing check (see the "Do not cheat the check." pattern above for the explicit forbidden list).
- NEVER skip pre-commit / CI hooks (no --no-verify, no --no-gpg-sign).
- NEVER bump dependencies that the failure log does not directly implicate.
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
// access to planwerk-review or its pattern dirs.
func BuildBareFixPrompt(ctx fix.BareContext) string {
	var sb strings.Builder

	sb.WriteString(`You are a Staff Engineer fixing failing CI checks on a GitHub pull request.

`)
	sb.WriteString(baselineBehavioralPrinciples)
	sb.WriteString(`Apply these task-specific thinking patterns on top of the baseline above:
- "Diagnose before patching." — Read every failing log to the bottom. Classify the failure category (build/compile, test, lint/format, type-check, dependency/security scan, infra/flake) BEFORE editing any file.
- "Find the root cause." — A failing assertion is a symptom; the broken invariant in the code under test is the cause. Fix the cause, not the symptom.
- "Reproduce, then verify." — When the failing command can be re-run in this checkout (test, lint, build, type-check), run it locally to reproduce the failure FIRST, then run it again after your edits to confirm the fix BEFORE pushing.
- "Open the file, do not guess." — When a log cites a file:line, open the actual source. Never invent code shapes, error messages, or line numbers from the log alone.
- "Do not cheat the check." — Never disable, skip, or weaken a check to make it pass. Forbidden: t.Skip / pytest.skip / xit / xdescribe added solely to bypass; // nolint, # noqa, # type: ignore, @ts-ignore, @SuppressWarnings added solely to silence; widening types to Any/interface{}/unknown to silence type-checkers; deleting or relaxing assertions; deleting test cases; pinning to an older dependency to dodge a security finding; --no-verify on commits.
- "Minimal-invasive change." — Touch the smallest surface area that resolves each failure. No drive-by refactors, no reformatting unrelated code, no dependency bumps that are not directly implicated.
- "Regression guard." — If the broken behavior is in production code and existing tests did not catch it, extend or add a test that fails before your fix and passes after.
- "Simplify the diff." — Re-read your own diff and remove anything not strictly required. Prefer fewer lines, fewer files, fewer abstractions.
- "Self-review before committing." — Walk through the diff once more as the reviewer. Reject anything you would push back on.
- "Stay inside the PR." — The PR has a stated intent (title + body). Your fix must serve it. Do not change unrelated files.

`)

	fmt.Fprintf(&sb, "## Pull Request\n\n- Repository: %s\n- PR #%d\n\n",
		ctx.RepoFullName, ctx.PRNumber)

	if len(ctx.TechTags) > 0 {
		fmt.Fprintf(&sb, "Detected technologies in the target repo (used to filter the pattern catalog below): %s\n\n",
			strings.Join(ctx.TechTags, ", "))
	}

	sb.WriteString("You are already running inside a checkout of this PR's head branch. Do NOT re-checkout, do NOT clone. Operate on the working tree you have. You run as a one-shot session: discover the failing checks yourself, fix them, push a single follow-up commit, and report.\n\n")

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
5. Self-review the diff against the original PR scope. Stop if your fix is drifting outside it.
6. Stage your changes and create ONE follow-up commit:

   git add -A
   git commit -m "Fix failing CI checks" -m "Failed checks: <comma-separated names>" --trailer "Co-Authored-By: planwerk-review fix <noreply@planwerk>"

7. Push back to the PR's head branch:

   git push origin HEAD

8. After pushing, output a structured fix report in this exact shape:

   ## Fix Report

   ### Per check
   - <check name>
     - Category: <build|test|lint|typecheck|deps|infra>
     - Root cause: <one sentence>
     - Fix: <files touched + one-sentence description of the change>
     - Local verification: <exact command run + pass/fail, OR "not reproducible in this environment — relying on CI">
     - Regression test: <added/extended test name, OR "n/a — <reason from step 7 above>">
   ### Diff summary
   - Files: <comma-separated list>
   - Approx lines added/removed: <+N/-M>
   ### Status
   STATUS: <DONE | DONE_WITH_CONCERNS | BLOCKED | NEEDS_CONTEXT>
   (DONE = all checks fixed and verified; DONE_WITH_CONCERNS = pushed but with reservations a human should see; BLOCKED = could not make progress; NEEDS_CONTEXT = missing information only a human can supply. The orchestrator reads this line and stops the loop on BLOCKED or NEEDS_CONTEXT.)

## Hard rules

- NEVER force-push.
- NEVER change files outside the failure surface. If a fix would require it, STOP and explain instead of committing.
- NEVER skip, weaken, or suppress the failing check (see the "Do not cheat the check." pattern above for the explicit forbidden list).
- NEVER skip pre-commit / CI hooks (no --no-verify, no --no-gpg-sign).
- NEVER bump dependencies that the failure log does not directly implicate.
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

