# The house issue format

Every issue the planwerk skills author uses this format. `planwerk-agent plan`,
`implement`, and `ship` read these issues, so the section names and their order
are a contract, not a style preference.

Issues come at exactly two depths. A draft-depth issue describes work. An
elaborated issue plans it. Nothing in between.

`draft` and `meta` write depth 1. `elaborate` promotes depth 1 to depth 2.
`revisit` re-checks an issue at the depth it already has and leaves it there.

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

## Depth 2 — elaborated

Produced by `elaborate`. It replaces the draft body in place, keeping the
`Category` / `Scope` header line (correct `Scope` if the plan changed the size).

```markdown
**Category**: feature | **Scope**: Large

## Description

## Motivation

## User Stories

## Affected Areas

## Acceptance Criteria

## Non-Goals

## References

---

_Elaborated by [planwerk-agent](https://github.com/planwerk/planwerk-agent) with Claude:<your model id>_
```

Section rules, in the order they appear:

- **Description** — multi-paragraph prose. Include numbered "concrete
  boundaries" subsections that pair an "already exists" fact (with a file-path
  citation) against what "this issue adds". This is the densest section.
- **Motivation** — 2-4 paragraphs. Open on the concrete problem and its impact,
  never on background. Structure it as an arc: the current state, the gap this
  issue closes, what this change does about it.
- **User Stories** — optional. Each is `- As a {role}, I want {want}, so that
  {so_that}`, followed by the indented acceptance criteria that serve it.
  Generate exactly as many stories as the issue requires. For purely mechanical
  or infrastructure work (dependency bumps, formatter sweeps, CI fixes,
  refactors) emit none and omit the whole section. Never invent a synthetic
  persona to fill it.
- **Affected Areas** — one bullet per file, package, or directory that will be
  touched, each with a parenthetical naming what changes there.
- **Acceptance Criteria** — a `- [ ]` checkbox per item. Each starts with a verb
  and describes an observable check a reviewer can run.
- **Non-Goals** — one bullet per explicitly out-of-scope item, each with one
  sentence explaining why.
- **References** — READMEs, existing files, related issues, external specs.

Two optional annotation blocks may follow References, as bold lines rather than
headings, because they annotate the plan rather than belonging to the issue:

```markdown
**Executability score:** 8/10

**Reviewer Notes (unresolved):**

- <gap the refine loop could not close>
```

## Acceptance criteria must be executable

Every Acceptance Criterion maps to a concrete, named change in Description or
Affected Areas. A criterion with no corresponding change is a gap.

A **data-flow criterion** is any criterion about a function, handler, or
pipeline that consumes input and produces output. For every one of them, do not
stop at the happy path. Spell out its shadow paths as separate criteria, one
observable check each, since each renders as its own checkbox:

- Empty or zero-length input: the empty string, the empty slice or map, a zero
  count. State the expected behavior (return an empty result, not an error).
- Nil or absent input: a nil pointer or interface, a missing optional field, an
  absent config key. State the expected behavior.
- Upstream error: the dependency this code calls returns an error. Name the
  concrete error and how it is handled — `io.EOF`, `sql.ErrNoRows`,
  `context.DeadlineExceeded`, or a wrapped `fmt.Errorf("doing X: %w", err)`
  propagated to the caller.

Name the actual error value. Never write a vague "handle the error case".

## Plan quality rules

These are plan failures. Never emit them:

- Placeholders: "TBD", "TODO", "to be determined", "fill in later".
- Vague hand-waves: "add error handling", "handle edge cases", "add appropriate
  validation", "etc." Name the specific errors, edge cases, and validations.
- Cross-references in place of content: "similar to the X section", "same as
  above", "see Task N". Repeat the actual content; the engineer may read
  sections out of order.
- A reference to a type, function, file, flag, or migration that no section
  defines or cites by its real name.
- Delivery-splitting notes: "one commit ≈ one PR", "split this into separate
  PRs", "defer X to a follow-up issue", "phase 2 can land later".

That last rule is load-bearing. **One issue, one complete PR.** An elaborated
issue is implemented by one session and lands as exactly one pull request. Never
prescribe another delivery structure, and never move work the Description
requires into Non-Goals to shrink that delivery.

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

The footer names the skill that last wrote the body, so `revisit` replaces the
verb it finds rather than appending a second line. Nothing is lost: a Sub
Issue's parent is a native GitHub relationship, not the `Split from #N` prose.

Append your exact model id when your runtime context provides it (for example
`with Claude:claude-opus-4-8`). Otherwise write a bare `with Claude` — never
guess the id. Keep the `[planwerk-agent]` link intact so the issue points back
at the tool that produced it. Add the footer once, as the last line.
