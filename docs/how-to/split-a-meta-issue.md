# Split a Meta Issue

Turn a **Meta Issue** — an issue that frames a larger body of work as several
self-contained work packages — into linked, draft-depth Sub Issues with the
`/planwerk:meta` skill. It reads the Meta Issue, decides the breakdown on its
own, verifies that breakdown, and then, once you approve, files each Sub Issue,
links it to the Meta Issue with GitHub's native sub-issue relationship, records
the dependency ordering as native `blocked by` relationships, and back-fills the
Meta Issue body so its work-package lines reference the new Sub Issues.

Each Sub Issue stops at draft depth, like [`draft`](/how-to/draft-an-issue)
produces — a title plus Description, Motivation, and a rough Scope. It is
deliberately **not** elaborated. Pick a Sub Issue and run
[`elaborate`](/how-to/elaborate-an-issue) / [`implement`](/how-to/implement-an-issue)
on it when you are ready; `meta` itself stops at creating and linking.

Install the skills first: see [Use the issue skills](/how-to/use-the-skills).

```
/planwerk:meta owner/repo#123
```

## The skill decides the split, not you

`meta` does not ask what to split or how. It reads the Meta Issue and carves it,
then brings you a finished proposal to accept, adjust, or reject. Two rules
govern how many Sub Issues it carves, and they do not conflict:

- **Coverage is hard.** When the Meta Issue enumerates its work packages — a
  checkbox list, numbered tiers, lettered workstreams — every listed package maps
  to exactly one Sub Issue and every Sub Issue maps back to a listed package. No
  package is dropped to keep the split small, none are merged, and none are
  invented.
- **Fewest packages, otherwise.** For work the Meta Issue does not already
  enumerate, the skill groups it into the fewest sensible packages rather than
  many tiny ones.

Each Sub Issue is carved as a **vertical slice** — it cuts end-to-end through
every layer it touches and is demoable on its own — rather than a horizontal
layer that delivers nothing until a sibling lands.

## It checks its own work before you see it

This is what the `meta` subcommand could not do. Before the split reaches you,
the skill runs six checks and reports the result of each:

1. **Coverage, both directions.** Every enumerated work package has exactly one
   Sub Issue, and every Sub Issue maps back to one package.
2. **No cycles** in the `blockedBy` graph.
3. **Every blocker exists**, and no Sub Issue blocks itself.
4. **Vertical slices.** Each Sub Issue delivers something on its own.
5. **Draft depth.** No Sub Issue names a source file, a symbol, an acceptance
   criterion, or an implementation step.
6. **Keys unique and ordered**, matching the order the Meta Issue implies.

A split that fails a check is re-cut and re-checked. Only a passing split is
presented, together with the Sub Issue table, an ASCII dependency graph, and one
sentence on why the order is what it is. Nothing is filed until you approve. If
you contest a single Sub Issue, the skill re-opens that one rather than the whole
split.

## Dependencies are real GitHub relationships

The skill records each Sub Issue's ordering as a native `blocked by`
relationship, not as prose in the body. That relationship renders in GitHub's
issue UI, gates the dependent issue there, and is what
[`ship`](/how-to/ship-a-meta-issue) reads back to drive the Sub Issues in
dependency order. A `Blocked by: b` line in an issue body is invisible to `ship`.

Setting a relationship is best-effort, like sub-issue linking. A GitHub
deployment that does not expose issue dependencies degrades to "all Sub Issues
independent", and the failure is reported rather than swallowed. A failed link
never deletes an issue that was already created.

## The Meta Issue body is synced, or left alone

Where the Meta body carries a work-package list, each line is back-filled with
the new `#number` reference so the prose and the sub-issue list agree. The body
is written back **only when every Sub Issue resolved to a line**. A partial
substitution is worse than none, so on a mismatch the body is left untouched and
you are told to place the references yourself.

## Sub Issues are planned against their Meta Issue

Because the Sub Issues are linked, `elaborate` and `implement`'s planning session
read that link back: run on a Sub Issue, they pull in the Meta Issue and the
sibling Sub Issues so each Sub Issue is planned as a coherent slice of the whole —
scoped to its part, deferring adjacent work to the sibling that owns it. See
[Sub Issues are elaborated against their Meta Issue](/how-to/elaborate-an-issue#sub-issues-are-elaborated-against-their-meta-issue).

## What it does not do

`meta` mirrors `draft`: it clones nothing, loads no review patterns, and does not
elaborate. It also does not orchestrate the Sub Issues — it does not drive them
through `elaborate`, `implement`, or `fix`, and it does not close the Meta Issue.
To drive them all to merged in dependency order, use
[`ship`](/how-to/ship-a-meta-issue).
