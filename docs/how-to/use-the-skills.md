# Use the issue skills

planwerk-agent ships four Claude Code Skills that author the issues the rest of
the pipeline consumes:

| Skill | What it does |
|-------|--------------|
| `/planwerk:draft` | Turns a rough idea into a ready-to-file issue through a short clarifying conversation |
| `/planwerk:elaborate` | Expands an issue into an engineering plan grounded in the repository |
| `/planwerk:meta` | Splits a Meta Issue into linked, dependency-ordered Sub Issues |
| `/planwerk:revisit` | Re-checks a prepared issue against what has actually landed since, and corrects what went stale |

They replace the `draft` and `meta` subcommands, which were removed. Each one
needs decisions only a human can make, and a skill can ask for them mid-run in a
way a one-shot subcommand never could.

`elaborate` exists both ways: as this skill, and as the
[`elaborate` command](/reference/cli#elaborate) for unattended use in CI. Both
render the same [issue format](#one-format-every-skill).

## Install

The repository is a Claude Code plugin marketplace. Register it once, then
install the plugin:

```bash
claude plugin marketplace add planwerk/planwerk-agent
claude plugin install planwerk@planwerk-agent
```

Restart Claude Code. `/planwerk:draft`, `/planwerk:elaborate`, `/planwerk:meta`,
and `/planwerk:revisit` are now available in any session.

To update after a new release:

```bash
claude plugin update planwerk
```

To develop against a local checkout instead of GitHub, point the marketplace at
the directory:

```bash
claude plugin marketplace add /path/to/planwerk-agent
claude plugin install planwerk@planwerk-agent
```

Confirm what got installed with `claude plugin details planwerk`.

## Prerequisites

The skills call the [`gh` CLI](https://cli.github.com/), so `gh auth status` must
succeed. `/planwerk:elaborate` and `/planwerk:revisit` read the repository, so run
them from inside a checkout of the repo whose issue you are working on.
`/planwerk:draft` and `/planwerk:meta` only talk to the GitHub API and need no
checkout.

## Draft an idea

```
/planwerk:draft owner/repo add a dark mode toggle to the settings page
```

The skill resolves the target repository, asks three to five clarifying
questions in your own language, drafts the issue in English, checks the tracker
for near-duplicates, shows you the rendered body, and files it only after you say
so. See [Draft an issue](/how-to/draft-an-issue).

## Elaborate it into a plan

```
/planwerk:elaborate owner/repo#42
```

The skill reads the issue and its Meta/Sub-Issue neighborhood, walks the
repository, and then asks you about the decisions the plan cannot make on its
own — each one grounded in a concrete file it just read. It scores its own draft
for executability and refines until the score clears 8, then asks whether to
replace the issue body or post a comment. See
[Elaborate an issue](/how-to/elaborate-an-issue).

## Split a Meta Issue

```
/planwerk:meta owner/repo#113
```

The skill decides the breakdown itself, then verifies it — coverage against the
Meta Issue's own work-package list, an acyclic dependency graph, vertical slices,
draft depth — and shows you the result before filing anything. On your approval it
files each Sub Issue, links it natively under the Meta Issue, records the
`blocked by` dependencies, and back-fills the Meta Issue body with the new issue
references. See [Split a Meta Issue](/how-to/split-a-meta-issue).

## Revisit it once it has aged

```
/planwerk:revisit owner/repo#42
```

An issue is planned against a snapshot: the files that existed, the siblings that
had not landed. The skill re-checks every claim the issue makes against the
current default branch, and re-checks a Sub Issue's scope against what its
closed siblings actually **merged** rather than what their bodies promised. It
returns one of four verdicts — Current, Stale, Re-scoped, or Obsolete — and
corrects the body under a minimal-diff rule: every changed line traces to a check
that failed, and you approve a diff rather than a rewritten body. It never
changes the issue's depth, and never closes anything. See
[Revisit an issue](/how-to/revisit-an-issue).

## Nothing reaches GitHub without a yes

Every skill reads GitHub freely and writes only once, behind an explicit
confirmation. If you decline, nothing is created. If you skip a question, the
skill records it as an unresolved decision in the issue rather than quietly
picking an answer.

## One format, every skill

The skills share their format specification rather than each restating it, so an
issue is the same shape whichever produced it. That matters because
[`plan`](/reference/cli#implement), [`implement`](/reference/cli#implement), and
[`ship`](/reference/cli#ship) read these issues:

- A draft-depth issue (`draft`, and each Sub Issue from `meta`) carries a
  `**Category**` / `**Scope**` header line, a `## Description`, and a
  `## Motivation`. Nothing more — no file paths, no acceptance criteria.
- An elaborated issue adds `## User Stories` (when the work serves a persona),
  `## Affected Areas`, `## Acceptance Criteria` as `- [ ]` checkboxes,
  `## Non-Goals`, and `## References`.
- Every body ends with an attribution footer naming planwerk-agent and the exact
  Claude model that wrote it.

There are exactly these two depths. `elaborate` promotes a draft to a plan;
`revisit` re-checks an issue at the depth it already has and leaves it there.

The specification lives in `plugins/planwerk/shared/issue-format.md`. A Go test
(`TestBuildIssueBody_MatchesSharedFormat`) fails when the `elaborate` command's
renderer and that document disagree, so the two `elaborate` paths cannot drift.

## Where the pipeline goes next

`/planwerk:draft` → `/planwerk:elaborate` → `planwerk-agent implement`, with
`/planwerk:revisit` before the last step when the issue has been sitting long
enough for the branch to move under it. Or `/planwerk:meta` →
`planwerk-agent ship` to drive every Sub Issue to merged in
dependency order. `ship` reads the native sub-issue and `blocked by`
relationships `/planwerk:meta` writes, which is why the skill records the
dependency graph as real GitHub relationships and not as prose.
