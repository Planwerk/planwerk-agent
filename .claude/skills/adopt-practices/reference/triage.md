# Triage: from the inventory to reading groups

A source repository arrives as a list of text files, most of which hold no
practice at all. One `practice-extractor` reads one group in full, so the
triage decides what an analysis reads, and the ledger records it, so that two
readings of the same repository are comparable.

## Roles

Each group has one role, and the role tells its extractor how to read it.

- **guidance**: prose that recommends. Best-practice documents, tips, reports,
  the tips in a README, the rules in a CLAUDE.md. Extracted as practices.
- **artifact**: what the source runs. Skills with their reference files,
  agents, commands, hooks with their scripts, settings, rules, plugin
  manifests, workflow directories. Read as patterns: the practice a file
  embodies (a gate, a frontmatter field, a tool grant, an orchestration shape,
  a hook event) whether or not a document states it. An example built to show
  a mechanism contributes the mechanism, not its domain.
- **mirror**: a restatement of a vendor page, such as a table of frontmatter
  fields, settings, flags, or hook events with a link to the vendor's
  documentation. The vendor page is the authority, so record its URL. A
  mirror is read only for what it adds beyond the page (version notes, worked
  examples), and only when the author picks it.
- **secondary**: long-form material such as talk and podcast transcripts,
  slide notes, and learning journeys. Dense in context, thin in practices; a
  practice the guidance cites from it is read there through its provenance.
  Recommended off unless the source has little else.
- **skip**: content without a practice for this repository. Generated content
  (changelogs and logs a workflow of the source writes), installation
  tutorials for the source itself, marketing, sponsor and star-history
  sections, presentations, and anything about a product other than Claude Code
  unless a practice transfers. Each skipped set carries one reason.

The inventory's title column, the source's own map, and the first lines of a
file usually settle the role; read further only when they do not.

## Authority

Every practice carries the strongest authority one of its provenance lines
supports:

- `vendor-docs`: the source states it as what a vendor page says and links the
  page.
- `vendor-staff`: a statement by someone who builds Claude Code at Anthropic,
  with the link the source gives (a post, a talk).
- `community`: a named practitioner other than the source's author, with a
  link.
- `author`: the source's own recommendation, or the pattern its own artifact
  embodies.

Authority says where a practice comes from, not whether it is right. The
mappers settle every practice on the evidence in this repository whatever its
authority; the authority decides what a proposal needs before it may be built
in (SKILL.md step 5).

## Groups

A group is what one extractor reads in full: up to about 150 KB of text, from
files of one role and, as far as possible, of one topic, so the extractor's
keys stay comparable. A single file over the limit is a group of its own and
is read in parts. Artifacts are grouped by what they are (skills with their
files, agents, commands, hooks with their scripts and the settings that
register them), and a workflow directory that holds its own agents, commands,
and documentation stays one group. Small groups of one role merge. Name each
group after its content (`tips`, `config-and-hooks`), never by a number; the
ledger and the next analysis use the name.

## The example

`shanraisshan/claude-code-best-practice` at `64bcf8a` (2026-10-27): 454 files,
132 of them text, about 4.2 MB, MIT.

| group | role | files | KB |
|---|---|---|---|
| readme-and-memory | guidance | `README.md` (concepts, 83 tips with their sources), `CLAUDE.md`, `.claude/rules/markdown-docs.md` | 83 |
| tips | guidance | `tips/`: posts by the vendor's staff, expanded | 66 |
| reports | guidance | `reports/` | 127 |
| workflows | artifact | `development-workflows/` (RPI with its agents and commands, cross-model), `orchestration-workflow/`, `agent-teams/`, `implementation/` | 126 |
| maintenance-workflows | artifact | `.claude/commands/workflows/`, `.claude/agents/workflows/`: agents that check the source's mirrors against the vendor pages and changelog | 105 |
| config-and-hooks | artifact | `.claude/` agents, commands, and skills outside the two sets above and the presentation ones, `.claude/settings.json`, `.mcp.json`, `.claude/agent-memory/`, `.claude/hooks/`, `.codex/` | 115 |
| best-practice | mirror | `best-practice/` except settings: subagents, commands, skills, memory, MCP, CLI flags | 76 |
| best-practice-settings | mirror | `best-practice/claude-settings.md` alone | 164 |
| videos | secondary | `videos/`: eight talk and podcast transcripts, five groups | 644 |

Skipped: `changelog/` (2.5 MB, the findings log the maintenance workflows
write), the presentation agents, skills, rule, and transcript (slides for the
author's talks), `tutorial/` (installing Claude Code), `.github/FUNDING.yml`,
`LICENSE`, and `.codex/config.toml`.

The six groups above the mirrors are the recommended selection: six
extractors, then one mapper per slice the practices reach.
