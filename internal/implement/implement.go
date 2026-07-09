// Package implement turns an elaborated GitHub issue into a single Claude
// Code session that implements the feature end-to-end (code + tests + docs)
// inside a fresh clone of the target repository.
//
// The shape mirrors the fix package: an injectable Runner with GitHub /
// Claude / prompt-build dependencies, and two prompt-only escape hatches
// (--print-prompt embeds the issue body; --print-bare-prompt is a portable
// snippet that the user pastes into a manual session running inside their
// own checkout).
package implement

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/planwerk/planwerk-agent/internal/attribution"
	"github.com/planwerk/planwerk-agent/internal/capture"
	"github.com/planwerk/planwerk-agent/internal/detect"
	"github.com/planwerk/planwerk-agent/internal/github"
	"github.com/planwerk/planwerk-agent/internal/patterns"
	"github.com/planwerk/planwerk-agent/internal/report"
	"github.com/planwerk/planwerk-agent/internal/skills"
	"github.com/planwerk/planwerk-agent/internal/workspace"
)

// BundledPatternsURLBase is the public raw-markdown URL prefix the bare
// prompt's pattern catalog uses to point Claude at planwerk-agent's
// bundled pattern files. We pin to "main" so manual sessions always pick
// up the latest patterns without us baking the binary's version into URLs
// that then drift on dev builds.
const BundledPatternsURLBase = "https://raw.githubusercontent.com/planwerk/planwerk-agent/main/internal/patterns/patterns"

// Options configures the implement subcommand. Mirrors the Options style
// used by the review/audit/elaborate/fix packages.
type Options struct {
	IssueRef        string
	DryRun          bool // skip the Claude invocation; report what would happen
	PrintPrompt     bool // render the implement prompt to stdout and exit; never invoke Claude
	PrintBarePrompt bool // render a self-contained prompt to stdout and exit; never fetch issue or clone
	PrintPlanPrompt bool // render the planning prompt to stdout and exit; never invoke Claude
	NoPlan          bool // skip the planning session; the implement session plans for itself
	NoPlanReuse     bool // always run a fresh planning session; do not reuse a plan already posted on the issue
	NoPlanComment   bool // do not post the finished plan as a comment on the source issue
	NoReportComment bool // do not post the implementation report as a comment on the source issue
	Verify          bool // after implementing, run an independent verification pass against the diff
	// VerifyAdversarial red-teams the produced diff for the bugs it
	// introduces, reusing the adversarial-review machinery. Independent of
	// Verify: either, both, or neither may be set.
	VerifyAdversarial bool
	// NoSimplify disables the automatic simplify pass that, after the implement
	// session commits, folds removals of over-engineering into the branch's
	// commits on the local feature branch (no push). The pass is on by default.
	NoSimplify bool
	// NoReview disables the automatic review-and-fix pass that, after the
	// simplify pass, runs the adversarial review over the diff and folds each
	// fix into the commit it belongs to on the local branch (no push). The pass
	// is on by default; a full run is implement -> simplify -> review -> finalize.
	NoReview bool
	// NoCapture disables the read-only capture pass that, after the review pass
	// and before finalizing, proposes new wiki review patterns and memory pages
	// from the review findings, plan, and implementation report — writing
	// nothing. The pass is on by default but runs only when a wiki is resolved
	// (i.e. with --wiki); without one it is a no-op regardless of this flag.
	NoCapture bool
	// CaptureWiki gates the capture write-back: with it set, the capture pass
	// pushes the accepted proposal pages to the wiki after posting the proposal
	// comment. Default off keeps a run propose-only — Claude authors the page
	// bytes in the read-only proposal pass and a separate, confirmed write phase
	// performs the mechanical push.
	CaptureWiki bool
	// Yes skips the capture write-back's interactive confirmation, for a
	// non-interactive (CI) --capture-wiki run.
	Yes bool
	// MaxReviewIterations caps the review-and-fix loop: each round re-runs the
	// adversarial finder and, while it still reports findings, applies them —
	// stopping when the finder comes back clean, an apply escalates, or this many
	// rounds have run. <= 0 falls back to defaultMaxReviewIterations. Mirrors the
	// elaborate reviewer loop's bound.
	MaxReviewIterations int
	Local               bool // operate on the current working directory instead of cloning
	Force               bool // with Local, skip the dirty-working-tree confirmation prompt
	// NoResume disables resuming an earlier aborted run: instead of detecting the
	// feature branch a prior run for this issue left behind (commits already made
	// before it hit its session limit), checking it out, and continuing from the
	// next commit, the implement session starts a fresh branch. It also disables
	// pushing partial progress after an abort. Resume is on by default.
	NoResume bool
	Version  string

	// Pattern loading mirrors review/audit/elaborate so the implementation
	// is grounded in the same review catalog and any project-specific
	// patterns under .planwerk/review_patterns/ in the target repo.
	PatternDirs     []string
	NoRepoPatterns  bool
	NoLocalPatterns bool
	MaxPatterns     int
	// Remote configures how remote pattern URIs (--patterns github:..., git+...)
	// resolve into local directories; carries the --remote-patterns-ttl value.
	Remote patterns.RemoteOptions
	// Wiki configures the target repo's GitHub Wiki as a knowledge source
	// (review patterns + project memory); carries the --wiki/--no-wiki/--wiki-ref
	// values.
	Wiki patterns.WikiOptions
}

// defaultMaxReviewIterations bounds the review-and-fix loop so a finder and
// applier that keep disagreeing cannot spin forever. After this many rounds any
// findings still remaining are surfaced instead of retried. Mirrors elaborate's
// reviewer-loop bound.
const defaultMaxReviewIterations = 3

// Runner glues together the GitHub issue/clone calls, the Claude planner,
// the Claude implementer, and the prompt builders. Tests inject fakes via
// the exported fields.
type Runner struct {
	Claude ClaudeImplementer
	GitHub GitHubClient
	// Planner runs the read-only planning session whose output is embedded
	// into the implement prompt. When nil (or opts.NoPlan is set) the
	// implement session plans for itself, as before the planning phase
	// existed.
	Planner ClaudePlanner
	// BuildPrompt renders the implement prompt without invoking Claude.
	// Required when Options.PrintPrompt is set; nil otherwise is fine.
	BuildPrompt PromptBuildFn
	// BuildPlanPrompt renders the planning prompt without invoking Claude.
	// Required when Options.PrintPlanPrompt is set; nil otherwise is fine.
	BuildPlanPrompt PromptBuildFn
	// Verifier runs the optional independent verification pass. When nil (or
	// opts.Verify is false) the pass is skipped.
	Verifier ImplementationVerifier
	// AdversarialVerifier red-teams the produced diff for introduced bugs.
	// When nil (or opts.VerifyAdversarial is false) the pass is skipped.
	AdversarialVerifier AdversarialVerifier
	// Simplifier runs the read-only ponytail-style simplify pass over the
	// produced diff. Both it and SimplifyApplier must be non-nil (and
	// opts.NoSimplify false) for the simplify pass to run.
	Simplifier SimplifyFinder
	// SimplifyApplier applies the simplify findings and folds each into the
	// commit it belongs to on the local branch (no push). Paired with Simplifier;
	// nil leaves the pass disabled.
	SimplifyApplier SimplifyApplier
	// ReviewApplier applies the review pass's findings and folds each fix into
	// the commit it belongs to on the local branch. The review pass reuses
	// AdversarialVerifier as its finder, so both that field and this one must be
	// non-nil (and opts.NoReview false) for the pass to run. Nil leaves the
	// review-and-fix pass disabled.
	ReviewApplier ReviewApplier
	// Capturer runs the read-only capture pass after the review pass: it proposes
	// new wiki review patterns and memory pages from the review findings, plan,
	// and implementation report, writing nothing. Nil (or a wiki that did not
	// resolve, or opts.NoCapture) leaves the pass disabled.
	Capturer ClaudeCapturer
	// Finalizer opens the draft pull request after the simplify and review passes
	// have run on the local branch (a full run is implement -> simplify -> review
	// -> finalize). When nil the run stops with the branch committed but no PR
	// opened. The production wiring always sets it.
	Finalizer PRFinalizer
	// ResolveWiki resolves the target repo's wiki. Defaults to
	// patterns.ResolveWiki; a Runner seam so the capture pass can be exercised
	// against a temp wiki without cloning a real one.
	ResolveWiki resolveWikiFn
	// CaptureWriter performs the gated capture write-back: a fresh authenticated
	// clone of the wiki and the addition+push of the accepted pages. Defaults to
	// capture.DefaultWikiWriter; a Runner seam so the write-back can be exercised
	// without cloning or pushing a real wiki.
	CaptureWriter capture.WikiWriter
	// In is the stream the capture write-back's confirmation reads from. Defaults
	// to os.Stdin.
	In io.Reader
	// IsTTY reports whether the capture write-back may prompt interactively. When
	// it returns false a --capture-wiki run without --yes refuses rather than
	// failing open. Defaults to workspace.IsStdinTTY.
	IsTTY func() bool
}

