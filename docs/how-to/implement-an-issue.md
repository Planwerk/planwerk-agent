# Implement an issue

Use `implement` to take an elaborated GitHub issue and drive it to a draft pull
request: a read-only planning session grounds the issue in the code, an
implement session executes the plan end to end (code, tests, documentation,
commits on a feature branch), the simplify and review passes clean up the diff,
and a finalize session opens the PR last — so it lands already simplified and
self-reviewed.

```bash
# Plan and implement an elaborated issue
planwerk-agent implement owner/repo#123

# Skip the planning session and implement directly
planwerk-agent implement --no-plan owner/repo#123

# Force a fresh plan instead of reusing one already posted on the issue
planwerk-agent implement --no-plan-reuse owner/repo#123

# Run an independent verification pass against the Acceptance Criteria
planwerk-agent implement --verify owner/repo#123

# Red-team the produced diff for introduced bugs (composes with --verify)
planwerk-agent implement --verify-adversarial owner/repo#123

# Skip the automatic simplify pass (on by default)
planwerk-agent implement --no-simplify owner/repo#123

# Skip the automatic review-and-fix pass (on by default)
planwerk-agent implement --no-review owner/repo#123

# Keep the review-and-fix pass but skip its first-round specialist fan-out
planwerk-agent implement --no-specialists owner/repo#123

# Propose new wiki patterns and memory pages from the run (needs --wiki)
planwerk-agent implement --wiki owner/repo#123

# Skip the read-only capture pass (on by default with --wiki)
planwerk-agent implement --wiki --no-capture owner/repo#123

# Push the accepted capture pages to the wiki (opt-in; confirms first)
planwerk-agent implement --wiki --capture-wiki owner/repo#123

# ... non-interactively (CI), skipping the write confirmation
planwerk-agent implement --wiki --capture-wiki --yes owner/repo#123
```

