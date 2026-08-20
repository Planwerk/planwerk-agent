# Use the skills

planwerk-agent ships ten Claude Code Skills. Six author the issues the rest
of the pipeline consumes, one settles the decisions a Meta Issue deferred to a
spike, one implements a prepared issue directly in your checkout, one repairs
a pull request whose checks went red, and one rewrites prose that reads
machine-written:

| Skill | What it does |
|-------|--------------|
| `/planwerk:draft` | Turns a rough idea into a ready-to-file issue through a short clarifying conversation |
| `/planwerk:elaborate` | Expands an issue into an engineering plan grounded in the repository |
| `/planwerk:cleanup` | Surveys a codebase for dead and duplicated code, and files a Meta Issue with a phased, evidence-backed cleanup plan |
| `/planwerk:meta` | Splits a Meta Issue into linked, dependency-ordered Sub Issues |
| `/planwerk:decide` | Verifies the decisions a Meta Issue's split deferred to a spike, and folds the outcomes into the Meta Issue and every Sub Issue that assumed one |
| `/planwerk:revisit` | Re-checks a prepared issue against what has actually landed since, and corrects what went stale |
| `/planwerk:clarify` | Answers the open questions that stopped a planning session, and records them in the issue body |
| `/planwerk:implement` | Implements a prepared issue in your checkout — a plan you approve in plan mode, one complete pull request behind your yes, none of the pipeline's passes |
| `/planwerk:fix` | Repairs a pull request's failing CI checks, and asks you at the forks a diagnosis cannot settle |
| `/planwerk:humanize` | Rewrites existing prose to remove the signs of AI writing, preserving every fact |

`draft` and `meta` replace the subcommands of the same names, which were
removed. Each skill needs decisions only a human can make, and a skill can ask
for them mid-run in a way a one-shot subcommand never could.

