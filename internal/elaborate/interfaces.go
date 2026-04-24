package elaborate

import (
	"github.com/planwerk/planwerk-review/internal/github"
	"github.com/planwerk/planwerk-review/internal/patterns"
)

// Context is the input for the Claude elaboration prompt. Patterns are
// injected so the elaboration is grounded in the same review catalog used
// by review/audit/propose, instead of the model inventing repo conventions.
type Context struct {
	Patterns    []patterns.Pattern
	MaxPatterns int
	RepoName    string
	Issue       *github.Issue
}

// ClaudeElaborator turns a high-level issue into a detailed engineering
// plan. The elaborate package depends on this interface so tests can inject
// a deterministic fake.
type ClaudeElaborator interface {
	Elaborate(dir string, ctx Context) (*Result, error)
}

// ElaborateFn is the bare-function form of ClaudeElaborator that the CLI
// passes in. It is adapted to the interface via elaborateFnAdapter.
type ElaborateFn func(dir string, ctx Context) (*Result, error)

type elaborateFnAdapter struct {
	fn ElaborateFn
}

func (a elaborateFnAdapter) Elaborate(dir string, ctx Context) (*Result, error) {
	return a.fn(dir, ctx)
}

// GitHubClient wraps the GitHub operations the elaborate pipeline needs:
// resolving the default-branch HEAD for cache keying, fetching the source
// issue, cloning the repo, and (optionally) writing the elaborated body
// back to the issue.
type GitHubClient interface {
	DefaultBranchHEAD(owner, name string) (string, error)
	GetIssue(owner, name string, number int) (*github.Issue, error)
	CloneRepo(ref string) (*github.Repo, error)
	EditIssueBody(owner, name string, number int, body string) error
	AddIssueComment(owner, name string, number int, body string) (string, error)
}

// defaultGitHubClient is the production GitHubClient backed by the github package.
type defaultGitHubClient struct{}

func (defaultGitHubClient) DefaultBranchHEAD(owner, name string) (string, error) {
	return github.DefaultBranchHEAD(owner, name)
}

func (defaultGitHubClient) GetIssue(owner, name string, number int) (*github.Issue, error) {
	return github.GetIssue(owner, name, number)
}

func (defaultGitHubClient) CloneRepo(ref string) (*github.Repo, error) {
	return github.CloneRepo(ref)
}

func (defaultGitHubClient) EditIssueBody(owner, name string, number int, body string) error {
	return github.EditIssueBody(owner, name, number, body)
}

func (defaultGitHubClient) AddIssueComment(owner, name string, number int, body string) (string, error) {
	return github.AddIssueComment(owner, name, number, body)
}