See the [CLI reference](/reference/cli#implement) for every flag, including the
`--print-prompt` / `--print-plan-prompt` / `--print-bare-prompt` escape hatches,
the `--plan-model` / `--plan-effort` planning-session overrides, and the
`--implement-model` override for the implement session itself.

### Orchestrator mode: a strong model oversees, worker subagents write the code

`--implement-worker-model` (env: `PLANWERK_IMPLEMENT_WORKER_MODEL`) splits the
implement session into an orchestrator and workers, inside one Claude Code
session:

```sh
# Fable keeps the whole issue in view; Opus subagents implement the packages
planwerk-agent implement \
  --implement-model fable \
  --implement-worker-model opus \
  owner/repo#123
```

The session runs on `--implement-model` as the **orchestrator**: it never edits
a file itself. Instead it delegates every work package — in the plan's
commit-sequence order, one at a time — to an `implementer` subagent running on
the worker model at `--implement-worker-effort` (default `xhigh`). Each
delegation brief is self-contained (the worker shares the checkout but not the
orchestrator's context), and after each worker returns the orchestrator
verifies the delivered package against the actual diff — reading the commits,
running the tests in the foreground, checking the package's Acceptance
Criteria — and dispatches follow-up delegations for every gap before moving
on. The Implementation Report and its terminal `STATUS` line stay with the
orchestrator, so resume, the PARTIAL/BLOCKED guards, and the surrounding
simplify/review/finalize passes behave exactly as in a single-session run.

The subagent is defined inline via Claude Code's `--agents` flag, so the target
checkout stays untouched and the session stays hermetic; the workers inherit
auto mode, so the permission classifier keeps vetting their actions.
Attribution follows the code, not the reviewer: the report footer names the
worker model, and the workers' commits carry their exact model id in the
`Assisted-by` trailer. Pass an exact model id (e.g.
`--implement-worker-model claude-opus-4-8`) when you want the footer to name
it precisely. Without `--implement-worker-model` the implement session writes
the code itself, exactly as before. `ship` accepts the same two flags for its
per–Sub Issue implement runs.

## How it works

1. **Issue Input**: A GitHub issue reference (URL or `owner/repo#number`), typically already elaborated via `elaborate`.
2. **Fetch Issue**: Title, body, URL, and state are fetched via `gh issue view`.
3. **Clone**: The repository is cloned into a temp directory (or the current checkout is used with `--local`).
4. **Pattern Load**: The same pattern catalog used by `review` / `audit` / `elaborate` is loaded, filtered by detected technologies.
5. **Planning Session**: A read-only Claude Code session on the dedicated planning model (`--plan-model`, default `fable`, env: `PLANWERK_PLAN_MODEL`) at the dedicated planning effort (`--plan-effort`, default `max`, env: `PLANWERK_PLAN_EFFORT`) grounds the issue in the actual code — verifying every cited file and symbol — and emits a structured implementation plan: user stories (extracted from the issue's User Stories section, or derived when absent, and omitted for purely mechanical work), change set, commit sequence, test plan, documentation plan, verification commands, risks. When the issue is a **Sub Issue** of a Meta Issue, the Meta Issue and the sibling Sub Issues are fetched (best-effort, via the GitHub GraphQL API) and injected into the planning prompt, so the plan covers only this issue's slice of the larger effort, honors the Meta Issue's framing, and defers a shared task's remaining part to the sibling that owns it with an explicit `#K` cross-reference. This context flows into the plan; the implement session itself then works from that plan. `--print-plan-prompt` renders the planning prompt with this context included. The plan steers the entire implementation, so it gets the strongest model at the largest thinking budget. When the plan is ready it is posted back onto the source issue as a comment (disable with `--no-plan-comment`), so the brief that drives the implementation is recorded on the issue itself; an escalated plan is posted too, so the human who must clarify a `STATUS: BLOCKED` / `NEEDS_CONTEXT` issue sees it there (the [`/planwerk:clarify`](/how-to/clarify-an-issue) skill answers a `NEEDS_CONTEXT` plan's open questions and records them in the issue body). A plan that reports `STATUS: BLOCKED` or `NEEDS_CONTEXT` aborts the run before any code is written. Skip the phase entirely with `--no-plan`. If planwerk-agent already posted a plan on the issue on an earlier run — identified by its `## Implementation Plan` heading and attribution footer — that plan is reused verbatim instead of running the session again (the footer is stripped, no duplicate comment is posted, and the reused plan is still subject to the same `STATUS` abort); pass `--no-plan-reuse` to override this and plan afresh when the issue has changed since. Reading the issue's comments to find a reusable plan is load-bearing: if that lookup fails the run aborts rather than silently paying for a fresh planning pass.
6. **Implement Session**: A fresh Claude Code session in auto mode (`--permission-mode auto`) receives the plan embedded in its prompt and executes it end-to-end: code, tests, documentation, small reviewable commits on a fresh feature branch. It does **not** open a pull request — the simplify and review passes run over the committed diff first, and the finalize session opens the PR last. The implement session uses the global `--claude-model` (default `opus`) and `--claude-effort` (default `xhigh`); `--implement-model` (env: `PLANWERK_IMPLEMENT_MODEL`) overrides the model for just this one session, while the planning session runs on the dedicated `--plan-model` / `--plan-effort` and the simplify/review/finalize passes always stay on `--claude-model`. Once the session finishes, its implementation report is posted back onto the source issue as a comment (disable with `--no-report-comment`), just like the plan in step 5 — on every run, including ones where nothing was implemented or the attempt failed, so the course of the implementation is recorded on the issue itself. The run guards on a complete report: because the session is one-shot and headless, a session that yields mid-work (for example, backgrounding its tests to be "notified" later, or deferring a commit to a turn that never comes) returns output with no `## Implementation Report` heading and no terminal `STATUS` line. That output is **not** posted as a report and the run aborts before the simplify/review/finalize passes, so no pull request is opened on a half-built branch. A report whose status is `BLOCKED` or `NEEDS_CONTEXT` is a complete report — it is posted so the human who must intervene sees it, and then the run stops before opening a PR. When the target repository ships **Agent Skills** under `.claude/skills/`, they are discovered from the checkout and listed in the implement prompt (as they are for the `fix` and `address` commands), so the session invokes a project-provided skill for a specialized task instead of improvising — only the repository's own committed skills are surfaced, never the operator's user-global ones.
7. **Simplify Pass (default-on)**: Once the branch is committed, a read-only ponytail-style finder reviews the produced diff through a minimalist decision ladder (prefer not building it (YAGNI) → the standard library → a platform/framework-native feature → an already-present dependency → a one-liner → only then the minimum new code) and emits a delete/collapse list of over-engineering. When it finds something, a fresh auto-mode session applies the simplifications and folds each into the commit it belongs to (`git commit --fixup` + `git rebase --autosquash`) on the local branch — no push, since no PR exists yet. It runs **before** the review-and-fix pass, so that pass and the verification passes assess the smaller, leaner diff. A hard guardrail keeps it from ever removing validation, error handling, security, or accessibility code, or deleting/weakening tests or assertions; any finding that touches a test or assertion file is dropped before the apply step. The report is posted as a comment on the source issue (best-effort — a failed post never aborts the run), and a `STATUS: BLOCKED` / `NEEDS_CONTEXT` report stops the pass without retrying. When there is nothing to simplify it is a clean no-op: no commit, no issue comment. The whole pass is non-fatal. Disable it with `--no-simplify`.
8. **Review-and-Fix Pass (default-on)**: After the simplify pass — a full run is **implement → simplify → review → finalize** — this pass runs the same finders and the same finding hygiene the [`review`](/how-to/review-a-pr) command runs (package `internal/hygiene`), then folds each surviving fix into the commit it belongs to (`git commit --fixup` + `git rebase --autosquash`) on the local branch — no push, since no PR exists yet. Unlike simplify, this pass is allowed and expected to add regression tests.
   - **First-round fan-out**: the first round runs the adversarial finder plus the domain-specialist fan-out — the same six specialists as `review --specialists` (security, data-migration, testing, performance, api-contract, maintainability) — concurrently, adaptively gated by the files the branch changed and grounded in the same review-pattern catalog a later review would apply. The fan-out is on by default because `implement` runs unattended with nobody present to opt in; later rounds re-check the applied fixes with the cheaper adversarial finder alone, bounding the fan-out to the first round. Turn it off with `--no-specialists` (the adversarial finder still runs).
   - **Shared hygiene**: the merged findings pass through the same hygiene stage the reporting path runs — the multi-pass merge (with its confidence boost and cross-pass provenance), the file-less dedup fallback, the quote-or-demote snippet gate, and claim verification. Withheld findings are reported with the gate's own recorded reason (a refuted claim's note, or the snippet gate's demotion reason) rather than a guessed one.
   - **Survive-before-apply gate**: only findings that **survive** hygiene are handed to the editing session. A finding whose quoted code cannot be found in the changed files, or whose claim a verifier refuted, is reported — on stdout and in the review comment on the source issue — but **never applied**. The harness enforces this; the editing session is not merely asked to skip false positives.
   - It runs as a **bounded loop**: after each apply it re-reviews the branch it just changed and, while the finder still reports findings that survive hygiene, fixes them again — stopping when the finder comes back clean, when a round yields no survivors, when an apply escalates (`STATUS: BLOCKED` / `NEEDS_CONTEXT`), or after `--max-review-iterations` rounds (default 3), in which case it notes the findings still unresolved when the budget ran out. Each round's report is posted as a comment on the source issue (best-effort — a failed post never aborts the run). When the review finds nothing on the first round it is a clean no-op: no commit, no issue comment beyond a short "review found nothing" note on stdout. The whole pass is non-fatal — a failed or escalated review never changes the run's exit code. The read-only `--verify` / `--verify-adversarial` flags remain available for a report-only run. Disable the whole pass with `--no-review`, or just the specialist fan-out with `--no-specialists`.
9. **Capture Pass (default-on, needs `--wiki`)**: After the review pass and before finalizing, a read-only pass proposes new project knowledge for the wiki — generalizable review findings become candidate `review_patterns/` pages, durable rationale from the plan and the implementation report becomes candidate `memory/` pages, and every candidate is deduplicated against the wiki's existing entries and the bundled pattern catalog. It is **propose-only**: the suggestions surface in the run report and as a comment on the source issue, and nothing is written to the wiki. The pass reuses the harness-read-only runner, so Claude authors the candidate page bodies but cannot push them. It runs only when a wiki is resolved (`--wiki`); it is a clean no-op when nothing clears the bar, and the whole pass is non-fatal. Disable it with `--no-capture`. By default the pass is **propose-only** and writes nothing; pass `--capture-wiki` to turn the accepted pages into real wiki growth — a separate, mechanical write phase clones the wiki fresh, writes each page (provenance marker included) under the pinned tool identity, and pushes (creating the wiki's first commit when it is uninitialized). Claude never pushes: it authored the bytes in the read-only proposal pass, and this phase performs the push. The write confirms interactively and refuses a non-TTY run without `--yes`; like the surrounding passes it is non-fatal, so a refusal or push failure degrades back to propose-only. The gate is also settable via `PLANWERK_CAPTURE_WIKI` or a `capture.wiki` config key. See [Use the GitHub Wiki](/how-to/use-the-github-wiki#capture-knowledge-from-a-findings-producing-run-propose-only) for the memory write convention (one page per durable decision, a stable slug, a provenance marker).
10. **Verification (optional)**: Two independent passes run over the actual committed diff, not the implementer's self-report. With `--verify`, a session diffs the feature branch against the issue's Acceptance Criteria; any unmet criteria it finds are then fed into the same review applier the review-and-fix pass uses, so the gaps are fixed on the local branch before the finalize step opens the PR (that apply is non-fatal too, and a clean pass — or a run with no applier wired — stays render-only). With `--verify-adversarial`, a red-team pass — the same adversarial-review machinery as `review --thorough` — hunts for the bugs the change introduces (injection, race conditions, failure modes). The two flags are independent: enable either, both, or neither. Both are non-fatal — a finding is reported, it does not fail the run.
11. **Finalize Session**: With the simplify and review passes done, a fresh auto-mode session opens the draft pull request last, so it lands already simplified and self-reviewed. It resolves the base branch from `origin/HEAD`, pushes the feature branch, and runs `gh pr create --draft` with a description that walks the reviewer through the commits in order and links the issue with the closing keyword `Closes #N`. This is the run's deliverable, so — unlike the passes above — a genuine failure to push or open the PR is **fatal** (the branch is committed locally, and the operator is told the PR was not created). A branch that carries no commits over the base opens no PR and is not an error.

Prompt escape hatches mirror the fix subcommand: `--print-plan-prompt` renders
the planning prompt, `--print-prompt` the implement prompt (without a plan), and
`--print-bare-prompt` a portable, self-contained variant for manual sessions.
