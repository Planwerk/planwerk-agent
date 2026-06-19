# Implement an issue

Use `implement` to take an elaborated GitHub issue and drive it to a draft pull
request: a read-only planning session grounds the issue in the code, then an
implement session executes the plan end to end (code, tests, documentation,
commits) and opens the PR.

```bash
# Plan and implement an elaborated issue
planwerk-review implement owner/repo#123

# Skip the planning session and implement directly
planwerk-review implement --no-plan owner/repo#123

# Force a fresh plan instead of reusing one already posted on the issue
planwerk-review implement --no-plan-reuse owner/repo#123

# Run an independent verification pass against the Acceptance Criteria
planwerk-review implement --verify owner/repo#123

# Red-team the produced diff for introduced bugs (composes with --verify)
planwerk-review implement --verify-adversarial owner/repo#123

# Skip the automatic simplify pass (on by default)
planwerk-review implement --no-simplify owner/repo#123

# Skip the automatic review-and-fix pass (on by default)
planwerk-review implement --no-review owner/repo#123
```

See the [CLI reference](/reference/cli#implement) for every flag, including the
`--print-prompt` / `--print-plan-prompt` / `--print-bare-prompt` escape hatches
and the `--plan-model` / `--plan-effort` planning-session overrides.

## How it works

1. **Issue Input**: A GitHub issue reference (URL or `owner/repo#number`), typically already elaborated via `elaborate`.
2. **Fetch Issue**: Title, body, URL, and state are fetched via `gh issue view`.
3. **Clone**: The repository is cloned into a temp directory (or the current checkout is used with `--local`).
4. **Pattern Load**: The same pattern catalog used by `review` / `audit` / `elaborate` is loaded, filtered by detected technologies.
5. **Planning Session**: A read-only Claude Code session on the dedicated planning model (`--plan-model`, default `fable`, env: `PLANWERK_PLAN_MODEL`) at the dedicated planning effort (`--plan-effort`, default `max`, env: `PLANWERK_PLAN_EFFORT`) grounds the issue in the actual code — verifying every cited file and symbol — and emits a structured implementation plan: change set, commit sequence, test plan, documentation plan, verification commands, risks. When the issue is a **Sub Issue** of a Meta Issue, the Meta Issue and the sibling Sub Issues are fetched (best-effort, via the GitHub GraphQL API) and injected into the planning prompt, so the plan covers only this issue's slice of the larger effort, honors the Meta Issue's framing, and defers a shared task's remaining part to the sibling that owns it with an explicit `#K` cross-reference. This context flows into the plan; the implement session itself then works from that plan. `--print-plan-prompt` renders the planning prompt with this context included. The plan steers the entire implementation, so it gets the strongest model at the largest thinking budget. When the plan is ready it is posted back onto the source issue as a comment (disable with `--no-plan-comment`), so the brief that drives the implementation is recorded on the issue itself; an escalated plan is posted too, so the human who must clarify a `STATUS: BLOCKED` / `NEEDS_CONTEXT` issue sees it there. A plan that reports `STATUS: BLOCKED` or `NEEDS_CONTEXT` aborts the run before any code is written. Skip the phase entirely with `--no-plan`. If planwerk-review already posted a plan on the issue on an earlier run — identified by its `## Implementation Plan` heading and attribution footer — that plan is reused verbatim instead of running the session again (the footer is stripped, no duplicate comment is posted, and the reused plan is still subject to the same `STATUS` abort); pass `--no-plan-reuse` to override this and plan afresh when the issue has changed since. Reading the issue's comments to find a reusable plan is load-bearing: if that lookup fails the run aborts rather than silently paying for a fresh planning pass.
6. **Implement Session**: A fresh Claude Code session in auto mode (`--permission-mode auto`) receives the plan embedded in its prompt and executes it end-to-end: code, tests, documentation, small reviewable commits on a fresh feature branch, draft pull request linked to the issue. The implement session uses the global `--claude-model` (default `opus`) and `--claude-effort` (default `xhigh`); only the planning session runs on the dedicated `--plan-model` / `--plan-effort`. Once the session finishes, its implementation report is posted back onto the source issue as a comment (disable with `--no-report-comment`), just like the plan in step 5 — on every run, including ones where nothing was implemented or the attempt failed, so the course of the implementation is recorded on the issue itself.
7. **Simplify Pass (default-on)**: Once the draft PR is open, a read-only ponytail-style finder reviews the produced diff through a minimalist decision ladder (prefer not building it (YAGNI) → the standard library → a platform/framework-native feature → an already-present dependency → a one-liner → only then the minimum new code) and emits a delete/collapse list of over-engineering. When it finds something, a fresh auto-mode session applies the simplifications and folds each into the commit it belongs to (`git commit --fixup` + `git rebase --autosquash`), then force-pushes the leaner branch to the PR's own head with `git push --force-with-lease` (never plain `--force`, never the base branch). It runs **before** the review-and-fix pass, so that pass and the verification passes assess the smaller, leaner diff. A hard guardrail keeps it from ever removing validation, error handling, security, or accessibility code, or deleting/weakening tests or assertions; any finding that touches a test or assertion file is dropped before the apply step. The report is posted as a PR comment (best-effort — a failed post never aborts the run), and a `STATUS: BLOCKED` / `NEEDS_CONTEXT` report stops the pass without retrying. When there is nothing to simplify it is a clean no-op: no commit, no force-push, no PR comment. The whole pass is non-fatal. Disable it with `--no-simplify`.
8. **Review-and-Fix Pass (default-on)**: After the simplify pass — a full run is **implement → simplify → review** — the same adversarial-review machinery that `--verify-adversarial` uses runs read-only over the produced diff and yields structured findings. When it finds something, a fresh auto-mode session resolves each finding and folds the fix into the commit it belongs to (`git commit --fixup` + `git rebase --autosquash`), then force-pushes the fixed branch to the PR's own head with `git push --force-with-lease` (never plain `--force`, never the base branch) — the same mechanism the simplify pass and the `fix` command use. Unlike simplify, this pass is allowed and expected to add regression tests. The report is posted as a PR comment (best-effort — a failed post never aborts the run), and a `STATUS: BLOCKED` / `NEEDS_CONTEXT` report stops the pass without retrying. When the review finds nothing it is a clean no-op: no commit, no force-push, no PR comment beyond a short "review found nothing" note on stdout. The whole pass is non-fatal — a failed or escalated review never changes the run's exit code, which reflects only the implement session. The read-only `--verify` / `--verify-adversarial` flags remain available for a report-only run. Disable the apply behavior with `--no-review`.
9. **Verification (optional)**: Two independent passes run over the actual committed diff, not the implementer's self-report. With `--verify`, a session diffs the feature branch against the issue's Acceptance Criteria. With `--verify-adversarial`, a red-team pass — the same adversarial-review machinery as `review --thorough` — hunts for the bugs the change introduces (injection, race conditions, failure modes). The two flags are independent: enable either, both, or neither. Both are non-fatal — a finding is reported, it does not fail the run.

Prompt escape hatches mirror the fix subcommand: `--print-plan-prompt` renders
the planning prompt, `--print-prompt` the implement prompt (without a plan), and
`--print-bare-prompt` a portable, self-contained variant for manual sessions.
