# planwerk-review

[![CI](https://github.com/planwerk/planwerk-review/actions/workflows/ci.yml/badge.svg)](https://github.com/planwerk/planwerk-review/actions/workflows/ci.yml)
[![codecov](https://codecov.io/gh/planwerk/planwerk-review/branch/main/graph/badge.svg)](https://codecov.io/gh/planwerk/planwerk-review)

AI-powered code review and codebase analysis tool for GitHub repositories. Uses Claude Code to automatically analyze PR changes and produce structured review results, to analyze entire repositories and generate actionable feature proposals, to audit an entire codebase against all known review patterns, to elaborate high-level issues into detailed engineering plans, or to generate copy-paste-ready prompts that fix or implement an issue.

## Concept

### Overview

```
Review:
┌──────────────┐     ┌──────────────────┐     ┌───────────────┐     ┌──────────────┐
│  GitHub PR   │────▶│  planwerk-review │────▶│  Claude Code  │────▶│  Markdown    │
│  (URL/Ref)   │     │                  │     │  /review      │     │  Report      │
└──────────────┘     └──────────────────┘     └───────────────┘     └──────────────┘
                            │                                              │
                            ▼                                              ├──▶ stdout
                     ┌──────────────────┐                                  ├──▶ PR comment (--post-review)
                     │ Review Patterns  │                                  └──▶ Inline review (--inline)
                     │ (local + repo)   │
                     └──────────────────┘

Propose:
┌──────────────┐     ┌──────────────────┐     ┌───────────────┐     ┌──────────────┐
│  GitHub Repo │────▶│  planwerk-review │────▶│  Claude Code  │────▶│  Proposals   │
│  (URL/Ref)   │     │  propose         │     │  (analysis)   │     │  (MD/JSON)   │
└──────────────┘     └──────────────────┘     └───────────────┘     └──────────────┘
                            │                        │                      │
                            ▼                        ▼                      ▼
                     ┌──────────────────┐     ┌───────────────┐     ┌──────────────┐
                     │ Cache (SHA-based)│     │  Structure    │     │ --create-    │
                     │                  │     │  into JSON    │     │ issues (gh)  │
                     └──────────────────┘     └───────────────┘     └──────────────┘

Audit:
┌──────────────┐     ┌──────────────────┐     ┌───────────────┐     ┌──────────────┐
│  GitHub Repo │────▶│  planwerk-review │────▶│  Claude Code  │────▶│  Findings    │
│  (URL/Ref)   │     │  audit           │     │  (full scan)  │     │  (MD/JSON)   │
└──────────────┘     └──────────────────┘     └───────────────┘     └──────────────┘
                            │                        │
                            ▼                        ▼
                     ┌──────────────────┐     ┌───────────────┐
                     │ Review Patterns  │     │ Structure into│
                     │ (local + repo)   │     │ BLOCKING/…/   │
                     │                  │     │ INFO findings │
                     └──────────────────┘     └───────────────┘

Elaborate:
┌──────────────┐     ┌──────────────────┐     ┌───────────────┐     ┌──────────────┐
│  GitHub      │────▶│  planwerk-review │────▶│  Claude Code  │────▶│  Detailed    │
│  Issue       │     │  elaborate       │     │  (repo walk)  │     │  Issue Body  │
└──────────────┘     └──────────────────┘     └───────────────┘     └──────────────┘
                            │                        │                      │
                            ▼                        ▼                      ▼
                     ┌──────────────────┐     ┌───────────────┐     ┌──────────────┐
                     │ Cache (SHA+body) │     │  Structure    │     │ --update-    │
                     │                  │     │  into JSON    │     │ issue (gh)   │
                     └──────────────────┘     └───────────────┘     └──────────────┘

Prompt:
┌──────────────┐     ┌──────────────────┐     ┌──────────────────┐
│  GitHub      │────▶│  planwerk-review │────▶│  Claude Code     │
│  Issue       │     │  prompt          │     │  prompt (stdout) │
└──────────────┘     └──────────────────┘     └──────────────────┘
                            │
                            ▼
                     ┌──────────────────┐
                     │ Auto-mode by     │
                     │ severity marker  │
                     └──────────────────┘
```

### Review Workflow

1. **PR Input**: The tool receives a GitHub PR as input (URL or `owner/repo#number`).
2. **Checkout**: The PR is checked out locally (diff between base and head). PR title and description are fetched for scope analysis.
3. **Load Review Patterns**: Patterns are loaded from two sources:
   - the planwerk-review pattern catalog, embedded in the binary (source: `internal/patterns/patterns/`)
   - `.planwerk/review_patterns/` in the target repository (repo-specific patterns)
4. **Claude Code Review**: `claude /review` is executed with a structured prompt that includes persona framing, scope analysis, a two-pass checklist, suppression rules, and review patterns.
5. **Result Aggregation**: Review results are collected, deduplicated, categorized by severity, and classified by actionability. Findings are enriched with code snippets, suggested fixes, confidence levels, and cross-references.
6. **Output**: A structured report is written to `stdout`, optionally posted as a PR comment (`--post-review`), or posted as inline review comments on the PR diff (`--inline`).

### Review Methodology

The review prompt uses techniques inspired by [gstack](https://github.com/garrytan/gstack) to maximize review quality:

#### Staff Engineer Persona

Claude is instructed to review as a Staff Engineer, applying specific cognitive patterns:
- *"What happens at 10x scale?"* — Load, data volume, concurrent users
- *"What's the blast radius?"* — If this code fails, what else breaks?
- *"What happens at 3am?"* — Error paths, oncall clarity, log quality
- *"Would a new team member understand this?"* — Code clarity and intent
- *"Where are the tests?"* — Does every new behavior have a test?
- *"Would I find this in the docs?"* — Is this feature discoverable from documentation?

#### Scope Drift Detection

Before reviewing code quality, the tool checks for:
- **Scope Creep**: Files changed that are unrelated to the PR title/description
- **Missing Requirements**: Requirements from the PR description not addressed in the diff

#### Three-Pass Review Checklist

Claude works through a structured checklist in three passes:

| Pass | Focus | Categories |
|------|-------|------------|
| **Pass 1 — Critical** | Always checked | SQL & Data Safety, Race Conditions, Error Handling, Security, Input Validation, LLM Output Trust, Crypto |
| **Pass 2 — Semantic** | Requires tracing beyond the diff | Enum Completeness, Conditional Side Effects, Type Coercion, Test Coverage for New Code, Documentation Completeness |
| **Pass 3 — Informational** | Checked if time permits | Magic Numbers, Dead Code, Test Quality, Performance, API Contract, View/Frontend, Time Window |

#### Suppressions

To reduce false positives, the following are explicitly suppressed:
- TODO/FIXME comments with issue tracker references
- Missing tests for trivial getters/setters (does not suppress missing tests for functions with logic)
- Import ordering or formatting differences
- Variable naming matching existing project conventions
- Missing documentation on private functions (does not suppress missing docs for public APIs)
- Minor style preferences
- Code that was not changed in the diff (only added or modified lines are reviewed)

#### Test & Documentation Verification

After the checklist passes, the review explicitly verifies:
- **Test Completeness**: Every new or significantly modified function should have corresponding tests matching the project's testing conventions. The tool actively searches for all test categories: unit tests (`_test.go`, `test_*.py`, `*.spec.ts`), integration tests (`tests/integration/`), and E2E tests (`e2e/`, `chainsaw/`, `.chainsaw/`, `chainsaw-test.yaml`, kuttl). If the project uses multiple test types, new code must include matching tests for each category. Missing E2E tests are flagged separately from missing unit tests.
- **Documentation Completeness**: New public APIs, CLI flags, configuration options, and user-facing behavior changes must be reflected in documentation (README, CHANGELOG, doc comments).
- **New File Detection**: Newly added source files are flagged as candidates for documentation if they are not test files or internal configuration. Test file detection covers language-based conventions as well as infrastructure test patterns (Chainsaw, E2E directories).

#### Anti-Sycophancy Rules

Claude is instructed to be direct and decisive — no hedging with phrases like "you might want to consider" or "this could potentially cause". Every finding takes a clear position.

#### Actionability Classification

Each finding is classified by actionability:

| Classification | Meaning | Examples |
|----------------|---------|----------|
| **auto-fix** | A senior engineer would apply without discussion | Dead code, magic numbers, missing error wrapping |
| **needs-discussion** | Requires team input before fixing | Security decisions, API changes, behavioral changes |
| **architectural** | Needs a broader design conversation | Wrong abstraction, missing layer, significant refactor |

#### Adaptive Specialist Gating

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

### Propose Workflow

1. **Repo Input**: The tool receives a GitHub repository reference (URL or `owner/repo`).
2. **Clone**: The repository is cloned locally with a partial clone filter.
3. **Cache Check**: The default branch HEAD SHA is fetched via `git ls-remote`. If a cached result exists for this SHA, it is reused.
4. **Claude Analysis**: Claude performs a deep codebase analysis covering architecture, code quality, feature gaps, DX, performance, security, testing, and CI/CD.
5. **Structuring**: A second Claude call converts the raw analysis into structured JSON proposals with priority, category, scope, and acceptance criteria.
6. **Output**: Proposals are rendered as Markdown (default), JSON, or GitHub issue templates.
7. **Interactive Issue Creation** (optional): With `--create-issues`, the user is shown a summary table and walked through each proposal with a prompt to create a GitHub issue via `gh`.

### Audit Workflow

1. **Repo Input**: The tool receives a GitHub repository reference (URL or `owner/repo`).
2. **Clone**: The repository is cloned locally with a partial clone filter.
3. **Cache Check**: The default branch HEAD SHA is fetched via `git ls-remote`. If a cached result exists for this SHA and set of flags, it is reused.
4. **Technology Detection**: The clone is scanned for language/framework markers (Go, Python, Kubernetes, Helm, GitHub Actions, …) and patterns are filtered to those applicable.
5. **Pattern Load**: Patterns are loaded from the embedded catalog (source: `internal/patterns/patterns/`) and `.planwerk/review_patterns/` (repo-specific) — identical sources to the review command.
6. **Claude Audit**: Claude is instructed to apply EVERY loaded pattern to the ENTIRE current state of the codebase (not a diff) and emit concrete violations with file paths, line numbers, code snippets, and suggested fixes. Beyond patterns, it also flags BLOCKING/CRITICAL issues it encounters (security, data loss, broken error handling) and missing tests/docs matching the project's own conventions.
7. **Structuring**: A second Claude call converts the raw findings into the same structured JSON format used by the review command (`BLOCKING`/`CRITICAL`/`WARNING`/`INFO` with fix class, confidence, related findings).
8. **Output**: Findings are rendered as Markdown (default) or JSON, with an audit-specific verdict line (`Action required` / `Improvements suggested` / `Codebase healthy`) instead of the PR merge verdict.

### Gap Analysis Workflow

1. **Repo Input**: The tool receives a GitHub repository reference (URL or `owner/repo`).
2. **Cache Check**: The default-branch HEAD SHA is fetched first so a hit can short-circuit the clone. The cache key folds in `--feature` and `--file` so a single-feature run never overwrites the full-repo result.
3. **Clone**: On a miss, the repo is cloned locally with a partial filter.
4. **Spec Load**: Every `.json` under `.planwerk/completed/` is parsed via the existing Planwerk feature loader. `--feature CC-NNNN` filters by `feature_id`; `--file <path>` narrows to a single completed file (paths outside `.planwerk/completed/` are rejected — gap analysis runs only against features the team has declared done).
5. **Pattern Load**: The same pattern catalog used by `audit` / `review` / `propose` is loaded for context, but it is NOT the focus — the spec is.
6. **Claude Gap Analysis**: Claude compares each spec block (stories, requirements + scenarios, planned test specifications, completed tasks) against the actual codebase and reports four gap types: `missing_criterion`, `missing_scenario`, `missing_test`, and `missing_task`. Severity is mapped from the requirement priority (critical → CRITICAL, high/medium → WARNING, low → INFO; default WARNING). `BLOCKING` is never used because the work is already merged.
7. **Structuring**: A second Claude call converts the report into strict JSON grouped by `feature_id`, with one bucket per analyzed feature. Features the model omitted are surfaced with an empty `gaps` array so users see what was checked even when nothing is wrong.
8. **Output**: Gaps are rendered as a Markdown table plus per-feature detail sections (default), or as JSON. With `--create-issues`, the same interactive flow used by `audit` and `propose` walks each gap, dedupes against existing GitHub issues by title, and posts the model's `suggested_issue` (title + body) verbatim once the user confirms.

### Elaborate Workflow

1. **Issue Input**: The tool receives a GitHub issue reference (URL or `owner/repo#number`).
2. **Fetch Issue**: Title, body, URL, and state are fetched via `gh issue view`.
3. **Cache Check**: The default-branch HEAD SHA is resolved via `gh api graphql`. The cache key combines repo + HEAD + issue number + a fingerprint of the issue body, so the cache invalidates automatically when either the repo or the issue is edited.
4. **Clone**: On a cache miss, the repository is cloned locally.
5. **Pattern Load**: The same pattern catalog used by `review` / `audit` / `propose` is loaded, filtered by detected technologies.
6. **Claude Elaboration**: Claude is instructed to walk the repo first, identify what already exists vs. what the issue adds, and emit a detailed plan in six sections (Description with concrete "already exists / this story adds" boundaries, Motivation, Affected Areas, Acceptance Criteria, Non-Goals, References).
7. **Structuring**: A second Claude call converts the elaboration into a strict JSON schema so the final body renders consistently.
8. **Output**: The elaborated body is rendered as Markdown (default) or JSON. With `--update-issue`, the issue body is overwritten; with `--post-comment`, the elaboration is posted as a new comment.

### Prompt Workflow

1. **Issue Input**: A GitHub issue reference (URL or `owner/repo#number`).
2. **Fetch Issue**: Title, body, URL, and state are fetched via `gh issue view`.
3. **Mode Selection**: `auto` (default) inspects the issue body — audit findings carry a `**Severity**:` marker and get the "fix" prompt; everything else gets the "implement" prompt. Override with `--mode fix` or `--mode implement`.
4. **Prompt Assembly**: The runner deterministically assembles a prompt containing the agent workflow, rules (no scope creep, no `--no-verify`, run tests, update docs), and the issue metadata + body. No Claude call is made — the output is reproducible so it can be piped into other tools or diffed over time.
5. **Output**: The prompt is written to stdout, ready to paste into Claude Code or any other AI coding agent.

### Implement Workflow

1. **Issue Input**: A GitHub issue reference (URL or `owner/repo#number`), typically already elaborated via `elaborate`.
2. **Fetch Issue**: Title, body, URL, and state are fetched via `gh issue view`.
3. **Clone**: The repository is cloned into a temp directory (or the current checkout is used with `--local`).
4. **Pattern Load**: The same pattern catalog used by `review` / `audit` / `elaborate` is loaded, filtered by detected technologies.
5. **Planning Session**: A read-only Claude Code session on the dedicated planning model (`--plan-model`, default `fable`, env: `PLANWERK_PLAN_MODEL`) grounds the issue in the actual code — verifying every cited file and symbol — and emits a structured implementation plan: change set, commit sequence, test plan, documentation plan, verification commands, risks. A plan that reports `STATUS: BLOCKED` or `NEEDS_CONTEXT` aborts the run before any code is written. Skip the phase entirely with `--no-plan`.
6. **Implement Session**: A fresh Claude Code session in auto mode (`--permission-mode auto`) receives the plan embedded in its prompt and executes it end-to-end: code, tests, documentation, small reviewable commits on a fresh feature branch, draft pull request linked to the issue. Both sessions run at the global `--claude-effort` (default `xhigh`); the implement session uses the global `--claude-model` (default `opus`).
7. **Verification (optional)**: With `--verify`, an independent session diffs the feature branch against the issue's Acceptance Criteria without trusting the implementer's report.

Prompt escape hatches mirror the fix subcommand: `--print-plan-prompt` renders the planning prompt, `--print-prompt` the implement prompt (without a plan), and `--print-bare-prompt` a portable, self-contained variant for manual sessions.

### Local Mode

By default every repo-facing subcommand (`review`, `fix`, `implement`,
`propose`, `audit`, `gap-analysis`, `review-prepared`, `elaborate`) performs a
fresh `gh repo clone` into a temp directory and deletes it on exit. The
`--local` flag makes the command operate on the **current working directory**
instead — no clone, and the checkout is left in place when the command exits.

This unlocks three workflows the temp-dir clone blocks:

- **CI**: `actions/checkout` already populates the runner workspace, so a
  second clone of the same repo doubles the cold-start time and network cost.
  `--local` reuses the existing checkout. See [Local mode in CI](#local-mode-in-ci).
- **Local-first iteration**: review or fix unpushed commits and experimental
  branches without pushing them first.
- **Post-run inspection**: after `fix` or `implement` finishes, the working
  tree it operated on is still there to `cd` into and inspect — it is never
  `rm`-ed.

Semantics:

- **Reference inference.** The PR/repo reference may be omitted for the
  repo-facing commands: `review`/`fix` infer the PR from the current branch
  (via `gh pr view`); `propose`/`audit`/`gap-analysis`/`review-prepared` infer
  owner/repo from the `origin` remote. `elaborate` and `implement` still
  require their issue reference (you must name the issue) — only the repository
  checkout is taken locally. When a reference **is** given explicitly, its
  owner/repo must match the cwd's `origin`, otherwise the run aborts.
- **Branch left on.** For `review`/`fix` the working tree is switched to the
  PR head via `gh pr checkout` (no restore afterwards). The runner logs
  `working tree left on PR branch` so you know where you landed.
- **Dirty-tree gate.** If the working tree has uncommitted changes, `--local`
  asks for confirmation before doing anything. With `--force` it proceeds and
  logs a warning instead. In a non-interactive context (stdin is not a TTY,
  e.g. CI) a dirty tree aborts with an actionable error suggesting `--force` —
  the tool never silently stashes or discards your changes.
- **Never deletes your tree.** The cleanup step that removes a temp-dir clone
  is a no-op in local mode, so there is no code path that can `rm -rf` your
  working directory.
- **`fix` loop.** Each fix iteration fast-forwards the existing checkout with
  `git pull --ff-only` instead of re-cloning, which is materially cheaper and
  produces the same state for the next Claude session.

```bash
# Review the PR for the current branch, using this checkout
planwerk-review --local

# Audit the repo whose origin is this checkout (no clone)
planwerk-review audit --local

# Fix the current branch's PR, proceeding even if the tree is dirty
planwerk-review fix --local --force
```

`--local` does not change cache behavior, `gh` authentication, or the
`--patterns` flag. It also does not (yet) accept an arbitrary directory other
than the current one.

### CLI Interface

#### Review (default command)

```bash
# Simple invocation with PR URL
planwerk-review https://github.com/owner/repo/pull/123

# Short form with owner/repo#number
planwerk-review owner/repo#123

# With explicit pattern directory
planwerk-review --patterns ./custom-patterns owner/repo#123

# Only output specific severity levels
planwerk-review --min-severity warning owner/repo#123

# Post review as inline comments on the PR
planwerk-review --inline owner/repo#123

# Write output to file
planwerk-review owner/repo#123 > review.md
```

##### Flags

| Flag | Description | Default |
|------|-------------|---------|
| `--patterns` | Additional pattern source: local directory, `github:owner/repo[/sub][@ref]`, or `git+https://…[#ref[:sub]]` (see [Remote Pattern Sources](#remote-pattern-sources)) | - |
| `--min-severity` | Minimum severity level for output (`info`, `warning`, `critical`, `blocking`) | `info` |
| `--no-repo-patterns` | Ignore repo-specific patterns | `false` |
| `--no-local-patterns` | Ignore local patterns from the tool | `false` |
| `--no-cache` | Ignore cache, force a fresh review | `false` |
| `--clear-cache` | Clear all cached reviews and exit | `false` |
| `--format` | Output format (`markdown`, `json`) | `markdown` |
| `--post-review` | Post review as a comment on the PR (updates existing if found) | `false` |
| `--inline` | Post review with inline comments using GitHub Review API (implies `--post-review`) | `false` |
| `--thorough` | Run additional adversarial review pass for security and failure modes | `false` |
| `--coverage-map` | Generate test coverage map for changed functions | `false` |
| `--specialists` | Run the domain-specialist review fan-out (security, data-migration, testing, performance, api-contract, maintainability) concurrently and merge their findings. Specialists are adaptively gated: one whose relevant paths the diff never touches is skipped with a log line, while `security` and `data-migration` always run (see [Adaptive Specialist Gating](#adaptive-specialist-gating)). | `false` |
| `--max-patterns` | Max review patterns injected into the prompt (`<=0` disables truncation; see [Configuration File](#configuration-file) for precedence with `PLANWERK_MAX_PATTERNS`) | `0` (unlimited) |
| `--max-findings` | Cap on findings returned (`<=0` disables cap) | `0` |
| `--local` | Operate on the current working directory instead of cloning into a temp dir (see [Local Mode](#local-mode)). The PR reference may be omitted — it is inferred from the current branch. | `false` |
| `--force` | With `--local`, skip the confirmation prompt when the working tree is dirty | `false` |

##### Global Flags

These flags apply to every `planwerk-review` command (`review`, `propose`, `audit`):

| Flag | Description | Default |
|------|-------------|---------|
| `--verbose`, `-v` | Enable debug-level logging (also shows verbose build info with `--version`) | `false` |
| `--log-format` | Log output format: `text` (human-friendly, default) or `json` (one JSON object per record, CI-friendly) | `text` |
| `--remote-patterns-ttl` | Refresh interval for remote pattern sources (env: `PLANWERK_REMOTE_PATTERNS_TTL`; `<=0` disables refresh once cached). See [Remote Pattern Sources](#remote-pattern-sources). | `24h` |
| `--claude-timeout` | Maximum duration for a single Claude Code invocation. Applies to every Claude call across all subcommands (`review`, `propose`, `audit`, `elaborate`, `implement`, `fix`, `gap-analysis`, `review-prepared`, and their `*-structure` / `*-repair` follow-ups). Accepts any `time.ParseDuration` value (e.g. `20m`, `1h30m`). Must be `> 0` — disabling the timeout would let a stuck `claude` process hang the CLI indefinitely. Env: `PLANWERK_CLAUDE_TIMEOUT`. | `15m` |
| `--show-claude-output` | Stream Claude Code's live output (assistant messages and tool activity) to stderr while a run is in flight, instead of only the periodic heartbeat. Each line is prefixed with the call label (`[review]`, `[structure]`, `[adversarial]`, …). When stderr is not a terminal, the same events are emitted as `slog.Info` records so they integrate with `--log-format json`. Env: `PLANWERK_SHOW_CLAUDE_OUTPUT` (truthy: `1`, `true`, `yes`, `on`). | `false` |
| `--claude-model` | Model passed to Claude Code via `--model` for every Claude call across all subcommands. Accepts any value Claude Code understands — a short alias (`opus`, `fable`, `sonnet`) or a full model ID (`claude-fable-5`). The default `opus` alias tracks the latest Opus release; set `fable` to run reviews on Fable. Unknown names are rejected by Claude Code, not by this flag. Env: `PLANWERK_CLAUDE_MODEL`. | `opus` |
| `--claude-effort` | Reasoning effort passed to Claude Code via `--effort` for every Claude call across all subcommands. One of `low`, `medium`, `high`, `xhigh`, `max`. The default `xhigh` is Claude Code's own default and the recommended setting for coding and agentic workloads (`max` buys little on top and tends toward overthinking); lower it to trade thoroughness for speed/cost. Env: `PLANWERK_CLAUDE_EFFORT`. | `xhigh` |

Logs are written to stderr; when stderr is not a terminal, Claude-invocation heartbeats are still emitted at INFO level so long-running runs are visible in CI log streams.

#### Propose (subcommand)

```bash
# Analyze a repository and generate feature proposals
planwerk-review propose https://github.com/owner/repo

# Short form
planwerk-review propose owner/repo

# Output as JSON
planwerk-review propose --format json owner/repo

# Output as GitHub issue templates
planwerk-review propose --format issues owner/repo

# Force fresh analysis (ignore cache)
planwerk-review propose --no-cache owner/repo

# Interactively create GitHub issues from proposals
planwerk-review propose --create-issues owner/repo

# Write proposals to file
planwerk-review propose owner/repo > proposals.md
```

##### Flags

| Flag | Description | Default |
|------|-------------|---------|
| `--patterns` | Additional pattern source: local directory, `github:owner/repo[/sub][@ref]`, or `git+https://…[#ref[:sub]]` (see [Remote Pattern Sources](#remote-pattern-sources)) | - |
| `--no-repo-patterns` | Ignore repo-specific patterns | `false` |
| `--no-local-patterns` | Ignore local patterns from the tool | `false` |
| `--no-cache` | Ignore cache, force a fresh analysis | `false` |
| `--format` | Output format (`markdown`, `json`, `issues`) | `markdown` |
| `--max-patterns` | Max review patterns injected into the prompt (`<=0` disables truncation; see [Configuration File](#configuration-file) for precedence with `PLANWERK_MAX_PATTERNS`) | `0` (unlimited) |
| `--create-issues` | Interactively create GitHub issues from proposals | `false` |
| `--no-issue-dedupe` | Do not filter proposals whose title matches an existing GitHub issue | `false` |
| `--local` | Operate on the current working directory instead of cloning into a temp dir (see [Local Mode](#local-mode)). The repository reference may be omitted — it is inferred from the `origin` remote. | `false` |
| `--force` | With `--local`, skip the confirmation prompt when the working tree is dirty | `false` |

Proposals are grounded in the same review-pattern catalog used by `review` and
`audit`. Patterns load from the tool's embedded catalog, any
`--patterns` directories you supply, and the target repo's
`.planwerk/review_patterns/`. When a proposal addresses a pattern (closes a
gap, hardens against a violation, or extends coverage) Claude references the
pattern by name so reviewers can trace the rationale back to the catalog.

#### Audit (subcommand)

```bash
# Apply all loaded review patterns to an entire codebase
planwerk-review audit https://github.com/owner/repo

# Short form
planwerk-review audit owner/repo

# Only output findings at or above a severity threshold
planwerk-review audit --min-severity warning owner/repo

# JSON output for tooling
planwerk-review audit --format json owner/repo

# Force fresh audit (ignore cache)
planwerk-review audit --no-cache owner/repo

# Cap the number of findings Claude returns
planwerk-review audit --max-findings 25 owner/repo

# Write findings to file
planwerk-review audit owner/repo > audit.md
```

##### Flags

| Flag | Description | Default |
|------|-------------|---------|
| `--patterns` | Additional pattern source: local directory, `github:owner/repo[/sub][@ref]`, or `git+https://…[#ref[:sub]]` (see [Remote Pattern Sources](#remote-pattern-sources)) | - |
| `--min-severity` | Minimum severity level for output (`info`, `warning`, `critical`, `blocking`) | `info` |
| `--no-repo-patterns` | Ignore repo-specific patterns | `false` |
| `--no-local-patterns` | Ignore local patterns from the tool | `false` |
| `--no-cache` | Ignore cache, force a fresh audit | `false` |
| `--format` | Output format (`markdown`, `json`) | `markdown` |
| `--max-patterns` | Max review patterns injected into the prompt (`<=0` disables truncation; see [Configuration File](#configuration-file) for precedence with `PLANWERK_MAX_PATTERNS`) | `0` (unlimited) |
| `--max-findings` | Cap on findings returned (`<=0` disables cap) | `0` |
| `--create-issues` | Interactively create GitHub issues from findings | `false` |
| `--issue-min-severity` | Minimum severity for issue creation | `warning` |
| `--no-issue-dedupe` | Do not filter findings whose title matches an existing GitHub issue | `false` |
| `--local` | Operate on the current working directory instead of cloning into a temp dir (see [Local Mode](#local-mode)). The repository reference may be omitted — it is inferred from the `origin` remote. | `false` |
| `--force` | With `--local`, skip the confirmation prompt when the working tree is dirty | `false` |

#### Gap Analysis (subcommand)

Compare every Planwerk feature file under `.planwerk/completed/` in the
target repo against the actual codebase and report incomplete
implementations. Useful when you want to verify that "completed" features
really are complete: missing acceptance criteria, scenarios that are not
honored by code, planned tests that were never written, or tasks marked
done whose description is not visible anywhere.

```bash
# Audit every completed feature in the repo
planwerk-review gap-analysis owner/repo

# Single feature by ID
planwerk-review gap-analysis --feature CC-0042 owner/repo

# Single feature by file (path or basename, must be under .planwerk/completed/)
planwerk-review gap-analysis --file CC-0042-thing.json owner/repo

# JSON output for automation
planwerk-review gap-analysis --format json owner/repo

# Walk the gaps interactively and create GitHub issues for the ones you select
planwerk-review gap-analysis --create-issues owner/repo
```

##### Flags

| Flag | Description | Default |
|------|-------------|---------|
| `--patterns` | Additional pattern source: local directory, `github:owner/repo[/sub][@ref]`, or `git+https://…[#ref[:sub]]` | - |
| `--no-repo-patterns` | Ignore repo-specific patterns | `false` |
| `--no-local-patterns` | Ignore local patterns from the tool | `false` |
| `--no-cache` | Ignore cache, force a fresh gap analysis | `false` |
| `--cache-max-age` | Reject cached entries older than this duration (`0` disables the TTL) | `720h` |
| `--format` | Output format (`markdown`, `json`) | `markdown` |
| `--max-patterns` | Max review patterns injected into the prompt (`<=0` disables truncation) | `0` (unlimited) |
| `--feature` | Limit analysis to a single feature by `feature_id` (e.g. `CC-0042`) | - |
| `--file` | Limit analysis to a single feature file under `.planwerk/completed/` (path or basename) | - |
| `--create-issues` | Interactively create GitHub issues from gaps | `false` |
| `--no-issue-dedupe` | Do not filter gaps whose suggested-issue title matches an existing GitHub issue | `false` |
| `--local` | Operate on the current working directory instead of cloning into a temp dir (see [Local Mode](#local-mode)). The repository reference may be omitted — it is inferred from the `origin` remote. | `false` |
| `--force` | With `--local`, skip the confirmation prompt when the working tree is dirty | `false` |

`--feature` and `--file` may be combined as a sanity check; if the file's
`feature_id` does not match `--feature`, the run aborts before invoking
Claude.

#### Elaborate (subcommand)

Take a high-level GitHub issue (typically the output of `propose` or
`audit`) and expand it into a deeply detailed engineering plan grounded in
the actual repository state — the kind of issue body a senior engineer can
pick up and execute without further clarification (mirrors the structure
shown in [plexsphere/plexsphere#10](https://github.com/plexsphere/plexsphere/issues/10):
Description with concrete "already exists / this story adds" boundaries,
Motivation, Affected Areas, Acceptance Criteria, Non-Goals, References).

```bash
# Render the elaborated body to stdout
planwerk-review elaborate https://github.com/owner/repo/issues/123

# Short form
planwerk-review elaborate owner/repo#123

# JSON for automation
planwerk-review elaborate --format json owner/repo#123

# Replace the issue body with the elaborated body
planwerk-review elaborate --update-issue owner/repo#123

# Or post the elaboration as a new comment instead
planwerk-review elaborate --post-comment owner/repo#123
```

##### Flags

| Flag | Description | Default |
|------|-------------|---------|
| `--patterns` | Additional pattern source: local directory, `github:owner/repo[/sub][@ref]`, or `git+https://…[#ref[:sub]]` (see [Remote Pattern Sources](#remote-pattern-sources)) | - |
| `--no-repo-patterns` | Ignore repo-specific patterns | `false` |
| `--no-local-patterns` | Ignore local patterns from the tool | `false` |
| `--no-cache` | Ignore cache, force a fresh elaboration | `false` |
| `--cache-max-age` | Reject cached entries older than this duration (`0` disables the TTL) | `720h` |
| `--format` | Output format (`markdown`, `json`) | `markdown` |
| `--max-patterns` | Max review patterns injected into the prompt (`<=0` disables truncation) | `0` (unlimited) |
| `--update-issue` | Replace the issue body with the elaborated body via `gh issue edit` | `false` |
| `--post-comment` | Post the elaborated body as a new issue comment via `gh issue comment` | `false` |
| `--local` | Ground the elaboration in the current working directory instead of cloning into a temp dir (see [Local Mode](#local-mode)). The issue reference is still required — only the repository checkout is local. | `false` |
| `--force` | With `--local`, skip the confirmation prompt when the working tree is dirty | `false` |

`--update-issue` and `--post-comment` are mutually exclusive — pick the one
that matches your team's workflow (overwrite the source issue vs. preserve
history and append a follow-up comment).

#### Prompt (subcommand)

Generate a copy-paste-ready Claude Code prompt for an existing GitHub issue
— either to fix an audit finding or to implement a proposal/elaborated
issue. No Claude call is involved; the prompt is a deterministic assembly
so the output is stable and safe to pipe into other tools.

```bash
# Auto-detected mode (audit titles get the fix prompt, others the implement prompt)
planwerk-review prompt https://github.com/owner/repo/issues/42

# Force the fix variant
planwerk-review prompt --mode fix owner/repo#42

# Force the implement variant
planwerk-review prompt --mode implement owner/repo#42

# Pipe straight into the clipboard (macOS)
planwerk-review prompt owner/repo#42 | pbcopy
```

Mode auto-detection looks at the issue body: audit-generated issues carry a
`**Severity**:` marker and get the "fix" prompt, everything else gets the
"implement" prompt.

##### Flags

| Flag | Description | Default |
|------|-------------|---------|
| `--mode` | Prompt variant (`auto`, `fix`, `implement`) | `auto` |

#### Cache (subcommand)

Inspect the on-disk cache shared by `review`, `propose`, `audit`, `elaborate`, and `gap-analysis`:

```bash
# Show total entries, size, age distribution, and per-command breakdown
planwerk-review cache stats

# Dump metadata and pretty-printed payload for one key (keys come from `cache stats`)
planwerk-review cache inspect <key>
```

`cache stats` surfaces which commands dominate the cache and how stale entries
are — useful before running `--clear-cache` to decide whether you actually need
a full wipe. `cache inspect <key>` shows the cached command, `writtenAt`, age,
size, and the full JSON payload for a single entry, so you can confirm what
would be reused on the next run without rerunning the analysis.

#### Existing-Issue Dedupe

Before rendering, both `propose` and `audit` query the target repo's GitHub
issues (open and closed) once via `gh issue list` and drop any
proposal/finding whose title matches an existing issue. This keeps repeated
runs idempotent: work that's already tracked upstream disappears from every
output format — Markdown, JSON, `--format=issues`, and the interactive
`--create-issues` flow.

Matching is case-insensitive, trims surrounding whitespace, collapses internal
whitespace, and ignores trailing punctuation (`.`, `!`, `?`, `,`, `;`, `:`).
Audit-issue titles no longer carry a `[SEVERITY]` prefix, so severity drift
between runs does not split a finding into a new duplicate. If `gh issue list`
fails, dedupe is skipped with a warning and the pipeline continues.

Pass `--no-issue-dedupe` (on either subcommand) to disable the filter for
debugging or when you want to see the full candidate list regardless of
upstream state.

### Configuration File

For repos that run `review`, `propose`, or `audit` repeatedly with the same
flags, defaults can be pinned in `.planwerk/config.yaml`. The file is loaded
from the current working directory if present — so dropping it at the repo
root lets teams standardize conventions once instead of repeating flags in
every CI invocation and local run.

#### Precedence

Values are resolved in this order (highest wins):

1. **Command-line flag** — `--min-severity`, `--max-patterns`, etc.
2. **Config file** — `.planwerk/config.yaml` entries.
3. **Environment variable** — e.g. `PLANWERK_MAX_PATTERNS`.
4. **Compiled-in default** — what you get with no config at all.

Only fields explicitly set in the file override the lower tiers; absent keys
fall through. A malformed file (bad YAML or unknown keys) is a hard error so
that typos surface immediately rather than silently running with the wrong
settings.

#### Schema

```yaml
# .planwerk/config.yaml
review:
  min-severity: warning        # info | warning | critical | blocking
  max-patterns: 40             # <=0 disables truncation
  max-findings: 25             # <=0 disables cap
  format: markdown             # markdown | json
  patterns:
    - ./custom-review-patterns

propose:
  max-patterns: 60
  format: issues               # markdown | json | issues
  patterns: []

audit:
  min-severity: warning        # info | warning | critical | blocking
  issue-min-severity: critical # default: warning
  max-patterns: 40
  max-findings: 50
  format: markdown             # markdown | json
  patterns: []
```

All keys are optional. Flags beyond `--min-severity`, `--max-patterns`,
`--max-findings`, `--format`, and `--patterns` (the high-churn ones) remain
CLI-only to keep the config surface small; boolean toggles like
`--post-review`, `--inline`, `--thorough`, and `--no-cache` stay on the
command line where they belong.

### Shell Completions & Man Pages

Completions for `bash`, `zsh`, `fish`, and `powershell` are emitted via Cobra's built-in `completion` subcommand:

```bash
# Load completions for the current shell session (bash)
source <(planwerk-review completion bash)

# Install persistently (zsh, Homebrew example)
planwerk-review completion zsh > "$(brew --prefix)/share/zsh/site-functions/_planwerk-review"

# Fish
planwerk-review completion fish > ~/.config/fish/completions/planwerk-review.fish
```

When installed from Homebrew, deb, or rpm packages, completions and man pages (`man planwerk-review`) are installed automatically. Packages are produced by `goreleaser` — see `.goreleaser.yml`.

For local development, regenerate the artifacts into `completions/` and `docs/man/`:

```bash
make completions
make man
```

### GitHub Action

The repo ships a composite GitHub Action at the root (`action.yml`) that wraps
the `review` command for use on pull requests. It installs Claude Code,
downloads the planwerk-review release binary, runs the review against the PR
that triggered the workflow, and posts a summary plus inline review comments.

Minimal example workflow:

```yaml
name: Planwerk Review

on:
  pull_request:

permissions:
  contents: read
  pull-requests: write

jobs:
  review:
    runs-on: ubuntu-latest
    steps:
      - uses: Planwerk/planwerk-review@v1
        with:
          anthropic-api-key: ${{ secrets.ANTHROPIC_API_KEY }}
```

The major-version tag (`@v1`) follows the standard GitHub Action convention and
is updated alongside each minor/patch release. To pin a specific version, use
the `version` input or a full tag (`Planwerk/planwerk-review@v1.2.3`).

#### Inputs

| Input | Description | Default |
|-------|-------------|---------|
| `pr-ref` | PR reference (URL, `owner/repo#number`, or bare PR number for the current repo) | the PR that triggered the workflow |
| `patterns` | Comma-separated additional pattern directories | `""` |
| `min-severity` | Minimum severity to report (`info`, `warning`, `critical`, `blocking`) | `info` |
| `format` | Output format written to the action log (`markdown`, `json`); posting always uses markdown | `markdown` |
| `max-findings` | Cap on findings returned (`0` disables cap) | `0` |
| `post-inline` | Post inline review comments and a summary via the GitHub Review API | `true` |
| `thorough` | Run the additional adversarial review pass | `false` |
| `local` | Review the repository `actions/checkout` already placed in the runner workspace (passes `--local`) instead of cloning it a second time. See [Local mode in CI](#local-mode-in-ci). | `false` |
| `version` | planwerk-review release tag to install (`latest` resolves to the most recent release) | `latest` |
| `binary-path` | Path to a pre-built binary; skips the download step (used by the in-repo smoke test) | `""` |
| `github-token` | Token used to fetch PR data and post review comments (`pull-requests: write`) | `${{ github.token }}` |
| `anthropic-api-key` | Anthropic API key consumed by Claude Code in non-interactive mode (**required**) | — |

#### Outputs

| Output | Description |
|--------|-------------|
| `findings-count` | Total number of findings reported |
| `blocking-count` | Number of `BLOCKING` findings |
| `critical-count` | Number of `CRITICAL` findings |
| `warning-count` | Number of `WARNING` findings |
| `info-count` | Number of `INFO` findings |

Counts are extracted by parsing the `<!-- planwerk-review-data ... -->` JSON
block embedded in the posted PR review/comment, so they reflect the same set
of findings the reviewer sees on the PR.

The action is exercised end-to-end on every relevant PR via
`.github/workflows/action-smoke.yml`, which builds the binary from source and
runs the action with `binary-path` pointing at the dev build. The smoke job is
gated on `pull_request.head.repo.full_name == github.repository` so forked PRs
(which cannot read `secrets.ANTHROPIC_API_KEY`) skip cleanly.

#### Local mode in CI

`actions/checkout` already places the repository in the runner workspace before
the action runs, so the default behavior clones the same repo a second time
into a temp dir — on a moderate repo this doubles the cold-start time and the
dominant network cost. Set `local: true` to point planwerk-review at the
existing checkout instead (it passes `--local`), skipping the redundant clone:

```yaml
jobs:
  review:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: Planwerk/planwerk-review@v1
        with:
          local: true
          anthropic-api-key: ${{ secrets.ANTHROPIC_API_KEY }}
```

The action still passes the PR reference explicitly, so `--local` validates it
against the checkout's `origin` and switches the working tree to the PR head
via `gh pr checkout`. The default stays `false` so existing workflows are
unaffected. See [Local Mode](#local-mode) for the full semantics.

### Output Format

The generated Markdown report follows a fixed structure:

```markdown
# Review: owner/repo#123

> *Feature: Add user authentication*
> Reviewed by planwerk-review vX.Y.Z with Claude Code

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

#### Severity Levels

| Level | Meaning | Action |
|-------|---------|--------|
| **BLOCKING** | Fundamental architecture/security issues | PR must not be merged |
| **CRITICAL** | Bugs, security vulnerabilities, severe problems | Must be fixed before merge |
| **WARNING** | Code quality, potential issues | Should be fixed |
| **INFO** | Style questions, improvement suggestions | Optional, for information |

#### Actionability Levels

| Level | Meaning | Action |
|-------|---------|--------|
| **auto-fix** | A senior engineer would fix without discussion | Apply the suggested fix directly |
| **needs-discussion** | Requires team input before fixing | Discuss in PR comments or team sync |
| **architectural** | Fundamental design issue | Needs broader design conversation |

#### Enriched Finding Fields

Each finding includes additional metadata for tooling and automation:

| Field | Description |
|-------|-------------|
| **FixClass** | `AUTO-FIX` or `ASK` — derived from Actionability, indicates whether the fix can be applied directly |
| **Confidence** | `verified`, `likely`, or `uncertain` — how certain the reviewer is about the finding |
| **CodeSnippet** | The relevant code fragment from the diff |
| **SuggestedFix** | Concrete replacement code for auto-fix findings |
| **RelatedTo** | IDs of related findings (e.g., `["B-001", "C-003"]`) |
| **LineEnd** | End line for multi-line findings (enables line-range comments) |

#### Machine-Readable Output

The Markdown report includes an HTML comment with counts and recommendation verdict for machine consumption:

```html
<!-- planwerk-review: blocking=1 critical=2 warning=0 info=3 recommendation=HOLD -->
```

Verdict values: `HOLD` (blockers/criticals present), `REVIEW` (warnings only), `MERGE` (clean), `CUSTOM` (manual recommendation).

Recommendations use GitHub Alert syntax (`[!CAUTION]`, `[!WARNING]`, `[!TIP]`, `[!IMPORTANT]`) for native rendering.

#### Inline Review Mode (`--inline`)

With `--inline`, findings are posted as inline comments on the PR using the GitHub Review API instead of (or in addition to) a single summary comment:

- Each finding that maps to a line in the PR diff becomes an inline comment on that line
- Auto-fix findings with a `SuggestedFix` use GitHub's `suggestion` syntax, enabling one-click apply
- Findings that cannot be mapped to diff lines are included in the review summary body
- The PR diff is fetched and parsed to validate that finding lines are within the diff (right side)
- Implies `--post-review`

### Review Patterns

Review Patterns are structured rules that systematically improve the review. They codify knowledge from past reviews and make it reusable.

#### Pattern Sources (lowest to highest priority)

Patterns are resolved from up to five tiers. Later tiers override earlier ones
by pattern name, so the more specific source wins on a name collision:

1. **Embedded catalog** — a compile-time copy of `internal/patterns/patterns/`
   baked into the binary with `//go:embed`. It is always present, so every
   install method (`go install`, raw `go build`, release archives, OS packages)
   produces a self-contained binary that loads the full catalog with no external
   files. This is the lowest-priority source; any on-disk source below overrides
   an embedded pattern of the same name.

2. **Bundled on-disk catalog** (`<binDir>/../patterns`) — an optional copy next
   to the installed binary. Lets a distribution ship the catalog as a separately
   updatable data file (so pattern fixes can land without rebuilding the binary);
   when present it overrides the embedded copy.

3. **Working-directory catalog** (`./patterns`) — picked up when running from a
   planwerk-review checkout during development of the tool itself.

4. **Repo-specific Patterns** (`.planwerk/review_patterns/*.md` in the target repo)
   - Created and maintained by the development team (Planwerk) themselves
   - Contain repo-specific knowledge (e.g., "In this repo, all DB queries must go through the QueryBuilder")
   - Versioned with the repository
   - Suppressed independently by `--no-repo-patterns`

5. **Explicit / Remote Patterns** (passed via `--patterns <URI>` or the config file)
   - Local directories or remote URIs — lets a team maintain a single, shared pattern catalog in a separate repository instead of vendoring it into every consuming repo
   - Remote sources are cloned into a per-user cache on first use and refreshed by TTL
   - Highest priority: override every tier above on a name collision
   - See [Remote Pattern Sources](#remote-pattern-sources) below for URI forms, caching, and authentication

`--no-local-patterns` suppresses the first three tiers — the embedded catalog
and both on-disk tool copies (`<binDir>/../patterns` and `./patterns`) — leaving
only repo-specific and `--patterns` sources. `--no-repo-patterns` independently
drops tier 4.

#### Remote Pattern Sources

Any value passed to `--patterns` (or the `patterns:` array in `.planwerk/config.yaml`) may be either a local directory or a remote URI. Two URI forms are accepted:

```text
github:owner/repo[/subpath][@ref]              # GitHub shorthand
git+https://host.example/group/repo.git[#ref[:subpath]]   # any git host
```

Examples:

```bash
# Default branch of a GitHub repo
planwerk-review --patterns github:planwerk/patterns owner/repo#123

# Pinned tag, sub-directory inside the repo
planwerk-review --patterns github:planwerk/patterns/security@v1.2.3 owner/repo#123

# Generic git URL with ref + subpath (separator: ":" inside the fragment)
planwerk-review --patterns git+https://gitlab.example.com/team/p.git#main:patterns/web owner/repo#123

# Mix local + remote, in priority order
planwerk-review --patterns ./local-overrides --patterns github:planwerk/patterns owner/repo#123
```

Anything that doesn't match `github:` or `git+http(s)://` is treated as a local path, so existing usage is unchanged.

**Caching.** Remote sources are cloned into `<UserCacheDir>/planwerk-review/patterns/<hash>/repo/` (typically `~/.cache/planwerk-review/patterns/…` on Linux, `~/Library/Caches/planwerk-review/patterns/…` on macOS). A neighbouring `meta.json` records when the clone was last refreshed. The cache is keyed by the URI (excluding the subpath), so two URIs that differ only in their subpath share the same checkout.

**Refresh TTL.** Cached clones are refreshed when older than `--remote-patterns-ttl` (default `24h`, env: `PLANWERK_REMOTE_PATTERNS_TTL`). Setting `--remote-patterns-ttl 0` disables refresh entirely — once cached, the clone is reused indefinitely (useful for offline / air-gapped environments). On refresh the existing checkout is removed and re-cloned; this keeps the cache logic simple and is cheap because pattern repos are small.

**Authentication.**

| Form | How auth works |
|------|----------------|
| `github:owner/repo` | Cloned via `gh repo clone`, which uses your `gh auth login` credentials or the `GH_TOKEN` env var. Private GitHub repos work transparently if you can already access them with `gh`. |
| `git+https://…` | Cloned via plain `git clone`. Standard git credential helpers (`~/.git-credentials`, `git config credential.helper`) apply. For env-var-based auth, embed the token directly in the URI using shell-style `${VAR}` expansion: `git+https://oauth2:${MY_TOKEN}@gitlab.example.com/team/p.git`. The expansion runs before `git clone` is invoked. |

#### Prompt Budget

By default, all loaded patterns are injected into the prompt without truncation (`--max-patterns 0`, env: `PLANWERK_MAX_PATTERNS`). To cap pattern injection — e.g. to keep prompts within Claude's context window — set `--max-patterns` to a positive integer. When more patterns are loaded than the budget allows, the tool keeps the highest-priority patterns by severity (`BLOCKING` > `CRITICAL` > `WARNING` > `INFO`) and prints a warning to stderr.

#### Pattern Format

```markdown
# Review Pattern: <Pattern Name>

**Review-Area**: <architecture|security|quality|testing|workflow|...>
**Detection-Hint**: <Description of when/how this pattern should be detected>
**Severity**: <BLOCKING|CRITICAL|WARNING|INFO>
**Occurrences**: <Number of previous findings>

## What to check

<Detailed description of what to check>

## Why it matters

<Explanation of why this pattern is important>

## Examples from external reviews

### <ID> — <Source>
- **Feedback**: <Concrete feedback from an actual review>
- **What was missed**: <What was overlooked>
- **Fix**: <How it was fixed>
```

#### Knowledge Building

The tool systematically builds knowledge over time:

```
First Review           Subsequent Reviews       Mature System
────────────          ────────────────────      ─────────────
Claude /review   ──▶  Claude /review       ──▶  Claude /review
(no patterns)         + general patterns        + general patterns
                      + repo-specific           + repo-specific
      │               patterns                  patterns (many)
      ▼                     │                         │
Suggest new                 ▼                         ▼
patterns             Refine patterns            High-precision
                     + suggest new ones         reviews
```

**Knowledge building process:**

1. **After the first review**: The tool analyzes review results and suggests new general patterns that should be added to `internal/patterns/patterns/`.
2. **For recurring findings**: When the same issue occurs across multiple repos, the `Occurrences` field is incremented and the pattern is refined.
3. **Repo-specific patterns**: The development team creates these themselves in `.planwerk/review_patterns/` based on their domain knowledge. planwerk-review picks them up automatically.

### Project Structure

```
planwerk-review/
├── .github/
│   └── workflows/
│       ├── ci.yml              # Test, Build, Vet on push/PR
│       ├── lint.yml            # golangci-lint
│       └── release.yml         # GoReleaser on tag push
├── cmd/
│   └── planwerk-review/
│       ├── main.go             # CLI wiring: build runtimeDeps, register subcommands
│       ├── root_cmd.go         # review (root) command + persistent & cache flags
│       ├── resolve.go          # env-var / flag resolution helpers, format constants
│       ├── version.go          # build-version metadata (--version)
│       ├── cache_cmd.go        # cache subcommand group + cache helpers
│       └── <name>_cmd.go       # one file per subcommand (newProposeCmd, newAuditCmd, …)
├── internal/
│   ├── audit/
│   │   ├── auditor.go          # Orchestration: Repo → Patterns → Claude → Findings
│   │   └── auditor_test.go
│   ├── cache/
│   │   ├── cache.go            # SHA-based caching (review + propose + audit)
│   │   └── cache_test.go
│   ├── checklist/
│   │   ├── checklist.go        # Load review checklist (embedded default + override)
│   │   ├── checklist.md        # Default review checklist (embedded)
│   │   └── checklist_test.go
│   ├── cli/
│   │   └── cli.go              # Flag parsing, configuration
│   ├── claude/
│   │   ├── claude.go           # Review command entry point (Review, ReviewContext)
│   │   ├── prompt.go           # /review prompt builder (buildReviewPrompt)
│   │   ├── runner.go           # Claude Code subprocess invocation (runClaude, timeout/model)
│   │   ├── repair.go           # JSON decode with one-shot Claude repair
│   │   ├── structure.go        # Review output → structured findings + IDs
│   │   ├── claude_test.go
│   │   ├── adversarial.go      # Adversarial review pass (--thorough)
│   │   ├── audit.go            # Full-codebase audit against review patterns
│   │   ├── audit_test.go
│   │   ├── coverage.go         # Test coverage map generation (--coverage-map)
│   │   ├── elaborate.go        # Issue → detailed engineering plan
│   │   ├── propose.go          # Codebase analysis for proposals
│   │   └── propose_test.go
│   ├── elaborate/
│   │   ├── elaborate.go        # Pipeline: Issue → Repo → Claude → Detailed body
│   │   ├── elaborate_test.go
│   │   ├── interfaces.go
│   │   ├── renderer.go         # Markdown body assembly
│   │   └── result.go           # Structured elaboration result
│   ├── prompt/
│   │   ├── interfaces.go
│   │   ├── prompt.go           # Deterministic Claude Code prompt assembler
│   │   └── prompt_test.go
│   ├── doccheck/
│   │   ├── doccheck.go         # Detect stale documentation files
│   │   └── doccheck_test.go
│   ├── github/
│   │   ├── comments.go         # Post/update PR comments (gh CLI)
│   │   ├── comments_test.go
│   │   ├── diff.go             # Fetch and parse PR diffs (DiffMap)
│   │   ├── diff_test.go
│   │   ├── issues.go           # Create/search GitHub issues (gh CLI)
│   │   ├── pr.go               # Fetch PR data, checkout (gh CLI)
│   │   ├── pr_test.go
│   │   ├── repo.go             # Clone repo (gh CLI), fetch default-branch HEAD SHA (gh API)
│   │   ├── repo_test.go
│   │   ├── review.go           # Submit PR reviews via GitHub Review API
│   │   └── review_test.go
│   ├── patterns/
│   │   ├── embedded.go         # //go:embed all:patterns + loadEmbedded()
│   │   ├── loader.go           # Load patterns from directories
│   │   ├── pattern.go          # Pattern data structure + parsing
│   │   ├── pattern_test.go
│   │   ├── sources.go          # Source dispatch (embedded + on-disk + remote)
│   │   └── patterns/           # Embedded review-pattern catalog (14 design + 67 technology + SOURCES.md)
│   ├── propose/
│   │   ├── interactive.go      # Interactive GitHub issue creation flow
│   │   ├── proposal.go         # Proposal data structure + categorization
│   │   ├── proposal_test.go
│   │   ├── proposer.go         # Orchestration: Repo → Claude → Proposals
│   │   ├── proposer_test.go
│   │   └── renderer.go         # Markdown/JSON/Issues output
│   ├── report/
│   │   ├── categorizer.go      # Severity categorization
│   │   ├── categorizer_test.go
│   │   ├── coverage.go         # Coverage result data structure + rendering
│   │   ├── coverage_test.go
│   │   ├── finding.go          # Finding data structure (Severity, Actionability, FixClass, Confidence)
│   │   ├── finding_test.go
│   │   ├── inline.go           # Format findings as GitHub inline review comments
│   │   ├── inline_test.go
│   │   ├── renderer.go         # Markdown/JSON output (compact format, GitHub Alerts, audit verdicts)
│   │   ├── renderer_test.go
│   │   └── audit_renderer_test.go
│   ├── review/
│   │   ├── reviewer.go         # Orchestration: PR → Claude → Report
│   │   ├── reviewer_test.go
│   │   ├── merge.go            # Merge results from multiple review passes
│   │   └── merge_test.go
│   └── todocheck/
│       ├── todocheck.go        # Load TODOS.md for cross-reference
│       └── todocheck_test.go
├── Makefile
├── go.mod
├── go.sum
├── .golangci.yml
├── .goreleaser.yml
└── README.md
```

### GitHub Workflows

#### CI (`ci.yml`)

- **Trigger**: Push to `main`, Pull Requests
- **Jobs**:
  - `test`: `go test ./...` on matrix (Ubuntu, macOS)
  - `build`: `go build ./cmd/planwerk-review/`
  - `vet`: `go vet ./...`

#### Lint (`lint.yml`)

- **Trigger**: Push to `main`, Pull Requests
- **Jobs**:
  - `lint`: `golangci-lint run`

#### Release (`release.yml`)

- **Trigger**: Tag push (`v*`)
- **Jobs**:
  - GoReleaser: Binaries for Linux/macOS/Windows (amd64, arm64)
  - GitHub Release with changelog

### Dependencies

- **Go 1.25+**
- **Claude Code**: Must be installed and authenticated on the system (`claude` in PATH)
- **gh CLI**: Required for cloning repos (incl. private), fetching PR metadata, checkout, and default-branch HEAD lookup (`gh` in PATH)
- **git**: Required as the underlying VCS for `gh repo clone` and local git operations

### Prerequisites

1. Go 1.25+ installed (or download a release binary)
2. Claude Code installed and authenticated (`claude` in PATH)
3. `gh` CLI installed and authenticated (`gh auth login`)
4. Access to the target repository (for checkout/clone)

### Design Decisions

| # | Question | Decision | Rationale |
|---|----------|----------|-----------|
| 1 | **Claude Code invocation** | Once for the entire PR | More efficient; Claude sees full context across files |
| 2 | **Pattern delivery** | Inline in the prompt before `/review` | Patterns are prepended to the `/review` command so Claude considers them during its built-in review |
| 3 | **Result parsing** | Second Claude call for structuring | `/review` returns unstructured text; a second `claude -p` call converts it to JSON matching the `ReviewResult` schema |
| 4 | **Authentication** | `gh auth` | Simplest setup; leverages existing developer workflow |
| 5 | **Review caching** | Based on PR HEAD SHA | Avoids repeated reviews of unchanged PR state |
| 6 | **Propose: two-step Claude** | Analysis → Structure | First call explores codebase freely; second call converts to strict JSON schema |
| 7 | **Propose: cache invalidation** | Based on default branch HEAD SHA | Cache key includes the default-branch HEAD (resolved via `gh api graphql` so private repos work), so proposals refresh when the repo changes |
| 8 | **Propose: output formats** | Markdown, JSON, Issues, Interactive | Markdown for reading, JSON for automation, Issues for templates, `--create-issues` for interactive `gh issue create` |
| 9 | **Review prompt structure** | Multi-section structured prompt | Persona framing, scope analysis, two-pass checklist, suppressions, and anti-sycophancy rules produce higher-quality, more consistent reviews (inspired by [gstack](https://github.com/garrytan/gstack)) |
| 10 | **Actionability classification** | auto-fix / needs-discussion / architectural | Helps teams prioritize which findings to address immediately vs. discuss first |
| 11 | **Scope drift detection** | PR title + body analyzed before code review | Catches scope creep and missing requirements — often the most valuable review feedback |
| 12 | **PR comment posting** | `--post-review` updates existing comment | Idempotent: detects and replaces prior planwerk-review comment via HTML signature. Truncates to GitHub's 65 536-char limit. |
| 13 | **Adversarial review** | `--thorough` runs a second pass | Independent security-focused review merged with primary results, deduplicating by file+line+title |
| 14 | **Coverage map** | `--coverage-map` maps changed functions to tests | Produces a table rating each changed function's test coverage (★★★/★★/★/GAP) with separate E2E gap analysis for projects using Chainsaw or similar frameworks |
| 15 | **External command timeouts** | All `claude`, `gh`, `git` calls have timeouts | Claude: 15 min, git clone: 5 min, gh: 2 min — prevents indefinite blocking |
| 16 | **Test & doc verification** | Dedicated prompt section + checklist items for test/doc completeness | Missing tests and documentation are the most common review gaps; explicit checks at SEMANTIC severity ensure they are flagged consistently. E2E test detection covers Chainsaw (`chainsaw-test.yaml`), kuttl, Helm chart tests, and generic `e2e/` directories |
| 17 | **Enriched findings** | Code snippets, suggested fixes, confidence, fix class, line ranges, relationships | Enables downstream tooling (Claude Code, CI) to process, apply, and correlate findings programmatically |
| 18 | **Inline review comments** | `--inline` posts via GitHub Review API with `suggestion` syntax | Puts findings exactly where the code is; auto-fix suggestions become one-click "Apply suggestion" buttons on GitHub |
| 19 | **Machine-readable comment** | HTML comment with counts + verdict in Markdown output | CI scripts and Claude Code can parse review results without processing full Markdown |
| 20 | **Compact Markdown format** | Empty sections skipped, single-line metadata, GitHub Alert syntax | Reduces noise for human readers and GitHub rendering; no "No findings." placeholders |
| 21 | **Audit: reuse finding schema** | Same `ReviewResult`/`Finding` types as review | Audit findings drop straight into existing tooling, filters, and renderers — no parallel schema to maintain |
| 22 | **Audit: verdict phrasing** | `Action required` / `Improvements suggested` / `Codebase healthy` | PR merge verdicts (`Do not merge` / `Ready to merge`) do not apply to a full-codebase audit; audit-specific phrasing avoids misleading readers |
| 23 | **Audit: no patterns = error** | `audit` fails fast when no patterns load | An audit with zero patterns would produce an unfocused, generic review; surfacing the misconfiguration is better than silently running it |
| 24 | **Adaptive specialist gating** | Skip specialists whose relevant paths the diff does not touch | A small or docs-only PR should not spin up all six specialists; gating cuts wall-clock and cost. `security` and `data-migration` always run because a missed vulnerability or destructive migration is too costly to gate; an unknown diff fails open so nothing is silently skipped |

### Future Extensions

- **Pattern suggestions**: Automatically generate new pattern suggestions after a review
- **Diff-based re-review**: Only check new changes since the last review
- **Multi-reviewer**: Integrate other review tools alongside Claude
- **Dashboard**: Overview of review statistics per repository
- **Propose: incremental analysis**: Track which proposals have been addressed across runs
