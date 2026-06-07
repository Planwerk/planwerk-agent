package audit

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
	"github.com/planwerk/planwerk-review/internal/report"
)

// Options configures the audit pipeline.
type Options struct {
	RepoRef          string
	PatternDirs      []string
	NoRepoPatterns   bool
	NoLocalPatterns  bool
	NoCache          bool
	MinSeverity      report.Severity
	MinConfidence    report.Confidence
	Format           string // "markdown" or "json"
	Version          string
	MaxPatterns      int             // max patterns to inject into prompt; <= 0 disables truncation
	MaxFindings      int             // cap on findings Claude returns; <= 0 disables cap
	CreateIssues     bool            // interactively create GitHub issues after audit
	IssueMinSeverity report.Severity // minimum severity for a finding group to become an issue candidate
	NoIssueDedupe    bool            // skip filtering findings against existing GitHub issues
	CacheMaxAge      time.Duration   // reject cache entries older than this; <= 0 disables the TTL
}

// AuditFn performs the Claude-backed codebase audit for a cloned repo.
// It is injected so tests can substitute a fake implementation without
// invoking the real Claude Code.
type AuditFn func(dir string, ctx AuditContext) (*report.ReviewResult, error)

// AuditContext holds all context needed to build the audit prompt.
type AuditContext struct {
	Patterns    []patterns.Pattern
	MaxPatterns int
	MaxFindings int
	RepoName    string // "owner/repo" for context in the prompt
}

// Runner executes the audit pipeline using injected Claude and GitHub
// clients. Constructing a Runner per invocation keeps dependencies explicit
// and allows tests to run in parallel without mutating package-level state.
type Runner struct {
	Claude ClaudeAuditor
	GitHub GitHubClient
}

// NewRunner returns a Runner wired with the production GitHub (git/gh CLI)
// backend and the given Claude audit function.
func NewRunner(auditFn AuditFn) *Runner {
	return &Runner{
		Claude: auditFnAdapter{fn: auditFn},
		GitHub: defaultGitHubClient{},
	}
}

// Run is a package-level convenience that delegates to NewRunner(auditFn).Run.
// Callers that need to inject alternative Claude or GitHub backends should
// construct a Runner directly.
func Run(w io.Writer, opts Options, auditFn AuditFn) error {
	return NewRunner(auditFn).Run(w, opts)
}

// Run executes the full audit pipeline: resolve HEAD SHA, check cache, and on
// a miss clone the repo, detect tech, load patterns, run Claude, cache, and
// render. Cache hits skip the clone entirely so CI loops against the same
// commit don't pay the clone cost.
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

	// Build cache key (includes min-severity so filtered caches don't leak).
	var cacheFlags []string
	if opts.MinSeverity != "" {
		cacheFlags = append(cacheFlags, "min="+string(opts.MinSeverity))
	}
	cacheKey := cache.AuditKey(owner, name, headSHA, cacheFlags...)

	if !opts.NoCache && headSHA != "" {
		if data, ok := cache.GetRaw(cacheKey, opts.CacheMaxAge); ok {
			var result report.ReviewResult
			if err := json.Unmarshal(data, &result); err == nil {
				slog.Info("using cached audit result — skipping clone", "repo", opts.RepoRef)
				r.applyIssueDedupe(&result, owner, name, opts)
				return renderAudit(w, &result, &github.Repo{Owner: owner, Name: name}, opts)
			}
			slog.Warn("cache corrupted, running fresh audit")
		}
	}

	slog.Info("cloning repository", "repo", opts.RepoRef)
	repo, err := r.GitHub.CloneRepo(opts.RepoRef)
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
	if len(pats) == 0 {
		return fmt.Errorf("no review patterns loaded — nothing to audit against")
	}
	slog.Info("loaded review patterns", "count", len(pats))

	slog.Info("auditing codebase with Claude")
	result, err := r.Claude.Audit(repo.Dir, AuditContext{
		Patterns:    pats,
		MaxPatterns: opts.MaxPatterns,
		MaxFindings: opts.MaxFindings,
		RepoName:    repo.FullName(),
	})
	if err != nil {
		return fmt.Errorf("claude audit: %w", err)
	}

	if !opts.NoCache && headSHA != "" {
		if data, err := json.Marshal(result); err == nil {
			if err := cache.PutRaw(cacheKey, cache.CommandAudit, data); err != nil {
				slog.Warn("could not cache result", "err", err)
			}
		}
	}

	slog.Info("audit complete")
	r.applyIssueDedupe(result, repo.Owner, repo.Name, opts)
	return renderAudit(w, result, repo, opts)
}

// applyIssueDedupe filters out findings whose grouped issue-candidate title
// matches an existing GitHub issue (open or closed) in the target repo. Runs
// after the cache layer so cached Claude output stays faithful to what Claude
// returned — issue state changes take effect on every run without invalidating
// the cache. A lister error logs a warning and skips dedupe rather than
// failing the run.
func (r *Runner) applyIssueDedupe(result *report.ReviewResult, owner, name string, opts Options) {
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

	groups := GroupFindings(result.Findings)
	dropKeys := make(map[string]struct{})
	for _, g := range groups {
		if match, ok := idx.Lookup(buildGroupTitle(g)); ok {
			slog.Debug("skipping finding group already tracked by an existing issue",
				"title", buildGroupTitle(g), "existing", match.URL)
			dropKeys[g.Key] = struct{}{}
		}
	}
	if len(dropKeys) == 0 {
		return
	}

	before := len(result.Findings)
	kept := make([]report.Finding, 0, before)
	for _, f := range result.Findings {
		if _, drop := dropKeys[findingGroupKey(f)]; drop {
			continue
		}
		kept = append(kept, f)
	}
	result.Findings = kept
	slog.Info("filtered findings with existing issues",
		"groups_filtered", len(dropKeys),
		"findings_filtered", before-len(kept),
		"findings_kept", len(kept))
}

// findingGroupKey replicates the grouping key used by GroupFindings so the
// dedupe pass can map individual findings back to the group that produced the
// candidate title. Keep these two definitions in sync.
func findingGroupKey(f report.Finding) string {
	pattern := f.Pattern
	if pattern == "" {
		pattern = f.Title
	}
	return pattern + "|" + f.File
}

// collectPatternDirs assembles the list of pattern directories to load from,
// honoring the --no-local-patterns and --no-repo-patterns flags and appending
// any explicit --patterns directories supplied by the caller.
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

func renderAudit(w io.Writer, result *report.ReviewResult, repo *github.Repo, opts Options) error {
	renderer := report.NewRenderer(w)
	repoInfo := report.RepoInfo{
		Owner: repo.Owner,
		Name:  repo.Name,
	}

	switch opts.Format {
	case "json":
		return renderer.RenderJSON(*result, opts.MinSeverity, opts.MinConfidence)
	default:
		renderer.RenderAuditMarkdown(*result, repoInfo, opts.MinSeverity, opts.MinConfidence, opts.Version)
	}

	if opts.CreateIssues {
		return RunInteractiveIssueCreation(w, os.Stdin, result, repo.Owner, repo.Name, opts.IssueMinSeverity)
	}
	return nil
}
