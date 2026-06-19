# Draft to implement

This tutorial walks one feature idea through the whole pipeline —
`draft → elaborate → implement` — from a one-line thought to a draft pull
request. Each step builds on the issue the previous one produced.

You need [Claude Code](https://docs.claude.com/en/docs/claude-code) and the
[`gh` CLI](https://cli.github.com/) installed and authenticated, and write
access to a repository you can file issues and open PRs against. We use
`owner/repo` as a placeholder — substitute your own.

## 1. Draft the idea

Capture a rough idea as a clean issue. `draft` asks a few clarifying questions,
then drafts a structured issue and files it on your confirmation:

```bash
planwerk-review draft owner/repo "add a dark mode toggle to the settings page"
```

Answer the clarifying questions, review the preview, and confirm with `y`. The
command prints the new issue URL — note its number, for example `#42`.

If you are sitting inside a checkout of the target repository, you can skip the
repo-ref and let `draft` read it from `origin`:

```bash
planwerk-review draft --local "add a dark mode toggle to the settings page"
```

`draft` deliberately stops at an initial description (title, Description,
Motivation, rough Scope). It does not plan the work — that is the next step.

## 2. Elaborate it into a plan

Expand the captured idea into a detailed engineering plan grounded in the actual
repository, and write the plan back onto the issue:

```bash
planwerk-review elaborate owner/repo#42 --update-issue
```

This clones the repo, walks the code, and rewrites the issue body with concrete
Affected Areas, Acceptance Criteria, and Non-Goals — the detail an implementer
needs to execute without further questions.

## 3. Implement it

Hand the elaborated issue to the implement command. It runs a planning session,
implements the change end to end (code, tests, docs) on a feature branch, cleans
the diff up with automatic simplify and review passes, and then opens a draft
pull request linked to the issue:

```bash
planwerk-review implement owner/repo#42
```

When it finishes, open the draft PR it created, review the diff, and take it
through your normal review process.

## Where to go next

- [Draft an issue](/how-to/draft-an-issue) — the full `draft` flow and flags.
- [Elaborate an issue](/how-to/elaborate-an-issue) — how the plan is produced.
- [Implement an issue](/how-to/implement-an-issue) — the implement session in
  detail.
- [CLI reference](/reference/cli) — every command and flag.
