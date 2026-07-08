package implement

import (
	"github.com/planwerk/planwerk-agent/internal/capture"
	"github.com/planwerk/planwerk-agent/internal/github"
	"github.com/planwerk/planwerk-agent/internal/patterns"
	"github.com/planwerk/planwerk-agent/internal/report"
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
	// Memory is the target repo's project memory from its GitHub Wiki. It is
	// injected into the planning prompt (the implement prompt itself stays
	// unchanged — the plan carries any memory-derived context forward). Empty
	// when the repo has no wiki memory.
	Memory string
	// MetaIssue, SiblingIssues, and ChildIssues place the source issue in its
	// Meta/Sub-Issue neighborhood so the planning session grounds a Sub Issue in
	// its larger effort instead of in isolation. They feed BuildPlanPrompt (the
	// implement prompt itself stays unchanged — the plan already carries any
	// cross-references forward). MetaIssue is the parent Meta Issue (nil when the
	// source issue is not a Sub Issue); SiblingIssues are the Meta Issue's other
	// Sub Issues; ChildIssues are the source issue's own Sub Issues when it is
	// itself a Meta Issue. All empty when the issue stands alone.
	MetaIssue     *github.Issue
	SiblingIssues []github.Issue
	ChildIssues   []github.Issue
	// Resume, when non-nil, marks this as a continuation of an earlier implement
	// run for the same issue that aborted mid-work (e.g. the session hit its usage
	// limit). The orchestrator has already checked out the feature branch that run
	// left behind; Resume carries that branch name and the commits already on it
	// so the implement prompt tells the session to stay on the branch, reconcile
	// the existing commits against the plan, and continue from where the earlier
	// run stopped instead of recreating the branch and redoing the work. Nil for a
	// fresh run.
	Resume *ResumeContext
}

// ResumeContext describes the partial work an earlier aborted run for this issue
// left on its feature branch, so the implement prompt can continue from it rather
// than start over. Branch is the feature branch (already checked out by the
// orchestrator); Commits are the commits already on it over the base branch,
// oldest-first, for the session to reconcile against the plan's Commit Sequence.
type ResumeContext struct {
	Branch  string
	Commits []github.Commit
}

// ImplementFn is the bare-function shape the CLI passes in to wire Claude
// into the orchestrator. Returns a short human-readable summary of what
// Claude did (already trimmed) — the orchestrator logs/prints this verbatim —
// and the resolved Claude model id for the report's attribution footer.
type ImplementFn func(dir string, ctx Context) (report, model string, err error)

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
// from the bundled planwerk-agent catalog) plus relative checkout paths
// (for project-specific patterns under .planwerk/review_patterns/). The
// pasted-into Claude session fetches each URL itself, so the prompt stays
// short and the patterns Claude sees are always the same as those
// displayed on github.com/planwerk/planwerk-agent.
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
	Implement(dir string, ctx Context) (report, model string, err error)
}

type implementFnAdapter struct {
	fn ImplementFn
}

func (a implementFnAdapter) Implement(dir string, ctx Context) (string, string, error) {
	return a.fn(dir, ctx)
}

// PlanFn is the bare-function shape of the planning session the CLI wires
// in. It runs read-only inside the checkout and returns the implementation
// plan text (already trimmed) that the implement session receives via
// Context.Plan, plus the resolved planning-model id for the plan comment's
// attribution footer.
type PlanFn func(dir string, ctx Context) (plan, model string, err error)

// ClaudePlanner is the injected dependency for the planning phase that
// precedes the implement session. The production implementation is
// claude.Plan (running on the dedicated planning model); tests substitute
// a fake that returns scripted plans without invoking the real Claude CLI.
type ClaudePlanner interface {
	Plan(dir string, ctx Context) (plan, model string, err error)
}

type planFnAdapter struct {
	fn PlanFn
}

