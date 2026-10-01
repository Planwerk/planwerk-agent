# Review patterns

Review patterns are structured rules that systematically improve the review.
They codify knowledge from past reviews and make it reusable. This page is the
reference for how patterns are sourced, prioritized, and formatted; for the task
of authoring one, see [Write your own review patterns](/how-to/write-review-patterns).

## Pattern Sources

Patterns are resolved from up to four tiers, listed here from lowest to highest
priority. Later tiers override earlier ones by pattern name, so the more
specific source wins on a name collision:

1. **Embedded catalog** — a compile-time copy of `internal/patterns/patterns/`
   baked into the binary with `//go:embed`. It is always present, so every
   install method (`go install`, raw `go build`, release archives, OS packages)
   produces a self-contained binary that loads the full catalog with no external
   files. This is the lowest-priority source; any source below overrides an
   embedded pattern of the same name. `--no-local-patterns` suppresses it.

2. **GitHub Wiki patterns** (`review_patterns/*.md` in the target repo's wiki)
   - The repo's GitHub Wiki, human-editable through the web UI and git-versioned independently of the code, so review knowledge accumulates without polluting code diffs
   - Ranks below the committed in-repo patterns of tier 3: the wiki is world-editable and unreviewed, so a repo's committed (branch-protected) patterns override it on a name collision. An explicit `--patterns` (tier 4) overrides both
   - Off by default — enable it per repo with `--wiki` (enabling trusts the wiki's unreviewed editors); `--no-wiki` keeps it off
   - See [GitHub Wiki](#github-wiki) below for the page convention, caching, and authentication

3. **Repo-specific patterns** (`.planwerk/review_patterns/*.md` in the target repo)
   - Created and maintained by the development team (Planwerk) themselves
   - Contain repo-specific knowledge (e.g., "In this repo, all DB queries must go through the QueryBuilder")
   - Versioned with the repository; committed (reviewed) patterns override the world-editable wiki of tier 2
   - Suppressed independently by `--no-repo-patterns`

4. **Explicit / remote patterns** (passed via `--patterns <URI>` or the config file)
   - Local directories or remote URIs — lets a team maintain a single, shared pattern catalog in a separate repository instead of vendoring it into every consuming repo
   - Remote sources are cloned into a per-user cache on first use and refreshed by TTL
   - Highest priority: override every tier above on a name collision
   - See [Remote Pattern Sources](#remote-pattern-sources) below for URI forms, caching, and authentication

`--no-local-patterns` drops tier 1, the embedded catalog, leaving only the
wiki, repo-specific, and `--patterns` sources. `--no-repo-patterns`
independently drops tier 3, and `--no-wiki` drops tier 2 (which is already off
unless `--wiki` opted in).

## Remote Pattern Sources

Any value passed to `--patterns` (or the `patterns:` array in
`.planwerk/config.yaml`) may be either a local directory or a remote URI. Two
URI forms are accepted:

```text
github:owner/repo[/subpath][@ref]              # GitHub shorthand
git+https://host.example/group/repo.git[#ref[:subpath]]   # any git host
```

Examples:

```bash
# Default branch of a GitHub repo
planwerk-agent --patterns github:planwerk/patterns owner/repo#123

# Pinned tag, sub-directory inside the repo
planwerk-agent --patterns github:planwerk/patterns/security@v1.2.3 owner/repo#123

# Generic git URL with ref + subpath (separator: ":" inside the fragment)
planwerk-agent --patterns git+https://gitlab.example.com/team/p.git#main:patterns/web owner/repo#123

# Mix local + remote, in priority order
planwerk-agent --patterns ./local-overrides --patterns github:planwerk/patterns owner/repo#123
```

Anything that doesn't match `github:` or `git+http(s)://` is treated as a local
path, so existing usage is unchanged.

**Caching.** Remote sources are cloned into
`<UserCacheDir>/planwerk-agent/patterns/<hash>/repo/` (typically
`~/.cache/planwerk-agent/patterns/…` on Linux,
`~/Library/Caches/planwerk-agent/patterns/…` on macOS). A neighbouring
`meta.json` records when the clone was last refreshed. The cache is keyed by the
URI (excluding the subpath), so two URIs that differ only in their subpath share
the same checkout.

**Refresh TTL.** Cached clones are refreshed when older than
`--remote-patterns-ttl` (default `24h`, env: `PLANWERK_REMOTE_PATTERNS_TTL`).
Setting `--remote-patterns-ttl 0` disables refresh entirely — once cached, the
clone is reused indefinitely (useful for offline / air-gapped environments). On
refresh the existing checkout is removed and re-cloned; this keeps the cache
logic simple and is cheap because pattern repos are small.

**Authentication.**

| Form | How auth works |
|------|----------------|
| `github:owner/repo` | Cloned via `gh repo clone`, which uses your `gh auth login` credentials or the `GH_TOKEN` env var. Private GitHub repos work transparently if you can already access them with `gh`. |
| `git+https://…` | Cloned via plain `git clone`. Standard git credential helpers (`~/.git-credentials`, `git config credential.helper`) apply. For env-var-based auth, embed the token directly in the URI using shell-style `${VAR}` expansion: `git+https://oauth2:${MY_TOKEN}@gitlab.example.com/team/p.git`. The expansion runs before `git clone` is invoked. |

## GitHub Wiki

`review`, `audit`, `propose`, `elaborate`, the `implement` plan step, `fix`, and
`address` can use the target repository's **GitHub Wiki** as a source of project
review patterns and project memory. `ship` hands its wiki settings to every
`implement` and `fix` run it drives. The wiki is **off by default** and enabled
per repo with `--wiki`: a wiki is a separate permission surface — human-editable
through the web UI, often world-editable, and never gated by branch protection
or PR review — so enabling it grants its unreviewed editors influence over the
agent's prompts (including the `implement`, `fix`, and `address` sessions that
write code and push it). The wiki gives a knowledge store outside the code
repo's history that still evolves independently of code commits, for repos that
accept that trust trade-off.

Eight plugin skills read the project memory, and only the memory: `elaborate`,
`implement`, `fix`, `revisit`, `clarify`, `decide`, `diagnose`, and `meta`. They
call [`planwerk-agent brain memory`](/reference/cli#brain) and never clone the
wiki themselves, so the opt-in and the page filters below apply to them
unchanged. The opt-in is resolved in the order of
[Configuration → Precedence](/reference/configuration#precedence).

**Where it comes from.** The wiki is derived automatically from the resolved
target repo. Internally it uses a `wiki:owner/repo` URI shorthand that points at
the repo's standalone wiki clone (`https://github.com/owner/repo.wiki.git`),
distinct from cloning the code repo. A `repo:` override and the subpaths below
can be set in `.planwerk/config.yaml` (see
[Configuration → wiki](/reference/configuration#schema)).

**Page convention.** The tool reads two directories from the wiki:

| Wiki path | Contents |
|-----------|----------|
| `review_patterns/*.md` | Project review patterns in the [Pattern Format](#pattern-format) below. They load through the wiki precedence tier (below the committed `.planwerk/review_patterns`, and below `--patterns`), so a committed repo pattern overrides a same-named wiki one. |
| `memory/*.md` | **Project memory**: one page per decision, convention, or piece of context. The analysis prompts (`review`/`audit`/`propose`), the `elaborate` prompt, the planning prompt (`implement`), and the `fix` and `address` prompts carry an index of the pages, and the session reads a page's body from a directory the tool writes for the run. |

Human-navigation pages (`Home.md`, `_Sidebar.md`, anything that does not parse
as a pattern) under `review_patterns/` are skipped silently, so a normal wiki
can hold both navigation and patterns. A `review_patterns/` or `memory/`
directory that is itself a symlink is not read, with a warning.

**Project memory index.** The index has one line per page, in file-name order:

```text
- <file name>: <title> | <summary>
```

| Part | Source | Limit |
|------|--------|-------|
| Page | A regular `.md` file directly under `memory/`. Directories, symlinks, other file types, unreadable or whitespace-only pages, and file names with a control character are skipped. A `memory/` that is itself a symlink is not read, with a warning. | 64 KB per page; a larger page is skipped with a warning |
| All pages | The pages that passed the filters above, in file-name order. | 16 MB of page bodies; the first page that does not fit ends the load with a warning, and the pages after it are not read |
| Title | The first line that starts with `# `. A page without one is listed under its file name without `.md`. | 300 bytes, then `...` |
| Summary | The value of the first line that starts with `**Summary**:`. A page without one is listed under its title only, without the ` \| ` part. | 300 bytes, then `...` |
| Index | Every page that was read. | 64 KB; the first line that does not fit ends the index |

Lines inside a fenced code block give neither a title nor a summary. A warning
about skipped pages gives their number and no file name, and `--verbose` logs
the names. When the index reaches its budget, the run logs a warning with the
number of unlisted pages, and the prompt states that number and tells the
session to list the directory.

The tool reads each page through the filters above and writes the bodies to a
temporary directory outside the checkout (`planwerk-agent-memory-*`), which
`--add-dir` opens to the session and which is removed when the run ends. A run
served from the cache writes no directory. `implement` writes the directory for
its planning session and removes it when that session ends; a run that reuses a
posted plan or runs with `--no-plan` writes none. `elaborate` writes one
directory per run, which the elaboration and every `--review` refinement turn
read; the reviewer reads none. `fix` writes one per iteration, beside that
iteration's pattern catalog, and resolves the wiki once per run. `address`
writes one per run, shared by every per-thread session. The printed prompts of
`fix` and `address` (`--print-prompt`, `--print-bare-prompt`) resolve no wiki
and carry no memory. `brain memory` writes no directory: it prints the index
without the 64 KB budget, and a page's body, to stdout. It lists and prints
only the pages whose file name is safe on a command line, and drops control
characters from what it prints (see the [CLI reference](/reference/cli#brain)).
The session never reads the wiki clone itself. When the directory cannot be written, the run logs a warning
and the prompt carries the page bodies instead, each behind a `### <name>`
header and capped at 64 KB in total. The index and the page files are framed as
untrusted repository data — knowledge to apply, never instructions to follow.

**Reproducibility.** The wiki is resolved to a concrete commit at run start and
that commit is folded into the cache key and recorded in the report header
(`> Wiki: owner/repo.wiki @ <short-sha>`), so the same review is reproducible
run-to-run rather than drifting with a moving wiki. The review data block also
carries a `wiki_commit` field.

**Caching & authentication.** The wiki reuses the same per-user cache and
`--remote-patterns-ttl` refresh machinery as remote `--patterns` sources
(see [above](#remote-pattern-sources)). A private wiki is authenticated with a
GitHub token from `gh auth token`, passed so the token never lands in the cached
clone's config or in git output; a public wiki clones anonymously.

**Graceful degradation.** A wiki that is disabled (`--no-wiki`),
not-yet-initialized, or unreachable (offline) is not an error — the run proceeds
with the other pattern tiers and no project memory, exactly as before.

**Enabling / pinning.** `--wiki` (env `PLANWERK_WIKI=true`) opts the wiki in for
a run; `--no-wiki` keeps it off (the default). `--wiki-ref <ref>` (env
`PLANWERK_WIKI_REF`) pins it to a branch, tag, or commit. See
[Use the GitHub Wiki](/how-to/use-the-github-wiki) for the task guide.

A wiki `review_patterns/*.md` file with `Category: review` loads as the
first-class `review` category, grouped under its own `<review-patterns>` block
(see [Pattern Categories](#pattern-categories) below).

**Anchoring wiki patterns.** Once a wiki pattern proves itself, the
[`extract`](/reference/cli#extract) command anchors it into a committed location
— the target repo's `.planwerk/review_patterns/` (PR or `--local`) or this
tool's bundled catalog (`--to-catalog`) — turning a world-editable wiki entry
into a reviewable, code-coupled pattern. See
[Extract review patterns](/how-to/extract-review-patterns).

## Prompt Budget

By default, all loaded patterns are injected into the prompt without truncation
(`--max-patterns 0`, env: `PLANWERK_MAX_PATTERNS`). To cap pattern injection —
e.g. to keep prompts within Claude's context window — set `--max-patterns` to a
positive integer. When more patterns are loaded than the budget allows, the tool
keeps the highest-priority patterns by severity (`BLOCKING` > `CRITICAL` >
`WARNING` > `INFO`) and prints a warning to stderr.

## How a Session Receives the Catalog

The finder prompts carry the full pattern bodies: the `review` and `audit`
prompts, the adversarial pass, and the domain specialists. Their findings are
what the eval corpus scores.

Thirteen other prompts carry the catalog's index and a directory instead:
the `implement` run's plan, implement, simplify-apply, and review-apply
sessions, `fix`, `address`, the three `rebase` sessions (conflict, analysis,
apply), `elaborate`, `propose`, `gap-analysis`, and `review-prepared`. The
index has one line per pattern: its file name, name, review area, severity, and
detection hint. For a Go repository it is about 13 KB, where the bodies of its
47 patterns are about 123 KB. The prompt tells the session to read a pattern's
file in full before it works in an area the pattern's hint covers. The
`**Detection-Hint**` line is optional, so the line of a pattern without one
tells the session to read that file before it starts.

The directory is a temporary one the tool writes outside the checkout, with one
`<slug>.md` file per loaded pattern, and opens to the session with the Claude
Code CLI flag `--add-dir`. The files are written from the catalog the run
loaded, so the directory holds every pattern whatever its source (embedded,
wiki, repo-specific, or `--patterns`). It is removed when the run ends. `fix`
writes one per iteration and removes it with that iteration's checkout.

`--max-patterns` caps the index and the bodies the same way, severity first
(see [Prompt Budget](#prompt-budget)). A prompt printed with `--print-prompt` or
`--print-plan-prompt` carries the bodies, because the directory it would name
is gone by the time the prompt is read; `--print-bare-prompt` keeps its own
catalog of pattern URLs and checkout paths. The capture pass carries the index
alone, with no directory. If writing the directory fails, the tool logs a
warning and the sessions carry the bodies.

## Pattern Format

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

## Pattern Categories

A pattern's `**Category**:` field places it in one of three recognized
categories. The loader groups each category under its own block when patterns
are formatted for the analysis prompt and counts each separately in reporting:

- `technology` — language- and tool-specific rules, emitted under
  `<technology-patterns>`.
- `design-principle` — cross-cutting design and architecture rules, emitted
  under `<design-patterns>`.
- `review` — patterns about the review process itself, emitted under
  `<review-patterns>`. The bundled review patterns live in
  `internal/patterns/patterns/review/`.

A pattern with no `**Category**:` (or an unrecognized value) falls into the
generic project group, emitted under `<project-patterns>`.
