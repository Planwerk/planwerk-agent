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
| `min-severity` | Minimum severity to report (`info`, `warning`, `critical`, `blocking`). Empty falls through to `.planwerk/config.yaml` `review.min-severity`, then to the compiled-in `info` | *(empty)* |
| `format` | Output format (`markdown`, `json`, `sarif`). `markdown` and `json` go to the action log; `sarif` goes to a file named by the `sarif-file` output. Posting always uses markdown, so `post-inline` is ignored for `json` and `sarif`. The action decides by this input, so set `sarif` here rather than in `.planwerk/config.yaml`. Empty falls through to `.planwerk/config.yaml` `review.format`, then to the compiled-in `markdown` | *(empty)* |
| `max-findings` | Cap on findings returned (`0` disables cap) | `0` |
| `post-inline` | Post findings as inline review comments and keep one summary comment up to date. A finding below `min-severity` or in the summary's Unverified section gets no inline comment. Ignored when `format` is `json` or `sarif` | `true` |
| `resolve-outdated` | Resolve the inline review threads earlier runs opened once GitHub marks them outdated, unless someone replied or a current finding still covers their lines (passes `--resolve-outdated`; needs `post-inline` and a token with `contents: write`). See [Resolving outdated threads](/reference/output-format#resolving-outdated-threads) | `false` |
| `thorough` | Run the additional adversarial review pass | `false` |
| `structure-model` | Model for the mechanical JSON-structuring passes, independent of the main review model (env: `PLANWERK_STRUCTURE_MODEL`). Empty keeps the compiled-in default (`sonnet`). | `""` |
| `structure-effort` | Reasoning effort for the JSON-structuring passes (`low`, `medium`, `high`, `xhigh`, `max`; env: `PLANWERK_STRUCTURE_EFFORT`). Empty keeps the compiled-in default (`xhigh`). | `""` |
| `local` | Review the repository `actions/checkout` already placed in the runner workspace (passes `--local`) instead of cloning it a second time. See [Local mode in CI](/how-to/use-the-github-action#local-mode-in-ci). | `false` |
| `claude-code-version` | Claude Code version the action installs from npm (`@anthropic-ai/claude-code@<version>`). The default is the version this release of the action is tested with; planwerk-agent needs v2.1.306 or newer. `latest` installs the newest release; an empty value fails the step | `2.1.306` |
| `version` | planwerk-agent release tag to install (`latest` resolves to the most recent release). The archive is checked against the release's checksums file before it is unpacked; its name drops the tag's leading `v` (`planwerk-agent_0.3.0_checksums.txt` for `v0.3.0`). A missing file, a missing entry, or a different SHA-256 fails the step | `latest` |
| `binary-path` | Path to a pre-built binary, used as given: no download and no checksum check (used by the in-repo smoke test) | `""` |
| `github-token` | Token used to fetch PR data and post review comments (`pull-requests: write`; `contents: write` with `resolve-outdated`). Where another workflow copies pull request text into comments under the default token, pass a token of an account nothing else posts as; see [Outputs](#outputs) | <code v-pre>${{ github.token }}</code> |
| `anthropic-api-key` | Anthropic API key consumed by Claude Code in non-interactive mode (**required**) | — |

## Outputs

| Output | Description |
|--------|-------------|
| `findings-count` | Total number of findings reported |
| `blocking-count` | Number of `BLOCKING` findings |
| `critical-count` | Number of `CRITICAL` findings |
| `warning-count` | Number of `WARNING` findings |
| `info-count` | Number of `INFO` findings |
| `sarif-file` | Path of the SARIF 2.1.0 report when `format: sarif`, empty otherwise; see [Upload findings to code scanning](/how-to/use-the-github-action#upload-findings-to-code-scanning) |

Counts are extracted by parsing the `<!-- planwerk-agent-data ... -->` JSON
block embedded in the planwerk-agent summary comment. A review carries one only
when it predates the summary moving into that comment, or when the comment
could not be posted; the newer of the two wins, by the comment's last edit and
the review's submission time. Only a post that opens with the report's
`# Review:` heading is read, and only one by the `github-token`'s own account,
by a maintainer (`OWNER`, `MEMBER`, `COLLABORATOR`), or, when `github-token` is
the job's own token, by `github-actions[bot]`. So a block another account
pastes into a comment cannot set the counts. Under the job's own token,
though, every workflow and every comment-posting action in the repository
posts as `github-actions[bot]`. A comment one of them makes by copying a pull
request body verbatim, when that body opens with the heading and ends in a
forged block, can be read as the tool's own, by the counts and by the next
re-review ([Re-reviews](/reference/output-format#re-reviews)). In a repository
that runs such a workflow, pass `github-token` a token of an account nothing
else posts as, such as a machine user's. The step reads the token's account
from the GraphQL `viewer`. When GitHub refuses that lookup, as it may for a
GitHub App's token, the step logs a note and only the other rules apply, so
the summary a custom app's token posted is not counted. The block carries
every finding of the run, including those a re-review leaves out of the report and the inline
comments because an earlier review already reported them on an unchanged file
or a resolved thread dismissed them (see
[Re-reviews](/reference/output-format#re-reviews)), so a gate on
`blocking-count` still sees them. The same block also
carries a `usage` object with the Run's Claude token totals and estimated cost —
see [Claude Usage Totals](/reference/output-format#claude-usage-totals) — and a
`passes` array recording whether each requested pass ran, was gated out, or
failed ([Pass Status](/reference/output-format#pass-status)), both available for
extraction the same way. Each entry of `usage.passes` also records its
sessions' turns, permission denials, tool calls and session ids. The
transcripts those ids name live on the runner, so they are gone when the job
ends unless the workflow uploads `~/.claude/projects`.

With `format: sarif` nothing is posted, so the counts come from the SARIF
report instead: the results it holds, counted by each result's `severity`
property, which leaves out the findings that name no file.

## What the action installs

The action's steps are shell scripts that call no other action, so pinning
`Planwerk/planwerk-agent` to a commit fixes every step. Two inputs choose what
those steps download:

| Download | Pinned by | Checked |
|----------|-----------|---------|
| Claude Code, through `npm install -g @anthropic-ai/claude-code@<version>` | `claude-code-version` (default `2.1.306`) | npm never lets a published version be republished, so the version fixes the bytes. The review step sets `DISABLE_AUTOUPDATER=1`, so the installed version is the one that runs |
| The planwerk-agent release archive, through `gh release download` | `version` (default `latest`) | The archive's SHA-256 must match its entry in the same release's checksums file (`planwerk-agent_0.3.0_checksums.txt` for `v0.3.0`), or the step fails before it unpacks anything. This catches a truncated or swapped archive, not a release whose checksums file was replaced along with it |

The repository's own workflows pin every action to a commit as well, and
`tools/verify-action-pins.sh` enforces that on each pull request. For how to
pin the action in your workflow, see
[Pin the action and what it installs](/how-to/use-the-github-action#pin-the-action-and-what-it-installs).