// NewRunner builds a Runner with the production GitHub backend, the given
// Claude plan/implement functions, their prompt builders, the optional
// acceptance-criteria and adversarial verifiers, the optional simplify
// finder/applier, the optional review applier, and the finalize function. The
// CLI wires claude.Plan / claude.BuildPlanPrompt / claude.Implement /
// claude.BuildImplementPrompt / claude.VerifyImplementation /
// claude.AdversarialReview / claude.SimplifyFindings / claude.ApplySimplifications /
// claude.ApplyReview / claude.Capture / claude.FinalizePR so the import direction
// stays claude -> implement. A nil planFn disables the planning phase; a nil
// verifyFn leaves the verification pass disabled; a nil adversarialFn leaves both
// the adversarial pass and the review-and-fix finder disabled; a nil
// simplifyFindFn or simplifyApplyFn leaves the simplify pass disabled; a nil
// reviewApplyFn leaves the review-and-fix pass disabled; a nil captureFn leaves
// the capture pass disabled; a nil finalizeFn leaves the run on the committed
// branch with no PR opened.
func NewRunner(planFn PlanFn, buildPlan PromptBuildFn, fn ImplementFn, build PromptBuildFn, verifyFn VerifyFn, adversarialFn AdversarialFn, simplifyFindFn SimplifyFindFn, simplifyApplyFn SimplifyApplyFn, reviewApplyFn ReviewApplyFn, captureFn CaptureFn, finalizeFn FinalizeFn) *Runner {
	r := &Runner{
		Claude:          implementFnAdapter{fn: fn},
		GitHub:          defaultGitHubClient{},
		BuildPrompt:     build,
		BuildPlanPrompt: buildPlan,
	}
	if planFn != nil {
		r.Planner = planFnAdapter{fn: planFn}
	}
	if verifyFn != nil {
		r.Verifier = verifyFnAdapter{fn: verifyFn}
	}
	if adversarialFn != nil {
		r.AdversarialVerifier = adversarialFnAdapter{fn: adversarialFn}
	}
	if simplifyFindFn != nil {
		r.Simplifier = simplifyFindFnAdapter{fn: simplifyFindFn}
	}
	if simplifyApplyFn != nil {
		r.SimplifyApplier = simplifyApplyFnAdapter{fn: simplifyApplyFn}
	}
	if reviewApplyFn != nil {
		r.ReviewApplier = reviewApplyFnAdapter{fn: reviewApplyFn}
	}
	if captureFn != nil {
		r.Capturer = captureFnAdapter{fn: captureFn}
	}
	if finalizeFn != nil {
		r.Finalizer = finalizeFnAdapter{fn: finalizeFn}
	}
	return r
}

// Run is a package-level convenience that delegates to NewRunner(...).Run.
func Run(w io.Writer, opts Options, planFn PlanFn, buildPlan PromptBuildFn, fn ImplementFn, build PromptBuildFn, verifyFn VerifyFn, adversarialFn AdversarialFn, simplifyFindFn SimplifyFindFn, simplifyApplyFn SimplifyApplyFn, reviewApplyFn ReviewApplyFn, captureFn CaptureFn, finalizeFn FinalizeFn) error {
	return NewRunner(planFn, buildPlan, fn, build, verifyFn, adversarialFn, simplifyFindFn, simplifyApplyFn, reviewApplyFn, captureFn, finalizeFn).Run(w, opts)
}

// PrintBarePrompt is a package-level convenience that delegates to
// NewRunner(nil, ...).PrintBarePrompt. The prompt itself is built without
// invoking Claude, so the functions passed to NewRunner are not used here.
func PrintBarePrompt(w io.Writer, opts Options, build BarePromptBuildFn) error {
	return NewRunner(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil).PrintBarePrompt(w, opts, build)
}

// PrintBarePrompt builds a self-contained ("bare") implement prompt from
// the issue reference. Even though no Claude call is made, we still clone
// the target repo so the prompt can carry concrete context: detected
// technologies and the filtered review-pattern catalog (local +
// .planwerk/review_patterns/ + --patterns sources), inlined so the manual
// Claude session that pastes this prompt does not need access to
// planwerk-agent or its pattern dirs.
//
// The pasted-into Claude session is still expected to operate on its own
// checkout of the repository; the rendered prompt instructs it to fetch the
// issue itself and implement it end-to-end.
func (r *Runner) PrintBarePrompt(w io.Writer, opts Options, build BarePromptBuildFn) error {
	if build == nil {
		return errors.New("--print-bare-prompt requires a prompt builder; wire claude.BuildBareImplementPrompt")
	}
	owner, name, number, err := github.ParseIssueRef(opts.IssueRef)
	if err != nil {
		return fmt.Errorf("parsing issue ref: %w", err)
	}
	fullName := fmt.Sprintf("%s/%s", owner, name)

	repo, err := r.openRepo(opts, fullName)
	if err != nil {
		return fmt.Errorf("cloning repo for bare prompt build: %w", err)
	}
	defer repo.Cleanup()

	tags := detect.Technologies(repo.Dir)
	if len(tags) > 0 {
		slog.Info("detected technologies for bare prompt", "technologies", strings.Join(tags, ", "))
	}
	dirs, err := patterns.Resolve(patterns.ResolveOptions{
		NoLocal: opts.NoLocalPatterns,
		NoRepo:  opts.NoRepoPatterns,
		RepoDir: repo.Dir,
		Extra:   opts.PatternDirs,
	})
	if err != nil {
		slog.Warn("resolving pattern sources failed; bare prompt will omit them", "err", err)
	}
	pats, err := patterns.LoadFilteredWithOptions(patterns.LoadOptions{Remote: opts.Remote, NoEmbedded: opts.NoLocalPatterns}, tags, dirs...)
	if err != nil {
		slog.Warn("loading review patterns failed; bare prompt will omit them", "err", err)
		pats = nil
	}
	if len(pats) > 0 {
		design, technology, review, general := patterns.CountByCategory(pats)
		slog.Info("loaded patterns for bare prompt", "count", len(pats), "design", design, "technology", technology, "review", review, "general", general)
	}

	catalog := patterns.BuildCatalogReferences(pats, patterns.CatalogRefOptions{
		BundledRoot:    patterns.LocalPatternDir(opts.NoLocalPatterns),
		BundledURLBase: BundledPatternsURLBase,
		RepoRoot:       patterns.RepoPatternDir(opts.NoRepoPatterns, repo.Dir),
		RepoRelBase:    ".planwerk/review_patterns",
	})

	hasRepoLocal := false
	for _, c := range catalog {
		if c.LocalPath != "" {
			hasRepoLocal = true
			break
		}
	}

	prompt := build(BareContext{
		RepoFullName:     fullName,
		IssueNumber:      number,
		TechTags:         tags,
		PatternCatalog:   catalog,
		BundledURLBase:   BundledPatternsURLBase,
		HasRepoLocalRefs: hasRepoLocal,
		Skills:           skills.Load(repo.Dir),
	})
	if _, err := io.WriteString(w, prompt); err != nil {
		return fmt.Errorf("writing prompt: %w", err)
	}
	if !strings.HasSuffix(prompt, "\n") {
		_, _ = fmt.Fprintln(w)
	}
	return nil
}

