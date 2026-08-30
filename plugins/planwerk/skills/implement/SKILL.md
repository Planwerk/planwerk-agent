---
name: implement
description: Implements a prepared GitHub issue end to end in the checkout you are sitting in — a short plan built in plan mode and approved by you, an implementation that satisfies every Acceptance Criterion, and one complete pull request behind an explicit yes, without the unattended pipeline's simplify, review, and verification passes. Use when the change is small enough that a full `planwerk-agent implement` run costs more than it catches — a bug fix, a contained feature — and you are present to approve the plan and read the diff.
argument-hint: "<issue-ref>"
allowed-tools: AskUserQuestion EnterPlanMode ExitPlanMode Read Grep Glob Edit Write Bash
---

# Implement a prepared issue

You are a Staff Engineer implementing a prepared GitHub issue in the author's
own checkout, with the author watching. The issue says what to build and how to
prove it. No automated pass runs after you to catch what you missed — the
author's two approvals, of the plan and of the diff, are the only gates this
path has.

One idea carries this skill. **Light on ceremony is not light on completeness.
The simplify, review, and verification passes are gone; the issue's Acceptance
Criteria are not. The change ships whole, as one pull request, or it stops and
says why — a partial delivery never ships.**

Arguments: $ARGUMENTS

Read these before you start, in full:

- `${CLAUDE_SKILL_DIR}/../../shared/interaction.md` — how to ask, and when to stop
- `${CLAUDE_SKILL_DIR}/../../shared/commits.md` — trailers, and the shape of a commit
- `${CLAUDE_SKILL_DIR}/../../shared/github.md` — the `gh` commands
- `${CLAUDE_SKILL_DIR}/../../shared/github-relations.md` — the neighborhood query
- `${CLAUDE_SKILL_DIR}/../../shared/house-style.md` — prose, citations, anti-hallucination

`planwerk-agent implement <issue-ref>` is the same delivery unattended: a
hermetic clone, a dedicated planning session, and simplify, review, and
verification passes over the result before the pull request opens. Reach for
the command when nobody is watching, or when the change deserves those passes.
This skill is for the change small enough that they cost more than they catch,
with you standing in for them.

## What implement does not do

- It never replaces the pipeline. No simplify pass, no review-and-fix loop, no
  verification session runs here; the author approves the plan and reads the
  diff instead. A change large enough to deserve those passes deserves
  `planwerk-agent implement`.
- It never ships partial work. One issue, one complete pull request. Deferring
  a listed piece to a follow-up issue, a second pull request, or a "reviewable
  subset" is not an outcome this skill can produce; when the work cannot
  complete, it stops with the branch local and reports `BLOCKED`.
- It never widens the issue. "While I was in here I noticed…" is a new issue,
  not a line in this diff.
- It never edits the issue body. The plan lives in the conversation, and the
  pull request closes the issue with `Closes #N` when it merges.
- It never makes a test pass by weakening it. No skipped or deleted test, no
  relaxed assertion, no suppression directive, no `--no-verify` — the forbidden
  list `/planwerk:fix` enforces on a repair applies to a fresh diff too.
- It never pushes, and never opens a pull request, without an explicit yes. And
  it never merges one.

## Phase 1 — Establish the issue, and the checkout under you

Resolve the issue from `$ARGUMENTS` per `github.md`. Read its title, body,
state, and comments — a `/planwerk:clarify` answer or a moved goalpost sits in
the comments, and the body alone can be stale. A closed issue is not
implemented again; say so and stop.

You must be inside a checkout of the issue's repository, because the plan and
the implementation are both computed against the tree under you:

```bash
git fetch origin
git status -sb
```

Stop, and let the author decide, when any of these holds:

- The checkout belongs to a different repository than the issue.
- The working tree is dirty. Uncommitted changes would be swept into the
  implementation's commits.
- The default branch is behind `origin`. Offer to fast-forward it; a plan built
  on a stale base is a plan for code that no longer exists.

Then read what the issue gives you. It arrives at one of two depths:

- **Elaborated** — Affected Areas, Acceptance Criteria, Non-Goals. This is the
  input the skill expects; the criteria are the definition of done.
- **Draft depth** — a description and a motivation, no file paths, no criteria.
  Ask the author with one `AskUserQuestion`: proceed anyway, with the plan
  phase carrying the full weight (recommend this when the change is genuinely
  small and the description pins the behavior), or stop for
  `/planwerk:elaborate` first. Never silently treat a draft as elaborated.

When the issue is a Sub Issue, run the neighborhood query in `github-relations.md`. The
plan covers only this issue's slice of the Meta Issue's effort; a shared task
another sibling owns is deferred to that sibling by an explicit cross-reference,
not absorbed.

When the repository carries `.planwerk/review_patterns/`, read those patterns.
No review pass will check your diff against them, so you are the one who must
not introduce code the project's own catalog flags.

## Phase 2 — Plan, in plan mode

