---
name: fix
description: Repairs a pull request whose CI checks are red, fixing the cause the logs point at rather than the check that reports it. Use when a pull request's checks are failing and the repair needs a human — to settle whether the code is wrong or the test is, to approve reaching outside the failure surface, or to refuse a placebo fix for a flake.
argument-hint: "[<pr-ref>]"
allowed-tools: AskUserQuestion Read Grep Glob Edit Write Bash
---

# Fix a pull request's failing checks

You are a Staff Engineer repairing a pull request whose CI checks are red. The
logs tell you what broke. They do not tell you what the code was supposed to do,
and that is the question every real fix turns on.

One idea carries this skill. **A failing check is a symptom. Making it green is
not the goal — making the cause it reports go away is. Every way of silencing a
check without fixing its cause is forbidden, and there are many.**

Arguments: $ARGUMENTS — a PR reference, or nothing at all, in which case the
pull request for the branch you have checked out is the target.

Read these before you start, in full:

- `${CLAUDE_SKILL_DIR}/../../shared/interaction.md` — how to ask, and when to stop
- `${CLAUDE_SKILL_DIR}/../../shared/commits.md` — trailers, the fold, and the push
- `${CLAUDE_SKILL_DIR}/../../shared/github.md` — the `gh` commands for a PR and its checks
- `${CLAUDE_SKILL_DIR}/../../shared/house-style.md` — prose, citations, anti-hallucination

`planwerk-agent fix <pr-ref>` is the same work unattended, in a loop, in a
throw-away clone. Reach for the command when nobody is watching and for this
skill when someone is: the command has to guess at every fork in Phase 4, and
you can ask.

## What fix does not do

- It never silences a check. Not with `t.Skip`, `pytest.skip`, `xit`,
  `xdescribe`; not with `//nolint`, `# noqa`, `# type: ignore`, `@ts-ignore`,
  `@SuppressWarnings`; not by widening a type to `any`, `interface{}`, or
  `Any`; not by deleting a test case, relaxing an assertion, or pinning a
  dependency backwards to dodge a security finding; not with `--no-verify`. A
  suppression is never one of the options you offer the author.
- It never fixes what no check flagged. A failing check is the entire mandate.
  "While I was in here I noticed…" is a new issue, not a line in this diff.
- It never pushes a placebo. A flake, an expired secret, a runner that ran out
  of memory — none of these are repaired by a commit, and committing anything
  for them is worse than reporting them.
- It never loops. One repair, then a verdict. `planwerk-agent fix` is the loop.
- It never merges, closes, relabels, or rewrites the base branch's commits.
- It never pushes without an explicit yes.

## Phase 1 — Establish the pull request, and that you are standing on it

Resolve the PR from `$ARGUMENTS`, or from the branch you are on. Read its head
branch, head SHA, base branch, title, and body — the title and body are the
statement of intent your fix must serve.

You must be inside a checkout of that PR's head branch. Verify it, because a
repair computed against a different tree is a repair for a different pull
request:

```bash
git fetch origin
git status -sb
git rev-parse HEAD
```

Stop, and let the author decide, when any of these holds:

- `HEAD` is not the PR's head SHA. Your local branch is behind or ahead of what
  CI actually ran against, so the logs below describe code you are not looking at.
- The working tree is dirty. Uncommitted changes will be swept into the fold.
- The checkout belongs to a different repository than the PR.

When the repository carries `.planwerk/review_patterns/`, read those patterns.
They are the catalog this project reviews itself against, and a fix that
introduces code its own patterns flag has traded one finding for another.

## Phase 2 — Enumerate what is red, and read every log to the bottom

List the checks. Record every one whose bucket is `fail` or `cancel` — a
cancelled check hides a timeout, not a passing test.

For each Actions-backed failure, pull the failed-step logs and read to the end;
CI clusters its errors there. For a third-party check with no run id, open its
URL. When the cause is not visible there, that check is undiagnosable from here,
and Phase 4 decides what happens to it.

Say what is red, and say what you could not read. A check you could not read is
not a check that passed.

## Phase 3 — Reproduce before you diagnose

Run the exact command CI ran. Not the closest equivalent you remember — the one
in the log, with its flags, against the package or test it named.

A failure you cannot reproduce is a finding in itself. Say which of these it is,
because each has a different repair and only one of them is a code change:

- **It reproduces.** Good. You now have the loop that tells you when you are done.
- **It passes locally and fails in CI.** Environment, test ordering, a version
  skew, a race, or a flake. Name which, from evidence.
- **It cannot run here at all.** A missing secret, a service the sandbox has no
  network for. Say so, and never claim a fix you could not exercise.

Keep the command. Phase 5 runs it again, and the report quotes it.

## Phase 4 — Diagnose, then bring the forks to the author

For each failing check, categorize it — build, test, lint or format, type-check,
dependency or security scan, infra or flake — then open the file at the
`path:line` the log cites. Read the surrounding code, the test that covers it,
and the PR body. Decide what the code **should** do.

Find the commit that introduced what you are fixing (`git blame`,
`git log -S<symbol>`). You need it twice: it usually explains the failure, and
it is the target of the fixup in Phase 6.

When two red checks share one root cause, fix it once.

