# Output format

The generated Markdown report follows a fixed structure:

```markdown
# Review: owner/repo#123

> *Feature: Add user authentication*
> Reviewed by [planwerk-review](https://github.com/planwerk/planwerk-review) vX.Y.Z with Claude:claude-opus-4-8

<!-- planwerk-review: blocking=1 critical=2 warning=3 info=1 recommendation=HOLD -->

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
`[planwerk-review](https://github.com/planwerk/planwerk-review)` — and, right
after the link, names the build that produced the report (the same string
`planwerk-review --version` prints) and the exact Claude model —
`with Claude:claude-opus-4-8`, not the alias passed via `--claude-model`. The
model id is read from the model the session reports at startup; when it is
unavailable the clause falls back to a bare `with Claude`, and when the build
version is unknown the repository link stands alone. Every artifact
planwerk-review leaves on GitHub (issue bodies, pull request descriptions, review
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

## Machine-Readable Output

The Markdown report includes an HTML comment with counts and recommendation
verdict for machine consumption:

```html
<!-- planwerk-review: blocking=1 critical=2 warning=0 info=3 recommendation=HOLD -->
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
embedded in the `<!-- planwerk-review-data ... -->` comment as a `usage` object
for CI extraction, alongside the findings:

```json
{
  "commit_sha": "abc123",
  "findings": [],
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

## JSON Schema

The `--format json` output of every command is described by a JSON Schema
(draft 2020-12) checked into the repository under `internal/report/schema/`:

| Schema file | Describes | Commands |
|-------------|-----------|----------|
| `report-result.schema.json` | `ReviewResult` (findings, summary, recommendation) | `review`, `audit` |
| `proposal.schema.json` | `ProposalResult` envelope (repository overview + proposals) | `propose` |
| `rebase-analysis.schema.json` | `RebaseAnalysis` (per-commit adjustments, summary, recommendation) | `rebase` |
| `draft.schema.json` | `DraftResult` (title, description, motivation, scope, body) | `draft` |

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
planwerk-review review --format json owner/repo#123 > review.json
planwerk-review schema review > report-result.schema.json
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