// Run executes the implement workflow:
//  1. Resolve the issue (gh CLI).
//  2. In --print-prompt / --print-plan-prompt mode: render the requested
//     prompt with the issue body embedded and exit.
//  3. Otherwise clone the repo into a fresh temp directory.
//  4. In --dry-run mode: report what would happen and exit.
//  5. Unless --no-plan: supply the implement context with a plan. By default
//     an implementation plan planwerk-agent already posted on the issue is
//     reused verbatim (no planning session, no duplicate comment); --no-plan-reuse
//     forces a fresh read-only Claude planning session inside the clone (on the
//     dedicated planning model) whose finished plan is posted back onto the
//     source issue as a comment (unless --no-plan-comment). Either way the plan
//     is embedded into the implement context, and a STATUS: BLOCKED /
//     NEEDS_CONTEXT plan aborts before any code is written.
//  6. Run a Claude session inside the clone to implement the issue
//     end-to-end (code + tests + docs), committing on a fresh feature branch —
//     but NOT opening a pull request yet.
//  7. Post the implementation report back onto the source issue as a comment
//     (unless --no-report-comment), so every run — including no-op or failed
//     ones — leaves its course of events recorded on the issue.
//  8. Run the simplify and review-and-fix passes over the committed diff, on the
//     local branch, posting their reports onto the source issue.
//  9. Open the draft pull request last, so it lands already simplified and
//     self-reviewed (a full run is implement -> simplify -> review -> finalize).
func (r *Runner) Run(w io.Writer, opts Options) error {
	if opts.PrintPrompt && r.BuildPrompt == nil {
		return errors.New("--print-prompt requires a prompt builder; wire claude.BuildImplementPrompt via NewRunner")
	}
	if opts.PrintPlanPrompt && r.BuildPlanPrompt == nil {
		return errors.New("--print-plan-prompt requires a plan prompt builder; wire claude.BuildPlanPrompt via NewRunner")
	}

	owner, name, number, err := github.ParseIssueRef(opts.IssueRef)
	if err != nil {
		return fmt.Errorf("parsing issue ref: %w", err)
	}
	fullName := fmt.Sprintf("%s/%s", owner, name)
	slog.Info("implement starting",
		"issue", fmt.Sprintf("%s#%d", fullName, number),
		"dry_run", opts.DryRun,
		"print_prompt", opts.PrintPrompt,
	)

	issue, err := r.GitHub.GetIssue(owner, name, number)
	if err != nil {
		return fmt.Errorf("fetching issue: %w", err)
	}
	slog.Info("fetched issue", "repo", fullName, "issue", number, "title", issue.Title)

	ctx := Context{
		RepoFullName: fullName,
		IssueNumber:  number,
		IssueTitle:   issue.Title,
		IssueBody:    issue.Body,
		IssueURL:     issue.URL,
		IssueState:   issue.State,
		MaxPatterns:  opts.MaxPatterns,
	}

	// Resolve the issue's Meta/Sub-Issue neighborhood so the planning session
	// grounds a Sub Issue in its Meta Issue and sibling Sub Issues. Fetched
	// before the print-prompt branch so --print-plan-prompt renders the same
	// context the live planning session sees. Best-effort: a repo without
	// sub-issue relationships, a missing token scope, or an older GHES degrades
	// to "no relations" rather than aborting the run.
	relations, err := r.GitHub.GetIssueRelations(owner, name, number)
	if err != nil {
		slog.Warn("could not fetch sub-issue relations; planning without Meta/sibling context", "issue", number, "err", err)
		relations = &github.IssueRelations{}
	}
	if relations.Parent != nil {
		slog.Info("issue is a sub-issue; including Meta and sibling context in the plan", "issue", number, "meta", relations.Parent.Number, "siblings", len(relations.Siblings))
	}
	ctx.MetaIssue = relations.Parent
	ctx.SiblingIssues = relations.Siblings
	ctx.ChildIssues = relations.Children

	// In --print-prompt / --print-plan-prompt mode the only stdout payload
	// is the prompt itself; status chatter is silenced via slog (the prompt
	// goes to w). No clone, so no tech-detection/pattern-loading either —
	// the bare prompt asks Claude to inspect .planwerk/review_patterns/
	// itself if present.
	if opts.PrintPrompt || opts.PrintPlanPrompt {
		build := r.BuildPrompt
		if opts.PrintPlanPrompt {
			build = r.BuildPlanPrompt
		}
		prompt := build(ctx)
		if _, err := io.WriteString(w, prompt); err != nil {
			return fmt.Errorf("writing prompt: %w", err)
		}
		if !strings.HasSuffix(prompt, "\n") {
			_, _ = fmt.Fprintln(w)
		}
		return nil
	}

	planEnabled := r.Planner != nil && !opts.NoPlan

	if opts.DryRun {
		if planEnabled {
			_, _ = fmt.Fprintf(w, "[dry-run] would clone %s, run a Claude planning session, and run Claude to implement #%d (%s)\n",
				fullName, number, issue.Title)
		} else {
			_, _ = fmt.Fprintf(w, "[dry-run] would clone %s and run Claude to implement #%d (%s)\n",
				fullName, number, issue.Title)
		}
		return nil
	}

	repo, err := r.openRepo(opts, fullName)
	if err != nil {
		return fmt.Errorf("cloning repo: %w", err)
	}
	defer repo.Cleanup()
	slog.Info("cloned repository", "dir", repo.Dir)

	// Resolve the target repo's GitHub Wiki (best-effort): its review patterns
	// flow into the implement prompt through the shared pattern catalog, and its
	// project memory is injected into the planning prompt. An absent, disabled,
	// or offline wiki returns the zero value and leaves the run unchanged.
	resolveWiki := r.ResolveWiki
	if resolveWiki == nil {
		resolveWiki = patterns.ResolveWiki
	}
	wiki := resolveWiki(owner, name, opts.Wiki, opts.Remote)
	if wiki.CommitSHA != "" {
		slog.Info("resolved wiki", "repo", wiki.Repo, "commit", wiki.CommitSHA)
	}
	ctx.Patterns = loadPatterns(opts, repo.Dir, wiki.PatternsDir)
	ctx.Skills = skills.Load(repo.Dir)
	ctx.Memory = wiki.Memory

	if planEnabled {
		if err := r.preparePlan(w, opts, owner, name, repo.Dir, &ctx); err != nil {
			return err
		}
	}

	// Resume detection: when an earlier run for this issue aborted mid-work (e.g.
	// the session hit its usage limit) it left commits on a feature branch. Detect
	// and check that branch out, and hand the implement session the commits
	// already on it so it continues from where the earlier run stopped instead of
	// recreating the branch and redoing the work. Off with --no-resume.
	if !opts.NoResume {
		r.prepareResume(w, repo.Dir, number, &ctx)
	}

	implReport, model, err := r.Claude.Implement(repo.Dir, ctx)
	if err != nil {
		return fmt.Errorf("claude implement: %w", err)
	}

	// Guard the run on a complete implementation report. A one-shot, headless
	// implement session has no follow-up turn, so a session that yields mid-work
	// — backgrounding its tests to be "notified" later, or deferring a commit to
	// a turn that never comes — returns prose with neither the report heading nor
	// a terminal STATUS line. That is an unfinished implementation: do NOT post it
	// as a report, and do NOT run the simplify/review/finalize passes (which open
	// the pull request) on a half-built branch. The session's raw output still
	// goes to stdout so the operator can see what came back.
	status := implementReportStatus(implReport)
	if !strings.Contains(implReport, reportHeading) || status == "" {
		if strings.TrimSpace(implReport) != "" {
			_, _ = fmt.Fprintf(w, "\nClaude returned no valid implementation report:\n%s\n", implReport)
		}
		// The session stopped mid-work (this is the aborted-run case resume exists
		// for). Persist whatever it committed so the next run can continue from it,
		// then abort — the branch is half-built, so no PR is opened.
		r.persistPartialProgress(w, opts, repo.Dir)
		return fmt.Errorf("the implement session did not produce a complete implementation report (missing the %q heading or a terminal STATUS line); the implementation did not finish and no pull request was opened", reportHeading)
	}

	_, _ = fmt.Fprintf(w, "\nClaude implementation report:\n%s\n", implReport)
	r.postReportComment(w, opts, owner, name, number, implReport, model)

	// A BLOCKED / NEEDS_CONTEXT report means the session stopped before finishing
	// the work (a wrong issue, an unreachable criterion). The report is already
	// posted for the human who must intervene; stop here rather than open a PR on
	// an incomplete branch, mirroring the planning phase's escalation handling.
	if status == statusBlocked || status == statusNeedsContext {
		// Any commits the session did make before escalating are worth keeping for a
		// follow-up run that continues once the blocker is cleared.
		r.persistPartialProgress(w, opts, repo.Dir)
		return fmt.Errorf("the implement session reported %s; the implementation did not finish and no pull request was opened — review the report above and clarify the issue", status)
	}

	// Only a complete implementation of every work package ships. An issue lands
	// as exactly ONE pull request, so a PARTIAL report — the session was genuinely
	// interrupted (a circuit breaker, an exhausted budget) with at least one work
	// package unfinished — opens NO pull request: the committed progress is
	// persisted on the feature branch and the next implement run resumes it (see
	// prepareResume) until every package is done. Deferring the remaining packages
	// to a follow-up "Refs #N" PR is exactly the split delivery this guard forbids.
	if status == statusPartial {
		slog.Warn("implement session reported PARTIAL; persisting the branch for a resume instead of opening a pull request", "issue", number)
		r.persistPartialProgress(w, opts, repo.Dir)
		return fmt.Errorf("the implement session reported PARTIAL; no pull request was opened — rerun implement to resume the feature branch and finish the remaining work packages")
	}
	if status == statusDoneWithConcerns {
		slog.Warn("implement session reported DONE_WITH_CONCERNS", "issue", number)
	}
	slog.Info("implementation complete", "issue", number)

	// Simplify pass: runs over the diff the implement session committed, on the
	// local feature branch before any pull request exists. On by default;
	// --no-simplify or an unwired dependency skips it. Non-fatal, like the verify
	// passes.
	if !opts.NoSimplify && r.Simplifier != nil && r.SimplifyApplier != nil {
		r.runSimplify(w, repo.Dir, owner, name, number, ctx)
	}

	// Review-and-fix pass: runs after the simplify pass (a full run is
	// implement -> simplify -> review -> finalize), reusing the adversarial
	// review as the finder and folding each fix into the commit it belongs to. On
	// by default; --no-review or an unwired dependency skips it. Non-fatal, like
	// the verify and simplify passes. Its findings feed the capture pass below.
	var reviewFindings []report.Finding
	if !opts.NoReview && r.AdversarialVerifier != nil && r.ReviewApplier != nil {
		reviewFindings = r.runReview(w, repo.Dir, owner, name, number, ctx, opts)
	}

	// Capture pass: read-only proposal of new wiki review patterns and memory
	// pages from the review findings, the plan, and the implementation report —
	// writing nothing. Runs after the review pass and before finalizing, on by
	// default, but only when a wiki resolved (proposing wiki pages is pointless
	// without one). Non-fatal, like the surrounding passes. Capture still runs
	// (memory-only) when --no-review left the findings empty.
	if !opts.NoCapture && r.Capturer != nil && wiki.Dir != "" {
		r.runCapture(w, repo.Dir, owner, name, number, ctx, opts, implReport, reviewFindings, wiki)
	}

	// Optional read-only verification passes assess the final, simplified and
	// self-reviewed diff before the PR is opened. Both are non-fatal.
	if opts.Verify && r.Verifier != nil {
		r.runVerification(w, repo.Dir, ctx)
	}
	if opts.VerifyAdversarial && r.AdversarialVerifier != nil {
		r.runAdversarialVerification(w, repo.Dir)
	}

	// Finalize: open the draft pull request last, so it lands already simplified
	// and self-reviewed. Unlike the passes above this is the run's deliverable, so
	// a genuine failure to open the PR is fatal — the branch is committed and the
	// operator must know the PR was not created. (An empty change set is not a
	// failure: the finalize session opens no PR and says so.)
	if r.Finalizer != nil {
		if err := r.runFinalize(w, repo.Dir, ctx); err != nil {
			return err
		}
	}

	if opts.Local {
		// The feature branch the implement session created lives in the user's
		// working tree — tell them where to find it (stdout, since users want
		// the branch name there).
		_, _ = fmt.Fprintf(w, "\nWorking tree left on the feature branch in %s\n", repo.Dir)
		slog.Info("operating on local checkout", "dir", repo.Dir)
	}
	return nil
}

