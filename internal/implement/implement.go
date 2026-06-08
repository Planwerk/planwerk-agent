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
	"os"
	"path/filepath"
	"strings"

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
const BundledPatternsURLBase = "https://raw.githubusercontent.com/planwerk/planwerk-review/main/patterns"

// Options configures the implement subcommand. Mirrors the Options style
// used by the review/audit/elaborate/fix packages.
type Options struct {
	IssueRef        string
	DryRun          bool // skip the Claude invocation; report what would happen
	PrintPrompt     bool // render the implement prompt to stdout and exit; never invoke Claude
	PrintBarePrompt bool // render a self-contained prompt to stdout and exit; never fetch issue or clone
	Verify          bool // after implementing, run an independent verification pass against the diff
	Local           bool // operate on the current working directory instead of cloning
	Force           bool // with Local, skip the dirty-working-tree confirmation prompt
	Version         string

	// Pattern loading mirrors review/audit/elaborate so the implementation
	// is grounded in the same review catalog and any project-specific
	// patterns under .planwerk/review_patterns/ in the target repo.
	PatternDirs     []string
	NoRepoPatterns  bool
	NoLocalPatterns bool
	MaxPatterns     int
}

// Runner glues together the GitHub issue/clone calls, the Claude
// implementer, and the prompt builder. Tests inject fakes via the
// exported fields.
type Runner struct {
	Claude ClaudeImplementer
	GitHub GitHubClient
	// BuildPrompt renders the implement prompt without invoking Claude.
	// Required when Options.PrintPrompt is set; nil otherwise is fine.
	BuildPrompt PromptBuildFn
	// Verifier runs the optional independent verification pass. When nil (or
	// opts.Verify is false) the pass is skipped.
	Verifier ImplementationVerifier
}

// NewRunner builds a Runner with the production GitHub backend, the given
// Claude implement function, the prompt builder, and an optional verifier. The
// CLI wires claude.Implement / claude.BuildImplementPrompt /
// claude.VerifyImplementation so the import direction stays claude -> implement.
// A nil verifyFn leaves the verification pass disabled.
func NewRunner(fn ImplementFn, build PromptBuildFn, verifyFn VerifyFn) *Runner {
	r := &Runner{
		Claude:      implementFnAdapter{fn: fn},
		GitHub:      defaultGitHubClient{},
		BuildPrompt: build,
	}
	if verifyFn != nil {
		r.Verifier = verifyFnAdapter{fn: verifyFn}
	}
	return r
}

// Run is a package-level convenience that delegates to NewRunner(...).Run.
func Run(w io.Writer, opts Options, fn ImplementFn, build PromptBuildFn, verifyFn VerifyFn) error {
	return NewRunner(fn, build, verifyFn).Run(w, opts)
}

