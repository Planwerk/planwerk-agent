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
)

type Options struct {
	PRRef           string
	PatternDirs     []string
	NoRepoPatterns  bool
	NoLocalPatterns bool
	NoCache         bool
	MinSeverity     report.Severity
	Format          string
	Version         string
	PostReview      bool
	InlineReview    bool
	Thorough        bool
	CoverageMap     bool
	MaxPatterns     int           // max patterns to inject into prompt; <= 0 disables truncation
	MaxFindings     int           // cap on findings Claude returns; <= 0 disables cap
	CacheMaxAge     time.Duration // reject cache entries older than this; <= 0 disables the TTL
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
	pr, err := r.GitHub.FetchAndCheckout(opts.PRRef)
	if err != nil {
		return fmt.Errorf("fetching PR: %w", err)
	}
	defer pr.Cleanup()

	slog.Info("checked out PR", "dir", pr.Dir)

	// 2. Check cache (include flags that affect output in the cache key)
	var cacheFlags []string
	if opts.Thorough {
		cacheFlags = append(cacheFlags, "thorough")
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
	if err := g.Wait(); err != nil {
		return err
	}

	if advErr != nil {
		slog.Warn("adversarial review failed", "err", advErr)
	} else if advResult != nil {
		result = mergeResults(result, advResult)
	}
	if complianceErr != nil {
		slog.Warn("feature compliance check failed", "err", complianceErr)
	} else if complianceResult != nil {
		result = mergeResults(result, complianceResult)
	}
	if covErr != nil {
		slog.Warn("coverage map failed", "err", covErr)
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
		if err := renderer.RenderJSON(*result, opts.MinSeverity); err != nil {
			return err
		}
	default:
		renderer.RenderMarkdown(*result, prInfo, opts.MinSeverity, opts.Version)
	}

	// Append coverage map if available
	if coverage != nil {
		report.RenderCoverageMap(output, *coverage)
	}

	if opts.InlineReview {
		slog.Info("posting inline review with GitHub Review API")
		err := r.postInlineReview(result, pr, &buf)
		if err != nil {
			slog.Warn("inline review failed, falling back to PR comment", "err", err)
			_, err = r.GitHub.PostPRComment(pr.Owner, pr.Repo, pr.Number, buf.String())
			if err != nil {
				return fmt.Errorf("posting PR comment (fallback): %w", err)
			}
		}
	} else if opts.PostReview {
		slog.Info("posting review as PR comment (will update existing if found)")
		// Append data block for machine consumption
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

func (r *Runner) postInlineReview(result *report.ReviewResult, pr *github.PR, summaryBuf *bytes.Buffer) error {
	// 1. Fetch the diff
	rawDiff, err := r.GitHub.FetchDiff(pr.Owner, pr.Repo, pr.Number)
	if err != nil {
		return fmt.Errorf("fetching diff: %w", err)
	}

	// 2. Parse the diff into a map
	diffMap := github.ParseDiff(rawDiff)

	// 3. Partition findings into inline-eligible and body-only
	var inlineComments []github.ReviewComment
	for _, f := range result.Findings {
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

	// 4. Build the review summary body with data block
	dataBlock := report.RenderDataBlock(*result, pr.HeadSHA)
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
