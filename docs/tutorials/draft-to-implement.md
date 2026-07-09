# Draft to implement

This tutorial walks one feature idea through the whole pipeline —
`draft → elaborate → implement` — from a one-line thought to a draft pull
request. Each step builds on the issue the previous one produced.

The first two steps are Claude Code Skills, so you run them inside a Claude Code
session. The third is a command you run in a terminal.

You need [Claude Code](https://docs.claude.com/en/docs/claude-code) and the
[`gh` CLI](https://cli.github.com/) installed and authenticated, and write
access to a repository you can file issues and open PRs against. We use
`owner/repo` as a placeholder — substitute your own.

## 0. Install the skills

Once per machine:

```bash
claude plugin marketplace add planwerk/planwerk-agent
claude plugin install planwerk@planwerk-agent
```

Restart Claude Code. See [Use the issue skills](/how-to/use-the-skills) for
details.

## 1. Draft the idea

Capture a rough idea as a clean issue. In a Claude Code session:

```
/planwerk:draft owner/repo add a dark mode toggle to the settings page
```

The skill asks three to five clarifying questions in your own language, drafts
the issue in English, checks the tracker for near-duplicates, and shows you the
rendered body. Confirm, and it files the issue and prints the URL — note its
number, for example `#42`.

Inside a checkout of the target repository you can drop the reference and let the
skill read it from `origin`:

```
/planwerk:draft add a dark mode toggle to the settings page
```

`draft` deliberately stops at an initial description (title, Description,
Motivation, rough Scope). It does not plan the work — that is the next step.

## 2. Elaborate it into a plan

Expand the captured idea into a detailed engineering plan grounded in the actual
repository. Run this from inside a checkout of the repository:

```
/planwerk:elaborate owner/repo#42
```

The skill walks the code first, then asks you about the decisions the plan cannot
make on its own — each one citing the file that raised it. It scores its own draft
for executability, refines until it clears the bar, and asks whether to replace
the issue body. Say yes, and the issue now carries concrete Affected Areas,
Acceptance Criteria, and Non-Goals: the detail an implementer needs to execute
without further questions.

For an unattended run — in CI, or over a batch of issues — use the command
instead, which clones the repository itself:

```bash
planwerk-agent elaborate owner/repo#42 --update-issue --review
```

## 3. Implement it

Hand the elaborated issue to the implement command. It runs a planning session,
implements the change end to end (code, tests, docs) on a feature branch, cleans
the diff up with automatic simplify and review passes, and then opens a draft
pull request linked to the issue:

```bash
planwerk-agent implement owner/repo#42
```

When it finishes, open the draft PR it created, review the diff, and take it
through your normal review process.

## Where to go next

- [Use the issue skills](/how-to/use-the-skills) — installing and updating them.
- [Draft an issue](/how-to/draft-an-issue) — the full `draft` flow.
- [Elaborate an issue](/how-to/elaborate-an-issue) — how the plan is produced,
  as a skill and as a command.
- [Split a Meta Issue](/how-to/split-a-meta-issue) — when the idea is too big for
  one issue.
- [Implement an issue](/how-to/implement-an-issue) — the implement session in
  detail.
- [CLI reference](/reference/cli) — every command and flag.
