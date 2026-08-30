# The house issue format

Every issue the planwerk skills author uses this format. `planwerk-agent plan`,
`implement`, and `ship` read these issues, so the section names and their order
are a contract, not a style preference.

Issues come at exactly two depths. A draft-depth issue describes work. An
elaborated issue plans it. Nothing in between.

`draft` and `meta` write depth 1. `elaborate` promotes depth 1 to depth 2.
`revisit` re-checks an issue at the depth it already has and leaves it there.
`cleanup` files a Meta Issue at depth 1 plus the sections its findings need —
see `issue-format-survey.md`.

This file carries depth 1 and the two rules every issue obeys whatever its
depth: the title and the footer. Depth 2, and the rules a plan must satisfy to
be executable, are in `issue-format-plan.md`. The survey Meta Issue `cleanup`
files is in `issue-format-survey.md`. Read the one your skill writes.

## Depth 1 — draft

Produced by `draft`, and by `meta` for each Sub Issue. It describes the idea and
stops there.

```markdown
**Category**: feature | **Scope**: Medium

## Description

A few short paragraphs framing the problem and what the work does, in plain
terms a maintainer can act on.

## Motivation

Why this matters: who benefits, and what is worse without it.

---

_Drafted by [planwerk-agent](https://github.com/planwerk/planwerk-agent) with Claude:<your model id>_
```

- `Category` is always `feature`.
- `Scope` is exactly one of `Small`, `Medium`, `Large`.
- A Sub Issue is byte-identical except for its footer verb — see
  [Attribution footer](#attribution-footer).

### Hard non-goals at draft depth

A draft describes; it does not plan. Never write any of these:

- A file-level affected-areas breakdown.
- A step-by-step implementation design.
- Acceptance criteria grounded in concrete files, symbols, or functions.
- The name of a specific source file or function, or a codebase analysis for a plan.

If you catch yourself writing an "Affected Areas" list, "Acceptance Criteria",
or implementation steps, stop. That is `elaborate`'s job, run later on this issue.

## Titles

Descriptive and specific, imperative mood, no severity or priority prefix.

## Attribution footer

Every issue body ends with a `---` separator and a single italic footer line:

```markdown
---

_<verb> [planwerk-agent](https://github.com/planwerk/planwerk-agent) with Claude:<your model id>_
```

| Skill | Verb |
|-------|------|
| `draft` | `Drafted by` |
| `elaborate` | `Elaborated by` |
| `meta` (each Sub Issue) | `Split from #<meta issue number> by` |
| `revisit` | `Revisited by` |
| `clarify` | `Clarified by` |
| `decide` (each issue it corrects) | `Decided by` |
| `cleanup` (the Meta Issue it files) | `Surveyed by` |

The footer names the skill that last wrote the body, so `revisit`, `clarify`,
and `decide` replace the verb they find rather than appending a second line.
Nothing is lost: a Sub Issue's parent is a native GitHub relationship, not the
`Split from #N` prose.

A Sub Issue filed in another repository is signed `Split from owner/repo#N by`,
naming the Meta Issue's repository. The bare `#N` in that footer would otherwise
resolve against the Sub Issue's own repository and credit an unrelated issue.

`decide` is narrower than the other two: it only ever touches one section of a
Meta Issue's body (its decisions block) rather than the whole document, so it
signs the Meta Issue only when a footer already exists there. Many Meta Issues
are hand-authored and never ran through a planwerk skill; adding a footer to
prose `decide` did not otherwise write would misattribute the rest of the
document. Every Sub Issue it actually corrects still gets the verb swap, since
`meta` already left one to replace.

Append your exact model id when your runtime context provides it (for example
`with Claude:claude-opus-5`). Otherwise write a bare `with Claude` — never
guess the id. Keep the `[planwerk-agent]` link intact so the issue points back
at the tool that produced it. Add the footer once, as the last line.
