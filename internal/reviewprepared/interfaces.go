package reviewprepared

import (
	"github.com/planwerk/planwerk-agent/internal/github"
	"github.com/planwerk/planwerk-agent/internal/patterns"
)

// AnalysisContext is everything Claude needs to review one batch of prepared
// features. Built by the runner after loading features and patterns.
type AnalysisContext struct {
	Features    []PreparedFeature
	Patterns    []patterns.Pattern
	MaxPatterns int
	RepoName    string
	// IncludeImproved tells Claude to emit a full rewritten feature JSON
	// per file. Toggled on by the runner only when --create-pr is set,
	// to avoid spending tokens on a payload nobody is going to use.
	IncludeImproved bool
}

// ClaudeReviewer reviews a batch of prepared feature specs and returns
// findings (always) and an improved JSON per feature (when requested).
type ClaudeReviewer interface {
	ReviewPrepared(dir string, ctx AnalysisContext) (*Result, error)
}

// AnalyzeFn is the bare-function form of ClaudeReviewer that the CLI passes
// in. Adapted to the interface via analyzeFnAdapter.
type AnalyzeFn func(dir string, ctx AnalysisContext) (*Result, error)

type analyzeFnAdapter struct {
	fn AnalyzeFn
}

func (a analyzeFnAdapter) ReviewPrepared(dir string, ctx AnalysisContext) (*Result, error) {
	return a.fn(dir, ctx)
}

// GitHubClient wraps the GitHub operations the review-prepared pipeline
// needs. The PR-creation methods are split out so a read-only run does not
// pull the push/PR code paths into its dependency graph.
type GitHubClient interface {
	CloneRepo(ref string) (*github.Repo, error)
	UseLocalRepo(ref string, opts github.LocalOptions) (*github.Repo, error)
	DefaultBranchHEAD(owner, name string) (string, error)
	OpenImprovementPR(repo *github.Repo, opts github.ImprovementPROptions) (string, error)
}



// The production client satisfies the interface structurally; a drift in
// either fails the build here rather than at the call site.
var _ GitHubClient = github.Client{}
