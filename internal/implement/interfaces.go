package implement

import (
	"github.com/planwerk/planwerk-review/internal/github"
)

// Context is the input for the Claude implement prompt. It carries the
// elaborated issue plus enough repository identity to let the prompt write
// commit messages, branch names, and the eventual draft PR.
type Context struct {
	RepoFullName string
	IssueNumber  int
	IssueTitle   string
	IssueBody    string
	IssueURL     string
	IssueState   string
}

// ImplementFn is the bare-function shape the CLI passes in to wire Claude
// into the orchestrator. Returns a short human-readable summary of what
// Claude did (already trimmed) — the orchestrator logs/prints this verbatim.
type ImplementFn func(dir string, ctx Context) (string, error)

// PromptBuildFn renders the implement prompt for the given issue context
// without invoking Claude. Wired in by the CLI so the implement subcommand
// can support --print-prompt mode while keeping the import direction
// claude -> implement.
type PromptBuildFn func(ctx Context) string

// BarePromptBuildFn renders a self-contained implement prompt from the
// issue reference alone — no issue body, no repository walk. Wired in by
// the CLI for --print-bare-prompt mode while keeping the import direction
// claude -> implement.
type BarePromptBuildFn func(repoFullName string, issueNumber int) string

// ClaudeImplementer is the injected dependency the orchestrator uses to
// run a single implementation session. The production implementation is
// claude.Implement; tests substitute a fake that returns scripted summaries
// without invoking the real Claude CLI.
type ClaudeImplementer interface {
	Implement(dir string, ctx Context) (string, error)
}

type implementFnAdapter struct {
	fn ImplementFn
}

func (a implementFnAdapter) Implement(dir string, ctx Context) (string, error) {
	return a.fn(dir, ctx)
}

// GitHubClient is the subset of github operations the implement command
// needs: fetching the source issue and cloning the repository so Claude has
// a working tree to operate on. Each method maps to a single gh CLI
// invocation under the hood.
type GitHubClient interface {
	GetIssue(owner, name string, number int) (*github.Issue, error)
	CloneRepo(ref string) (*github.Repo, error)
}

// defaultGitHubClient is the production GitHubClient backed by the github
// package. Mirrors the elaborate package's adapter shape.
type defaultGitHubClient struct{}

func (defaultGitHubClient) GetIssue(owner, name string, number int) (*github.Issue, error) {
	return github.GetIssue(owner, name, number)
}

func (defaultGitHubClient) CloneRepo(ref string) (*github.Repo, error) {
	return github.CloneRepo(ref)
}
