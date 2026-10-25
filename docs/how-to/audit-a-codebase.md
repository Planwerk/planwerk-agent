# Audit a codebase against all patterns

Use `audit` to apply every loaded review pattern to the entire current state of
a codebase (not a diff) and produce prioritized improvement findings.

```bash
# Apply all loaded review patterns to an entire codebase
planwerk-agent audit https://github.com/owner/repo

# Short form
planwerk-agent audit owner/repo

# Only output findings at or above a severity threshold
planwerk-agent audit --min-severity warning owner/repo

# JSON output for tooling
planwerk-agent audit --format json owner/repo

# SARIF for GitHub code scanning
planwerk-agent audit --format sarif owner/repo > audit.sarif

# Force fresh audit (ignore cache)
planwerk-agent audit --no-cache owner/repo

# Cap the number of findings Claude returns
planwerk-agent audit --max-findings 25 owner/repo

# Write findings to file
planwerk-agent audit owner/repo > audit.md
```

See the [CLI reference](/reference/cli#audit) for every flag. Audit reuses the
review finding schema, so findings render the same way — see
[Output format](/reference/output-format).

When you are sitting in a checkout and want the findings verified line by line
and filed as issues you approve one by one, the
[`/planwerk:audit` skill](/how-to/audit-a-codebase-interactively) does the same
audit in your session: it quotes the code behind every finding, brings you the
calls only you can make, and files one issue per work package behind your yes.

## How it works

1. **Repo Input**: The tool receives a GitHub repository reference (URL or `owner/repo`).
2. **Clone**: The repository is cloned locally with a partial clone filter.
3. **Cache Check**: The default branch HEAD SHA is fetched via `git ls-remote`. If a cached result exists for this SHA and set of flags, it is reused.
4. **Technology Detection**: The clone is scanned for language/framework markers (Go, Python, Kubernetes, Helm, GitHub Actions, …) and patterns are filtered to those applicable.
5. **Pattern Load**: Patterns are loaded from the embedded catalog (source: `internal/patterns/patterns/`) and `.planwerk/review_patterns/` (repo-specific) — identical sources to the review command.
6. **Claude Audit**: Claude is instructed to apply EVERY loaded pattern to the ENTIRE current state of the codebase (not a diff) and emit concrete violations with file paths, line numbers, code snippets, and suggested fixes. Beyond patterns, it also flags BLOCKING/CRITICAL issues it encounters (security, data loss, broken error handling) and missing tests/docs matching the project's own conventions. The session emits its findings as JSON under a schema, in the format the review command uses (`BLOCKING`/`CRITICAL`/`WARNING`/`INFO` with actionability, confidence, related findings).
7. **Finding hygiene**: Before the result is cached, the findings pass the stages `review` runs on its own findings. Each quote is first anchored to its line in the cited file; an audit never moves a finding to another file. The snippet gate then looks for each finding's quoted code in the checkout: there is no diff, so the whole checkout is the ground truth, and a quote found nowhere in it is demoted. A finding that names a missing file, cites no line and quotes nothing passes, because the file is absent. Claim verification then re-checks every BLOCKING and CRITICAL finding's claim in one read-only session on the `--finder-model` tier. A WARNING or INFO finding the snippet gate demotes, and any finding the verifier refutes, moves to the Unverified section with its reason. A BLOCKING or CRITICAL finding whose quote was not found keeps its section and shows the reason. Nothing is dropped. The counts are recorded in `gates`, the anchor's included (see [Demotion-Gate Records](/reference/output-format#demotion-gate-records)).
8. **Output**: Findings are rendered as Markdown (default), JSON, or SARIF, with an audit-specific verdict line (`Action required` / `Improvements suggested` / `Codebase healthy`) instead of the PR merge verdict.

When creating issues with `--create-issues`, the same
[existing-issue dedupe](/how-to/analyze-a-repository#existing-issue-dedupe)
applies as for `propose`. An issue body carries the finding's claim-check and
snippet-check notes, so a demoted finding says why it was demoted.

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
planwerk-agent audit --wiki owner/repo

# Skip the capture pass
planwerk-agent audit --wiki --no-capture owner/repo

# Push the accepted pages to the wiki (confirms first; --yes for CI)
planwerk-agent audit --wiki --capture-wiki owner/repo
```

See [Use the GitHub Wiki](/how-to/use-the-github-wiki#capture-knowledge-from-a-findings-producing-run-propose-only)
for the page conventions and the gated write-back.
