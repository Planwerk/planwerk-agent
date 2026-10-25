# Review a pull request

Run a Staff-Engineer-grade review of a GitHub pull request and produce a
structured report.

```bash
# Simple invocation with PR URL
planwerk-agent https://github.com/owner/repo/pull/123

# Short form with owner/repo#number
planwerk-agent owner/repo#123

# With an explicit pattern directory
planwerk-agent --patterns ./custom-patterns owner/repo#123

# Only output specific severity levels
planwerk-agent --min-severity warning owner/repo#123

# Post review as inline comments on the PR
planwerk-agent --inline owner/repo#123

# Also resolve the tool's own threads a push made outdated
planwerk-agent --inline --resolve-outdated owner/repo#123

# SARIF for GitHub code scanning
planwerk-agent --format sarif owner/repo#123 > results.sarif

# Leave lock files, vendored and generated files out of the review
planwerk-agent --diff-exclusions owner/repo#123

# Write output to file
planwerk-agent owner/repo#123 > review.md
```

`--post-review` posts a single summary comment on the PR and updates it on
every later run. `--inline` implies `--post-review`: it keeps that summary
comment and adds a review via the GitHub Review API only for the inline
comments a run posts, so re-reviews do not pile up summaries. The inline
comments follow `--min-severity` and `--min-confidence` the way the report
does, and a finding in the report's Unverified section stays in the summary
without an inline comment. So does a finding whose quote pins no single place
in the reviewed code. `--resolve-outdated` also resolves the tool's own
inline threads once GitHub marks them outdated, unless someone replied or a
current finding still covers their lines (see
[Resolving outdated threads](/reference/output-format#resolving-outdated-threads)).
With `--diff-exclusions`, the report lists every file it left out of the
review ([Excluded Files](/reference/output-format#excluded-files)). For every
flag, see the [CLI reference](/reference/cli#review-default-command);
for the shape of the report, see [Output format](/reference/output-format).

On a re-review, a posted review leaves out the findings the last one already
listed in a severity section, on a file that has not changed since. It also
leaves out a finding whose inline thread you resolved while the code it quotes
stayed the same, so resolving the thread is how to dismiss a finding for later
runs. The resolve has to come from an account other than the one that posted
the comment. Neither kind is shown in the report or as a new inline comment,
and a note at the end of the report lists them; see
[Re-reviews](/reference/output-format#re-reviews).

## How it works

1. **PR Input**: The tool receives a GitHub PR as input (URL or `owner/repo#number`).
2. **Checkout**: The PR is checked out locally (diff between base and head). The checked-out commit is verified against the head GitHub reports: when the branch was pushed to during the clone, the newer commit is the one reviewed, cached, and named in the posted review (the data block's `commit_sha` and an inline review's `commit_id`). PR title and description are fetched for scope analysis.
3. **Load Review Patterns**: Patterns are loaded from two sources:
   - the planwerk-agent pattern catalog, embedded in the binary (source: `internal/patterns/patterns/`)
   - `.planwerk/review_patterns/` in the target repository (repo-specific patterns)

   With `--scope-patterns` (experimental, off by default) the review carries the bodies of the patterns that apply to every file or whose technology or path matches a changed file, and lists the rest as an index it reads from disk.
4. **Claude Code Review**: a `claude -p` session runs a structured review prompt that includes persona framing, scope analysis, a two-pass checklist, suppression rules, review patterns, and the list of changed files, which the session accounts for one by one. The session reads a diff file the tool wrote beforehand rather than running `git diff` itself (see [The Diff Every Pass Reads](/explanation/review-methodology#the-diff-every-pass-reads)).
5. **Result Aggregation**: Each finding's line is anchored on the code it quotes before the passes are merged ([Line Anchoring](/explanation/review-methodology#line-anchoring)). Review results are collected, deduplicated, categorized by severity, and classified by actionability. Findings are enriched with code snippets, suggested fixes, confidence levels, and cross-references. The session's account of the changed files is compared with that list, and the report names any file it did not review ([Changed-File Coverage](/reference/output-format#changed-file-coverage)).
6. **Output**: A structured report is written to `stdout` as Markdown, JSON, or SARIF, optionally posted as a PR comment (`--post-review`), or posted as inline review comments on the PR diff (`--inline`).

The cognitive patterns and checklist Claude applies are described in
[Review methodology](/explanation/review-methodology).

## Preview a review before it runs

To see what a review would run before you pay for it, ask for a dry run:

```bash
planwerk-agent --dry-run --specialists owner/repo#123
```

The dry run prepares the review as a review does (the checkout, the wiki, the
patterns, the cache key) and stops before the first Claude session. Read three
things in its report:

- **Cache**: `hit` means a review of this commit with these flags would be
  served the cached result and start no session; `--no-cache` would run the
  passes listed.
- **Passes**: which specialists the adaptive gate lets through for this diff,
  and why it skips the others.
- **Prompt sizes**: how many pattern bodies and bytes each pass would be sent,
  before you raise `--max-patterns` or turn on `--specialists` or
  `--thorough`. The sizes are bytes, not tokens, and the passes that work on
  the findings are not sized.

To inspect the review prompt itself, or paste it into a manual session, print
it:

```bash
planwerk-agent --print-prompt owner/repo#123
```

The printed prompt carries the pattern bodies and the project memory, and it
names the `git diff` command where a run hands the session a diff file it
writes. See the [CLI reference](/reference/cli#review-default-command) for both
flags and [Dry-run report](/reference/output-format#dry-run-report-dry-run) for
the report's fields.

## Review a large pull request

When a pull request touches dozens of files, split the review so each part
gets a session of its own:

```bash
planwerk-agent --partition-files 15 owner/repo#123
```

A pull request whose diff keeps more than 15 changed files, not counting its
`.planwerk/` planning artifacts, is cut into partitions of at most 15 that
follow its directories and are balanced by changed lines, and each partition
is reviewed by its own session. Each partition costs about one review session,
so a 60-file pull request runs at least four review sessions. The adversarial
pass and the specialists still see the whole change, and the report records
the partitions. To set it for a repository, add `review.partition-files` to
`.planwerk/config.yaml` ([Configuration file](/reference/configuration#schema)).
The GitHub Action has no input for it, and the file is loaded from the working
directory, so the Action reads the key only when the job runs
`actions/checkout` before it, as the
[Local mode in CI](/how-to/use-the-github-action#local-mode-in-ci) workflow
does. On a `pull_request` run the default checkout is the pull request's merge
commit, so the pull request's own copy of the file applies. The
[minimal workflow](/how-to/use-the-github-action) has no checkout step, and
there the key does nothing. See the
[CLI reference](/reference/cli#review-default-command) for the flag and
[Partitioned Review](/explanation/review-methodology#partitioned-review) for how
the sessions split the work and how their findings merge.

## Capture knowledge to the wiki

When the review runs with `--wiki`, a read-only **capture pass** mines the review
findings for generalizable `review_patterns/` pages worth recording on the
target repo's GitHub Wiki, so the wiki grows from every review — not only from
full `implement` cycles. A standalone review has no plan or implementation
report, so it proposes **patterns only**, never `memory/` pages.

The pass is always **propose-only**: the suggestions go to `stdout`, and — only
with `--post-review` — as a PR comment of their own, beside the summary;
**nothing is ever written to the wiki**.
Unlike `implement` and `audit`, review has no `--capture-wiki` flag and never pushes
the accepted pages: it analyzes an untrusted pull request and the proposal pass reads
attacker-controlled source, so auto-pushing its free-form pages would let an
external contributor poison the shared knowledge base. To grow the wiki from
captured patterns, run the write-back from a trusted source — `implement` on your
own branch or `audit` on your own repo (see
[Push accepted pages to the wiki](/how-to/use-the-github-wiki#push-accepted-pages-to-the-wiki-opt-in)).
The pass runs on a cache miss only, is non-fatal, and is a clean no-op when nothing
clears the bar.

```bash
# Propose patterns from the review (default with --wiki)
planwerk-agent --wiki owner/repo#123

# Skip the capture pass
planwerk-agent --wiki --no-capture owner/repo#123
```

See [Use the GitHub Wiki](/how-to/use-the-github-wiki#capture-knowledge-from-a-findings-producing-run-propose-only)
for the page conventions and the gated write-back.
