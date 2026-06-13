# Wire it up as a GitHub Action

The repo ships a composite GitHub Action at the root (`action.yml`) that wraps
the `review` command for use on pull requests. It installs Claude Code,
downloads the planwerk-review release binary, runs the review against the PR
that triggered the workflow, and posts a summary plus inline review comments.

Minimal example workflow:

```yaml
name: Planwerk Review

on:
  pull_request:

permissions:
  contents: read
  pull-requests: write

jobs:
  review:
    runs-on: ubuntu-latest
    steps:
      - uses: Planwerk/planwerk-review@v1
        with:
          anthropic-api-key: ${{ secrets.ANTHROPIC_API_KEY }}
```

The major-version tag (`@v1`) follows the standard GitHub Action convention and
is updated alongside each minor/patch release. To pin a specific version, use
the `version` input or a full tag (`Planwerk/planwerk-review@v1.2.3`).

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
dominant network cost. Set `local: true` to point planwerk-review at the
existing checkout instead (it passes `--local`), skipping the redundant clone:

```yaml
jobs:
  review:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: Planwerk/planwerk-review@v1
        with:
          local: true
          anthropic-api-key: ${{ secrets.ANTHROPIC_API_KEY }}
```

The action still passes the PR reference explicitly, so `--local` validates it
against the checkout's `origin` and switches the working tree to the PR head via
`gh pr checkout`. The default stays `false` so existing workflows are
unaffected. See [Use local mode](/how-to/use-local-mode) for the full semantics.
