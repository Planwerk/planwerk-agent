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
skipped with a log line, so a 5-line docs-only PR runs only the two always-on
specialists instead of all six. When the changed-file set cannot be determined,
the gate fails open and every specialist runs.

## Cross-Pass Merge and Dedup

When a secondary pass contributes findings (`--thorough`, `--specialists`, or a
feature-compliance pass), the pipeline folds them into the primary review. Two
independently worded passes almost never produce byte-identical titles, so the
merge matches *fuzzily*: two findings fold together when they share the same
file, their line ranges overlap within ±3 lines, and their titles share at least
half their tokens. A folded pair keeps the higher severity, unions its
provenance, and — the first time a finding is confirmed by two or more distinct
passes — has its confidence boosted one step.

Findings with no file cannot be anchored by the fuzzy matcher, so a single cheap
structure-tier call groups the file-less duplicates by index; the pipeline folds
each group in Go with the same merge semantics. The dedup call is non-fatal: if
it fails, the findings ship unmerged.

## Claim Verification

The snippet gate demotes a finding whose quoted code cannot be found in the
changed files, but it verifies the *quote*, not the *claim* — a finding passes
by quoting one real line even when its conclusion is wrong. After the snippet
gate, a claim-verification pass re-checks every BLOCKING and CRITICAL finding
against the checkout in one batched, read-only call. The verifier confirms or
refutes each finding's claim and may refute *only* with concrete quoted
counter-evidence; absent such evidence it confirms. A refuted finding is demoted
to `uncertain` confidence with the refutation attached as a `**Claim check**`
note, which routes it into the Unverified / Low-Confidence section rather than
dropping it. The pass is fail-open: a failed call publishes the findings
unchanged.
