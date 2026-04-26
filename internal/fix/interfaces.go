package fix

import (
	"github.com/planwerk/planwerk-review/internal/github"
)

// FailedCheck is a flattened, prompt-friendly view of a single failing check
// run plus its truncated logs. The orchestrator builds these from the
// GitHub Checks API response and hands them to Claude.
type FailedCheck struct {
	Name          string
	Conclusion    string
	HTMLURL       string
	OutputTitle   string
	OutputSummary string
	Logs          string
	WorkflowRunID int64
}

// Context is the input for the Claude fix prompt for a single iteration.
type Context struct {
	RepoFullName  string
	PRNumber      int
	PRTitle       string
	HeadBranch    string
	HeadSHA       string
	Iteration     int
	MaxIterations int
	FailedChecks  []FailedCheck
}

// FixFn is the bare-function shape the CLI passes in to wire Claude into the
// orchestrator. Returns a short human-readable summary of what Claude did
// (already trimmed) — the orchestrator logs/prints this verbatim.
type FixFn func(dir string, ctx Context) (string, error)

// PromptBuildFn renders the fix prompt for a single iteration without invoking
// Claude. Wired in by the CLI so the fix subcommand can support --print-prompt
// mode while keeping the import direction claude -> fix.
type PromptBuildFn func(ctx Context) string

// ClaudeFixer is the injected dependency the orchestrator uses to run a
// single fix iteration. The production implementation is claude.Fix; tests
// substitute a fake that returns scripted summaries without invoking the
// real Claude CLI.
type ClaudeFixer interface {
	Fix(dir string, ctx Context) (string, error)
}

type fixFnAdapter struct {
	fn FixFn
}

func (a fixFnAdapter) Fix(dir string, ctx Context) (string, error) {
	return a.fn(dir, ctx)
}

// GitHubClient is the subset of github operations the fix loop needs. Each
// method maps to a single gh CLI invocation.
type GitHubClient interface {
	FetchAndCheckout(ref string) (*github.PR, error)
	ListChecks(owner, name, sha string) ([]github.CheckRun, error)
	FailedRunLogs(owner, name string, runID int64) (string, error)
	HeadSHA(owner, name string, branch string) (string, error)
}

// defaultGitHubClient is the production GitHubClient backed by the github
// package. Mirrors the elaborate package's adapter shape.
type defaultGitHubClient struct{}

func (defaultGitHubClient) FetchAndCheckout(ref string) (*github.PR, error) {
	return github.FetchAndCheckout(ref)
}

func (defaultGitHubClient) ListChecks(owner, name, sha string) ([]github.CheckRun, error) {
	return github.ListChecks(owner, name, sha)
}

func (defaultGitHubClient) FailedRunLogs(owner, name string, runID int64) (string, error) {
	return github.FailedRunLogs(owner, name, runID)
}

func (defaultGitHubClient) HeadSHA(owner, name string, branch string) (string, error) {
	return github.BranchHeadSHA(owner, name, branch)
}

// Prompter abstracts the "should we continue?" question asked between
// iterations when --interactive is set. The default reads y/n from stdin.
type Prompter interface {
	Confirm(message string) (bool, error)
}
