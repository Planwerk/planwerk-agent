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

# Run an independent verification pass against the Acceptance Criteria
planwerk-review implement --verify owner/repo#123
```

See the [CLI reference](/reference/cli#implement) for every flag, including the
`--print-prompt` / `--print-plan-prompt` / `--print-bare-prompt` escape hatches
and the `--plan-model` / `--plan-effort` planning-session overrides.

## How it works

1. **Issue Input**: A GitHub issue reference (URL or `owner/repo#number`), typically already elaborated via `elaborate`.
2. **Fetch Issue**: Title, body, URL, and state are fetched via `gh issue view`.
3. **Clone**: The repository is cloned into a temp directory (or the current checkout is used with `--local`).
4. **Pattern Load**: The same pattern catalog used by `review` / `audit` / `elaborate` is loaded, filtered by detected technologies.
5. **Planning Session**: A read-only Claude Code session on the dedicated planning model (`--plan-model`, default `fable`, env: `PLANWERK_PLAN_MODEL`) at the dedicated planning effort (`--plan-effort`, default `max`, env: `PLANWERK_PLAN_EFFORT`) grounds the issue in the actual code — verifying every cited file and symbol — and emits a structured implementation plan: change set, commit sequence, test plan, documentation plan, verification commands, risks. The plan steers the entire implementation, so it gets the strongest model at the largest thinking budget. When the plan is ready it is posted back onto the source issue as a comment (disable with `--no-plan-comment`), so the brief that drives the implementation is recorded on the issue itself; an escalated plan is posted too, so the human who must clarify a `STATUS: BLOCKED` / `NEEDS_CONTEXT` issue sees it there. A plan that reports `STATUS: BLOCKED` or `NEEDS_CONTEXT` aborts the run before any code is written. Skip the phase entirely with `--no-plan`.
6. **Implement Session**: A fresh Claude Code session in auto mode (`--permission-mode auto`) receives the plan embedded in its prompt and executes it end-to-end: code, tests, documentation, small reviewable commits on a fresh feature branch, draft pull request linked to the issue. The implement session uses the global `--claude-model` (default `opus`) and `--claude-effort` (default `xhigh`); only the planning session runs on the dedicated `--plan-model` / `--plan-effort`.
7. **Verification (optional)**: With `--verify`, an independent session diffs the feature branch against the issue's Acceptance Criteria without trusting the implementer's report.

Prompt escape hatches mirror the fix subcommand: `--print-plan-prompt` renders
the planning prompt, `--print-prompt` the implement prompt (without a plan), and
`--print-bare-prompt` a portable, self-contained variant for manual sessions.
