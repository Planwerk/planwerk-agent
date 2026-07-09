# Clarify an issue

Take an issue whose planning session stopped at `NEEDS_CONTEXT`, answer the
questions that stopped it, and record the answers in the body so the next
planning session runs to `PLAN_READY`.

```
/planwerk:clarify owner/repo#42
```

Run it from inside a checkout of the issue's repository. `clarify` is a skill
only; there is no `planwerk-agent clarify` command.

## Why an issue stalls

[`implement`](/how-to/implement-an-issue) plans before it writes code. When the
planning session finds a question it has no authority to answer — a design fork
the issue never named, scope the issue implies but never states — it records the
question under `### Risks & Open Questions`, sets `STATUS: NEEDS_CONTEXT`, and
the run aborts before a line is written:

```
the implementation plan already posted on issue #42 reported NEEDS_CONTEXT;
review the plan above and clarify the issue, then rerun with --no-plan-reuse to
plan afresh
```

That message names a step that had no tool behind it. The plan is posted on the
issue, often thousands of words long, with the two questions that actually block
the work buried among risks that need no answer at all. `clarify` is that step.

## Most open questions are not questions

`NEEDS_CONTEXT` reports what a planning session could not settle inside its own
budget. It is not a finding that a human must decide, and the difference is the
whole point of the skill.

So `clarify` answers first and asks second. It reads the plan's own
`### Ground-Truth Notes`, verifies the notes it intends to lean on, opens the
files at HEAD, and checks whether the Meta Issue already made the decision. Every
question lands in exactly one bucket:

| Bucket | What it means | Who settles it |
|--------|---------------|----------------|
| **Answered** | The repository, the Meta Issue, or the body settles it | The skill, citing `path:line` |
| **Decision** | Two or more defensible options, and the choice changes what gets built | You |
| **Beyond the repository** | It turns on infrastructure, policy, or cost | You, as an open question |

The planner's own `OPEN QUESTION` marker is treated as a guess, not a verdict. An
entry carrying it that the repository answers is answered, and a risk carrying no
marker may still hide a decision. `clarify` never files a question as a Decision
without first opening the files that would have answered it.

## What a decision looks like when it reaches you

One decision per question, with a recommendation, each option carrying a concrete
upside and an honest downside — the same doctrine every planwerk skill asks under.
Two things only `clarify` can add:

- **What the plan already assumed.** A plan that stops at `NEEDS_CONTEXT` has
  usually still written its change set against one of the options. Knowing that
  "the change set and commit 5 assume (a)" tells you what (b) costs.
- **What the other option invalidates** — the change-set entries, commits, and
  fixtures that stop being true.

Decisions arrive largest blast radius first, because an answer that discards half
the plan makes the smaller questions moot.

## The answers go in the body, not the plan

An answer recorded in the plan comment is an answer the next planning session
never reads: the body is the planner's input, the comment is its output. So each
answer lands in the section that holds its kind of fact — a decision that adds
work becomes an `## Affected Areas` entry and an acceptance criterion, one that
forbids work becomes a `## Non-Goals` bullet, and a confirmed reading of an
ambiguous sentence rewrites that sentence rather than annotating it.

That is the elaborated case. A **draft-depth** issue has only a Description and a
Motivation, and the [issue format](/how-to/use-the-skills#one-format-every-skill)
forbids it from naming a source file. This is not a corner case: every Sub Issue
`/planwerk:meta` files is draft depth, and [`ship`](/how-to/ship-a-meta-issue)
plans them exactly as they stand, so a draft-depth `NEEDS_CONTEXT` is the ordinary
one. There an answer is recorded the way a draft records everything — as behavior
and the interfaces it touches. "The service exposes its retry budget as a
configurable setting" is a draft-depth answer; "add `RetryBudget` to
`internal/api/config.go`" is not. When an answer cannot be stated without naming a
file, the skill says so and offers `/planwerk:elaborate` instead of quietly
deepening the issue.

A question the repository answered records nothing. Research is not
underspecification. The exception is a question the planner raised only because
the Description was silent or wrong: that fact goes into the Description, so the
next planning session does not raise it twice.

As with [`revisit`](/how-to/revisit-an-issue), every changed line must trace to a
named answered question, and what you approve is a diff rather than a rewritten
body. Unlike `revisit`, `clarify` may **grow** an issue — but only by what the
option you chose requires.

## It never touches the plan

The posted plan still reports `NEEDS_CONTEXT` after a successful `clarify`, and
that is deliberate. A decision that changed the change set makes the plan wrong,
not ready. Hand-flipping its `STATUS:` line to `PLAN_READY` would ship a plan
whose change set contradicts the decision you just made — precisely the failure
the gate exists to prevent.

A corrected body earns a fresh plan. The handoff is the command the abort message
already names:

```
planwerk-agent implement owner/repo#42 --no-plan-reuse
```

Without `--no-plan-reuse`, `implement` finds the stale plan comment, re-checks its
status, and aborts exactly as before.

## What it will not do

- **It never answers a `BLOCKED` plan.** `BLOCKED` means the issue is wrong — a
  cited file does not exist, a criterion is unreachable — and no answer from you
  repairs that. Use [`/planwerk:revisit`](/how-to/revisit-an-issue) when the code
  moved under the issue, or
  [`/planwerk:elaborate`](/how-to/elaborate-an-issue) when the plan itself is wrong.
- **It never changes an issue's depth**, and it never plans, implements, or
  closes. When an answer cannot be stated without naming a source file, a
  draft-depth issue cannot hold it — the skill says so and offers
  [`/planwerk:elaborate`](/how-to/elaborate-an-issue) rather than smuggling a path
  into a draft.

If you skip a decision, it is recorded in the body as an explicit assumption
naming the part of the plan that now rests on it — never quietly resolved to the
recommendation.

## Where it sits in the pipeline

`/planwerk:elaborate` → `planwerk-agent implement` → (`NEEDS_CONTEXT`) →
`/planwerk:clarify` → `planwerk-agent implement --no-plan-reuse`.

`clarify` is reactive: it answers a plan that already ran and stopped. That
separates it from [`/planwerk:revisit`](/how-to/revisit-an-issue), which is
preventive and asks a different question — not "what did we never decide" but
"what has changed under us since we decided it".
