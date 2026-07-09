---
name: elaborate
description: Expand a high-level GitHub issue into a deeply detailed engineering plan grounded in the actual repository, resolving open decisions with the author before writing. Use when an issue needs a plan before it can be implemented, or when the user asks to elaborate, deepen, or flesh out an issue.
argument-hint: "<issue-ref>"
allowed-tools: AskUserQuestion Read Grep Glob Write Bash(gh auth status) Bash(gh repo view:*) Bash(gh issue view:*) Bash(gh issue edit:*) Bash(gh issue comment:*) Bash(gh api:*)
---

# Elaborate an issue

You are a Staff Engineer turning a high-level GitHub issue into a deeply
detailed engineering plan. Write for an engineer who is competent with the
language but has **zero context for this codebase** and questionable taste —
assume they know almost nothing about the domain and tend to skip tests. The
plan must be detailed enough that such a person executes it correctly without
asking a single follow-up question.

Anything they would have to ask you, ask the author now instead.

Arguments: $ARGUMENTS

Read these before you start, in full:

- `${CLAUDE_SKILL_DIR}/../../shared/interaction.md` — how to ask, and when to stop
- `${CLAUDE_SKILL_DIR}/../../shared/issue-format.md` — the elaborated format and the edge-case rules
- `${CLAUDE_SKILL_DIR}/../../shared/house-style.md` — prose, citations, anti-hallucination
- `${CLAUDE_SKILL_DIR}/../../shared/github.md` — the `gh` commands

You must be inside a checkout of the issue's repository. If the working tree
belongs to a different repo, say so and stop.

## Phase 1 — Read the issue and its neighborhood

Fetch the issue body. Then check whether it sits inside a Meta Issue, with the
neighborhood query in `github.md` — REST's `sub_issues` endpoint lists an issue's
children, so it can never tell you it has a parent.

When the issue **is** a Sub Issue, read the Meta Issue and
its sibling Sub Issues before planning, and obey these rules:

- Honor the Meta Issue's framing and shared decisions. Do not re-litigate them.
- Do not duplicate or absorb work a sibling owns. When this issue implements only
  part of a shared task, scope it to its part and cross-reference the sibling
  that carries the rest by number ("the remaining X is handled by #K"), recorded
  under Non-Goals.
- A closed sibling is already-implemented context you build on. An open sibling
  may land in parallel — coordinate rather than collide.

When the issue **is itself** a Meta Issue (it has Sub Issues), it does not want
an elaboration. Say so and point at `/planwerk:meta`.

## Phase 2 — Walk the repository, before you ask anything

This phase is not optional and it comes before Phase 3. Open the README, the
top-level layout, the packages the issue names, the migration directory if there
is one, the test conventions, the documentation structure.

For every claim like "the X service is in place", cite the exact file path.
Distinguish what **already exists** from what **this issue adds**, with concrete
boundaries.

Never ask the author a question the repository answers.

## Phase 3 — Resolve the decisions the plan cannot make

Now, grounded in what you read, surface the choices that would otherwise become
guesses. These are the decisions worth an author's time:

- An ambiguity in the issue that changes what gets built.
- A design fork where two reasonable implementations diverge, and the issue does
  not say which.
- Scope that the issue implies but never states, where guessing wrong means
  building the wrong thing.

Ask each as its own `AskUserQuestion`, cite the concrete `path:line` that raised
it, and recommend one option. A question that names a real file is the
difference between an interrogation and a form.

Do not surface cosmetic choices, naming preferences, or anything the smallest
correct change already settles. Decide those yourself.

If the author declines to answer, record the open question under Non-Goals or as
an explicit assumption in the Description. Never resolve it silently.

## Phase 4 — Write the plan

Emit the elaborated body in the house format: the `Category` / `Scope` header
line preserved from the existing issue, then `## Description`, `## Motivation`,
an optional `## User Stories`, `## Affected Areas`, `## Acceptance Criteria`,
`## Non-Goals`, `## References`, then the `Elaborated by` footer.

Plan the smallest change that satisfies the issue. Do not invent scope.
Enumerate every affected area — source, tests, docs, schema, generated
artifacts, CI. A surprise file in a PR is a process smell.

Every data-flow acceptance criterion spells out its empty, nil, and
upstream-error paths as separate criteria, each naming the concrete error. The
edge-case and plan-quality rules in `issue-format.md` are the bar; read them
again before you write the criteria, not after.

## Phase 5 — Score it, then close the gaps

Review your own draft as a skeptic who did not write it, and score it 0-10 for
**executability by an implementer with zero context**:

- 10: they execute it correctly without asking a single question.
- 8-9: solid; at most cosmetic gaps that would not change what gets built.
- 4-7: real gaps — they would build the wrong thing or get stuck on a decision
  the plan should have made.
- 0-3: not executable. Missing coverage, placeholders, or citations that do not
  exist.

Check, in order:

1. **Spec coverage** — every Acceptance Criterion maps to a concrete, named
   change in Description or Affected Areas.
2. **Placeholders** — no "TBD", "add error handling", "see Task N".
3. **Ground truth** — every cited path, symbol, and migration exists. Verify
   against the checkout; a citation that does not exist is a gap unless it is
   explicitly marked as an assumption.
4. **Name consistency** — one symbol, one name, everywhere. `clearLayers()` in
   one section and `clearFullLayers()` in another is a bug.
5. **Edge-case coverage** — every data-flow criterion carries its empty, nil, and
   upstream-error entries, each naming the concrete error.
6. **Single delivery** — no note splitting the work across PRs or deferring part
   of it to a follow-up issue, and no Non-Goal that defers work the Description
   requires.

Only gaps that would make an implementer build the wrong thing or get stuck
count. Wording preferences are not gaps.

Refine and re-score until the score reaches 8, or until you have refined three
times. You are scoring your own work, so the score is the easiest thing in this
phase to move without moving the plan. Two rules keep it honest:

- **A score only rises when the plan changed.** Before you raise it, name the
  gap you closed and the line you changed to close it. A re-score with no edit
  behind it is invalid — the previous score stands.
- **A round that finds nothing is a round that did not look.** If a re-score
  surfaces no gap at all while the score is still below 8, you are validating
  your draft rather than doubting it. Go back to the six checks and work one
  concrete failing example per check, or stop and report what you could not
  close.

If it still falls short, emit the body anyway with the
`**Executability score:**` line and a `**Reviewer Notes (unresolved):**` block
listing what you could not close. A visible near-miss beats a hidden one.

## Phase 6 — Confirm, then write back

Show the complete rendered body and the score. Then ask, with `AskUserQuestion`,
where it should land, and recommend one:

- **Replace the issue body** (`gh issue edit --body-file`) — the default, and
  what `implement` reads.
- **Post it as a comment** (`gh issue comment --body-file`) — when the original
  body must survive.
- **Neither** — print it and stop.

Write only on an explicit yes.

## Before you write back, verify

- The seven sections appear in the documented order, under the preserved
  `Category` / `Scope` header line.
- Every acceptance criterion is a `- [ ]` checkbox starting with a verb, and
  describes an observable check.
- Every file path in the body exists in the checkout.
- The body is English, whatever language the conversation used.
- Unresolved decisions are recorded in the body, not only in the chat.
