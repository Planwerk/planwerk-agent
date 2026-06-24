# Use the GitHub Wiki

Make your repository's **GitHub Wiki** a source of project review patterns and
project memory for `review`, `audit`, `propose`, and the `implement` plan step.
The wiki is human-editable through the web UI and git-versioned, so this
knowledge evolves independently of code commits and never pollutes a diff.

The wiki is **off by default** and opted into per repo with `--wiki`. A GitHub
Wiki is a separate permission surface — often editable by any authenticated
GitHub user (or any collaborator, including triage-only members), and never
gated by branch protection or PR review. Enabling it feeds its content into the
agent's prompts, including the `implement` agent that writes code and opens pull
requests, so turn it on only for repos whose wiki editors you trust as much as
your committers.

## What the tool reads

The tool reads two directories from the target repo's wiki:

| Wiki page | Purpose |
|-----------|---------|
| `review_patterns/<name>.md` | A project review pattern in the standard [Pattern Format](/reference/review-patterns#pattern-format). It loads through the wiki precedence tier — below the committed `.planwerk/review_patterns` (so a committed pattern overrides a same-named wiki one) and below an explicit `--patterns`. |
| `memory/<name>.md` | A free-form **project memory** page: decisions, conventions, and context. Every page is concatenated (sorted by filename, capped at 64 KB) into a memory block injected into the analysis prompts and the implement plan. |

Any other page — `Home`, `_Sidebar`, navigation, or prose that does not parse as
a pattern — is ignored, so a normal wiki can hold both human navigation and
machine-read patterns side by side.

## Author the pages

A GitHub Wiki is itself a git repo (`https://github.com/owner/repo.wiki.git`).
Edit it through the web UI, or clone and push:

```bash
git clone https://github.com/owner/repo.wiki.git
cd repo.wiki
mkdir -p review_patterns memory
# a review pattern in the standard format
cat > review_patterns/db-query-builder.md <<'MD'
# Review Pattern: DB queries go through QueryBuilder

**Review-Area**: architecture
**Severity**: WARNING

## What to check

All database access must go through the QueryBuilder, never raw SQL strings.

## Why it matters

Raw SQL bypasses the query allow-list and parameterization the QueryBuilder
enforces.
MD
# a project-memory page
cat > memory/decisions.md <<'MD'
We pin every dependency and never float a version range.
Errors are returned as Problem Details (RFC 9457), never bare strings.
MD
git add -A && git commit -m "Add review patterns and memory" && git push
```

The wiki must be **initialized** first: create at least one page through the
repository's Wiki tab on github.com before the `.wiki.git` clone exists. A wiki
that was never initialized is treated as "no wiki" — the run proceeds with the
other pattern tiers and no project memory.

## Enable, disable, and pin

The wiki is **off by default**. You opt in per run:

```bash
# Off by default: no wiki is read
planwerk-review review owner/repo#123

# Turn it on for one run
planwerk-review review --wiki owner/repo#123

# Pin the wiki to a fixed commit, tag, or branch for a reproducible run
planwerk-review review --wiki --wiki-ref v1.4.0 owner/repo#123
```

Or set defaults in `.planwerk/config.yaml`:

```yaml
wiki:
  enabled: true              # opt the wiki in (the default is off); false is the same as --no-wiki
  repo: owner/repo           # override the wiki source (default: the target repo)
  ref: main                  # pin to a branch/tag/commit
```

Precedence is flag → environment variable (`PLANWERK_WIKI`, `PLANWERK_WIKI_REF`)
→ config file → default-off. `--no-wiki` overrides `--wiki`.

## Private wikis

A private wiki is cloned with your GitHub token (taken from `gh auth token`),
so it works transparently whenever you can already access the repo with `gh`. A
public wiki clones anonymously. The token is never written to the cached clone
or to git's output.

## Reproducibility

The wiki is resolved to a concrete commit at the start of each run. That commit
is recorded in the report header (`> Wiki: owner/repo.wiki @ <short-sha>`) and
folded into the cache key, so editing the wiki re-runs the review rather than
serving a stale cached result, and two runs against the same wiki commit produce
the same review.

See the [Review patterns reference](/reference/review-patterns#github-wiki) for
the precedence model and the [CLI reference](/reference/cli) for every flag.
