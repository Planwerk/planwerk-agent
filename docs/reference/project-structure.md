# Project structure

```text
planwerk-agent/
├── .github/
│   └── workflows/
│       ├── ci.yml              # Test, Build, Vet on push/PR
│       ├── lint.yml            # golangci-lint
│       └── release.yml         # GoReleaser on tag push
├── cmd/
│   └── planwerk-agent/
│       ├── main.go             # CLI wiring: build runtimeDeps, register subcommands
│       ├── root_cmd.go         # review (root) command + persistent & cache flags
│       ├── resolve.go          # env-var / flag resolution helpers, format constants
│       ├── version.go          # build-version metadata (--version)
│       ├── cache_cmd.go        # cache subcommand group + cache helpers
│       └── <name>_cmd.go       # one file per subcommand (newProposeCmd, newAuditCmd, …)
├── internal/
│   ├── audit/
│   │   ├── auditor.go          # Orchestration: Repo → Patterns → Claude → Findings
│   │   └── auditor_test.go
│   ├── brain/
│   │   ├── memory.go           # brain memory: print the project memory index or one page
│   │   ├── memory_test.go
│   │   ├── bootstrap.go        # brain bootstrap: the run, its reports, stop and resume, the gated wiki write
│   │   ├── units.go            # Group the history into ordered units (BuildUnits)
│   │   ├── docs.go             # Discover decision documents and cut them into chunks
│   │   ├── state.go            # .planwerk-brain-sync: state.json, page files, the wiki refresh
│   │   ├── source.go           # Source seam, APISource, a unit's items, the working-set index
│   │   ├── mirror_source.go    # MirrorSource: the history read from the local mirror (brain bootstrap --source mirror)
│   │   └── search.go           # brain search: the run and its text and JSON output
│   ├── cache/
│   │   ├── cache.go            # SHA-based caching (review + propose + audit)
│   │   └── cache_test.go
│   ├── checklist/
│   │   ├── checklist.go        # Load review checklist (embedded default + override)
│   │   ├── checklist.md        # Default review checklist (embedded)
│   │   └── checklist_test.go
│   ├── cli/
│   │   └── config_file.go      # .planwerk/config.yaml loading, applied onto each command's Options
│   ├── domains/
│   │   ├── domains.go          # Load planning domain sweep (embedded default + override)
│   │   ├── domains.md          # Default domain list (embedded)
│   │   └── domains_test.go
│   ├── claude/
│   │   ├── claude.go           # Review command entry point (Review, ReviewContext)
│   │   ├── prompt.go           # review prompt builder (buildReviewPrompt)
│   │   ├── runner.go           # Claude Code subprocess invocation (runClaude, timeout/model)
│   │   ├── repair.go           # JSON decode with one-shot Claude repair
│   │   ├── structure.go        # Finder JSON → findings + IDs, schema repair
│   │   ├── claude_test.go
│   │   ├── adversarial.go      # Adversarial review pass (review --thorough, implement's review loop)
│   │   ├── audit.go            # Full-codebase audit against review patterns
│   │   ├── audit_test.go
│   │   ├── coverage.go         # Test coverage map generation (--coverage-map)
│   │   ├── bootstrap.go        # brain bootstrap: unit analysis and page review sessions
│   │   ├── elaborate.go        # Issue → detailed engineering plan
│   │   ├── propose.go          # Codebase analysis for proposals
│   │   └── propose_test.go
│   ├── elaborate/
│   │   ├── elaborate.go        # Pipeline: Issue → Repo → Claude → Detailed body
│   │   ├── elaborate_test.go
│   │   ├── interfaces.go
│   │   ├── renderer.go         # Markdown body assembly
│   │   └── result.go           # Structured elaboration result
│   ├── prompt/
│   │   ├── interfaces.go
│   │   ├── prompt.go           # Deterministic Claude Code prompt assembler
│   │   └── prompt_test.go
│   ├── doccheck/
│   │   ├── doccheck.go         # Detect stale documentation files
│   │   └── doccheck_test.go
│   ├── github/
│   │   ├── client.go           # Client: the one value every command's GitHub interface is satisfied by
│   │   ├── comments.go         # Post/update PR comments (gh CLI)
│   │   ├── continuation.go     # Split an oversized issue body into continuation comments, and merge them back
│   │   ├── comments_test.go
│   │   ├── diff.go             # Fetch and parse PR diffs (DiffMap)
│   │   ├── history.go          # List the default-branch commits, merged PRs, and closed issues (gh GraphQL)
│   │   ├── item.go             # List items by update time, read one issue or PR with its whole conversation
│   │   ├── thread.go           # Read an issue or PR with every comment, review, and commit
│   │   ├── diff_test.go
│   │   ├── issues.go           # Create/search GitHub issues (gh CLI)
│   │   ├── pr.go               # Fetch PR data, checkout (gh CLI)
│   │   ├── pr_test.go
│   │   ├── repo.go             # Clone repo (gh CLI), fetch default-branch HEAD SHA (gh API)
│   │   ├── repo_test.go
│   │   ├── review.go           # Submit PR reviews via GitHub Review API
│   │   ├── review_test.go
│   │   └── githubtest/         # The shared test fake for Client (imported by tests only)
│   ├── hygiene/                # Shared finding hygiene (review + implement self-review)
│   │   ├── merge.go            # Multi-pass merge, confidence boost, ConfirmedBy provenance
│   │   ├── dedup.go            # File-less duplicate fold (DedupFileless)
│   │   ├── snippets.go         # Quote-or-demote snippet gate (VerifySnippets)
│   │   └── claims.go           # Claim verification demotion (VerifyClaims, ClaimVerdict)
│   ├── mirror/                 # brain sync: the local mirror of a repository's knowledge on GitHub
│   │   ├── mirror.go           # The mirror directory, state.json, the adapter seam, the run (Syncer)
│   │   ├── format.go           # The item file: frontmatter and blocks (Render, Parse, ReadFrontmatter)
│   │   ├── items.go            # ItemsAdapter: issues and pull requests, fetched by update time
│   │   ├── history.go          # HistoryAdapter: history.jsonl, the default branch's commit list
│   │   ├── wiki.go             # WikiAdapter: a full clone of the wiki
│   │   └── testdata/           # One rendered issue and one rendered pull request (golden files)
│   ├── patterns/
│   │   ├── catalog.go          # Materialize: the loaded catalog on disk, plus its index
│   │   ├── memory.go           # MaterializeMemory: the wiki's project memory on disk, plus its index
│   │   ├── embedded.go         # //go:embed all:patterns + loadEmbedded()
│   │   ├── loader.go           # Load patterns from directories
│   │   ├── pattern.go          # Pattern data structure + parsing
│   │   ├── pattern_test.go
│   │   ├── sources.go          # LoadForRepo: the one catalog loader (embedded + wiki + repo + remote)
│   │   ├── patternstest/       # Shared test helpers for the project memory and the wiki seam (imported by tests only)
│   │   └── patterns/           # Embedded review-pattern catalog (16 design + 67 technology + review + SOURCES.md)
│   ├── propose/
│   │   ├── interactive.go      # Interactive GitHub issue creation flow
│   │   ├── proposal.go         # Proposal data structure + categorization
│   │   ├── proposal_test.go
│   │   ├── proposer.go         # Orchestration: Repo → Claude → Proposals
│   │   ├── proposer_test.go
│   │   └── renderer.go         # Markdown/JSON/Issues output
│   ├── report/
│   │   ├── categorizer.go      # Severity categorization
│   │   ├── categorizer_test.go
│   │   ├── coverage.go         # Coverage result data structure + rendering
│   │   ├── coverage_test.go
│   │   ├── finding.go          # Finding data structure (Severity, Actionability, FixClass, Confidence)
│   │   ├── finding_test.go
│   │   ├── inline.go           # Format findings as GitHub inline review comments
│   │   ├── inline_test.go
│   │   ├── renderer.go         # Markdown/JSON output (compact format, GitHub Alerts, audit verdicts)
│   │   ├── renderer_test.go
│   │   ├── audit_renderer_test.go
│   │   ├── schema_test.go      # JSON Schema contract tests (fixtures + renderer drift guard)
│   │   ├── schema/             # Embedded JSON Schemas for --format json output
│   │   │   ├── schema.go       # //go:embed of the schema files
│   │   │   ├── report-result.schema.json  # ReviewResult (review + audit)
│   │   │   ├── proposal.schema.json       # ProposalResult envelope (propose)
│   │   │   └── finder-output.schema.json  # Finder pass wire contract (--json-schema)
│   │   └── testdata/
│   │       └── schema/         # JSON fixtures validated against the schemas
│   ├── review/
│   │   ├── reviewer.go         # Orchestration: PR → Claude → Report
│   │   ├── reviewer_test.go
│   │   ├── merge.go            # Merge results from multiple review passes
│   │   └── merge_test.go
│   ├── search/                 # brain search: the full-text index of a mirror directory
│   │   ├── index.go            # index.sqlite: the schema, the version check, the refresh, the block cutting (Open)
│   │   ├── query.go            # The ranked query and the block read (Search, Block)
│   │   └── surface.go          # Surface: the command and the permission rule a run hands a read-only session
│   └── todocheck/
│       ├── todocheck.go        # Load TODOS.md for cross-reference
│       └── todocheck_test.go
├── .claude/                    # This repository's own Claude Code tooling (never shipped)
│   ├── agents/
│   │   ├── prompt-auditor.md   # Read-only audit of one prompt builder against the doctrine
│   │   └── skill-auditor.md    # Read-only audit of a plugin skill or shared document
│   ├── commands/
│   │   └── audit-prompt.md     # /audit-prompt <builder>: delegates to prompt-auditor
│   ├── skills/
│   │   └── iterate-prompts/    # /iterate-prompts: one audit iteration over every prompt surface
│   │       ├── SKILL.md
│   │       ├── reference/      # The brief and ledger templates, the seven audit slices
│   │       └── scripts/        # probe-models.sh (alias → model id), check.sh (is an iteration due)
│   └── prompt-audits/          # The ledger: one file per iteration, newest is the state
├── .claude-plugin/
│   └── marketplace.json        # Claude Code marketplace catalog (this repo)
├── plugins/
│   └── planwerk/               # The plugin: the thirteen interactive skills
│       ├── .claude-plugin/
│       │   └── plugin.json
│       ├── shared/             # One source for the format, style, doctrine, gh calls
│       │   ├── issue-format.md        # Draft depth, titles, the footer
│       │   ├── issue-format-plan.md   # Depth 2 and the rules a plan satisfies
│       │   ├── issue-format-survey.md # The survey Meta Issue cleanup files
│       │   ├── issue-format-audit.md  # The audit issue, draft depth plus evidence
│       │   ├── house-style.md
│       │   ├── humanizer.md
│       │   ├── interaction.md
│       │   ├── memory.md              # Reading the project memory through brain memory
│       │   ├── cross-repo.md
│       │   ├── commits.md             # Trailers and cross-repo references
│       │   ├── commits-fold.md        # The fold, the push, the SHA repair
│       │   ├── github.md              # The gh commands every skill needs
│       │   ├── github-relations.md    # Neighborhood query, sub-issue wiring
│       │   └── github-checks.md       # A pull request and its checks
│       └── skills/             # One directory per skill, SKILL.md each
│           ├── audit/
│           ├── clarify/
│           ├── cleanup/
│           ├── decide/
│           ├── diagnose/
│           ├── draft/
│           ├── elaborate/
│           ├── fix/
│           ├── humanize/
│           ├── implement/
│           ├── meta/
│           ├── rebase/
│           └── revisit/
├── tools/
│   └── toolbox/
│       ├── Dockerfile          # Toolbox image every make target runs in (Go, golangci-lint, claude)
│       └── run.sh              # Starts the toolbox container around the checkout
├── Makefile
├── go.mod
├── go.sum
├── .golangci.yml
├── .goreleaser.yml
└── README.md
```

## GitHub Workflows

### CI (`ci.yml`)

- **Trigger**: Push to `main`, Pull Requests
- **Jobs**:
  - `test`: `go test ./...` on matrix (Ubuntu, macOS)
  - `build`: `go build ./cmd/planwerk-agent/`
  - `vet`: `go vet ./...`
  - `plugin`: `claude plugin validate --strict` on the marketplace and plugin manifests

### Lint (`lint.yml`)

- **Trigger**: Push to `main`, Pull Requests
- **Jobs**:
  - `lint`: `golangci-lint run`

### Release (`release.yml`)

- **Trigger**: Tag push (`v*`)
- **Jobs**:
  - GoReleaser: Binaries for Linux/macOS/Windows (amd64, arm64)
  - GitHub Release with changelog

## Dependencies

- **Go 1.25+**
- **Claude Code**: Must be installed and authenticated on the system (`claude` in PATH)
- **gh CLI**: Required for cloning repos (incl. private), fetching PR metadata, checkout, and default-branch HEAD lookup (`gh` in PATH)
- **git**: Required as the underlying VCS for `gh repo clone` and local git operations
