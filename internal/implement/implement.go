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

	"github.com/planwerk/planwerk-review/internal/attribution"
	"github.com/planwerk/planwerk-review/internal/detect"
	"github.com/planwerk/planwerk-review/internal/github"
	"github.com/planwerk/planwerk-review/internal/patterns"
	"github.com/planwerk/planwerk-review/internal/report"
	"github.com/planwerk/planwerk-review/internal/workspace"
)

// BundledPatternsURLBase is the public raw-markdown URL prefix the bare
// prompt's pattern catalog uses to point Claude at planwerk-review's
// bundled pattern files. We pin to "main" so manual sessions always pick
// up the latest patterns without us baking the binary's version into URLs
// that then drift on dev builds.
const BundledPatternsURLBase = "https://raw.githubusercontent.com/planwerk/planwerk-review/main/internal/patterns/patterns"

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
	Local             bool // operate on the current working directory instead of cloning
	Force             bool // with Local, skip the dirty-working-tree confirmation prompt
	Version           string

	// Pattern loading mirrors review/audit/elaborate so the implementation
	// is grounded in the same review catalog and any project-specific
	// patterns under .planwerk/review_patterns/ in the target repo.
	PatternDirs     []string
	NoRepoPatterns  bool
	NoLocalPatterns bool
	MaxPatterns     int
}

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
}

// NewRunner builds a Runner with the production GitHub backend, the given
// Claude plan/implement functions, their prompt builders, and the optional
// acceptance-criteria and adversarial verifiers. The CLI wires claude.Plan /
// claude.BuildPlanPrompt / claude.Implement / claude.BuildImplementPrompt /
// claude.VerifyImplementation / claude.AdversarialReview so the import
// direction stays claude -> implement. A nil planFn disables the planning
// phase; a nil verifyFn leaves the verification pass disabled; a nil
// adversarialFn leaves the adversarial pass disabled.
func NewRunner(planFn PlanFn, buildPlan PromptBuildFn, fn ImplementFn, build PromptBuildFn, verifyFn VerifyFn, adversarialFn AdversarialFn) *Runner {
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
	return r
}

// Run is a package-level convenience that delegates to NewRunner(...).Run.
func Run(w io.Writer, opts Options, planFn PlanFn, buildPlan PromptBuildFn, fn ImplementFn, build PromptBuildFn, verifyFn VerifyFn, adversarialFn AdversarialFn) error {
	return NewRunner(planFn, buildPlan, fn, build, verifyFn, adversarialFn).Run(w, opts)
}

// PrintBarePrompt is a package-level convenience that delegates to
// NewRunner(nil, ...).PrintBarePrompt. The prompt itself is built without
// invoking Claude, so the functions passed to NewRunner are not used here.
func PrintBarePrompt(w io.Writer, opts Options, build BarePromptBuildFn) error {
	return NewRunner(nil, nil, nil, nil, nil, nil).PrintBarePrompt(w, opts, build)
}

