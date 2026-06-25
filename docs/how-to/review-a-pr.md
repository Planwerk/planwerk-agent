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

## Capture knowledge to the wiki

When the review runs with `--wiki`, a read-only **capture pass** mines the review
findings for generalizable `review_patterns/` pages worth recording on the
target repo's GitHub Wiki, so the wiki grows from every review — not only from
full `implement` cycles. A standalone review has no plan or implementation
report, so it proposes **patterns only**, never `memory/` pages.

The pass is always **propose-only**: the suggestions go to `stdout`, and — only
with `--post-review` — as a PR comment; **nothing is ever written to the wiki**.
Unlike `implement` and `audit`, review never pushes the accepted pages, even under
`--capture-wiki`: it analyzes an untrusted pull request and the proposal pass reads
attacker-controlled source, so auto-pushing its free-form pages would let an
external contributor poison the shared knowledge base. To grow the wiki from
captured patterns, run the write-back from a trusted source — `implement` on your
own branch or `audit` on your own repo (see
[Push accepted pages to the wiki](/how-to/use-the-github-wiki#push-accepted-pages-to-the-wiki-opt-in)).
The pass runs on a cache miss only, is non-fatal, and is a clean no-op when nothing
clears the bar.

```bash
# Propose patterns from the review (default with --wiki)
planwerk-review --wiki owner/repo#123

# Skip the capture pass
planwerk-review --wiki --no-capture owner/repo#123
```

See [Use the GitHub Wiki](/how-to/use-the-github-wiki#capture-knowledge-from-a-findings-producing-run-propose-only)
for the page conventions and the gated write-back.
