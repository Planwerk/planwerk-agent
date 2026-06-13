package implement

import (
	"github.com/planwerk/planwerk-review/internal/github"
	"github.com/planwerk/planwerk-review/internal/patterns"
	"github.com/planwerk/planwerk-review/internal/report"
)

// Context is the input for the Claude implement prompt. It carries the
// elaborated issue plus enough repository identity to let the prompt write
// commit messages, branch names, and the eventual draft PR.
//
// Patterns + MaxPatterns are injected so the implementation is grounded in
// the same review/audit/elaborate pattern catalog the rest of the tool uses,
// honoring any project-specific patterns under .planwerk/review_patterns/
// in the target repo.
type Context struct {
	RepoFullName string
	IssueNumber  int
	IssueTitle   string
	IssueBody    string
	IssueURL     string
	IssueState   string
	Patterns     []patterns.Pattern
	MaxPatterns  int
	// Plan is the implementation plan the preceding read-only planning
	// session produced. When non-empty it is embedded verbatim into the
	// implement prompt; empty means the implement session plans for
	// itself (--no-plan, or no planner wired).
	Plan string
}

// ImplementFn is the bare-function shape the CLI passes in to wire Claude
// into the orchestrator. Returns a short human-readable summary of what
// Claude did (already trimmed) — the orchestrator logs/prints this verbatim.
type ImplementFn func(dir string, ctx Context) (string, error)

// PromptBuildFn renders the implement prompt for the given issue context
// without invoking Claude. Wired in by the CLI so the implement subcommand
// can support --print-prompt mode while keeping the import direction
// claude -> implement.
type PromptBuildFn func(ctx Context) string

// BareContext is the input for the self-contained ("bare") implement
// prompt rendered by --print-bare-prompt. The orchestrator clones the
// target repo at prompt-build time so it can run technology detection and
// prepare a reference catalog of the relevant review patterns — the
// resulting prompt is then portable: it is pasted into a manual Claude
// session that operates on its own checkout, with no further coordination
// required.
//
// The pattern catalog is shipped as a list of remote URLs (for patterns
// from the bundled planwerk-review catalog) plus relative checkout paths
// (for project-specific patterns under .planwerk/review_patterns/). The
// pasted-into Claude session fetches each URL itself, so the prompt stays
// short and the patterns Claude sees are always the same as those
// displayed on github.com/planwerk/planwerk-review.
type BareContext struct {
	RepoFullName     string
	IssueNumber      int
	TechTags         []string
	PatternCatalog   []patterns.CatalogReference
	BundledURLBase   string
	HasRepoLocalRefs bool
}

// BarePromptBuildFn renders a self-contained implement prompt — no issue
// body, no repository walk. Wired in by the CLI for --print-bare-prompt
// mode while keeping the import direction claude -> implement. The context
// is populated by the orchestrator from a fresh clone of the target repo so
// the resulting prompt can embed the filtered pattern catalog inline.
type BarePromptBuildFn func(ctx BareContext) string

// ClaudeImplementer is the injected dependency the orchestrator uses to
// run a single implementation session. The production implementation is
// claude.Implement; tests substitute a fake that returns scripted summaries
// without invoking the real Claude CLI.
type ClaudeImplementer interface {
	Implement(dir string, ctx Context) (string, error)
}

type implementFnAdapter struct {
	fn ImplementFn
}

func (a implementFnAdapter) Implement(dir string, ctx Context) (string, error) {
	return a.fn(dir, ctx)
}

// PlanFn is the bare-function shape of the planning session the CLI wires
// in. It runs read-only inside the checkout and returns the implementation
// plan text (already trimmed) that the implement session receives via
// Context.Plan.
type PlanFn func(dir string, ctx Context) (string, error)

// ClaudePlanner is the injected dependency for the planning phase that
// precedes the implement session. The production implementation is
// claude.Plan (running on the dedicated planning model); tests substitute
// a fake that returns scripted plans without invoking the real Claude CLI.
type ClaudePlanner interface {
	Plan(dir string, ctx Context) (string, error)
}

type planFnAdapter struct {
	fn PlanFn
}

func (a planFnAdapter) Plan(dir string, ctx Context) (string, error) {
	return a.fn(dir, ctx)
}

// ImplementationVerifier independently checks a produced change set against
// the issue's Acceptance Criteria, deliberately ignoring the implementation's
// own report. Optional: wired only when --verify is set.
type ImplementationVerifier interface {
	VerifyImplementation(dir, issueTitle, issueBody string) (*report.ReviewResult, error)
}

// VerifyFn is the bare-function form of ImplementationVerifier.
type VerifyFn func(dir, issueTitle, issueBody string) (*report.ReviewResult, error)

type verifyFnAdapter struct {
	fn VerifyFn
}

func (a verifyFnAdapter) VerifyImplementation(dir, issueTitle, issueBody string) (*report.ReviewResult, error) {
	return a.fn(dir, issueTitle, issueBody)
}

// GitHubClient is the subset of github operations the implement command
// needs: fetching the source issue, cloning the repository so Claude has a
// working tree to operate on, and posting the finished plan back onto the
// issue as a comment. Each method maps to a single gh CLI invocation under
// the hood.
type GitHubClient interface {
	GetIssue(owner, name string, number int) (*github.Issue, error)
	CloneRepo(ref string) (*github.Repo, error)
	CloneRepoLocal(ref string, opts github.LocalOptions) (*github.Repo, error)
	AddIssueComment(owner, name string, number int, body string) (string, error)
}

// defaultGitHubClient is the production GitHubClient backed by the github
// package. Mirrors the elaborate package's adapter shape.
type defaultGitHubClient struct{}

func (defaultGitHubClient) GetIssue(owner, name string, number int) (*github.Issue, error) {
	return github.GetIssue(owner, name, number)
}

func (defaultGitHubClient) CloneRepo(ref string) (*github.Repo, error) {
	return github.CloneRepo(ref)
}

func (defaultGitHubClient) CloneRepoLocal(ref string, opts github.LocalOptions) (*github.Repo, error) {
	return github.UseLocalRepo(ref, opts)
}

func (defaultGitHubClient) AddIssueComment(owner, name string, number int, body string) (string, error) {
	return github.AddIssueComment(owner, name, number, body)
}