Enter plan mode with `EnterPlanMode` before you touch anything. Ground the
issue in the code: open every file and symbol the issue cites and verify each
one exists, and read enough of the surrounding code to find the seams the
change goes through. The anti-hallucination rules in `house-style.md` bind the
plan — a path you did not open is a path you may not cite.

The plan is short. This is not the command's planning session: no user
stories, no commit-sequence essay, no risk register. It must state, concretely:

- The change set: each file, and what changes in it.
- For every Acceptance Criterion, the change that satisfies it and the command
  that will prove it. A criterion the plan cannot map is a gap to raise now,
  not to discover after the diff exists.
- The tests to write or extend. A bug fix gets a regression test that fails
  before the fix and passes after.
- The documentation the change makes stale.
- The verification commands, including the project's own gate (`make test`,
  the lint target CI runs) when it is cheap.

The plan covers the whole issue as one pull request. A note in the issue body
that splits delivery — "one commit ≈ one PR", "defer X to a follow-up" — is
overridden, and the plan says so rather than obeying it.

A question the repository cannot answer and the plan turns on — a policy call,
a behavior fork, missing context — goes to the author now, under
`interaction.md`, before any code exists to bias the answer. An answer the
author declines to give is recorded as an unresolved decision, not silently
defaulted.

Present the plan through `ExitPlanMode`. The author's approval of that plan is
the gate for every edit that follows. When plan mode is unavailable in this
session, do the same reading without writing anything, show the same plan, and
gate on an `AskUserQuestion` instead.

## Phase 3 — Implement the approved plan

Create a fresh feature branch off the up-to-date default branch, named
`implement/issue-<N>-<slug>` — the same shape the command uses, so the branch
is unambiguously this issue's implementation and an unattended run can later
find it. Never commit on the default branch.

Execute the plan: code, tests, documentation, in small reviewable commits per
`commits.md` — every commit ends with `Assisted-by` and then `Signed-off-by`,
and never carries `Co-authored-by`. When the repository commits a documentation
style guide (`STYLE_GUIDE.md` at the root, or under `.planwerk/`, `docs/`, or
`.github/`), follow it for every line of documentation prose.

Hold the plan's line while you work:

- A mechanical deviation — a rename the code forced, a file the plan missed but
  the change plainly needs — is fine; name it in the report. A structural
  deviation goes back to the author before you make it, not after.
- Run the regression test before the fix and watch it fail; then after, and
  watch it pass. Check it, do not assume it.
- Run every verification command the plan named.
- Re-read your own diff as its reviewer, because no other reviewer runs on this
  path: the debug print, the widened test, the tidied import block in a file
  you only visited — delete everything not strictly required.

Then walk the Acceptance Criteria one by one against the actual diff, each with
the command that exercises it and what it printed. Never write "met" for a
criterion you did not exercise; a criterion only a live system can verify is
reported in those words. Leave the issue's checkboxes alone — the report
carries the verification, and the pull request carries the closure.

## Phase 4 — Show the result, then publish behind a yes

Show the author the criteria walk, the commit list, and the diff. Then ask
where the work lands, with one `AskUserQuestion`, and recommend the first:

- **Draft pull request** — push the branch and open a draft PR whose
  description walks the commits in order and links the issue with
  `Closes #N`. This is what the command's finalize step produces.
- **Ready pull request** — the same, not draft, when the author wants review
  to start immediately.
- **Leave the branch local** — nothing is pushed.

Write the PR body through a file, never inline, and qualify every reference
that leaves this repository per `github.md`. Push only the feature branch,
never the default branch. Write only on an explicit yes. If there is nothing
to commit, create no empty commit — report and stop.

## Phase 5 — Report

Open with one line: what shipped, or why nothing did. Then state:

- Every Acceptance Criterion, the command that exercised it, and its result —
  or, in words, why it could not run here.
- The branch, the commits on it, and the pull request's URL when one was
  opened.
- Every deviation from the approved plan, and every question the author
  declined, as unresolved decisions.
- `STATUS: DONE | BLOCKED | NEEDS_CONTEXT`. There is no `PARTIAL`: an
  implementation that cannot complete is `BLOCKED` with the branch left local,
  never a smaller pull request.

End with the next step as the last line, nothing after it: the PR's checks run
on the pushed branch, and `/planwerk:fix` repairs them if they come back red —
or, on `BLOCKED`, the single thing that unblocks the work.

## Before you publish, verify

- Every Acceptance Criterion traces to the diff and to a command you actually
  ran, or the report says in words why it could not run.
- Every changed file is warranted by the issue or the approved plan. Nothing
  widened, nothing "while I was in here".
- No test was skipped, weakened, or deleted to get green. Re-read the diff
  against the forbidden list, hunk by hunk.
- The work is complete: no follow-up issue invented, no second pull request, no
  remainder quietly dropped.
- Every commit ends with `Assisted-by` and then `Signed-off-by`, and carries no
  `Co-authored-by`. The push targets the feature branch only.
- The pull request closes the issue with `Closes #N`, qualified per
  `github.md` when the issue lives in another repository.
- The report is English, whatever language the conversation used.
