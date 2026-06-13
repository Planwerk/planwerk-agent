package rebase

import (
	"github.com/planwerk/planwerk-review/internal/github"
	"github.com/planwerk/planwerk-review/internal/patterns"
	"github.com/planwerk/planwerk-review/internal/report"
)

// ConflictContext is the input for the Claude conflict-resolution prompt for a
// single stopped commit. It carries both the commit being replayed and the
// files git could not merge, plus the pattern catalog so the resolution stays
// consistent with the project's review patterns.
type ConflictContext struct {
	RepoFullName    string
	PRNumber        int
	Onto            string
	HeadBranch      string
	Commit          github.Commit // the replayed commit that conflicted
	ConflictedFiles []string
	Patterns        []patterns.Pattern
	MaxPatterns     int
}

// AnalysisContext is the input for the post-rebase analysis prompt. It pairs
// the rebased commits with the upstream range that entered the base since the
// PR forked, so Claude can judge — per commit — whether those upstream changes
// invalidate any assumptions, even absent a textual conflict.
type AnalysisContext struct {
	RepoFullName    string
	PRNumber        int
	Onto            string
	RebasedCommits  []github.Commit
	UpstreamCommits []github.Commit
	Patterns        []patterns.Pattern
	MaxPatterns     int
}

// ApplyContext is the input for the Claude apply prompt invoked under
// --apply-adjustments. It carries the analysis to act on plus the branch
// metadata the fixup/autosquash workflow needs. The orchestrator owns the
// force-push, so the apply session never pushes.
type ApplyContext struct {
	RepoFullName string
	PRNumber     int
	Onto         string
	HeadBranch   string
	Analysis     report.RebaseAnalysis
	Patterns     []patterns.Pattern
	MaxPatterns  int
}

// BareContext is the input for the self-contained ("bare") rebase prompt
// rendered by --print-bare-prompt: a portable prompt covering the rebase,
// semantic conflict resolution, and the post-rebase analysis, with the pattern
// catalog inlined. Mirrors fix.BareContext.
type BareContext struct {
	RepoFullName     string
	PRNumber         int
	Onto             string
	TechTags         []string
	PatternCatalog   []patterns.CatalogReference
	BundledURLBase   string
	HasRepoLocalRefs bool
}

// Function shapes the CLI passes in to wire Claude into the orchestrator,
// keeping the import direction claude -> rebase. Each is adapted to the
// ClaudeRebaser interface so tests can substitute fakes.
type (
	// ResolveConflictFn resolves the conflict on one stopped commit and stages
	// the result. It returns a short human-readable summary.
	ResolveConflictFn func(dir string, ctx ConflictContext) (string, error)
	// AnalyzeFn produces the structured post-rebase analysis.
	AnalyzeFn func(dir string, ctx AnalysisContext) (*report.RebaseAnalysis, error)
	// ApplyFn applies the analysis as fixup commits (no push).
	ApplyFn func(dir string, ctx ApplyContext) (string, error)
	// AnalysisPromptFn renders the analysis prompt without invoking Claude,
	// for --print-prompt.
	AnalysisPromptFn func(ctx AnalysisContext) string
	// BarePromptFn renders the self-contained prompt for --print-bare-prompt.
	BarePromptFn func(ctx BareContext) string
)

// ClaudeRebaser is the injected dependency the orchestrator drives: resolve one
// conflict, analyze the rebased commits, and optionally apply the adjustments.
// The production implementation is the claude package; tests inject a fake.
type ClaudeRebaser interface {
	ResolveConflict(dir string, ctx ConflictContext) (string, error)
	AnalyzeRebasedCommits(dir string, ctx AnalysisContext) (*report.RebaseAnalysis, error)
	ApplyAdjustments(dir string, ctx ApplyContext) (string, error)
}

// claudeFns adapts the three bare functions the CLI passes in to the
// ClaudeRebaser interface.
type claudeFns struct {
	resolve ResolveConflictFn
	analyze AnalyzeFn
	apply   ApplyFn
}

func (c claudeFns) ResolveConflict(dir string, ctx ConflictContext) (string, error) {
	return c.resolve(dir, ctx)
}

func (c claudeFns) AnalyzeRebasedCommits(dir string, ctx AnalysisContext) (*report.RebaseAnalysis, error) {
	return c.analyze(dir, ctx)
}

func (c claudeFns) ApplyAdjustments(dir string, ctx ApplyContext) (string, error) {
	return c.apply(dir, ctx)
}

// GitClient is the subset of github operations the rebase loop needs. Each
// method maps to one git or gh invocation. Tests substitute a fake.
type GitClient interface {
	FetchAndCheckout(ref string) (*github.PR, error)
	FetchAndCheckoutLocal(ref string, opts github.LocalOptions) (*github.PR, error)
	FetchBranch(dir, branch string) error
	MergeBase(dir, ref1, ref2 string) (string, error)
	CommitsInRange(dir, rangeExpr string) ([]github.Commit, error)
	StartRebase(dir, onto string) (github.RebaseState, error)
	RebaseContinue(dir string) (github.RebaseState, error)
	RebaseAbort(dir string) error
	ResetHard(dir, ref string) error
	ForceWithLeasePush(dir, branch string) error
	AddPRComment(owner, repo string, number int, body string) (string, error)
}

// defaultGitClient is the production GitClient backed by the github package.
type defaultGitClient struct{}

func (defaultGitClient) FetchAndCheckout(ref string) (*github.PR, error) {
	return github.FetchAndCheckout(ref)
}

func (defaultGitClient) FetchAndCheckoutLocal(ref string, opts github.LocalOptions) (*github.PR, error) {
	return github.OpenLocalPR(ref, opts)
}

func (defaultGitClient) FetchBranch(dir, branch string) error {
	return github.FetchBranch(dir, branch)
}

func (defaultGitClient) MergeBase(dir, ref1, ref2 string) (string, error) {
	return github.MergeBase(dir, ref1, ref2)
}

func (defaultGitClient) CommitsInRange(dir, rangeExpr string) ([]github.Commit, error) {
	return github.CommitsInRange(dir, rangeExpr)
}

func (defaultGitClient) StartRebase(dir, onto string) (github.RebaseState, error) {
	return github.StartRebase(dir, onto)
}

func (defaultGitClient) RebaseContinue(dir string) (github.RebaseState, error) {
	return github.RebaseContinue(dir)
}

func (defaultGitClient) RebaseAbort(dir string) error {
	return github.RebaseAbort(dir)
}

func (defaultGitClient) ResetHard(dir, ref string) error {
	return github.ResetHard(dir, ref)
}

func (defaultGitClient) ForceWithLeasePush(dir, branch string) error {
	return github.ForceWithLeasePush(dir, branch)
}

func (defaultGitClient) AddPRComment(owner, repo string, number int, body string) (string, error) {
	return github.AddPRComment(owner, repo, number, body)
}
