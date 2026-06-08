package audit

import (
	"github.com/planwerk/planwerk-review/internal/github"
	"github.com/planwerk/planwerk-review/internal/report"
)

// ClaudeAuditor performs the Claude-backed codebase audit for a cloned repo.
// The audit package depends on this interface rather than the concrete claude
// package so tests can inject fakes and alternative backends can be swapped
// in without touching the audit pipeline.
type ClaudeAuditor interface {
	Audit(dir string, ctx AuditContext) (*report.ReviewResult, error)
}

// GitHubClient wraps the GitHub operations the audit pipeline needs: cloning
// the repository, resolving the default-branch HEAD for cache keying, and
// listing existing issues for duplicate detection. Tests inject mock clients
// to avoid touching the real git or gh CLI.
type GitHubClient interface {
	CloneRepo(ref string) (*github.Repo, error)
	CloneRepoLocal(ref string, opts github.LocalOptions) (*github.Repo, error)
	DefaultBranchHEAD(owner, name string) (string, error)
	ListExistingIssues(owner, name string) ([]github.ExistingIssue, error)
}

// auditFnAdapter adapts an AuditFn to the ClaudeAuditor interface so callers
// passing a bare function (the CLI does) keep working.
type auditFnAdapter struct {
	fn AuditFn
}

func (a auditFnAdapter) Audit(dir string, ctx AuditContext) (*report.ReviewResult, error) {
	return a.fn(dir, ctx)
}

// defaultGitHubClient is the production GitHubClient backed by the github package.
type defaultGitHubClient struct{}

func (defaultGitHubClient) CloneRepo(ref string) (*github.Repo, error) {
	return github.CloneRepo(ref)
}

func (defaultGitHubClient) CloneRepoLocal(ref string, opts github.LocalOptions) (*github.Repo, error) {
	return github.UseLocalRepo(ref, opts)
}

func (defaultGitHubClient) DefaultBranchHEAD(owner, name string) (string, error) {
	return github.DefaultBranchHEAD(owner, name)
}

func (defaultGitHubClient) ListExistingIssues(owner, name string) ([]github.ExistingIssue, error) {
	return github.ListAllIssues(owner, name)
}
