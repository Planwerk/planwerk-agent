# Output format

The generated Markdown report follows a fixed structure:

```markdown
# Review: owner/repo#123

> *Feature: Add user authentication*
> Reviewed by [planwerk-agent](https://github.com/planwerk/planwerk-agent) vX.Y.Z with Claude:claude-opus-5-5

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

## Files Not Reviewed (1 of 6)

The review did not report reading these changed files, so the findings above may not cover them.

- `go.sum`: skipped (generated checksum file)

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
`with Claude:claude-opus-5-5`, not the alias passed via `--claude-model`. The
model id is read from the model the session reports at startup; when it is
unavailable the clause falls back to a bare `with Claude`, and when the build
version is unknown the repository link stands alone. Every artifact
planwerk-agent leaves on GitHub (issue bodies, pull request descriptions, review
comments, thread replies) carries the same self-attribution footer in this shape,
so the report headers and the comment footers read identically.

The Files Not Reviewed section appears only when the review session did not
report reading every changed file its prompt listed; it names each such file,
with the session's reason when it skipped one (see
[Changed-File Coverage](#changed-file-coverage)). Each path sits in a code span
that a backtick in the path cannot close, and a `<` in a path shows as `&lt;`,
so a file name cannot write a comment marker such as the data block's into
the posted comment.

## Severity Levels

| Level | Meaning | Action |
|-------|---------|--------|
| **BLOCKING** | An exploitable security vulnerability, data loss or corruption, or a design flaw that needs the change reworked | PR must not be merged |
| **CRITICAL** | A defect that breaks correct behavior on a normal path: a crash, a wrong result, a broken contract | Must be fixed before merge |
| **WARNING** | A defect on an edge or failure path, one that erodes reliability over time (a leak, a swallowed error), or a code quality problem | Should be fixed |
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
| **CodeSnippet** | The relevant code fragment from the diff; a re-review matches findings on it (see [Re-reviews](#re-reviews)) |
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

A run that spent its tokens across more than one pass follows the totals with a
per-pass breakdown, most expensive first:

```text
claude usage: 350.0k in / 33.0k out across 7 calls, est. $12.29
  implement                 1 call   180.0k in / 21.0k out, est. $7.40
  plan                      1 call   96.0k in / 8.0k out, est. $2.90
  specialist-security       1 call   38.0k in / 1.4k out, est. $0.62
  +4 further passes         4 calls  36.0k in / 2.6k out, est. $1.37
