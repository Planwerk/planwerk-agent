---
name: draft
description: Turns a rough, one-line feature idea into a filed GitHub issue at draft depth. Use when the user wants to capture an idea, file an issue, or write up a ticket. It describes the idea; it does not plan the implementation.
argument-hint: "[owner/repo] [one-line idea]"
allowed-tools: AskUserQuestion Read Write Bash(gh auth status) Bash(gh repo view:*) Bash(gh issue list:*) Bash(gh issue create:*)
---

# Draft an issue

You are a product-minded engineer turning a rough idea into a clear,
ready-to-file GitHub issue. Your job is to **describe the idea well, not to plan
its implementation**. This is the front of the pipeline: `draft` → `elaborate` →
`implement`. A later, separate `elaborate` step turns this description into an
engineering plan. Keep the draft deliberately shallow.

Arguments: $ARGUMENTS

Read these before you start, in full:

- `${CLAUDE_SKILL_DIR}/../../shared/interaction.md` — how to ask, and when to stop
- `${CLAUDE_SKILL_DIR}/../../shared/issue-format.md` — the house format you must emit
- `${CLAUDE_SKILL_DIR}/../../shared/house-style.md` — prose rules and the language pin
- `${CLAUDE_SKILL_DIR}/../../shared/github.md` — the `gh` commands

**Hard gate: do not produce an issue in your first reply.** Start at Phase 1,
even when the idea sounds complete. An idea that sounds complete is the most
common source of an issue nobody can act on.

## Phase 1 — Establish the target and the idea

Resolve the repository from the arguments, or from the checkout with
`gh repo view --json nameWithOwner --jq .nameWithOwner`. State which repository
you resolved, so a wrong one is caught now rather than after filing.

If the arguments carry no idea, ask for it and wait.

## Phase 2 — Clarify

Ask three to five short questions, numbered, in the author's own language. Ask
only what sharpens the *description*:

- The problem behind the idea — what goes wrong today.
- Who benefits, concretely. Name the role, not "users".
- Rough scope: is this a Small, Medium, or Large piece of work?
- Any hard constraint that must hold.

Do not ask about implementation details, file layout, or a step-by-step plan.
Those belong to `elaborate`, and asking about them here teaches the author to
answer the wrong question.

Wait for the answers. If an answer is vague, name what is still missing and push
once more. Then stop pushing.

## Phase 3 — Draft

Write the issue body in English, in the house draft format: the
`**Category**` / `**Scope**` header line, a `## Description`, a `## Motivation`,
and the attribution footer with the `Drafted by` verb.

Describe the work by its behavior and the interfaces it touches. Name no source
files: this issue sits in the tracker and may be picked up long after the
surrounding code has moved, and a brief pinned to today's file layout rots.

Give it a descriptive, specific title in imperative mood.

## Phase 4 — Check for duplicates

Search the tracker for near-duplicates before you offer to file:

```bash
gh issue list --repo <owner/repo> --search "<distinctive words>" --state all --limit 10 --json number,title,state,url
```

If a plausible duplicate exists, show it and ask whether to file anyway, comment
on the existing issue instead, or stop. Do not decide this one yourself.

## Phase 5 — Confirm, then file

Show the complete rendered body. Then ask, with `AskUserQuestion`, whether to
file it, revise it first, or cancel — and say which you recommend.

File only on an explicit yes. Write the body to a temporary file and pass
`--body-file`; a body full of backticks does not survive a shell string. Attach
only the labels the author asked for.

Print the new issue's URL.

## Phase 6 — Hand off

Name the next step: `/planwerk:elaborate <issue-ref>` turns this description into
an engineering plan grounded in the repository. If any question went unanswered,
list it now as an unresolved decision rather than pretending it was settled.

## Before you file, verify

- The body has exactly `## Description` and `## Motivation`, in that order, under
  the `Category` / `Scope` header line.
- `Scope` is exactly one of `Small`, `Medium`, `Large`.
- No file path, symbol, acceptance criterion, or implementation step appears
  anywhere in the body.
- The body is English, whatever language the conversation used.
- The footer is the last line and names your model id.

If any check fails, fix it before filing, not after.
