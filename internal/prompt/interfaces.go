package prompt

import "github.com/planwerk/planwerk-agent/internal/github"

// GitHubClient wraps the GitHub operations the prompt pipeline needs: fetching
// the source issue, and its comments when the body continues in them. Tests
// inject a fake to avoid touching gh.
type GitHubClient interface {
	GetIssue(owner, name string, number int) (*github.Issue, error)
	ListIssueComments(owner, name string, number int) ([]github.IssueComment, error)
}

// The production client satisfies the interface structurally; a drift in
// either fails the build here rather than at the call site.
var _ GitHubClient = github.Client{}
