package review

import (
	"github.com/planwerk/planwerk-review/internal/claude"
	"github.com/planwerk/planwerk-review/internal/github"
	"github.com/planwerk/planwerk-review/internal/planwerk"
	"github.com/planwerk/planwerk-review/internal/report"
)

// ClaudeRunner performs AI-powered code review on a checkout directory.
// The review package depends on this interface rather than the concrete
// claude package so tests can inject mock implementations and alternative
// backends (e.g. the Claude API) can be swapped in without touching the
// review pipeline.
type ClaudeRunner interface {
	Review(dir string, ctx claude.ReviewContext) (*report.ReviewResult, error)
	AdversarialReview(dir, baseBranch string) (*report.ReviewResult, error)
	CoverageMap(dir, baseBranch string) (*report.CoverageResult, error)
	FeatureCompliance(dir, baseBranch string, feature *planwerk.Feature) (*report.ReviewResult, error)
	SpecialistReview(dir, baseBranch, key, focus string) (*report.ReviewResult, error)
}

// GitHubClient wraps the GitHub operations the review pipeline needs:
// fetching a PR checkout, posting and updating PR comments, submitting
// inline reviews, and fetching the PR diff. Tests inject mock clients
// to avoid touching the real GitHub API or gh CLI.
type GitHubClient interface {
	FetchAndCheckout(ref string) (*github.PR, error)
	FetchAndCheckoutLocal(ref string, opts github.LocalOptions) (*github.PR, error)
	PostPRComment(owner, repo string, number int, body string) (string, error)
	SubmitPRReview(owner, repo string, number int, commitSHA, body string, comments []github.ReviewComment) (string, error)
	FetchDiff(owner, repo string, number int) (string, error)
	FetchReviewComment(owner, repo string, number int) (string, bool, error)
}

// defaultClaudeRunner is the production ClaudeRunner backed by the claude
// package. It delegates to an injected *claude.Client so each runner carries
// its own Claude Code configuration instead of sharing package-level state.
type defaultClaudeRunner struct {
	client *claude.Client
}

func (r defaultClaudeRunner) Review(dir string, ctx claude.ReviewContext) (*report.ReviewResult, error) {
	return r.client.Review(dir, ctx)
}

func (r defaultClaudeRunner) AdversarialReview(dir, baseBranch string) (*report.ReviewResult, error) {
	return r.client.AdversarialReview(dir, baseBranch)
}

func (r defaultClaudeRunner) CoverageMap(dir, baseBranch string) (*report.CoverageResult, error) {
	return r.client.CoverageMap(dir, baseBranch)
}

func (r defaultClaudeRunner) SpecialistReview(dir, baseBranch, key, focus string) (*report.ReviewResult, error) {
	return r.client.SpecialistReview(dir, baseBranch, key, focus)
}

func (r defaultClaudeRunner) FeatureCompliance(dir, baseBranch string, feature *planwerk.Feature) (*report.ReviewResult, error) {
	return r.client.FeatureCompliance(dir, baseBranch, feature)
}

// defaultGitHubClient is the production GitHubClient backed by the github package.
type defaultGitHubClient struct{}

func (defaultGitHubClient) FetchAndCheckout(ref string) (*github.PR, error) {
	return github.FetchAndCheckout(ref)
}

func (defaultGitHubClient) FetchAndCheckoutLocal(ref string, opts github.LocalOptions) (*github.PR, error) {
	return github.OpenLocalPR(ref, opts)
}

func (defaultGitHubClient) PostPRComment(owner, repo string, number int, body string) (string, error) {
	return github.PostPRComment(owner, repo, number, body)
}

func (defaultGitHubClient) SubmitPRReview(owner, repo string, number int, commitSHA, body string, comments []github.ReviewComment) (string, error) {
	return github.SubmitPRReview(owner, repo, number, commitSHA, body, comments)
}

func (defaultGitHubClient) FetchDiff(owner, repo string, number int) (string, error) {
	return github.FetchDiff(owner, repo, number)
}

func (defaultGitHubClient) FetchReviewComment(owner, repo string, number int) (string, bool, error) {
	return github.FetchReviewComment(owner, repo, number)
}
