package extract

import "github.com/planwerk/planwerk-agent/internal/github"

// GitHubClient wraps the GitHub operations the extract pipeline needs: cloning
// the target repo (for the default PR mode), using the current working tree
// (for --local), and opening the improvement PR. It is an interface so tests can
// inject a fake that captures the PR options without touching git or gh.
// Mirrors reviewprepared.GitHubClient.
type GitHubClient interface {
	CloneRepo(ref string) (*github.Repo, error)
	UseLocalRepo(ref string, opts github.LocalOptions) (*github.Repo, error)
	OpenImprovementPR(repo *github.Repo, opts github.ImprovementPROptions) (string, error)
}

// The production client satisfies the interface structurally; a drift in
// either fails the build here rather than at the call site.
var _ GitHubClient = github.Client{}
