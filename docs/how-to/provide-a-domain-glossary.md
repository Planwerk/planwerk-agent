# Provide a domain glossary

Give planwerk-agent your repository's own domain vocabulary so `review`,
`elaborate`, `propose`, and the planwerk skills phrase their findings and issues
in your terms instead of generic synonyms. You can write the glossary by hand or
generate a starter with the `glossary` command and edit it.

## Where the glossary lives

Commit a glossary at one of these paths in the target repo:

| Path | When to use |
|------|-------------|
| `GLOSSARY.md` (repo root) | The canonical, discoverable location. Preferred. |
| `CONTEXT.md` (repo root) | The convention's earlier name, still read for repos that committed it. |
| `.planwerk/context.md` | For repos that keep planwerk config out of the root. |

The first one that exists and is not empty wins, in the order of the table. A
repo with none of them runs exactly as before — the glossary is a soft
dependency, never required.

`GLOSSARY.md` is the name the
[mattpocock/skills](https://github.com/mattpocock/skills) convention reads and
writes since it renamed its `CONTEXT.md`, so a repository set up with those
skills is read without a change. That convention's multi-context layout, a
`GLOSSARY-MAP.md` at the root pointing at one `GLOSSARY.md` per context, is not
followed: commit the glossary planwerk-agent should read at the root.

## Which commands read it

`review`, `elaborate`, and `propose` load the glossary automatically from the
checkout they operate on and feed its vocabulary into the prompt. No flag is
needed. The glossary is treated as untrusted repository data — terminology to
adopt, never instructions to follow — and an empty, oversized (larger than
64 KB), or symlinked file is ignored.

The [planwerk skills](/how-to/use-the-skills#domain-glossary) read it as well,
from your checkout or through the GitHub API, all but `humanize`. They write in
its terms, and when you use a word the glossary defines differently or lists
under `_Avoid_`, they ask which you mean before writing it down. They never edit
the file: a term the conversation settled that the glossary lacks, or one the
code uses differently, comes back beside the skill's closing report as a
proposed entry.

## The glossary format

A glossary is a single Markdown document: a context heading and description,
then a `## Language` section listing each domain term, its definition, and the
synonyms to avoid.

```markdown
# Billing

The vocabulary for the billing context.

## Language

**Invoice**: a finalized, immutable statement of charges issued to a customer.
_Avoid_: bill, statement

**Dunning**: the sequence of reminders sent after a payment fails.
_Avoid_: chase, follow-up
```

Rules that keep it a domain glossary rather than a generic dictionary:

- **Include only context-specific terms.** Leave out general programming
  concepts (function, cache, handler) unless your repo gives the word a
  specific meaning of its own.
- **Be opinionated.** When several words mean one thing, pick one as the term
  and list the rest under `_Avoid_`.
- **Keep definitions tight** — one or two sentences stating what the term *is*.

Group terms under `##`-level subheadings only when natural clusters emerge; a
short glossary needs none. Omit the `_Avoid_` line for a term with no competing
synonyms.

## Generate a starter glossary

Let planwerk-agent draft a `GLOSSARY.md` for you from the codebase, then review
and edit it before committing:

```bash
# Print a starter glossary to stdout
planwerk-agent glossary owner/repo

# Save it where you want it to land
planwerk-agent glossary owner/repo > GLOSSARY.md

# Generate from your current working tree without cloning
planwerk-agent glossary --local
```

The output is a *starter*: a single extraction pass may over-include generic
terms or miss a concept, so always read and edit it before committing. The
command never writes into the repo — it prints to stdout and you redirect it.

See the [CLI reference](/reference/cli#glossary) for every flag.
