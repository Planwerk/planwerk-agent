package fix

import (
	"github.com/planwerk/planwerk-review/internal/github"
	"github.com/planwerk/planwerk-review/internal/patterns"
)

// FailedCheck is a flattened, prompt-friendly view of a single failing check
// run plus its truncated logs. The orchestrator builds these from the
// GitHub Checks API response and hands them to Claude.
type FailedCheck struct {
	Name          string
	Conclusion    string
	HTMLURL       string
	OutputTitle   string
	OutputSummary string
	Logs          string
	WorkflowRunID int64
}

// Context is the input for the Claude fix prompt for a single iteration.
//
// Patterns + MaxPatterns are injected so the fix is grounded in the same
// review/audit/elaborate pattern catalog used by the rest of the tool, and
// honors any project-specific patterns under .planwerk/review_patterns/ in
// the target repo.
type Context struct {
	RepoFullName  string
	PRNumber      int
	PRTitle       string
	HeadBranch    string
	HeadSHA       string
	Iteration     int
	MaxIterations int
	FailedChecks  []FailedCheck
	Patterns      []patterns.Pattern
	MaxPatterns   int

	// Local marks a --local run: the fix operates on the user's own checkout
	// (PullOnBranch each iteration) instead of a throw-away temp-dir clone. It
	// controls only WHERE the fix happens, not HOW it is committed — the commit
	// strategy is selected by Fixup.
	Local bool
	// Fixup selects the commit strategy. When true (the default) each change is
	// folded into the branch commit it belongs to (git commit --fixup + git
	// rebase --autosquash) and the rewritten branch is published with
	// git push --force-with-lease. When false (--no-fixup) the fix is appended
	// as a single on-top follow-up commit and pushed without rewriting history.
	Fixup bool
	// BaseBranch is the PR's base (e.g. "main"). In Fixup mode it bounds the
	// autosquash rebase to the branch's own commits (origin/<base>..HEAD) so
	// the fold never rewrites history that already exists on the base branch.
	BaseBranch string
}

// FixFn is the bare-function shape the CLI passes in to wire Claude into the
// orchestrator. Returns a short human-readable summary of what Claude did
// (already trimmed) — the orchestrator logs/prints this verbatim.
type FixFn func(dir string, ctx Context) (string, error)

// PromptBuildFn renders the fix prompt for a single iteration without invoking
// Claude. Wired in by the CLI so the fix subcommand can support --print-prompt
// mode while keeping the import direction claude -> fix.
type PromptBuildFn func(ctx Context) string

// BareContext is the input for the self-contained ("bare") fix prompt
// rendered by --print-bare-prompt. The orchestrator clones the target repo
// at prompt-build time so it can run technology detection and prepare a
// reference catalog of the relevant review patterns — the resulting prompt
// is then portable: it is pasted into a manual Claude session that
// operates on its own checkout, with no further coordination required.
//
// The pattern catalog is shipped as a list of remote URLs (for patterns
// from the bundled planwerk-review catalog) plus relative checkout paths
// (for project-specific patterns under .planwerk/review_patterns/). The
// pasted-into Claude session fetches each URL itself, so the prompt stays
// short and the patterns Claude sees are always the same as those
// displayed on github.com/planwerk/planwerk-review.
type BareContext struct {
	RepoFullName     string
	PRNumber         int
	TechTags         []string
	PatternCatalog   []patterns.CatalogReference
	BundledURLBase   string // for the prompt to mention canonical source
	HasRepoLocalRefs bool   // signals that LocalPath entries exist
	// Fixup mirrors Context.Fixup for the self-contained bare prompt: when true
	// (the default) the manual session is told to discover the PR's base branch
	// itself, fold each change via git commit --fixup + git rebase --autosquash,
	// and publish with git push --force-with-lease; when false (--no-fixup) it
	// appends a single on-top follow-up commit and pushes plainly.
	Fixup bool
}

// BarePromptBuildFn renders a self-contained fix prompt — no failing-check
// analysis, no log retrieval. Wired in by the CLI for --print-bare-prompt
// mode while keeping the import direction claude -> fix. The context is
// populated by the orchestrator from a fresh checkout of the target repo
// so the resulting prompt can embed the filtered pattern catalog inline.
type BarePromptBuildFn func(ctx BareContext) string

// ClaudeFixer is the injected dependency the orchestrator uses to run a
// single fix iteration. The production implementation is claude.Fix; tests
// substitute a fake that returns scripted summaries without invoking the
// real Claude CLI.
type ClaudeFixer interface {
	Fix(dir string, ctx Context) (string, error)
}

type fixFnAdapter struct {
	fn FixFn
}

func (a fixFnAdapter) Fix(dir string, ctx Context) (string, error) {
	return a.fn(dir, ctx)
}

// GitHubClient is the subset of github operations the fix loop needs. Each
// method maps to a single gh CLI invocation.
type GitHubClient interface {
	FetchAndCheckout(ref string) (*github.PR, error)
	FetchAndCheckoutLocal(ref string, opts github.LocalOptions) (*github.PR, error)
	ListChecks(owner, name, sha string) ([]github.CheckRun, error)
	FailedRunLogs(owner, name string, runID int64) (string, error)
	HeadSHA(owner, name string, branch string) (string, error)
	// PullOnBranch fast-forwards the local checkout in dir to the latest
	// commits on branch. Used in --local mode to pick up the previous
	// iteration's follow-up commit without re-cloning.
	PullOnBranch(dir, branch string) error
	// AddPRComment posts a fresh comment on the PR — one per fix iteration —
	// recording what the pushed follow-up commit changed.
	AddPRComment(owner, name string, number int, body string) (string, error)
}

// defaultGitHubClient is the production GitHubClient backed by the github
// package. Mirrors the elaborate package's adapter shape.
type defaultGitHubClient struct{}

func (defaultGitHubClient) FetchAndCheckout(ref string) (*github.PR, error) {
	return github.FetchAndCheckout(ref)
}

func (defaultGitHubClient) FetchAndCheckoutLocal(ref string, opts github.LocalOptions) (*github.PR, error) {
	return github.OpenLocalPR(ref, opts)
}

func (defaultGitHubClient) PullOnBranch(dir, branch string) error {
	return github.PullFFOnly(dir, branch)
}

func (defaultGitHubClient) ListChecks(owner, name, sha string) ([]github.CheckRun, error) {
	return github.ListChecks(owner, name, sha)
}

func (defaultGitHubClient) FailedRunLogs(owner, name string, runID int64) (string, error) {
	return github.FailedRunLogs(owner, name, runID)
}

func (defaultGitHubClient) HeadSHA(owner, name string, branch string) (string, error) {
	return github.BranchHeadSHA(owner, name, branch)
}

func (defaultGitHubClient) AddPRComment(owner, name string, number int, body string) (string, error) {
	return github.AddPRComment(owner, name, number, body)
}

// Prompter abstracts the "should we continue?" question asked between
// iterations when --interactive is set. The default reads y/n from stdin.
type Prompter interface {
	Confirm(message string) (bool, error)
}