```

The pass name is the label the runner tags the `claude -p` invocation with, so
calls that belong to the same pass (the finder rounds of a review loop)
accumulate together. At most eight rows are
shown; the rest are summed into a trailing `+N further passes` line, so the
breakdown always accounts for the whole run.

When a review is posted, the same totals are embedded as a `usage` object in
the `<!-- planwerk-agent-data ... -->` block of the summary comment that both
`--post-review` and `--inline` post, alongside the findings, for CI
extraction:

```json
{
  "commit_sha": "abc123",
  "findings": [],
  "gates": {
    "snippet": { "examined": 8, "demoted": 2 },
    "claim": { "sent": 3, "verdicts": 3, "refuted": 1 },
    "files": {
      "changed": 6,
      "reviewed": 5,
      "missing": [{ "file": "go.sum", "status": "skipped", "reason": "generated checksum file" }]
    }
  },
  "passes": [{ "pass": "adversarial", "status": "ran" }],
  "usage": {
    "calls": 6,
    "input_tokens": 13400,
    "output_tokens": 4200,
    "cache_read_input_tokens": 15626,
    "cache_creation_input_tokens": 2464,
    "est_cost_usd": 0.42,
    "passes": [
      {
        "pass": "review",
        "calls": 1,
        "input_tokens": 9800,
        "output_tokens": 3600,
        "cache_read_input_tokens": 12000,
        "cache_creation_input_tokens": 2464,
        "est_cost_usd": 0.31
      }
    ]
  }
}
```

`commit_sha` is the commit the review checked out and read: the cache key names
it, and an inline review is submitted against it as `commit_id`.

`est_cost_usd` is the literal estimate Claude Code reports, summed across calls —
not a recomputed tokens × price figure. `usage.passes` carries the same six
counters per pass, ordered as in the stderr breakdown and complete (never
truncated); it is omitted when no pass was recorded. The top-level `passes`
array records how each requested pass ended, not what it spent (see
[Pass Status](#pass-status)).

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

### Changed-File Coverage

The review prompt lists the changed files, and the review session accounts for
each one: reviewed, or skipped with a reason (see
[Changed-File Coverage](/explanation/review-methodology#changed-file-coverage)).
After the gates, the pipeline compares that account with the list and records
the outcome in `gates.files`:

- `files.changed`: the files the review prompt listed. These are the changed
  files without the ones `--diff-exclusions` left out of the diff and without
  the `.planwerk/` planning artifacts; `.planwerk/checklist.md`,
  `.planwerk/checklist.d/` and `.planwerk/review_patterns/` stay listed.
- `files.reviewed`: the listed files the session reported reading.
- `files.missing[]`: every other listed file, in listed order, with `status`
  `skipped` and the session's `reason`, or `unreported` when the session's
  account left the file out. Absent when the session reported reading every
  listed file.

The record is the review session's own account, and a session can call a file
reviewed it never opened, so `reviewed` is an upper bound. The record never
moves a finding or changes the verdict, and only `review` writes it: the audit
and `implement`'s self-review list no files. The `--format json` output and the
data block carry this record, not the session's own list. An absent `files`
entry means no list was given: the changed files could not be listed, none
remained after the exclusions, or the prompt would have listed more than 200.
The Markdown report names the missing files in the Files Not Reviewed section
before the Summary.

## Pass Status

A review records how each secondary pass it requested ended:

- `adversarial` with `--thorough`
- `coverage` with `--coverage-map`
- `compliance` when a Planwerk feature file is detected
- `specialist-<key>` for each domain specialist with `--specialists`, in
  registry order

The primary review is never listed: its failure fails the run.

| `status` | Meaning |
|----------|---------|
| `ran` | The pass returned, and its findings (possibly none) were merged. |
| `gated-out` | [Adaptive gating](/explanation/review-methodology#adaptive-specialist-gating) skipped the specialist, since the diff touches none of its paths. |
| `failed` | The pass errored and contributed nothing. `error` holds the first line of the error, secrets redacted, cut to at most 200 bytes; the full error stays in the log. |

The Markdown report lists the statuses on a `**Passes**:` line in the Summary,
in the order ran, gated out, failed. When a pass failed, an alert above the
findings names it with its error:

```markdown
> [!WARNING]
> **Incomplete review** — 1 requested pass(es) failed and contributed nothing to this report:
> - `specialist-security`: running security specialist review: claude (model opus): timed out after 1h0m0s (--claude-timeout)
>
> A review with a failed pass is never cached, so running it again retries the pass.

## BLOCKING (1)

...

## Summary

The PR introduces user authentication ...

