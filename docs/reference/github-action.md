# GitHub Action

The repo ships a composite GitHub Action at the root (`action.yml`) that wraps
the `review` command for use on pull requests. This page documents its inputs
and outputs; for a working workflow, see
[Use the GitHub Action](/how-to/use-the-github-action).

## Inputs

| Input | Description | Default |
|-------|-------------|---------|
| `pr-ref` | PR reference (URL, `owner/repo#number`, or bare PR number for the current repo) | the PR that triggered the workflow |
| `patterns` | Comma-separated additional pattern directories | `""` |
| `min-severity` | Minimum severity to report (`info`, `warning`, `critical`, `blocking`) | `info` |
| `format` | Output format written to the action log (`markdown`, `json`); posting always uses markdown | `markdown` |
| `max-findings` | Cap on findings returned (`0` disables cap) | `0` |
| `post-inline` | Post inline review comments and a summary via the GitHub Review API | `true` |
| `thorough` | Run the additional adversarial review pass | `false` |
| `local` | Review the repository `actions/checkout` already placed in the runner workspace (passes `--local`) instead of cloning it a second time. See [Local mode in CI](/how-to/use-the-github-action#local-mode-in-ci). | `false` |
| `version` | planwerk-review release tag to install (`latest` resolves to the most recent release) | `latest` |
| `binary-path` | Path to a pre-built binary; skips the download step (used by the in-repo smoke test) | `""` |
| `github-token` | Token used to fetch PR data and post review comments (`pull-requests: write`) | <code v-pre>${{ github.token }}</code> |
| `anthropic-api-key` | Anthropic API key consumed by Claude Code in non-interactive mode (**required**) | — |

## Outputs

| Output | Description |
|--------|-------------|
| `findings-count` | Total number of findings reported |
| `blocking-count` | Number of `BLOCKING` findings |
| `critical-count` | Number of `CRITICAL` findings |
| `warning-count` | Number of `WARNING` findings |
| `info-count` | Number of `INFO` findings |

Counts are extracted by parsing the `<!-- planwerk-review-data ... -->` JSON
block embedded in the posted PR review/comment, so they reflect the same set of
findings the reviewer sees on the PR. The same block also carries a `usage`
object with the Run's Claude token totals and estimated cost — see
[Claude Usage Totals](/reference/output-format#claude-usage-totals) — available
for extraction the same way.
