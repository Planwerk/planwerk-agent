# Prompt-design doctrine

planwerk-agent is, underneath the GitHub plumbing, a prompt compiler. Every
subcommand assembles a large instruction string from smaller blocks and hands it
to Claude Code. Those builders live in `internal/claude/` — roughly forty of
them — and the quality of the tool is, to a first approximation, the quality of
the prompts they emit.

The tool has a second prompt surface: the three Claude Code Skills under
`plugins/planwerk/skills/` (see
[design decision 64](./design-decisions.md)). They are prompts a human converses
with rather than prompts a subcommand fires once, so they add rules this page
does not cover — when to ask instead of guess, how to present a choice, what
must never happen without a confirmation. Those rules live in
`plugins/planwerk/shared/interaction.md`. Everything below applies to both
surfaces.

That authoring discipline has so far lived only in practice. The builders are
consistent because the people writing them carried the rules in their heads, not
because the rules were written down. A discipline that lives only in heads
cannot be taught to a new contributor, applied evenly in review, or enforced
against drift. This page writes it down so it can be.

It is an Explanation, not a how-to: it gives the vocabulary and the reasoning
behind it. The mechanics — how the builders are structured, where the shared
blocks live, how the tests are regenerated — are in the code and its comments
(`internal/claude/components.go`, `internal/claude/baseline.go`,
`internal/claude/prompts_golden_test.go`).

## Predictability is the root virtue

A prompt builder is a contract. Given the same context it must produce the same
text, and that text must steer the model toward the same behavior every run.
Everything else in this doctrine is downstream of predictability: we collapse
duplication so two copies cannot drift apart, we sharpen weak imperatives so the
model cannot interpret an instruction two ways, and we make completion criteria
checkable so "done" means the same thing every time.