// openRepo returns the working tree for the implementation: the user's cwd
// when --local is set (no clone, Cleanup is a no-op), otherwise a fresh
// temp-dir clone of fullName.
func (r *Runner) openRepo(opts Options, fullName string) (*github.Repo, error) {
	if opts.Local {
		repo, err := r.GitHub.CloneRepoLocal(fullName, github.LocalOptions{Force: opts.Force, Prompter: workspace.NewStdinPrompter()})
		if err != nil {
			return nil, err
		}
		slog.Info("operating on local checkout", "dir", repo.Dir)
		return repo, nil
	}
	slog.Info("cloning repository for implementation", "repo", fullName)
	return r.GitHub.CloneRepo(fullName)
}

// prepareResume detects a feature branch an earlier aborted run for this issue
// left behind — the commits it made before it stopped — checks it out, and
// records it in ctx.Resume so the implement session continues on that branch
// instead of starting a fresh one. It is best-effort: any failure to detect or
// check out the branch is logged and surfaced but leaves ctx.Resume nil, so the
// run proceeds as a normal fresh implementation. No branch to resume is the
// silent, common case.
func (r *Runner) prepareResume(w io.Writer, dir string, number int, ctx *Context) {
	state, err := r.GitHub.PrepareResume(dir, number)
	if err != nil {
		slog.Warn("could not check for a resumable branch; implementing fresh", "issue", number, "err", err)
		_, _ = fmt.Fprintf(w, "\nCould not check for an earlier aborted run to resume: %v\n", err)
		return
	}
	if state == nil || len(state.Commits) == 0 {
		return
	}
	ctx.Resume = &ResumeContext{Branch: state.Branch, Commits: state.Commits}
	slog.Info("resuming an earlier aborted run", "issue", number, "branch", state.Branch, "commits", len(state.Commits))
	_, _ = fmt.Fprintf(w, "\nResuming on branch %s: %d commit(s) from an earlier aborted run are already present — continuing from where it stopped.\n", state.Branch, len(state.Commits))
}

// persistPartialProgress preserves the commits an aborted implement session left
// on its feature branch so a later run can resume them. In clone mode it pushes
// the branch to origin (no PR) — the temp clone is about to be deleted, so
// without the push the work is lost, and a later run then finds origin/<branch>
// and resumes. In --local mode the branch already persists in the user's
// checkout, so it only points the operator at it. The whole step is best-effort:
// a failure to resolve or push is logged and surfaced but never changes the abort
// error the caller is about to return. Disabled by --no-resume.
func (r *Runner) persistPartialProgress(w io.Writer, opts Options, dir string) {
	if opts.NoResume {
		return
	}
	state, err := r.GitHub.CurrentFeatureProgress(dir)
	if err != nil {
		slog.Warn("could not resolve partial progress to preserve", "err", err)
		return
	}
	if state == nil || len(state.Commits) == 0 {
		return // no commits to preserve
	}
	if opts.Local {
		_, _ = fmt.Fprintf(w, "\nPartial progress (%d commit(s)) is on local branch %s; rerun implement --local to resume it.\n", len(state.Commits), state.Branch)
		return
	}
	if err := r.GitHub.PushBranch(dir, state.Branch); err != nil {
		slog.Warn("could not push partial progress; it will be lost with the clone", "branch", state.Branch, "err", err)
		_, _ = fmt.Fprintf(w, "\nCould not push partial progress on branch %s (it will be lost with the temporary clone): %v\n", state.Branch, err)
		return
	}
	slog.Info("pushed partial progress for a later resume", "branch", state.Branch, "commits", len(state.Commits))
	_, _ = fmt.Fprintf(w, "\nPushed partial progress to origin/%s (%d commit(s)); rerun implement to resume it.\n", state.Branch, len(state.Commits))
}

// planHeading is the first line every plan carries ("## Implementation Plan
// (issue #N)"). It mirrors claude.planHeading; the constant is duplicated here
// rather than imported because the import direction is claude -> implement, so
// implement cannot reach into the claude package. mostRecentPlanComment uses it
// (together with planCommentFooter) to recognize a comment as a posted plan.
const planHeading = "## Implementation Plan"

// reportHeading is the first line every implementation report carries
// ("## Implementation Report (issue #N)"). It mirrors
// claude.implementReportHeading; the constant is duplicated here rather than
// imported because the import direction is claude -> implement, so implement
// cannot reach into the claude package. Run uses it, together with
// implementReportStatus, to confirm the implement session returned an actual
// completed report and not a bailed-session blurb.
const reportHeading = "## Implementation Report"

// preparePlan supplies the implement context with its plan. By default it first
// looks for an implementation plan planwerk-agent already posted on the source
// issue — left by an earlier run that planned but was aborted before
// implementing — and reuses it verbatim, skipping the expensive planning
// session and posting no duplicate comment. --no-plan-reuse forces a fresh
// planning session instead, for a plan that has gone stale because the issue
// changed after it was posted.
//
// A reused plan is held to the same bar as a fresh one: it is printed for the
// operator and run through planEscalation, so a previously posted STATUS:
// BLOCKED / NEEDS_CONTEXT plan still aborts before any code is written.
//
// Reading the comments is treated as load-bearing, not best-effort: if the
// lookup fails we abort rather than silently paying for a fresh planning pass
// the operator may not expect. --no-plan-reuse (skip the lookup) and --no-plan
// (skip planning) are the escape hatches when GitHub is unreachable.
func (r *Runner) preparePlan(w io.Writer, opts Options, owner, name, dir string, ctx *Context) error {
	if !opts.NoPlanReuse {
		comments, err := r.GitHub.ListIssueComments(owner, name, ctx.IssueNumber)
		if err != nil {
			return fmt.Errorf("reading issue comments to reuse a posted plan: %w (rerun with --no-plan-reuse to plan afresh, or --no-plan to skip planning)", err)
		}
		if plan := mostRecentPlanComment(comments); plan != "" {
			_, _ = fmt.Fprintf(w, "\nReusing the implementation plan already posted on issue #%d:\n%s\n", ctx.IssueNumber, plan)
			if status := planEscalation(plan); status != "" {
				return fmt.Errorf("the implementation plan already posted on issue #%d reported %s; review the plan above and clarify the issue, then rerun with --no-plan-reuse to plan afresh", ctx.IssueNumber, status)
			}
			ctx.Plan = plan
			slog.Info("reused existing plan from issue", "issue", ctx.IssueNumber)
			return nil
		}
		slog.Info("no reusable plan on issue; planning fresh", "issue", ctx.IssueNumber)
	}
	return r.runPlanning(w, opts, owner, name, dir, ctx)
}

