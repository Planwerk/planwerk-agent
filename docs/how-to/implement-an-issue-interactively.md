# Implement an issue interactively

Take a prepared GitHub issue and implement it directly in the checkout you are
sitting in: a short plan built in plan mode, your approval, and one complete
pull request — without the unattended pipeline's simplify, review, and
verification passes.

```
/planwerk:implement owner/repo#123
```

Run it from inside a checkout of the issue's repository, on a clean working
tree. The skill branches off the up-to-date default branch and never commits on
it.

## Skill or command?

`implement` exists both ways, and unlike `elaborate` and `fix` the difference is
more than supervision: the command runs passes the skill deliberately omits.

| | `/planwerk:implement` | [`planwerk-agent implement`](/reference/cli#implement) |
|---|---|---|
| Runs in | Your session, in your checkout | A throw-away clone, or `--local` |
| Plans | A short plan in plan mode, approved by you | A dedicated read-only planning session, posted to the issue |
| After the code | You read the diff | Simplify, review-and-fix, and optional verification passes |
| Opens the PR | Only after you say yes | Automatically, as the run's deliverable |

Reach for the command when nobody is watching, or when the change is large
enough to deserve the passes. Reach for the skill when the change is small — a
bug fix, a contained feature — and a full pipeline run costs more than it
catches. The point of the skill is that small changes stop being implemented ad
hoc, outside every convention: the branch shape, the commit trailers, the
Acceptance Criteria walk, and the one-PR discipline all still apply.

## The plan is still the gate

The skill enters plan mode before touching anything, grounds the issue in the
actual code — every cited file and symbol verified — and presents a short plan:
the change set, the change and verification command behind every Acceptance
Criterion, the tests to write, the documentation the change makes stale.
Nothing is edited before you approve that plan.

A draft-depth issue (no Affected Areas, no Acceptance Criteria) is not silently
treated as elaborated: the skill asks whether to proceed with the plan carrying
the full weight, or to stop for [`/planwerk:elaborate`](/how-to/elaborate-an-issue)
first.

## Complete, or not at all

The plan covers the whole issue as one pull request, and the implementation
either delivers it or stops. There is no `PARTIAL`: deferring a listed piece to
a follow-up issue or a second PR is not an outcome the skill can produce, the
same discipline the command enforces (design decision 62). When the work cannot
complete, the report is `BLOCKED`, the branch stays local, and nothing is
pushed.

The passes the skill omits do not soften what "done" means. Every Acceptance
Criterion is exercised by a command the report quotes, a bug fix carries a
regression test the skill watched fail before the fix, and the diff is re-read
against `.planwerk/review_patterns/` when the repository carries them — because
no review pass will do it later.

## How the work lands

The branch is named `implement/issue-<N>-<slug>`, the same shape the command
uses, so it is unambiguously this issue's implementation. Commits are small and
reviewable, and carry `Assisted-by` above `Signed-off-by`, never
`Co-authored-by` — the doctrine in `plugins/planwerk/shared/commits.md`.

After you have seen the criteria walk, the commits, and the diff, you choose
where it lands, and the first is recommended:

- **Draft pull request** — the branch is pushed and a draft PR opened, its
  description walking the commits in order and closing the issue with
  `Closes #N`. This is what the command's finalize step produces.
- **Ready pull request** — the same, not draft.
- **Leave the branch local** — nothing is pushed.

Nothing reaches GitHub without an explicit yes, and the issue body is never
edited — the plan lives in the conversation, and the PR closes the issue when
it merges.

## Next steps

- [Fix failing checks](/how-to/fix-failing-checks) when the pushed PR's checks
  come back red.
- [Implement an issue](/how-to/implement-an-issue) with the unattended command
  when the change deserves the full pipeline.
- [Elaborate an issue](/how-to/elaborate-an-issue) when the skill reports the
  issue is still at draft depth.
