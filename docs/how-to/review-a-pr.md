# Review a pull request

Run a Staff-Engineer-grade review of a GitHub pull request and produce a
structured report.

```bash
# Simple invocation with PR URL
planwerk-review https://github.com/owner/repo/pull/123

# Short form with owner/repo#number
planwerk-review owner/repo#123

# With an explicit pattern directory
planwerk-review --patterns ./custom-patterns owner/repo#123

# Only output specific severity levels
planwerk-review --min-severity warning owner/repo#123

# Post review as inline comments on the PR
planwerk-review --inline owner/repo#123

# Write output to file
planwerk-review owner/repo#123 > review.md
```

`--post-review` posts (and updates) a single summary comment on the PR;
`--inline` posts findings as inline review comments via the GitHub Review API
and implies `--post-review`. For every flag, see the
[CLI reference](/reference/cli#review-default-command); for the shape of the
report, see [Output format](/reference/output-format).

## How it works

1. **PR Input**: The tool receives a GitHub PR as input (URL or `owner/repo#number`).
2. **Checkout**: The PR is checked out locally (diff between base and head). PR title and description are fetched for scope analysis.
3. **Load Review Patterns**: Patterns are loaded from two sources:
   - the planwerk-review pattern catalog, embedded in the binary (source: `internal/patterns/patterns/`)
   - `.planwerk/review_patterns/` in the target repository (repo-specific patterns)
4. **Claude Code Review**: `claude /review` is executed with a structured prompt that includes persona framing, scope analysis, a two-pass checklist, suppression rules, and review patterns.
5. **Result Aggregation**: Review results are collected, deduplicated, categorized by severity, and classified by actionability. Findings are enriched with code snippets, suggested fixes, confidence levels, and cross-references.
6. **Output**: A structured report is written to `stdout`, optionally posted as a PR comment (`--post-review`), or posted as inline review comments on the PR diff (`--inline`).

The cognitive patterns and checklist Claude applies are described in
[Review methodology](/explanation/review-methodology).
