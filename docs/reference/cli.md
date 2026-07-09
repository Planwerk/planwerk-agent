# CLI reference

This page documents every user-facing `planwerk-agent` subcommand and flag. A
PR/issue/repo reference can be a full URL or the short form (`owner/repo#123`,
`owner/repo`).

The hidden `gen-man-pages` helper (used by release tooling) is intentionally
omitted. Shell completions and man pages are produced by the built-in
`completion` command and packaging — see
[Install completions & man pages](/how-to/install-completions-and-man-pages).

::: info Drafting and splitting are skills, not subcommands
`draft` and `meta` are no longer `planwerk-agent` subcommands. They are Claude
Code Skills — `/planwerk:draft` and `/planwerk:meta` — because both turn on
decisions only a human can make mid-run. `elaborate` and `fix` exist both ways:
as the commands documented below, and as the `/planwerk:elaborate` and
`/planwerk:fix` skills. See [Use the skills](/how-to/use-the-skills).
:::

## Global flags

These persistent flags apply to every command (`review`, `propose`, `audit`,
`glossary`, `gap-analysis`, `review-prepared`, `elaborate`, `prompt`, `fix`,
`rebase`, `address`, `implement`, `ship`, `cache`, `schema`).

| Flag | Description | Default |
|------|-------------|---------|
| `--verbose`, `-v` | Enable debug-level logging (also shows verbose build info with `--version`) | `false` |
| `--log-format` | Log output format: `text` (human-friendly) or `json` (one JSON object per record, CI-friendly) | `text` |
| `--remote-patterns-ttl` | Refresh interval for remote pattern sources (env: `PLANWERK_REMOTE_PATTERNS_TTL`; `<=0` disables refresh once cached). See [Remote pattern sources](/reference/review-patterns#remote-pattern-sources). | `24h` |
| `--claude-timeout` | Maximum duration for a single Claude Code invocation, applied to every Claude call across all subcommands. Accepts any `time.ParseDuration` value (e.g. `20m`, `1h30m`); must be `> 0`. Env: `PLANWERK_CLAUDE_TIMEOUT`. | `15m` |
| `--show-claude-output` | Stream Claude Code's live output to stderr while a run is in flight, instead of only the periodic heartbeat. Env: `PLANWERK_SHOW_CLAUDE_OUTPUT` (truthy: `1`, `true`, `yes`, `on`). | `false` |
| `--claude-model` | Model passed to Claude Code via `--model` for every Claude call. Accepts a short alias (`opus`, `fable`, `sonnet`) or a full model ID (`claude-fable-5`). Env: `PLANWERK_CLAUDE_MODEL`. | `opus` |
| `--claude-effort` | Reasoning effort passed to Claude Code via `--effort`: one of `low`, `medium`, `high`, `xhigh`, `max`. Env: `PLANWERK_CLAUDE_EFFORT`. | `xhigh` |
| `--structure-model` | Model for the mechanical JSON-structuring passes — the secondary calls that cast an upstream reasoning call's prose into the report schema (review, propose, elaborate, audit, gap-analysis, sync, capture, review-prepared). Also governs those passes' JSON-repair and schema-repair recovery calls. Independent of `--claude-model`: a cheap tier for bounded transcription. Accepts a short alias (`sonnet`, `opus`, `fable`) or full model ID. Env: `PLANWERK_STRUCTURE_MODEL`. | `sonnet` |
| `--structure-effort` | Reasoning effort for the JSON-structuring passes: one of `low`, `medium`, `high`, `xhigh`, `max`. The model swap is the primary cost lever; this is the secondary tunable. Env: `PLANWERK_STRUCTURE_EFFORT`. | `medium` |
| `--claude-inherit-user-config` | Let orchestrated Claude sessions inherit your user-global `~/.claude` settings and MCP servers. Off by default: every session runs hermetically (`--setting-sources project --strict-mcp-config`) so a review is reproducible across machines. Enable only if your `claude` authentication lives in a user-global setting (e.g. `apiKeyHelper`). Env: `PLANWERK_CLAUDE_INHERIT_USER_CONFIG` (truthy: `1`, `true`, `yes`, `on`). See [design decisions #45–#46](/explanation/design-decisions) for the reproducibility rationale. | `false` |

Logs are written to stderr; when stderr is not a terminal, Claude-invocation
heartbeats are still emitted at INFO level so long-running runs are visible in
CI log streams.

## `review` (default command)

The root command reviews a single GitHub pull request.

> The review structuring pass constrains its output with Claude Code's
> `--json-schema` flag, so this command requires Claude Code v2.1.83+ (the same
> minimum the `implement` auto mode needs).

```bash
# Simple invocation with PR URL
planwerk-agent https://github.com/owner/repo/pull/123

# Short form with owner/repo#number
planwerk-agent owner/repo#123

# Post review as inline comments on the PR
planwerk-agent --inline owner/repo#123

# Write output to file
planwerk-agent owner/repo#123 > review.md
```

| Flag | Description | Default |
|------|-------------|---------|
| `--patterns` | Additional pattern source: local directory, `github:owner/repo[/sub][@ref]`, or `git+https://…[#ref[:sub]]` (see [Remote pattern sources](/reference/review-patterns#remote-pattern-sources)) | - |
| `--min-severity` | Minimum severity level for output (`info`, `warning`, `critical`, `blocking`) | `info` |
| `--min-confidence` | Minimum confidence shown in the main report (`verified`, `likely`, `uncertain`); findings below the threshold are filtered out, and uncertain low-severity findings otherwise move to an Unverified section | - |
| `--no-repo-patterns` | Ignore repo-specific patterns | `false` |
| `--no-local-patterns` | Ignore local patterns from the tool | `false` |
| `--no-cache` | Ignore cache, force a fresh review | `false` |
| `--wiki` | Use the target repo's GitHub Wiki as a knowledge source (off by default — enabling trusts the wiki's unreviewed editors; review patterns + project memory; env: `PLANWERK_WIKI`). See [GitHub Wiki](/reference/review-patterns#github-wiki). | `false` |
| `--no-wiki` | Do not use the target repo's GitHub Wiki (overrides `--wiki`) | `false` |
| `--wiki-ref` | Pin the wiki to a branch, tag, or commit (env: `PLANWERK_WIKI_REF`) | - |
| `--clear-cache` | Clear cached reviews and exit (honors `--clear-cache-scope`) | `false` |
| `--clear-cache-scope` | Restrict `--clear-cache` to a single command (`review`, `propose`, `audit`, `glossary`, `elaborate`, `gap-analysis`, `review-prepared`) | - |
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
| `--no-capture` | Skip the read-only capture pass that proposes new wiki review patterns from the review findings (only runs with `--wiki`; writes nothing) | `false` |
| `--capture-wiki` | Ignored by review — a review analyzes an untrusted pull request, so its capture pass is always propose-only and never pushes to the wiki. Capture pattern pages from a trusted source instead (`implement` or `audit`; env: `PLANWERK_CAPTURE_WIKI`). | `false` |
| `--yes` | Skip the `--capture-wiki` write confirmation prompt (for a non-interactive write); has no effect on review, which never writes | `false` |
| `--version` | Show version information and exit | `false` |

When the review uses `--wiki`, a read-only **capture pass** then proposes new
project knowledge for the wiki: generalizable review findings become candidate
`review_patterns/` pages, deduplicated against the wiki's existing entries and
the bundled pattern catalog. It is always **propose-only** — the suggestions
surface on stdout, and (only with `--post-review`) as a PR comment; nothing is ever
written to the wiki. Unlike `implement` and `audit`, review never pushes the
accepted pages, even under `--capture-wiki`: it analyzes an untrusted pull request
and the proposal pass reads attacker-controlled source, so auto-pushing its
free-form pages would let an external contributor poison the shared knowledge base.
A standalone review has no plan or implementation report, so it proposes patterns
only, never `memory/` pages. The pass runs on a cache miss only, is non-fatal, is a
clean no-op when nothing clears the bar, and is skipped without a resolved wiki.
Disable it with `--no-capture`. To grow the wiki from captured patterns, run the
write-back from a trusted source — `implement` or `audit`. See
[Use the GitHub Wiki](/how-to/use-the-github-wiki#capture-knowledge-from-a-findings-producing-run-propose-only).

## `propose`

Analyze a GitHub repository in depth and generate feature proposals.

```bash
planwerk-agent propose owner/repo
planwerk-agent propose --format issues owner/repo
planwerk-agent propose --create-issues owner/repo
```

| Flag | Description | Default |
|------|-------------|---------|
| `--patterns` | Additional pattern source (see [Remote pattern sources](/reference/review-patterns#remote-pattern-sources)) | - |
| `--no-repo-patterns` | Ignore repo-specific patterns | `false` |
| `--no-local-patterns` | Ignore local patterns from the tool | `false` |
| `--no-cache` | Ignore cache, force a fresh analysis | `false` |
| `--wiki` | Use the target repo's GitHub Wiki as a knowledge source (off by default — enabling trusts the wiki's unreviewed editors; review patterns + project memory; env: `PLANWERK_WIKI`). See [GitHub Wiki](/reference/review-patterns#github-wiki). | `false` |
| `--no-wiki` | Do not use the target repo's GitHub Wiki (overrides `--wiki`) | `false` |
| `--wiki-ref` | Pin the wiki to a branch, tag, or commit (env: `PLANWERK_WIKI_REF`) | - |
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
planwerk-agent audit owner/repo
planwerk-agent audit --min-severity warning owner/repo
planwerk-agent audit --format json owner/repo
```

| Flag | Description | Default |
|------|-------------|---------|
| `--patterns` | Additional pattern source (see [Remote pattern sources](/reference/review-patterns#remote-pattern-sources)) | - |
| `--min-severity` | Minimum severity level for output (`info`, `warning`, `critical`, `blocking`) | `info` |
| `--min-confidence` | Minimum confidence shown in the main report (`verified`, `likely`, `uncertain`); findings below the threshold are filtered out, and uncertain low-severity findings otherwise move to an Unverified section | - |
| `--no-repo-patterns` | Ignore repo-specific patterns | `false` |
| `--no-local-patterns` | Ignore local patterns from the tool | `false` |
| `--no-cache` | Ignore cache, force a fresh audit | `false` |
| `--wiki` | Use the target repo's GitHub Wiki as a knowledge source (off by default — enabling trusts the wiki's unreviewed editors; review patterns + project memory; env: `PLANWERK_WIKI`). See [GitHub Wiki](/reference/review-patterns#github-wiki). | `false` |
| `--no-wiki` | Do not use the target repo's GitHub Wiki (overrides `--wiki`) | `false` |
| `--wiki-ref` | Pin the wiki to a branch, tag, or commit (env: `PLANWERK_WIKI_REF`) | - |
| `--cache-max-age` | Reject cached entries older than this duration (`0` disables the TTL) | `720h` |
| `--format` | Output format (`markdown`, `json`) | `markdown` |
| `--max-patterns` | Max review patterns injected into the prompt (`<=0` disables truncation; env: `PLANWERK_MAX_PATTERNS`) | `0` (unlimited) |
| `--max-findings` | Cap on findings returned (`<=0` disables cap) | `0` |
| `--create-issues` | Interactively create GitHub issues from audit findings | `false` |
| `--issue-min-severity` | Minimum severity for issue creation | `warning` |
| `--no-issue-dedupe` | Do not filter findings whose title matches an existing GitHub issue | `false` |
| `--local` | Operate on the current working directory instead of cloning into a temp dir (see [Use local mode](/how-to/use-local-mode)). The repository reference may be omitted — it is inferred from the `origin` remote. | `false` |
| `--force` | With `--local`, skip the confirmation prompt when the working tree is dirty | `false` |
| `--no-capture` | Skip the read-only capture pass that proposes new wiki review patterns from the audit findings (only runs with `--wiki`; writes nothing) | `false` |
| `--capture-wiki` | Push the accepted capture pages to the wiki instead of only proposing them (off by default — a normal run is propose-only; confirms first, refuses a non-TTY run without `--yes`; env: `PLANWERK_CAPTURE_WIKI`, config: `capture.wiki`) | `false` |
| `--yes` | Skip the `--capture-wiki` write confirmation prompt (for a non-interactive write) | `false` |

When the audit uses `--wiki`, the same read-only **capture pass** proposes new
`review_patterns/` pages from the audit findings, deduplicated against the wiki
and the catalog. It is **propose-only** by default — the suggestions go to stdout
(an audit has no PR or issue to comment on); nothing is written to the wiki. Like
review it proposes patterns only (no plan or report ⇒ no `memory/` pages), runs
on a cache miss only, is non-fatal, and is skipped without a resolved wiki.
Disable it with `--no-capture`; push the accepted pages with `--capture-wiki`
(`--yes` to skip the confirmation). See
[Use the GitHub Wiki](/how-to/use-the-github-wiki#capture-knowledge-from-a-findings-producing-run-propose-only).

## `extract`

Anchor a target repository's [GitHub Wiki](/reference/review-patterns#github-wiki)
review patterns into committed, reproducible files — the path back from a
fast-moving, world-editable wiki to a code-coupled knowledge store. The command
is mechanical (it never calls Claude): it reads the wiki's `review_patterns/`
directory, lets you select which entries to anchor, and writes the selected
files.

There are three write modes:

- **Default** — write the selected patterns into the target repo's
  `.planwerk/review_patterns/` and open a pull request through the existing
  PR-creation path.
- **`--local`** — write them directly into the current working tree's
  `.planwerk/review_patterns/` instead of opening a PR.
- **`--to-catalog`** — anchor them into this `planwerk-agent` checkout's
  bundled review catalog (`internal/patterns/patterns/review/`), normalizing
  each pattern's frontmatter to the `review` category. This is the
  maintainer/contribution path and must be run from a `planwerk-agent`
  checkout.

By default the patterns are selected interactively (`y/N/q` per pattern). Pass
`--all` to take every pattern, or `--pattern <stem>` (repeatable) to take
specific ones by filename. A non-interactive run (no TTY) requires one of those
flags: the wiki is an untrusted, world-editable source, so it refuses to extract
(and, in the default mode, push into a PR) every pattern without an explicit
choice rather than failing open.

```bash
planwerk-agent extract owner/repo                       # interactive, opens a PR
planwerk-agent extract owner/repo --all                 # every pattern, opens a PR
planwerk-agent extract owner/repo --pattern my-rule --local
planwerk-agent extract owner/repo --all --to-catalog    # contribute to the bundled catalog
```

| Flag | Description | Default |
|------|-------------|---------|
| `--pattern` | Extract only the named wiki pattern(s) by filename stem (repeatable) | - |
| `--all` | Extract every wiki review pattern without prompting | `false` |
| `--to-catalog` | Anchor into this checkout's bundled review catalog (`internal/patterns/patterns/review/`), normalizing frontmatter to the `review` category | `false` |
| `--local` | Write directly into the current working tree's `.planwerk/review_patterns/` instead of opening a PR (see [Use local mode](/how-to/use-local-mode)). The repository reference may be omitted — it is inferred from the `origin` remote. | `false` |
| `--force` | With `--local`, skip the confirmation prompt when the working tree is dirty | `false` |
| `--overwrite` | With `--local` or `--to-catalog`, replace an existing pattern at the destination instead of refusing the collision | `false` |
| `--wiki-ref` | Pin the wiki to a branch, tag, or commit (env: `PLANWERK_WIKI_REF`) | - |

`--to-catalog` and `--local` are mutually exclusive, as are `--all` and
`--pattern`. `--to-catalog` and the default (PR) mode require an explicit
`<repo-ref>`; `--local` may infer it from the `origin` remote.

The destination filename is the wiki-controlled pattern stem, so `--local` and
`--to-catalog` refuse to write when a file of that name already exists (a wiki
author cannot silently clobber a trusted repo or catalog pattern); pass
`--overwrite` to replace it deliberately.

## `sync`

Reconcile a target repository's [GitHub Wiki](/reference/review-patterns#github-wiki)
knowledge — its review patterns and project-memory pages — against the current
state of the code. The repo and its wiki are cloned and a read-only Claude pass
flags entries that are **stale** (they reference code that no longer exists) or
**redundant** (duplicated or superseded by another entry), then reports them.

`--dry-run` is the default and reports only. `--prune` (or its alias `--apply`)
runs a separate write phase that deletes the flagged entries on the wiki and
pushes — never inside the read-only analysis. The write phase asks for
confirmation first; pass `--yes` to confirm a non-interactive prune. It clones
the wiki fresh, deletes only the flagged entries that still exist (reporting any
that already vanished and noting a wiki that moved since analysis), commits, and
pushes to the wiki's default branch.

The wiki is always read — reconciling it is the command's whole purpose — so
there is no `--wiki`/`--no-wiki` here, only `--wiki-ref` to pin it. `sync` is
scoped to whole-entry deletion; it does not edit entry contents.

```bash
planwerk-agent sync owner/repo                  # dry run: report only
planwerk-agent sync owner/repo --format json    # machine-readable report
planwerk-agent sync owner/repo --prune          # delete flagged entries (confirms first)
planwerk-agent sync owner/repo --prune --yes    # prune without the prompt (CI)
```

| Flag | Description | Default |
|------|-------------|---------|
| `--dry-run` | Report stale and redundant entries without changing the wiki (the default) | `true` |
| `--prune` | Delete the flagged entries on the wiki and push (the write phase) | `false` |
| `--apply` | Alias of `--prune` | `false` |
| `--yes` | Skip the write-phase confirmation prompt (for a non-interactive prune) | `false` |
| `--format` | Output format (`markdown`, `json`) | `markdown` |
| `--wiki-ref` | Pin the wiki to a branch, tag, or commit (env: `PLANWERK_WIKI_REF`) | - |

`--dry-run` and `--prune`/`--apply` are mutually exclusive when both are set
explicitly. A `--prune` run without `--yes` requires a TTY to confirm; in a
non-interactive context it refuses rather than pruning unprompted.

See [Sync the wiki](/how-to/sync-the-wiki) for the workflow and the GitHub
Action auth requirements.

## `glossary`

Generate a starter domain glossary (`CONTEXT.md`) for a codebase and print it to
stdout. The glossary captures the repository's own domain vocabulary so that
`review`, `elaborate`, and `propose` phrase their output in the repo's terms
once it is committed as `CONTEXT.md`. See
[Provide a domain glossary](/how-to/provide-a-domain-glossary) for the schema and
how the commands read it back.

```bash
planwerk-agent glossary owner/repo > CONTEXT.md
planwerk-agent glossary --local
```

The output is a starter — review and edit it before committing. The command
prints to stdout and never writes into the repo.

| Flag | Description | Default |
|------|-------------|---------|
| `--no-cache` | Ignore cache, force a fresh glossary | `false` |
| `--cache-max-age` | Reject cached entries older than this duration (`0` disables the TTL) | `720h` |
| `--local` | Operate on the current working directory instead of cloning into a temp dir (see [Use local mode](/how-to/use-local-mode)). The repository reference may be omitted — it is inferred from the `origin` remote. | `false` |
| `--force` | With `--local`, skip the confirmation prompt when the working tree is dirty | `false` |

## `gap-analysis`

Compare every Planwerk feature file under `.planwerk/completed/` in the target
repo against the actual codebase and report incomplete implementations.

```bash
planwerk-agent gap-analysis owner/repo
planwerk-agent gap-analysis --feature CC-0042 owner/repo
planwerk-agent gap-analysis --file CC-0042-thing.json owner/repo
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
planwerk-agent review-prepared owner/repo
planwerk-agent review-prepared --feature PX-0028 owner/repo
planwerk-agent review-prepared --create-pr owner/repo
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
| `--pr-branch` | Branch name for `--create-pr` | `planwerk-agent/improve-prepared-features` |
| `--pr-base` | Base branch for `--create-pr` | repo default branch |
| `--local` | Operate on the current working directory instead of cloning into a temp dir | `false` |
| `--force` | With `--local`, skip the confirmation prompt when the working tree is dirty | `false` |

## `elaborate`

Expand a high-level GitHub issue into a detailed engineering plan grounded in
the actual repository state.

```bash
planwerk-agent elaborate owner/repo#123
planwerk-agent elaborate --update-issue owner/repo#123
planwerk-agent elaborate --post-comment owner/repo#123
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

## `prompt`

Deterministically render a copy-paste-ready Claude Code prompt for an existing
GitHub issue. No Claude call is involved.

```bash
planwerk-agent prompt owner/repo#42
planwerk-agent prompt --mode fix owner/repo#42
planwerk-agent prompt --mode implement owner/repo#42
```

| Flag | Description | Default |
|------|-------------|---------|
| `--mode` | Prompt variant (`auto`, `fix`, `implement`). In `auto`, issue bodies carrying a `**Severity**:` marker get the `fix` prompt; everything else gets the `implement` prompt. | `auto` |

## `fix`

Watch a pull request's CI checks and, when one fails, dispatch a fresh Claude
Code session to apply a minimal fix and publish it. The loop continues until
every check is green or `--max-iterations` is exhausted.

The loop runs unattended, so it decides alone at every fork the repair turns on —
whether the code or the test is the wrong one, whether to reach outside the
failure surface. When you are there to answer those, use the
[`/planwerk:fix` skill](/how-to/fix-failing-checks) instead.

By default each fix is folded into the branch commit it belongs to
(`git commit --fixup` + `git rebase --autosquash`) and published with
`git push --force-with-lease`, so the branch history stays clean instead of
accumulating "Fix failing CI checks" commits. This is the default in both
temp-dir and `--local` runs. Pass `--no-fixup` to append the fix as a fresh
on-top follow-up commit and push without rewriting history.

```bash
planwerk-agent fix owner/repo#123
planwerk-agent fix --dry-run owner/repo#123
planwerk-agent fix --no-fixup owner/repo#123
planwerk-agent fix --local --force
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
planwerk-agent rebase owner/repo#123
planwerk-agent rebase --onto develop owner/repo#123
planwerk-agent rebase --dry-run owner/repo#123
planwerk-agent rebase --local --push
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

A plan planwerk-agent already posted on the issue (from an earlier run that
planned but was aborted before implementing) is reused by default: the planning
session is skipped and no duplicate plan comment is posted. Use `--no-plan-reuse`
to force a fresh planning session when the posted plan has gone stale.

```bash
planwerk-agent implement owner/repo#123
planwerk-agent implement --no-plan owner/repo#123
planwerk-agent implement --no-plan-reuse owner/repo#123
planwerk-agent implement --verify owner/repo#123
planwerk-agent implement --verify-adversarial owner/repo#123
planwerk-agent implement --verify --verify-adversarial owner/repo#123
planwerk-agent implement --no-simplify owner/repo#123
planwerk-agent implement --no-review owner/repo#123
planwerk-agent implement --wiki owner/repo#123
planwerk-agent implement --wiki --no-capture owner/repo#123
planwerk-agent implement --wiki --capture-wiki owner/repo#123
planwerk-agent implement --wiki --capture-wiki --yes owner/repo#123
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
| `--implement-model` | Model for the implement session only, passed to Claude Code via `--model`; the simplify/review/finalize passes stay on `--claude-model` (env: `PLANWERK_IMPLEMENT_MODEL`) | inherits `--claude-model` |
| `--verify` | After implementing, run an independent pass that checks the actual diff against the issue's Acceptance Criteria without trusting the implementer's report; any unmet criteria are then fed into the review applier and fixed on the branch before the PR opens | `false` |
| `--verify-adversarial` | After implementing, red-team the produced diff for the bugs it introduces using the adversarial-review pass (independent of `--verify`) | `false` |
| `--no-simplify` | Skip the automatic simplify pass that folds over-engineering removals into the branch before the review phase | `false` |
| `--no-review` | Skip the automatic review-and-fix pass that folds review findings into the branch after the simplify pass | `false` |
| `--max-review-iterations` | Cap on the review-and-fix loop: each round re-reviews the branch and re-fixes until the finder comes back clean or this bound is hit (`<=0` uses the default of 3) | `0` |
| `--no-capture` | Skip the read-only capture pass that proposes new wiki review patterns and memory pages (only runs with `--wiki`; writes nothing) | `false` |
| `--capture-wiki` | Push the accepted capture pages to the wiki instead of only proposing them (off by default — a normal run is propose-only; confirms first, refuses a non-TTY run without `--yes`; env: `PLANWERK_CAPTURE_WIKI`, config: `capture.wiki`) | `false` |
| `--yes` | Skip the `--capture-wiki` write confirmation prompt (for a non-interactive write) | `false` |
| `--patterns` | Additional pattern source: local directory, `github:owner/repo[/sub][@ref]`, or `git+https://…[#ref[:sub]]` | - |
| `--no-repo-patterns` | Ignore repo-specific patterns under `.planwerk/review_patterns/` in the target repo | `false` |
| `--no-local-patterns` | Ignore local patterns from the tool | `false` |
| `--max-patterns` | Max review patterns injected into the prompt (`<=0` disables truncation; env: `PLANWERK_MAX_PATTERNS`) | `0` (unlimited) |
| `--wiki` | Use the target repo's GitHub Wiki as a knowledge source (off by default — enabling trusts the wiki's unreviewed editors; review patterns flow into the plan step's pattern catalog + project memory into the planning prompt; env: `PLANWERK_WIKI`). See [GitHub Wiki](/reference/review-patterns#github-wiki). | `false` |
| `--no-wiki` | Do not use the target repo's GitHub Wiki (overrides `--wiki`) | `false` |
| `--wiki-ref` | Pin the wiki to a branch, tag, or commit (env: `PLANWERK_WIKI_REF`) | - |
| `--local` | Operate on the current working directory instead of cloning into a temp dir | `false` |
| `--force` | With `--local`, skip the confirmation prompt when the working tree is dirty | `false` |
| `--no-resume` | Start a fresh feature branch instead of resuming the commits an earlier aborted run for this issue left on its branch; also disables pushing partial progress after an abort | `false` |

`--dry-run`, `--print-prompt`, `--print-bare-prompt`, and `--print-plan-prompt`
are mutually exclusive. The implement session runs in Claude Code's auto mode
and requires Claude Code v2.1.83+.

If an earlier run for this issue aborted mid-implementation (for example the
Claude session hit its usage limit), it left commits on a feature branch. By
default the next run detects that branch, checks it out, and continues from
where it stopped — reconciling the commits already present against the plan's
commit sequence — instead of redoing the committed work. In `--local` mode the
branch persists in your checkout; in clone/CI mode the aborted run pushes its
partial progress to `origin` (no PR) so the next clone can fetch and resume it.
Pass `--no-resume` to start a fresh branch and disable pushing partial progress.

`--verify` and `--verify-adversarial` are independent verification passes that
both run over the actual committed diff, not the implementer's self-report:
`--verify` checks acceptance-criteria coverage, while `--verify-adversarial`
reuses the same adversarial-review machinery as `review --thorough` to hunt for
the bugs the change introduces (injection, race conditions, failure modes).
Enable either, both, or neither. Both are non-fatal — a finding is reported, it
does not fail the run. When `--verify` finds unmet criteria, those findings are
also fed into the same review applier the review-and-fix pass uses, so the gaps
are fixed on the local branch before the finalize step opens the PR (this apply
is non-fatal too, and a clean pass — or a run with no applier wired — stays
render-only).

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
simplify pass, it is allowed to add regression tests. It runs as a **bounded
loop**: after each apply it re-reviews the branch it just changed and, while the
finder still reports findings, fixes them again — stopping when the finder comes
back clean, when an apply escalates (`STATUS: BLOCKED` / `NEEDS_CONTEXT`), or
after `--max-review-iterations` rounds (default 3), noting any findings still
unresolved when the budget runs out. Each round's report is posted as a comment
on the source issue (best-effort). Nothing to fix on the first round is a clean
no-op (no commit, no issue comment beyond a short stdout note). The pass is
non-fatal — a failed or escalated review never changes the run's exit code. The
read-only `--verify` / `--verify-adversarial` flags remain available for a
report-only run. Disable the apply behavior with `--no-review`.

When the run uses `--wiki`, a read-only capture pass then proposes new project
knowledge for the wiki: generalizable review findings become candidate
`review_patterns/` pages, durable rationale from the plan and the implementation
report becomes candidate `memory/` pages, and every candidate is deduplicated
against the wiki's existing entries and the bundled pattern catalog. It is
**propose-only** — the suggestions surface in the run report and as a comment on
the source issue, and nothing is written to the wiki. The pass is non-fatal, is a
clean no-op when nothing clears the bar, and is skipped without a resolved wiki.
Disable it with `--no-capture`. See [Use the GitHub Wiki](/how-to/use-the-github-wiki#capture-knowledge-from-a-findings-producing-run-propose-only)
for the memory write convention it follows.

By default the capture pass is propose-only — it writes nothing. Pass
`--capture-wiki` to push the accepted pages to the wiki: a separate, mechanical
write phase clones the wiki fresh, writes each page (provenance marker included)
under the pinned tool identity, and pushes — creating the wiki's first commit
when it is still uninitialized. Claude never pushes; it authored the page bytes
in the read-only proposal pass, and this phase performs the push. The write is
gated like the rest of the wiki surface: it confirms interactively and refuses a
non-TTY run without `--yes`. The write-back is non-fatal — a refusal or push
failure degrades back to propose-only without failing the run. The gate is also
settable via `PLANWERK_CAPTURE_WIKI` or a `capture.wiki` config key (flag → env →
config → off).

Once the simplify and review passes are done, a finalize session opens the draft
pull request last: it resolves the base branch from `origin/HEAD`, pushes the
feature branch, and runs `gh pr create --draft` with a description that walks the
reviewer through the commits and links the issue with `Closes #N`. This is the
run's deliverable, so — unlike the passes above — a failure to push or open the
PR is fatal. A branch that carries no commits over the base opens no PR and is
not an error.

## `ship`

Take a Meta Issue — the kind the [`/planwerk:meta` skill](/how-to/split-a-meta-issue)
produces — and drive every one of
its Sub Issues to merged on the default branch, in dependency order, without a
human in the loop. Where `implement` is supervised and deliberately stops at a
draft pull request, `ship` makes those decisions itself: for each Sub Issue it
runs the full `implement` pipeline, marks the opened PR ready, waits for CI,
fixes red CI itself (reusing the [`fix`](#fix) loop), and merges when green, then
advances to the next ready Sub Issue.

Sub Issues are processed in the order their dependencies allow. `ship` reads the
native "blocked by" relationships `/planwerk:meta` records and works them
topologically, so
a Sub Issue becomes eligible only once every Sub Issue it is blocked by has
merged; independent Sub Issues stay independently shippable. When a Sub Issue
cannot be finished autonomously — `implement` reports `BLOCKED` / `NEEDS_CONTEXT`,
CI stays red past the fix budget, or the PR will not merge — `ship` skips it and
everything transitively blocked by it, then continues with any remaining Sub Issue
whose blockers have all merged. The failed Sub Issue's PR is left open with its
report for a human to pick up.

`ship` narrates its progress on the Meta Issue and posts a final summary. Because
state lives in GitHub (closed Sub Issues, merged PRs), a re-run resumes naturally
— a Sub Issue already merged is recognized and skipped — so an interrupted run can
simply be invoked again. When every Sub Issue has merged, the Meta Issue is
closed.

```bash
planwerk-agent ship owner/repo#123
planwerk-agent ship --dry-run owner/repo#123
planwerk-agent ship --no-merge owner/repo#123
planwerk-agent ship --merge-method squash owner/repo#123
planwerk-agent ship --start-at 456 owner/repo#123
```

| Flag | Description | Default |
|------|-------------|---------|
| `--dry-run` | Report the planned order of Sub Issues without cloning, calling Claude, or merging | `false` |
| `--no-merge` | Run the whole pipeline but stop at green CI, leaving the merges to a human | `false` |
| `--merge-method` | Merge method for each PR (`rebase`, `squash`, `merge`) | `rebase` |
| `--start-at` | Begin from a specific Sub Issue number (`0` = from the top of the dependency order) | `0` |
| `--max-fix-iterations` | CI self-heal budget per PR before the Sub Issue is skipped | `5` |
| `--interval` | Polling interval between CI check-status queries | `1m` |
| `--no-simplify` | Skip the automatic simplify pass in each per–Sub Issue implement run | `false` |
| `--no-review` | Skip the automatic review-and-fix pass in each per–Sub Issue implement run | `false` |
| `--verify` | In each implement run, check the produced diff against the Sub Issue's Acceptance Criteria | `false` |
| `--verify-adversarial` | In each implement run, red-team the produced diff for the bugs it introduces | `false` |
| `--no-plan` | Skip the planning session in each per–Sub Issue implement run | `false` |
| `--no-plan-reuse` | Always run a fresh planning session; do not reuse a plan already posted on the Sub Issue | `false` |
| `--no-plan-comment` | Do not post the generated implementation plan as a comment on each Sub Issue | `false` |
| `--plan-model` | Model for the planning session passed to Claude Code via `--model` (env: `PLANWERK_PLAN_MODEL`) | `fable` |
| `--plan-effort` | Reasoning effort for the planning session passed via `--effort` (env: `PLANWERK_PLAN_EFFORT`) | `max` |
| `--implement-model` | Model for the implement session in each per–Sub Issue run; the other sessions stay on `--claude-model` (env: `PLANWERK_IMPLEMENT_MODEL`) | inherits `--claude-model` |
| `--patterns` | Additional pattern source: local directory, `github:owner/repo[/sub][@ref]`, or `git+https://…[#ref[:sub]]` | - |
| `--no-repo-patterns` | Ignore repo-specific patterns under `.planwerk/review_patterns/` in the target repo | `false` |
| `--no-local-patterns` | Ignore local patterns from the tool | `false` |
| `--max-patterns` | Max review patterns injected into the prompt (`<=0` disables truncation; env: `PLANWERK_MAX_PATTERNS`) | `0` (unlimited) |

Autonomy and merge safety: `ship` merges to the default branch unattended, so it
honors branch protection — it refuses to merge (skipping the Sub Issue) when a
required check or review would block, or when the PR has a conflict, and **never
force-merges past a protection rule**. `--no-merge` is the escape hatch from full
autonomy: it stops the pipeline at green CI for every Sub Issue (so nothing merges
and, by construction, only the initially-unblocked Sub Issues run). `--start-at`
resumes from a chosen Sub Issue, treating Sub Issues ordered before it as
already-handled unless they are still open. The per–Sub Issue implement runs honor
the same `--no-simplify` / `--no-review` switches as `implement`, so each diff is
cleaned and self-reviewed before CI ever sees it. `ship` does not create Sub
Issues — that stays the job of the [`/planwerk:meta` skill](/how-to/split-a-meta-issue).

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
planwerk-agent address owner/repo#123
planwerk-agent address --all owner/repo#123
planwerk-agent address --thread PRRT_kwDOAbc123 owner/repo#123
planwerk-agent address --resolve owner/repo#123
planwerk-agent address --dry-run owner/repo#123
planwerk-agent address --local --force
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

Inspect the on-disk cache shared by `review`, `propose`, `audit`, `glossary`,
`elaborate`, and `gap-analysis`. See [Caching model](/explanation/caching) for
background.

```bash
# Show total entries, size, age distribution, and per-command breakdown
planwerk-agent cache stats

# Dump metadata and pretty-printed payload for one key
planwerk-agent cache inspect <key>
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
planwerk-agent schema review

# Validate piped JSON against the schema (example with check-jsonschema)
planwerk-agent propose --format json owner/repo > proposals.json
planwerk-agent schema propose > proposal.schema.json
check-jsonschema --schemafile proposal.schema.json proposals.json
```

| Argument | Description |
|----------|-------------|
| `review` | Schema for `review --format json` output (`report-result.schema.json`) |
| `audit` | Schema for `audit --format json` output — identical to `review`, because audit reuses the review result shape |
| `propose` | Schema for `propose --format json` output (`proposal.schema.json`, the proposal-result envelope) |
| `rebase` | Schema for the `rebase` post-rebase analysis output (`rebase-analysis.schema.json`) |

## Built-in commands

`completion` and `help` are provided by [Cobra](https://github.com/spf13/cobra).
`completion <shell>` emits shell completion scripts for `bash`, `zsh`, `fish`,
and `powershell` — see
[Install completions & man pages](/how-to/install-completions-and-man-pages).
`help [command]` prints help for any command.