// mostRecentPlanComment returns the body — footer stripped — of the most recent
// comment that planwerk-agent posted as an implementation plan, or "" when no
// comment is one. A plan comment is identified by carrying BOTH the
// "## Implementation Plan" heading and the plan attribution footer marker;
// requiring both keeps a report comment (reportCommentFooter + "## Implementation
// Report") from ever being mistaken for a plan. Matching on the model-independent
// planCommentMarker keeps detection working across model changes. gh lists
// comments oldest-first, so the walk runs newest-first and returns the first match.
func mostRecentPlanComment(comments []github.IssueComment) string {
	for i := len(comments) - 1; i >= 0; i-- {
		body := comments[i].Body
		if strings.Contains(body, planHeading) && strings.Contains(body, planCommentMarker) {
			return stripPlanCommentFooter(body)
		}
	}
	return ""
}

// stripPlanCommentFooter reverses formatPlanComment: it drops the "---"
// separator and attribution footer that wrap the plan in its comment body,
// returning the plan text alone (with its own "## Implementation Plan" heading)
// so it can feed Context.Plan exactly as a freshly generated plan would. It cuts
// at the model-independent planCommentMarker so the model-aware suffix is dropped
// regardless of which model produced the plan. A body without the footer is
// returned trimmed but otherwise unchanged.
func stripPlanCommentFooter(body string) string {
	if i := strings.LastIndex(body, planCommentMarker); i >= 0 {
		body = body[:i]
	}
	body = strings.TrimSpace(body)
	body = strings.TrimSuffix(body, "---")
	return strings.TrimSpace(body)
}

// runPlanning runs the read-only planning session inside the checkout and
// stores its plan in ctx.Plan for the implement prompt. Unlike verification,
// planning failures are fatal: the operator explicitly asked for a
// plan-first run, and silently proceeding without a plan would burn an
// unattended implement session on an unvetted route. A plan that reports
// STATUS: BLOCKED or NEEDS_CONTEXT also aborts — the planner found the
// issue unimplementable as specified, so a human must look at the plan
// before any code is written.
//
// Whichever way the plan turns out, the finished plan is posted back onto the
// source issue as a comment (unless --no-plan-comment) before the escalation
// check, so an escalated plan still lands where the human who must clarify the
// issue will see it.
func (r *Runner) runPlanning(w io.Writer, opts Options, owner, name, dir string, ctx *Context) error {
	slog.Info("running planning session", "issue", ctx.IssueNumber)
	plan, model, err := r.Planner.Plan(dir, *ctx)
	if err != nil {
		return fmt.Errorf("claude plan: %w (use --plan-model to plan on a different model, or --no-plan to skip the planning phase)", err)
	}
	plan = strings.TrimSpace(plan)
	if plan != "" {
		_, _ = fmt.Fprintf(w, "\nImplementation plan:\n%s\n", plan)
		r.postPlanComment(w, opts, owner, name, ctx.IssueNumber, plan, model)
	}
	if status := planEscalation(plan); status != "" {
		return fmt.Errorf("planning session reported %s; review the plan above and clarify the issue, or rerun with --no-plan", status)
	}
	ctx.Plan = plan
	slog.Info("planning complete", "issue", ctx.IssueNumber)
	return nil
}

// planCommentMarker is the stable, version- and model-independent prefix of the
// plan comment footer: it stops at the repository link, before planCommentFooter
// appends the build version, the " implement" word, and the model-aware
// "with Claude:<id>" suffix. mostRecentPlanComment keys its lookup on it (rather
// than the full footer) so a plan posted under one build or model is still
// recognized after either changes.
const planCommentMarker = "_Implementation plan generated by " + attribution.Link

// planCommentFooter attributes the posted plan to planwerk-agent, naming the
// model that produced it and matching the footer the propose/elaborate/audit
// subcommands append to the artifacts they leave on GitHub.
func planCommentFooter(model string) string {
	return "_Implementation plan generated by " + attribution.Tool() + " implement " + attribution.AssistantWith(model) + "_"
}

// postPlanComment posts the finished implementation plan as a comment on the
// source issue, so the plan that drives the implement session is recorded
// where reviewers and later runs can read it. Disabled by --no-plan-comment.
//
// Posting is best-effort: a failure to reach GitHub is logged and surfaced to
// the operator but never aborts the run — the plan is already in hand and the
// implement session can still proceed on it.
func (r *Runner) postPlanComment(w io.Writer, opts Options, owner, name string, number int, plan, model string) {
	if opts.NoPlanComment {
		return
	}
	url, err := r.GitHub.AddIssueComment(owner, name, number, formatPlanComment(plan, model))
	if err != nil {
		slog.Warn("posting plan comment failed", "issue", number, "err", err)
		_, _ = fmt.Fprintf(w, "\nCould not post the implementation plan as an issue comment: %v\n", err)
		return
	}
	slog.Info("posted implementation plan comment", "issue", number, "url", url)
	_, _ = fmt.Fprintf(w, "\nPosted the implementation plan as a comment on issue #%d", number)
	if url != "" {
		_, _ = fmt.Fprintf(w, " (%s)", url)
	}
	_, _ = fmt.Fprintln(w)
}

// formatPlanComment wraps the plan text in the issue-comment body: the plan
// verbatim (it already carries its own "## Implementation Plan" heading)
// followed by the attribution footer.
func formatPlanComment(plan, model string) string {
	return plan + "\n\n---\n\n" + planCommentFooter(model) + "\n"
}

// reportCommentFooter attributes the posted implementation report to
// planwerk-agent, naming the model that produced it and matching the footer
// the plan/fix subcommands append to the artifacts they leave on GitHub.
func reportCommentFooter(model string) string {
	return "_Implementation report generated by " + attribution.Tool() + " implement " + attribution.AssistantWith(model) + "_"
}

// postReportComment posts the implementation report as a comment on the source
// issue, so every implement run — success, no-op, or self-reported failure —
// records its course of events where reviewers and later runs can read it,
// the same way the plan already lands there. Disabled by --no-report-comment.
//
// Posting is best-effort: a failure to reach GitHub is logged and surfaced to
// the operator but never aborts the run — the implementation already happened
// and its report is on stdout regardless.
func (r *Runner) postReportComment(w io.Writer, opts Options, owner, name string, number int, report, model string) {
	if opts.NoReportComment {
		return
	}
	url, err := r.GitHub.AddIssueComment(owner, name, number, formatReportComment(report, model))
	if err != nil {
		slog.Warn("posting report comment failed", "issue", number, "err", err)
		_, _ = fmt.Fprintf(w, "\nCould not post the implementation report as an issue comment: %v\n", err)
		return
	}
	slog.Info("posted implementation report comment", "issue", number, "url", url)
	_, _ = fmt.Fprintf(w, "\nPosted the implementation report as a comment on issue #%d", number)
	if url != "" {
		_, _ = fmt.Fprintf(w, " (%s)", url)
	}
	_, _ = fmt.Fprintln(w)
}

// formatReportComment wraps the report text in the issue-comment body: the
// report verbatim (it already carries its own "## Implementation Report"
// heading) followed by the attribution footer.
func formatReportComment(report, model string) string {
	return report + "\n\n---\n\n" + reportCommentFooter(model) + "\n"
}

// Terminal STATUS markers a plan or implementation report can carry. They are
// shared by planEscalation and implementReportStatus rather than imported from
// internal/fix, keeping the import direction claude -> implement intact — the
// same reason planHeading/reportHeading are duplicated here.
const (
	statusDone             = "DONE"
	statusDoneWithConcerns = "DONE_WITH_CONCERNS"
	statusPartial          = "PARTIAL"
	statusBlocked          = "BLOCKED"
	statusNeedsContext     = "NEEDS_CONTEXT"
)

