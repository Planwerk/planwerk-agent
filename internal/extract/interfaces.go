package extract

import "github.com/planwerk/planwerk-review/internal/github"

// GitHubClient wraps the GitHub operations the extract pipeline needs: cloning
// the target repo (for the default PR mode), using the current working tree
// (for --local), and opening the improvement PR. It is an interface so tests can
// inject a fake that captures the PR options without touching git or gh.
// Mirrors reviewprepared.GitHubClient.
type GitHubClient interface {
	CloneRepo(ref string) (*github.Repo, error)
	CloneRepoLocal(ref string, opts github.LocalOptions) (*github.Repo, error)
	OpenImprovementPR(repo *github.Repo, opts github.ImprovementPROptions) (string, error)
}

// defaultGitHubClient is the production GitHubClient backed by the github
// package.
type defaultGitHubClient struct{}

func (defaultGitHubClient) CloneRepo(ref string) (*github.Repo, error) {
	return github.CloneRepo(ref)
}

func (defaultGitHubClient) CloneRepoLocal(ref string, opts github.LocalOptions) (*github.Repo, error) {
	return github.UseLocalRepo(ref, opts)
}

func (defaultGitHubClient) OpenImprovementPR(repo *github.Repo, opts github.ImprovementPROptions) (string, error) {
	return github.OpenImprovementPR(repo, opts)
}
