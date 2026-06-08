package review

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/planwerk/planwerk-review/internal/cache"
	"github.com/planwerk/planwerk-review/internal/checklist"
	"github.com/planwerk/planwerk-review/internal/claude"
	"github.com/planwerk/planwerk-review/internal/detect"
	"github.com/planwerk/planwerk-review/internal/doccheck"
	"github.com/planwerk/planwerk-review/internal/github"
	"github.com/planwerk/planwerk-review/internal/patterns"
	"github.com/planwerk/planwerk-review/internal/planwerk"
	"github.com/planwerk/planwerk-review/internal/redact"
	"github.com/planwerk/planwerk-review/internal/report"
	"github.com/planwerk/planwerk-review/internal/todocheck"
	"github.com/planwerk/planwerk-review/internal/workspace"
)

type Options struct {
	PRRef           string
	PatternDirs     []string
	NoRepoPatterns  bool
	NoLocalPatterns bool
	NoCache         bool
	MinSeverity     report.Severity
	MinConfidence   report.Confidence
	Format          string
	Version         string
	PostReview      bool
	InlineReview    bool
	Thorough        bool
	Specialists     bool // run the domain-specialist fan-out and merge its findings
	CoverageMap     bool
	MaxPatterns     int           // max patterns to inject into prompt; <= 0 disables truncation
	MaxFindings     int           // cap on findings Claude returns; <= 0 disables cap
	CacheMaxAge     time.Duration // reject cache entries older than this; <= 0 disables the TTL
	Local           bool          // operate on the current working directory instead of cloning
	Force           bool          // with Local, skip the dirty-working-tree confirmation prompt
}

// Runner executes the review pipeline using injected Claude and GitHub
// clients. Constructing a Runner per invocation keeps dependencies explicit
// and allows tests to run in parallel without mutating package-level state.
type Runner struct {
	Claude ClaudeRunner
	GitHub GitHubClient
}

// NewRunner returns a Runner wired with the production Claude Code and
// GitHub (gh CLI) backends.
func NewRunner() *Runner {
	return &Runner{
		Claude: defaultClaudeRunner{},
		GitHub: defaultGitHubClient{},
	}
}

// Run is a package-level convenience that delegates to NewRunner().Run.
// Callers that need to inject alternative Claude or GitHub backends should
// construct a Runner directly.
func Run(w io.Writer, opts Options) error {
	return NewRunner().Run(w, opts)
}

