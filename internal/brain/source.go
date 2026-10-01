package brain

import "github.com/planwerk/planwerk-agent/internal/github"

// Listing is the history of a repository as BuildUnits groups it: the commits
// of the default branch, oldest first, every merged pull request, and every
// closed issue.
type Listing struct {
	History []github.HistoryCommit
	PRs     []github.MergedPR
	Issues  []github.ClosedIssue
}