// planEscalation extracts the plan's terminal STATUS verdict and returns it
// when the plan is non-executable (BLOCKED or NEEDS_CONTEXT), or "" when the
// plan is executable (PLAN_READY, or a free-form plan with no STATUS line).
//
// Only the plan's authoritative verdict counts — the last line whose trimmed
// content begins with "STATUS: " followed by a known marker. A plan may
// legitimately *mention* "STATUS: BLOCKED" mid-sentence or inside backticks
// when the work it describes is about those very status values (the plan for
// issue #89, which hardens the implement session's BLOCKED/DONE_WITH_CONCERNS
// stop conditions, is exactly this case). Such mentions are documentation, not
// the verdict, and must not abort the run. Scanning line-anchored for the last
// standalone STATUS line — rather than a substring anywhere in the body —
// keeps those mentions, and the prompt's "STATUS: <PLAN_READY | BLOCKED |
// NEEDS_CONTEXT>" format spec, from tripping a false escalation.
func planEscalation(plan string) string {
	verdict := ""
	for _, line := range strings.Split(plan, "\n") {
		rest, ok := strings.CutPrefix(strings.TrimSpace(line), "STATUS: ")
		if !ok {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			continue
		}
		switch fields[0] {
		case "PLAN_READY", statusBlocked, statusNeedsContext:
			verdict = fields[0]
		}
	}
	if verdict == statusBlocked || verdict == statusNeedsContext {
		return verdict
	}
	return ""
}

// implementReportStatus returns the implement report's terminal STATUS verdict
// (DONE, DONE_WITH_CONCERNS, PARTIAL, BLOCKED, or NEEDS_CONTEXT), or "" when the
// report carries no recognized STATUS line. Like planEscalation it scans line-anchored
// and lets the last standalone verdict win, so a mid-sentence mention of a
// status value is not mistaken for the verdict; it additionally tolerates the
// markdown decoration a model sometimes adds (a leading list marker or heading,
// surrounding bold/backticks) and a trailing reason after the verdict word.
//
// Run keys the implement guard on it: an empty result means the session
// produced no terminal status — what a session that yielded mid-work returns —
// so the implementation did not finish and the run must abort rather than open
// a pull request on a half-built branch.
func implementReportStatus(report string) string {
	verdict := ""
	for _, raw := range strings.Split(report, "\n") {
		line := strings.TrimLeft(strings.TrimSpace(raw), "-*#> \t")
		if !strings.HasPrefix(strings.ToUpper(line), "STATUS:") {
			continue
		}
		fields := strings.Fields(strings.TrimSpace(line[len("STATUS:"):]))
		if len(fields) == 0 {
			continue
		}
		switch word := strings.ToUpper(strings.Trim(fields[0], "*`_")); word {
		case statusDone, statusDoneWithConcerns, statusPartial, statusBlocked, statusNeedsContext:
			verdict = word
		}
	}
	return verdict
}

// runVerification runs the independent verification pass against the change set
// the implement session produced and prints its verdict. Verification failures
// are non-fatal — the implementation still happened, and the operator gets the
// implementer's own report regardless.
//
// When the pass finds unmet-criteria findings and a review applier is wired, the
// findings are fed into the same ReviewApplier the review-and-fix pass uses so
// the gaps are closed on the local branch before the finalize step opens the PR.
// That apply is non-fatal, mirroring runReview: a failed branch resolution or
// apply is logged and surfaced but never aborts the run. With no applier wired
// (or a clean pass) the pass stays render-only, exactly as before.
func (r *Runner) runVerification(w io.Writer, dir string, ctx Context) {
	slog.Info("running independent implementation verification")
	result, err := r.Verifier.VerifyImplementation(dir, ctx.IssueTitle, ctx.IssueBody)
	if err != nil {
		slog.Warn("implementation verification failed", "err", err)
		_, _ = fmt.Fprintf(w, "\nIndependent verification could not run: %v\n", err)
		return
	}
	renderVerification(w, result)

	if r.ReviewApplier == nil || result == nil || len(result.Findings) == 0 {
		return
	}

	branch, err := r.GitHub.CurrentBranchRef(dir)
	if err != nil {
		slog.Warn("could not resolve the branch to apply verification findings; skipping", "err", err)
		_, _ = fmt.Fprintf(w, "\nVerification fixes skipped: could not resolve the base branch for this checkout: %v\n", err)
		return
	}

	slog.Info("feeding verification findings into the review applier", "findings", len(result.Findings))
	verifyReport, _, err := r.ReviewApplier.ApplyReview(dir, ReviewApplyContext{
		RepoFullName: ctx.RepoFullName,
		BaseBranch:   branch.BaseBranch,
		Findings:     result.Findings,
		Patterns:     ctx.Patterns,
		MaxPatterns:  ctx.MaxPatterns,
	})
	if err != nil {
		slog.Warn("verification apply failed", "err", err)
		_, _ = fmt.Fprintf(w, "\nVerification fixes could not run: %v\n", err)
		return
	}
	if verifyReport != "" {
		_, _ = fmt.Fprintf(w, "\nVerification fixes report:\n%s\n", verifyReport)
	}
	slog.Info("verification fixes complete")
}

// renderVerification prints the verification verdict: a clean pass when no
// criterion gaps were found, otherwise one block per unmet criterion.
func renderVerification(w io.Writer, result *report.ReviewResult) {
	if result == nil || len(result.Findings) == 0 {
		_, _ = fmt.Fprint(w, "\nIndependent verification: all Acceptance Criteria satisfied against the actual diff.\n")
		return
	}
	_, _ = fmt.Fprintf(w, "\nIndependent verification: %d unmet criterion finding(s) — the implementer's report was not trusted.\n\n", len(result.Findings))
	for _, f := range result.Findings {
		_, _ = fmt.Fprintf(w, "- [%s] %s", f.Severity, f.Title)
		if f.File != "" {
			_, _ = fmt.Fprintf(w, " (%s)", f.File)
		}
		_, _ = fmt.Fprintln(w)
		if f.Problem != "" {
			_, _ = fmt.Fprintf(w, "  %s\n", f.Problem)
		}
	}
}

// runAdversarialVerification red-teams the change set the implement session
// produced for the bugs it introduces, reusing the adversarial-review
// machinery, and prints its verdict. Like the acceptance-criteria pass it is
// non-fatal — the implementation already happened, and a red-team failure must
// not mask it. The base branch is left empty so AdversarialReview falls back to
// the repository's default branch.
func (r *Runner) runAdversarialVerification(w io.Writer, dir string) {
	slog.Info("running adversarial verification over the produced diff")
	result, err := r.AdversarialVerifier.AdversarialReview(dir, "")
	if err != nil {
		slog.Warn("adversarial verification failed", "err", err)
		_, _ = fmt.Fprintf(w, "\nAdversarial verification could not run: %v\n", err)
		return
	}
	renderAdversarialVerification(w, result)
}

// renderAdversarialVerification prints the red-team verdict: a clean pass when
// no introduced bug was found, otherwise one block per finding.
func renderAdversarialVerification(w io.Writer, result *report.ReviewResult) {
	if result == nil || len(result.Findings) == 0 {
		_, _ = fmt.Fprint(w, "\nAdversarial verification: no introduced bugs found in the produced diff.\n")
		return
	}
	_, _ = fmt.Fprintf(w, "\nAdversarial verification: %d finding(s) red-teaming the produced diff.\n\n", len(result.Findings))
	for _, f := range result.Findings {
		_, _ = fmt.Fprintf(w, "- [%s] %s", f.Severity, f.Title)
		if f.File != "" {
			_, _ = fmt.Fprintf(w, " (%s)", f.File)
		}
		_, _ = fmt.Fprintln(w)
		if f.Problem != "" {
			_, _ = fmt.Fprintf(w, "  %s\n", f.Problem)
		}
	}
}

