# Elaborate an issue

Take a high-level GitHub issue (typically the output of `draft`, `propose`, or
`audit`) and expand it into a deeply detailed engineering plan grounded in the
actual repository state — the kind of issue body a senior engineer can pick up and
execute without further clarification: a Description with concrete "already
exists / this issue adds" boundaries, a Motivation, an optional User Stories
section, Affected Areas, Acceptance Criteria, Non-Goals, and References.

`elaborate` comes two ways. Both render the same issue body.

## The skill: `/planwerk:elaborate`

Use this when a human is at the keyboard. It is the better choice by default,
because elaboration turns on decisions the issue never made, and the skill asks
you about them instead of guessing.

```
/planwerk:elaborate owner/repo#123
```

Run it from inside a checkout of the issue's repository. The skill reads the
issue and its Meta/Sub-Issue neighborhood, **walks the repository before it asks
you anything**, and then surfaces only the decisions that would otherwise become
guesses — each grounded in the concrete `path:line` that raised it. A question it
can answer by reading the code, it does not ask.

It then writes the plan, scores its own draft for executability, refines until
the score clears 8, and asks whether to replace the issue body or post a comment.
Nothing is written to GitHub until you say so. A question you decline to answer
is recorded in the issue under Non-Goals or as an explicit assumption, never
resolved silently.

Install it first: see [Use the issue skills](/how-to/use-the-skills).

## The command: `planwerk-agent elaborate`

Use this for unattended runs — CI, scripts, batch elaboration — where there is
nobody to ask. It clones the repository itself, so it needs no checkout.

```bash
# Render the elaborated body to stdout
planwerk-agent elaborate https://github.com/owner/repo/issues/123

# Short form
planwerk-agent elaborate owner/repo#123

# JSON for automation
planwerk-agent elaborate --format json owner/repo#123

# Replace the issue body with the elaborated body
planwerk-agent elaborate --update-issue owner/repo#123

# Or post the elaboration as a new comment instead
planwerk-agent elaborate --post-comment owner/repo#123
```

`--update-issue` and `--post-comment` are mutually exclusive — pick the one that
matches your team's workflow (overwrite the source issue vs. preserve history
and append a follow-up comment). See the
[CLI reference](/reference/cli#elaborate) for every flag.

Where the skill asks you, the command records the ambiguity in Non-Goals and
plans the smallest change that satisfies the issue. That is the trade: the
command never blocks, and never gets an answer it could not derive.

## How the command works

1. **Issue Input**: The tool receives a GitHub issue reference (URL or `owner/repo#number`).
2. **Fetch Issue**: Title, body, URL, and state are fetched via `gh issue view`.
3. **Fetch Relations**: When the issue is a **Sub Issue** of a Meta Issue, the Meta Issue and the other Sub Issues are fetched via the GitHub GraphQL API (best-effort — a repo without sub-issue links, a missing token scope, or an older GitHub Enterprise Server degrades to "no relations" without failing the run). See [Sub Issues are elaborated against their Meta Issue](#sub-issues-are-elaborated-against-their-meta-issue) below.
4. **Cache Check**: The default-branch HEAD SHA is resolved via `gh api graphql`. The cache key combines repo + HEAD + issue number + a fingerprint of the issue body — plus, when the issue is a Sub Issue, a fingerprint of the Meta Issue and sibling Sub Issues — so the cache invalidates automatically when the repo, the issue, the Meta Issue, or any sibling is edited.
5. **Clone**: On a cache miss, the repository is cloned locally.
6. **Pattern Load**: The same pattern catalog used by `review` / `audit` / `propose` is loaded, filtered by detected technologies.
7. **Claude Elaboration**: Claude is instructed to walk the repo first, identify what already exists vs. what the issue adds, and emit a detailed plan in six core sections (Description with concrete "already exists / this story adds" boundaries, Motivation, Affected Areas, Acceptance Criteria, Non-Goals, References), plus an optional **User Stories** section between Motivation and Affected Areas that groups the acceptance criteria under `As a {role}, I want {want}, so that {so_that}` stories. User Stories are proportional — emitted only when the issue serves a distinct persona and omitted entirely for purely mechanical or infrastructure work (dependency bumps, formatter sweeps, CI fixes), never padded with a synthetic "As a developer" story. For a Sub Issue, the Meta Issue and sibling Sub Issues from step 3 are injected so the elaboration covers only this issue's slice and defers adjacent parts to the sibling that owns them.
8. **Structuring**: A second Claude call converts the elaboration into a strict JSON schema so the final body renders consistently.
9. **Output**: The elaborated body is rendered as Markdown (default) or JSON. With `--update-issue`, the issue body is overwritten; with `--post-comment`, the elaboration is posted as a new comment.

## Sub Issues are elaborated against their Meta Issue

When the issue is a **Sub Issue** created by [`meta`](/how-to/split-a-meta-issue)
(or linked through GitHub's native sub-issue relationship), `elaborate` reads the
**Meta Issue** and the **other Sub Issues** alongside it and injects them into the
prompt as a *Meta / Sub-Issue Context* section. The elaboration is then told to:

- plan only this Sub Issue's slice of the larger effort and honor the Meta
  Issue's framing rather than re-deciding it;
- avoid duplicating work a sibling Sub Issue owns; and
- when this Sub Issue intentionally implements only part of a shared task because
  the remaining part lands in another Sub Issue, scope it to its part and
  cross-reference the sibling that carries the rest (e.g. *"the remaining X is
  handled by #K"*), recording the deferral under Non-Goals.

A closed sibling is treated as already-implemented context to build on; an open
one as work that may land in parallel. This is automatic — there is no flag — and
best-effort: an issue that is not a Sub Issue, or a repo where the relationship
cannot be read, elaborates exactly as before.

## The issue body keeps its header

An elaboration replaces the whole issue body, so it carries the source issue's
`**Category**: … | **Scope**: …` header line through and corrects the `Scope`
when the plan changed the size. An issue that never had that line renders without
one. Both the skill and the command do this, and a Go test
(`TestBuildIssueBody_MatchesSharedFormat`) fails when the two paths disagree
about the format.

## Score the draft before output (`--review`)

`--review` adds a reviewer pass between elaboration and output. A reviewer
scores the draft from 0 to 10 for executability — a 10 is a plan a zero-context
implementer executes without asking a single question. While the score stays
below the bar, the refine loop revises the draft to close the reviewer's gaps
and iterates until the score clears the bar or `--max-review-iterations` is
exhausted (default 3).

The skill always runs this pass; on the command it is opt-in.

The final score is surfaced in the output as `Executability score: N/10`, so a
near-miss is visible rather than hidden behind a binary pass/fail. When the loop
runs out of iterations below the bar, the surviving gaps and a "what a 10/10
plan would look like" target are rendered alongside the score under **Reviewer
Notes (unresolved)** — address them before implementing.
