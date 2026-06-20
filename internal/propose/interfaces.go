package propose

import (
	"github.com/planwerk/planwerk-review/internal/github"
	"github.com/planwerk/planwerk-review/internal/patterns"
)

// AnalysisContext carries the inputs the analysis prompt grounds itself in.
// Patterns let the proposer reference the project's pattern catalog so
// suggestions stay specific instead of reverting to generic software advice.
type AnalysisContext struct {
	Patterns    []patterns.Pattern
	MaxPatterns int
	RepoName    string // "owner/repo" for context in the prompt
	// OutOfScope carries rejected ideas loaded from the target repo's
	// .planwerk/out-of-scope/ knowledge base so the analysis stops
	// re-suggesting them.
	OutOfScope []OutOfScopeEntry
	// Glossary is the target repo's domain vocabulary loaded from its
	// CONTEXT.md / .planwerk/context.md so proposals use the repo's own terms.
	// Populated only by propose; the other commands sharing AnalysisContext
	// leave it empty (like OutOfScope), so their prompts are unaffected.
	Glossary string
}

// ClaudeAnalyzer performs the Claude-backed codebase analysis that produces
// feature proposals. The propose package depends on this interface rather
// than the concrete claude package so tests can inject fakes.
type ClaudeAnalyzer interface {
	Analyze(dir string, ctx AnalysisContext) (*ProposalResult, error)
}

// GitHubClient wraps the GitHub operations the propose pipeline needs:
// cloning the repository, resolving the default-branch HEAD for cache keying,
// and listing existing issues for duplicate detection. Tests inject mock
// clients to avoid touching the real git or gh CLI.
type GitHubClient interface {
	CloneRepo(ref string) (*github.Repo, error)
	CloneRepoLocal(ref string, opts github.LocalOptions) (*github.Repo, error)
	DefaultBranchHEAD(owner, name string) (string, error)
	ListExistingIssues(owner, name string) ([]github.ExistingIssue, error)
}

// AnalyzeFn is the bare-function form of ClaudeAnalyzer that existing callers
// (the CLI) pass in. It is adapted to the interface via analyzeFnAdapter.
type AnalyzeFn func(dir string, ctx AnalysisContext) (*ProposalResult, error)

type analyzeFnAdapter struct {
	fn AnalyzeFn
}

func (a analyzeFnAdapter) Analyze(dir string, ctx AnalysisContext) (*ProposalResult, error) {
	return a.fn(dir, ctx)
}

// defaultGitHubClient is the production GitHubClient backed by the github package.
type defaultGitHubClient struct{}

func (defaultGitHubClient) CloneRepo(ref string) (*github.Repo, error) {
	return github.CloneRepo(ref)
}

func (defaultGitHubClient) CloneRepoLocal(ref string, opts github.LocalOptions) (*github.Repo, error) {
	return github.UseLocalRepo(ref, opts)
}

func (defaultGitHubClient) DefaultBranchHEAD(owner, name string) (string, error) {
	return github.DefaultBranchHEAD(owner, name)
}

func (defaultGitHubClient) ListExistingIssues(owner, name string) ([]github.ExistingIssue, error) {
	return github.ListAllIssues(owner, name)
}
