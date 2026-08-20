---
name: cleanup
description: Surveys the checkout you are in for dead code and duplicated code, and files the verified findings as a Meta Issue — evidence-backed findings grouped into compact cleanup phases sized one pull request each, ready for /planwerk:meta to split into Sub Issues. Use when a codebase has accumulated unused or copy-pasted code and the author wants a verifiable cleanup plan rather than an ad-hoc deletion pass. It plans the cleanup; it never deletes code itself.
argument-hint: "[<path>…]"
allowed-tools: AskUserQuestion Read Grep Glob Write Bash
---

# Survey a codebase for cleanup

You are a Staff Engineer surveying a codebase for dead code and duplicated
code. The product of the survey is not a diff: it is a Meta Issue recording
every verified finding with its evidence, grouped into compact cleanup phases
that each land as one pull request. `/planwerk:meta` splits that Meta Issue
into Sub Issues later; `elaborate` and the implement paths drive each one. You
delete nothing.

One idea carries this skill. **A finding is a lead until the search that would
refute it comes back empty.** Dead-code detection's failure mode is deleting
something reflection, a build tag, or a framework convention still reaches, so
every claim in the Meta Issue must survive the refutation searches in Phase 3
— and must name the evidence that proves it.

Arguments: $ARGUMENTS — optional paths that narrow the survey. With none, the
whole repository is in scope.

Read these before you start, in full:

- `${CLAUDE_SKILL_DIR}/../../shared/interaction.md` — how to ask, and when to stop
- `${CLAUDE_SKILL_DIR}/../../shared/issue-format.md` — the survey Meta Issue and its footer
- `${CLAUDE_SKILL_DIR}/../../shared/house-style.md` — prose, citations, anti-hallucination
- `${CLAUDE_SKILL_DIR}/../../shared/github.md` — the `gh` commands

You must be inside a checkout of the repository you are surveying, and the
checkout must be current — a symbol proven dead against a stale tree is not
proven at all:

```bash
git fetch origin
git status -sb
git rev-parse --short=12 HEAD
```

If the branch is behind after the fetch, or the working tree carries
uncommitted changes, say so and let the author decide before you read a single
file. Record the HEAD commit: it is the **surveyed commit** every finding is
pinned to.

## What cleanup does not do

- It never edits, deletes, or reformats code. The working tree is exactly as
  it found it; the Meta Issue is the only artifact.
- It never installs a tool and never adds configuration. Detectors already on
  `PATH` may run read-only; a missing one is a coverage note in the report,
  not a stop.
- It never files Sub Issues or wires dependencies. That is `/planwerk:meta`,
  run afterward on the Meta Issue this skill files.
- It never asserts dead what it could not verify. A candidate only an external
  consumer, dynamic dispatch, or a policy answer could settle goes to the
  author or into the open-decisions block — never into a phase as fact.
- It never proposes consolidating incidental similarity: code that looks alike
  today but changes for different reasons stays separate.

## Phase 1 — Take stock

Resolve the repository from the checkout
(`gh repo view --json nameWithOwner --jq .nameWithOwner`) and state it, so a
wrong target is caught before hours of analysis, not after.

Then inventory what you are about to survey:

- The languages and their manifests, and roughly how much code each holds.
- Generated and vendored code — mark the directories and exclude them. What a
  generator emits is the generator's to fix, and a vendor drop is upstream's.
- **Whether anything outside this repository imports it.** A library or plugin
  host has a public surface no internal search can prove dead; a leaf
  application does not. Decide this now, because it decides which bucket an
  unreferenced exported symbol can ever reach.
- The linters the repository already runs and what unused-code checks they
  have on or off. A finding an enabled check should have caught points at a
  broken setup worth one line in the report.

## Phase 2 — Collect leads

Tools first, hands second. Every hit is a lead — nothing is a finding yet.

**From the toolchain.** Run the read-only detectors that exist
(`command -v` first) and fit the stack — for example `go vet ./...` and
`deadcode ./...` for Go, `vulture` or a configured `ruff` for Python, `knip`,
`ts-prune`, or `depcheck` for JavaScript and TypeScript, `jscpd` or `dupl` for
duplication in any of them. Use what is present; name what was absent in the
report.

**Dead code, by hand.** Hunt for the kinds tools miss:

- Symbols, files, and whole modules nothing references.
- Dependencies the manifest declares and no source file imports.
- Commented-out code blocks — `git log -S` tells you when they died and
  whether the replacement still exists.
- Config keys, flags, and environment variables nothing reads.
- Branches behind a feature flag that is pinned permanently on or off.
- Code only its own tests reference: the test keeps it alive, production
  never reaches it, and both go together.

**Duplication, by hand.** Search for distinctive literals — an error string, a
magic number — appearing in several files, and for sibling functions of the
same shape. Classify each cluster: an exact clone, structural duplication
(the same algorithm over different types), or a **divergent twin** — copies
that drifted, where a bug fixed in one is still alive in the other. Divergent
twins are the expensive kind; say which sites diverge and how.

## Phase 3 — Verify every lead

For each dead-code lead, run the searches that would refute it, and record
which you ran:

1. The identifier as a word, across the whole tree — code, tests, scripts,
   CI, docs, examples.
2. The identifier as a **string literal** — reflection, registries, dependency
   injection, route tables, templates, serialization tags, codegen inputs.
3. Build variants — files for other platforms, build tags, conditional
   imports. Unused here may be used there.
4. Convention-discovered entry points — main functions, plugin hooks,
   migrations, scheduled jobs, anything a framework finds by name rather than
   by reference.

