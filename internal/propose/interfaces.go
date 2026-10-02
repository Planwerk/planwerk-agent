package propose

import (
	"github.com/planwerk/planwerk-agent/internal/github"
	"github.com/planwerk/planwerk-agent/internal/patterns"
	"github.com/planwerk/planwerk-agent/internal/search"
)

// AnalysisContext carries the inputs the analysis prompt grounds itself in.
// Patterns let the proposer reference the project's pattern catalog so
// suggestions stay specific instead of reverting to generic software advice.
type AnalysisContext struct {
	Patterns []patterns.Pattern
	// Catalog is the on-disk form of Patterns the session reads (written by
	// patterns.Materialize); a zero Catalog puts the bodies in the prompt.
	Catalog     patterns.Catalog
	MaxPatterns int
	RepoName    string // "owner/repo" for context in the prompt
	// OutOfScope carries rejected ideas loaded from the target repo's
	// .planwerk/out-of-scope/ knowledge base so the analysis stops
	// re-suggesting them.
	OutOfScope []OutOfScopeEntry
	// Glossary is the target repo's domain vocabulary loaded from its
	// CONTEXT.md / .planwerk/context.md so proposals use the repo's own terms.
	// Populated only by propose; the other commands sharing AnalysisContext
	// leave it empty (like OutOfScope), so their prompts are unaffected.
	Glossary string
	// Memory is the target repo's project memory from its GitHub Wiki: the
	// pages, listed in the analysis prompt, and the directory a session reads
	// them from. Populated only by propose; the other commands sharing
	// AnalysisContext leave it zero, so their prompts are unaffected.
	Memory patterns.MemoryCatalog
	// Brain is the search of the local mirror the analysis session may run.
	// The zero Surface means no search. Populated only by propose.
	Brain search.Surface
}

// ClaudeAnalyzer performs the Claude-backed codebase analysis that produces
// feature proposals. The propose package depends on this interface rather
// than the concrete claude package so tests can inject fakes.
type ClaudeAnalyzer interface {
	Analyze(dir string, ctx AnalysisContext) (*ProposalResult, error)
}

// GitHubClient wraps the GitHub operations the propose pipeline needs:
// cloning the repository, resolving the default-branch HEAD for cache keying,
// and listing existing issues for duplicate detection. Tests inject mock
// clients to avoid touching the real git or gh CLI.
type GitHubClient interface {
	CloneRepo(ref string) (*github.Repo, error)
	UseLocalRepo(ref string, opts github.LocalOptions) (*github.Repo, error)
	DefaultBranchHEAD(owner, name string) (string, error)
	ListAllIssues(owner, name string) ([]github.ExistingIssue, error)
}

// AnalyzeFn is the bare-function form of ClaudeAnalyzer that existing callers
// (the CLI) pass in. It is adapted to the interface via analyzeFnAdapter.
type AnalyzeFn func(dir string, ctx AnalysisContext) (*ProposalResult, error)

type analyzeFnAdapter struct {
	fn AnalyzeFn
}

func (a analyzeFnAdapter) Analyze(dir string, ctx AnalysisContext) (*ProposalResult, error) {
	return a.fn(dir, ctx)
}

// The production client satisfies the interface structurally; a drift in
// either fails the build here rather than at the call site.
var _ GitHubClient = github.Client{}
