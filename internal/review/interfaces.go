package review

import (
	"github.com/planwerk/planwerk-agent/internal/claude"
	"github.com/planwerk/planwerk-agent/internal/github"
	"github.com/planwerk/planwerk-agent/internal/hygiene"
	"github.com/planwerk/planwerk-agent/internal/planwerk"
	"github.com/planwerk/planwerk-agent/internal/report"
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
	// DedupFindings groups findings that describe the same underlying issue,
	// returning index groups into the passed slice. It backstops the fuzzy
	// merge matcher for findings with no file to anchor on.
	DedupFindings(findings []report.Finding) ([][]int, error)
	// VerifyFindingClaims re-checks each finding's claim against the checkout at
	// dir, returning one verdict per finding it judged (keyed by index). It reads
	// the cited code, so it runs on the main tier.
	VerifyFindingClaims(dir string, findings []report.Finding) ([]hygiene.ClaimVerdict, error)
	// UsageTotals reports the per-Run Claude token usage and estimated cost
	// accumulated across this runner's calls, for embedding in the data block.
	UsageTotals() report.Usage
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

func (r defaultClaudeRunner) DedupFindings(findings []report.Finding) ([][]int, error) {
	return r.client.DedupFindings(findings)
}

func (r defaultClaudeRunner) VerifyFindingClaims(dir string, findings []report.Finding) ([]hygiene.ClaimVerdict, error) {
	return r.client.VerifyFindingClaims(dir, findings)
}

func (r defaultClaudeRunner) UsageTotals() report.Usage {
	return r.client.UsageTotals()
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
