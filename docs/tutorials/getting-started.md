# Getting started

This tutorial takes you from an empty machine to your first completed
pull-request review. Follow the steps in order; by the end you will have run
planwerk-review against a real PR and read its report.

## Prerequisites

Before you start, make sure you have:

1. **Go 1.25+** installed (or download a release binary).
2. **Claude Code** installed and authenticated (`claude` in `PATH`).
3. The **`gh` CLI** installed and authenticated (`gh auth login`).
4. Access to the target repository (for checkout/clone).

planwerk-review also relies on **git** as the underlying VCS for `gh repo clone`
and local git operations.

## Step 1: Install planwerk-review

Install the latest release with `go install`:

```bash
go install github.com/planwerk/planwerk-review/cmd/planwerk-review@latest
```

Or, on macOS / Linux with Homebrew:

```bash
brew install planwerk/tap/planwerk-review
```

Confirm the binary is on your `PATH`:

```bash
planwerk-review --version
```

## Step 2: Review your first pull request

Point the tool at any pull request you can access. Use either the full URL or
the short `owner/repo#number` form:

```bash
planwerk-review owner/repo#123
```

planwerk-review checks out the PR, loads its review patterns, runs Claude Code's
`/review` with a structured prompt, and aggregates the results. The structured
report is written to `stdout`.

## Step 3: Read the report

The report is Markdown. It opens with a machine-readable summary comment,
followed by findings grouped by severity (`BLOCKING`, `CRITICAL`, `WARNING`,
`INFO`) and a closing summary with a merge recommendation:

```markdown
# Review: owner/repo#123

<!-- planwerk-review: blocking=1 critical=2 warning=3 info=1 recommendation=HOLD -->

## BLOCKING (1)

### B-001: Hardcoded secrets in configuration
**File**: `config/auth.go:42` — **Fix**: ASK — **Confidence**: verified

**Problem**: API secret is hardcoded directly in the source code.

**Action Required**: Remove secret from code and provide it via
environment variable or secret manager.
```

To keep the report, redirect it to a file:

```bash
planwerk-review owner/repo#123 > review.md
```

## Next steps

- Post the review directly onto the PR with `--inline` —
  see [Review a pull request](/how-to/review-a-pr).
- Learn what every field in the report means —
  see [Output format](/reference/output-format).
- Turn a whole repository into tracked work —
  see [From repo to GitHub issues](/tutorials/from-repo-to-issues).
