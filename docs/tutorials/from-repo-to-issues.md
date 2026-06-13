# From repo to GitHub issues

This tutorial walks you through turning an entire repository into a set of
tracked GitHub issues: you will analyze the repo with `propose`, review the
generated proposals, and create issues for the ones worth pursuing.

This builds on [Getting started](/tutorials/getting-started) — make sure
planwerk-review is installed and `gh` is authenticated before you begin.

## Step 1: Generate proposals

Run `propose` against the repository. Use the full URL or the short
`owner/repo` form:

```bash
planwerk-review propose owner/repo
```

planwerk-review clones the repo, runs a deep Claude analysis covering
architecture, code quality, feature gaps, DX, performance, security, testing,
and CI/CD, and then renders structured proposals as Markdown on `stdout`. Each
proposal carries a priority, a category, a scope, and acceptance criteria.

## Step 2: Review the proposals

Read the Markdown output to decide which proposals are worth tracking. To keep
them for review, redirect to a file:

```bash
planwerk-review propose owner/repo > proposals.md
```

## Step 3: Create issues interactively

When you are ready to track the proposals you picked, re-run with
`--create-issues`. planwerk-review shows a summary table and walks you through
each proposal with a prompt to create a GitHub issue via `gh`:

```bash
planwerk-review propose --create-issues owner/repo
```

Proposals whose title already matches an existing GitHub issue are dropped
automatically, so re-running is idempotent — see
[existing-issue dedupe](/how-to/analyze-a-repository#existing-issue-dedupe) for
how matching works and how to disable it.

## Next steps

- Expand a created issue into a full engineering plan —
  see [Elaborate an issue](/how-to/elaborate-an-issue).
- Generate a copy-paste prompt to fix or implement an issue —
  see [Generate a fix/implement prompt](/how-to/generate-a-prompt).
