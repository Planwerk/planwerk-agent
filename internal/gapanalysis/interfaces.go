package gapanalysis

import (
	"github.com/planwerk/planwerk-review/internal/github"
	"github.com/planwerk/planwerk-review/internal/patterns"
	"github.com/planwerk/planwerk-review/internal/planwerk"
)

// AnalysisContext carries everything Claude needs to compare the spec against
// the code. The runner builds it after loading features and patterns.
type AnalysisContext struct {
	Features    []*planwerk.Feature
	Patterns    []patterns.Pattern
	MaxPatterns int
	RepoName    string // "owner/repo" for context in the prompt
}

// ClaudeGapAnalyzer compares Planwerk feature specs against the cloned repo
// and returns structured gaps. The package depends on this interface rather
// than the concrete claude package so tests can inject fakes.
type ClaudeGapAnalyzer interface {
	GapAnalysis(dir string, ctx AnalysisContext) (*Result, error)
}

// AnalyzeFn is the bare-function form of ClaudeGapAnalyzer that the CLI passes
// in. Adapted to the interface via analyzeFnAdapter.
type AnalyzeFn func(dir string, ctx AnalysisContext) (*Result, error)

type analyzeFnAdapter struct {
	fn AnalyzeFn
}

func (a analyzeFnAdapter) GapAnalysis(dir string, ctx AnalysisContext) (*Result, error) {
	return a.fn(dir, ctx)
}

// GitHubClient wraps the GitHub operations the gap-analysis pipeline needs.
// Tests inject mock clients to avoid touching the real git or gh CLI.
type GitHubClient interface {
	CloneRepo(ref string) (*github.Repo, error)
	CloneRepoLocal(ref string, opts github.LocalOptions) (*github.Repo, error)
	DefaultBranchHEAD(owner, name string) (string, error)
	ListExistingIssues(owner, name string) ([]github.ExistingIssue, error)
}

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
