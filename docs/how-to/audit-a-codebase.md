# Audit a codebase against all patterns

Use `audit` to apply every loaded review pattern to the entire current state of
a codebase (not a diff) and produce prioritized improvement findings.

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

See the [CLI reference](/reference/cli#audit) for every flag. Audit reuses the
review finding schema, so findings render the same way — see
[Output format](/reference/output-format).

## How it works

1. **Repo Input**: The tool receives a GitHub repository reference (URL or `owner/repo`).
2. **Clone**: The repository is cloned locally with a partial clone filter.
3. **Cache Check**: The default branch HEAD SHA is fetched via `git ls-remote`. If a cached result exists for this SHA and set of flags, it is reused.
4. **Technology Detection**: The clone is scanned for language/framework markers (Go, Python, Kubernetes, Helm, GitHub Actions, …) and patterns are filtered to those applicable.
5. **Pattern Load**: Patterns are loaded from the embedded catalog (source: `internal/patterns/patterns/`) and `.planwerk/review_patterns/` (repo-specific) — identical sources to the review command.
6. **Claude Audit**: Claude is instructed to apply EVERY loaded pattern to the ENTIRE current state of the codebase (not a diff) and emit concrete violations with file paths, line numbers, code snippets, and suggested fixes. Beyond patterns, it also flags BLOCKING/CRITICAL issues it encounters (security, data loss, broken error handling) and missing tests/docs matching the project's own conventions.
7. **Structuring**: A second Claude call converts the raw findings into the same structured JSON format used by the review command (`BLOCKING`/`CRITICAL`/`WARNING`/`INFO` with fix class, confidence, related findings).
8. **Output**: Findings are rendered as Markdown (default) or JSON, with an audit-specific verdict line (`Action required` / `Improvements suggested` / `Codebase healthy`) instead of the PR merge verdict.

When creating issues with `--create-issues`, the same
[existing-issue dedupe](/how-to/analyze-a-repository#existing-issue-dedupe)
applies as for `propose`.

## Capture knowledge to the wiki

When the audit runs with `--wiki`, a read-only **capture pass** mines the audit
findings for generalizable `review_patterns/` pages worth recording on the
target repo's GitHub Wiki, so the wiki grows from whole-codebase audits too. Like
review, a standalone audit has no plan or implementation report, so it proposes
**patterns only**, never `memory/` pages.

The pass is **propose-only** by default: an audit has no PR or issue to comment
on, so the suggestions go to `stdout`; nothing is written to the wiki. It runs on
a cache miss only, is non-fatal, and is a clean no-op when nothing clears the bar.

```bash
# Propose patterns from the audit (default with --wiki)
planwerk-review audit --wiki owner/repo

# Skip the capture pass
planwerk-review audit --wiki --no-capture owner/repo

# Push the accepted pages to the wiki (confirms first; --yes for CI)
planwerk-review audit --wiki --capture-wiki owner/repo
```

See [Use the GitHub Wiki](/how-to/use-the-github-wiki#capture-knowledge-from-a-findings-producing-run-propose-only)
for the page conventions and the gated write-back.
