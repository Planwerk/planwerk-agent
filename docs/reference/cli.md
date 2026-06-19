# CLI reference

This page documents every user-facing `planwerk-review` subcommand and flag. A
PR/issue/repo reference can be a full URL or the short form (`owner/repo#123`,
`owner/repo`).

The hidden `gen-man-pages` helper (used by release tooling) is intentionally
omitted. Shell completions and man pages are produced by the built-in
`completion` command and packaging — see
[Install completions & man pages](/how-to/install-completions-and-man-pages).

## Global flags

These persistent flags apply to every command (`review`, `propose`, `audit`,
`gap-analysis`, `review-prepared`, `draft`, `elaborate`, `meta`, `prompt`,
`fix`, `rebase`, `address`, `implement`, `cache`, `schema`).

| Flag | Description | Default |
|------|-------------|---------|
| `--verbose`, `-v` | Enable debug-level logging (also shows verbose build info with `--version`) | `false` |
| `--log-format` | Log output format: `text` (human-friendly) or `json` (one JSON object per record, CI-friendly) | `text` |
| `--remote-patterns-ttl` | Refresh interval for remote pattern sources (env: `PLANWERK_REMOTE_PATTERNS_TTL`; `<=0` disables refresh once cached). See [Remote pattern sources](/reference/review-patterns#remote-pattern-sources). | `24h` |
| `--claude-timeout` | Maximum duration for a single Claude Code invocation, applied to every Claude call across all subcommands. Accepts any `time.ParseDuration` value (e.g. `20m`, `1h30m`); must be `> 0`. Env: `PLANWERK_CLAUDE_TIMEOUT`. | `15m` |
| `--show-claude-output` | Stream Claude Code's live output to stderr while a run is in flight, instead of only the periodic heartbeat. Env: `PLANWERK_SHOW_CLAUDE_OUTPUT` (truthy: `1`, `true`, `yes`, `on`). | `false` |
| `--claude-model` | Model passed to Claude Code via `--model` for every Claude call. Accepts a short alias (`opus`, `fable`, `sonnet`) or a full model ID (`claude-fable-5`). Env: `PLANWERK_CLAUDE_MODEL`. | `opus` |
| `--claude-effort` | Reasoning effort passed to Claude Code via `--effort`: one of `low`, `medium`, `high`, `xhigh`, `max`. Env: `PLANWERK_CLAUDE_EFFORT`. | `xhigh` |

Logs are written to stderr; when stderr is not a terminal, Claude-invocation
heartbeats are still emitted at INFO level so long-running runs are visible in
CI log streams.

## `review` (default command)

The root command reviews a single GitHub pull request.

```bash
# Simple invocation with PR URL
planwerk-review https://github.com/owner/repo/pull/123

# Short form with owner/repo#number
planwerk-review owner/repo#123

# Post review as inline comments on the PR
planwerk-review --inline owner/repo#123

# Write output to file
planwerk-review owner/repo#123 > review.md
```

| Flag | Description | Default |
|------|-------------|---------|
| `--patterns` | Additional pattern source: local directory, `github:owner/repo[/sub][@ref]`, or `git+https://…[#ref[:sub]]` (see [Remote pattern sources](/reference/review-patterns#remote-pattern-sources)) | - |
| `--min-severity` | Minimum severity level for output (`info`, `warning`, `critical`, `blocking`) | `info` |
| `--min-confidence` | Minimum confidence shown in the main report (`verified`, `likely`, `uncertain`); findings below the threshold are filtered out, and uncertain low-severity findings otherwise move to an Unverified section | - |
| `--no-repo-patterns` | Ignore repo-specific patterns | `false` |
| `--no-local-patterns` | Ignore local patterns from the tool | `false` |
| `--no-cache` | Ignore cache, force a fresh review | `false` |
| `--clear-cache` | Clear cached reviews and exit (honors `--clear-cache-scope`) | `false` |
| `--clear-cache-scope` | Restrict `--clear-cache` to a single command (`review`, `propose`, `audit`, `elaborate`, `gap-analysis`, `review-prepared`) | - |
| `--cache-stats` | Show cache size, age distribution, and per-command breakdown, then exit | `false` |
| `--cache-inspect` | Print the metadata and payload for the given cache key, then exit | - |
| `--cache-max-age` | Reject cached entries older than this duration (`0` disables the TTL) | `720h` |
| `--format` | Output format (`markdown`, `json`) | `markdown` |
| `--post-review` | Post the review as a comment on the PR (updates existing if found) | `false` |
| `--inline` | Post review with inline comments using the GitHub Review API (implies `--post-review`) | `false` |
| `--thorough` | Run an additional adversarial review pass for security and failure modes | `false` |
| `--specialists` | Run the domain-specialist review fan-out (security, data-migration, testing, performance, api-contract, maintainability) concurrently and merge their findings. Specialists are adaptively gated (see [Adaptive specialist gating](/explanation/review-methodology#adaptive-specialist-gating)). | `false` |
| `--coverage-map` | Generate a test coverage map for changed functions | `false` |
| `--max-patterns` | Max review patterns injected into the prompt (`<=0` disables truncation; env: `PLANWERK_MAX_PATTERNS`; see [Configuration file](/reference/configuration) for precedence) | `0` (unlimited) |
| `--max-findings` | Cap on findings returned (`<=0` disables cap) | `0` |
| `--local` | Operate on the current working directory instead of cloning into a temp dir (see [Use local mode](/how-to/use-local-mode)). The PR reference may be omitted — it is inferred from the current branch. | `false` |
| `--force` | With `--local`, skip the confirmation prompt when the working tree is dirty | `false` |
| `--version` | Show version information and exit | `false` |

## `propose`

Analyze a GitHub repository in depth and generate feature proposals.

```bash
planwerk-review propose owner/repo
planwerk-review propose --format issues owner/repo
planwerk-review propose --create-issues owner/repo
```

| Flag | Description | Default |
|------|-------------|---------|
| `--patterns` | Additional pattern source (see [Remote pattern sources](/reference/review-patterns#remote-pattern-sources)) | - |
| `--no-repo-patterns` | Ignore repo-specific patterns | `false` |
| `--no-local-patterns` | Ignore local patterns from the tool | `false` |
| `--no-cache` | Ignore cache, force a fresh analysis | `false` |
| `--cache-max-age` | Reject cached entries older than this duration (`0` disables the TTL) | `720h` |
| `--format` | Output format (`markdown`, `json`, `issues`) | `markdown` |
| `--max-patterns` | Max review patterns injected into the prompt (`<=0` disables truncation; env: `PLANWERK_MAX_PATTERNS`) | `0` (unlimited) |
| `--create-issues` | Interactively create GitHub issues from proposals | `false` |
| `--no-issue-dedupe` | Do not filter proposals whose title matches an existing GitHub issue | `false` |
| `--local` | Operate on the current working directory instead of cloning into a temp dir (see [Use local mode](/how-to/use-local-mode)). The repository reference may be omitted — it is inferred from the `origin` remote. | `false` |
| `--force` | With `--local`, skip the confirmation prompt when the working tree is dirty | `false` |

## `audit`

Apply every loaded review pattern to an entire codebase.

```bash
planwerk-review audit owner/repo
planwerk-review audit --min-severity warning owner/repo
planwerk-review audit --format json owner/repo
```

| Flag | Description | Default |
|------|-------------|---------|
| `--patterns` | Additional pattern source (see [Remote pattern sources](/reference/review-patterns#remote-pattern-sources)) | - |
| `--min-severity` | Minimum severity level for output (`info`, `warning`, `critical`, `blocking`) | `info` |
| `--min-confidence` | Minimum confidence shown in the main report (`verified`, `likely`, `uncertain`); findings below the threshold are filtered out, and uncertain low-severity findings otherwise move to an Unverified section | - |
| `--no-repo-patterns` | Ignore repo-specific patterns | `false` |
| `--no-local-patterns` | Ignore local patterns from the tool | `false` |
| `--no-cache` | Ignore cache, force a fresh audit | `false` |
| `--cache-max-age` | Reject cached entries older than this duration (`0` disables the TTL) | `720h` |
| `--format` | Output format (`markdown`, `json`) | `markdown` |
| `--max-patterns` | Max review patterns injected into the prompt (`<=0` disables truncation; env: `PLANWERK_MAX_PATTERNS`) | `0` (unlimited) |
| `--max-findings` | Cap on findings returned (`<=0` disables cap) | `0` |
| `--create-issues` | Interactively create GitHub issues from audit findings | `false` |
| `--issue-min-severity` | Minimum severity for issue creation | `warning` |
| `--no-issue-dedupe` | Do not filter findings whose title matches an existing GitHub issue | `false` |
| `--local` | Operate on the current working directory instead of cloning into a temp dir (see [Use local mode](/how-to/use-local-mode)). The repository reference may be omitted — it is inferred from the `origin` remote. | `false` |
| `--force` | With `--local`, skip the confirmation prompt when the working tree is dirty | `false` |

## `gap-analysis`

Compare every Planwerk feature file under `.planwerk/completed/` in the target
repo against the actual codebase and report incomplete implementations.

```bash
planwerk-review gap-analysis owner/repo
planwerk-review gap-analysis --feature CC-0042 owner/repo
planwerk-review gap-analysis --file CC-0042-thing.json owner/repo
```

| Flag | Description | Default |
|------|-------------|---------|
| `--patterns` | Additional pattern source: local directory, `github:owner/repo[/sub][@ref]`, or `git+https://…[#ref[:sub]]` | - |
| `--no-repo-patterns` | Ignore repo-specific patterns | `false` |
| `--no-local-patterns` | Ignore local patterns from the tool | `false` |
| `--no-cache` | Ignore cache, force a fresh gap analysis | `false` |
| `--cache-max-age` | Reject cached entries older than this duration (`0` disables the TTL) | `720h` |
| `--format` | Output format (`markdown`, `json`) | `markdown` |
| `--max-patterns` | Max review patterns injected into the prompt (`<=0` disables truncation; env: `PLANWERK_MAX_PATTERNS`) | `0` (unlimited) |
| `--feature` | Limit analysis to a single feature by `feature_id` (e.g. `CC-0042`) | - |
| `--file` | Limit analysis to a single feature file under `.planwerk/completed/` (path or basename) | - |
| `--create-issues` | Interactively create GitHub issues from gaps | `false` |
| `--no-issue-dedupe` | Do not filter gaps whose suggested-issue title matches an existing GitHub issue | `false` |
| `--local` | Operate on the current working directory instead of cloning into a temp dir (see [Use local mode](/how-to/use-local-mode)). The repository reference may be omitted — it is inferred from the `origin` remote. | `false` |
| `--force` | With `--local`, skip the confirmation prompt when the working tree is dirty | `false` |

`--feature` and `--file` may be combined as a sanity check; if the file's
`feature_id` does not match `--feature`, the run aborts before invoking Claude.

## `review-prepared`

Review every Planwerk feature spec under `.planwerk/features/` whose status is
`prepared` — surface weaknesses in the spec text itself and, with `--create-pr`,
open a pull request that rewrites the JSON to address every WARNING-or-higher
finding. This command reviews the spec only; it does not compare the spec to the
codebase (use [`gap-analysis`](#gap-analysis) for that).

```bash
planwerk-review review-prepared owner/repo
planwerk-review review-prepared --feature PX-0028 owner/repo
planwerk-review review-prepared --create-pr owner/repo
```

| Flag | Description | Default |
|------|-------------|---------|
| `--patterns` | Additional pattern source: local directory, `github:owner/repo[/sub][@ref]`, or `git+https://…[#ref[:sub]]` | - |
| `--no-repo-patterns` | Ignore repo-specific patterns | `false` |
| `--no-local-patterns` | Ignore local patterns from the tool | `false` |
| `--no-cache` | Ignore cache, force a fresh review | `false` |
| `--cache-max-age` | Reject cached entries older than this duration (`0` disables the TTL) | `720h` |
| `--format` | Output format (`markdown`, `json`) | `markdown` |
| `--max-patterns` | Max review patterns injected into the prompt (`<=0` disables truncation; env: `PLANWERK_MAX_PATTERNS`) | `0` (unlimited) |
| `--min-severity` | Minimum severity to render (`info`, `warning`, `critical`) | `info` |
| `--feature` | Limit review to a single feature by `feature_id` (e.g. `PX-0028`) | - |
| `--file` | Limit review to a single feature file under `.planwerk/features/` (path or basename) | - |
| `--create-pr` | After the review, commit improved feature JSON files on a fresh branch and open a pull request | `false` |
| `--pr-branch` | Branch name for `--create-pr` | `planwerk-review/improve-prepared-features` |
| `--pr-base` | Base branch for `--create-pr` | repo default branch |
| `--local` | Operate on the current working directory instead of cloning into a temp dir | `false` |
| `--force` | With `--local`, skip the confirmation prompt when the working tree is dirty | `false` |

## `draft`

Turn a rough, one-line feature idea into a ready-to-file GitHub issue through a
short clarifying Q&A. The draft is previewed, duplicate-title-checked, and
created only on explicit confirmation. `draft` is the front of the pipeline —
`draft → elaborate → implement` — and deliberately stops at an initial feature
description: it does not produce an engineering plan (that is `elaborate`).

```bash
# Draft an issue for an explicit repository (prompts for the idea, then asks)
planwerk-review draft owner/repo

# Seed the idea on the command line
planwerk-review draft owner/repo "add a dark mode toggle"

# File against the current checkout's origin (no repo-ref needed)
planwerk-review draft --local "add a dark mode toggle"

# Draft without the clarifying questions, and preview without filing
planwerk-review draft --no-interactive --dry-run owner/repo "add a dark mode toggle"
```

Without `--local`, the first positional is the repository reference
(`owner/repo` or URL) and the second, optional, is the one-line idea. With
`--local` the issue is filed against the current checkout's `origin` (no
repo-ref needed) and the single positional is the idea; an explicit ref given
under `--local` must match `origin`. When the idea is omitted it is prompted for
interactively — except in a non-interactive context (stdin is not a TTY, or
`--no-interactive`), where a missing idea aborts with an actionable error.

On an interactive terminal (both stdin and stderr a TTY), the idea and each
clarifying answer are captured in a multi-line composer: `Enter` inserts a
newline, `Ctrl-D` submits, `Ctrl-C` cancels, and `Ctrl-E` opens an external
editor on the current text (precedence `$VISUAL` → `$EDITOR` → `vi`). When
stdin is piped, stderr is redirected, or `--no-interactive` is set, `draft`
falls back to single-line reads so scripted input stays stable. See
[Compose your input](/how-to/draft-an-issue#compose-your-input).

| Flag | Description | Default |
|------|-------------|---------|
| `--local` | File against the current checkout's `origin` repo instead of taking an explicit repo-ref (see [Use local mode](/how-to/use-local-mode)). `draft` needs only the `origin` owner/repo — it takes no local checkout. | `false` |
| `--no-interactive`, `-y` | Skip the clarifying Q&A loop and draft straight from the seed idea | `false` |
| `--dry-run` | Render the drafted issue without filing it | `false` |
| `--no-create` | Alias of `--dry-run`: render the drafted issue without filing it | `false` |
| `--label` | Label to attach to the created issue (repeatable; no severity/priority levels — house convention) | - |
| `--format` | Output format (`markdown`, `json`) | `markdown` |
| `--print-prompt` | Render the draft prompt for the idea to stdout and exit; do not invoke Claude or GitHub | `false` |
| `--print-bare-prompt` | Render a self-contained draft prompt (for a manual Claude session) to stdout and exit | `false` |

`--print-prompt` and `--print-bare-prompt` are mutually exclusive and both
require an idea. The create step always asks for confirmation, even with
`--no-interactive` (which skips only the clarifying questions) — script
non-interactive runs with `--dry-run` or `--format json`.

## `elaborate`

Expand a high-level GitHub issue into a detailed engineering plan grounded in
the actual repository state.

```bash
planwerk-review elaborate owner/repo#123
planwerk-review elaborate --update-issue owner/repo#123
planwerk-review elaborate --post-comment owner/repo#123
```

| Flag | Description | Default |
|------|-------------|---------|
| `--patterns` | Additional pattern source (see [Remote pattern sources](/reference/review-patterns#remote-pattern-sources)) | - |
| `--no-repo-patterns` | Ignore repo-specific patterns | `false` |
| `--no-local-patterns` | Ignore local patterns from the tool | `false` |
| `--no-cache` | Ignore cache, force a fresh elaboration | `false` |
| `--cache-max-age` | Reject cached entries older than this duration (`0` disables the TTL) | `720h` |
| `--format` | Output format (`markdown`, `json`) | `markdown` |
| `--max-patterns` | Max review patterns injected into the prompt (`<=0` disables truncation; env: `PLANWERK_MAX_PATTERNS`) | `0` (unlimited) |
| `--update-issue` | Replace the issue body with the elaborated body via `gh issue edit` | `false` |
| `--post-comment` | Post the elaborated body as a new issue comment via `gh issue comment` | `false` |
| `--review` | Run a reviewer pass that checks the draft for executability and refines it to close gaps before output | `false` |
| `--max-review-iterations` | Cap on reviewer refine iterations when `--review` is set (`<=0` uses the default of 3) | `0` |
| `--local` | Ground the elaboration in the current working directory instead of cloning into a temp dir. The issue reference is still required — only the repository checkout is local. | `false` |
| `--force` | With `--local`, skip the confirmation prompt when the working tree is dirty | `false` |

`--update-issue` and `--post-comment` are mutually exclusive.

## `meta`

Expand a Meta Issue — an issue that frames a larger body of work as several
self-contained work packages — into linked, draft-depth Sub Issues. The command
reads the Meta Issue and decides the breakdown on its own: it carves it into the
fewest sensible Sub Issues, files each one at draft depth, links it to the Meta
Issue via GitHub's native sub-issue relationship, and back-fills the Meta Issue
body so its work-package lines reference the freshly created Sub Issues.

Like `draft`, each Sub Issue stops at an initial feature description — it is
deliberately **not** elaborated. Turning a Sub Issue into a file-level
engineering plan stays the job of `elaborate` / `implement`, run per Sub Issue.
The command stops at creating and linking: it does not orchestrate the Sub
Issues through `elaborate` / `implement` / `fix`, and it does not close the Meta
Issue.

```bash
# Preview the planned split without filing or linking anything
planwerk-review meta --dry-run owner/repo#123

# Carve the Meta Issue into Sub Issues, link them, and sync the body
planwerk-review meta owner/repo#123

# Attach a label to each created Sub Issue
planwerk-review meta --label enhancement owner/repo#123
```

| Flag | Description | Default |
|------|-------------|---------|
| `--dry-run` | Render the planned split without filing or linking any sub-issues | `false` |
| `--no-create` | Alias of `--dry-run`: render the planned split without filing | `false` |
| `--label` | Label to attach to each created sub-issue (repeatable; no severity/priority levels — house convention) | - |
| `--format` | Output format (`markdown`, `json`) | `markdown` |

A link failure on one Sub Issue does not abort the run — the Sub Issue is still
created and the failure is reported so it can be linked by hand. The Meta Issue
body is back-filled only when every reference resolves, so it is never left with
a dangling placeholder.

## `prompt`

Deterministically render a copy-paste-ready Claude Code prompt for an existing
GitHub issue. No Claude call is involved.

```bash
planwerk-review prompt owner/repo#42
planwerk-review prompt --mode fix owner/repo#42
planwerk-review prompt --mode implement owner/repo#42
```

| Flag | Description | Default |
|------|-------------|---------|
| `--mode` | Prompt variant (`auto`, `fix`, `implement`). In `auto`, issue bodies carrying a `**Severity**:` marker get the `fix` prompt; everything else gets the `implement` prompt. | `auto` |

## `fix`

Watch a pull request's CI checks and, when one fails, dispatch a fresh Claude
Code session to apply a minimal fix and publish it. The loop continues until
every check is green or `--max-iterations` is exhausted.

By default each fix is folded into the branch commit it belongs to
(`git commit --fixup` + `git rebase --autosquash`) and published with
`git push --force-with-lease`, so the branch history stays clean instead of
accumulating "Fix failing CI checks" commits. This is the default in both
temp-dir and `--local` runs. Pass `--no-fixup` to append the fix as a fresh
on-top follow-up commit and push without rewriting history.

```bash
planwerk-review fix owner/repo#123
planwerk-review fix --dry-run owner/repo#123
planwerk-review fix --no-fixup owner/repo#123
planwerk-review fix --local --force
```

| Flag | Description | Default |
|------|-------------|---------|
| `--interval` | Polling interval between check-status queries | `1m` |
| `--max-iterations` | Maximum number of fix attempts before giving up | `5` |
| `--interactive` | Ask before starting each new fix iteration (after the first) | `false` |
| `--dry-run` | Report failing checks but do not invoke Claude or commit | `false` |
| `--print-prompt` | Render the fix prompt for the current failing checks to stdout and exit | `false` |
| `--print-bare-prompt` | Render a self-contained fix prompt (no check analysis) to stdout and exit | `false` |
| `--no-fix-comment` | Do not post each iteration's fix report as a comment on the pull request | `false` |
| `--patterns` | Additional pattern source: local directory, `github:owner/repo[/sub][@ref]`, or `git+https://…[#ref[:sub]]` | - |
| `--no-repo-patterns` | Ignore repo-specific patterns under `.planwerk/review_patterns/` in the target repo | `false` |
| `--no-local-patterns` | Ignore local patterns from the tool | `false` |
| `--max-patterns` | Max review patterns injected into the prompt (`<=0` disables truncation; env: `PLANWERK_MAX_PATTERNS`) | `0` (unlimited) |
| `--local` | Operate on the current working directory instead of cloning into a temp dir | `false` |
| `--force` | With `--local`, skip the confirmation prompt when the working tree is dirty | `false` |
| `--no-fixup` | Append the fix as a fresh on-top follow-up commit instead of folding it into the commits it belongs to (`git commit --fixup` + `git rebase --autosquash`, then `push --force-with-lease`) | `false` |

`--dry-run`, `--print-prompt`, and `--print-bare-prompt` are mutually exclusive.

## `rebase`

Rebase a pull request's branch onto a base branch (`--onto`, default `main`),
resolving conflicts semantically with Claude rather than a naive `ours`/`theirs`
pick, preserving the individual commits. After a clean rebase, analyze each
rebased commit against the upstream commits that entered the base since the PR
forked and report concrete per-commit adjustments — even where git produced no
textual conflict. History is force-pushed only with `--push`.

```bash
planwerk-review rebase owner/repo#123
planwerk-review rebase --onto develop owner/repo#123
planwerk-review rebase --dry-run owner/repo#123
planwerk-review rebase --local --push
```

| Flag | Description | Default |
|------|-------------|---------|
| `--onto` | Base branch to rebase onto | `main` |
| `--push` | Force-push the rebased branch with `--force-with-lease` (never done implicitly) | `false` |
| `--apply-adjustments` | Apply the post-rebase analysis as fixup commits instead of only reporting | `false` |
| `--max-iterations` | Maximum number of conflict-resolution iterations before aborting | `10` |
| `--no-analysis` | Skip the post-rebase commit analysis | `false` |
| `--no-analysis-comment` | Do not post the post-rebase analysis as a comment on the pull request | `false` |
| `--dry-run` | Show the rebase plan and conflicting commit without resolving, committing, or pushing | `false` |
| `--print-prompt` | Render the post-rebase analysis prompt to stdout and exit; do not rebase or invoke Claude | `false` |
| `--print-bare-prompt` | Render a self-contained rebase prompt (rebase + conflict resolution + analysis) to stdout and exit | `false` |
| `--patterns` | Additional pattern source: local directory, `github:owner/repo[/sub][@ref]`, or `git+https://…[#ref[:sub]]` | - |
| `--no-repo-patterns` | Ignore repo-specific patterns under `.planwerk/review_patterns/` in the target repo | `false` |
| `--no-local-patterns` | Ignore local patterns from the tool | `false` |
| `--max-patterns` | Max review patterns injected into the prompt (`<=0` disables truncation; env: `PLANWERK_MAX_PATTERNS`) | `0` (unlimited) |
| `--local` | Operate on the current working directory instead of cloning into a temp dir | `false` |
| `--force` | With `--local`, skip the confirmation prompt when the working tree is dirty | `false` |

`--dry-run`, `--print-prompt`, and `--print-bare-prompt` are mutually exclusive.
The conflict resolution and the apply step run in Claude Code's auto mode.

## `implement`

Take an elaborated GitHub issue, run a read-only Claude Code planning session,
then a fresh implement session that executes the plan end to end (code, tests,
docs) and commits on a feature branch. The simplify and review passes then run
over the committed diff, and a finalize session opens the draft pull request
last — so it lands already simplified and self-reviewed. The implementation
report is posted back onto the source issue as a comment on every run (use
`--no-report-comment` to skip that), so the course of each implementation is
recorded on the issue.

A plan planwerk-review already posted on the issue (from an earlier run that
planned but was aborted before implementing) is reused by default: the planning
session is skipped and no duplicate plan comment is posted. Use `--no-plan-reuse`
to force a fresh planning session when the posted plan has gone stale.

```bash
planwerk-review implement owner/repo#123
planwerk-review implement --no-plan owner/repo#123
planwerk-review implement --no-plan-reuse owner/repo#123
planwerk-review implement --verify owner/repo#123
planwerk-review implement --verify-adversarial owner/repo#123
planwerk-review implement --verify --verify-adversarial owner/repo#123
planwerk-review implement --no-simplify owner/repo#123
planwerk-review implement --no-review owner/repo#123
```

| Flag | Description | Default |
|------|-------------|---------|
| `--dry-run` | Report what would happen but do not clone, invoke Claude, or push anything | `false` |
| `--print-prompt` | Render the implement prompt (with the issue body embedded, without a plan) to stdout and exit | `false` |
| `--print-bare-prompt` | Render a self-contained implement prompt (no issue body) to stdout and exit | `false` |
| `--print-plan-prompt` | Render the planning prompt (with the issue body embedded) to stdout and exit | `false` |
| `--no-plan` | Skip the planning session and implement directly in a single session | `false` |
| `--no-plan-reuse` | Always run a fresh planning session; do not reuse an implementation plan already posted on the issue | `false` |
| `--no-plan-comment` | Do not post the generated implementation plan as a comment on the source issue | `false` |
| `--no-report-comment` | Do not post the implementation report as a comment on the source issue | `false` |
| `--plan-model` | Model for the planning session passed to Claude Code via `--model` (e.g. `fable`, `opus`; env: `PLANWERK_PLAN_MODEL`) | `fable` |
| `--plan-effort` | Reasoning effort for the planning session passed via `--effort` (`low`, `medium`, `high`, `xhigh`, `max`; env: `PLANWERK_PLAN_EFFORT`) | `max` |
| `--verify` | After implementing, run an independent pass that checks the actual diff against the issue's Acceptance Criteria without trusting the implementer's report | `false` |
| `--verify-adversarial` | After implementing, red-team the produced diff for the bugs it introduces using the adversarial-review pass (independent of `--verify`) | `false` |
| `--no-simplify` | Skip the automatic simplify pass that folds over-engineering removals into the branch before the review phase | `false` |
| `--no-review` | Skip the automatic review-and-fix pass that folds review findings into the branch after the simplify pass | `false` |
| `--patterns` | Additional pattern source: local directory, `github:owner/repo[/sub][@ref]`, or `git+https://…[#ref[:sub]]` | - |
| `--no-repo-patterns` | Ignore repo-specific patterns under `.planwerk/review_patterns/` in the target repo | `false` |
| `--no-local-patterns` | Ignore local patterns from the tool | `false` |
| `--max-patterns` | Max review patterns injected into the prompt (`<=0` disables truncation; env: `PLANWERK_MAX_PATTERNS`) | `0` (unlimited) |
| `--local` | Operate on the current working directory instead of cloning into a temp dir | `false` |
| `--force` | With `--local`, skip the confirmation prompt when the working tree is dirty | `false` |

`--dry-run`, `--print-prompt`, `--print-bare-prompt`, and `--print-plan-prompt`
are mutually exclusive. The implement session runs in Claude Code's auto mode
and requires Claude Code v2.1.83+.

`--verify` and `--verify-adversarial` are independent verification passes that
both run over the actual committed diff, not the implementer's self-report:
`--verify` checks acceptance-criteria coverage, while `--verify-adversarial`
reuses the same adversarial-review machinery as `review --thorough` to hunt for
the bugs the change introduces (injection, race conditions, failure modes).
Enable either, both, or neither. Both are non-fatal — a finding is reported, it
does not fail the run.

The simplify pass runs by default once the branch is committed, before the
review-and-fix and verification passes, so they assess the leaner diff. A
read-only ponytail-style finder reviews the diff through a YAGNI decision ladder
for over-engineering; when it finds something, a fresh session folds each removal
into the commit it belongs to (`git commit --fixup` + `git rebase --autosquash`)
on the local branch — no push, since no pull request exists yet, and never
touching commits already on the base branch. It never removes validation, error
handling, security, or accessibility code and never deletes or weakens tests or
assertions; its report is posted as a comment on the source issue. Nothing to
simplify is a clean no-op (no commit, no issue comment), and the pass is
non-fatal. Disable it with `--no-simplify`.

The review-and-fix pass runs by default after the simplify pass — a full run is
**implement → simplify → review → finalize**. The same adversarial-review
machinery that `--verify-adversarial` uses runs read-only over the produced diff;
when it finds something, a fresh session resolves each finding and folds the fix
into the commit it belongs to (`git commit --fixup` + `git rebase --autosquash`)
on the local branch — no push, since no pull request exists yet. Unlike the
simplify pass, it is allowed to add regression tests. Its report is posted as a
comment on the source issue (best-effort), and a `STATUS: BLOCKED` /
`NEEDS_CONTEXT` report stops the pass without retrying. Nothing to fix is a clean
no-op (no commit, no issue comment beyond a short stdout note). The pass is
non-fatal — a failed or escalated review never changes the run's exit code. The
read-only `--verify` / `--verify-adversarial` flags remain available for a
report-only run. Disable the apply behavior with `--no-review`.

Once the simplify and review passes are done, a finalize session opens the draft
pull request last: it resolves the base branch from `origin/HEAD`, pushes the
feature branch, and runs `gh pr create --draft` with a description that walks the
reviewer through the commits and links the issue with `Closes #N`. This is the
run's deliverable, so — unlike the passes above — a failure to push or open the
PR is fatal. A branch that carries no commits over the base opens no PR and is
not an error.

## `address`

Read a pull request's human review threads, present the unresolved ones as an
interactive selection list, and drive a fresh Claude Code session to incorporate
the selected ones as follow-up commits on the PR head branch — then, gated,
reply to and resolve each addressed thread. This closes the loop the other
commands leave open: `fix` loops on failing CI checks, `rebase` resolves merge
conflicts, and `implement` works from an issue — none of them consume the
inline reviewer feedback on a PR.

Threads GitHub already marks resolved, and the tool's own inline review
comments, are skipped by default. The orchestrator pushes the follow-up commits;
replies are best-effort and on by default, resolving is best-effort and off by
default (it is outward-facing).

```bash
planwerk-review address owner/repo#123
planwerk-review address --all owner/repo#123
planwerk-review address --thread PRRT_kwDOAbc123 owner/repo#123
planwerk-review address --resolve owner/repo#123
planwerk-review address --dry-run owner/repo#123
planwerk-review address --local --force
```

| Flag | Description | Default |
|------|-------------|---------|
| `--all` | Address every unresolved thread without prompting | `false` |
| `--thread` | Address only the named review thread(s) (repeatable) | - |
| `--include-resolved` | Also offer threads GitHub already marks resolved | `false` |
| `--reply` | Post a per-thread reply summarizing the change | `true` |
| `--no-reply` | Do not post per-thread replies (overrides `--reply`) | `false` |
| `--resolve` | Mark addressed threads as resolved (outward-facing) | `false` |
| `--one-commit-per-thread` | Commit each thread separately instead of one aggregate commit | `true` |
| `--no-address-comment` | Do not post the aggregate address report as a comment on the pull request | `false` |
| `--max-iterations` | Maximum number of per-thread address iterations | `10` |
| `--dry-run` | List the selected threads and the planned changes without invoking Claude or committing | `false` |
| `--print-prompt` | Render the address prompt for the selected threads to stdout and exit | `false` |
| `--print-bare-prompt` | Render a self-contained address prompt (no thread fetch) to stdout and exit | `false` |
| `--patterns` | Additional pattern source: local directory, `github:owner/repo[/sub][@ref]`, or `git+https://…[#ref[:sub]]` | - |
| `--no-repo-patterns` | Ignore repo-specific patterns under `.planwerk/review_patterns/` in the target repo | `false` |
| `--no-local-patterns` | Ignore local patterns from the tool | `false` |
| `--max-patterns` | Max review patterns injected into the prompt (`<=0` disables truncation; env: `PLANWERK_MAX_PATTERNS`) | `0` (unlimited) |
| `--local` | Operate on the current working directory instead of cloning into a temp dir | `false` |
| `--force` | With `--local`, skip the confirmation prompt when the working tree is dirty | `false` |

`--dry-run`, `--print-prompt`, and `--print-bare-prompt` are mutually exclusive.
The address session runs in Claude Code's auto mode.

## `cache`

Inspect the on-disk cache shared by `review`, `propose`, `audit`, `elaborate`,
and `gap-analysis`. See [Caching model](/explanation/caching) for background.

```bash
# Show total entries, size, age distribution, and per-command breakdown
planwerk-review cache stats

# Dump metadata and pretty-printed payload for one key
planwerk-review cache inspect <key>
```

| Subcommand | Arguments | Description |
|------------|-----------|-------------|
| `cache stats` | none | Show cache size, age distribution, and per-command breakdown |
| `cache inspect` | `<key>` | Print metadata and the pretty-printed payload for a single cache key (keys come from `cache stats`) |

## `schema`

Print the JSON Schema (draft 2020-12) that describes a command's `--format json`
output to stdout. Downstream tooling can validate piped JSON against the same
contract the renderers follow. See [Output format](/reference/output-format#json-schema)
for the field-level contract.

```bash
# Print the schema for review/audit JSON output
planwerk-review schema review

# Validate piped JSON against the schema (example with check-jsonschema)
planwerk-review propose --format json owner/repo > proposals.json
planwerk-review schema propose > proposal.schema.json
check-jsonschema --schemafile proposal.schema.json proposals.json
```

| Argument | Description |
|----------|-------------|
| `review` | Schema for `review --format json` output (`report-result.schema.json`) |
| `audit` | Schema for `audit --format json` output — identical to `review`, because audit reuses the review result shape |
| `propose` | Schema for `propose --format json` output (`proposal.schema.json`, the proposal-result envelope) |
| `rebase` | Schema for the `rebase` post-rebase analysis output (`rebase-analysis.schema.json`) |
| `draft` | Schema for `draft --format json` output (`draft.schema.json`, the drafted issue) |

## Built-in commands

`completion` and `help` are provided by [Cobra](https://github.com/spf13/cobra).
`completion <shell>` emits shell completion scripts for `bash`, `zsh`, `fish`,
and `powershell` — see
[Install completions & man pages](/how-to/install-completions-and-man-pages).
`help [command]` prints help for any command.
