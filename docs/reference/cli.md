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
| `--claude-timeout` | Maximum duration for a single Claude Code invocation, applied to every Claude call across all subcommands. Accepts any `time.ParseDuration` value (e.g. `20m`, `1h30m`); must be `> 0`. Env: `PLANWERK_CLAUDE_TIMEOUT`. | `60m` |
| `--show-claude-output` | Stream Claude Code's live output to stderr while a run is in flight, instead of only the periodic heartbeat. Env: `PLANWERK_SHOW_CLAUDE_OUTPUT` (truthy: `1`, `true`, `yes`, `on`). | `false` |
| `--claude-model` | Model passed to Claude Code via `--model` for every Claude call. Accepts a short alias (`opus`, `fable`, `sonnet`) or a full model ID (`claude-fable-5-1`). Env: `PLANWERK_CLAUDE_MODEL`. | `opus` |
| `--claude-effort` | Reasoning effort passed to Claude Code via `--effort`: one of `low`, `medium`, `high`, `xhigh`, `max`. Env: `PLANWERK_CLAUDE_EFFORT`. | `xhigh` |
| `--structure-model` | Model for the mechanical JSON-structuring passes — the secondary calls that cast an upstream reasoning call's prose into its artifact's JSON schema (propose, elaborate, gap-analysis, sync, capture, review-prepared). Also governs every JSON-repair and schema-repair recovery call, including those behind the passes that emit findings, and the dedup fallback. Independent of `--claude-model`: a cheap tier for bounded transcription. These passes run in an empty working directory with every built-in tool disabled, so they load no project settings or memory and cannot read the checkout — a transcription needs neither. Accepts a short alias (`sonnet`, `opus`, `fable`) or full model ID. Env: `PLANWERK_STRUCTURE_MODEL`. | `sonnet` |
| `--structure-effort` | Reasoning effort for the JSON-structuring passes: one of `low`, `medium`, `high`, `xhigh`, `max`. The model swap is the primary cost lever; this is the secondary tunable (`medium` is enough to transcribe). Env: `PLANWERK_STRUCTURE_EFFORT`. | `xhigh` |
| `--finder-model` | Model for the read-only **finder passes** — the adversarial pass, each domain specialist, the coverage map, the feature-compliance check, the simplify finder, and claim verification. Independent of `--claude-model`, and empty by default, which inherits it. These passes are the fan-out: six specialists run concurrently on every `review --specialists` and on the first round of `implement`'s review loop, so they are where a cheaper tier saves most — and, because their findings drive an editing session, where it can cost recall. Measure with `make eval` before lowering it. Env: `PLANWERK_FINDER_MODEL`. | _(inherits `--claude-model`)_ |
| `--finder-effort` | Reasoning effort for the finder passes: one of `low`, `medium`, `high`, `xhigh`, `max`. Empty inherits `--claude-effort`. Thinking tokens are output tokens, so this is the cheaper half of the experiment above: try `high` before swapping the model. Env: `PLANWERK_FINDER_EFFORT`. | _(inherits `--claude-effort`)_ |
| `--claude-inherit-user-config` | Let orchestrated Claude sessions inherit your user-global `~/.claude` settings and MCP servers. Off by default: every session runs hermetically (`--setting-sources project --strict-mcp-config`) so a review is reproducible across machines. Enable only if your `claude` authentication lives in a user-global setting (e.g. `apiKeyHelper`). Env: `PLANWERK_CLAUDE_INHERIT_USER_CONFIG` (truthy: `1`, `true`, `yes`, `on`). See [design decisions #45–#46](/explanation/design-decisions) for the reproducibility rationale. | `false` |

Logs are written to stderr; when stderr is not a terminal, Claude-invocation
heartbeats are still emitted at INFO level so long-running runs are visible in
CI log streams.

## `review` (default command)

The root command reviews a single GitHub pull request.

> Every pass that emits findings (the review, the audit, the adversarial pass,
> the domain specialists, the feature-compliance check, the simplify finder and
> the implementation verifier) constrains its output with Claude Code's
> `--json-schema` flag, so `review`, `audit` and `implement` require Claude Code
> v2.1.83+ (the same minimum the `implement` auto mode needs). The review and
> the audit run on `--claude-model`; the other five run on the `--finder-model`
> tier.

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
| `--version` | Show version information and exit | `false` |

When the review uses `--wiki`, a read-only **capture pass** then proposes new
project knowledge for the wiki: generalizable review findings become candidate
`review_patterns/` pages, deduplicated against the wiki's existing entries and
the bundled pattern catalog. It is always **propose-only** — the suggestions
surface on stdout, and (only with `--post-review`) as a PR comment; nothing is ever
written to the wiki. Unlike `implement` and `audit`, review has no `--capture-wiki` flag and never
pushes the accepted pages: it analyzes an untrusted pull request
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
planwerk-agent elaborate --wiki owner/repo#123
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
| `--wiki` | Use the target repo's GitHub Wiki as a knowledge source (off by default — enabling trusts the wiki's unreviewed editors; review patterns + project memory; env: `PLANWERK_WIKI`). See [GitHub Wiki](/reference/review-patterns#github-wiki). | `false` |
| `--no-wiki` | Do not use the target repo's GitHub Wiki (overrides `--wiki`) | `false` |
| `--wiki-ref` | Pin the wiki to a branch, tag, or commit (env: `PLANWERK_WIKI_REF`) | - |

`--update-issue` and `--post-comment` are mutually exclusive.

With the wiki enabled, the elaboration loads the wiki's `review_patterns/` and
reads the project memory: the prompt carries the memory index, and the session
reads a page from a directory the run writes and removes when it ends. The
`--review` reviewer reads neither. The wiki's commit is part of the cache key,
so a wiki that moved re-elaborates. A run whose wiki loaded while its commit
could not be resolved logs a warning and neither reads nor writes the cache.

The body is written to a 40,000-character budget: over it, `--review` refines
the draft to size, and a run without it logs a warning. A body over GitHub's
65,536-character cap is not truncated: `--update-issue` writes it as the body
plus continuation comments and `--post-comment` as a run of comments (refused
when the issue body is itself continued), and every command that reads the
issue merges the parts back first. See
[Elaborate an issue](/how-to/elaborate-an-issue#large-plans-stay-whole-a-budget-and-a-continuation-comment).

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
| `--wiki` | Use the target repo's GitHub Wiki as a knowledge source (off by default — enabling trusts the wiki's unreviewed editors; review patterns + project memory; env: `PLANWERK_WIKI`). See [GitHub Wiki](/reference/review-patterns#github-wiki). | `false` |
| `--no-wiki` | Do not use the target repo's GitHub Wiki (overrides `--wiki`) | `false` |
| `--wiki-ref` | Pin the wiki to a branch, tag, or commit (env: `PLANWERK_WIKI_REF`) | - |

`--dry-run`, `--print-prompt`, and `--print-bare-prompt` are mutually exclusive.

With the wiki enabled, each fix session loads the wiki's `review_patterns/` and
reads the project memory from a directory written for that iteration. The wiki
is resolved once per run, on the first iteration that dispatches a session, so
a run whose checks are green and a `--dry-run` never clone it. The printed
prompts resolve no wiki and carry no memory.

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

An issue that has not been elaborated (its body carries no Acceptance Criteria
heading) gives the plan no definition of done, so before cloning the run asks
whether to implement it anyway. Answering no aborts with the `elaborate`
invocation to run first; a non-TTY run refuses instead of asking. Use
`--allow-unelaborated` to implement such an issue without the question. `ship`
never asks: it runs unattended over the draft-depth Sub Issues `meta` files.

```bash
planwerk-agent implement owner/repo#123
planwerk-agent implement --allow-unelaborated owner/repo#123
planwerk-agent implement --no-plan owner/repo#123
planwerk-agent implement --no-plan-reuse owner/repo#123
planwerk-agent implement --verify owner/repo#123
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
| `--plan-model` | Model for the planning session passed to Claude Code via `--model` (e.g. `opus`, `fable`; env: `PLANWERK_PLAN_MODEL`) | `opus` |
| `--plan-effort` | Reasoning effort for the planning session passed via `--effort` (`low`, `medium`, `high`, `xhigh`, `max`; env: `PLANWERK_PLAN_EFFORT`) | `xhigh` |
| `--implement-model` | Model for the implement session only, passed to Claude Code via `--model`; the simplify/review/finalize passes stay on `--claude-model` (env: `PLANWERK_IMPLEMENT_MODEL`) | inherits `--claude-model` |
| `--implement-worker-model` | Model for the `implementer` subagents the implement session delegates its work packages to. Setting it switches the session into **orchestrator mode**: the session (on `--implement-model`, e.g. `fable`) keeps the whole issue in view, delegates each work package to a subagent on this model, and verifies every delivered package against the actual diff before moving on. The subagent is defined inline via Claude Code's `--agents` flag, so the checkout stays untouched; the report's attribution names this model, and the workers' commit trailers carry their exact model id. Pass an exact model id (e.g. `claude-opus-5-5`) for exact footer attribution. Empty keeps the single-session behavior (env: `PLANWERK_IMPLEMENT_WORKER_MODEL`) | - (orchestrator mode off) |
| `--implement-worker-effort` | Reasoning effort for the `implementer` subagents in orchestrator mode (`low`, `medium`, `high`, `xhigh`, `max`); ignored without `--implement-worker-model` (env: `PLANWERK_IMPLEMENT_WORKER_EFFORT`) | `xhigh` |
| `--verify` | After implementing, run an independent pass that checks the actual diff against the issue's Acceptance Criteria without trusting the implementer's report; any unmet criteria are then fed into the review applier and fixed on the branch before the PR opens | `false` |
| `--no-simplify` | Skip the automatic simplify pass that folds over-engineering removals into the branch before the review phase | `false` |
| `--no-review` | Skip the automatic review-and-fix pass that folds review findings into the branch after the simplify pass | `false` |
| `--no-specialists` | Skip the domain-specialist fan-out on the review pass's first round; the adversarial finder still runs | `false` |
| `--max-review-iterations` | Cap on the review-and-fix loop: each round re-reviews the branch and re-fixes until the finder comes back clean, an apply resolves nothing, or this bound is hit (`<=0` uses the default of 3) | `0` |
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
| `--allow-unelaborated` | Implement an issue that has not been elaborated (no Acceptance Criteria) without asking first; without it such a run asks, and a non-TTY run refuses | `false` |
| `--no-resume` | Start a fresh feature branch instead of resuming the commits an earlier aborted run for this issue left on its branch; also disables pushing partial progress and posting the progress note after an abort | `false` |

`--dry-run`, `--print-prompt`, `--print-bare-prompt`, and `--print-plan-prompt`
are mutually exclusive. The implement session runs in Claude Code's auto mode
and requires Claude Code v2.1.83+.

A session that ends without its implementation report (typically it yielded to
"wait" for a backgrounded test run whose result can never arrive in a one-shot
session) is first resumed in place with a completion nudge — the same Claude
session, full context intact, told to finish the outstanding verification and
emit the report. Only when that fails does the run abort; the session's final
output is then preserved as a `## Progress Note` comment on the issue.

The implement session also receives its report's status contract (the `STATUS`
verdict definitions and the `unproven` Acceptance Criterion status) in its
system prompt, through Claude Code's `--append-system-prompt`, on the first
turn and on every completion nudge turn. The implement prompt carries the same
definitions, but it arrives as the session's first message, and a session long
enough to compact its context can lose them from the summary. Claude Code
rebuilds the system prompt after a compaction, so the contract is present when
the session writes its report. `--print-prompt` output is unchanged, since the
printed prompt already holds the same definitions.

If an earlier run for this issue aborted mid-implementation (for example the
Claude session hit its usage limit), it left commits on a feature branch. By
default the next run detects that branch, checks it out, and continues from
where it stopped — reconciling the commits already present against the plan's
commit sequence — instead of redoing the committed work, and feeds the most
recent progress note or partial report from the issue back into the session so
already-verified work is not re-derived. In `--local` mode the
branch persists in your checkout; in clone/CI mode the aborted run pushes its
partial progress to `origin` (no PR) so the next clone can fetch and resume it.
Pass `--no-resume` to start a fresh branch and disable pushing partial
progress and the progress note.

When the run that stopped had already finished implementing — its
implementation report on the issue says `DONE` (or `DONE_WITH_CONCERNS`) and it
stopped in one of the passes after the session, typically at a usage limit —
the resume continues from that pass instead of running the implement session
again: a posted simplification report skips the simplify pass, each posted
review report counts as a used round of `--max-review-iterations`, and the
review loop resumes at the next round, scoped to the fixes of the last one
(branch-wide when the checkout no longer has that round's pre-fix commit). A
`PARTIAL` report whose Work Breakdown Coverage lists every work package as done
counts as a finished implementation: the run that posted it read the verdict as
`DONE_WITH_CONCERNS` and continued past the implement session.
Capture, verification, and the finalize session then run as usual. A finalize
session that fails persists the branch the same way an abort does, so the next
run resumes it and opens the pull request.

`--verify` is a verification pass that runs over the actual committed diff, not
the implementer's self-report, and checks it for acceptance-criteria coverage.
It is non-fatal — a finding is reported, it
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
**implement → simplify → review → finalize**. It runs the same finders and the
same finding hygiene the `review` command runs (package `internal/hygiene`),
then folds each surviving fix into the commit it belongs to (`git commit --fixup`
+ `git rebase --autosquash`) on the local branch — no push, since no pull request
exists yet. Unlike the simplify pass, it is allowed to add regression tests.

The **first round** runs the adversarial finder plus the domain-specialist
fan-out (security, data-migration, testing, performance, api-contract,
maintainability) concurrently, adaptively gated by the files the branch changed
(see [Adaptive specialist gating](/explanation/review-methodology#adaptive-specialist-gating))
and grounded in the same review-pattern catalog a later review of the diff would
apply. The fan-out is on by default because `implement` runs unattended with
nobody present to opt in; `--no-specialists` turns it off, and later rounds run
the cheaper adversarial finder alone regardless, bounding the fan-out's cost to
the first round.

The merged findings pass through the shared **finding hygiene** — multi-pass
merge (with its confidence boost and cross-pass provenance), file-less dedup, the
quote-or-demote snippet gate, and claim verification — and only findings that
**survive** it are handed to the editing session. Findings that do not survive
(an unverifiable snippet, a refuted claim) are reported on stdout and on the
source issue but **never applied** — the restriction is enforced by the harness,
not requested in the editing session's prompt.

It runs as a **bounded loop**: after each apply it re-reviews and, while the
finder still reports actionable findings, fixes them again — stopping when the
finder comes back clean, when a round yields no findings that
survive hygiene, when an apply escalates (`STATUS: BLOCKED` / `NEEDS_CONTEXT`),
when an apply resolved none of the findings it was handed — the branch is then
unchanged, so re-reviewing it can only re-report them — or
after `--max-review-iterations` rounds (default 3), noting any findings still
unresolved when the budget runs out. Each round's report is posted as a comment
on the source issue (best-effort). Nothing to fix on the first round is a clean
no-op (no commit, no issue comment beyond a short stdout note).

Each round re-reviews at a **narrower scope** than the last. The first round
reviews the whole branch diff with the full fan-out; from the second round on,
the adversarial finder reviews only what the previous round's fixes changed —
`git diff <pre-fix commit>`, recorded before the editing session ran — since the
whole branch was already reviewed and the open question is whether those fixes
hold. If the pre-fix commit cannot be recorded the round falls back to the
branch-wide scope. The pass is
non-fatal — a failed or escalated review never changes the run's exit code. The
read-only `--verify` flag remains available for a
report-only run. Disable the whole pass with `--no-review`, or just the
first-round specialist fan-out with `--no-specialists`.

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
settable via `PLANWERK_CAPTURE_WIKI` or a `capture.wiki` config key (flag →
config → env → off).

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
planwerk-agent ship --wiki owner/repo#123
planwerk-agent ship --wiki --capture-wiki --yes owner/repo#123
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
| `--no-plan` | Skip the planning session in each per–Sub Issue implement run | `false` |
| `--no-plan-reuse` | Always run a fresh planning session; do not reuse a plan already posted on the Sub Issue | `false` |
| `--no-plan-comment` | Do not post the generated implementation plan as a comment on each Sub Issue | `false` |
| `--plan-model` | Model for the planning session passed to Claude Code via `--model` (env: `PLANWERK_PLAN_MODEL`) | `opus` |
| `--plan-effort` | Reasoning effort for the planning session passed via `--effort` (env: `PLANWERK_PLAN_EFFORT`) | `xhigh` |
| `--implement-model` | Model for the implement session in each per–Sub Issue run; the other sessions stay on `--claude-model` (env: `PLANWERK_IMPLEMENT_MODEL`) | inherits `--claude-model` |
| `--implement-worker-model` | Model for the `implementer` subagents in each per–Sub Issue implement run; setting it switches those runs into orchestrator mode, exactly as on `implement` (env: `PLANWERK_IMPLEMENT_WORKER_MODEL`) | - (orchestrator mode off) |
| `--implement-worker-effort` | Reasoning effort for the `implementer` subagents in orchestrator mode; ignored without `--implement-worker-model` (env: `PLANWERK_IMPLEMENT_WORKER_EFFORT`) | `xhigh` |
| `--patterns` | Additional pattern source: local directory, `github:owner/repo[/sub][@ref]`, or `git+https://…[#ref[:sub]]` | - |
| `--no-repo-patterns` | Ignore repo-specific patterns under `.planwerk/review_patterns/` in the target repo | `false` |
| `--no-local-patterns` | Ignore local patterns from the tool | `false` |
| `--max-patterns` | Max review patterns injected into the prompt (`<=0` disables truncation; env: `PLANWERK_MAX_PATTERNS`) | `0` (unlimited) |
| `--wiki` | Use the target repo's GitHub Wiki as a knowledge source (off by default — enabling trusts the wiki's unreviewed editors; review patterns + project memory; env: `PLANWERK_WIKI`). See [GitHub Wiki](/reference/review-patterns#github-wiki). | `false` |
| `--no-wiki` | Do not use the target repo's GitHub Wiki (overrides `--wiki`) | `false` |
| `--wiki-ref` | Pin the wiki to a branch, tag, or commit (env: `PLANWERK_WIKI_REF`) | - |
| `--no-capture` | Skip the read-only capture pass in each per–Sub Issue implement run (only runs with `--wiki`; writes nothing) | `false` |
| `--capture-wiki` | Push the accepted capture pages of each per–Sub Issue implement run to the wiki; `ship` never asks for confirmation, so the push also needs `--yes` (off by default; env: `PLANWERK_CAPTURE_WIKI`) | `false` |
| `--yes` | Confirm the `--capture-wiki` write for the whole run | `false` |

Autonomy and merge safety: `ship` merges to the default branch unattended, so it
honors branch protection — it refuses to merge (skipping the Sub Issue) when a
required check or review would block, or when the PR has a conflict, and **never
force-merges past a protection rule**. `--no-merge` is the escape hatch from full
autonomy: it stops the pipeline at green CI for every Sub Issue (so nothing merges
and, by construction, only the initially-unblocked Sub Issues run). `--start-at`
resumes from a chosen Sub Issue, treating Sub Issues ordered before it as
already-handled unless they are still open. The per–Sub Issue implement runs honor
the same `--no-simplify` / `--no-review` switches as `implement`, so each diff is
cleaned and self-reviewed before CI ever sees it. `ship` gains no fan-out
off-switch of its own: each per–Sub Issue run inherits the default-on first-round
specialist fan-out, and `--no-review` remains the whole-pass switch — so every Sub
Issue is checked across the same domains and its self-review findings pass the
same hygiene before any fix lands. `ship` does not create Sub Issues — that stays
the job of the [`/planwerk:meta` skill](/how-to/split-a-meta-issue).

Wiki and capture: `ship` resolves the wiki settings once and hands them to
every `implement` and `fix` run it drives. With the wiki enabled, each of those
runs reads the wiki's `review_patterns/` and the project memory, and each
`implement` run ends with the capture pass, which proposes new wiki pages in a
comment on its Sub Issue. `--no-capture` skips that pass. `ship` never shows
the confirmation prompt the capture write phase has on `implement`: it pushes
the accepted pages only when the write-back is enabled (`--capture-wiki`, the
`capture.wiki` config key, or `PLANWERK_CAPTURE_WIKI`) and `--yes` is given.
Enabled without `--yes`, `ship` logs one warning at start and every run stays
propose-only.

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
| `--wiki` | Use the target repo's GitHub Wiki as a knowledge source (off by default — enabling trusts the wiki's unreviewed editors; review patterns + project memory; env: `PLANWERK_WIKI`). See [GitHub Wiki](/reference/review-patterns#github-wiki). | `false` |
| `--no-wiki` | Do not use the target repo's GitHub Wiki (overrides `--wiki`) | `false` |
| `--wiki-ref` | Pin the wiki to a branch, tag, or commit (env: `PLANWERK_WIKI_REF`) | - |

`--dry-run`, `--print-prompt`, and `--print-bare-prompt` are mutually exclusive.
The address session runs in Claude Code's auto mode.

With the wiki enabled, the run loads the wiki's `review_patterns/` and writes
the project memory to one directory that every per-thread session reads. A run
with no thread to address and a `--dry-run` never clone the wiki. The printed
prompts resolve no wiki and carry no memory.

## `brain`

Read and build the project memory a repository keeps on its
[GitHub Wiki](/reference/review-patterns#github-wiki), and mirror what the
repository knows on GitHub to local files.

| Subcommand | Arguments | Description |
|------------|-----------|-------------|
| `brain memory` | `<repo-ref>` | Print the memory index of the repository's wiki |
| `brain memory` | `<repo-ref> <page>` | Print the memory page with that file name |
| `brain bootstrap` | `<repo-ref>` | Build the memory from the repository's history |
| `brain sync` | `<repo-ref>` | Mirror the issues, pull requests, commit list, and wiki to local markdown |

### `brain memory`

Print the project memory. The command starts no Claude session and writes no
file. The [skills](/how-to/use-the-skills#project-memory) call it, so a skill
reads the memory through the same opt-in, authentication, and page checks as
the commands above.

```bash
# Print the memory index: a header, a blank line, one line per page
planwerk-agent brain memory owner/repo

# Print one page, named by its file name in the index
planwerk-agent brain memory owner/repo pin-dependencies.md
```

| Flag | Description | Default |
|------|-------------|---------|
| `--wiki` | Use the target repo's GitHub Wiki as a knowledge source (off by default — enabling trusts the wiki's unreviewed editors; review patterns + project memory; env: `PLANWERK_WIKI`). See [GitHub Wiki](/reference/review-patterns#github-wiki). | `false` |
| `--no-wiki` | Do not use the target repo's GitHub Wiki (overrides `--wiki`) | `false` |
| `--wiki-ref` | Pin the wiki to a branch, tag, or commit (env: `PLANWERK_WIKI_REF`) | - |

The index opens with a header that names the wiki, its commit, and the number
of pages. Each following line carries a page's file name, its title, and after
a `|` the page's summary where it states one:

```text
Project memory from acme/widgets.wiki @ 1a2b3c4, pages: 2

- conventions.md: conventions
- pin-dependencies.md: Pin every dependency | Dependencies are pinned to exact versions.
```

The index has no size budget, unlike the index a prompt carries. Both forms
see a page only when its file name starts with a letter or a digit and holds
nothing but letters, digits, `.`, `_`, and `-`, because a caller passes the
name back on a command line. Any other page is skipped: one warning gives the
number of skipped pages, and `--verbose` logs their names. A script that takes
the name from the index puts it after a `--`
(`planwerk-agent brain memory owner/repo -- "$page"`). A page name is matched by
its bytes first and then by its Unicode NFC form, so a name the wiki stores in
decomposed form is found when it is typed precomposed, as long as only one page
has that name. Control characters other than newline and tab are dropped from
what the command prints on stdout.

The wiki is off by default. The flag decides first, then the `wiki` section of
`.planwerk/config.yaml` in the working directory, then `PLANWERK_WIKI`. With the
wiki off, with a wiki that cannot be resolved, or with a wiki that has no memory
pages, the index form prints nothing on stdout and exits 0. A page the memory
does not hold is an error: the command prints nothing on stdout and exits
non-zero with `no project memory page named "<page>" for <owner>/<repo>`. Log
lines go to stderr.

### `brain bootstrap`

Distill the history of a repository into project memory pages and review
patterns. The command reads the history unit by unit, runs one analysis session
per unit and one review session per unit that proposes a page, and keeps the
pages in a state directory. It writes to the wiki only under `--write-wiki` and
after a confirmation at a terminal. See
[Bootstrap the project memory](/how-to/bootstrap-the-project-memory) for the
workflow.

```bash
planwerk-agent brain bootstrap owner/repo --dry-run        # list the units, no session
planwerk-agent brain bootstrap owner/repo --max-units 5    # process five units and stop
planwerk-agent brain bootstrap owner/repo                  # process every remaining unit
planwerk-agent brain bootstrap owner/repo --write-wiki     # push the pages after a y
```

| Flag | Description | Default |
|------|-------------|---------|
| `--dry-run` | Clone the repository, list the units, and stop: no Claude session, no wiki clone, no state write | `false` |
| `--max-units` | Stop after this many units in this run; `0` processes every remaining unit | `0` |
| `--write-wiki` | Once no unit remains, push the changed pages after a confirmation at a terminal | `false` |
| `--wiki-ref` | Pin the wiki to a branch, tag, or commit (env: `PLANWERK_WIKI_REF`) | - |
| `--review-model` | Model of the page review (env: `PLANWERK_BRAIN_REVIEW_MODEL`) | `fable` |
| `--review-effort` | Reasoning effort of the page review: one of `low`, `medium`, `high`, `xhigh`, `max` (env: `PLANWERK_BRAIN_REVIEW_EFFORT`) | `high` |
| `--decision-docs` | Repository-relative paths of the decision documents to read, comma-separated or repeated; replaces discovery | - |
| `--no-decision-docs` | Read no decision document | `false` |
| `--source` | Where the history is read from: `api` (the GitHub API) or `mirror` (the local mirror of [`brain sync`](#brain-sync)) | `api` |

`--dry-run` and `--write-wiki` are mutually exclusive, and so are
`--decision-docs` and `--no-decision-docs`. `--max-units` must not be negative.
An unknown `--review-effort` and an unknown `--source` are rejected before any
session runs.

The command always reads the wiki, so it has `--wiki-ref` and neither `--wiki`
nor `--no-wiki`. It reads `wiki.repo` and `wiki.ref` from
`.planwerk/config.yaml` and ignores `wiki.enabled`. It has no `--yes`.

#### Units

The history is read from the GitHub API, or from the local mirror with
`--source mirror`, and grouped into units:

| Kind | Key | Content |
|------|-----|---------|
| Issue | `issue-<n>` | A closed issue with its comments, the merged pull requests that closed it (comments, reviews, review threads, commits), and the commit that closed it when no pull request did |
| Pull request | `pr-<n>` | A merged pull request that closed no closed issue. A pull request opened by a bot is skipped and counted |
| Commit range | `commits-<first12>-<last12>` | Up to 25 adjacent commits of the default branch that belong to no merged pull request and closed no issue |
| Document | `doc-<path>@<hash12>` | One chunk of a decision document, cut at line boundaries into chunks of at most 16 KiB |

Units follow the default branch. A unit sorts by the newest commit it holds on
the default branch. A unit without such a commit (an issue closed by hand, a
pull request whose commits are no longer on the default branch) sorts by the
time it was closed or merged, after the units of the commits up to that time.
Document units come last, in path order.

With `--source mirror` the listing and every issue and pull request thread come
from the files [`brain sync`](#brain-sync) wrote, and the run calls no GitHub
API for them. The mirror is read as it is: the run does not sync it, so the
history ends where the last `brain sync` ended. The repository is still cloned,
and commit messages are read from that clone. A mirror that cannot be used
fails the run:

- Without a mirror, the listing fails with `no mirror of <owner/repo> at <dir>;
  run "planwerk-agent brain sync <owner/repo>" first`.
- With a mirror in which no sync has finished, it fails with `the mirror of
  <owner/repo> at <dir> has never finished a sync; run "planwerk-agent brain
  sync <owner/repo>" again`.
- With an item file that cannot be read, it fails with
  `reading <file>: <cause>`. A file is not skipped, because a skipped file
  would shorten the history without notice.
- When a unit names an issue or a pull request the mirror does not hold, the
  unit fails with `<owner/repo>#<n> is not in the mirror at <dir>; run
  "planwerk-agent brain sync <owner/repo>"`.

A Markdown file is a decision document when a directory on its path is named
`adr`, `adrs`, or `decisions`, or when its name is `design-decisions.md`,
`decisions.md`, `decision-log.md`, or `adr.md` (compared without case). A
symlink, a file over 2 MiB, and a path that holds `--` or a character outside
letters, digits, `.`, `_`, `/`, and `-` are skipped with a warning. A path given
with `--decision-docs` must be a regular file inside the repository, or the run
fails with `decision document "<path>" not found in the repository`.

Every piece of a unit's text is scrubbed of known secret patterns. A piece over
32 KiB is cut and ends with `[truncated: <n> bytes omitted]`. A unit carries at
most 384 KiB into a session; the pieces past that are left out, and the prompt
says how many.

#### Sessions

The analysis runs on `--claude-model` and `--claude-effort`. It proposes memory
pages and review patterns, or nothing. A new page must be
`memory/<name>.md` or `review_patterns/<name>.md` with a name of lowercase
letters, digits, `.`, `_`, and `-` that starts with a letter or a digit; a
proposal for the path of an existing page is an update. Any other path is
rejected as `invalid path`, and a second proposal for one path as
`duplicate path`.

The review runs on `--review-model` and `--review-effort`, and only for a unit
with at least one valid proposal. It gives each page one verdict: `accept`
keeps the proposed page, `revise` replaces it with the reviewer's page, and
`reject` drops it. A page without a verdict is rejected as
`no review verdict`, a `reject` without a reason is recorded as
`rejected without a reason`, and a `revise` without a page as
`revise verdict without a page`. A page that is over 64 KiB with its provenance
marker is rejected as `page exceeds 64 KiB`.

Both sessions are read-only, run in a clone of the repository at the default
branch's HEAD, and may read the pages of the state directory.

#### State directory

The state lives in `.planwerk-brain-sync` in the directory the command runs in:

```text
.planwerk-brain-sync/
├── .gitignore        # one line, "*", so git never tracks the directory
├── state.json        # processed units, one entry per page, usage of all runs
├── pages/
│   ├── memory/<name>.md
│   └── review_patterns/<name>.md
└── orphans/          # files found under pages/ that state.json has no entry for
```

A page file holds the page without its provenance marker. `state.json` records
for each page the source its marker names (`source`: the unit that last wrote
the page, or the source the wiki page's marker named), the hash of the wiki
version it is based on (`base_sha256`), and whether it diverged. A page is dirty
when its file differs from that wiki version; dirty pages are what
`--write-wiki` pushes.

A file under `pages/` that `state.json` has no entry for is not a page of the
working set. Every run except a dry run moves it to `orphans/`, under the same
relative path, and logs a warning that names both paths. When `orphans/` already
holds something under that path, the file is moved to the first free name
`<path>.1`, `<path>.2`, and so on, and what was there stays. A directory of the
path whose name `orphans/` holds as a file takes the first free name the same
way. A `state.json` that names a page outside `memory/` and `review_patterns/`
fails the run with `<file> names the page "<path>", which is not a file in
memory/ or review_patterns/; delete the directory to start over`.

Every run except a dry run starts by cloning the wiki and refreshing the pages:

| Local page | Wiki page | Result |
|------------|-----------|--------|
| Absent | Present | The page is added |
| Unchanged | Changed | The page takes the wiki's text |
| Changed | Unchanged | Nothing changes |
| Changed | Changed to the same text | The page counts as unchanged |
| Changed | Changed to another text | The page is marked diverged |
| Unchanged | Removed | The page is deleted |
| Changed | Removed | The page becomes a new page |

A wiki file in `memory/` or `review_patterns/` whose name is only `.md` is not
added: the run logs a warning that names it and skips it.

A diverged page is never pushed. Every run reports it until the local file and
the wiki page hold the same text. When the wiki cannot be cloned (a wiki without
a first page cannot), the run logs a warning and continues with the pages it
has.

#### Output

The run prints to stdout:

```text
Units: 196 total, 2 processed, 194 remaining
  103 issues, 66 pull requests, 16 commit ranges, 11 decision document chunks; 18 bot-authored pull requests skipped
[3/196] issue-6: 2 proposed, 1 accepted, 1 rejected
Pages: 4 new, 1 updated, 3 unchanged, 0 diverged
- `memory/pin-dependencies.md` (new) from owner/repo#6
- `memory/one-off.md` (issue-6): a one-off, not a decision
Models: analysis claude-opus-5-5, review claude-fable-5-1
Usage this run: 48211 input tokens, 9120 output tokens, 4 calls, est. $1.84
Usage all runs: 131004 input tokens, 26377 output tokens, 12 calls, est. $5.02
Propose-only: nothing was written to the wiki. The pages are under .planwerk-brain-sync/pages; run again with --write-wiki to push them.
```

- The two `Units:` lines count the units by state and by kind. A dry run then
  prints one line per remaining unit, `<key>  <title>`, and stops.
- One `[<position>/<total>]` line per processed unit.
- The `Pages:` line, then one line per dirty page (`new` or `update`, and the
  source of the page, or `a local edit` for a hand-edited page that had no
  provenance marker on the wiki), one line per proposal rejected in this run
  with its unit and the reason, and one line per diverged page.
- The `Models:` line names the model each kind of session reported, `-` for a
  kind that did not run.
- The two `Usage` lines, for this run and summed over all runs.

Control characters other than newline and tab are dropped from what the command
prints. Log lines and the usage summary go to stderr.

#### Stop and resume

The state is saved after every unit. A unit that fails ends the run: the run
prints `Stopped at unit <key>. The state is saved in <dir>; run the same command
again to continue.` and exits non-zero with `unit <key>: <step>: <cause>`, where
the step is `content`, `analysis`, `review`, or `apply`. A Claude usage limit, a
session timeout, and a GitHub rate limit all arrive this way.

The next run lists the history again and skips every unit in the state. It
continues with the first unit that is not processed, and a later run processes
only what was closed, merged, or committed since. A commit range that grew gets
a new key and is processed again. An issue keeps its key: its unit is processed
again when it holds a pull request or a closer commit that its record in
`state.json` does not name, for example after the issue was reopened and closed
by a later pull request. Deleting `.planwerk-brain-sync` starts over.

#### Wiki write

With `--write-wiki`, and once no unit remains, the run lists every dirty page
that is not diverged and asks `Write <n> pages to the <owner/repo> wiki and
push? (y/N):`. On `y` it clones the wiki fresh and pushes the pages as one
commit. Each page carries the provenance marker of its unit. A page that came
from the wiki and that you edited by hand keeps the marker it had there, and is
pushed without one when it had none.

- With units remaining, the run prints `<n> units remain; the wiki write runs
  once every unit is processed.` and pushes nothing.
- With no dirty page, it prints `Nothing to write: every page matches the wiki.`
- When stdin is not a terminal, it fails with `refusing to write to the wiki:
  brain bootstrap pushes only after a confirmation at a terminal, and stdin is
  not a TTY`.
- When the wiki moved since the refresh, updates are skipped and new pages are
  still written.
- A new page whose path the wiki already holds is skipped.
- When `state.json` was last refreshed from another wiki than the one the run
  writes to, it fails with `the pages in <dir> were last refreshed from the
  "<owner/repo>" wiki, and this run writes to the <owner/repo> wiki; run again
  once that wiki can be cloned, or delete the directory to start over`.
- When the wiki holds `memory` or `review_patterns` as a symbolic link, it fails
  with `the <owner/repo> wiki holds <directory> as a symbolic link; refusing to
  write through it`.

The command never deletes a wiki page; [`sync --prune`](#sync) does.

### `brain sync`

Keep a local mirror of a repository's knowledge on GitHub: one markdown file
per issue and per pull request with the whole conversation, the commit list of
the default branch, and a clone of the wiki. The command starts no Claude
session, and no session is given the mirror. It is not the [`sync`](#sync)
command, which reconciles the wiki's pages against the code. See
[Mirror a repository](/how-to/mirror-a-repository) for the workflow.

```bash
planwerk-agent brain sync owner/repo           # fetch what changed since the last run
planwerk-agent brain sync owner/repo --full    # delete the mirror and build it again
```

| Flag | Description | Default |
|------|-------------|---------|
| `--full` | Delete this repository's mirror first, then sync | `false` |
| `--wiki-ref` | Pin the wiki to a branch, tag, or commit (env: `PLANWERK_WIKI_REF`) | - |

The command always mirrors the wiki, so it has `--wiki-ref` and neither `--wiki`
nor `--no-wiki`. It reads `wiki.repo` and `wiki.ref` from
`.planwerk/config.yaml` and ignores `wiki.enabled`.

#### Mirror directory

The mirror lives in the user cache directory, keyed by owner and repository in
lowercase: `<user cache dir>/planwerk-agent/brain/<owner>/<name>` (on Linux
`~/.cache/planwerk-agent/brain/<owner>/<name>`). The first output line prints
the path. No flag, config key, or environment variable moves it. A run that
cannot resolve a user cache directory (neither `HOME` nor `XDG_CACHE_HOME` is
set) stops with an error: the mirror is never written to the temp directory.

```text
<owner>/<name>/
├── state.json          # the items cursor, the mirrored wiki and its commit, the end of the last finished sync
├── issues/<number>.md
├── pulls/<number>.md
├── history.jsonl       # one line per commit of the default branch, oldest first
└── wiki/               # a full git clone of <repo>.wiki.git
```

The mirror directory, `issues/`, and `pulls/` have mode `0700`, and the files
the command writes have mode `0600`. The wiki clone keeps git's own modes
inside that directory. Every file is written through a temporary file and a
rename. A run that is interrupted can leave its temporary file (`*.tmp`) in the
mirror directory, `issues/`, or `pulls/`. A later run removes it once it is more
than one hour old. The files hold the text as GitHub returns it, with any secret
someone pasted into a comment.

The mirror is a copy: deleting it loses nothing, and the next run builds it
again. `--clear-cache` leaves it in place.

`state.json` has this form:

```json
{
  "version": 1,
  "repo": "owner/name",
  "synced_at": "2026-10-02T09:00:00Z",
  "items": { "cursor": "2026-10-02T08:17:02Z" },
  "wiki": { "repo": "owner/name", "commit": "<40 hex>" }
}
```

`synced_at` is the end of the last run in which every part succeeded, in UTC.
`items.cursor` is the newest update time a run has handled. A line of
`history.jsonl` is `{"sha":"<40 hex>","committed_at":"2026-03-01T10:00:00Z","pr":9}`,
where `pr` is the merged pull request GitHub associates with the commit, or `0`.

#### Item file

A file is a YAML frontmatter between two `---` lines, a blank line, the title as
a `# ` heading, a blank line, and the blocks. The frontmatter always holds
these keys, in this order:

| Key | Value |
|-----|-------|
| `format` | `1` |
| `kind` | `issue` or `pull` |
| `repo` | `<owner>/<name>` |
| `number`, `id`, `url`, `title` | The item's number, GraphQL node id, URL, and title |
| `state` | `open`, `closed`, or `merged` |
| `state_reason` | An issue's close reason in lowercase (`completed`, `not_planned`), or `""` |
| `author`, `author_is_bot`, `author_association` | The author's login (`""` for a deleted account), whether GitHub types the author as a bot, and the author's relation to the repository |
| `labels` | The label names |
| `created_at`, `updated_at`, `closed_at`, `merged_at` | Timestamps as GitHub prints them, `""` where there is none |
| `base_branch` | A pull request's base branch |
| `closed_by_prs` | An issue's merged closing pull requests of the same repository |
| `closer_commit` | The commit that closed an issue |
| `closes_issues` | The issues of the same repository a pull request closes |

A block is a marker line, the lines of its text, and one blank line:

```markdown
<!-- planwerk-agent:mirror comment {"id":"IC_x","url":"https://github.com/acme/widgets/issues/7#issuecomment-1","author":"octocat","association":"MEMBER","created":"2026-03-01T10:00:00Z","updated":"2026-03-01T10:05:00Z","lines":2} -->
First line of the comment.
Second line.

```

The marker is `<!-- planwerk-agent:mirror <kind> <json> -->`. `lines` is the
number of lines of the text, `0` for an empty text. The text is written
unchanged. A reader takes `lines` lines after a marker and never scans a text
for markers, so a comment that holds a marker line or a `---` line opens no
block. In the JSON, `<`, `>`, and `&` are written as `\u003c`, `\u003e`, and
`\u0026`, so no value closes the HTML comment. The blocks come in this order:

| Kind | JSON keys, in order | Text |
|------|---------------------|------|
| `body` | `lines` | The item's body |
| `comment` | `id`, `url`, `author`, `association`, `created`, `updated`, `lines` | A conversation comment, in GitHub's order |
| `review` | `id`, `url`, `author`, `association`, `state`, `submitted`, `updated`, `lines` | A review's summary (pull requests); `state` as GitHub prints it, for example `APPROVED` |
| `thread` | `id`, `path`, `line`, `resolved`, `outdated`, `lines` | The diff hunk of a review thread |
| `thread-comment` | The keys of `comment` | One comment of the thread opened by the last `thread` block |
| `commit` | `sha`, `committed`, `headline`, `lines` | The commit message body |

A review thread with more than 100 comments keeps the first 100, and the run
logs a warning that names the pull request and the thread.

#### Output of a sync

The run prints to stdout:

```text
Mirror of acme/widgets at /home/u/.cache/planwerk-agent/brain/acme/widgets
items: 262 listed, 262 fetched, 262 in the mirror (116 issues, 146 pull requests)
history: 563 new commits, 563 in the mirror
wiki: acme/widgets.wiki at 1a2b3c4
```

- The `items:` line counts the issues and pull requests GitHub listed, the ones
  the run fetched, and the files the mirror holds.
- The `history:` line counts the commits the run added. When the default branch
  no longer holds the last mirrored commit (after a force push), the run
  replaces the file and prints `history: replaced, <n> commits in the mirror`.
  GitHub lists the history by commit date, so a merge can place commits behind
  the last mirrored one. The run reads past that commit until the commits it
  read and the mirrored ones add up to the commit count of the branch, and
  counts those commits as new.
- The `wiki:` line names the wiki and its commit, with `, unchanged` when the
  commit is the one the last run stored. When the wiki cannot be cloned (a
  repository without a wiki cannot), the run logs a warning, keeps the clone it
  has, and prints `wiki: <owner/repo>.wiki not mirrored`.

Log lines go to stderr. A run logs `mirroring items` after every 50 fetched
items.

#### What a run does

A run asks GitHub for the issues and pull requests updated since
`items.cursor`, oldest update first, and fetches each one whose file is missing
or older than the listing says. Issues and pull requests share the cursor: an
item's update time rises when a comment on it is edited and when a review is
submitted. The listing includes the cursor's own time, so a run right after
another one lists the newest item again and prints `items: 1 listed, 0 fetched`.

The three parts (items, history, wiki) run in that order, and the state is
saved after each. A part that fails does not stop the next one. The run then
exits non-zero with every failure under its part's name, for example
`items: fetching acme/widgets#42: <cause>`, and leaves `synced_at` as it was. A
failed wiki clone is a warning, not a failure. After a failed fetch the cursor
stays at the last item the run handled, so the next run continues there. A
GitHub rate limit arrives this way: the run does not wait it out.

A run does not remove what was deleted on GitHub, because no listing reports a
deletion. A deleted comment leaves the mirror when its issue or pull request is
updated and fetched again. A deleted or transferred issue or pull request
leaves it on `--full`.

`--full` deletes the mirror directory of this repository, and no other, before
it syncs. It asks GitHub for one listing first, so a run that cannot reach
GitHub or authenticate deletes nothing. Two full runs over the same state of
GitHub write the same `issues/`, `pulls/`, and `history.jsonl`, byte for byte.

A `state.json` of another version stops the run with `unsupported mirror state
version <n> in <file>; run brain sync --full to rebuild`. An item file of
another format stops its reader with
`unsupported mirror format <n>; run brain sync --full to rebuild`.

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