// PrintBarePrompt is a package-level convenience that delegates to
// NewRunner(nil, nil, nil).PrintBarePrompt. The prompt itself is built without
// invoking Claude, so the functions passed to NewRunner are not used here.
func PrintBarePrompt(w io.Writer, opts Options, build BarePromptBuildFn) error {
	return NewRunner(nil, nil, nil).PrintBarePrompt(w, opts, build)
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
	dirs := collectPatternDirs(opts, repo.Dir)
	pats, err := patterns.LoadFiltered(tags, dirs...)
	if err != nil {
		slog.Warn("loading review patterns failed; bare prompt will omit them", "err", err)
		pats = nil
	}
	if len(pats) > 0 {
		slog.Info("loaded review patterns for bare prompt", "count", len(pats))
	}

	catalog := patterns.BuildCatalogReferences(pats, patterns.CatalogRefOptions{
		BundledRoot:    bundledPatternsRoot(opts),
		BundledURLBase: BundledPatternsURLBase,
		RepoRoot:       repoPatternsRoot(opts, repo.Dir),
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
//  2. In --print-prompt mode: render the prompt with the issue body
//     embedded and exit.
//  3. Otherwise clone the repo into a fresh temp directory.
//  4. In --dry-run mode: report what would happen and exit.
//  5. Run a Claude session inside the clone to implement the issue
//     end-to-end (code + tests + docs) and open a draft PR.
func (r *Runner) Run(w io.Writer, opts Options) error {
	if opts.PrintPrompt && r.BuildPrompt == nil {
		return errors.New("--print-prompt requires a prompt builder; wire claude.BuildImplementPrompt via NewRunner")
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

	// In --print-prompt mode the only stdout payload is the prompt itself;
	// status chatter is silenced via slog (the prompt goes to w). No clone,
	// so no tech-detection/pattern-loading either — the bare prompt asks
	// Claude to inspect .planwerk/review_patterns/ itself if present.
	if opts.PrintPrompt {
		prompt := r.BuildPrompt(ctx)
		if _, err := io.WriteString(w, prompt); err != nil {
			return fmt.Errorf("writing prompt: %w", err)
		}
		if !strings.HasSuffix(prompt, "\n") {
			_, _ = fmt.Fprintln(w)
		}
		return nil
	}

	if opts.DryRun {
		_, _ = fmt.Fprintf(w, "[dry-run] would clone %s and run Claude to implement #%d (%s)\n",
			fullName, number, issue.Title)
		return nil
	}

	repo, err := r.openRepo(opts, fullName)
	if err != nil {
		return fmt.Errorf("cloning repo: %w", err)
	}
	defer repo.Cleanup()
	slog.Info("cloned repository", "dir", repo.Dir)

	ctx.Patterns = loadPatterns(opts, repo.Dir)

	implReport, err := r.Claude.Implement(repo.Dir, ctx)
	if err != nil {
		return fmt.Errorf("claude implement: %w", err)
	}
	if implReport != "" {
		_, _ = fmt.Fprintf(w, "\nClaude implementation report:\n%s\n", implReport)
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
	dirs := collectPatternDirs(opts, repoDir)
	pats, err := patterns.LoadFiltered(tags, dirs...)
	if err != nil {
		slog.Warn("loading review patterns failed; continuing without them", "err", err)
		return nil
	}
	if len(pats) > 0 {
		slog.Info("loaded review patterns", "count", len(pats))
	}
	return pats
}

// collectPatternDirs assembles the pattern source list:
//   - the local catalog shipped with planwerk-review (./patterns next to the
//     binary, plus ./patterns relative to cwd for development)
//   - the target repo's .planwerk/review_patterns/ directory if present
//   - any explicit --patterns directories from the user
//
// The same toggles --no-local-patterns / --no-repo-patterns the other
// subcommands expose are honored here too.
func collectPatternDirs(opts Options, repoDir string) []string {
	var dirs []string
	if r := bundledPatternsRoot(opts); r != "" {
		dirs = append(dirs, r)
	}
	if r := repoPatternsRoot(opts, repoDir); r != "" {
		dirs = append(dirs, r)
	}
	dirs = append(dirs, opts.PatternDirs...)
	return dirs
}

// bundledPatternsRoot resolves the planwerk-review-bundled local pattern
// catalog (next to the binary, or ./patterns relative to cwd). Returns ""
// when --no-local-patterns is set or no candidate exists. Exported intent:
// the bare-prompt builder needs the same root to map a pattern's FilePath
// back to the canonical github.com/planwerk/planwerk-review URL.
func bundledPatternsRoot(opts Options) string {
	if opts.NoLocalPatterns {
		return ""
	}
	if exe, err := os.Executable(); err == nil {
		candidate := filepath.Join(filepath.Dir(exe), "..", "patterns")
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
	}
	if info, err := os.Stat("patterns"); err == nil && info.IsDir() {
		return "patterns"
	}
	return ""
}

// repoPatternsRoot resolves the target repo's project-specific pattern
// directory. Returns "" when --no-repo-patterns is set or the directory
// does not exist. The bare-prompt builder uses this to emit "read this
// from your checkout" entries instead of remote URLs.
func repoPatternsRoot(opts Options, repoDir string) string {
	if opts.NoRepoPatterns {
		return ""
	}
	candidate := filepath.Join(repoDir, ".planwerk", "review_patterns")
	if info, err := os.Stat(candidate); err == nil && info.IsDir() {
		return candidate
	}
	return ""
}