// Run executes the full review pipeline:
// fetch & checkout PR → load patterns → claude /review → structure → render report.
func (r *Runner) Run(w io.Writer, opts Options) error {
	// 1. Fetch and checkout PR
	slog.Info("fetching and checking out PR", "pr", opts.PRRef)
	var (
		pr  *github.PR
		err error
	)
	if opts.Local {
		pr, err = r.GitHub.FetchAndCheckoutLocal(opts.PRRef, github.LocalOptions{Force: opts.Force, Prompter: workspace.NewStdinPrompter()})
	} else {
		pr, err = r.GitHub.FetchAndCheckout(opts.PRRef)
	}
	if err != nil {
		return fmt.Errorf("fetching PR: %w", err)
	}
	defer pr.Cleanup()

	slog.Info("checked out PR", "dir", pr.Dir)
	if opts.Local {
		slog.Info("working tree left on PR branch", "branch", pr.HeadBranch, "dir", pr.Dir)
	}

	// 2. Check cache (include flags that affect output in the cache key)
	var cacheFlags []string
	if opts.Thorough {
		cacheFlags = append(cacheFlags, "thorough")
	}
	if opts.Specialists {
		cacheFlags = append(cacheFlags, "specialists")
	}
	if opts.CoverageMap {
		cacheFlags = append(cacheFlags, "coverage-map")
	}
	cacheKey := cache.Key(pr.Owner, pr.Repo, pr.Number, pr.HeadSHA, cacheFlags...)
	if !opts.NoCache {
		if result, ok := cache.Get(cacheKey, opts.CacheMaxAge); ok {
			slog.Info("using cached review result")
			return r.renderResult(w, result, pr, opts, nil)
		}
	}

	// 3. Detect technologies in the reviewed repo
	techTags := detect.Technologies(pr.Dir)
	if len(techTags) > 0 {
		slog.Info("detected technologies", "technologies", strings.Join(techTags, ", "))
	}

	// 4. Load patterns (filtered by detected technologies)
	var patternDirs []string

	if !opts.NoLocalPatterns {
		// General patterns shipped with planwerk-review
		exe, err := os.Executable()
		if err == nil {
			localPatterns := filepath.Join(filepath.Dir(exe), "..", "patterns")
			if info, err := os.Stat(localPatterns); err == nil && info.IsDir() {
				patternDirs = append(patternDirs, localPatterns)
			}
		}
		// Also check relative to working directory (for development)
		if info, err := os.Stat("patterns"); err == nil && info.IsDir() {
			patternDirs = append(patternDirs, "patterns")
		}
	}

	if !opts.NoRepoPatterns {
		// Repo-specific patterns from the checked-out PR repo
		repoPatterns := filepath.Join(pr.Dir, ".planwerk", "review_patterns")
		if info, err := os.Stat(repoPatterns); err == nil && info.IsDir() {
			patternDirs = append(patternDirs, repoPatterns)
		}
	}

	patternDirs = append(patternDirs, opts.PatternDirs...)

	pats, err := patterns.LoadFiltered(techTags, patternDirs...)
	if err != nil {
		return fmt.Errorf("loading patterns: %w", err)
	}

	if len(pats) > 0 {
		slog.Info("loaded review patterns", "count", len(pats))
	}

	// 5. Load checklist
	checklistContent := checklist.Load(pr.Dir)

	// 6. Fetch commit log for scope drift detection
	commitLog := getCommitLog(pr.Dir, pr.BaseBranch)

	// 7. Check for stale documentation
	staleDocs := doccheck.Check(pr.Dir, pr.BaseBranch)

	// 7b. Check for new features that may need documentation
	newFeatures := doccheck.CheckNewFeatures(pr.Dir, pr.BaseBranch)

	// 8. Load TODOS.md for cross-reference
	todoContent := todocheck.Load(pr.Dir)

	// 8b. Detect Planwerk feature file for compliance checking
	feature, _ := planwerk.DetectFeature(pr.Dir, pr.Title, pr.Body, pr.HeadBranch, pr.ChangedFiles)
	if feature != nil {
		slog.Info("detected Planwerk feature file", "feature_id", feature.FeatureID, "title", feature.Title)
	}

	// 9-12. Run Claude /review, adversarial review, coverage map, and feature compliance concurrently.
	// All calls operate on the same checkout and diff with no data dependencies,
	// so running them in parallel cuts wall-clock time from sum to max.
	slog.Info("running Claude /review")
	if opts.Thorough {
		slog.Info("running adversarial review pass")
	}
	if opts.CoverageMap {
		slog.Info("generating test coverage map")
	}
	if feature != nil {
		slog.Info("running feature compliance check")
	}

	// Scrub obvious secrets from untrusted PR-supplied text before it is
	// forwarded to Claude. A PR body or commit log accidentally committing
	// a token would otherwise be echoed verbatim into the prompt.
	redactedTitle := redact.Redact(pr.Title)
	redactedBody := redact.Redact(pr.Body)
	redactedCommitLog := redact.Redact(commitLog)
	warnRedaction("PR title", redactedTitle)
	warnRedaction("PR body", redactedBody)
	warnRedaction("commit log", redactedCommitLog)

	reviewCtx := claude.ReviewContext{
		Patterns:    pats,
		MaxPatterns: opts.MaxPatterns,
		MaxFindings: opts.MaxFindings,
		BaseBranch:  pr.BaseBranch,
		PRTitle:     redactedTitle.Text,
		PRBody:      redactedBody.Text,
		Checklist:   checklistContent,
		CommitLog:   redactedCommitLog.Text,
		StaleDocs:   staleDocs,
		NewFeatures: newFeatures,
		TodoContent: todoContent,
	}

	var (
		result           *report.ReviewResult
		advResult        *report.ReviewResult
		complianceResult *report.ReviewResult
		coverageResult   *report.CoverageResult
		advErr           error
		complianceErr    error
		covErr           error
	)
	// specialistResults[i] holds the findings from claude.Specialists[i]; a nil
	// entry means that specialist failed (non-fatal) and is skipped at merge.
	var specialistResults []*report.ReviewResult
	if opts.Specialists {
		specialistResults = make([]*report.ReviewResult, len(claude.Specialists))
		slog.Info("running specialist review fan-out", "specialists", len(claude.Specialists))
	}

	var g errgroup.Group
	g.Go(func() error {
		res, err := r.Claude.Review(pr.Dir, reviewCtx)
		if err != nil {
			return fmt.Errorf("claude review: %w", err)
		}
		result = res
		return nil
	})
	if opts.Thorough {
		g.Go(func() error {
			advResult, advErr = r.Claude.AdversarialReview(pr.Dir, pr.BaseBranch)
			return nil
		})
	}
	if opts.CoverageMap {
		g.Go(func() error {
			coverageResult, covErr = r.Claude.CoverageMap(pr.Dir, pr.BaseBranch)
			return nil
		})
	}
	if feature != nil {
		g.Go(func() error {
			complianceResult, complianceErr = r.Claude.FeatureCompliance(pr.Dir, pr.BaseBranch, feature)
			return nil
		})
	}
	if opts.Specialists {
		for i, sp := range claude.Specialists {
			g.Go(func() error {
				res, err := r.Claude.SpecialistReview(pr.Dir, pr.BaseBranch, sp.Key, sp.Focus)
				if err != nil {
					// A failed specialist must not sink the whole review.
					slog.Warn("specialist review failed", "specialist", sp.Key, "err", err)
					return nil
				}
				specialistResults[i] = res
				return nil
			})
		}
	}
	if err := g.Wait(); err != nil {
		return err
	}

	if advErr != nil {
		slog.Warn("adversarial review failed", "err", advErr)
	} else if advResult != nil {
		tagPass(result, passReview)
		tagPass(advResult, passAdversarial)
		result = mergeResults(result, advResult)
		appendSummaryNote(result, "includes adversarial review pass")
	}
	if complianceErr != nil {
		slog.Warn("feature compliance check failed", "err", complianceErr)
	} else if complianceResult != nil {
		tagPass(result, passReview)
		tagPass(complianceResult, passCompliance)
		result = mergeResults(result, complianceResult)
		appendSummaryNote(result, "includes feature-compliance pass")
	}
	if opts.Specialists {
		result = mergeSpecialists(result, specialistResults)
	}
	if covErr != nil {
		slog.Warn("coverage map failed", "err", covErr)
	}

	// 11b. Quote-or-demote gate: downgrade findings whose code snippet cannot
	// be located in the changed files so unverifiable claims land in the
	// Unverified section instead of next to confirmed bugs.
	if n := verifyFindingSnippets(result, pr.Dir, pr.ChangedFiles); n > 0 {
		slog.Info("demoted findings failing snippet verification", "count", n)
	}

	// 12. Cache result
	if !opts.NoCache {
		if err := cache.Put(cacheKey, cache.CommandReview, result); err != nil {
			slog.Warn("could not cache result", "err", err)
		}
	}

	// 13. Render
	slog.Info("review complete")
	return r.renderResult(w, result, pr, opts, coverageResult)
}

