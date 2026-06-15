package meta

import "github.com/planwerk/planwerk-review/internal/github"

// Context is the input for the Claude meta-split prompt: the Meta Issue to
// decompose. The meta command reads the issue and decides the breakdown on its
// own — it neither clones the repo nor loads review patterns, so the context
// carries nothing but the issue.
type Context struct {
	Issue *github.Issue
}

// ClaudeMetaSplitter carves a Meta Issue into the fewest sensible draft-depth
// Sub Issues. The meta package depends on this interface so tests can inject a
// deterministic fake, mirroring the draft and elaborate packages.
type ClaudeMetaSplitter interface {
	Split(ctx Context) (*Result, error)
}

// MetaFn is the bare-function form of ClaudeMetaSplitter that the CLI passes
// in. It is adapted to the interface via metaFnAdapter.
type MetaFn func(ctx Context) (*Result, error)

type metaFnAdapter struct {
	fn MetaFn
}

func (a metaFnAdapter) Split(ctx Context) (*Result, error) {
	return a.fn(ctx)
}

// GitHubClient wraps the GitHub operations the meta pipeline needs: fetching
// the Meta Issue, creating each Sub Issue, linking it to the Meta Issue via the
// native sub-issue relationship, and back-filling the Meta Issue body with the
// fresh references. The meta package depends on this interface so tests can
// inject a fake without touching gh.
type GitHubClient interface {
	GetIssue(owner, name string, number int) (*github.Issue, error)
	CreateIssueWithLabels(owner, name, title, body string, labels []string) (string, error)
	AddSubIssue(owner, name string, parentNumber, childNumber int) error
	EditIssueBody(owner, name string, number int, body string) error
}

// defaultGitHubClient is the production GitHubClient backed by the github
// package.
type defaultGitHubClient struct{}

func (defaultGitHubClient) GetIssue(owner, name string, number int) (*github.Issue, error) {
	return github.GetIssue(owner, name, number)
}

func (defaultGitHubClient) CreateIssueWithLabels(owner, name, title, body string, labels []string) (string, error) {
	return github.CreateIssueWithLabels(owner, name, title, body, labels)
}

func (defaultGitHubClient) AddSubIssue(owner, name string, parentNumber, childNumber int) error {
	return github.AddSubIssue(owner, name, parentNumber, childNumber)
}

func (defaultGitHubClient) EditIssueBody(owner, name string, number int, body string) error {
	return github.EditIssueBody(owner, name, number, body)
}
