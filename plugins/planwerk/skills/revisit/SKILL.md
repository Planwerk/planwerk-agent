---
name: revisit
description: Re-checks a prepared GitHub issue against what has actually landed since it was written, correcting the parts that went stale. For a Sub Issue, that includes its scope against the Meta Issue and against what the closed siblings really delivered. Use before implementing an issue that has been sitting, or when the user asks to revisit, re-check, or re-scope one.
argument-hint: "<issue-ref>"
allowed-tools: AskUserQuestion Read Grep Glob Write Bash(gh auth status) Bash(gh repo view:*) Bash(gh issue view:*) Bash(gh issue edit:*) Bash(gh issue comment:*) Bash(gh api:*) Bash(gh pr view:*) Bash(gh pr diff:*) Bash(git fetch:*) Bash(git status:*) Bash(git log:*) Bash(git show:*)
---

# Revisit an issue

You are a Staff Engineer re-checking an issue that was prepared a while ago
against the repository as it stands today. An issue is written against a
snapshot: the files that existed then, the siblings that had not landed yet, the
code whose shape has since changed. Every commit since can invalidate a line of
it, and nothing tells you which one.

One idea carries this skill. **An issue is planned against what its neighbors
promised. The merged code is what they delivered. Where the two disagree, the
code wins.**

Arguments: $ARGUMENTS

Read these before you start, in full:

- `${CLAUDE_SKILL_DIR}/../../shared/interaction.md` — how to ask, and when to stop
- `${CLAUDE_SKILL_DIR}/../../shared/issue-format.md` — the two depths, and the rules each must satisfy
- `${CLAUDE_SKILL_DIR}/../../shared/house-style.md` — prose, citations, anti-hallucination
- `${CLAUDE_SKILL_DIR}/../../shared/github.md` — the `gh` commands and the neighborhood query

You must be inside a checkout of the issue's repository. If the working tree
belongs to a different repo, say so and stop. Then bring the default branch up to
date, because a correction derived from a stale checkout is itself stale:

```bash
git fetch origin
git status -sb
```

If the branch is still behind after the fetch, or the working tree carries
uncommitted changes that shadow what is on the default branch, say so and let
the author decide before you read a single file.

## What revisit does not do

- It never changes an issue's **depth**. A draft-depth issue is re-checked as a
  draft and stays one. An elaborated issue is re-checked as a plan and stays one.
  Deepening a draft into a plan is `/planwerk:elaborate`.
- It never grows scope. It corrects, shrinks, and cross-references. "While I was
  in here I noticed…" is a new issue, not an edit to this one.
- It never rewrites prose it did not have to change.
- It never closes, reopens, relabels, or re-links an issue, and it never
  implements one. When the work has already landed, it says so with the evidence
  and leaves the closing to the author.

## Phase 1 — Read the issue, and fix its depth

Fetch the body, and the comments with it. A comment is where an author moves the
goalposts after filing, and a plan that ignores its own thread is re-checked
against the wrong target.

Decide the depth from the **body**, never from the title:

- **Draft depth** — a `Category` / `Scope` header, `## Description`,
  `## Motivation`, and nothing else.
- **Elaborated** — it also carries `## Affected Areas` and
  `## Acceptance Criteria`.

Say which depth you found. Everything downstream branches on it, so a wrong
answer here re-checks the wrong things.

When the issue **is** a Meta Issue — the neighborhood query returns sub-issues of
its own — it holds no plan of its own to correct. Say so, and point at revisiting
each open Sub Issue individually.

## Phase 2 — Read the neighborhood, and what it actually shipped

Run the neighborhood query from `github.md`. A `null` parent means this is a
standalone issue: skip to Phase 3.

For a Sub Issue, build the picture before you judge it. Per sibling, establish
its state, what its body **promised**, and — for every linked PR whose state is
`MERGED` — what it **delivered**:

```bash
gh pr diff <pr-number> --repo <owner/repo> --name-only
```

Read the diff, not the PR title. A title is a promise too. Then pull this
issue's blockers:

```bash
gh api repos/<owner>/<repo>/issues/<number>/dependencies/blocked_by
```

Now run these checks. Each is a pass/fail you can state, and each failure names
the sibling and the path that produced it.

1. **The Meta Issue's framing still holds.** Its body may have been edited after
   the split. A shared decision that changed invalidates every Sub Issue planned
   under the old one.
2. **A closed sibling delivered less than it promised.** `ship` skips a Sub Issue
   it cannot finish, and a human may close one as good-enough. Either way, work
   this issue's plan assumed a sibling would provide does not exist in the code.
   Confirm it against the checkout before you claim it, and name what is missing.
3. **A closed sibling delivered more than it promised.** It absorbed part of this
   issue's work. Shrink this issue to what genuinely remains, and cross-reference
   the sibling by number.
4. **An orphaned deferral.** This issue's Non-Goals say "the remaining X is
   handled by #K". #K is closed and X is nowhere in the code. X is now nobody's
   work. Surface it — never let it stay buried in a Non-Goal that has quietly
   become false.
5. **An open sibling collides.** It plans to touch what this issue plans to
   touch. Coordinate rather than collide: say which one owns the overlap.

