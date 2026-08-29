package prompt

import "github.com/planwerk/planwerk-agent/internal/github"

// GitHubClient wraps the single GitHub operation the prompt pipeline needs:
// fetching the source issue. Tests inject a fake to avoid touching gh.
type GitHubClient interface {
	GetIssue(owner, name string, number int) (*github.Issue, error)
}

// The production client satisfies the interface structurally; a drift in
// either fails the build here rather than at the call site.
var _ GitHubClient = github.Client{}
