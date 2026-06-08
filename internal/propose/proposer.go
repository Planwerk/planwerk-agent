package propose

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/planwerk/planwerk-review/internal/cache"
	"github.com/planwerk/planwerk-review/internal/detect"
	"github.com/planwerk/planwerk-review/internal/github"
	"github.com/planwerk/planwerk-review/internal/patterns"
	"github.com/planwerk/planwerk-review/internal/workspace"
)

// Options configures the proposal pipeline.
type Options struct {
	RepoRef         string
	PatternDirs     []string
	NoRepoPatterns  bool
	NoLocalPatterns bool
	NoCache         bool
	Format          string // "markdown", "json", "issues"
	Version         string
	MaxPatterns     int           // max patterns to inject into prompt; <= 0 disables truncation
	CreateIssues    bool          // interactive issue creation after proposal generation
	NoIssueDedupe   bool          // skip filtering proposals against existing GitHub issues
	CacheMaxAge     time.Duration // reject cache entries older than this; <= 0 disables the TTL
	Local           bool          // operate on the current working directory instead of cloning
	Force           bool          // with Local, skip the dirty-working-tree confirmation prompt
}

// Runner executes the propose pipeline using injected Claude and GitHub
// clients. Constructing a Runner per invocation keeps dependencies explicit
// and allows tests to run in parallel without mutating package-level state.
type Runner struct {
	Claude ClaudeAnalyzer
	GitHub GitHubClient
}

// NewRunner returns a Runner wired with the production GitHub (git/gh CLI)
// backend and the given Claude analyze function.
func NewRunner(analyzeFn AnalyzeFn) *Runner {
	return &Runner{
		Claude: analyzeFnAdapter{fn: analyzeFn},
		GitHub: defaultGitHubClient{},
	}
}

// Run is a package-level convenience that delegates to NewRunner(analyzeFn).Run.
// Callers that need to inject alternative Claude or GitHub backends should
// construct a Runner directly.
func Run(w io.Writer, opts Options, analyzeFn AnalyzeFn) error {
	return NewRunner(analyzeFn).Run(w, opts)
}

// Run executes the full proposal pipeline: resolve HEAD SHA, check cache, and
// on a miss clone the repo, run Claude, cache, and render. Cache hits skip the
// clone entirely so CI loops against the same commit don't pay the clone cost.
func (r *Runner) Run(w io.Writer, opts Options) error {
	owner, name, err := github.ParseRepoRef(opts.RepoRef)
	if err != nil {
		return fmt.Errorf("parsing repo ref: %w", err)
	}

	// Resolve HEAD SHA via git ls-remote before cloning, so a cache hit can
	// short-circuit the clone entirely.
	headSHA, err := r.GitHub.DefaultBranchHEAD(owner, name)
	if err != nil {
		slog.Warn("could not fetch HEAD SHA, caching disabled", "err", err)
		opts.NoCache = true
		headSHA = ""
	}

	cacheKey := cache.RepoKey(owner, name, headSHA)
	if !opts.NoCache && headSHA != "" {
		if data, ok := cache.GetRaw(cacheKey, opts.CacheMaxAge); ok {
			var result ProposalResult
			if err := json.Unmarshal(data, &result); err == nil {
				slog.Info("using cached proposal result — skipping clone", "repo", opts.RepoRef)
				r.applyIssueDedupe(&result, owner, name, opts)
				return renderProposals(w, &result, &github.Repo{Owner: owner, Name: name}, opts)
			}
			slog.Warn("cache corrupted, running fresh analysis")
		}
	}

	repo, err := r.openRepo(opts)
	if err != nil {
		return fmt.Errorf("cloning repo: %w", err)
	}
	defer repo.Cleanup()

	slog.Info("cloned repository", "dir", repo.Dir)

	techTags := detect.Technologies(repo.Dir)
	if len(techTags) > 0 {
		slog.Info("detected technologies", "technologies", strings.Join(techTags, ", "))
	}

	patternDirs := collectPatternDirs(opts, repo.Dir)
	pats, err := patterns.LoadFiltered(techTags, patternDirs...)
	if err != nil {
		return fmt.Errorf("loading patterns: %w", err)
	}
	if len(pats) > 0 {
		slog.Info("loaded review patterns", "count", len(pats))
	} else {
		slog.Warn("no review patterns loaded — proposals will not be grounded in the pattern catalog")
	}

	slog.Info("analyzing codebase with Claude")
	result, err := r.Claude.Analyze(repo.Dir, AnalysisContext{
		Patterns:    pats,
		MaxPatterns: opts.MaxPatterns,
		RepoName:    repo.FullName(),
	})
	if err != nil {
		return fmt.Errorf("claude analysis: %w", err)
	}

	if !opts.NoCache && headSHA != "" {
		if data, err := json.Marshal(result); err == nil {
			if err := cache.PutRaw(cacheKey, cache.CommandPropose, data); err != nil {
				slog.Warn("could not cache result", "err", err)
			}
		}
	}

	slog.Info("analysis complete")
	r.applyIssueDedupe(result, owner, name, opts)
	return renderProposals(w, result, repo, opts)
}

