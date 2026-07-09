# Draft an issue

Turn a rough, one-line idea into a clean, ready-to-file GitHub issue with the
`/planwerk:draft` skill. It asks a few clarifying questions, drafts a structured
issue (a descriptive title plus Description, Motivation, and a rough Scope),
checks for duplicates, and files it only when you confirm.

`draft` is the front of the pipeline — `draft → elaborate → implement`. It
captures the idea; it does **not** plan the work. Turning the description into a
file-level engineering plan is the separate [`elaborate`](/how-to/elaborate-an-issue)
step.

Install the skills first: see [Use the issue skills](/how-to/use-the-skills).

## Draft from an idea

```
/planwerk:draft owner/repo add a dark mode toggle to the settings page
```

Inside a checkout of the target repository, omit the reference and the skill
reads it from `origin`, stating which repository it resolved:

```
/planwerk:draft add a dark mode toggle to the settings page
```

Omit the idea as well, and the skill asks for it.

## The interactive flow

1. **The target is named.** The skill states the repository it resolved, so a
   wrong one is caught before anything is filed.
2. **Three to five clarifying questions**, numbered, asked in the language you
   wrote the idea in. They probe the problem behind the idea, who benefits, the
   rough scope, and any hard constraint. They never ask about implementation
   details or file layout — that question belongs to `elaborate`, and asking it
   here teaches you to answer the wrong one.
3. **A draft** in the house format: a `**Category**` / `**Scope**` header line,
   `## Description`, `## Motivation`, and an attribution footer naming
   planwerk-agent and the exact Claude model that wrote it.
4. **A duplicate check** against the tracker. When a plausible duplicate turns
   up, the skill shows it and asks whether to file anyway, comment on the
   existing issue instead, or stop. It does not decide that on its own.
5. **A preview and a confirmation.** Nothing is filed until you say yes.

## Answer in your own language

The questions come in whatever language you wrote the idea in, so you can answer
comfortably. The issue itself is always written in English, like every artifact
planwerk-agent produces. Write the idea in German, get German questions and an
English issue.

## What a draft never contains

A draft describes; it does not plan. The skill refuses to write any of these,
because they belong to `elaborate` and would rot in the tracker long before
anyone picked the issue up:

- A file-level affected-areas breakdown.
- A step-by-step implementation design.
- Acceptance criteria grounded in concrete files, symbols, or functions.
- The name of a specific source file or function.

The work is described by its **behavior and the interfaces it touches** instead.
A brief written that way survives the code moving underneath it.

## When you are in a hurry

If an answer leaves the description ambiguous, the skill names what is missing
and asks once more. It stops there. Tell it to skip the questions and it asks the
two that matter most, explains why, then proceeds. Anything you leave unanswered
is recorded as an unresolved decision rather than silently guessed.

## Hand off to elaborate and implement

Once the issue exists, take it through the rest of the pipeline:

```
/planwerk:elaborate owner/repo#42
```

```bash
planwerk-agent implement owner/repo#42
```

See the [Draft to implement](/tutorials/draft-to-implement) tutorial for the full
walkthrough.