func (a planFnAdapter) Plan(dir string, ctx Context) (string, string, error) {
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

// AdversarialVerifier red-teams a produced change set for the bugs it
// introduces, reusing the adversarial-review machinery instead of only
// checking acceptance-criteria coverage. baseBranch scopes the review to the
// diff against that branch; an empty value falls back to the default branch.
//
// It serves two passes. As the report-only --verify-adversarial pass it is
// wired only when that flag is set. As the finder for the default-on
// review-and-fix pass (paired with ReviewApplier) it is always wired, so the
// review pass runs unless --no-review disables it.
type AdversarialVerifier interface {
	AdversarialReview(dir, baseBranch string) (*report.ReviewResult, error)
}

// AdversarialFn is the bare-function form of AdversarialVerifier. It matches
// claude.AdversarialReview so the CLI can wire it directly.
type AdversarialFn func(dir, baseBranch string) (*report.ReviewResult, error)

type adversarialFnAdapter struct {
	fn AdversarialFn
}

func (a adversarialFnAdapter) AdversarialReview(dir, baseBranch string) (*report.ReviewResult, error) {
	return a.fn(dir, baseBranch)
}

// SimplifyApplyContext is the input for the Claude simplify-apply session: the
// simplification findings to fold into the local feature branch and the pattern
// catalog so the apply session stays consistent with the same review patterns
// the implementation honored. The apply session folds each finding into the
// commit it belongs to (git commit --fixup + git rebase --autosquash) but does
// NOT push — it runs before any pull request exists, on the branch the implement
// session committed; the finalize pass opens the PR afterwards. BaseBranch bounds
// the fold-rebase to the branch's own commits (the range origin/<base>..HEAD).
type SimplifyApplyContext struct {
	RepoFullName string
	BaseBranch   string
	Findings     []report.Finding
	Patterns     []patterns.Pattern
	MaxPatterns  int
}

// SimplifyFinder runs the read-only ponytail-style pass over a produced diff and
// returns structured simplification findings (a delete/collapse list of
// over-engineering). Optional: wired only when the simplify pass is enabled.
// baseBranch scopes the pass to the diff against that branch.
type SimplifyFinder interface {
	SimplifyFindings(dir, baseBranch string) (*report.ReviewResult, error)
}

// SimplifyFindFn is the bare-function form of SimplifyFinder. It matches
// claude.SimplifyFindings so the CLI can wire it directly.
type SimplifyFindFn func(dir, baseBranch string) (*report.ReviewResult, error)

type simplifyFindFnAdapter struct {
	fn SimplifyFindFn
}

func (a simplifyFindFnAdapter) SimplifyFindings(dir, baseBranch string) (*report.ReviewResult, error) {
	return a.fn(dir, baseBranch)
}

// SimplifyApplier applies the simplification findings and folds each into the
// commit it belongs to via fixup/autosquash on the local branch (no push). It
// returns the apply report and the resolved Claude model id for the report
// comment's attribution footer. Optional: wired only when the simplify pass is
// enabled.
type SimplifyApplier interface {
	ApplySimplifications(dir string, ctx SimplifyApplyContext) (report, model string, err error)
}

// SimplifyApplyFn is the bare-function form of SimplifyApplier. It matches
// claude.ApplySimplifications so the CLI can wire it directly.
type SimplifyApplyFn func(dir string, ctx SimplifyApplyContext) (report, model string, err error)

type simplifyApplyFnAdapter struct {
	fn SimplifyApplyFn
}

func (a simplifyApplyFnAdapter) ApplySimplifications(dir string, ctx SimplifyApplyContext) (string, string, error) {
	return a.fn(dir, ctx)
}

// ReviewApplyContext is the input for the Claude review-apply session: the
// adversarial-review findings to resolve on the local feature branch and the
// pattern catalog so the apply session stays consistent with the same review
// patterns the implementation honored. The apply session resolves each finding
// and folds the fix into the commit it belongs to (git commit --fixup + git
// rebase --autosquash) but does NOT push — it runs before any pull request
// exists; the finalize pass opens the PR afterwards. BaseBranch bounds the
// fold-rebase to the branch's own commits (the range origin/<base>..HEAD). It
// mirrors SimplifyApplyContext; the two stay separate types so each pass's
// prompt can evolve independently.
type ReviewApplyContext struct {
	RepoFullName string
	BaseBranch   string
	Findings     []report.Finding
	Patterns     []patterns.Pattern
	MaxPatterns  int
}

// ReviewApplier resolves the review pass's findings and folds each fix into the
// commit it belongs to via fixup/autosquash on the local branch (no push). It
// returns the apply report and the resolved Claude model id for the report
// comment's attribution footer. Optional: paired with the AdversarialVerifier
// finder; nil leaves the review-and-fix pass disabled.
type ReviewApplier interface {
	ApplyReview(dir string, ctx ReviewApplyContext) (report, model string, err error)
}

// ReviewApplyFn is the bare-function form of ReviewApplier. It matches
// claude.ApplyReview so the CLI can wire it directly.
type ReviewApplyFn func(dir string, ctx ReviewApplyContext) (report, model string, err error)

type reviewApplyFnAdapter struct {
	fn ReviewApplyFn
}

func (a reviewApplyFnAdapter) ApplyReview(dir string, ctx ReviewApplyContext) (string, string, error) {
	return a.fn(dir, ctx)
}

// ClaudeCapturer runs the read-only knowledge-proposal pass after the review
// pass: it mines the review findings, plan, and implementation report for
// candidate review patterns and memory pages, deduplicated against the wiki and
// the catalog, and authors them without writing anything. The production
// implementation is claude.Capture; tests substitute a fake. Optional — nil (or
// a wiki that did not resolve) leaves the capture pass disabled.
type ClaudeCapturer interface {
	Capture(dir string, ctx capture.CaptureContext) (*capture.CaptureResult, error)
}

// CaptureFn is the bare-function form of ClaudeCapturer. It matches
// claude.Capture so the CLI can wire it directly.
type CaptureFn func(dir string, ctx capture.CaptureContext) (*capture.CaptureResult, error)

type captureFnAdapter struct {
	fn CaptureFn
}

func (a captureFnAdapter) Capture(dir string, ctx capture.CaptureContext) (*capture.CaptureResult, error) {
	return a.fn(dir, ctx)
}

// resolveWikiFn resolves the target repo's wiki to its clone root, review
// patterns, memory, and commit. It matches patterns.ResolveWiki and is a Runner
// seam (defaulting to patterns.ResolveWiki) so the capture pass can be exercised
// against a temp wiki without cloning a real one. Mirrors sync.resolveWikiFn.
type resolveWikiFn func(owner, name string, wopts patterns.WikiOptions, ropts patterns.RemoteOptions) patterns.ResolvedWiki

// FinalizeContext is the input for the Claude finalize session that opens the
// draft pull request once the implement, simplify, and review passes have all
// run on the local feature branch. The session reads the final diff, writes the
// PR description (with the link to IssueNumber), pushes the branch, and opens the
// draft PR. It needs only the repository identity and the source issue — it
// resolves the base/head branches and the change set from git itself, so a
// non-"main" default branch and an empty change set are handled in the session
// rather than threaded through here.
//
// Closing selects how the PR links the issue. When true (a complete
// implementation — STATUS: DONE or DONE_WITH_CONCERNS), the PR links with the
// GitHub closing keyword "Closes #N" so merging closes the issue. When false (a
// PARTIAL implementation — at least one work package is unfinished), the PR links
// with a non-closing "Refs #N" instead, so the issue stays open for the remaining
// work packages and a merge does not falsely close it.
type FinalizeContext struct {
	RepoFullName string
	IssueNumber  int
	IssueTitle   string
	Closing      bool
}

// PRFinalizer opens the draft pull request for the implemented + simplified +
// reviewed branch: it reads the final diff, writes the PR description, pushes the
// branch, and opens the draft PR linked to the issue. It returns a short report
// (carrying the PR URL) and the resolved Claude model id. The production
// implementation is claude.FinalizePR; tests substitute a fake. When there is
// nothing to ship (no commits on the branch), the session opens no PR and says so
// in the report rather than erroring.
type PRFinalizer interface {
	FinalizePR(dir string, ctx FinalizeContext) (report, model string, err error)
}

// FinalizeFn is the bare-function form of PRFinalizer. It matches
// claude.FinalizePR so the CLI can wire it directly.
type FinalizeFn func(dir string, ctx FinalizeContext) (report, model string, err error)

type finalizeFnAdapter struct {
	fn FinalizeFn
}

func (a finalizeFnAdapter) FinalizePR(dir string, ctx FinalizeContext) (string, string, error) {
	return a.fn(dir, ctx)
}

// GitHubClient is the subset of github operations the implement command
// needs: fetching the source issue, listing its comments (to detect and reuse
// an implementation plan posted on an earlier run), cloning the repository so
// Claude has a working tree to operate on, posting the plan/report/simplify/
// review artifacts back onto the issue as comments, and — for the simplify and
// review passes — resolving the checkout's base branch from git so they can
// scope their diff before any pull request exists. Each method maps to a single
// gh or git invocation under the hood.
type GitHubClient interface {
	GetIssue(owner, name string, number int) (*github.Issue, error)
	GetIssueRelations(owner, name string, number int) (*github.IssueRelations, error)
	ListIssueComments(owner, name string, number int) ([]github.IssueComment, error)
	CloneRepo(ref string) (*github.Repo, error)
	CloneRepoLocal(ref string, opts github.LocalOptions) (*github.Repo, error)
	AddIssueComment(owner, name string, number int, body string) (string, error)
	CurrentBranchRef(dir string) (*github.BranchRef, error)
	// PrepareResume finds and checks out a feature branch an earlier aborted run
	// for this issue left behind, returning the commits already on it (nil when
	// there is nothing to resume). CurrentFeatureProgress reports the commits the
	// current checkout has committed over the base branch (nil when still on the
	// base or nothing is ahead), so the orchestrator can push partial progress
	// after an abort. PushBranch publishes the branch to origin (no PR) so a later
	// clone-mode run can fetch and resume it. Together they implement resuming an
	// aborted implement run.
	PrepareResume(dir string, number int) (*github.ResumeState, error)
	CurrentFeatureProgress(dir string) (*github.ResumeState, error)
	PushBranch(dir, branch string) error
}

// defaultGitHubClient is the production GitHubClient backed by the github
// package. Mirrors the elaborate package's adapter shape.
type defaultGitHubClient struct{}

func (defaultGitHubClient) GetIssue(owner, name string, number int) (*github.Issue, error) {
	return github.GetIssue(owner, name, number)
}

func (defaultGitHubClient) GetIssueRelations(owner, name string, number int) (*github.IssueRelations, error) {
	return github.GetIssueRelations(owner, name, number)
}

func (defaultGitHubClient) ListIssueComments(owner, name string, number int) ([]github.IssueComment, error) {
	return github.ListIssueComments(owner, name, number)
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

func (defaultGitHubClient) CurrentBranchRef(dir string) (*github.BranchRef, error) {
	return github.CurrentBranchRef(dir)
}

func (defaultGitHubClient) PrepareResume(dir string, number int) (*github.ResumeState, error) {
	return github.PrepareResume(dir, number)
}

func (defaultGitHubClient) CurrentFeatureProgress(dir string) (*github.ResumeState, error) {
	return github.CurrentFeatureProgress(dir)
}

func (defaultGitHubClient) PushBranch(dir, branch string) error {
	return github.PushHead(dir, branch)
}
