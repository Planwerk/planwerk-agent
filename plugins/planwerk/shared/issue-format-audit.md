# The audit issue

The issues `audit` files: one per work package a codebase audit found worth a
pull request, shaped so `elaborate` can plan it. Read it beside
`issue-format.md`, whose draft depth it builds on.

## The audit issue

`audit` files a draft-depth body carrying one extra section, the evidence:

````markdown
**Category**: feature | **Scope**: Small

## Description

What is wrong and what the change does, in the terms a maintainer acts on:
the behavior, the contract, the principle the code violates. No file names.

## Motivation

What the defect costs — who it reaches, what fails or erodes, and what is
worse the longer it stays.

## Evidence

Audited at `<commit>`. Severity: WARNING. Pattern: `<pattern name>`.

- `path/to/file.go:42-45`: what triggers the finding at this site, in one
  line. Handled elsewhere: not found (`git grep -n "<what you searched>"`).
  Test coverage: unknown.

  ```go
  <the triggering lines, verbatim, original indentation>
  ```

- `path/to/other.go:17`: the next site, the same way.

Also at: `path/a.go:12`, `path/b.go:30`, `path/c.go:8`.

---

_Audited by [planwerk-agent](https://github.com/planwerk/planwerk-agent) with Claude:<your model id>_
````

- `## Evidence` is the one place a path may appear. This is a deliberate
  exception to the draft-depth no-paths rule, scoped like the survey Meta
  Issue's: a draft describes work by its behavior, and the Description and
  the Motivation still do, but a finding without its site is an opinion. The
  sites are the finding's proof, not an implementation plan; `elaborate`
  reads them as leads and grounds its plan in the checkout it plans in, so a
  site that moved since the audit is corrected there.
- The first line of the section pins the audited commit, states the highest
  severity in the package, and names the pattern. `Pattern:` is the exact
  `# Review Pattern:` name of a pattern loaded in Phase 1, comma-separated
  when the package spans several; a finding from a principle family or from
  beyond every pattern reads `Pattern: none`. Never name a pattern the audit
  did not load. The severity lives here and nowhere else: never in the
  title, never as a label.
- Every site quotes the triggering lines verbatim, with any secret's value
  replaced by `<redacted>`, and records the refutation searches the audit
  ran: the `file:line` that handles the defect or `not found (<search>)`,
  and the test that covers the behavior or `test coverage unknown`. A site
  the audit could not quote is not evidence and is not in the issue. A
  missing file is the one site without a quote; it cites where the file
  belongs and the search that found nothing.
- Fence each quote with one backtick more than the longest backtick run
  inside it, never fewer than three, so a quoted Markdown file cannot end
  the fence early.
- A package with many sites keeps the three to five most representative in
  full and lists the remaining ones after `Also at:` as `path:line` entries,
  each one opened.
- One package is one complete pull request. A body that reads as several
  deliveries is two issues.
