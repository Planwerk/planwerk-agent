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

## Capture knowledge from a findings-producing run (propose-only)

When `implement`, `review`, or `audit` runs with `--wiki`, a read-only
**capture** pass proposes new wiki pages from the findings — so the wiki grows
from every findings-producing run, not only by hand. Generalizable review
findings become candidate `review_patterns/` pages; under `implement`, durable
rationale from the plan and the implementation report also becomes candidate
`memory/` pages. (A standalone `review` or `audit` has no plan or report, so it
proposes patterns only.) Every candidate is deduplicated against the wiki's
existing entries and the bundled pattern catalog, so capture does not re-propose
what is already recorded.

The pass is **propose-only**: the suggestions surface in the run report — and as
a comment on the source issue (`implement`) or PR (`review --post-review`) — and
**nothing is written to the wiki**. Review them and add the ones worth keeping.
It is on by default whenever a wiki is resolved; disable it with `--no-capture`.
It runs on a cache miss only, so a cached `review`/`audit` proposes nothing.

The proposed `memory/` pages follow a small write convention so they stay easy to
maintain by hand or by a later automated write-back:

- **One page per durable decision.** A memory page records a single "why" — a
  non-obvious choice, a constraint to honor, a trade-off that was weighed — not a
  catch-all log.
- **A stable, descriptive slug.** Re-running capture on the same decision reuses
  the same `memory/<slug>.md` path, so it **updates the page in place** rather
  than appending a near-duplicate.
- **A provenance marker.** Each proposed page begins with an HTML comment —
  `<!-- planwerk-review: captured from owner/repo#123 -->` — that marks it as
  tool-authored (rather than hand-authored) and names the issue it came from. The
  marker is fixed for a given source, so a re-run does not churn the page.

### Push accepted pages to the wiki (opt-in)

By default the capture pass writes nothing — it only proposes. Pass
`--capture-wiki` to turn the accepted pages into real wiki growth: a separate,
mechanical write phase clones the wiki fresh, writes each page (provenance marker
included) under the pinned `planwerk-review` identity, and pushes. When the wiki
has never been initialized, the first page creates its initial commit.

The write-back is available only from a **trusted source** — `implement` (your own
branch) and `audit` (your own repo). **`review` ignores `--capture-wiki` and is
always propose-only**: a review analyzes an untrusted pull request and the proposal
pass reads attacker-controlled source, so auto-pushing its free-form pages would let
an external contributor poison the shared knowledge base via indirect prompt
injection. Capture patterns from a review by reading its proposals and adding the
ones worth keeping by hand.

```bash
planwerk-review implement --wiki --capture-wiki owner/repo#123          # confirms, then pushes
planwerk-review implement --wiki --capture-wiki --yes owner/repo#123    # non-interactive (CI)
planwerk-review audit --wiki --capture-wiki owner/repo                  # from a standalone audit
```

The write is gated to match the rest of the wiki surface. Claude never pushes: it
authored the page bytes in the read-only proposal pass, and this phase performs
the push, preserving the read-only-author / write-phase separation. The phase
confirms interactively first and **refuses a non-TTY run without `--yes`**. The
gate is also settable per repo via the `PLANWERK_CAPTURE_WIKI` environment
variable or a `capture.wiki: true` config key (flag → env → config → off). The
write-back is non-fatal: a refusal or push failure degrades back to propose-only
rather than failing the run. The push authenticates a private wiki exactly as
[`sync`](/how-to/sync-the-wiki) does — see its write-phase note for the auth
details.

## Keep the wiki trustworthy

Wiki knowledge drifts as the code changes, so the highest-priority source quietly
rots. Run [`sync`](/how-to/sync-the-wiki) to flag entries that reference code that
no longer exists (stale) or that duplicate another entry (redundant), and to prune
them after confirmation — keeping the wiki worth reading.

See the [Review patterns reference](/reference/review-patterns#github-wiki) for
the precedence model and the [CLI reference](/reference/cli) for every flag.
