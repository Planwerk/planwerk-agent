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

This file carries depth 1 and the three rules every issue obeys whatever its
depth: the title, the footer, and what to do when a document exceeds its
body's limit.
Depth 2, and the rules a plan must satisfy to be executable, are in
`issue-format-plan.md`. The survey Meta Issue `cleanup` files is in
`issue-format-survey.md`. Read the one your skill writes.

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

## The body's limit, and the continuation comment

A body has a limit, and a document over it is not shortened. Once a body
carries a plan — the elaborated depth — it holds at most 40,000 characters
(`issue-format-plan.md` says why). A draft-depth body and a survey Meta Issue
are bounded only by GitHub's cap of 65,536 characters, which GitHub enforces
on every body and every comment by rejecting a longer write outright. A
document over its limit is written as the body plus one or more **continuation
comments**, and every skill that reads an issue reads the parts as one
document (the reading rule is in `github.md`, under Reading). Nothing is
dropped, tightened, or summarised to avoid the split. It is lossless: the split
changes where the text is stored and nothing else.

Count before every write: `wc -c < body.md`. At or under the limit, write the
body as it is. Over it, split it:

1. Detach the footer: the trailing `---` rule and the italic line under it.
2. Cut the rest into parts of at most 38,000 characters, which leaves the body
   room for its pointer and footer under 40,000. Cut on a `## ` heading when
   one falls in the upper half of the part, otherwise on the nearest `### `
   heading or blank line, and never inside a fenced code block.
3. The body is part 1, then a blank line, then these two lines, then a blank
   line and the footer:

   ```markdown
   <!-- planwerk-agent:continued 1/N -->
   _This body continues in a comment below (part 2 of 2: <the `## ` sections it carries>). Read the parts as one document._
   ```

   With more than one comment the pointer reads `N-1 comments below (parts 2
   to N of N: <the `## ` sections they carry>)`.

4. Every further part is a comment that opens with these two lines, then a
   blank line, then the part:

   ```markdown
   <!-- planwerk-agent:continuation k/N -->
   _Issue body, continued (part k of N). The sections below belong to the body above, which reached its size limit. Read them as part of the body; whoever rewrites the body rewrites this comment with it._
   ```

5. Write each part as its own file with `Write` — the draft is in your
   context, so no shell tool has to cut it — and count each file: `wc -c`, the
   body at most 40,000 characters and no comment larger.

`N` counts the body, so a body and one comment are `1/2` and `2/2`. The HTML
markers are the contract: `planwerk-agent implement`, `elaborate`, and `prompt`
find the parts by them and merge them before they read a section, and the
skills do the same by hand.

A rewrite owns the continuations. Whenever you replace a body, list the issue's
continuation comments first (the query is in `github.md`) and then, in this
order: edit the body; rewrite each existing continuation comment in place with
the new part of the same number; post a new comment for every part beyond
them; delete every existing continuation comment beyond the new count. A body
that shrank back under its limit therefore leaves no comment behind, which
matters because the next reader would merge a stale continuation into the new
body. A new issue is created with part 1, and its continuations are posted
right after.

A document posted as a comment rather than as the body — `elaborate`'s second
landing option — meets GitHub's cap on a comment. Over it, split the document
the same way, at the same part size, and post the parts in order: part 1, with
its `continued` marker, pointer, and footer, as the first comment, then each
continuation comment. Nothing merges a comment run by machine; people read it in
thread order. Never post one on an issue whose body is itself continued: its
continuation comments carry the same markers, and every later read would merge
the run's parts into the body. Replace the body there instead, or stop.
