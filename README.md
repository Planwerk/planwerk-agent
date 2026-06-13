# planwerk-review

[![CI](https://github.com/planwerk/planwerk-review/actions/workflows/ci.yml/badge.svg)](https://github.com/planwerk/planwerk-review/actions/workflows/ci.yml)
[![codecov](https://codecov.io/gh/planwerk/planwerk-review/branch/main/graph/badge.svg)](https://codecov.io/gh/planwerk/planwerk-review)

AI-powered code review and codebase analysis tool for GitHub repositories. Uses Claude Code to automatically analyze PR changes and produce structured review results, to analyze entire repositories and generate actionable feature proposals, to audit an entire codebase against all known review patterns, to elaborate high-level issues into detailed engineering plans, or to generate copy-paste-ready prompts that fix or implement an issue.

## Features

- **Review** a pull request and produce a structured, severity-categorized report
- **Propose** feature work by analyzing an entire repository
- **Audit** a codebase against every known review pattern
- **Gap-analysis** of completed features against the actual code
- **Elaborate** a high-level issue into a detailed engineering plan
- **Prompt** generation that fixes or implements an issue
- **Implement** an elaborated issue end to end and open a draft PR
- **Fix** a PR's failing CI checks in a self-healing loop

## Quick start

Install the latest release:

```bash
go install github.com/planwerk/planwerk-review/cmd/planwerk-review@latest
# or, with Homebrew:
brew install planwerk/tap/planwerk-review
```

Review a pull request:

```bash
planwerk-review owner/repo#123
```

You need [Claude Code](https://docs.claude.com/en/docs/claude-code) and the
[`gh` CLI](https://cli.github.com/) installed and authenticated. See
[Getting started](https://planwerk.github.io/planwerk-review/tutorials/getting-started)
for the full walkthrough.

## Documentation

Full documentation lives at
**<https://planwerk.github.io/planwerk-review/>**, organized along the
[Diátaxis](https://diataxis.fr/) framework:

- [Tutorials](https://planwerk.github.io/planwerk-review/tutorials/) — learning-oriented, guided paths
- [How-to guides](https://planwerk.github.io/planwerk-review/how-to/) — task-oriented recipes
- [Reference](https://planwerk.github.io/planwerk-review/reference/) — every command, flag, and field
- [Explanation](https://planwerk.github.io/planwerk-review/explanation/) — concept, methodology, and design decisions

## License

Licensed under the Apache License 2.0 — see [LICENSE](LICENSE).