Three exist both ways. `elaborate` is also the
[`elaborate` command](/reference/cli#elaborate), and `fix` is also the
[`fix` command](/reference/cli#fix), for unattended use in CI. Reach for a
command when nobody is watching — it has to guess where the skill would have
asked. For `implement` the difference is more than supervision: the
[`implement` command](/reference/cli#implement) runs simplify, review, and
verification passes over the result, which the skill deliberately omits — you
approve the plan and read the diff instead.

## Install

The repository is a Claude Code plugin marketplace. Register it once, then
install the plugin:

```bash
claude plugin marketplace add planwerk/planwerk-agent
claude plugin install planwerk@planwerk-agent
```

Restart Claude Code. `/planwerk:draft`, `/planwerk:elaborate`,
`/planwerk:cleanup`, `/planwerk:meta`, `/planwerk:decide`, `/planwerk:revisit`,
`/planwerk:clarify`, `/planwerk:implement`, `/planwerk:fix`, and
`/planwerk:humanize` are now available in any session.

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
succeed. `/planwerk:elaborate`, `/planwerk:decide`, `/planwerk:revisit`, and
`/planwerk:clarify` read the repository, so run them from inside a checkout of
the repo whose issue you are working on. `/planwerk:implement` goes further: it
writes code, so it needs a clean working tree it can branch in, on an
up-to-date default branch. `/planwerk:fix` needs the PR's own head branch
checked out, with a clean working tree. `/planwerk:cleanup` surveys the code
itself, so it always runs from inside a checkout, on an up-to-date default
branch. `/planwerk:draft`
and `/planwerk:meta` only talk to the GitHub API and need no checkout.
`/planwerk:humanize` is the inverse: it works on files in your checkout and
needs no GitHub access at all.

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

## Plan a codebase cleanup

```
/planwerk:cleanup
```

Run it from inside a checkout. The skill surveys the repository for dead code
and duplicated code — read-only detectors already on `PATH` first, hand
searches for what tools miss — and treats every hit as a lead: a finding
reaches the Meta Issue only after the searches that would refute it (the
identifier as a word, as a string literal, across build variants) come back
empty. Verified findings are grouped into compact phases sized one pull
request each, deletions before the consolidations they dissolve. A public
surface is never asserted dead from an internal search alone: those
candidates come to you as questions, and what you leave open lands in an
`## Open decisions` block `/planwerk:decide` can settle later. It files one
Meta Issue on your yes and deletes nothing. See
[Plan a codebase cleanup](/how-to/plan-a-codebase-cleanup).

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

## Settle a Meta Issue's decisions

```
/planwerk:decide owner/repo#767
```

Some Meta Issues defer a block of decisions to a spike when they are split —
each item pairing a recommendation with something nobody has checked yet, and
`/planwerk:meta` already filed a Sub Issue whose job is verifying and recording
the outcomes. The skill verifies every item against the repository and
whatever it names as its own source, puts the genuine judgment calls to you —
largest blast radius first, citing what a sibling Sub Issue already assumes —
and records the outcomes in the Meta Issue's own decisions block. It then
corrects every open sibling whose body cites a decision that turned out
different from what it assumed, minimal diff, and leaves closed siblings and
Sub Issue depth alone. See
[Settle a Meta Issue's decisions](/how-to/settle-meta-issue-decisions).

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

## Clarify it when planning stops

```
/planwerk:clarify owner/repo#42
```

`implement` plans before it writes code, and a planning session that hits a
question it has no authority to answer reports `STATUS: NEEDS_CONTEXT` and aborts
the run. The skill reads that posted plan, and answers from the repository every
question the repository answers — the planner reports what *it* could not settle,
which is not the same as what a human must decide. Only the genuine forks reach
you, largest blast radius first, each one saying which option the plan already
assumed and what the other one invalidates. The answers land in the issue body,
never in the plan. See [Clarify an issue](/how-to/clarify-an-issue).

## Implement it in your checkout

```
/planwerk:implement owner/repo#42
```

For the change small enough that a full `planwerk-agent implement` run costs
more than it catches. The skill reads the issue, enters plan mode, and grounds
a short plan in the actual code: the change set, the change and verification
command behind every Acceptance Criterion, the tests to write. You approve the
plan before anything is edited. It then implements on an
`implement/issue-<N>-<slug>` branch — small commits, regression test first
watched failing, every criterion exercised — and opens one complete pull
request only after you have seen the diff and said yes. It never ships partial
work: what cannot complete stops as `BLOCKED` with the branch left local. See
[Implement an issue interactively](/how-to/implement-an-issue-interactively).

## Fix the pull request when its checks go red

```
/planwerk:fix owner/repo#123
```

Run it from inside a checkout of the PR's head branch; with no argument it
targets the pull request for the branch you are on. The skill reads every failing
check's logs, reproduces the failure with the exact command CI ran, and opens the
file the log cites. Then it asks you the questions a diagnosis cannot answer —
above all whether the production code is wrong or the test encodes behavior
nobody wants any more, a fork where both answers make the check green and only
one is right. It folds the repair into the commit that introduced the bug and
pushes only once you say so. See
[Fix failing checks](/how-to/fix-failing-checks).

## Humanize prose that reads machine-written

```
/planwerk:humanize docs/ README.md
```

The skill rewrites existing documents against a curated catalog of the
patterns that mark text as AI-generated — inflated significance, the AI
vocabulary, forced triads, em dashes, filler — while preserving every fact,
the document's language, and its register. A committed `STYLE_GUIDE.md`
outranks the catalog. It edits your working tree only and never touches
GitHub; you review the result with `git diff`. See
[Humanize documentation](/how-to/humanize-documentation).

## Nothing reaches GitHub without a yes

Every skill reads GitHub freely and writes only once, behind an explicit
confirmation. If you decline, nothing is created, `/planwerk:fix` pushes
nothing, and `/planwerk:implement` leaves its branch local. `/planwerk:humanize` never writes to GitHub at all: it edits files in
your working tree, and confirms the file list first when it inferred one
rather than being given it. If you skip a question, the skill records it as an unresolved decision
in the issue — or, for `fix`, as a concern in its report — rather than quietly
picking an answer.

## One format, every issue skill

The six issue skills share their format specification rather than each restating
it, so an issue is the same shape whichever produced it. That matters because
[`plan`](/reference/cli#implement), [`implement`](/reference/cli#implement), and
[`ship`](/reference/cli#ship) read these issues:

- A draft-depth issue (`draft`, and each Sub Issue from `meta`) carries a
  `**Category**` / `**Scope**` header line, a `## Description`, and a
  `## Motivation`. Nothing more — no file paths, no acceptance criteria.
- An elaborated issue adds `## User Stories` (when the work serves a persona),
  `## Affected Areas`, `## Acceptance Criteria` as `- [ ]` checkboxes,
  `## Non-Goals`, and `## References`.
- The survey Meta Issue `cleanup` files keeps the draft sections and adds
  `## Cleanup phases` — the work-package list `meta` splits — plus, when the
  survey left open calls, an `## Open decisions` block. Its findings name
  files and symbols, pinned to the surveyed commit, because dead code has no
  behavior to describe it by; the Sub Issues split from it stay path-free.
- Every body ends with an attribution footer naming planwerk-agent and the exact
  Claude model that wrote it.

There are exactly these two depths. `elaborate` promotes a draft to a plan;
`revisit` and `clarify` work on an issue at the depth it already has and leave it
there.

The specification lives in `plugins/planwerk/shared/issue-format.md`. A Go test
(`TestBuildIssueBody_MatchesSharedFormat`) fails when the `elaborate` command's
renderer and that document disagree, so the two `elaborate` paths cannot drift.

## Declare the repositories that follow this one

Work in one repository often implies work in another: a service changes its API
surface, and its client or provider has to follow. That second piece needs its
own issue, because a pull request cannot span repositories.

Declare those repositories in `.planwerk/related-repos.md`, beside the other
per-repository conventions (`.planwerk/STYLE_GUIDE.md`,
`.planwerk/review_patterns/`, `.planwerk/out-of-scope/`). One `##` heading per
counterpart, in `owner/repo` form, each with the condition that warrants an issue
there:

```markdown
# Related repositories

## acme/gadgets

When: the change alters the control-plane API surface, the registration
handshake, or the policy wire format.
Not when: the change is confined to storage, scheduling, or the admin UI.

## acme/terraform-provider-acme

When: the change adds or removes a user-facing resource type, or a field on one.
```

`draft`, `meta` and `elaborate` read it. They propose a counterpart only when the
map names the repository *and* the work meets its condition, so a generated
client is left out of the map rather than listed with a caveat. A repository with
no map has no counterparts, and the skills say nothing about them.

Each repository declares its own, so a client can name the service it consumes
for work flowing the other way.

Anything the skills write about another repository carries that repository with
it: `owner/repo#123` for an issue or pull request, `owner/repo@<sha>` for a
commit, a URL for everything else. A bare `#123` resolves against the repository
the text was written into, and a bare sha against whatever the reader is
browsing, so an unqualified reference does not fail — it points somewhere else.
The rule is in `shared/github.md` and applies to issue bodies, comments, commit
messages, and the reports the skills print. The headless `elaborate` and planning
prompts label a Sub Issue and its linked pull requests that way before the
session ever cites them.

## Where the pipeline goes next

`/planwerk:draft` → `/planwerk:elaborate` → `planwerk-agent implement` →
`/planwerk:fix`, with `/planwerk:revisit` before the implement step when the
issue has been sitting long enough for the branch to move under it, and
`/planwerk:clarify` after it when the planning session stopped at
`NEEDS_CONTEXT`. For a change small enough that the unattended pipeline costs
more than it catches, `/planwerk:implement` takes the implement step
interactively, in your own checkout. Or `/planwerk:meta` → `planwerk-agent ship` to drive every Sub
Issue to merged in dependency order. `ship` reads the native sub-issue and
`blocked by` relationships `/planwerk:meta` writes, which is why the skill
records the dependency graph as real GitHub relationships and not as prose.
A cleanup enters that chain one step earlier: `/planwerk:cleanup` surveys the
checkout and files the Meta Issue that `/planwerk:meta` then splits.

When the split produced a decisions or spike Sub Issue, run `/planwerk:decide`
on it before elaborating the siblings that assume its outcomes — an
unverified recommendation is a guess, and a plan built on a guess inherits it.
