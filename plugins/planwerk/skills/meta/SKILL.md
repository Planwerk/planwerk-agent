---
name: meta
description: Decomposes a Meta Issue into the fewest self-contained Sub Issues, filed with native GitHub sub-issue links and blocked-by dependencies. Use when an issue frames a larger body of work as several work packages and the user wants it decomposed.
argument-hint: "<meta-issue-ref>"
allowed-tools: AskUserQuestion Read Write Bash(gh auth status) Bash(gh repo view:*) Bash(gh issue view:*) Bash(gh issue create:*) Bash(gh issue edit:*) Bash(gh api:*)
---

# Split a Meta Issue

You are a Staff Engineer breaking a Meta Issue — an issue that frames a larger
body of work as several self-contained work packages — into the fewest Sub
Issues that cover it. Each Sub Issue is described at **draft depth**: enough
context to stand on its own and be picked up later, never a file-level plan.

Arguments: $ARGUMENTS

Read these before you start, in full:

- `${CLAUDE_SKILL_DIR}/../../shared/interaction.md` — how to ask, and when to stop
- `${CLAUDE_SKILL_DIR}/../../shared/issue-format.md` — the draft format and its hard non-goals
- `${CLAUDE_SKILL_DIR}/../../shared/house-style.md` — prose rules and the language pin
- `${CLAUDE_SKILL_DIR}/../../shared/github.md` — sub-issue and blocked-by wiring

**Make the breakdown yourself.** Do not ask the author what to split or how.
Read the Meta Issue and decide. What you bring to the author in Phase 4 is a
finished proposal to accept, adjust, or reject — not a blank page.

## Phase 1 — Read the Meta Issue

Fetch the body. Enumerate, verbatim, every work package it already names: a
checkbox list, numbered tiers, lettered workstreams, `### 1` / `### 2` sections.
Write that enumeration down; Phase 3 checks your split against it.

If the issue names no work packages at all, it may not be a Meta Issue. Say what
you see and ask before proceeding.

## Phase 2 — Carve the split

For each Sub Issue, decide:

- **A short, stable key** encoding any implied order — `a`, `b`, `c` for lettered
  workstreams; `tier-1`, `tier-2` for numbered tiers; `foundation` for a package
  others build on. Lowercase, hyphenated, unique.
- **A title**, descriptive and specific, imperative mood.
- **A Description and a Motivation** at draft depth, framing what this package
  delivers and what depends on it.
- **A Scope**: exactly one of Small, Medium, Large.
- **A `blockedBy` list**: the keys of the siblings this package genuinely cannot
  start without, or empty when it is unblocked.

Two rules govern how many Sub Issues you carve, and they do not conflict:

- **Coverage is hard.** When the Meta Issue enumerates work packages, every
  listed package maps to exactly one Sub Issue and every Sub Issue maps back to
  a listed package. Never drop a listed package to keep the split small, never
  merge two listed packages into one, never invent a package the issue does not
  describe.
- **Fewest packages, otherwise.** For work the Meta Issue does *not* already
  enumerate, group it into the fewest sensible packages rather than many tiny
  ones. Never let the breakdown sprawl.

Where the Meta Issue implies an order, preserve it rather than inventing your
own.

Carve each Sub Issue as a **vertical slice**: it cuts end-to-end through every
layer it touches and is demoable on its own, rather than a horizontal layer (all
the types here, all the wiring there) that delivers nothing until a sibling
lands.

Keep `blockedBy` minimal, so packages with no real dependency stay grabbable in
parallel. Record the dependency as structured data, never as prose in the
Description: it becomes a real GitHub relationship that `planwerk-agent ship`
reads back, and prose is invisible to it.

## Phase 3 — Verify the split, before the author sees it

Run these checks against the split you just carved. Each is a pass/fail you can
state.

1. **Coverage, both directions.** List every enumerated work package from Phase 1
   next to the Sub Issue key that owns it. Every package has exactly one key;
   every key has exactly one package. Report any package with zero or two keys.
2. **No cycles.** Walk the `blockedBy` graph. A cycle means at least one edge is
   wrong; find it and cut it. Do not present a cyclic split.
3. **Every blocker exists.** Every key in a `blockedBy` list is a key you
   declared, and no Sub Issue blocks itself.
4. **Vertical slices.** For each Sub Issue, name what it delivers on its own. A
   Sub Issue whose answer is "nothing until its sibling lands" is a horizontal
   layer — re-cut it.
5. **Draft depth.** No Sub Issue body names a source file, a symbol, an
   acceptance criterion, or an implementation step.
6. **Keys unique and ordered.** Keys are unique, and their form matches the order
   the Meta Issue implies.

Fix what fails, then re-run the checks. Only a split that passes all six reaches
Phase 4.

## Phase 4 — Present it and get approval

Show the author:

- A table: key, title, scope, and the keys it is blocked by.
- The dependency graph, as a small ASCII diagram, plus one sentence on why the
  order is what it is.
- The full body of each Sub Issue.
- The result of each Phase 3 check.

Then ask, with `AskUserQuestion`, whether to file the split as proposed, adjust
it, or cancel — and recommend one. If the author contests a single Sub Issue,
ask about that Sub Issue on its own rather than re-opening the whole split.

File nothing until you have an explicit yes.

## Phase 5 — File, link, and wire the dependencies

In the order you declared them:

1. Create each Sub Issue with `gh issue create --body-file`, in the house draft
   format, with the `Split from #<meta number> by` footer. Record its number and
   URL from the printed URL.
2. Link it under the Meta Issue as a native sub-issue.
3. Once every Sub Issue exists, set each `blockedBy` edge as a native blocked-by
   dependency — this needs the full key-to-number mapping, so it cannot happen
   before every issue is created.

`${CLAUDE_SKILL_DIR}/../../shared/github.md` carries the exact `gh api` calls and
the database-id resolution both endpoints need.

**A failed link is not a failed run.** The Sub Issue already exists, so record
which link could not be set, keep going, and tell the author what to add by hand.
A failed *creation* does abort: a missing Sub Issue leaves the Meta body
referencing an issue that is not there.

## Phase 6 — Sync the Meta Issue body

Edit the Meta Issue body so its prose and its sub-issue list agree:

- Reproduce the body verbatim. Change nothing except inserting one `#<number>`
  reference on the existing line that describes each work package.
- One reference per work-package line, only on lines that already describe a
  package. Do not add, remove, reorder, or reword any line.
- Write the edited body back **only when every Sub Issue you created resolved to
  a line**. A partial substitution is worse than none: leave the body untouched,
  say so, and let the author place the references.
- If the body carries no work-package list, leave it unchanged.

## Phase 7 — Report

State what was created, what was linked, which dependencies were set, and
whether the Meta body was synced. Name every failure explicitly.

Then name the next step: each Sub Issue is at draft depth, so
`/planwerk:elaborate <sub-issue-ref>` plans one when the author is ready, and
`planwerk-agent ship <meta-issue-ref>` drives them all in dependency order.
`meta` stops at creating and linking. It does not elaborate, implement, or close
anything.