**Passes**: ran adversarial, specialist-data-migration; gated out specialist-testing, specialist-performance, specialist-api-contract, specialist-maintainability; failed specialist-security
```

The same records travel in the `passes` array of the `--format json` output and
of the data block, and a [SARIF](#sarif) log names each failed pass in a
warning notification:

```json
"passes": [
  { "pass": "adversarial", "status": "ran" },
  {
    "pass": "specialist-security",
    "status": "failed",
    "error": "running security specialist review: claude (model opus): timed out after 1h0m0s (--claude-timeout)"
  },
  { "pass": "specialist-data-migration", "status": "ran" },
  { "pass": "specialist-testing", "status": "gated-out" }
]
```

`pass` is the label [`usage.passes`](#claude-usage-totals) records the pass's
sessions under, so a failed pass's cost and its sessions are found under the
same name. It is not the `confirmed_by` provenance label: a specialist's
findings carry `specialist:<key>` there.

An absent `passes` array means the run requested no secondary pass, or the
result was produced before pass recording existed. A result with a `failed`
entry is rendered and posted but never cached, so the next run of the same
commit retries every pass (see
[Caching model](/explanation/caching#what-every-key-is-scoped-to)).

## Excluded Files

With `--diff-exclusions`, the diff the review sessions read leaves out the
changed lock files, the changed files under a `vendor/` or `node_modules/`
directory, and the files whose leading comment says `Code generated … DO NOT
EDIT.`; tests outside a vendored directory never are (see
[The Diff Every Pass Reads](/explanation/review-methodology#the-diff-every-pass-reads)).
The Markdown report lists them in a note between the findings and the
Summary, one line per entry for at most 50 entries and then a count of the
rest:

```markdown
> [!NOTE]
> 14 changed file(s) were left out of the diff the review read (--diff-exclusions): lock files, vendored code, and generated code. Tests outside vendored directories are never left out.
> - `api/v1/zz_generated.deepcopy.go` (generated)
> - `go.sum` (lock file)
> - `vendor/` (vendored, 12 files)
```

Each path is written as in the Files Not Reviewed section: in a code span
that a backtick in the path cannot close, with a `<` shown as `&lt;`.

The `--format json` output and the data block carry every entry in
`excluded_files`. Each entry has a `path`, a `reason` (`lockfile`, `vendored`,
`generated`) and, for a vendored directory, whose `path` ends in `/`, `files`,
the number of changed files under it that were left out:

```json
"excluded_files": [
  { "path": "api/v1/zz_generated.deepcopy.go", "reason": "generated" },
  { "path": "go.sum", "reason": "lockfile" },
  { "path": "vendor/", "reason": "vendored", "files": 12 }
]
```

A [SARIF](#sarif) log names the same entries in a notification at level
`note`. An absent `excluded_files` means the exclusions were off or no changed
file matched them. Results cached before the field existed carry none.

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

## SARIF

`--format sarif` on `review` and `audit` writes one
[SARIF 2.1.0](https://docs.oasis-open.org/sarif/sarif/v2.1.0/sarif-v2.1.0.html)
log to stdout, for GitHub code scanning; for the workflow that uploads it, see
[Upload findings to code scanning](/how-to/use-the-github-action#upload-findings-to-code-scanning).
The log holds the findings `--min-severity` and `--min-confidence` keep, the
set `--format json` prints and in its order, as one run of the tool
`planwerk-agent`.

| SARIF field | Filled from |
|-------------|-------------|
| `ruleId` | `pattern/<slug>` of the finding's pattern name: lowercased, its letters and digits kept, and each run of other characters between them turned into one `-`, so `Hardcoded secrets` gives `pattern/hardcoded-secrets`. A finding that names no pattern gets `finding`. Each rule is listed once in `tool.driver.rules`, with the pattern name as its `shortDescription` |
| `level` | `error` for BLOCKING and CRITICAL, `warning` for WARNING, and `note` for INFO and for every finding of the Unverified / Low-Confidence section, whatever its severity |
| `message.text` | The title, then the problem |
| `locations[0]` | The file, as a URI relative to the repository root. `line` becomes `startLine`, and `line_end` becomes `endLine` when it ends after `line`; a finding without a line has no region |
| `partialFingerprints` | `planwerkFinding/v1`: a SHA-256 over the file, the rule, and the quoted code with its whitespace collapsed (the line when no code is quoted), so the alert survives a moved line. Repeats get `#1`, `#2`, and so on |
| `properties` | `finding_id`, `severity` (the four-level severity that `level` folds into three), `confidence`, `actionability`, and `unverified` |

