# planwerk-agent

[![CI](https://github.com/planwerk/planwerk-agent/actions/workflows/ci.yml/badge.svg)](https://github.com/planwerk/planwerk-agent/actions/workflows/ci.yml)
[![codecov](https://codecov.io/gh/planwerk/planwerk-agent/branch/main/graph/badge.svg)](https://codecov.io/gh/planwerk/planwerk-agent)

AI-powered code review and codebase analysis tool for GitHub repositories. Uses Claude Code to automatically analyze PR changes and produce structured review results, to analyze entire repositories and generate actionable feature proposals, to audit an entire codebase against all known review patterns, to elaborate high-level issues into detailed engineering plans, or to generate copy-paste-ready prompts that fix or implement an issue.

## Features

### Commands

- **Review** a pull request and produce a structured, severity-categorized report
- **Propose** feature work by analyzing an entire repository
- **Audit** a codebase against every known review pattern
- **Sync** the GitHub Wiki against the code, flagging stale and redundant knowledge
- **Gap-analysis** of completed features against the actual code
- **Elaborate** a high-level issue into a detailed engineering plan
- **Prompt** generation that fixes or implements an issue
- **Implement** an elaborated issue end to end and open a draft PR
- **Ship** a Meta Issue: drive every Sub Issue to merged, in dependency order
- **Fix** a PR's failing CI checks in a self-healing loop
- **Rebase** a PR onto its base, resolve conflicts with Claude, and analyze the rebased commits
- **Address** a PR's review comments by incorporating selected threads as follow-up commits

### Skills

Some of this work needs decisions only a human can make, so it ships as
interactive [Claude Code Skills](https://code.claude.com/docs/en/skills) rather
than subcommands:

- **`/planwerk:draft`** turns a one-line idea into a ready-to-file GitHub issue
- **`/planwerk:elaborate`** expands an issue into an engineering plan grounded in the repository
- **`/planwerk:meta`** splits a Meta Issue into linked, dependency-ordered Sub Issues
- **`/planwerk:revisit`** re-checks a prepared issue against what has actually landed since, and corrects what went stale
- **`/planwerk:clarify`** answers the open questions that stopped a planning session, and records them where the next one reads them
- **`/planwerk:fix`** repairs a pull request's failing CI checks, asking you whether the code or the test is the wrong one

## Quick start

Install the latest release:

```bash
go install github.com/planwerk/planwerk-agent/cmd/planwerk-agent@latest
# or, with Homebrew:
brew install planwerk/tap/planwerk-agent
```

Review a pull request:

```bash
planwerk-agent owner/repo#123
```

Install the skills:

```bash
claude plugin marketplace add planwerk/planwerk-agent
claude plugin install planwerk@planwerk-agent
```

You need [Claude Code](https://docs.claude.com/en/docs/claude-code) and the
[`gh` CLI](https://cli.github.com/) installed and authenticated. See
[Getting started](https://planwerk.github.io/planwerk-agent/tutorials/getting-started)
for the full walkthrough, and
[Use the skills](https://planwerk.github.io/planwerk-agent/how-to/use-the-skills)
for the skills.

## Documentation

Full documentation lives at
**<https://planwerk.github.io/planwerk-agent/>**, organized along the
[Diátaxis](https://diataxis.fr/) framework:

- [Tutorials](https://planwerk.github.io/planwerk-agent/tutorials/) — learning-oriented, guided paths
- [How-to guides](https://planwerk.github.io/planwerk-agent/how-to/) — task-oriented recipes
- [Reference](https://planwerk.github.io/planwerk-agent/reference/) — every command, flag, and field
- [Explanation](https://planwerk.github.io/planwerk-agent/explanation/) — concept, methodology, and design decisions

## License

Licensed under the Apache License 2.0 — see [LICENSE](LICENSE).