Predictability is also why the prompts are plain Go string assembly rather than
a templating engine with conditionals scattered through it. The output of a
builder is easy to read, easy to diff, and — crucially — easy to snapshot. The
golden tests (see [enforcement](#how-the-doctrine-is-enforced)) turn
predictability from an aspiration into a property the build checks.

When a change makes a prompt *better* but *less* predictable — a clever
rephrasing that a future editor will quietly "improve" back, a block that is
shared in one builder and inlined in another — predictability wins. A prompt we
can reason about and hold stable is worth more than a prompt that is marginally
sharper but drifts.

## Information hierarchy

A prompt is read top to bottom by a model that weights early, specific
instructions heavily. So the load-bearing instruction leads. The review prompt
opens by pinning the review *scope* before it says anything about persona or
patterns, because reviewing the wrong diff makes every later instruction moot.
The implement prompt states the issue is the definition of done before it lists
thinking patterns.

This is the same rule the `proseStyleBlock` imposes on the prose the model
*writes* ("Lead with the most important information; never bury it") turned back
on the prose we write *to* the model. Bury the one instruction that matters
under a paragraph of context and the model weights them equally — which means it
weights the important one too little.

Concretely: state the constraint, then the rationale, not the reverse. "Review
the FULL pull request diff" comes first; the explanation of why multi-commit PRs
need it follows. A reader (human or model) who stops after the first sentence
still has the instruction.

## Completion criteria: checkable and exhaustive

The weakest part of most prompts is the end — how the model knows it is done.
"Review the code" has no checkable finish; "emit a finding for every pattern
violation, or an empty array if there are none" does. Two properties make a
completion criterion sound:

- **Checkable** — the criterion names an observable the model (and a later
  reader) can test. "Cite the exact `file:line` for every satisfied judgment, or
  downgrade it to partial" is checkable; "verify thoroughly" is not.
- **Exhaustive** — the criterion covers every branch, including the empty one.
  The structuring prompts say what to do when there are no findings ("return an
  empty findings array") precisely so the model does not invent one to look
  productive. The gap-analysis prompt walks four named checks and forbids merging
  them, so no branch is silently skipped.

This connects directly to two existing decisions in
[design decisions](./design-decisions.md): the elaborate command's forced
edge-case enumeration (#31), which makes every data-flow acceptance criterion
spell out its empty, nil, and error branches, and the implement complete-report
guard (#38), which refuses to treat output as a report unless it carries both
the mandated heading and a terminal `STATUS` line. Both are completion criteria
made checkable and exhaustive at the prompt level.

## Single source of truth

An instruction that more than one builder needs is written once and shared, not
copied. `internal/claude/components.go` is where the shared blocks live —
suppressions, prose style, output language, the commit-trailer convention, the
banned-vocabulary line, the architecture vocabulary. The motivating failure was
real: before the suppression list was extracted, the audit prompt carried a
shortened copy that had already drifted from the review prompt's version.

Extraction is the rule; copying is the exception that has to justify itself. The
test for whether something belongs in `components.go` is whether two builders
would otherwise have to be kept in sync by hand.

The skills obey the same rule on their own surface: the house issue format, the
prose rules, the interaction doctrine, and the `gh` invocations are written once
under `plugins/planwerk/shared/` and read by all three `SKILL.md` files, rather
than restated per skill.

One instruction genuinely lives in two places. `elaborate` exists as both a
command and a skill, so the issue format is expressed once as Go
(`elaborate.BuildIssueBody`) and once as prose
(`plugins/planwerk/shared/issue-format.md`). Discipline alone would not keep
those aligned, so `TestBuildIssueBody_MatchesSharedFormat` asserts it: the
sections, their order, the `- [ ]` checkbox form, and the footer must agree, or
the build fails. Where duplication is unavoidable, make the drift mechanically
detectable.

The rule has a deliberate inverse. Some text *looks* shared but is not the same
instruction: the Staff Engineer persona, the Verification-of-Claims rules, and
the Finding-Enrichment block read similarly across the diff-review and the
whole-codebase audit, but they carry scope-specific wording — a diff review
talks about "the diff", an audit about "the codebase". Forcing those into one
block would inject diff-only wording into the audit and vice versa, so they are
kept separate on purpose. `components.go` documents these exceptions in its
header. Single source of truth means *one source per instruction*, not *one
block for every superficially similar paragraph*.

## The named failure modes

The audit that pays this doctrine back across the builders looks for five
specific failures. Naming them makes them reviewable — a reviewer can point at a
line and say "that is sediment" instead of arguing taste.

- **No-op** — a sentence that instructs nothing checkable. "Be thorough and
  careful" adds no constraint the model can act on. *Caught by* asking of every
  sentence: what does the output look like if this line is deleted? If nothing
  changes, the line was a no-op.
- **Duplication** — the same instruction copied into more than one builder,
  free to drift. *Caught by* extraction into `components.go`; the drift between
  two copies is the tell.
- **Sediment** — wording that accumulated over edits and no longer pulls its
  weight: a qualifier on a qualifier, an example that restates the rule above it,
  a hedge left over from a constraint that has since been tightened. *Caught by*
  reading a block as a whole and asking which sentences survived only because
  nobody removed them.
- **Sprawl** — a prompt that grew long enough that its own important
  instructions compete for attention. Every added sentence dilutes the ones
  already there. *Caught by* the token cost (see below) and by the
  information-hierarchy test: if the lead instruction is now on screen three, the
  prompt has sprawled.
- **Premature completion** — a finish line the model can cross while the work is
  half done: a vague "done when it works", a criterion that covers the happy
  path but not the empty or error branch. *Caught by* the checkable-and-
  exhaustive test above.

## How the doctrine is enforced

The safety net is `internal/claude/prompts_golden_test.go` and its fixtures
under `internal/claude/testdata/prompts/`. Every builder has a golden test that
locks its exact output for a fixed context. Any edit to a prompt — intentional or
accidental — shows up as a byte-level diff in a `.golden` file, so an unintended
change to one builder cannot ride along in an unrelated commit.

When a prompt change *is* intentional, the workflow is to regenerate the
fixtures and review the diff:

```bash
go test ./internal/claude -update
```

The reviewer then reads the `.golden` diff as the real artifact of the change.
For an audit edit — collapsing duplication, deleting a no-op, sharpening an
imperative — the diff must be a wording or structure change only; if it would
alter what the model is asked to *do*, it is no longer an audit edit and needs to
be justified as a behavioral change on its own terms.

Token cost is the secondary signal. The session usage is reported by
`(*Client).LogUsageSummary` in `internal/claude/claude.go`; sprawl shows up there
as prompts that cost more without reviewing better. Trimming no-ops and sediment
lowers that cost as a side benefit, but the primary goal is a prompt that steers
the model predictably — cost is the thermometer, not the disease.

## Attribution

The vocabulary on this page — predictability, information hierarchy, the no-op /
duplication / sediment / sprawl / premature-completion failure modes — is adapted
from the `writing-great-skills` skill and `GLOSSARY.md` in
[mattpocock/skills](https://github.com/mattpocock/skills) (MIT), reframed from
authoring interactive Claude Code skills to authoring this project's
non-interactive prompt builders.
