package gapanalysis

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"sort"
	"strings"

	"github.com/planwerk/planwerk-review/internal/cache"
	"github.com/planwerk/planwerk-review/internal/detect"
	"github.com/planwerk/planwerk-review/internal/github"
	"github.com/planwerk/planwerk-review/internal/patterns"
	"github.com/planwerk/planwerk-review/internal/report"
	"github.com/planwerk/planwerk-review/internal/workspace"
)

// Runner executes the gap-analysis pipeline using injected Claude and GitHub
// clients.
type Runner struct {
	Claude ClaudeGapAnalyzer
	GitHub GitHubClient
}

// NewRunner returns a Runner wired with the production GitHub backend.
func NewRunner(analyzeFn AnalyzeFn) *Runner {
	return &Runner{
		Claude: analyzeFnAdapter{fn: analyzeFn},
		GitHub: defaultGitHubClient{},
	}
}

// Run is a package-level convenience that delegates to NewRunner(analyzeFn).Run.
func Run(w io.Writer, opts Options, analyzeFn AnalyzeFn) error {
	return NewRunner(analyzeFn).Run(w, opts)
}

// Run executes the gap-analysis pipeline: parse the repo ref, resolve the
// HEAD SHA for cache keying, hit the cache or clone+analyze, then render. The
// flow mirrors audit.Runner.Run so caching, dedupe, and pattern semantics stay
// consistent across commands.
func (r *Runner) Run(w io.Writer, opts Options) error {
	owner, name, err := github.ParseRepoRef(opts.RepoRef)
	if err != nil {
		return fmt.Errorf("parsing repo ref: %w", err)
	}

	headSHA, err := r.GitHub.DefaultBranchHEAD(owner, name)
	if err != nil {
		slog.Warn("could not fetch HEAD SHA, caching disabled", "err", err)
		opts.NoCache = true
		headSHA = ""
	}

	cacheKey := buildCacheKey(owner, name, headSHA, opts)
	if !opts.NoCache && headSHA != "" {
		if data, ok := cache.GetRaw(cacheKey, opts.CacheMaxAge); ok {
			var result Result
			if err := json.Unmarshal(data, &result); err == nil {
				slog.Info("using cached gap-analysis result — skipping clone", "repo", opts.RepoRef)
				r.applyIssueDedupe(&result, owner, name, opts)
				return render(w, &result, &github.Repo{Owner: owner, Name: name}, opts)
			}
			slog.Warn("cache corrupted, running fresh gap analysis")
		}
	}

	repo, err := r.openRepo(opts)
	if err != nil {
		return fmt.Errorf("cloning repo: %w", err)
	}
	defer repo.Cleanup()

	slog.Info("cloned repository", "dir", repo.Dir)

	features, err := LoadCompletedFeatures(repo.Dir, opts.FeatureID, opts.FilePath)
	if err != nil {
		return fmt.Errorf("loading completed features: %w", err)
	}
	slog.Info("loaded completed features", "count", len(features))

	techTags := detect.Technologies(repo.Dir)
	if len(techTags) > 0 {
		slog.Info("detected technologies", "technologies", strings.Join(techTags, ", "))
	}

	patternDirs, err := patterns.Resolve(patterns.ResolveOptions{
		NoLocal: opts.NoLocalPatterns,
		NoRepo:  opts.NoRepoPatterns,
		RepoDir: repo.Dir,
		Extra:   opts.PatternDirs,
	})
	if err != nil {
		return fmt.Errorf("resolving pattern sources: %w", err)
	}
	pats, err := patterns.LoadFilteredWithOptions(patterns.LoadOptions{Remote: opts.Remote, NoEmbedded: opts.NoLocalPatterns}, techTags, patternDirs...)
	if err != nil {
		return fmt.Errorf("loading patterns: %w", err)
	}
	if len(pats) > 0 {
		slog.Info("loaded review patterns", "count", len(pats))
	}

	slog.Info("running gap analysis with Claude")
	result, err := r.Claude.GapAnalysis(repo.Dir, AnalysisContext{
		Features:    features,
		Patterns:    pats,
		MaxPatterns: opts.MaxPatterns,
		RepoName:    repo.FullName(),
	})
	if err != nil {
		return fmt.Errorf("claude gap analysis: %w", err)
	}
	if result.RepoFullName == "" {
		result.RepoFullName = repo.FullName()
	}
	assignIDs(result)

	if !opts.NoCache && headSHA != "" {
		if data, err := json.Marshal(result); err == nil {
			if err := cache.PutRaw(cacheKey, CommandGapAnalysis, data); err != nil {
				slog.Warn("could not cache result", "err", err)
			}
		}
	}

	slog.Info("gap analysis complete")
	r.applyIssueDedupe(result, repo.Owner, repo.Name, opts)
	return render(w, result, repo, opts)
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

// buildCacheKey hashes owner/repo, the HEAD SHA, and any filter that narrows
// what was analyzed. A different filter must produce a different key so a
// single-feature run never overwrites a full-repo result.
func buildCacheKey(owner, name, headSHA string, opts Options) string {
	input := fmt.Sprintf("gap-analysis:%s/%s@%s", owner, name, headSHA)
	if opts.FeatureID != "" {
		input += "+feature=" + strings.ToUpper(opts.FeatureID)
	}
	if opts.FilePath != "" {
		input += "+file=" + filepath.Base(opts.FilePath)
	}
	h := sha256.Sum256([]byte(input))
	return fmt.Sprintf("%x", h[:16])
}

// applyIssueDedupe drops gaps whose suggested-issue title already exists as a
// GitHub issue. Mirrors audit.Runner.applyIssueDedupe so behavior stays
// predictable across commands.
func (r *Runner) applyIssueDedupe(result *Result, owner, name string, opts Options) {
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

	beforeTotal := 0
	afterTotal := 0
	for fi := range result.Features {
		fg := &result.Features[fi]
		beforeTotal += len(fg.Gaps)
		kept := make([]Gap, 0, len(fg.Gaps))
		for _, g := range fg.Gaps {
			title := g.Suggested.Title
			if title == "" {
				title = g.Title
			}
			if match, ok := idx.Lookup(title); ok {
				slog.Debug("skipping gap already tracked by an existing issue",
					"title", title, "existing", match.URL)
				continue
			}
			kept = append(kept, g)
		}
		fg.Gaps = kept
		afterTotal += len(kept)
	}
	if dropped := beforeTotal - afterTotal; dropped > 0 {
		slog.Info("filtered gaps with existing issues",
			"dropped", dropped, "kept", afterTotal)
	}
}

// assignIDs gives every gap a stable, severity-prefixed ID for renderers and
// downstream tooling. Mirrors claude.assignIDs but operates on Gap.
func assignIDs(result *Result) {
	if result == nil {
		return
	}
	prefixes := map[report.Severity]string{
		report.SeverityBlocking: "B",
		report.SeverityCritical: "C",
		report.SeverityWarning:  "W",
		report.SeverityInfo:     "I",
	}
	counters := map[report.Severity]int{}
	// Iterate in deterministic order so re-runs produce identical IDs even
	// when Claude's slice order shifts run-to-run.
	sort.SliceStable(result.Features, func(i, j int) bool {
		return result.Features[i].FeatureID < result.Features[j].FeatureID
	})
	for fi := range result.Features {
		for gi := range result.Features[fi].Gaps {
			g := &result.Features[fi].Gaps[gi]
			sev := report.Severity(strings.ToUpper(string(g.Severity)))
			if _, ok := prefixes[sev]; !ok {
				sev = report.SeverityWarning
			}
			g.Severity = sev
			counters[sev]++
			g.ID = fmt.Sprintf("%s-%03d", prefixes[sev], counters[sev])
		}
	}
}
