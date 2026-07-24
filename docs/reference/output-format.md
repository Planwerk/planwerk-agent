# Output format

The generated Markdown report follows a fixed structure:

```markdown
# Review: owner/repo#123

> *Feature: Add user authentication*
> Reviewed by [planwerk-agent](https://github.com/planwerk/planwerk-agent) vX.Y.Z with Claude:claude-opus-5

<!-- planwerk-agent: blocking=1 critical=2 warning=3 info=1 recommendation=HOLD -->

## BLOCKING (1)

### B-001: Hardcoded secrets in configuration
**File**: `config/auth.go:42` — **Fix**: ASK — **Confidence**: verified — **Pattern**: Hardcoded values

**Problem**: API secret is hardcoded directly in the source code.

**Action Required**: Remove secret from code and provide it via
environment variable or secret manager.

---

## CRITICAL (2)

### C-001: SQL Injection in User Query
**File**: `db/users.go:87-92` — **Fix**: ASK — **Confidence**: verified

**Problem**: User input is used in SQL query without sanitization.

**Action Required**: Use prepared statements.

---

### C-002: Missing error handling
**File**: `handlers/login.go:23` — **Fix**: AUTO-FIX — **Confidence**: likely

**Problem**: Error from `ValidateToken()` is ignored.

**Action Required**: Check error and return HTTP 401 on failure.

**Related**: B-001

---

## WARNING (3)

### W-001: ...

---

## Summary

The PR introduces user authentication with a well-structured handler layer, but hardcoded secrets and an SQL injection vulnerability must be addressed before merge. Error handling is inconsistent across the new endpoints.

| Severity | Count |
|----------|-------|
| BLOCKING | 1     |
| CRITICAL | 2     |
| WARNING  | 3     |
| INFO     | 1     |

> [!CAUTION]
> **Do not merge** — 1 BLOCKING and 2 CRITICAL findings must be resolved first.
```

The attribution line links back to the project repository —
`[planwerk-agent](https://github.com/planwerk/planwerk-agent)` — and, right
after the link, names the build that produced the report (the same string
`planwerk-agent --version` prints) and the exact Claude model —
`with Claude:claude-opus-5`, not the alias passed via `--claude-model`. The
model id is read from the model the session reports at startup; when it is
unavailable the clause falls back to a bare `with Claude`, and when the build
version is unknown the repository link stands alone. Every artifact
planwerk-agent leaves on GitHub (issue bodies, pull request descriptions, review
comments, thread replies) carries the same self-attribution footer in this shape,
so the report headers and the comment footers read identically.

## Severity Levels

| Level | Meaning | Action |
|-------|---------|--------|
| **BLOCKING** | Fundamental architecture/security issues | PR must not be merged |
| **CRITICAL** | Bugs, security vulnerabilities, severe problems | Must be fixed before merge |
| **WARNING** | Code quality, potential issues | Should be fixed |
| **INFO** | Style questions, improvement suggestions | Optional, for information |

## Actionability Levels

| Level | Meaning | Action |
|-------|---------|--------|
| **auto-fix** | A senior engineer would fix without discussion | Apply the suggested fix directly |
| **needs-discussion** | Requires team input before fixing | Discuss in PR comments or team sync |
| **architectural** | Fundamental design issue | Needs broader design conversation |

## Enriched Finding Fields

Each finding includes additional metadata for tooling and automation:

| Field | Description |
|-------|-------------|
| **FixClass** | `AUTO-FIX` or `ASK` — derived from Actionability, indicates whether the fix can be applied directly |
| **Confidence** | `verified`, `likely`, or `uncertain` — how certain the reviewer is about the finding |
| **CodeSnippet** | The relevant code fragment from the diff |
| **SuggestedFix** | Concrete replacement code for auto-fix findings |
| **RelatedTo** | IDs of related findings (e.g., `["B-001", "C-003"]`) |
| **LineEnd** | End line for multi-line findings (enables line-range comments) |
| **VerificationNote** | Set only by the claim-verification pass: why a BLOCKING/CRITICAL finding was refuted (e.g. `refuted: guarded at line 50`). A finding that carries it is demoted to `uncertain` confidence and routed to the Unverified / Low-Confidence section, and the Markdown report renders it as a `**Claim check**:` line. |
| **SnippetCheck** | Set only by the snippet-verification gate: `passed` when the finding's quoted code was found in the changed files, or a `demoted: <reason>` string when it was not. A demoted finding is lowered to `uncertain` confidence; the Markdown report renders the reason as a `**Snippet check**:` line, while a `passed` record renders nothing. Unlike `VerificationNote` it does **not** route the finding — it is a pure record of what the gate examined. |
| **ClaimCheck** | Set only by the claim-verification gate: `confirmed`, `refuted`, or `no-verdict` (the gate ran but returned no verdict for this finding — the fail-open case). This machine token records the gate's decision; the human-readable refutation reason continues to come from `VerificationNote` (the `**Claim check**:` line), which the token does not duplicate. |

## Machine-Readable Output

The Markdown report includes an HTML comment with counts and recommendation
verdict for machine consumption:

