package address

import (
	"github.com/planwerk/planwerk-agent/internal/github"
	"github.com/planwerk/planwerk-agent/internal/patterns"
	"github.com/planwerk/planwerk-agent/internal/report"
	"github.com/planwerk/planwerk-agent/internal/skills"
)

// Context is the input for the Claude address prompt for one unit of work. In
// per-thread mode (the default) Threads holds the single thread to address; in
// aggregate mode it holds every selected thread, to be folded into one commit.
//
// Patterns + MaxPatterns are injected so the change stays grounded in the same
// review/audit pattern catalog the rest of the tool uses, plus any
// project-specific patterns under .planwerk/review_patterns/ in the target repo.
type Context struct {
	RepoFullName string
	PRNumber     int
	PRTitle      string
	HeadBranch   string
	BaseBranch   string
	Threads      []github.ReviewThread
	// OneCommitPerThread mirrors the flag: when true the session is addressing
	// a single thread and commits one focused follow-up; when false it folds
	// every thread in Threads into one aggregate commit.
	OneCommitPerThread bool
	Patterns           []patterns.Pattern
	MaxPatterns        int
	// Skills lists the Agent Skills the target repo ships under .claude/skills/
	// (loaded by skills.Load from the checkout), rendered into the address prompt
	// so the session uses a matching project skill when resolving a thread instead
	// of improvising. Empty when the repo ships none.
	Skills []skills.Skill
	// StyleGuidePath is the repo-relative path of the documentation style
	// guide the target repo commits (found by styleguide.Find from the
	// checkout, e.g. "STYLE_GUIDE.md"). Rendered into the address prompt so
	// any documentation prose a resolved thread touches follows the repo's
	// own guide. Empty when the repo ships none.
	StyleGuidePath string
	// Local marks a --local run: the session operates on the user's own
	// checkout. The orchestrator owns the push in both modes.
	Local bool
}

// BareContext is the input for the self-contained ("bare") address prompt
// rendered by --print-bare-prompt. The orchestrator clones the target repo at
// prompt-build time so it can run technology detection and inline the relevant
// pattern catalog — the resulting prompt is portable and pasted into a manual
// Claude session that operates on its own checkout. Mirrors fix.BareContext.
type BareContext struct {
	RepoFullName     string
	PRNumber         int
	TechTags         []string
	PatternCatalog   []patterns.CatalogReference
	BundledURLBase   string // for the prompt to mention the canonical source
	HasRepoLocalRefs bool   // signals that LocalPath entries exist
}

// AddressFn is the bare-function shape the CLI passes in to wire Claude into
// the orchestrator. It returns the structured per-thread result the session
// produced. Wired as claude.Address so the import direction stays claude ->
// address.
type AddressFn func(dir string, ctx Context) (*report.AddressResult, error)

// PromptBuildFn renders the address prompt without invoking Claude. Wired in by
// the CLI so the address subcommand can support --print-prompt mode.
type PromptBuildFn func(ctx Context) string

// BarePromptBuildFn renders a self-contained address prompt for
// --print-bare-prompt mode. The context is populated by the orchestrator from a
// fresh checkout so the prompt can embed the filtered pattern catalog inline.
type BarePromptBuildFn func(ctx BareContext) string

// ClaudeAddresser is the injected dependency the orchestrator drives to address
// one unit of work. The production implementation is claude.Address; tests
// substitute a fake that returns scripted results without invoking Claude.
type ClaudeAddresser interface {
	Address(dir string, ctx Context) (*report.AddressResult, error)
}

type addressFnAdapter struct {
	fn AddressFn
}

func (a addressFnAdapter) Address(dir string, ctx Context) (*report.AddressResult, error) {
	return a.fn(dir, ctx)
}

// GitHubClient is the subset of github operations the address loop needs. Each
// method maps to a single git or gh invocation. Tests substitute a fake.
type GitHubClient interface {
	FetchAndCheckout(ref string) (*github.PR, error)
	FetchAndCheckoutLocal(ref string, opts github.LocalOptions) (*github.PR, error)
	FetchReviewThreads(owner, repo string, number int) ([]github.ReviewThread, error)
	// PushHead publishes the follow-up commits to the PR head branch. The
	// orchestrator owns the push so the Claude session only commits.
	PushHead(dir, branch string) error
	// AddReviewThreadReply posts a per-thread reply summarizing the change.
	AddReviewThreadReply(threadID, body string) (string, error)
	// ResolveReviewThread marks an addressed thread resolved (only under --resolve).
	ResolveReviewThread(threadID string) error
	// AddPRComment posts the aggregate address report as a fresh PR comment.
	AddPRComment(owner, repo string, number int, body string) (string, error)
}

// defaultGitHubClient is the production GitHubClient backed by the github
// package. Mirrors the fix/rebase adapter shape.
type defaultGitHubClient struct{}

func (defaultGitHubClient) FetchAndCheckout(ref string) (*github.PR, error) {
	return github.FetchAndCheckout(ref)
}

func (defaultGitHubClient) FetchAndCheckoutLocal(ref string, opts github.LocalOptions) (*github.PR, error) {
	return github.OpenLocalPR(ref, opts)
}

func (defaultGitHubClient) FetchReviewThreads(owner, repo string, number int) ([]github.ReviewThread, error) {
	return github.FetchReviewThreads(owner, repo, number)
}

func (defaultGitHubClient) PushHead(dir, branch string) error {
	return github.PushHead(dir, branch)
}

func (defaultGitHubClient) AddReviewThreadReply(threadID, body string) (string, error) {
	return github.AddReviewThreadReply(threadID, body)
}

func (defaultGitHubClient) ResolveReviewThread(threadID string) error {
	return github.ResolveReviewThread(threadID)
}

func (defaultGitHubClient) AddPRComment(owner, repo string, number int, body string) (string, error) {
	return github.AddPRComment(owner, repo, number, body)
}
