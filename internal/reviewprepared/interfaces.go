package reviewprepared

import (
	"github.com/planwerk/planwerk-review/internal/github"
	"github.com/planwerk/planwerk-review/internal/patterns"
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
	DefaultBranchHEAD(owner, name string) (string, error)
	OpenImprovementPR(repo *github.Repo, opts PROptions) (string, error)
}

// PROptions configures the PR side-effect.
type PROptions struct {
	Branch  string
	Base    string
	Title   string
	Body    string
	Files   []ImprovedFile
	Commit  string // commit message subject + body
}

// ImprovedFile is one file the runner is asking the GitHub client to write,
// stage, commit, and push.
type ImprovedFile struct {
	// RelativePath is repo-relative (e.g. ".planwerk/features/PX-0028-...json").
	RelativePath string
	// Content is the new file contents — UTF-8, byte-for-byte, no shell escaping.
	Content []byte
}

type defaultGitHubClient struct{}

func (defaultGitHubClient) CloneRepo(ref string) (*github.Repo, error) {
	return github.CloneRepo(ref)
}

func (defaultGitHubClient) DefaultBranchHEAD(owner, name string) (string, error) {
	return github.DefaultBranchHEAD(owner, name)
}

func (defaultGitHubClient) OpenImprovementPR(repo *github.Repo, opts PROptions) (string, error) {
	return github.OpenImprovementPR(repo, github.ImprovementPROptions{
		Branch:  opts.Branch,
		Base:    opts.Base,
		Title:   opts.Title,
		Body:    opts.Body,
		Commit:  opts.Commit,
		Files:   convertFiles(opts.Files),
	})
}

func convertFiles(files []ImprovedFile) []github.ImprovementFile {
	out := make([]github.ImprovementFile, 0, len(files))
	for _, f := range files {
		out = append(out, github.ImprovementFile{
			RelativePath: f.RelativePath,
			Content:      f.Content,
		})
	}
	return out
}