// openRepo returns the working tree to analyze: the user's cwd when --local is
// set (no clone, Cleanup is a no-op), otherwise a fresh temp-dir clone.
func (r *Runner) openRepo(opts Options) (*github.Repo, error) {
	if opts.Local {
		repo, err := r.GitHub.CloneRepoLocal(opts.RepoRef, github.LocalOptions{Force: opts.Force, Prompter: workspace.NewStdinPrompter()})
		if err != nil {
			return nil, err
		}
		slog.Info("operating on local checkout", "dir", repo.Dir)
		return repo, nil
	}
	slog.Info("cloning repository", "repo", opts.RepoRef)
	return r.GitHub.CloneRepo(opts.RepoRef)
}

// applyIssueDedupe filters out proposals whose title matches an existing
// GitHub issue (open or closed) in the target repo. Dedupe runs after the
// cache layer so cached Claude output stays faithful to what Claude returned —
// issue state changes take effect on every run without invalidating the cache.
// A lister error logs a warning and skips dedupe rather than failing the run.
func (r *Runner) applyIssueDedupe(result *ProposalResult, owner, name string, opts Options) {
	if opts.NoIssueDedupe || r.GitHub == nil {
		return
	}
	existing, err := r.GitHub.ListExistingIssues(owner, name)
	if err != nil {
		slog.Warn("could not list existing issues, skipping dedupe", "err", err)
		return
	}
	idx := github.BuildTitleIndex(existing)
	if len(idx) == 0 {
		return
	}
	before := len(result.Proposals)
	kept := make([]Proposal, 0, before)
	for _, p := range result.Proposals {
		if match, ok := idx.Lookup(p.Title); ok {
			slog.Debug("skipping proposal already tracked by an existing issue",
				"title", p.Title, "existing", match.URL)
			continue
		}
		kept = append(kept, p)
	}
	result.Proposals = kept
	if filtered := before - len(kept); filtered > 0 {
		slog.Info("filtered proposals with existing issues",
			"filtered", filtered, "kept", len(kept))
	}
}

// collectPatternDirs assembles the list of pattern directories to load from,
// honoring the --no-local-patterns and --no-repo-patterns flags and appending
// any explicit --patterns directories supplied by the caller. Mirrors the
// audit pipeline's helper of the same name.
func collectPatternDirs(opts Options, repoDir string) []string {
	var patternDirs []string

	if !opts.NoLocalPatterns {
		if exe, err := os.Executable(); err == nil {
			localPatterns := filepath.Join(filepath.Dir(exe), "..", "patterns")
			if info, err := os.Stat(localPatterns); err == nil && info.IsDir() {
				patternDirs = append(patternDirs, localPatterns)
			}
		}
		if info, err := os.Stat("patterns"); err == nil && info.IsDir() {
			patternDirs = append(patternDirs, "patterns")
		}
	}

	if !opts.NoRepoPatterns {
		repoPatterns := filepath.Join(repoDir, ".planwerk", "review_patterns")
		if info, err := os.Stat(repoPatterns); err == nil && info.IsDir() {
			patternDirs = append(patternDirs, repoPatterns)
		}
	}

	patternDirs = append(patternDirs, opts.PatternDirs...)
	return patternDirs
}

func renderProposals(w io.Writer, result *ProposalResult, repo *github.Repo, opts Options) error {
	renderer := NewRenderer(w)

	switch opts.Format {
	case "json":
		return renderer.RenderJSON(*result)
	case "issues":
		renderer.RenderIssues(*result, repo.FullName())
		return nil
	default:
		renderer.RenderMarkdown(*result, repo.FullName(), opts.Version)
	}

	if opts.CreateIssues {
		return RunInteractiveIssueCreation(w, result, repo.Owner, repo.Name)
	}

	return nil
}
