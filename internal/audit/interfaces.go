package audit

import (
	"github.com/planwerk/planwerk-agent/internal/github"
	"github.com/planwerk/planwerk-agent/internal/report"
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
	UseLocalRepo(ref string, opts github.LocalOptions) (*github.Repo, error)
	DefaultBranchHEAD(owner, name string) (string, error)
	ListAllIssues(owner, name string) ([]github.ExistingIssue, error)
}

// auditFnAdapter adapts an AuditFn to the ClaudeAuditor interface so callers
// passing a bare function (the CLI does) keep working.
type auditFnAdapter struct {
	fn AuditFn
}

func (a auditFnAdapter) Audit(dir string, ctx AuditContext) (*report.ReviewResult, error) {
	return a.fn(dir, ctx)
}

// The production client satisfies the interface structurally; a drift in
// either fails the build here rather than at the call site.
var _ GitHubClient = github.Client{}
