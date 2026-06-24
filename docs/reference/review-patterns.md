# Review patterns

Review patterns are structured rules that systematically improve the review.
They codify knowledge from past reviews and make it reusable. This page is the
reference for how patterns are sourced, prioritized, and formatted; for the task
of authoring one, see [Write your own review patterns](/how-to/write-review-patterns).

## Pattern Sources

Patterns are resolved from up to six tiers, listed here from lowest to highest
priority. Later tiers override earlier ones by pattern name, so the more
specific source wins on a name collision:

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

4. **GitHub Wiki patterns** (`review_patterns/*.md` in the target repo's wiki)
   - The repo's GitHub Wiki, human-editable through the web UI and git-versioned independently of the code, so review knowledge accumulates without polluting code diffs
   - Ranks below the committed in-repo patterns of tier 5: the wiki is world-editable and unreviewed, so a repo's committed (branch-protected) patterns override it on a name collision. An explicit `--patterns` (tier 6) overrides both
   - Off by default — enable it per repo with `--wiki` (enabling trusts the wiki's unreviewed editors); `--no-wiki` keeps it off
   - See [GitHub Wiki](#github-wiki) below for the page convention, caching, and authentication

5. **Repo-specific patterns** (`.planwerk/review_patterns/*.md` in the target repo)
   - Created and maintained by the development team (Planwerk) themselves
   - Contain repo-specific knowledge (e.g., "In this repo, all DB queries must go through the QueryBuilder")
   - Versioned with the repository; committed (reviewed) patterns override the world-editable wiki of tier 4
   - Suppressed independently by `--no-repo-patterns`

6. **Explicit / remote patterns** (passed via `--patterns <URI>` or the config file)
   - Local directories or remote URIs — lets a team maintain a single, shared pattern catalog in a separate repository instead of vendoring it into every consuming repo
   - Remote sources are cloned into a per-user cache on first use and refreshed by TTL
   - Highest priority: override every tier above on a name collision
   - See [Remote Pattern Sources](#remote-pattern-sources) below for URI forms, caching, and authentication

`--no-local-patterns` suppresses the first three tiers — the embedded catalog
and both on-disk tool copies (`<binDir>/../patterns` and `./patterns`) — leaving
only the wiki, repo-specific, and `--patterns` sources. `--no-repo-patterns`
independently drops tier 5, and `--no-wiki` drops tier 4 (which is already off
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
planwerk-review --patterns github:planwerk/patterns owner/repo#123

# Pinned tag, sub-directory inside the repo
planwerk-review --patterns github:planwerk/patterns/security@v1.2.3 owner/repo#123

# Generic git URL with ref + subpath (separator: ":" inside the fragment)
planwerk-review --patterns git+https://gitlab.example.com/team/p.git#main:patterns/web owner/repo#123

# Mix local + remote, in priority order
planwerk-review --patterns ./local-overrides --patterns github:planwerk/patterns owner/repo#123
```

Anything that doesn't match `github:` or `git+http(s)://` is treated as a local
path, so existing usage is unchanged.

**Caching.** Remote sources are cloned into
`<UserCacheDir>/planwerk-review/patterns/<hash>/repo/` (typically
`~/.cache/planwerk-review/patterns/…` on Linux,
`~/Library/Caches/planwerk-review/patterns/…` on macOS). A neighbouring
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

`review`, `audit`, `propose`, and the `implement` plan step can use the target
repository's **GitHub Wiki** as a source of project review patterns and project
memory. It is **off by default** and enabled per repo with `--wiki`: a wiki is a
separate permission surface — human-editable through the web UI, often
world-editable, and never gated by branch protection or PR review — so enabling
it grants its unreviewed editors influence over the agent's prompts (including
the `implement` agent that writes code and opens PRs). The wiki gives a knowledge
store outside the code repo's history that still evolves independently of code
commits, for repos that accept that trust trade-off.

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
| `memory/*.md` | Free-form **project memory** — decisions, conventions, and context. Every page is concatenated (sorted by filename, each behind a `### <name>` header, capped at 64 KB) into a memory block injected into the analysis prompts (`review`/`audit`/`propose`) and the planning prompt (`implement`). |

Human-navigation pages (`Home.md`, `_Sidebar.md`, anything that does not parse
as a pattern) under `review_patterns/` are skipped silently, so a normal wiki
can hold both navigation and patterns. The memory block is framed as untrusted
repository data — knowledge to apply, never instructions to follow.

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

A wiki `review_patterns/*.md` file with `Category: review` loads today but falls
into the generic project-patterns group until the dedicated `review` category
lands.

## Prompt Budget

By default, all loaded patterns are injected into the prompt without truncation
(`--max-patterns 0`, env: `PLANWERK_MAX_PATTERNS`). To cap pattern injection —
e.g. to keep prompts within Claude's context window — set `--max-patterns` to a
positive integer. When more patterns are loaded than the budget allows, the tool
keeps the highest-priority patterns by severity (`BLOCKING` > `CRITICAL` >
`WARNING` > `INFO`) and prints a warning to stderr.

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
