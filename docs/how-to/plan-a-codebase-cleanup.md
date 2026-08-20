# Plan a codebase cleanup

Survey a checkout for dead code and duplicated code, and file the verified
findings as a Meta Issue: evidence-backed findings grouped into compact
cleanup phases sized one pull request each, ready for
[`/planwerk:meta`](/how-to/split-a-meta-issue) to split into Sub Issues.

```
/planwerk:cleanup
```

Run it from inside a checkout of the repository, on an up-to-date default
branch. `cleanup` is a skill only; there is no `planwerk-agent cleanup`
command. Optional path arguments narrow the survey
(`/planwerk:cleanup internal/ cmd/`); with none, the whole repository is in
scope. The skill records the HEAD commit it surveyed, and every finding is
pinned to it.

It is narrower than [`planwerk-agent audit`](/how-to/audit-a-codebase), which
applies every loaded review pattern to the codebase and prints findings. The
skill hunts two specific defects — code nothing reaches and code that exists
twice — and its product is not a report but a Meta Issue wired for the split
pipeline.

## A finding is a lead until it survives refutation

The skill inventories the languages, runs whichever read-only dead-code and
duplication detectors are already installed (it never installs one), and
hunts by hand for what tools miss: unreferenced symbols and files, unused
dependencies, commented-out blocks, config keys nothing reads, feature flags
pinned permanently on or off, and code only its own tests keep alive.

Every hit is a lead, not a finding. A lead becomes a finding only after the
searches that would refute it come back empty: the identifier as a word
across the whole tree, the identifier as a string literal (reflection,
registries, route tables, templates), build variants for other platforms,
and the entry points a framework discovers by name rather than by reference.
Each lead lands in exactly one bucket:

| Bucket | What it means | Where it lands |
|--------|---------------|----------------|
| **Dead** | Every refutation search came back empty, on a surface no external consumer reaches | A cleanup phase, with its evidence |
| **Test-only** | Production never reaches it; its own tests keep it alive | A cleanup phase — the tests go with it |
| **Duplicated** | Two or more sites that change together, with where they diverge | A cleanup phase, one substantial cluster each |
| **Author's call** | A public surface or dynamic-dispatch candidate only you can settle | A question to you, or the `## Open decisions` block |
| **Healthy** | The lead was refuted | Dropped silently |

Duplication is only consolidated when the sites change together. Code that
looks alike today but changes for different reasons is incidental similarity,
and the skill drops it rather than proposing a merge that couples unrelated
modules. Divergent twins — copies that drifted, where a bug fixed in one is
still alive in the other — are called out with the exact divergence.

## The phases are sized to ship

Each phase in the Meta Issue is one future Sub Issue, and
[one issue is one complete pull request](/how-to/use-the-skills#one-format-every-issue-skill).
The skill carves the fewest compact phases, orders mechanical deletions
before the consolidations they dissolve (a deleted twin is duplication that
no longer needs consolidating), and records a `blocked by` edge only where a
phase touches code an earlier one deletes. Every phase leaves the tree green
on its own.

The findings under each phase name files and symbols, pinned to the surveyed
commit. That is a deliberate exception to the draft-depth no-paths rule: dead
code has no behavior to describe, so the paths are the evidence. The Sub
Issues split from the Meta Issue stay path-free like any other —
[`/planwerk:elaborate`](/how-to/elaborate-an-issue) reads the Meta Issue
through the issue's parent and re-verifies every finding against the checkout
it plans in, so a survey that has aged corrects itself at planning time.

## What reaches you

Deleting a published interface is irreversible, so an exported surface is
never asserted dead from an internal search alone. Those candidates reach you
as questions — grouped by surface, not one per symbol, each with a
recommendation, an upside and a downside per option, and what breaks if the
choice is wrong. What you settle moves into a phase; what you decline lands
in an `## Open decisions` block in the checkbox shape
[`/planwerk:decide`](/how-to/settle-meta-issue-decisions) works on, so the
split can proceed while the open calls stay visible.

You see the full rendered Meta Issue, the phase table with its dependencies,
the bucket totals, and the skill's own verification results before anything
is filed. Nothing reaches GitHub without your explicit yes.

## What it will not do

- **It never deletes code.** The working tree is untouched; the Meta Issue is
  the only artifact.
- **It never installs tools.** Detectors already on `PATH` run read-only; a
  missing one is named in the report as a coverage note.
- **It never files Sub Issues.** Run [`/planwerk:meta`](/how-to/split-a-meta-issue)
  on the Meta Issue for that.
- **It never asserts dead what it could not verify.** Public surfaces,
  dynamic dispatch, and anything only an external consumer settles go to you
  or into the open-decisions block.

## Where it sits in the pipeline

`/planwerk:cleanup` → `/planwerk:meta` → `/planwerk:decide` (when the survey
left an open-decisions block) → `/planwerk:elaborate` on each Sub Issue →
`planwerk-agent implement` or `ship`.

The skill enters the pipeline one step before
[`/planwerk:meta`](/how-to/split-a-meta-issue): where `meta` splits a Meta
Issue somebody already wrote, `cleanup` writes one from what the code itself
shows.