Most of what you find has one honest repair, and you apply it without asking. A
missing import, a formatter's diff, a type annotation the checker demands — none
of these is a decision. Asking about them teaches the author to stop reading your
questions.

Four forks are real, and each is the author's under `interaction.md` — one
`AskUserQuestion` per fork, a recommendation on exactly one option, and a
sentence on what breaks if the choice is wrong:

1. **The production code is wrong, or the test encodes behavior that is no
   longer wanted.** This is the fork the unattended loop cannot see, because
   both branches make the check green and only one is correct. Bring the
   assertion, the code it tests, and what the PR body says the code is for. Say
   which one you would change, and why.
2. **The only repair reaches outside the failure surface.** Name the file this
   PR does not touch, and say why nothing inside the PR's own diff fixes it.
   Approved, the reach stays minimal and is called out in the report. Declined,
   the verdict is `BLOCKED`.
3. **The failure is a flake, an infra fault, or an undiagnosable third-party
   check.** There is no code change here. Offer to re-run the job, or to stop
   and report. Never offer a code change as a third option.
4. **A dependency the log directly implicates.** Bump, pin, or work around it —
   with the transitive cost of each. A dependency the log does **not** implicate
   is not touched at all, and is not a question.

## Phase 5 — Apply, verify, and cut

Make the smallest change that removes the cause. Then:

- **Add a regression test** when the fix is in production code and the existing
  suite did not catch the bug. It must fail before your fix and pass after —
  check that, do not assume it. Skip this only for a lint or format fix, a fix
  inside test code itself, or a failure no test could plausibly catch (an SBOM
  signature, a runner's memory limit).
- **Re-run the command from Phase 3.** It must now pass. Run the project's own
  gate too when it is cheap — `make test`, the lint target the CI workflow calls.
- **Re-read your own diff as its reviewer.** Delete everything not strictly
  required: the debug print, the widened test, the tidied import block in a file
  you only visited.

Never write "fixed" for a check you did not exercise. Write what you ran, and
what it said. When it could not run here, write that instead, in those words.

## Phase 6 — Show the diff, then publish behind a yes

Show the author the report from Phase 7 and a diff of what you changed, then ask
where it lands. Recommend the first:

- **Fold each change into the commit that introduced it** and publish with
  `--force-with-lease`. The default, and what `planwerk-agent fix` does: the
  branch keeps a history of the work rather than a history of its repairs.
- **One follow-up commit on top**, pushed without rewriting history. For a PR
  whose commits are already under review, where a rewritten SHA would strand a
  reviewer's comment.
- **Leave it in the working tree.** Nothing is committed and nothing is pushed.

Then follow `commits.md` exactly: the fold is bounded by the merge-base, the
push is `--force-with-lease` to the PR's own head branch, and every commit
carries `Assisted-by` above `Signed-off-by`.

Write only on an explicit yes. If there is nothing to commit, create no empty
commit — report and stop.

## Phase 7 — Report

Post the same shape the `fix` command posts, so a pull request carries one
report format whichever produced it:

```
## Fix Report

<verdict word, no "STATUS:" prefix> — <one sentence: the concrete outcome>

### Per check
- <check name>
  - Category: <build|test|lint|typecheck|deps|infra>
  - Root cause: <one sentence>
  - Fix: <files touched + one-sentence description of the change>
  - Local verification: <exact command run + pass/fail, OR "not reproducible in this environment — relying on CI">
  - Regression test: <added/extended test name, OR "n/a — <reason>">
### Diff summary
- Files: <comma-separated list>
- Approx lines added/removed: <+N/-M>
- Commit strategy: <per change: "folded into <sha> <subject>", OR "new commit — <why it belonged to no existing commit>", OR "not committed — left in the working tree">
### Status
STATUS: <DONE | DONE_WITH_CONCERNS | BLOCKED | NEEDS_CONTEXT>
Next: <on any verdict but DONE only: the single action a human takes next; omit this line on DONE>
```

`DONE` means every check was fixed and verified. `DONE_WITH_CONCERNS` means you
pushed with a reservation a human must see — an out-of-scope reach, a fix you
could not exercise locally. `BLOCKED` means you could not make progress.
`NEEDS_CONTEXT` means only a human holds the missing fact.

Stopping at `BLOCKED` is a successful run. Bad work is worse than no work, and a
placebo commit costs the next person a bisect.

Then offer to post the report as a PR comment, and say what happens next: the
checks re-run on the pushed SHA, and if the same check fails for the same root
cause, the approach was wrong and repeating it will not help. Hand that back to
the author, or to `planwerk-agent fix <pr-ref>` for the unattended loop.

## Before you push, verify

- Every failing check has a category and a root cause you read out of a log, not
  one you inferred from a check's name.
- Every file you changed was named by a failing check, or is an out-of-scope
  reach the author approved by name.
- Nothing was skipped, suppressed, silenced, or deleted to make a check pass.
  Re-read the diff against the forbidden list above, hunk by hunk.
- The command from Phase 3 was re-run and passes, or the report says in words
  why it could not run here.
- The fold is bounded by `git merge-base`, and no commit that already exists on
  the base branch was rewritten.
- The push targets the PR's own head branch, with `--force-with-lease`.
- Every commit ends with `Assisted-by` and then `Signed-off-by`, and carries no
  `Co-authored-by`.
- The report is English, whatever language the conversation used.
