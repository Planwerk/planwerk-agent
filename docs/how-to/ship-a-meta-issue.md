# Ship a Meta Issue

Drive every Sub Issue of a **Meta Issue** — the kind
[`/planwerk:meta`](/how-to/split-a-meta-issue) produces — to merged on the default branch,
in dependency order, without a human in the loop. Where
[`implement`](/how-to/implement-an-issue) is supervised and deliberately stops at
a draft pull request, `ship` is the unattended fleet driver: for each Sub Issue it
runs the full `implement` pipeline, marks the opened PR ready, waits for CI, fixes
red CI itself, and merges when green — then advances to the next ready Sub Issue,
repeating until the Meta Issue is delivered or no further progress is possible.

`ship` is for work you are confident enough to delegate end to end. It does not
prompt, does not wait for review, and treats failing CI as its own problem to fix
rather than a hand-off. If you want to keep a human in the loop, run `implement`
per Sub Issue instead.

```bash
# Drive every Sub Issue to merged, in dependency order
planwerk-agent ship owner/repo#123

# Report the planned order without cloning or calling Claude
planwerk-agent ship --dry-run owner/repo#123

# Run the whole pipeline but stop at green CI, leaving merges to a human
planwerk-agent ship --no-merge owner/repo#123

# Merge with squash instead of the default rebase
planwerk-agent ship --merge-method squash owner/repo#123

# Resume from a specific Sub Issue
planwerk-agent ship --start-at 456 owner/repo#123
```

## Preview first

Run `--dry-run` before the real thing. It fetches the Meta Issue, its Sub Issues,
and their native "blocked by" dependencies, and prints the topological order each
Sub Issue would be shipped in — without cloning, calling Claude, or merging
anything. When the order looks right, drop the flag.

## The per–Sub Issue pipeline

For each eligible Sub Issue, `ship` runs:

1. **Implement** — the existing `implement` flow (plan → implement → simplify →
   review → finalize), producing a feature branch cut from the *current*
   default-branch tip and a PR that closes the Sub Issue. The automatic simplify
   and review passes run as in `implement` and honor the same `--no-simplify` /
   `--no-review` switches, so the diff is cleaned and self-reviewed before CI ever
   sees it.
2. **Mark the PR ready** — undraft it so its checks are the real merge gate.
3. **Wait for CI** — reusing the `fix` loop's polling (`--interval`).
4. **Self-heal red CI** — reusing the `fix` loop: pull the failed-run logs, apply
   a minimal fix, fold it via fixup/autosquash, force-push, and re-wait, up to
   `--max-fix-iterations`. No human is asked.
5. **Rebase-merge when green** — once the checks pass and the PR is mergeable,
   `ship` merges it (rebase by default, configurable with `--merge-method`).
6. **Advance** to the next ready Sub Issue and repeat.

## Dependency order

`ship` processes Sub Issues in the order their dependencies allow. `/planwerk:meta` records
each Sub Issue's "blocked by" ordering as a native GitHub relationship; `ship`
reads those back and works them topologically, so a Sub Issue becomes eligible
only once every Sub Issue it is blocked by has merged. Sub Issues with no real
dependency on each other stay independently shippable. Processing is sequential —
one merge at a time — so each branch is cut after the prior merges and sees them.

## When a Sub Issue cannot be shipped

If a Sub Issue cannot be finished autonomously — `implement` reports `BLOCKED` /
`NEEDS_CONTEXT`, CI stays red past `--max-fix-iterations`, or the PR will not
merge — `ship` does not abort the whole run. It **skips that Sub Issue and
everything transitively blocked by it**, then continues with any remaining Sub
Issue whose blockers have all merged. The failed Sub Issue's PR is left open with
its report attached for a human to pick up. The run ends when no eligible Sub
Issue remains.

## Resuming an interrupted run

Because state lives in GitHub — closed Sub Issues, merged PRs — a re-run is
naturally resumable. `ship` recognizes Sub Issues that have already merged and
skips straight past them, so an interrupted run can simply be invoked again to
continue. `--start-at <number>` resumes from a chosen Sub Issue, treating the ones
ordered before it as already-handled unless they are still open.

## Autonomy and merge safety

`ship` merges to the default branch unattended, so it honors branch protection: it
refuses to merge (skipping the Sub Issue) when a required check or review would
block, or when the PR has a conflict, and **never force-merges past a protection
rule**. `--no-merge` is the escape hatch from full autonomy — it runs the whole
pipeline but stops at green CI for every Sub Issue, leaving the merges to a human.

`ship` narrates its progress on the Meta Issue: a comment as each Sub Issue is
picked up and merged or skipped, plus a final summary listing what merged, what
was skipped, and why. When every Sub Issue has merged, the Meta Issue is closed.

## What it does not do

`ship` does **not** create Sub Issues — that stays the job of `/planwerk:meta`. It composes
`implement`, `fix`, and `/planwerk:meta`'s output; it does not replace them, and the
single-issue commands are untouched. See the [CLI reference](/reference/cli#ship)
for every flag.