func (r *Runner) renderResult(w io.Writer, result *report.ReviewResult, pr *github.PR, opts Options, coverage *report.CoverageResult) error {
	// Surface a generic, content-free verdict — the forced-recommendation prompt
	// rule should make the model name the specific blocking finding instead.
	if len(result.Findings) > 0 && report.IsBoilerplateRecommendation(result.Recommendation) {
		slog.Warn("review recommendation is generic — the model did not name a specific finding", "recommendation", result.Recommendation)
	}

	// Persistent skip-suppression: when re-reviewing a PR, drop findings the user
	// already saw last time and did not act on, as long as their file is
	// unchanged since that review. The rendered sections use the filtered set;
	// the data block keeps the full set so the next re-review can compare again.
	displayResult := result
	var suppressed []report.Finding
	if opts.PostReview || opts.InlineReview {
		if kept, supp := r.skipSuppressed(result, pr); len(supp) > 0 {
			filtered := *result
			filtered.Findings = kept
			displayResult = &filtered
			suppressed = supp
			slog.Info("suppressed previously-reported findings on unchanged files", "count", len(supp))
		}
	}

	prInfo := report.PRInfo{
		Owner:  pr.Owner,
		Repo:   pr.Repo,
		Number: pr.Number,
		Title:  pr.Title,
	}

	// If posting review, capture output in a buffer as well
	var buf bytes.Buffer
	output := io.Writer(w)
	if opts.PostReview || opts.InlineReview {
		output = io.MultiWriter(w, &buf)
	}

	renderer := report.NewRenderer(output)

	switch opts.Format {
	case "json":
		if err := renderer.RenderJSON(*displayResult, opts.MinSeverity, opts.MinConfidence); err != nil {
			return err
		}
	default:
		renderer.RenderMarkdown(*displayResult, prInfo, opts.MinSeverity, opts.MinConfidence, opts.Version)
	}

	// Append coverage map if available
	if coverage != nil {
		report.RenderCoverageMap(output, *coverage)
	}

	if len(suppressed) > 0 {
		writeSuppressionNote(output, suppressed)
	}

	if opts.InlineReview {
		slog.Info("posting inline review with GitHub Review API")
		// Inline comments come from the display set (no new comments for
		// suppressed findings); the data block carries the full result.
		err := r.postInlineReview(displayResult, result, pr, &buf)
		if err != nil {
			slog.Warn("inline review failed, falling back to PR comment", "err", err)
			_, err = r.GitHub.PostPRComment(pr.Owner, pr.Repo, pr.Number, buf.String())
			if err != nil {
				return fmt.Errorf("posting PR comment (fallback): %w", err)
			}
		}
	} else if opts.PostReview {
		slog.Info("posting review as PR comment (will update existing if found)")
		// Append data block for machine consumption — always the FULL result so
		// the next re-review sees every finding, including the suppressed ones.
		dataBlock := report.RenderDataBlock(*result, pr.HeadSHA)
		body := buf.String() + dataBlock
		postResult, err := r.GitHub.PostPRComment(pr.Owner, pr.Repo, pr.Number, body)
		if err != nil {
			return fmt.Errorf("posting PR comment: %w", err)
		}
		slog.Info("review posted", "url", postResult)
	}

	return nil
}

