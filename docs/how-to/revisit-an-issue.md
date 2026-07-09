# Revisit an issue

Take an issue that was prepared a while ago and re-check it against the code as
it stands today: citations that moved, boundaries that no longer hold,
acceptance criteria the repository already satisfies, and — for a Sub Issue —
scope that a sibling has quietly absorbed or abandoned.

```
/planwerk:revisit owner/repo#42
```

Run it from inside a checkout of the issue's repository. `revisit` is a skill
only; there is no `planwerk-agent revisit` command.

## Why an issue goes stale

An issue is written against a snapshot. `elaborate` cites the files that existed
that day, and it scopes a Sub Issue against what its siblings' bodies said they
would do. Both move.

The gap that matters is between **promise and delivery**. A sibling's issue body
is a promise; the pull request that closed it is what actually shipped. It may
have delivered more than it promised, absorbing part of this issue's work. It may
have delivered less — [`ship`](/how-to/ship-a-meta-issue) skips a Sub Issue it
cannot finish, and a maintainer may close one as good enough — leaving this
issue's plan resting on code nobody wrote.

Nothing else in the pipeline notices. `implement` trusts the issue body it is
given, and `ship` drives Sub Issues in dependency order without re-reading a plan
its predecessors invalidated. `revisit` is the step that reads the issue against
reality instead of against its own history.

## What it checks

Against the current default branch:

- Every cited path and symbol still exists, with the signature the plan assumes.
- Every "already exists" boundary in the Description still holds.
- Every "this issue adds" item is still absent — one that is already there is
  delivered work.
- Every acceptance criterion is still unsatisfied.
- The `Scope` still matches the size of what is left.

And, when the issue is a **Sub Issue**, against the Meta Issue and its siblings:

- The Meta Issue's framing and shared decisions still hold.
- A closed sibling delivered **less** than it promised, so this issue's plan
  depends on something that does not exist.
- A closed sibling delivered **more** than it promised, so this issue should
  shrink and cross-reference it.
- An **orphaned deferral**: this issue's Non-Goals say "the remaining X is
  handled by #K", #K is closed, and X is nowhere in the code. X is now nobody's
  work.
- An open sibling collides with what this issue plans to touch.

## The four verdicts

| Verdict | What it means | What `revisit` writes |
|---------|---------------|-----------------------|
| **Current** | Every check passed | Nothing. The issue is ready |
| **Stale** | Citations moved; the work still stands | The corrected body |
| **Re-scoped** | Part of the work landed elsewhere, or a sibling left a hole | The re-scoped body |
| **Obsolete** | Every criterion passes at HEAD, or the problem is gone | Nothing — it shows the evidence and lets you close the issue |

`Current` is a successful run, not a wasted one. `revisit` will not manufacture a
correction to justify itself: a verdict with no failing check behind it is
`Current`.

## Corrections are minimal, and you see the diff

Every changed line must trace to a named failing check. A changed line with no
failing check behind it is reverted before you ever see it, and what `revisit`
presents for approval is a unified diff of the old body against the new one — not
the whole body. An author who cannot trust the skill to leave their prose alone
will not run it twice.

Corrections shrink. `revisit` grows a plan in exactly one case: the current code
makes a criterion unimplementable as written, and satisfying the Description now
needs a larger change. It says so out loud rather than slipping it in.

When the body carries an `Executability score:` line from `elaborate`, that score
is a claim about text the correction just changed, so `revisit` re-scores against
the same rubric or removes the line.

## What it will not do

- **It never changes an issue's depth.** A draft-depth Sub Issue is re-checked as
  a draft and stays one; an elaborated issue is re-checked as a plan and stays
  one. Deepening a draft into a plan is
  [`/planwerk:elaborate`](/how-to/elaborate-an-issue).
- **It never closes, reopens, relabels, or re-links an issue**, and it never
  implements one. `Obsolete` is the one verdict whose correct action is
  destructive, so it is the one verdict where `revisit` hands you the evidence
  and stops.
- **It never grows scope opportunistically.** Something it noticed along the way
  is a new issue, not an edit to this one.

Nothing reaches GitHub without an explicit yes, as with every planwerk skill. On
approval it either replaces the issue body — the default, and the only option
`implement` and `ship` read — or posts the correction as a comment, leaving the
stale body in place.

## Where it sits in the pipeline

`/planwerk:draft` → `/planwerk:elaborate` → `/planwerk:revisit` →
`planwerk-agent implement`.

The middle step is worth running whenever an elaborated issue has been sitting
long enough for the branch to move under it, and especially on the Sub Issues of
a Meta Issue, where every merged sibling changes what the next one has left to
do. Revisit a Meta Issue itself and the skill will tell you it holds no plan of
its own — revisit its open Sub Issues instead.
