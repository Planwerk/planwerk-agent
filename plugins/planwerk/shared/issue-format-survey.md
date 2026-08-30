# The survey Meta Issue

The one Meta Issue the skills author themselves: the record of a dead-code and
duplication survey, shaped so `meta` can split it. Read it beside
`issue-format.md`, whose draft depth it builds on.

## The survey Meta Issue

`cleanup` files the one Meta Issue the skills author themselves: the record of
a dead-code and duplication survey, shaped so `meta` can split it. It is a
draft-depth body carrying up to two extra sections:

```markdown
**Category**: feature | **Scope**: Large

## Description

What was surveyed, the surveyed commit, the tools and searches used, and the
totals.

## Motivation

What this specific dead and duplicated code costs, quantified.

## Cleanup phases

### 1 — <imperative phase title>

One paragraph describing the package by what it removes or consolidates —
this paragraph is what the Sub Issue split from it will say. Then the
findings it carries:

- `path/to/file.go` — `SymbolName`: what proves it dead, or the cluster it
  belongs to.

### 2 — <next phase>

## Open decisions

- [ ] D1 — <the candidate>. Recommended: <answer>. Verify: <what must still
  check it>.

---

_Surveyed by [planwerk-agent](https://github.com/planwerk/planwerk-agent) with Claude:<your model id>_
```

- `## Cleanup phases` is the work-package enumeration `meta` reads for its
  coverage check: one `###` section per package, in the order the phases must
  land.
- The findings under a phase name files and symbols, pinned to the surveyed
  commit the Description records. This is a deliberate exception to the
  draft-depth no-paths rule: a draft describes work by its behavior, and dead
  code has no behavior to describe — the paths are the finding's evidence,
  not an implementation plan. `elaborate` re-verifies every one against the
  checkout it plans in.
- The Sub Issues `meta` splits from it stay path-free like any other: each
  describes its package in the phase paragraph's terms and relies on
  `elaborate` reading the Meta Issue through the issue's parent.
- A phase that carries several findings keeps each one as its own line, and its
  paragraph describes the whole set rather than its largest member. Grouping
  sizes the pull request; it does not merge the findings. The Sub Issue split
  from that phase inherits every one of them.
- `## Open decisions` is optional and uses the checkbox shape `decide` works
  on: a proposed answer paired with what must still verify it.
