package prompt

import "github.com/planwerk/planwerk-review/internal/github"

// GitHubClient wraps the single GitHub operation the prompt pipeline needs:
// fetching the source issue. Tests inject a fake to avoid touching gh.
type GitHubClient interface {
	GetIssue(owner, name string, number int) (*github.Issue, error)
}

// defaultGitHubClient is the production GitHubClient backed by the github
// package.
type defaultGitHubClient struct{}

func (defaultGitHubClient) GetIssue(owner, name string, number int) (*github.Issue, error) {
	return github.GetIssue(owner, name, number)
}
