# Wire it up as a GitHub Action

The repo ships a composite GitHub Action at the root (`action.yml`) that wraps
the `review` command for use on pull requests. It installs Claude Code,
downloads the planwerk-agent release binary, runs the review against the PR
that triggered the workflow, keeps one summary comment on the PR up to date,
and posts inline review comments. On each later push it leaves out the
findings it already posted on files the push did not change
([Re-reviews](/reference/output-format#re-reviews)). Resolve a finding's
inline thread to keep it out of later reports while the code it quotes stays
unchanged. Reading the review threads needs `pull-requests: read`, which the
`pull-requests: write` below already grants.

Minimal example workflow:

```yaml
name: Planwerk Agent

on:
  pull_request:

permissions:
  contents: read
  pull-requests: write

jobs:
  review:
    runs-on: ubuntu-latest
    steps:
      - uses: Planwerk/planwerk-agent@v1
        with:
          anthropic-api-key: ${{ secrets.ANTHROPIC_API_KEY }}
```

The major-version tag (`@v1`) follows the standard GitHub Action convention and
is updated alongside each minor/patch release. To pin a specific version, use
the `version` input or a full tag (`Planwerk/planwerk-agent@v1.2.3`).

For the complete list of inputs and outputs, see the
[GitHub Action reference](/reference/github-action).

The action is exercised end-to-end on every relevant PR via
`.github/workflows/action-smoke.yml`, which builds the binary from source and
runs the action with `binary-path` pointing at the dev build. The smoke job is
gated on `pull_request.head.repo.full_name == github.repository` so forked PRs
(which cannot read `secrets.ANTHROPIC_API_KEY`) skip cleanly.

## Local mode in CI

`actions/checkout` already places the repository in the runner workspace before
the action runs, so the default behavior clones the same repo a second time into
a temp dir — on a moderate repo this doubles the cold-start time and the
dominant network cost. Set `local: true` to point planwerk-agent at the
existing checkout instead (it passes `--local`), skipping the redundant clone:

```yaml
jobs:
  review:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: Planwerk/planwerk-agent@v1
        with:
          local: true
          anthropic-api-key: ${{ secrets.ANTHROPIC_API_KEY }}
```

The action still passes the PR reference explicitly, so `--local` validates it
against the checkout's `origin` and switches the working tree to the PR head via
`gh pr checkout`. The default stays `false` so existing workflows are
unaffected. See [Use local mode](/how-to/use-local-mode) for the full semantics.

## Upload findings to code scanning

With `format: sarif` the action writes the review as a SARIF report to a file
and names it in the `sarif-file` output, and
`github/codeql-action/upload-sarif` turns it into code scanning alerts on the
pull request and under **Security → Code scanning**:

```yaml
name: Planwerk Agent code scanning

on:
  pull_request:

permissions:
  contents: read
  pull-requests: read
  security-events: write # upload-sarif
  actions: read # upload-sarif, private repositories only

jobs:
  review:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
      - uses: Planwerk/planwerk-agent@<commit-sha> # vX.Y.Z
        id: review
        with:
          version: vX.Y.Z
          local: true
          format: sarif
          anthropic-api-key: ${{ secrets.ANTHROPIC_API_KEY }}
      - uses: github/codeql-action/upload-sarif@9b3254085494b55a0f7c4c2b3d2d52cc712ce370 # v4.39.1
        if: steps.review.outputs.sarif-file != ''
        with:
          sarif_file: ${{ steps.review.outputs.sarif-file }}
          category: planwerk-agent
```

A SARIF run posts nothing. `post-inline` is skipped, because the comment the
action posts is the Markdown report; the alerts take the place of the inline
comments, and the count outputs count the results of the SARIF report.
`local: true` reviews the checkout and leaves it on the pull request head, so
`upload-sarif` reads the files at the commit the review read and uploads the
analysis for that commit, under `refs/pull/<number>/head`; the alerts' lines
are the lines the review cites. A workflow whose checkout stays on the merge
commit, as it does without `local: true`, uploads the analysis for the merge
commit instead, so a change on the base branch above a finding in the same file
shifts its line. `category` names the analysis, so an upload under another
category, such as one of an `audit` run, does not replace it.

Code scanning has to be available for the repository: it is on a public
repository, and a private one needs GitHub Code Security. A pull request from a
fork gets no secrets and a read-only token, so the review has no API key and the
upload no write access. For how each finding maps to a SARIF result, see
[SARIF](/reference/output-format#sarif).

## Resolve outdated threads

A push that rewrites the lines an inline comment sat on leaves its thread
outdated but open. With `resolve-outdated: 'true'` the action passes
`--resolve-outdated` and resolves those threads after it posts: only the
tool's own threads that GitHub marks outdated, that nobody replied to, and
whose original lines no finding of the current run still covers. The full list
of guards is in
[Resolving outdated threads](/reference/output-format#resolving-outdated-threads).

```yaml
jobs:
  review:
    runs-on: ubuntu-latest
    permissions:
      contents: write
      pull-requests: write
    steps:
      - uses: Planwerk/planwerk-agent@<commit-sha> # vX.Y.Z
        with:
          version: vX.Y.Z
          resolve-outdated: 'true'
          anthropic-api-key: ${{ secrets.ANTHROPIC_API_KEY }}
```

Resolving needs `contents: write`, because GitHub ties resolving a review
thread to write access to the repository rather than to
`pull-requests: write`. With `contents: read`, the first resolve is refused,
the run logs a warning and resolves nothing more, and the review still
succeeds. On a pull request from a fork the token is read-only, so resolving is
refused the same way posting is. `post-inline` has to stay `true`, its default:
without it the action skips `resolve-outdated` with a note in the log.
