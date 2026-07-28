# Settle a Meta Issue's decisions

Take a Meta Issue whose split deferred a block of decisions to a spike, verify
each one against the repository, put the genuine judgment calls to you, and
record the outcomes in the Meta Issue and every Sub Issue that already assumes
one.

```
/planwerk:decide owner/repo#767
```

Run it from inside a checkout of the Meta Issue's repository. `decide` is a
skill only; there is no `planwerk-agent decide` command. Pass either the Meta
Issue itself or the Sub Issue [`/planwerk:meta`](/how-to/split-a-meta-issue)
filed to verify its decisions — the skill resolves the other end through the
same Meta/Sub-Issue neighborhood query `elaborate` and `revisit` use.

## Where the decisions block comes from

A Meta Issue frames a larger body of work as several work packages, and one of
those packages is sometimes not a deliverable at all but a spike: a checklist
of items, each pairing a recommendation with something nobody has checked yet
— which launcher to ship, which field a spec needs, which upstream behavior to
rely on. `/planwerk:meta` treats that block like any other work package and
files a Sub Issue whose whole job is to verify and record the outcomes, because
every other Sub Issue it splits out alongside it is already written as if the
recommendations were settled fact.

Nothing closes that loop by itself. The recommendation stays a recommendation
until something opens the files behind it, and every sibling keeps citing it as
though it had been checked. `decide` is that closing step.

## A recommendation is a guess until it is checked

The skill reads every item in the decisions block and verifies it against
whatever it names as its own source, in order: the repository at HEAD, a
pinned reference the repository's own configuration commits to (a lockfile
entry, a vendored copy, a submodule — checked at the pinned point, never at
that dependency's own upstream HEAD), documentation vendored in the checkout,
and a related decision a closed sibling already settled. Every item lands in
exactly one bucket:

| Bucket | What it means | Who settles it |
|--------|---------------|-----------------|
| **Confirmed** | Every source agrees with the recommendation as written | The skill, citing what it opened |
| **Corrected** | The direction holds, a detail does not — a different path, flag, or key | The skill, citing the source that forced it |
| **Contested** | A real alternative exists, or the sources are silent | You |
| **Broken** | The spike disproves the premise itself | You, since no option as written is right |

A "Recommended:" clause is treated exactly like a stalled plan's `OPEN
QUESTION` marker in [`clarify`](/how-to/clarify-an-issue): a guess, not a
verdict, until the files behind it are opened. `decide` never files an item as
Contested without having opened those files first.

## What reaches you

One `AskUserQuestion` per **Contested** item, largest blast radius first, with
the same doctrine every planwerk skill asks under — a recommendation on one
option, a concrete upside and an honest downside on each, one sentence on what
breaks if the choice is wrong. Two things only `decide` can add, because only it
has read both the Meta Issue and its siblings:

- **What a sibling already assumes.** A Sub Issue drafted alongside the split
  usually already writes as if the recommendation were fact — knowing that
  tells you what the other option costs downstream.
- **What the other option invalidates** — the sibling sentences or checklist
  items that stop being true.

A **Broken** item reaches you differently: inline, numbered, at the end of the
message, because no option as written is correct. The skill states what the
spike actually found and asks what should replace the disproven premise, rather
than offering invented alternatives.

If you decline an item, it is recorded as an explicit assumption, not quietly
resolved to the recommendation.

## The outcomes land in two places

The decisions block itself says to record outcomes in the Meta Issue's body —
that is what makes it the source of record. `decide` checks each item's box and
replaces its "Recommended:" framing with the settled fact and a short citation,
touching nothing else in the Meta Issue.

Then it searches every **open** sibling's body for a reference to a decision's
code (`D1`, `(D8)`, or prose restating the pre-verification recommendation) and
corrects only the ones a Confirmed, Corrected, or decided outcome actually
changes — minimal diff, the same discipline [`revisit`](/how-to/revisit-an-issue)
uses, one changed clause per settled fact. A closed sibling is left alone: its
merged pull request already shipped against whatever it assumed. A sibling
whose depth cannot hold the correction without naming a source file is left
for [`/planwerk:elaborate`](/how-to/elaborate-an-issue) instead.

You approve a diff per issue, not a rewritten body, and can approve the Meta
Issue alone while holding the sibling corrections back for later.

## What it will not do

- **It never plans a Sub Issue.** Recording a settled fact is not writing
  Affected Areas or Acceptance Criteria — run
  [`/planwerk:elaborate`](/how-to/elaborate-an-issue) afterward on whichever
  Sub Issue the decision unblocks.
- **It never changes a Sub Issue's depth**, and never grows one's scope. A
  settled decision that implies work no sibling covers is named as a gap in
  the report, not added by this skill.
- **It never closes an issue**, including the spike Sub Issue whose job just
  ended. `decide` posts a comment saying its verification is complete and lets
  you close it.
- **It never reaches outside the checkout.** A decision only a live system, an
  un-vendored upstream source, or a policy call can settle is asked as an open
  question, never guessed.

## Where it sits in the pipeline

`/planwerk:meta` → `/planwerk:decide` (when the split produced a decisions or
spike Sub Issue) → `/planwerk:elaborate` on the Sub Issues the decisions
unblocked → `planwerk-agent implement` or `ship`.

That separates it from [`/planwerk:clarify`](/how-to/clarify-an-issue), which
answers a plan that already stalled at `NEEDS_CONTEXT`, and from
[`/planwerk:revisit`](/how-to/revisit-an-issue), which re-checks an issue
against what has changed since it was written. `decide` runs earlier: it
settles decisions a Meta Issue deferred to a spike before any of them have
been checked at all.