// runSimplify runs the automatic simplify pass over the diff the implement
// session committed, on the local feature branch before any pull request
// exists: a read-only ponytail-style finder yields a delete-list of
// over-engineering, the guardrail filter drops any finding touching tests or
// assertions, and — when findings remain — a fresh session applies them and
// folds each into the commit it belongs to (no push; the finalize pass opens the
// PR afterwards). The whole pass is non-fatal: any failure (branch-ref
// resolution, finder error, apply error, comment-post failure) is logged and
// surfaced but never aborts the run, matching the verify passes' contract. No
// findings is a clean no-op — no commit, no issue comment.
func (r *Runner) runSimplify(w io.Writer, dir, owner, name string, number int, ctx Context) {
	slog.Info("running simplify pass over the produced diff")
	branch, err := r.GitHub.CurrentBranchRef(dir)
	if err != nil {
		slog.Warn("could not resolve the branch for the simplify pass; skipping", "err", err)
		_, _ = fmt.Fprintf(w, "\nSimplify pass skipped: could not resolve the base branch for this checkout: %v\n", err)
		return
	}

	result, err := r.Simplifier.SimplifyFindings(dir, branch.BaseBranch)
	if err != nil {
		slog.Warn("simplify finder failed", "err", err)
		_, _ = fmt.Fprintf(w, "\nSimplify pass could not run: %v\n", err)
		return
	}

	var findings []report.Finding
	if result != nil {
		findings = result.Findings
	}
	kept, rejected := keepSimplifyFindings(findings)
	if len(rejected) > 0 {
		slog.Info("simplify guardrail rejected findings touching tests or assertions", "rejected", len(rejected))
		_, _ = fmt.Fprintf(w, "\nSimplify guardrail rejected %d finding(s) touching test or assertion files.\n", len(rejected))
	}
	if len(kept) == 0 {
		slog.Info("simplify pass found nothing to simplify")
		_, _ = fmt.Fprintln(w, "\nNothing to simplify.")
		return
	}

	simplifyReport, model, err := r.SimplifyApplier.ApplySimplifications(dir, SimplifyApplyContext{
		RepoFullName: ctx.RepoFullName,
		BaseBranch:   branch.BaseBranch,
		Findings:     kept,
		Patterns:     ctx.Patterns,
		MaxPatterns:  ctx.MaxPatterns,
	})
	if err != nil {
		slog.Warn("simplify apply failed", "err", err)
		_, _ = fmt.Fprintf(w, "\nSimplify apply could not run: %v\n", err)
		return
	}
	if simplifyReport != "" {
		_, _ = fmt.Fprintf(w, "\nSimplification report:\n%s\n", simplifyReport)
		// Post the report before the escalation check so an escalated report
		// still lands on the issue for the human who must look at it.
		r.postSimplifyComment(w, owner, name, number, simplifyReport, model)
	}
	if status := planEscalation(simplifyReport); status != "" {
		_, _ = fmt.Fprintf(w, "\nClaude reported %s — stopping the simplify pass.\n", status)
	}
	slog.Info("simplify pass complete", "issue", number)
}

// keepSimplifyFindings splits simplify findings into those safe to apply and
// those the guardrail rejects. The hard guardrail forbids the pass from touching
// tests or assertions, so any finding whose File is a test or assertion file is
// rejected rather than applied. The other guardrail areas (validation, error
// handling, security, accessibility) are semantic and are enforced by the
// finder/apply prompts' hard rules.
func keepSimplifyFindings(in []report.Finding) (kept, rejected []report.Finding) {
	for _, f := range in {
		if isTestFile(f.File) {
			rejected = append(rejected, f)
			continue
		}
		kept = append(kept, f)
	}
	return kept, rejected
}

// isTestFile reports whether path looks like a test or assertion file, using a
// path heuristic that covers the conventions across the languages the tool
// reviews: a base name ending in _test.go / _test.py, a .test. / .spec.
// JS/TS-style name, or a path under a test/, tests/, or testdata/ directory.
func isTestFile(path string) bool {
	if path == "" {
		return false
	}
	p := strings.ToLower(path)
	base := p
	if i := strings.LastIndex(p, "/"); i >= 0 {
		base = p[i+1:]
	}
	if strings.HasSuffix(base, "_test.go") || strings.HasSuffix(base, "_test.py") {
		return true
	}
	if strings.Contains(base, ".test.") || strings.Contains(base, ".spec.") {
		return true
	}
	for _, seg := range []string{"test", "tests", "testdata"} {
		if strings.Contains(p, "/"+seg+"/") || strings.HasPrefix(p, seg+"/") {
			return true
		}
	}
	return false
}

// simplifyCommentFooter attributes the posted simplification report to
// planwerk-agent, naming the model that produced it and matching the footer the
// implement command's plan/report comments append to the artifacts they leave on
// GitHub.
func simplifyCommentFooter(model string) string {
	return "_Simplification report generated by " + attribution.Tool() + " implement " + attribution.AssistantWith(model) + "_"
}

// formatSimplifyComment wraps the simplification report in the issue-comment
// body: the report verbatim (it already carries its own "## Simplification
// Report" heading) followed by the attribution footer.
func formatSimplifyComment(simplifyReport, model string) string {
	return strings.TrimSpace(simplifyReport) + "\n\n---\n\n" + simplifyCommentFooter(model) + "\n"
}

// postSimplifyComment posts the simplification report as a comment on the source
// issue, so the record of what the simplify pass folded into the branch lands on
// the issue alongside the plan and implementation report — the PR does not exist
// yet when this pass runs.
//
// Posting is best-effort: a failure to reach GitHub is logged and surfaced to
// the operator but never aborts the run — the simplification is already folded
// into the branch, and its report is on stdout regardless.
func (r *Runner) postSimplifyComment(w io.Writer, owner, name string, number int, simplifyReport, model string) {
	url, err := r.GitHub.AddIssueComment(owner, name, number, formatSimplifyComment(simplifyReport, model))
	if err != nil {
		slog.Warn("posting simplify comment failed", "issue", number, "err", err)
		_, _ = fmt.Fprintf(w, "\nCould not post the simplification report as an issue comment: %v\n", err)
		return
	}
	slog.Info("posted simplification report comment", "issue", number, "url", url)
	_, _ = fmt.Fprintf(w, "\nPosted the simplification report as a comment on issue #%d", number)
	if url != "" {
		_, _ = fmt.Fprintf(w, " (%s)", url)
	}
	_, _ = fmt.Fprintln(w)
}

// runReview runs the automatic review-and-fix pass over the diff the implement
// session committed, on the local feature branch before any pull request
// exists: the adversarial review (the same finder --verify-adversarial uses,
// grounded in the implement pattern catalog via /review) yields structured
// findings, and — when findings remain — a fresh session resolves them and folds
// each fix into the commit it belongs to (no push; the finalize pass opens the
// PR afterwards).
//
// The pass is a bounded finder→apply loop (mirroring elaborate's reviewer refine
// loop): each round re-reviews the branch the prior apply just changed, so a fix
// that leaves a new problem behind is caught. The loop exits clean when the
// finder comes back with zero findings (the implement analog of elaborate's
// passing score), stops early when an apply escalates (STATUS: BLOCKED /
// NEEDS_CONTEXT), and otherwise caps at opts.MaxReviewIterations rounds — with a
// note when the budget runs out while findings still remain. The whole pass is
// non-fatal: any failure (branch-ref resolution, finder error, apply error,
// comment-post failure) is logged and surfaced but never aborts the run, matching
// the simplify and verify passes' contract. No findings on the first round is a
// clean no-op — no commit, no issue comment beyond a short stdout note.
//
// It returns the findings the finder produced across ALL iterations (even after
// they are folded in), so the capture pass can mine them for generalizable review
// patterns. A skipped or failed pass, and a pass that found nothing, return nil.
func (r *Runner) runReview(w io.Writer, dir, owner, name string, number int, ctx Context, opts Options) []report.Finding {
	slog.Info("running review-and-fix pass over the produced diff")
	branch, err := r.GitHub.CurrentBranchRef(dir)
	if err != nil {
		slog.Warn("could not resolve the branch for the review pass; skipping", "err", err)
		_, _ = fmt.Fprintf(w, "\nReview pass skipped: could not resolve the base branch for this checkout: %v\n", err)
		return nil
	}

	maxIter := opts.MaxReviewIterations
	if maxIter <= 0 {
		maxIter = defaultMaxReviewIterations
	}

	var allFindings []report.Finding
	for i := 1; i <= maxIter; i++ {
		result, err := r.AdversarialVerifier.AdversarialReview(dir, branch.BaseBranch)
		if err != nil {
			slog.Warn("review finder failed", "iteration", i, "err", err)
			_, _ = fmt.Fprintf(w, "\nReview pass could not run: %v\n", err)
			return allFindings
		}

		var findings []report.Finding
		if result != nil {
			findings = result.Findings
		}
		if len(findings) == 0 {
			if len(allFindings) == 0 {
				slog.Info("review pass found nothing to fix")
				_, _ = fmt.Fprintln(w, "\nReview found nothing to fix.")
			} else {
				slog.Info("review pass converged with no further findings", "issue", number, "iterations", i-1)
				_, _ = fmt.Fprintln(w, "\nReview pass converged: no further findings.")
			}
			return allFindings
		}
		allFindings = append(allFindings, findings...)

		reviewReport, model, err := r.ReviewApplier.ApplyReview(dir, ReviewApplyContext{
			RepoFullName: ctx.RepoFullName,
			BaseBranch:   branch.BaseBranch,
			Findings:     findings,
			Patterns:     ctx.Patterns,
			MaxPatterns:  ctx.MaxPatterns,
		})
		if err != nil {
			slog.Warn("review apply failed", "iteration", i, "err", err)
			_, _ = fmt.Fprintf(w, "\nReview apply could not run: %v\n", err)
			return allFindings
		}
		if reviewReport != "" {
			_, _ = fmt.Fprintf(w, "\nReview report:\n%s\n", reviewReport)
			// Post the report before the escalation check so an escalated report
			// still lands on the issue for the human who must look at it.
			r.postReviewComment(w, owner, name, number, reviewReport, model)
		}
		if status := planEscalation(reviewReport); status != "" {
			_, _ = fmt.Fprintf(w, "\nClaude reported %s — stopping the review pass.\n", status)
			return allFindings
		}
		slog.Info("review iteration applied; re-reviewing", "issue", number, "iteration", i)
	}

	// Budget exhausted with the finder still reporting findings on the last round.
	slog.Warn("review pass hit the iteration budget with findings still remaining", "issue", number, "iterations", maxIter)
	_, _ = fmt.Fprintf(w, "\nReview pass stopped after %d iteration(s) with findings still unresolved.\n", maxIter)
	return allFindings
}