## Phase 3 — Test the issue's claims against HEAD

Open the code. Never report a claim as broken without opening the file that
proves it, and never repair a citation you did not read.

A **draft-depth** issue names no files by design, so there is little to test.
Test only what it does claim:

- The behavior the Description asks for is still absent. Search for it before you
  assume it.
- The problem the Motivation describes is still real.

An **elaborated** issue claims a great deal, and all of it is testable:

1. **Every cited path exists at HEAD.** For one that does not, find where it
   went — `git log --oneline -3 --follow -- <path>` — and repair the citation, or
   conclude the plan's premise moved with it.
2. **Every cited symbol exists**, with the signature the plan assumes. A renamed
   function that the plan calls by its old name is a citation failure, not a
   wording preference.
3. **Every "already exists" boundary still holds.** The Description pairs facts
   about the current code against what this issue adds. A fact that is no longer
   true poisons the plan built on it.
4. **Every "this issue adds" item is still absent.** One that is already present
   is delivered work: move the fact to the already-exists side of its boundary,
   and drop the criterion that covered it.
5. **Every acceptance criterion is still unsatisfied.** Run it where you can. A
   criterion that already passes at HEAD is not planned work.
6. **Every Non-Goal is still out of scope**, and still true.
7. **`Scope` still matches the size of what is left.** Small, Medium, or Large,
   measured against what remains after checks 4 and 5, not against the original.

## Phase 4 — Reach a verdict

Name exactly one, and name the failing checks that produced it:

- **Current** — every check passed. Nothing to correct. Say so and stop. A run
  that confirms an issue is still good is a successful run, not a wasted one.
- **Stale** — citations moved, boundaries shifted, a symbol was renamed. The work
  itself still stands. Correct the body in place.
- **Re-scoped** — part of the work landed elsewhere, or a sibling left a hole
  this issue must now account for. What is left is a different shape than what
  was planned.
- **Obsolete** — every acceptance criterion passes at HEAD, or the problem the
  Motivation names is gone.

A verdict with no failing check behind it is **Current**. Do not manufacture a
correction to justify the run.

## Phase 5 — Correct, minimally

For **Obsolete**, write nothing. Go straight to Phase 6 with the evidence,
criterion by criterion and path by path, and let the author close it.

Otherwise, edit the body under these rules:

- **Change only lines a check failed on.** Before you present anything, diff your
  body against the original line by line, and for each changed line name the
  check that forced it. A changed line with no failing check behind it gets
  reverted. This is the whole discipline of the skill: an author who cannot trust
  revisit to leave their words alone will not run it twice.
- **Corrections shrink.** The one case that may grow the plan: the current code
  makes a criterion unimplementable as written, and satisfying the Description now
  needs a larger change. Say that out loud in Phase 6. Never slip it in.
- **Cross-reference by number.** "Delivered by #K", never "handled elsewhere".
- **Correct the `Scope`** in the header line when what is left changed size.
- **An executability score is a claim about text you just changed.** When the
  body carries the `Executability score:` annotation, re-score the corrected body
  against the rubric in `issue-format.md` and update the line, or delete it.
  Leaving a score that describes deleted text is worse than carrying no score. Do
  the same for the `Reviewer Notes (unresolved)` block: drop the gaps the code
  has since closed, keep the rest.
- **Replace the footer verb with `Revisited by`**, naming your model id. The
  footer names the skill that last wrote the body.

The corrected body must still satisfy every rule of its own depth in
`issue-format.md` — a draft that names no source file, or a plan whose data-flow
criteria still spell out their empty, nil, and upstream-error paths. A correction
that leaves the body malformed is not a correction.

## Phase 6 — Show the diff, then write back

Show the author:

- The verdict, and each failing check with the sibling or `path:line` that
  produced it.
- A unified diff of the old body against the new one. Not the full body — the
  diff is what they are being asked to approve.
- Anything you could not verify, and why.

Then ask, with `AskUserQuestion`, where the correction lands, and recommend one:

- **Replace the issue body** (`gh issue edit --body-file`) — the default.
  `implement` and `ship` read the body, so this is the only option that reaches
  them.
- **Post it as a comment** (`gh issue comment --body-file`) — when the original
  body must survive. Say plainly that the stale body is what `implement` will
  read.
- **Neither** — print it and stop.

Write only on an explicit yes. On a **Current** verdict, do not ask at all: there
is nothing to write.

## Phase 7 — Report

State the verdict, what changed, what you could not verify, and every question
the author declined to answer. An unresolved decision belongs in the body, under
Non-Goals or as a stated assumption — never only in the chat.

Then name the next step. **Current** and **Stale** hand off to
`planwerk-agent implement <issue-ref>`. **Re-scoped** hands off to the author to
re-read first, because the shape of the work changed. **Obsolete** hands off to
the author to close: `revisit` does not close issues.

## Before you write back, verify

- The depth is unchanged. No section was added that its depth forbids, and none
  was removed except a criterion or boundary a named check retired.
- Every file path in the new body exists in the checkout.
- Every `#<number>` cross-reference points at an issue you actually read.
- Every changed line traces to a named failing check.
- The body is English, whatever language the conversation used.
- The footer is a single `Revisited by` line, last in the body.
