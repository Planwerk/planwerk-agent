# Analyze a repository and generate proposals

Use `propose` to analyze an entire repository and generate concrete, actionable
feature proposals suitable for GitHub issues.

```bash
# Analyze a repository and generate feature proposals
planwerk-agent propose https://github.com/owner/repo

# Short form
planwerk-agent propose owner/repo

# Output as JSON
planwerk-agent propose --format json owner/repo

# Output as GitHub issue templates
planwerk-agent propose --format issues owner/repo

# Force fresh analysis (ignore cache)
planwerk-agent propose --no-cache owner/repo

# Interactively create GitHub issues from proposals
planwerk-agent propose --create-issues owner/repo

# Write proposals to file
planwerk-agent propose owner/repo > proposals.md
```

See the [CLI reference](/reference/cli#propose) for every flag.

## How it works

1. **Repo Input**: The tool receives a GitHub repository reference (URL or `owner/repo`).
2. **Clone**: The repository is cloned locally with a partial clone filter.
3. **Cache Check**: The default branch HEAD SHA is fetched via `git ls-remote`. If a cached result exists for this SHA, it is reused.
4. **Claude Analysis**: Claude performs a deep codebase analysis covering architecture, code quality, feature gaps, DX, performance, security, testing, and CI/CD.
5. **Structuring**: A second Claude call converts the raw analysis into structured JSON proposals with priority, category, scope, and acceptance criteria.
6. **Output**: Proposals are rendered as Markdown (default), JSON, or GitHub issue templates.
7. **Interactive Issue Creation** (optional): With `--create-issues`, the user is shown a summary table and walked through each proposal with a prompt to create a GitHub issue via `gh`.

Proposals are grounded in the same review-pattern catalog used by `review` and
`audit`. Patterns load from the tool's embedded catalog, any `--patterns`
directories you supply, and the target repo's `.planwerk/review_patterns/`. When
a proposal addresses a pattern (closes a gap, hardens against a violation, or
extends coverage) Claude references the pattern by name so reviewers can trace
the rationale back to the catalog. The propose session receives the catalog's
index and reads a pattern's file from a directory the run writes when it needs
one.

## Existing-issue dedupe

Before rendering, both `propose` and `audit` query the target repo's GitHub
issues (open and closed) once via `gh issue list` and drop any
proposal/finding whose title matches an existing issue. This keeps repeated runs
idempotent: work that's already tracked upstream disappears from every output
format — Markdown, JSON, `--format=issues`, and the interactive `--create-issues`
flow.

Matching is case-insensitive, trims surrounding whitespace, collapses internal
whitespace, and ignores trailing punctuation (`.`, `!`, `?`, `,`, `;`, `:`).
Audit-issue titles no longer carry a `[SEVERITY]` prefix, so severity drift
between runs does not split a finding into a new duplicate. If `gh issue list`
fails, dedupe is skipped with a warning and the pipeline continues.

Pass `--no-issue-dedupe` (on either subcommand) to disable the filter for
debugging or when you want to see the full candidate list regardless of upstream
state.

## Stop re-proposing rejected ideas

Existing-issue dedupe only filters ideas already filed as issues. To stop
`propose` from re-suggesting an idea your team considered and *rejected* — one
that was never filed — keep a knowledge base of rejected concepts in the target
repo under `.planwerk/out-of-scope/`, one Markdown file per concept:

```text
.planwerk/out-of-scope/
├── plugin-system.md
└── hosted-web-dashboard.md
```

Give each file a `#` heading (used as the concept's name; the filename without
its extension is the fallback) and a short rationale:

```markdown
# Plugin system

A runtime plugin loader was rejected: the pattern catalog is compiled in on
purpose.
```

`propose` reads the directory on every run and instructs Claude not to
re-propose those concepts or close variants of them. The directory is optional —
a repo without it runs unchanged — and read-only: rejecting a proposal does not
add an entry, you curate the files yourself. Because the knowledge base is
committed, editing it busts the per-commit analysis cache automatically, so the
next run picks up your change.