// maxInlineComments is the conservative limit for inline comments per review.
const maxInlineComments = 50

func (r *Runner) postInlineReview(displayResult, fullResult *report.ReviewResult, pr *github.PR, summaryBuf *bytes.Buffer) error {
	// 1. Fetch the diff
	rawDiff, err := r.GitHub.FetchDiff(pr.Owner, pr.Repo, pr.Number)
	if err != nil {
		return fmt.Errorf("fetching diff: %w", err)
	}

	// 2. Parse the diff into a map
	diffMap := github.ParseDiff(rawDiff)

	// 3. Partition findings into inline-eligible and body-only. Suppressed
	// findings are excluded from inline comments (displayResult), but the data
	// block below is built from the full result.
	var inlineComments []github.ReviewComment
	for _, f := range displayResult.Findings {
		if f.File == "" || f.Line <= 0 || !diffMap.Contains(f.File, f.Line) {
			continue
		}
		if len(inlineComments) >= maxInlineComments {
			break
		}

		comment := github.ReviewComment{
			Path: f.File,
			Line: f.Line,
			Side: "RIGHT",
			Body: report.FormatInlineComment(f),
		}
		// Handle multi-line comments
		if f.LineEnd > f.Line && diffMap.Contains(f.File, f.LineEnd) {
			comment.StartLine = f.Line
			comment.StartSide = "RIGHT"
			comment.Line = f.LineEnd
		}
		inlineComments = append(inlineComments, comment)
	}

	// 4. Build the review summary body with data block (full result).
	dataBlock := report.RenderDataBlock(*fullResult, pr.HeadSHA)
	summaryBody := summaryBuf.String() + dataBlock

	// 5. Submit the review
	url, err := r.GitHub.SubmitPRReview(pr.Owner, pr.Repo, pr.Number, pr.HeadSHA, summaryBody, inlineComments)
	if err != nil {
		return err
	}

	slog.Info("inline review posted", "url", url)
	return nil
}

