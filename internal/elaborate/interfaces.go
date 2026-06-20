package elaborate

import (
	"github.com/planwerk/planwerk-review/internal/github"
	"github.com/planwerk/planwerk-review/internal/patterns"
)

// Context is the input for the Claude elaboration prompt. Patterns are
// injected so the elaboration is grounded in the same review catalog used
// by review/audit/propose, instead of the model inventing repo conventions.
type Context struct {
	Patterns    []patterns.Pattern
	MaxPatterns int
	RepoName    string
	Issue       *github.Issue
	// PriorDraft, ReviewGaps, and ReviewTarget drive the reviewer refine loop.
	// When set, the elaboration prompt revises the prior draft to close the
	// listed gaps and reach the target instead of starting from scratch. All
	// empty on the first pass.
	PriorDraft   string
	ReviewGaps   []string
	ReviewTarget string
	// MetaIssue, SiblingIssues, and ChildIssues place the source issue in its
	// Meta/Sub-Issue neighborhood so the elaboration plans this Sub Issue's
	// slice of a larger effort coherently — honoring the Meta Issue's framing,
	// not duplicating or contradicting sibling Sub Issues, and deferring a
	// shared task's remaining part to the sibling that owns it. MetaIssue is the
	// parent Meta Issue (nil when the source issue is not a Sub Issue);
	// SiblingIssues are the Meta Issue's other Sub Issues; ChildIssues are the
	// source issue's own Sub Issues when it is itself a Meta Issue. All empty
	// when the issue stands alone.
	MetaIssue     *github.Issue
	SiblingIssues []github.Issue
	ChildIssues   []github.Issue
	// Glossary is the target repo's domain vocabulary loaded from its
	// CONTEXT.md / .planwerk/context.md so the elaborated issue uses the repo's
	// own terms. Empty when the repo carries no glossary.
	Glossary string
}

// ReviewResult is the verdict of the optional reviewer pass over an
// elaboration draft. Score rates the draft's executability from 0 to 10; Gaps
// lists the concrete reasons it falls short of a 10; ToReachTen describes what
// a 10/10 plan would look like so the next iteration has a target to optimize
// against.
type ReviewResult struct {
	Score      int      `json:"score"`
	Gaps       []string `json:"gaps"`
	ToReachTen string   `json:"to_reach_ten"`
}

// ClaudeElaborator turns a high-level issue into a detailed engineering
// plan. The elaborate package depends on this interface so tests can inject
// a deterministic fake.
type ClaudeElaborator interface {
	Elaborate(dir string, ctx Context) (*Result, error)
}

// ElaborationReviewer evaluates a rendered elaboration draft for
// executability and returns either approval or a list of gaps to close. It is
// a separate interface so the reviewer pass is opt-in and independently
// fakeable.
type ElaborationReviewer interface {
	ReviewElaboration(dir string, ctx Context, draftBody string) (*ReviewResult, error)
}

// ElaborateFn is the bare-function form of ClaudeElaborator that the CLI
// passes in. It is adapted to the interface via elaborateFnAdapter.
type ElaborateFn func(dir string, ctx Context) (*Result, error)

// ReviewFn is the bare-function form of ElaborationReviewer.
type ReviewFn func(dir string, ctx Context, draftBody string) (*ReviewResult, error)

type elaborateFnAdapter struct {
	fn ElaborateFn
}

func (a elaborateFnAdapter) Elaborate(dir string, ctx Context) (*Result, error) {
	return a.fn(dir, ctx)
}

type reviewFnAdapter struct {
	fn ReviewFn
}

func (a reviewFnAdapter) ReviewElaboration(dir string, ctx Context, draftBody string) (*ReviewResult, error) {
	return a.fn(dir, ctx, draftBody)
}

// GitHubClient wraps the GitHub operations the elaborate pipeline needs:
// resolving the default-branch HEAD for cache keying, fetching the source
// issue, cloning the repo, and (optionally) writing the elaborated body
// back to the issue.
type GitHubClient interface {
	DefaultBranchHEAD(owner, name string) (string, error)
	GetIssue(owner, name string, number int) (*github.Issue, error)
	GetIssueRelations(owner, name string, number int) (*github.IssueRelations, error)
	CloneRepo(ref string) (*github.Repo, error)
	CloneRepoLocal(ref string, opts github.LocalOptions) (*github.Repo, error)
	EditIssueBody(owner, name string, number int, body string) error
	AddIssueComment(owner, name string, number int, body string) (string, error)
}

// defaultGitHubClient is the production GitHubClient backed by the github package.
type defaultGitHubClient struct{}

func (defaultGitHubClient) DefaultBranchHEAD(owner, name string) (string, error) {
	return github.DefaultBranchHEAD(owner, name)
}

func (defaultGitHubClient) GetIssue(owner, name string, number int) (*github.Issue, error) {
	return github.GetIssue(owner, name, number)
}

func (defaultGitHubClient) GetIssueRelations(owner, name string, number int) (*github.IssueRelations, error) {
	return github.GetIssueRelations(owner, name, number)
}

func (defaultGitHubClient) CloneRepo(ref string) (*github.Repo, error) {
	return github.CloneRepo(ref)
}

func (defaultGitHubClient) CloneRepoLocal(ref string, opts github.LocalOptions) (*github.Repo, error) {
	return github.UseLocalRepo(ref, opts)
}

func (defaultGitHubClient) EditIssueBody(owner, name string, number int, body string) error {
	return github.EditIssueBody(owner, name, number, body)
}

func (defaultGitHubClient) AddIssueComment(owner, name string, number int, body string) (string, error) {
	return github.AddIssueComment(owner, name, number, body)
}
