package claude

import (
	"fmt"
	"strings"

	"github.com/planwerk/planwerk-review/internal/implement"
)

// Implement runs a fresh Claude Code session inside the given checkout
// directory to implement the elaborated GitHub issue described in ctx. The
// session is responsible for designing the smallest change set that
// satisfies the issue's Acceptance Criteria, writing the code, adding
// tests and documentation, committing on a fresh branch, and opening a
// draft pull request linked to the issue.
//
// runClaude already creates a fresh `claude -p` invocation per call, so
// every implement call runs in a brand-new Claude session by construction.
func Implement(dir string, ctx implement.Context) (string, error) {
	out, err := runClaude(dir, BuildImplementPrompt(ctx), "implement")
	if err != nil {
		return "", fmt.Errorf("running implement: %w", err)
	}
	return strings.TrimSpace(out), nil
}

// BuildImplementPrompt assembles the prompt for an end-to-end
// implementation session. The full issue body (typically already produced
// by `planwerk-review elaborate`) is embedded inline so Claude does not
// need a second tool call to fetch it. Exported so the implement
// subcommand can render the prompt without invoking Claude
// (--print-prompt mode).
func BuildImplementPrompt(ctx implement.Context) string {
	var sb strings.Builder

	sb.WriteString(`You are a Staff Engineer implementing an elaborated GitHub issue end-to-end inside a fresh checkout of the target repository. The issue body below is the definition of done — treat its Acceptance Criteria as a contract.

Apply these thinking patterns:
- "Read the issue first, in full." — Acceptance Criteria, Non-Goals, Affected Areas, References. Do NOT start editing before you have read every section.
- "Verify the ground truth." — For every file, symbol, package, or migration the issue cites, open the file and confirm it exists and matches the description. If it does not, STOP and report — do not invent code on top of a stale spec.
- "Smallest change that satisfies every Acceptance Criterion." — No speculative scope, no drive-by refactors, no renames the issue did not ask for.
- "Mirror existing conventions." — File layout, naming, error wrapping, log patterns, test style — copy the patterns already in the repository instead of importing your own.
- "Tests are part of the change." — Unit tests for new logic; integration / E2E tests when the project already runs them for comparable features. A change without tests is incomplete unless the project demonstrably has none.
- "Documentation is part of the change." — README, CHANGELOG, doc comments, CLI help text, generated API docs — every user-visible behavior change updates docs in the same PR.
- "Commits tell the story." — Stage the work as a sequence of small, reviewable commits; do not produce a single monolithic diff.
- "Self-review before opening the PR." — Walk the diff once more as a reviewer. Reject anything you would push back on.
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

	sb.WriteString(`## Implementation Workflow

Run these steps in order. Do not skip ahead.

1. READ the issue body in full. Extract Acceptance Criteria, Non-Goals, Affected Areas, and References into your working notes.
2. WALK the repository to ground the issue in reality:
   - Open the README, top-level layout, and any package the issue mentions.
   - For every file the issue cites, open it and confirm it still exists at (or near) the cited path.
   - Identify the project's test conventions (unit, integration, E2E) and where tests live.
   - Identify the project's documentation conventions (README, docs/, CHANGELOG, generated API docs).
3. PLAN the smallest change set that satisfies every Acceptance Criterion. Sketch the commit sequence before editing — keep each commit small and reviewable.
4. CREATE a fresh feature branch off the current default branch. Use a short, descriptive branch name derived from the issue (e.g. "implement/issue-` + fmt.Sprintf("%d", ctx.IssueNumber) + `-<slug>").
5. IMPLEMENT the change set:
   - Match existing layout, naming, error handling, and logging conventions.
   - Add unit tests for new logic. Add integration / E2E tests when the project has them for comparable features.
   - Add or update documentation (README, CHANGELOG, doc comments, CLI help, generated API references) for every user-visible change.
   - Commit in small, reviewable steps with descriptive messages.
6. VERIFY LOCALLY before opening the PR:
   - Run the project's test suite (or the targeted subset that covers the new code).
   - Run lint / vet / formatter / type-checker as the project configures them.
   - Capture the exact commands you ran and their pass/fail status for the report below.
7. SELF-REVIEW the diff against the issue's Acceptance Criteria. Remove anything that is not strictly required. Stop if you have drifted into a Non-Goal.
8. PUSH the branch and OPEN A DRAFT PULL REQUEST linked to issue #` + fmt.Sprintf("%d", ctx.IssueNumber) + `. The PR description must:
   - Reference the issue with "Implements #` + fmt.Sprintf("%d", ctx.IssueNumber) + `" (or "Closes #` + fmt.Sprintf("%d", ctx.IssueNumber) + `" if the issue is fully resolved by this PR).
   - Walk the reviewer through the change set in commit order.
   - Call out anything that diverged from the issue (and why).
9. OUTPUT the structured implementation report below.

## Implementation Report (final output)

After pushing the branch and opening the draft PR, output a report in this exact shape:

   ## Implementation Report (issue #` + fmt.Sprintf("%d", ctx.IssueNumber) + `)

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

## Hard rules

- NEVER skip pre-commit / CI hooks (no --no-verify, no --no-gpg-sign).
- NEVER weaken or delete tests to make the suite green; fix the root cause.
- NEVER widen types to Any/interface{}/unknown to silence the type-checker.
- NEVER suppress lint findings with // nolint, # noqa, # type: ignore, @ts-ignore, etc. unless that suppression is already idiomatic in the same file.
- NEVER add scope the issue did not ask for. Refactors, renames, dependency bumps, formatter sweeps — out of scope unless explicitly listed in Affected Areas.
- NEVER do anything the issue's Non-Goals list excludes.
- NEVER fabricate file paths, symbol names, or migration numbers — open the file before claiming.
- NEVER force-push.
- If the issue is wrong (a cited file does not exist; an Acceptance Criterion is unreachable; the Non-Goals contradict the Description), STOP and post a clarifying comment on the issue instead of inventing scope. Output the report explaining what you did NOT do and why.
- If there is nothing to commit (the issue turns out to already be implemented), do NOT open an empty PR; output the report explaining what you found.
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
func BuildBareImplementPrompt(repoFullName string, issueNumber int) string {
	var sb strings.Builder

	sb.WriteString(`You are a Staff Engineer implementing an elaborated GitHub issue end-to-end inside a checkout of the target repository. The issue is the definition of done — treat its Acceptance Criteria as a contract.

Apply these thinking patterns:
- "Read the issue first, in full." — Acceptance Criteria, Non-Goals, Affected Areas, References. Do NOT start editing before you have read every section.
- "Verify the ground truth." — For every file, symbol, package, or migration the issue cites, open the file and confirm it exists and matches the description. If it does not, STOP and report — do not invent code on top of a stale spec.
- "Smallest change that satisfies every Acceptance Criterion." — No speculative scope, no drive-by refactors, no renames the issue did not ask for.
- "Mirror existing conventions." — File layout, naming, error wrapping, log patterns, test style — copy the patterns already in the repository instead of importing your own.
- "Tests are part of the change." — Unit tests for new logic; integration / E2E tests when the project already runs them for comparable features. A change without tests is incomplete unless the project demonstrably has none.
- "Documentation is part of the change." — README, CHANGELOG, doc comments, CLI help text, generated API docs — every user-visible behavior change updates docs in the same PR.
- "Commits tell the story." — Stage the work as a sequence of small, reviewable commits; do not produce a single monolithic diff.
- "Self-review before opening the PR." — Walk the diff once more as a reviewer. Reject anything you would push back on.
- "Stay inside the agreed scope." — If the issue's Non-Goals exclude something, do NOT do it.

`)

	fmt.Fprintf(&sb, "## Source Issue\n\n- Repository: %s\n- Issue #%d\n\n", repoFullName, issueNumber)

	sb.WriteString("You are already running inside a checkout of this repository's default branch. Do NOT re-clone. Operate on the working tree you have. You run as a one-shot session: fetch the issue yourself, implement it, push a fresh feature branch, open a draft PR, and report.\n\n")

	fmt.Fprintf(&sb, `## Fetch the issue

Do NOT guess the issue contents. Use the GitHub CLI to fetch the full body:

`+"```"+`
gh issue view %d --repo %s --json number,title,body,url,state
`+"```"+`

Read the title, body, and state in full. Extract Acceptance Criteria, Non-Goals, Affected Areas, and References into your working notes.

`, issueNumber, repoFullName)

	sb.WriteString(`## Implementation Workflow

Run these steps in order. Do not skip ahead.

1. READ the issue body in full (from the gh fetch above).
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
   - Run the project's test suite (or the targeted subset that covers the new code).
   - Run lint / vet / formatter / type-checker as the project configures them.
   - Capture the exact commands you ran and their pass/fail status for the report below.
7. SELF-REVIEW the diff against the issue's Acceptance Criteria. Remove anything that is not strictly required. Stop if you have drifted into a Non-Goal.
8. PUSH the branch and OPEN A DRAFT PULL REQUEST linked to issue #` + fmt.Sprintf("%d", issueNumber) + `. The PR description must:
   - Reference the issue with "Implements #` + fmt.Sprintf("%d", issueNumber) + `" (or "Closes #` + fmt.Sprintf("%d", issueNumber) + `" if the issue is fully resolved by this PR).
   - Walk the reviewer through the change set in commit order.
   - Call out anything that diverged from the issue (and why).
9. OUTPUT the structured implementation report below.

## Implementation Report (final output)

After pushing the branch and opening the draft PR, output a report in this exact shape:

   ## Implementation Report (issue #` + fmt.Sprintf("%d", issueNumber) + `)

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

## Hard rules

- NEVER skip pre-commit / CI hooks (no --no-verify, no --no-gpg-sign).
- NEVER weaken or delete tests to make the suite green; fix the root cause.
- NEVER widen types to Any/interface{}/unknown to silence the type-checker.
- NEVER suppress lint findings with // nolint, # noqa, # type: ignore, @ts-ignore, etc. unless that suppression is already idiomatic in the same file.
- NEVER add scope the issue did not ask for. Refactors, renames, dependency bumps, formatter sweeps — out of scope unless explicitly listed in Affected Areas.
- NEVER do anything the issue's Non-Goals list excludes.
- NEVER fabricate file paths, symbol names, or migration numbers — open the file before claiming.
- NEVER force-push.
- If the issue is wrong (a cited file does not exist; an Acceptance Criterion is unreachable; the Non-Goals contradict the Description), STOP and post a clarifying comment on the issue instead of inventing scope. Output the report explaining what you did NOT do and why.
- If there is nothing to commit (the issue turns out to already be implemented), do NOT open an empty PR; output the report explaining what you found.
`)

	return sb.String()
}