```html
<!-- planwerk-agent: blocking=1 critical=2 warning=0 info=3 recommendation=HOLD -->
```

Verdict values: `HOLD` (blockers/criticals present), `REVIEW` (warnings only),
`MERGE` (clean), `CUSTOM` (manual recommendation).

Recommendations use GitHub Alert syntax (`[!CAUTION]`, `[!WARNING]`, `[!TIP]`,
`[!IMPORTANT]`) for native rendering.

## Claude Usage Totals

Every command aggregates the token usage and estimated cost of all the Claude
Code calls it makes in a single Run and surfaces the totals two ways.

On completion, a one-line summary is printed to **stderr**:

```text
claude usage: 13.4k in / 4.2k out across 6 calls, est. $0.42
```

`in`/`out` are the summed input/output tokens (rendered compactly as `k`/`M`),
`calls` is the number of Claude invocations, and the estimate is the sum of
Claude Code's own reported per-call cost. The line is omitted when a Run made no
Claude call (for example `--version`, a dry run, or `--print-prompt`).

When a review is posted (`--post-review` / `--inline`), the same totals are
embedded in the `<!-- planwerk-agent-data ... -->` comment as a `usage` object
for CI extraction, alongside the findings:

```json
{
  "commit_sha": "abc123",
  "findings": [],
  "gates": {
    "snippet": { "examined": 8, "demoted": 2 },
    "claim": { "sent": 3, "verdicts": 3, "refuted": 1 }
  },
  "usage": {
    "calls": 6,
    "input_tokens": 13400,
    "output_tokens": 4200,
    "cache_read_input_tokens": 15626,
    "cache_creation_input_tokens": 2464,
    "est_cost_usd": 0.42
  }
}
```

`est_cost_usd` is the literal estimate Claude Code reports, summed across calls —
not a recomputed tokens × price figure.

## Demotion-Gate Records

A review has two gates that can demote a finding: the snippet gate (demotes a
finding whose quoted code is absent from the changed files) and the
claim-verification gate (re-checks each BLOCKING/CRITICAL claim and demotes the
ones a verifier refutes). Each gate records what it did so a run where every
finding passed is distinguishable from one where the gate never ran.

The per-finding record travels on each finding as `snippet_check` / `claim_check`
(see the [Enriched Finding Fields](#enriched-finding-fields) table). The
run-level counts travel in the `gates` object of the data block and the
`--format json` output:

- `snippet.examined` / `snippet.demoted` — findings the snippet gate checked and
  the subset it demoted.
- `claim.sent` / `claim.verdicts` / `claim.refuted` — findings sent to the claim
  verifier, verdicts returned, and the subset refuted. `sent > 0` with
  `verdicts: 0` is the **fail-open** case: the gate ran but the verifier returned
  nothing.
- An **absent** gate entry means that gate never ran (an empty diff skips the
  snippet gate; no BLOCKING/CRITICAL finding leaves nothing for the claim gate to
  send). Read a missing entry as "not recorded", never as "nothing demoted".

The `refuted / sent` ratio accumulated across cached results and posted reports
is the claim verifier's own no-op signal: a verifier that refutes nothing across
many runs is agreeing rather than verifying. Data blocks and cached results
produced before gate recording existed carry no `gates` object and no
per-finding records.

## JSON Schema

The `--format json` output of every command is described by a JSON Schema
(draft 2020-12) checked into the repository under `internal/report/schema/`:

| Schema file | Describes | Commands |
|-------------|-----------|----------|
| `report-result.schema.json` | `ReviewResult` (findings, summary, recommendation) | `review`, `audit` |
| `proposal.schema.json` | `ProposalResult` envelope (repository overview + proposals) | `propose` |
| `rebase-analysis.schema.json` | `RebaseAnalysis` (per-commit adjustments, summary, recommendation) | `rebase` |

`review` and `audit` share `report-result.schema.json` because the audit path
reuses the review result shape. The schemas pin the severity, confidence,
actionability, fix-class, priority, category, and scope enums, and allow `null`
for the slice fields the renderer leaves empty (`findings`, `proposals`,
`affected_areas`, `acceptance_criteria`).

The schemas are the source of truth: contract tests validate the renderers'
output against them, so a struct change that is not reflected in the schema
fails CI. The [`schema` subcommand](/reference/cli#schema) prints the same files
to stdout so consumers can validate piped JSON:

```bash
planwerk-agent review --format json owner/repo#123 > review.json
planwerk-agent schema review > report-result.schema.json
check-jsonschema --schemafile report-result.schema.json review.json
```

## Inline Review Mode (`--inline`)

With `--inline`, findings are posted as inline comments on the PR using the
GitHub Review API instead of (or in addition to) a single summary comment:

- Each finding that maps to a line in the PR diff becomes an inline comment on that line
- Auto-fix findings with a `SuggestedFix` use GitHub's `suggestion` syntax, enabling one-click apply
- Findings that cannot be mapped to diff lines are included in the review summary body
- The PR diff is fetched and parsed to validate that finding lines are within the diff (right side)
- Implies `--post-review`