A finding that names no file is not a result, because code scanning shows every
result in a file. It goes to `invocations[0].toolExecutionNotifications`
instead: one notification at level `warning` names every such finding by ID and
title. Each requested pass that failed adds one more `warning` notification,
`<pass> failed: <error>; the review is incomplete`, with the label and the
error its [Pass Status](#pass-status) entry records. With `--diff-exclusions`, a
last notification at level `note` names the files the diff the review read left
out, `<path> (<reason>)` for at most 50 entries and then a count of the rest
(see [Excluded Files](#excluded-files)). `executionSuccessful` is `true` either
way: a run whose review failed prints no log, and a failed secondary pass does
not fail the run.

The log is the whole of stdout, so `review` refuses `--post-review`, `--inline`,
and `--coverage-map` with `--format sarif`, and `audit` refuses
`--create-issues`, and `--capture-wiki` without `--yes`. With `--wiki` the
capture pass still runs, and its printed proposals are discarded, as under
`--format json`.

```bash
planwerk-agent --format sarif owner/repo#123 > results.sarif
planwerk-agent audit --format sarif owner/repo > audit.sarif
```

## Inline Review Mode (`--inline`)

With `--inline`, findings are posted as inline comments on the PR using the
GitHub Review API, beside the summary comment `--post-review` posts:

- Each finding that maps to a line in the PR diff becomes an inline comment on
  that line, when the summary lists it in a severity section. A finding below
  `--min-severity` or `--min-confidence` gets no inline comment, and neither
  does one in the Unverified / Low-Confidence section: an uncertain WARNING or
  INFO finding, or a BLOCKING or CRITICAL finding the claim verifier refuted.
  Both kinds stay out of the diff, and the Unverified ones remain in the review
  summary.
- At most 50 inline comments are posted per review; a finding the rules above
  leave out does not count toward the limit.
- Auto-fix findings with a `SuggestedFix` use GitHub's `suggestion` syntax, enabling one-click apply
- Each inline comment ends with two hidden markers:
  `<!-- planwerk-agent-finding:<fingerprint> -->`, by which a re-review traces a
  resolved thread to its finding (see [Re-reviews](#re-reviews)), and
  `<!-- planwerk-agent-inline -->`, by which `address` skips the tool's own
  threads
- The summary comment carries the whole report, the findings that do not map
  to a diff line among them, and the `<!-- planwerk-agent-data ... -->` block.
  It is the same single comment `--post-review` posts, edited in place on every
  run, so a pull request reviewed ten times has one summary. Only a comment the
  authenticated `gh` account wrote that opens with the report's `# Review:`
  heading is edited. A signed comment from another account, or one of its own
  that opens with other text, as another workflow's comment under the same
  Actions token can, is left alone, and a new summary comment is posted
  instead.
- A run adds a review only when it has inline comments to post. The review's
  body names the comment count and the head commit and links the summary
  comment.
- When the summary comment cannot be posted, the review carries the summary
  and the data block instead, and is posted even without inline comments.
  When the review cannot be posted, the summary comment already holds every
  finding, and the run succeeds.
- The PR diff is fetched and parsed to validate that finding lines are within the diff (right side)
- Implies `--post-review`

### Resolving outdated threads

With `--resolve-outdated` (the GitHub Action's `resolve-outdated` input),
an `--inline` run resolves, after posting, the inline threads earlier runs
opened that a later push made stale. It is off by default and refused without
`--inline`. It resolves a thread only when all of these hold:

- GitHub marks the thread outdated, and it is not resolved yet.
- Its first comment carries the inline signature and was posted by the
  account `gh` is authenticated as, or by a bot account when GitHub refuses
  to name that account to a GitHub App's token (an Actions token may refuse
  the lookup). A run under a personal token therefore resolves only the
  threads that token's runs opened. When the account cannot be read for any
  other reason, such as a timeout, the run logs a warning and resolves
  nothing.
- It holds no other comment. planwerk-agent never replies in a thread it
  opened, so any reply keeps the thread open, and so does a thread with more
  comments than the 100 the lookup reads.
- Its original line, on the commit it was posted at, is known.
- No finding of the current run on the same file shares a line with the
  thread's original lines, counting the findings the report or the inline
  comments leave out. A current finding that names the file without a line
  keeps every thread on that file open. A finding without a file keeps nothing
  open.

It resolves at most 50 threads per run, one at a time, a second apart; the rest
wait for the next run. A thread it resolves never dismisses its finding on a
later run (see [Re-reviews](#re-reviews)): the account that resolved it is the
one that posted it, or GitHub names none.

Resolving needs write access to the repository, `contents: write` for an
Actions token. When GitHub refuses a resolve, the run logs a warning, stops
resolving for this run, and the review still succeeds.

Each resolved thread is logged at info level with its file and original line.
Each thread kept open is logged at debug level (`-v`) with its reason
(`partial_view`, `not_ours`, `unverified_author`, `replied`,
`unlocatable_path`, `no_line`, or `overlap`), and a closing info line counts the
threads checked, the candidates, those resolved, and the kept-open reasons.

## Re-reviews

A posted review (`--post-review` or `--inline`) of a pull request planwerk-agent
reviewed before leaves two kinds of finding out of the report sections and
gives neither a new inline comment: the findings the previous review already
reported on a file that has not changed since, and the findings dismissed in a
resolved inline thread. A `[!NOTE]` alert at the end of the report lists both,
in a group each. The new data block still carries every finding, so the next
re-review can compare against them again.

Two findings are the same when they name the same file and quote the same code:
their `code_snippet`, compared with every run of whitespace collapsed to one
space. Neither the line nor the title counts, so a finding that moved down the
file or was reworded still matches. A finding without a `code_snippet` is
matched on its file and title instead. Matching is one for one: each earlier
finding leaves out at most one current finding, even when both the previous
data block and a resolved thread name it, or when later reviews posted it again
in threads of their own. Only a finding this run's
`--min-severity` and `--min-confidence` would show is left out, so one below
them never takes the place of the finding it repeats. When more current
findings match than there are earlier ones, the least severe are left out
first, and the most severe stay in the report.

**Previously reported on an unchanged file.** The review reads the data block
of the previous one:

- **Where the block comes from.** The most recently updated planwerk-agent post
  on the pull request that opens with the report's `# Review:` heading and
  carries a data block: an inline review's body or the summary comment,
  whichever changed last. A signed post without a data block, such as the
  capture pass's proposals comment, is passed over.
- **Whose post counts.** Only one that the authenticated `gh` user or a
  maintainer (`OWNER`, `MEMBER`, `COLLABORATOR`) wrote. Every post of the
  authenticated account counts as the tool's own. Under the Actions
  `GITHUB_TOKEN` that account is `github-actions[bot]`, which every workflow
  and every comment-posting action in the repository posts as, so a comment
  another of them makes from pull request text can supply the block. One that
  puts its own text first cannot, since the post must open with the heading;
  a verbatim copy of a pull request body that opens with the heading and ends
  in a forged block can. In a repository that runs such a workflow, give the
  review a token of an account nothing else posts as. A finding such a block
  leaves out is still listed in the `[!NOTE]` alert and kept in the new data
  block.
- **Which block in it counts.** Only the one appended at the end of the post,
  with nothing after it but the signature. A block that appears earlier, for
  example in a pull request title the post quotes, is ignored.
- **What is left out.** A current finding that is the same as one in that
  block, when its file has not changed since the block's `commit_sha`. A
  finding whose file changed is reported again, as a possible regression.
- **Which block findings count.** Only those that this run's `--min-severity`
  and `--min-confidence` place in a severity section of the summary; the block
  does not record the thresholds of the run that wrote it. A block finding
  below them, or one in the Unverified / Low-Confidence section, leaves nothing
  out. A finding that comes back confirmed or above the thresholds is listed in
  its severity section, with `--inline` as an inline comment too, and one that
  stays Unverified is listed in the Unverified section again.
- **When the block leaves nothing out.** No such post exists; the lookup fails;
  the newest such post's data block cannot be read, for example because the
  body was truncated or its `commit_sha` is not a hex commit SHA; or the diff
  since its `commit_sha` cannot be computed. An older post is not tried
  instead. Resolved threads are still read.
- **How far back it looks.** The lookup reads the last 100 comments and the last
  100 reviews, so it misses a summary comment that has more than 100 newer
  comments after it.

**Dismissed in a resolved thread.** Each inline comment ends with a hidden
`<!-- planwerk-agent-finding:<fingerprint> -->` marker. The fingerprint is the
first 16 hex digits of the SHA-256 of the finding's identity: its file and
whitespace-collapsed `code_snippet`, or its file and title. The review reads the
pull request's review threads and leaves out a current finding when all of
these hold:

- **The thread is planwerk-agent's.** Its first comment carries the inline
  signature and the finding's marker.
- **Someone else resolved it.** The account that resolved the thread is not the
  one that posted the comment. A thread resolved by the posting account, or one
  GitHub names no resolver for, as when the GitHub Actions bot resolves it,
  dismisses nothing. So when the review runs under a personal token, resolving
  its threads with that same token dismisses nothing; the unchanged-file rule
  above still applies.
- **The quoted code is still there.** The finding has a `code_snippet`, and the
  whole of it is still in the file as one block, with whitespace and `//`
  markers ignored, and with or without the leading `+`/`-` diff markers of a
  quote taken from the diff. This holds even when the push changed the file
  elsewhere. An edit to the quoted lines reports the finding again, unless the
  same code, whitespace and `//` markers aside, still appears elsewhere in the
  file.

A review posts a finding again when its file changed while its thread was
still open, so several resolved threads can carry one finding's marker. They
dismiss as many current findings as the most of them any single review opened:
one finding posted by three reviews dismisses one, and two findings on the same
code in one review dismiss two.

This needs no data block. A comment posted before the marker existed dismisses
nothing, and when the threads cannot be read, the review logs a warning and
dismisses nothing.
