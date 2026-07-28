# Humanize documentation

Rewrite prose that reads machine-written — a README, docs pages, code comments,
an issue body draft — so it reads like a person wrote it, without changing a
single fact.

```
/planwerk:humanize docs/ README.md
```

Run it from inside a checkout. With no arguments it offers the prose files your
current branch touches. `humanize` is a skill only; there is no
`planwerk-agent humanize` command, and the skill never writes to GitHub — it
edits files in your working tree, and you review the result with `git diff`.

## What it removes

The skill edits against a curated catalog of the patterns that mark text as
AI-generated: inflated significance ("marks a pivotal moment"), the AI
vocabulary ("delve", "tapestry", "pivotal"), copula avoidance ("serves as"
where "is" is meant), forced groups of three, negative parallelisms, em
dashes, filler, hedging stacks, and generic upbeat conclusions — some thirty
patterns in all.

The catalog lives at `plugins/planwerk/shared/humanizer.md`, adapted from
[blader/humanizer](https://github.com/blader/humanizer) (MIT), itself based on
Wikipedia's "Signs of AI writing" guide maintained by WikiProject AI Cleanup.

## What it never changes

- **Facts.** Every name, number, date, and citation survives the rewrite, and
  nothing that is not in the source appears in it. The skill rewrites form,
  never meaning.
- **Code.** Code blocks, identifiers, frontmatter, data tables, and link
  targets stay byte-identical. In source files, only comments and docstrings
  are touched, and only when you name the file.
- **Language and register.** A German README stays German; a formal reference
  page stays formal.

It also knows what *not* to flag: the catalog's detection guidance looks for
clusters of tells, so prose that is merely dry, formal, or unusual is left
alone. A rewrite that flattens a human's voice is a defect.

## The repository's style guide wins

Before rewriting, the skill looks for a committed `STYLE_GUIDE.md` (repo root,
`.planwerk/`, `docs/`, or `.github/` — the same order the
[mutating sessions](/how-to/implement-an-issue) use). Where the guide
contradicts the catalog — it mandates em dashes, title-cased headings, emoji —
the guide wins, and the skill says which rules it suspended.

## The same rules bind generation

The catalog is not only a repair tool. Its compact form is in force wherever
planwerk writes prose in the first place:

- `house-style.md` carries a "Signs of AI writing" section every
  artifact-writing skill reads, so drafted and elaborated issues avoid the
  patterns from the start.
- The unattended sessions — `implement` in both variants, its worker
  subagents, `fix`, and `address` — carry the same compact rules in their
  prompts for every piece of documentation they write into a target
  repository.

A Go test (`TestSharedHumanizerDocMatchesPromptBlocks`) fails when the full
catalog, the house-style section, and the prompt blocks stop agreeing, so the
three surfaces cannot drift. `humanize` is for the prose that predates these
rules, or that arrived from somewhere else.

## Where it sits in the pipeline

Nowhere, and that is the point: it is the one skill with no pipeline position.
Reach for it when a document reads machine-written, whoever wrote it —
before publishing generated documentation, after inheriting a repo full of
LLM-drafted pages, or on a single README that never sounded right.