For each duplication cluster, verify every site exists, and answer the one
question that makes consolidation right: **do these sites change together?**
A cluster whose sites change for different reasons is incidental similarity —
drop it.

Sort every surviving lead into exactly one bucket:

- **Dead** — every search came back empty, and the symbol is not part of a
  surface an external consumer could reach. Evidence: the definition site and
  the searches that found nothing.
- **Test-only** — unreachable from production, kept alive by its own tests.
- **Duplicated** — a cluster of two or more sites, with what is identical,
  where they diverge, and why they change together.
- **Author's call** — a public surface, a dynamic-dispatch candidate, or
  anything only an external consumer or a policy answer settles.
- **Healthy** — the lead was refuted. Drop it silently; the Meta Issue does
  not list what is fine.

Note where the buckets touch: a Dead symbol that is one site of a Duplicated
cluster means the deletion dissolves part of the duplication — Phase 5 orders
the phases around exactly this.

## Phase 4 — Put the author's calls to the author

Deleting a published interface is on `interaction.md`'s irreversibility list,
so every **Author's call** item is genuinely theirs. Group the items by
surface — one question per exported package or API area, never one per symbol
— and ask each group as its own `AskUserQuestion`: a recommendation on one
option, a concrete upside and an honest downside on each, one sentence on what
breaks if the choice is wrong.

What the author settles moves to Dead or Healthy accordingly. What they
decline or leave open lands in the Meta Issue's `## Open decisions` block, in
the checkbox shape `/planwerk:decide` works on — a proposed answer paired with
what must still verify it. Never fold an unanswered call into a phase.

## Phase 5 — Carve the phases

Each phase is one future Sub Issue, and one issue is one complete pull
request — size every phase so a single session can land it. Then:

- **Fewest compact phases.** Group small findings of the same kind; give a
  substantial consolidation cluster a phase of its own. Never let the plan
  sprawl into ten two-line phases.
- **Mechanical before structural.** Provably dead deletions and
  dependency/config pruning come first; consolidations follow, because a
  deleted twin dissolves duplication and a consolidation planned before the
  deletion works on code that is about to vanish.
- **Each phase leaves the tree green** — independently mergeable, no phase
  that only pays off once a sibling lands.
- **Real dependencies only.** A `blocked by` edge exists when a phase touches
  code an earlier phase deletes or moves — otherwise phases stay parallel.

For each phase, decide a short stable key encoding the order, an imperative
title, one paragraph describing the package by what it removes or consolidates
(this paragraph is what its Sub Issue will say), a Scope (Small, Medium,
Large), and the findings it carries.

Then run these checks — each a pass/fail you can state:

1. Every Dead and Test-only finding names its definition site and the
   refutation searches that came back empty.
2. Every cluster names at least two sites and why they change together.
3. Every finding sits in exactly one phase; no phase is empty.
4. Deletions precede the consolidations they dissolve; the `blocked by` graph
   is acyclic and minimal.
5. Every path exists at the surveyed commit.
6. No Author's-call item appears in a phase as settled.
7. No phase reads as several deliveries — one phase, one pull request.

Fix what fails and re-run. Only a plan that passes all seven reaches Phase 6.

## Phase 6 — Write the Meta Issue

Emit the body in English, in the survey Meta Issue format from
`issue-format.md`: the `**Category**` / `**Scope**` header line, a
`## Description` naming what was surveyed, the surveyed commit, the tools and
searches used, and the totals; a `## Motivation` stating what this specific
dead and duplicated code costs — quantified, not adjectives; a
`## Cleanup phases` section with one `###` heading per phase, which is the
work-package enumeration `meta` splits; the `## Open decisions` block when
Phase 4 left any; and the `Surveyed by` footer.

The findings under each phase name files and symbols pinned to the surveyed
commit. That is the survey's deliberate exception to the draft-depth no-paths
rule: dead code has no behavior to describe, so the paths are the evidence.
The Sub Issues split from this issue stay path-free and point back here.

Give the issue a descriptive, specific title in imperative mood.

## Phase 7 — Confirm, then file

Show the author:

- A table: phase key, title, scope, and the keys it is blocked by.
- The bucket totals, including how many leads were refuted and dropped.
- The full rendered body.
- The result of each Phase 5 check.
- What could not be verified, and which detectors were missing.

Then ask, with `AskUserQuestion`, whether to file the Meta Issue as shown,
adjust it, or cancel — and recommend one. File only on an explicit yes: write
the body to a temporary file, pass `--body-file` to `gh issue create`, and
print the new issue's URL.

## Phase 8 — Report

Open with one line: the issue filed, how many dead findings and duplication
clusters it records, in how many phases, at which commit. Then name every
Author's call the author declined, every detector that was absent, and
anything the survey could not verify.

End with the next step as the last line, nothing after it — no closers, no
recap: `/planwerk:meta <issue-ref>` splits the Meta Issue into Sub Issues, and
when an `## Open decisions` block exists, `/planwerk:decide` settles it after
the split. `cleanup` stops at filing. It does not split, elaborate, or delete
anything.

## Before you file, verify

- The body carries `## Description`, `## Motivation`, and `## Cleanup phases`
  in that order under the `Category` / `Scope` header line, and `Scope` is
  exactly one of `Small`, `Medium`, `Large`.
- Every path in the body exists at the surveyed commit, and every refutation
  search the body cites was actually run.
- Every phase has a key, an imperative title, a scope, and at least one
  finding; every `blocked by` key exists.
- The `## Open decisions` items, if any, are checkboxes pairing a proposed
  answer with what must verify it.
- The body is English, whatever language the conversation used.
- The footer is the last line, reads `Surveyed by`, and names your model id.

If any check fails, fix it before filing, not after.
