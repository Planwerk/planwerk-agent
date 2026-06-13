# Project structure

```text
planwerk-review/
├── .github/
│   └── workflows/
│       ├── ci.yml              # Test, Build, Vet on push/PR
│       ├── lint.yml            # golangci-lint
│       └── release.yml         # GoReleaser on tag push
├── cmd/
│   └── planwerk-review/
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
│   ├── cache/
│   │   ├── cache.go            # SHA-based caching (review + propose + audit)
│   │   └── cache_test.go
│   ├── checklist/
│   │   ├── checklist.go        # Load review checklist (embedded default + override)
│   │   ├── checklist.md        # Default review checklist (embedded)
│   │   └── checklist_test.go
│   ├── cli/
│   │   └── cli.go              # Flag parsing, configuration
│   ├── claude/
│   │   ├── claude.go           # Review command entry point (Review, ReviewContext)
│   │   ├── prompt.go           # /review prompt builder (buildReviewPrompt)
│   │   ├── runner.go           # Claude Code subprocess invocation (runClaude, timeout/model)
│   │   ├── repair.go           # JSON decode with one-shot Claude repair
│   │   ├── structure.go        # Review output → structured findings + IDs
│   │   ├── claude_test.go
│   │   ├── adversarial.go      # Adversarial review pass (--thorough)
│   │   ├── audit.go            # Full-codebase audit against review patterns
│   │   ├── audit_test.go
│   │   ├── coverage.go         # Test coverage map generation (--coverage-map)
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
│   │   ├── comments.go         # Post/update PR comments (gh CLI)
│   │   ├── comments_test.go
│   │   ├── diff.go             # Fetch and parse PR diffs (DiffMap)
│   │   ├── diff_test.go
│   │   ├── issues.go           # Create/search GitHub issues (gh CLI)
│   │   ├── pr.go               # Fetch PR data, checkout (gh CLI)
│   │   ├── pr_test.go
│   │   ├── repo.go             # Clone repo (gh CLI), fetch default-branch HEAD SHA (gh API)
│   │   ├── repo_test.go
│   │   ├── review.go           # Submit PR reviews via GitHub Review API
│   │   └── review_test.go
│   ├── patterns/
│   │   ├── embedded.go         # //go:embed all:patterns + loadEmbedded()
│   │   ├── loader.go           # Load patterns from directories
│   │   ├── pattern.go          # Pattern data structure + parsing
│   │   ├── pattern_test.go
│   │   ├── sources.go          # Source dispatch (embedded + on-disk + remote)
│   │   └── patterns/           # Embedded review-pattern catalog (14 design + 67 technology + SOURCES.md)
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
│   │   │   ├── schema.go       # //go:embed of the two schema files
│   │   │   ├── report-result.schema.json  # ReviewResult (review + audit)
│   │   │   └── proposal.schema.json       # ProposalResult envelope (propose)
│   │   └── testdata/
│   │       └── schema/         # JSON fixtures validated against the schemas
│   ├── review/
│   │   ├── reviewer.go         # Orchestration: PR → Claude → Report
│   │   ├── reviewer_test.go
│   │   ├── merge.go            # Merge results from multiple review passes
│   │   └── merge_test.go
│   └── todocheck/
│       ├── todocheck.go        # Load TODOS.md for cross-reference
│       └── todocheck_test.go
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
  - `build`: `go build ./cmd/planwerk-review/`
  - `vet`: `go vet ./...`

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
