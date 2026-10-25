# Review methodology

The review prompt uses techniques inspired by [gstack](https://github.com/garrytan/gstack) to maximize review quality.

## Staff Engineer Persona

Claude is instructed to review as a Staff Engineer, applying specific cognitive patterns:

- *"What happens at 10x scale?"* — Load, data volume, concurrent users
- *"What's the blast radius?"* — If this code fails, what else breaks?
- *"What happens at 3am?"* — Error paths, oncall clarity, log quality
- *"Would a new team member understand this?"* — Code clarity and intent
- *"Where are the tests?"* — Does every new behavior have a test?
- *"Would I find this in the docs?"* — Is this feature discoverable from documentation?

## Scope Drift Detection

Before reviewing code quality, the tool checks for:

- **Scope Creep**: Files changed that are unrelated to the PR title/description
- **Missing Requirements**: Requirements from the PR description not addressed in the diff

## The Diff Every Pass Reads

Before a diff-scoped session starts (the review, the adversarial pass, the
domain specialists, the feature-compliance check, the simplify finder, and the
implementation verifier), the tool writes the diff to a file with
`git diff --no-ext-diff --no-textconv --no-color`. A diff driver, a text
conversion, or a color setting configured on the machine therefore cannot
change what the reviewer sees, and a large diff is not cut down to the preview
a long command output gets. The prompt names the file and its line count, and
the session reads every line of it a page at a time. It takes a finding's line
from the hunk header or the source file, because the line numbers it reads are
the diff file's own. When the file cannot be written, the session runs
`git diff` itself as before. The `implement` self-review reads the same file,
which from its second round on is `git diff <pre-fix commit>`. The coverage map
lists the changed files itself, and the audit has no diff.

`--diff-exclusions` leaves three kinds of changed file out of that diff:

- lock files, by name (`go.sum`, `package-lock.json`, `yarn.lock`,
  `pnpm-lock.yaml`, `Cargo.lock`, `poetry.lock`, and their kin), which a
  reviewer judges through the manifest beside them;
- the changed files under a `vendor/` or `node_modules/` directory, one entry
  per directory;
- files whose leading comment says `Code generated … DO NOT EDIT.`, which a
  reviewer judges through the generator input. A hand edit to such a file is
  therefore not reviewed.

A test is never left out unless it lies under a vendored directory, where it
is third-party code like the rest of that directory. A vendor directory under a
test directory, such as `testdata/vendor/`, is a fixture and stays. A change
made only of files these rules match is diffed in full. The prompt lists every
file it left out, in a fence it names as data because the pull request's author
picks the paths, and the report lists them
([Excluded Files](/reference/output-format#excluded-files)), so an opted-in run
never loses a file silently.

The flag is off by default because the pull request under review decides which
of its files a rule matches: a file under any `vendor/` directory, or one that
opens with the generated-code comment, leaves the diff by its own path or its
own first line, so a default-on rule would let an author hide a change from
every pass. See [design decision 137](/explanation/design-decisions).

## Three-Pass Review Checklist

Claude works through a structured checklist in three passes:

| Pass | Focus | Categories |
|------|-------|------------|
| **Pass 1 — Critical** | Always checked | SQL & Data Safety, Race Conditions, Error Handling, Security, Input Validation, LLM Output Trust, Crypto |
| **Pass 2 — Semantic** | Requires tracing beyond the diff | Enum Completeness, Conditional Side Effects, Type Coercion, Test Coverage for New Code, Documentation Completeness |
| **Pass 3 — Informational** | Checked if time permits | Magic Numbers, Dead Code, Test Quality, Performance, API Contract, View/Frontend, Time Window |

## Suppressions

To reduce false positives, the following are explicitly suppressed:

- TODO/FIXME comments with issue tracker references
- Missing tests for trivial getters/setters (does not suppress missing tests for functions with logic)
- Import ordering or formatting differences
- Variable naming matching existing project conventions
- Missing documentation on private functions (does not suppress missing docs for public APIs)
- Minor style preferences
- Code that was not changed in the diff (only added or modified lines are reviewed)

## Test & Documentation Verification

After the checklist passes, the review explicitly verifies:

- **Test Completeness**: Every new or significantly modified function should have corresponding tests matching the project's testing conventions. The tool actively searches for all test categories: unit tests (`_test.go`, `test_*.py`, `*.spec.ts`), integration tests (`tests/integration/`), and E2E tests (`e2e/`, `chainsaw/`, `.chainsaw/`, `chainsaw-test.yaml`, kuttl). If the project uses multiple test types, new code must include matching tests for each category. Missing E2E tests are flagged separately from missing unit tests.
- **Documentation Completeness**: New public APIs, CLI flags, configuration options, and user-facing behavior changes must be reflected in documentation (README, CHANGELOG, doc comments).
- **New File Detection**: Newly added source files are flagged as candidates for documentation if they are not test files or internal configuration. Test file detection covers language-based conventions as well as infrastructure test patterns (Chainsaw, E2E directories).

## Anti-Sycophancy Rules

Claude is instructed to be direct and decisive — no hedging with phrases like "you might want to consider" or "this could potentially cause". Every finding takes a clear position.

## Actionability Classification

Each finding is classified by actionability:

| Classification | Meaning | Examples |
|----------------|---------|----------|
| **auto-fix** | A senior engineer would apply without discussion | Dead code, magic numbers, missing error wrapping |
| **needs-discussion** | Requires team input before fixing | Security decisions, API changes, behavioral changes |
| **architectural** | Needs a broader design conversation | Wrong abstraction, missing layer, significant refactor |

The actionability values and their downstream `FixClass` mapping are documented
in the [output format reference](/reference/output-format#actionability-levels).

## Adaptive Specialist Gating

With `--specialists`, the review fans out into six domain reviewers that run
concurrently and merge their findings. To avoid spending a full fan-out on a
small change, each specialist is *gated*: it runs only when the PR diff touches
files relevant to its domain.

| Specialist | Runs when the diff touches |
|------------|----------------------------|
| `security` | always (a missed vulnerability is too costly to gate) |
| `data-migration` | always (a destructive migration is too costly to gate) |
| `testing` | any source-code file |
| `performance` | any source-code file |
| `maintainability` | any source-code file |
| `api-contract` | a routing / request-handler file (`api/`, `routes/`, `handlers/`, `controllers/`, or a `*handler*` / `*route*` / `*controller*` file) |

A file counts as source code unless it is documentation, configuration, data,
or media (`.md`, `.yaml`, `.json`, `.png`, …). Gated-out specialists are
skipped, and the report lists them as gated out beside the ones that ran, so a
5-line docs-only PR runs only the two always-on specialists instead of all six.
When the changed-file set cannot be determined, the gate fails open and every
specialist runs.

The same fan-out runs inside `implement`'s self-review, on the first round of
its review-and-fix loop (see [Shared with `implement`'s self-review](#shared-with-the-implement-self-review)).
Both the adversarial finder and each specialist are also grounded in the
project's review-pattern catalog, so a pass inspecting a fresh diff applies the
same patterns a later review of that diff would. A specialist gets the part of
the catalog its domain reads on rather than all of it: each declares its review
areas (security → `security`, testing → `testing`, maintainability → `quality`,
`architecture`, `documentation`, and so on), and the prompt renders only those
patterns. The rest are patterns the prompt already tells the pass not to act on,
since it reviews ONLY its domain — and they are still applied in full by the
primary review and adversarial passes, which carry the whole catalog. A
specialist that declares no areas, or whose areas match no loaded pattern, falls
back to the full catalog.

## Cross-Pass Merge and Dedup

When a secondary pass contributes findings (`--thorough`, `--specialists`, or a
feature-compliance pass), the pipeline folds them into the primary review. Two
independently worded passes almost never produce byte-identical titles, so the
merge matches *fuzzily*: two findings fold together when they share the same
file, their line ranges overlap within ±3 lines, and their titles share at least
half their tokens. A folded pair keeps the higher severity, unions its
provenance, and — the first time a finding is confirmed by two or more distinct
passes — has its confidence boosted one step.

A secondary pass that fails does not fail the review. The merge goes on without
it, the report names it with its error, and the result is not cached
([Pass Status](/reference/output-format#pass-status)).

Findings with no file cannot be anchored by the fuzzy matcher, so a single cheap
structure-tier call groups the file-less duplicates by index; the pipeline folds
each group in Go with the same merge semantics. The dedup call is non-fatal: if
it fails, the findings ship unmerged.

The merge, dedup, snippet-gate, and claim-verification stages live in the shared
`internal/hygiene` package, so `implement`'s self-review runs the exact same
hygiene the reporting path does — see [Shared with `implement`'s self-review](#shared-with-the-implement-self-review).

## Claim Verification

The snippet gate demotes a finding whose quoted code cannot be found in the
changed files, but it verifies the *quote*, not the *claim* — a finding passes
by quoting one real line even when its conclusion is wrong. After the snippet
gate, a claim-verification pass re-checks every BLOCKING and CRITICAL finding
against the checkout in one batched, read-only call. For each finding the
verifier writes its evidence and its reason first and its verdict last:
confirmed, refuted, or unverifiable when the checkout cannot settle the claim.
It refutes *only* with concrete quoted counter-evidence or a named search that
came back empty. A refuted finding is demoted to `uncertain` confidence with the
refutation attached as a `**Claim check**` note, which routes it into the
Unverified / Low-Confidence section rather than dropping it. The pass is
fail-open: a failed call publishes the findings unchanged.

Both gates record what they did, not only what they rejected. Each stamps every
finding it examined with its own per-finding outcome, and each writes run-level
counts — findings examined, verdicts returned, and demotions — onto the result,
where they ride the cached result and the machine-readable data block. This is
what makes the claim verifier's `refuted / sent` ratio answerable from history: a
verifier that refutes nothing across many runs is agreeing rather than verifying,
and telling the two apart needs the number, which now accumulates from real runs
instead of dying in a log line. See
[Demotion-Gate Records](/reference/output-format#demotion-gate-records) for the
recorded shape.

## Shared with the `implement` self-review

Everything above — the specialist fan-out, the cross-pass merge and dedup, the
snippet gate, and claim verification — is behavior the `review` command and the
`implement` command's self-review both run, out of the shared `internal/hygiene`
package. The distinction is what each command does with the result. `review`
*reports*: it renders every finding, routing the ones that did not survive
hygiene into an Unverified section a human can weigh, and under `--inline` posts
none of those as a comment on the diff. `implement` *acts*: a
second session edits the branch to resolve findings, unattended, with no human to
catch a hallucinated one before it becomes a commit.

That difference is why the acting path treats hygiene as a gate. A finding must
survive the same stages — merge, dedup, snippet gate, claim verification — before
`implement`'s editing session may act on it. Findings that do not survive are
reported (on stdout and on the issue) but never applied: the restriction is
enforced by the harness, not requested in the editing session's prompt. Among
the survivors, INFO is optional: the editing session applies an INFO finding only
when the fix stays in the code the finding cites and changes no behavior. That
one rule is asked in the prompt rather than enforced by the harness. Each posted
round report counts what the session was handed by severity and labels every
entry it resolved or skipped with one, so how often it applies an INFO finding
can be read off the issue ([design decision 124](/explanation/design-decisions)). A
specialist that fails on the first round is named, with its error, under
`### Passes that failed` in that round's report. To bound
the cost, `implement` runs the fan-out on the first round of its review loop only;
later rounds re-check the applied fixes with the cheaper adversarial finder alone,
and at a narrower scope: the first round reviews the whole branch diff, while
every round after it reviews only what the previous round's fixes changed
(`git diff <pre-fix commit>`). The branch has already been reviewed in full by
then, so the question a later round answers is whether the fixes hold — not the
one the first round already asked.
See [Review-and-Fix Pass](/how-to/implement-an-issue) for the operator's view and
[design decision 72](/explanation/design-decisions) for the rationale.