// PrintBarePrompt builds a self-contained ("bare") implement prompt from
// the issue reference. Even though no Claude call is made, we still clone
// the target repo so the prompt can carry concrete context: detected
// technologies and the filtered review-pattern catalog (local +
// .planwerk/review_patterns/ + --patterns sources), inlined so the manual
// Claude session that pastes this prompt does not need access to
// planwerk-review or its pattern dirs.
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
	pats, err := patterns.LoadFilteredWithOptions(patterns.LoadOptions{Remote: patterns.RemoteOpts(), NoEmbedded: opts.NoLocalPatterns}, tags, dirs...)
	if err != nil {
		slog.Warn("loading review patterns failed; bare prompt will omit them", "err", err)
		pats = nil
	}
	if len(pats) > 0 {
		slog.Info("loaded review patterns for bare prompt", "count", len(pats))
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
//     an implementation plan planwerk-review already posted on the issue is
//     reused verbatim (no planning session, no duplicate comment); --no-plan-reuse
//     forces a fresh read-only Claude planning session inside the clone (on the
//     dedicated planning model) whose finished plan is posted back onto the
//     source issue as a comment (unless --no-plan-comment). Either way the plan
//     is embedded into the implement context, and a STATUS: BLOCKED /
//     NEEDS_CONTEXT plan aborts before any code is written.
//  6. Run a Claude session inside the clone to implement the issue
//     end-to-end (code + tests + docs) and open a draft PR.
//  7. Post the implementation report back onto the source issue as a comment
//     (unless --no-report-comment), so every run — including no-op or failed
//     ones — leaves its course of events recorded on the issue.
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

	ctx.Patterns = loadPatterns(opts, repo.Dir)

	if planEnabled {
		if err := r.preparePlan(w, opts, owner, name, repo.Dir, &ctx); err != nil {
			return err
		}
	}

	implReport, err := r.Claude.Implement(repo.Dir, ctx)
	if err != nil {
		return fmt.Errorf("claude implement: %w", err)
	}
	if implReport != "" {
		_, _ = fmt.Fprintf(w, "\nClaude implementation report:\n%s\n", implReport)
		r.postReportComment(w, opts, owner, name, number, implReport)
	}
	slog.Info("implementation complete", "issue", number)

	if opts.Local {
		// The feature branch the implement session created lives in the user's
		// working tree — tell them where to find it (stdout, since users want
		// the branch name there).
		_, _ = fmt.Fprintf(w, "\nWorking tree left on the feature branch in %s\n", repo.Dir)
		slog.Info("operating on local checkout", "dir", repo.Dir)
	}

	if opts.Verify && r.Verifier != nil {
		r.runVerification(w, repo.Dir, ctx)
	}
	if opts.VerifyAdversarial && r.AdversarialVerifier != nil {
		r.runAdversarialVerification(w, repo.Dir)
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

// planHeading is the first line every plan carries ("## Implementation Plan
// (issue #N)"). It mirrors claude.planHeading; the constant is duplicated here
// rather than imported because the import direction is claude -> implement, so
// implement cannot reach into the claude package. mostRecentPlanComment uses it
// (together with planCommentFooter) to recognize a comment as a posted plan.
const planHeading = "## Implementation Plan"

// preparePlan supplies the implement context with its plan. By default it first
// looks for an implementation plan planwerk-review already posted on the source
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
// comment that planwerk-review posted as an implementation plan, or "" when no
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
	plan, err := r.Planner.Plan(dir, *ctx)
	if err != nil {
		return fmt.Errorf("claude plan: %w (use --no-plan to skip the planning phase)", err)
	}
	plan = strings.TrimSpace(plan)
	if plan != "" {
		_, _ = fmt.Fprintf(w, "\nImplementation plan:\n%s\n", plan)
		r.postPlanComment(w, opts, owner, name, ctx.IssueNumber, plan)
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

// planCommentFooter attributes the posted plan to planwerk-review, naming the
// model that produced it and matching the footer the propose/elaborate/audit
// subcommands append to the artifacts they leave on GitHub.
func planCommentFooter() string {
	return "_Implementation plan generated by " + attribution.Tool() + " implement " + attribution.Assistant() + "_"
}

// postPlanComment posts the finished implementation plan as a comment on the
// source issue, so the plan that drives the implement session is recorded
// where reviewers and later runs can read it. Disabled by --no-plan-comment.
//
// Posting is best-effort: a failure to reach GitHub is logged and surfaced to
// the operator but never aborts the run — the plan is already in hand and the
// implement session can still proceed on it.
func (r *Runner) postPlanComment(w io.Writer, opts Options, owner, name string, number int, plan string) {
	if opts.NoPlanComment {
		return
	}
	url, err := r.GitHub.AddIssueComment(owner, name, number, formatPlanComment(plan))
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
func formatPlanComment(plan string) string {
	return plan + "\n\n---\n\n" + planCommentFooter() + "\n"
}

// reportCommentFooter attributes the posted implementation report to
// planwerk-review, naming the model that produced it and matching the footer
// the plan/fix subcommands append to the artifacts they leave on GitHub.
func reportCommentFooter() string {
	return "_Implementation report generated by " + attribution.Tool() + " implement " + attribution.Assistant() + "_"
}

// postReportComment posts the implementation report as a comment on the source
// issue, so every implement run — success, no-op, or self-reported failure —
// records its course of events where reviewers and later runs can read it,
// the same way the plan already lands there. Disabled by --no-report-comment.
//
// Posting is best-effort: a failure to reach GitHub is logged and surfaced to
// the operator but never aborts the run — the implementation already happened
// and its report is on stdout regardless.
func (r *Runner) postReportComment(w io.Writer, opts Options, owner, name string, number int, report string) {
	if opts.NoReportComment {
		return
	}
	url, err := r.GitHub.AddIssueComment(owner, name, number, formatReportComment(report))
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
func formatReportComment(report string) string {
	return report + "\n\n---\n\n" + reportCommentFooter() + "\n"
}

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
		case "PLAN_READY", "BLOCKED", "NEEDS_CONTEXT":
			verdict = fields[0]
		}
	}
	if verdict == "BLOCKED" || verdict == "NEEDS_CONTEXT" {
		return verdict
	}
	return ""
}

// runVerification runs the independent verification pass against the change set
// the implement session produced and prints its verdict. Verification failures
// are non-fatal — the implementation still happened, and the operator gets the
// implementer's own report regardless.
func (r *Runner) runVerification(w io.Writer, dir string, ctx Context) {
	slog.Info("running independent implementation verification")
	result, err := r.Verifier.VerifyImplementation(dir, ctx.IssueTitle, ctx.IssueBody)
	if err != nil {
		slog.Warn("implementation verification failed", "err", err)
		_, _ = fmt.Fprintf(w, "\nIndependent verification could not run: %v\n", err)
		return
	}
	renderVerification(w, result)
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

// loadPatterns runs technology detection on the cloned repo and loads the
// review-pattern catalog filtered by those tags. Mirrors the
// audit/elaborate/propose helpers so the implement prompt is grounded in the
// same pattern set the rest of the tool uses, plus any project-specific
// patterns under .planwerk/review_patterns/ in the target repo.
//
// Failures are non-fatal: we fall back to running without patterns rather
// than blocking the implementation on a corrupt pattern source.
func loadPatterns(opts Options, repoDir string) []patterns.Pattern {
	tags := detect.Technologies(repoDir)
	if len(tags) > 0 {
		slog.Info("detected technologies", "technologies", strings.Join(tags, ", "))
	}
	dirs, err := patterns.Resolve(patterns.ResolveOptions{
		NoLocal: opts.NoLocalPatterns,
		NoRepo:  opts.NoRepoPatterns,
		RepoDir: repoDir,
		Extra:   opts.PatternDirs,
	})
	if err != nil {
		slog.Warn("resolving pattern sources failed; continuing without them", "err", err)
	}
	pats, err := patterns.LoadFilteredWithOptions(patterns.LoadOptions{Remote: patterns.RemoteOpts(), NoEmbedded: opts.NoLocalPatterns}, tags, dirs...)
	if err != nil {
		slog.Warn("loading review patterns failed; continuing without them", "err", err)
		return nil
	}
	if len(pats) > 0 {
		slog.Info("loaded review patterns", "count", len(pats))
	}
	return pats
}