// reviewCommentFooter attributes the posted review report to planwerk-agent,
// naming the model that produced it and matching the footer the implement
// command's plan/report/simplify comments append to the artifacts they leave on
// GitHub.
func reviewCommentFooter(model string) string {
	return "_Review report generated by " + attribution.Tool() + " implement " + attribution.AssistantWith(model) + "_"
}

// formatReviewComment wraps the review report in the issue-comment body: the
// report verbatim (it already carries its own "## Review Report" heading)
// followed by the attribution footer.
func formatReviewComment(reviewReport, model string) string {
	return strings.TrimSpace(reviewReport) + "\n\n---\n\n" + reviewCommentFooter(model) + "\n"
}

// postReviewComment posts the review report as a comment on the source issue, so
// the record of what the review pass folded into the branch lands on the issue
// alongside the plan, implementation, and simplification reports — the PR does
// not exist yet when this pass runs.
//
// Posting is best-effort: a failure to reach GitHub is logged and surfaced to
// the operator but never aborts the run — the fixes are already folded into the
// branch, and the report is on stdout regardless.
func (r *Runner) postReviewComment(w io.Writer, owner, name string, number int, reviewReport, model string) {
	url, err := r.GitHub.AddIssueComment(owner, name, number, formatReviewComment(reviewReport, model))
	if err != nil {
		slog.Warn("posting review comment failed", "issue", number, "err", err)
		_, _ = fmt.Fprintf(w, "\nCould not post the review report as an issue comment: %v\n", err)
		return
	}
	slog.Info("posted review report comment", "issue", number, "url", url)
	_, _ = fmt.Fprintf(w, "\nPosted the review report as a comment on issue #%d", number)
	if url != "" {
		_, _ = fmt.Fprintf(w, " (%s)", url)
	}
	_, _ = fmt.Fprintln(w)
}

// runCapture runs the read-only capture pass after the review pass and before
// finalizing: it mines the review findings, the plan, and the implementation
// report for generalizable review patterns and durable memory pages and proposes
// them as new wiki pages, deduplicated against the wiki and the catalog, writing
// nothing by default. The proposals go to stdout and are posted as an issue
// comment; under --capture-wiki the accepted pages are pushed to the wiki. The
// shared capture.Pass drives the propose-then-optionally-write mechanism so
// implement, review, and audit share one implementation.
//
// The whole pass is non-fatal: any failure (reading the wiki, the Claude call,
// the comment post, the push) is logged and surfaced but never aborts the run,
// matching the simplify and review passes' contract. The caller gates it on a
// resolved wiki and a wired Capturer (and not opts.NoCapture).
func (r *Runner) runCapture(w io.Writer, dir, owner, name string, number int, ctx Context, opts Options, implReport string, findings []report.Finding, wiki patterns.ResolvedWiki) {
	// The base branch only scopes the diff the proposal prompt reasons about, so
	// a failure to resolve it degrades to the default branch rather than skipping.
	baseBranch := ""
	if branch, err := r.GitHub.CurrentBranchRef(dir); err == nil && branch != nil {
		baseBranch = branch.BaseBranch
	}

	pass := capture.Pass{
		Propose: r.Capturer,
		// Post the proposals as a comment on the source issue so they surface
		// alongside the plan, implementation, and review reports.
		PostComment: func(body string) (string, error) {
			return r.GitHub.AddIssueComment(owner, name, number, body)
		},
		Writer: r.CaptureWriter,
		In:     r.In,
		IsTTY:  r.IsTTY,
	}
	// The write-back's error return is deliberately discarded here: implement's
	// capture runs before the finalize pass that opens the pull request (the run's
	// deliverable), so a wiki-push failure is logged and surfaced to w but must not
	// abort the run and block the PR — matching this pass's non-fatal contract. The
	// terminal review/audit capture passes propagate it instead.
	_ = pass.Run(w, capture.Request{
		Dir:         dir,
		Command:     "implement",
		Repo:        ctx.RepoFullName,
		Number:      number,
		BaseBranch:  baseBranch,
		Findings:    findings,
		Plan:        ctx.Plan,
		Report:      implReport,
		Patterns:    ctx.Patterns,
		Wiki:        wiki,
		WikiRef:     opts.Wiki.Ref,
		CaptureWiki: opts.CaptureWiki,
		Yes:         opts.Yes,
		Version:     opts.Version,
	})
}

// runFinalize opens the draft pull request as the final step, once the implement,
// simplify, and review passes have all run on the local feature branch. A fresh
// Claude session reads the final diff, writes the PR description (with the
// mandatory "Closes #N" link), pushes the branch, and opens the draft PR.
//
// Unlike the simplify/review passes this is the run's deliverable, so a genuine
// failure to open the PR is returned to the caller (fatal): the branch is
// committed but no PR was created, and the operator must know. An empty change
// set is NOT a failure — the finalize session opens no PR, reports that nothing
// was shippable, and returns no error, exactly as the implement session does when
// the issue turns out to be already implemented.
func (r *Runner) runFinalize(w io.Writer, dir string, ctx Context) error {
	slog.Info("running finalize pass to open the pull request", "issue", ctx.IssueNumber)
	// The finalize report carries the PR URL and goes to stdout; the PR is
	// auto-linked onto the issue by its issue-reference body, so the model id (used
	// elsewhere only to footer issue comments) is not needed here. Finalize only
	// ever runs for a complete implementation (a PARTIAL run aborts before it), so
	// the PR always links with the closing "Closes #N" keyword.
	finalizeReport, _, err := r.Finalizer.FinalizePR(dir, FinalizeContext{
		RepoFullName: ctx.RepoFullName,
		IssueNumber:  ctx.IssueNumber,
		IssueTitle:   ctx.IssueTitle,
	})
	if err != nil {
		slog.Warn("finalize pass failed; no pull request was opened", "issue", ctx.IssueNumber, "err", err)
		_, _ = fmt.Fprintf(w, "\nThe feature branch is committed, but opening the pull request failed: %v\n", err)
		return fmt.Errorf("opening the pull request: %w", err)
	}
	if finalizeReport != "" {
		_, _ = fmt.Fprintf(w, "\nPull request:\n%s\n", finalizeReport)
	}
	slog.Info("finalize pass complete", "issue", ctx.IssueNumber)
	return nil
}

// loadPatterns runs technology detection on the cloned repo and loads the
// review-pattern catalog filtered by those tags. Mirrors the
// audit/elaborate/propose helpers so the implement prompt is grounded in the
// same pattern set the rest of the tool uses, plus any project-specific
// patterns under .planwerk/review_patterns/ in the target repo.
//
// Failures are non-fatal: we fall back to running without patterns rather
// than blocking the implementation on a corrupt pattern source.
func loadPatterns(opts Options, repoDir, wikiPatternsDir string) []patterns.Pattern {
	tags := detect.Technologies(repoDir)
	if len(tags) > 0 {
		slog.Info("detected technologies", "technologies", strings.Join(tags, ", "))
	}
	dirs, err := patterns.Resolve(patterns.ResolveOptions{
		NoLocal: opts.NoLocalPatterns,
		NoRepo:  opts.NoRepoPatterns,
		RepoDir: repoDir,
		Wiki:    wikiPatternsDir,
		Extra:   opts.PatternDirs,
	})
	if err != nil {
		slog.Warn("resolving pattern sources failed; continuing without them", "err", err)
	}
	pats, err := patterns.LoadFilteredWithOptions(patterns.LoadOptions{Remote: opts.Remote, NoEmbedded: opts.NoLocalPatterns}, tags, dirs...)
	if err != nil {
		slog.Warn("loading review patterns failed; continuing without them", "err", err)
		return nil
	}
	if len(pats) > 0 {
		design, technology, review, general := patterns.CountByCategory(pats)
		slog.Info("loaded patterns", "count", len(pats), "design", design, "technology", technology, "review", review, "general", general)
	}
	return pats
}