// gitLogTimeout is the maximum time allowed for local git log operations.
const gitLogTimeout = 30 * time.Second

// warnRedaction emits a slog.Warn when redact scrubbed at least one secret
// from a PR-supplied text field. The source argument identifies which field
// (e.g. "PR body") so operators can trace back to the leaking commit.
func warnRedaction(source string, r redact.Result) {
	if r.Total() == 0 {
		return
	}
	attrs := []any{"source", source, "total", r.Total()}
	for _, name := range r.Names() {
		attrs = append(attrs, name, r.Counts[name])
	}
	slog.Warn("redacted secrets before sending to Claude", attrs...)
}

// skipSuppressed loads the prior review comment's data block and suppresses the
// current findings the user already saw on a file that has not changed since.
// On any failure (no prior comment, unparseable data block, uncomputable diff)
// it suppresses nothing — the full set is returned.
func (r *Runner) skipSuppressed(result *report.ReviewResult, pr *github.PR) (kept, suppressed []report.Finding) {
	body, found, err := r.GitHub.FetchReviewComment(pr.Owner, pr.Repo, pr.Number)
	if err != nil {
		slog.Warn("could not fetch prior review comment; skipping suppression", "err", err)
		return result.Findings, nil
	}
	if !found {
		return result.Findings, nil
	}
	priorSHA, priorFindings, ok := report.ParseDataBlock(body)
	if !ok || priorSHA == "" || len(priorFindings) == 0 {
		return result.Findings, nil
	}
	changed, ok := changedFilesSince(pr.Dir, priorSHA)
	if !ok {
		// Without a reliable diff we cannot tell skipped from regressed; do not
		// suppress, so nothing is hidden on a bad signal.
		return result.Findings, nil
	}
	return report.FilterPreviouslyReported(result.Findings, priorFindings, func(file string) bool {
		return !changed[file]
	})
}

// changedFilesSince returns the set of files changed between sha and HEAD. ok is
// false when the diff cannot be computed (e.g. the prior SHA is not in this
// checkout), in which case the caller must not suppress anything.
func changedFilesSince(dir, sha string) (map[string]bool, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), gitLogTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "diff", "--name-only", sha, "HEAD")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return nil, false
	}
	changed := make(map[string]bool)
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			changed[line] = true
		}
	}
	return changed, true
}

// writeSuppressionNote renders a collapsed note listing findings suppressed as
// previously-reported, so nothing is hidden silently.
func writeSuppressionNote(w io.Writer, suppressed []report.Finding) {
	_, _ = fmt.Fprintf(w, "> [!NOTE]\n> %d finding(s) suppressed as previously reported on unchanged files since the last review:\n", len(suppressed))
	for _, f := range suppressed {
		_, _ = fmt.Fprintf(w, "> - %s", f.Title)
		if f.File != "" {
			_, _ = fmt.Fprintf(w, " (%s)", f.File)
		}
		_, _ = fmt.Fprintln(w)
	}
	_, _ = fmt.Fprintln(w)
}

// getCommitLog returns the one-line commit log between the base branch and HEAD.
// Returns empty string on error (non-fatal).
func getCommitLog(dir, baseBranch string) string {
	if baseBranch == "" {
		baseBranch = claude.DefaultBaseBranch
	}
	ctx, cancel := context.WithTimeout(context.Background(), gitLogTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "log", "origin/"+baseBranch+"..HEAD", "--oneline")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
