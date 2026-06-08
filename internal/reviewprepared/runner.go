package reviewprepared

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
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

// DefaultPRBranch is the head branch used when --pr-branch is not set.
const DefaultPRBranch = "planwerk-review/improve-prepared-features"

// Runner executes the review-prepared pipeline using injected Claude and
// GitHub clients. Same shape as gapanalysis.Runner so the surrounding code
// reads as a family.
type Runner struct {
	Claude ClaudeReviewer
	GitHub GitHubClient
}

// NewRunner returns a Runner wired with the production GitHub backend.
func NewRunner(fn AnalyzeFn) *Runner {
	return &Runner{
		Claude: analyzeFnAdapter{fn: fn},
		GitHub: defaultGitHubClient{},
	}
}

// Run is a package-level convenience that delegates to NewRunner(fn).Run.
func Run(w io.Writer, opts Options, fn AnalyzeFn) error {
	return NewRunner(fn).Run(w, opts)
}

// Run executes the review-prepared pipeline:
//  1. Resolve the repo ref and HEAD SHA (for cache keying).
//  2. Hit the cache or clone + load patterns + invoke Claude.
//  3. Render the review (markdown/json).
//  4. When CreatePR is set AND any feature has improvements, open a PR.
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
	var result *Result
	if !opts.NoCache && headSHA != "" && !opts.CreatePR {
		// We never serve a cached result when CreatePR is set: the cache
		// envelope might predate the include-improved flag and would lack the
		// rewritten JSON payload, leaving us with nothing to push.
		if data, ok := cache.GetRaw(cacheKey, opts.CacheMaxAge); ok {
			var cached Result
			if err := json.Unmarshal(data, &cached); err == nil {
				slog.Info("using cached review-prepared result — skipping clone", "repo", opts.RepoRef)
				result = &cached
			} else {
				slog.Warn("cache corrupted, running fresh review")
			}
		}
	}

	var repo *github.Repo
	if result == nil {
		repo, err = r.openRepo(opts)
		if err != nil {
			return fmt.Errorf("cloning repo: %w", err)
		}
		// Defer cleanup only when we are NOT opening a PR — the PR helper
		// needs the working tree to commit + push.
		if !opts.CreatePR {
			defer repo.Cleanup()
		}

		features, err := LoadPreparedFeatures(repo.Dir, opts.FeatureID, opts.FilePath)
		if err != nil {
			repo.Cleanup()
			return fmt.Errorf("loading prepared features: %w", err)
		}
		slog.Info("loaded prepared features", "count", len(features))

		techTags := detect.Technologies(repo.Dir)
		if len(techTags) > 0 {
			slog.Info("detected technologies", "technologies", strings.Join(techTags, ", "))
		}

		patternDirs := collectPatternDirs(opts, repo.Dir)
		pats, err := patterns.LoadFilteredWithOptions(patterns.LoadOptions{Remote: patterns.RemoteOpts(), NoEmbedded: opts.NoLocalPatterns}, techTags, patternDirs...)
		if err != nil {
			repo.Cleanup()
			return fmt.Errorf("loading patterns: %w", err)
		}
		if len(pats) > 0 {
			slog.Info("loaded review patterns", "count", len(pats))
		}

		slog.Info("running review-prepared with Claude", "create_pr", opts.CreatePR)
		result, err = r.Claude.ReviewPrepared(repo.Dir, AnalysisContext{
			Features:        features,
			Patterns:        pats,
			MaxPatterns:     opts.MaxPatterns,
			RepoName:        repo.FullName(),
			IncludeImproved: opts.CreatePR,
		})
		if err != nil {
			repo.Cleanup()
			return fmt.Errorf("claude review-prepared: %w", err)
		}
		if result.RepoFullName == "" {
			result.RepoFullName = repo.FullName()
		}
		assignIDs(result)

		if !opts.NoCache && headSHA != "" && !opts.CreatePR {
			if data, err := json.Marshal(result); err == nil {
				if err := cache.PutRaw(cacheKey, CommandReviewPrepared, data); err != nil {
					slog.Warn("could not cache result", "err", err)
				}
			}
		}
	}

	filterBySeverity(result, opts.MinSeverity)

	if err := render(w, result, opts.Version, opts.Format); err != nil {
		return err
	}

	if opts.CreatePR {
		if repo == nil {
			return fmt.Errorf("--create-pr requires a fresh clone but none is available; rerun with --no-cache")
		}
		defer repo.Cleanup()
		url, err := r.openPR(repo, result, opts)
		if err != nil {
			return err
		}
		if url != "" {
			_, _ = fmt.Fprintf(w, "\nOpened pull request: %s\n", url)
		}
	}

	slog.Info("review-prepared complete")
	return nil
}

// openRepo returns the working tree to review: the user's cwd when --local is
// set (no clone, Cleanup is a no-op), otherwise a fresh temp-dir clone. The
// --create-pr path is compatible because the cwd is already a working branch
// the openPR helper can commit and push from.
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

// buildCacheKey hashes owner/repo, HEAD SHA, and any narrowing filter so a
// single-feature run never overwrites a full-directory result.
func buildCacheKey(owner, name, headSHA string, opts Options) string {
	input := fmt.Sprintf("review-prepared:%s/%s@%s", owner, name, headSHA)
	if opts.FeatureID != "" {
		input += "+feature=" + strings.ToUpper(opts.FeatureID)
	}
	if opts.FilePath != "" {
		input += "+file=" + filepath.Base(opts.FilePath)
	}
	h := sha256.Sum256([]byte(input))
	return fmt.Sprintf("%x", h[:16])
}

// assignIDs gives each finding a stable, severity-prefixed ID. Mirrors
// gapanalysis.assignIDs / claude.assignIDs.
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
	sort.SliceStable(result.Features, func(i, j int) bool {
		return result.Features[i].FeatureID < result.Features[j].FeatureID
	})
	for fi := range result.Features {
		for ji := range result.Features[fi].Findings {
			f := &result.Features[fi].Findings[ji]
			sev := report.Severity(strings.ToUpper(string(f.Severity)))
			if _, ok := prefixes[sev]; !ok {
				sev = report.SeverityWarning
			}
			f.Severity = sev
			counters[sev]++
			f.ID = fmt.Sprintf("%s-%03d", prefixes[sev], counters[sev])
		}
	}
}

// filterBySeverity drops findings below the configured threshold. Empty
// minSeverity keeps everything.
func filterBySeverity(result *Result, minSeverity report.Severity) {
	if result == nil || minSeverity == "" {
		return
	}
	rank := map[report.Severity]int{
		report.SeverityInfo:     0,
		report.SeverityWarning:  1,
		report.SeverityCritical: 2,
		report.SeverityBlocking: 3,
	}
	threshold, ok := rank[minSeverity]
	if !ok {
		return
	}
	for fi := range result.Features {
		kept := result.Features[fi].Findings[:0]
		for _, f := range result.Features[fi].Findings {
			if rank[f.Severity] >= threshold {
				kept = append(kept, f)
			}
		}
		result.Features[fi].Findings = kept
	}
}

// openPR collects every feature that came back with a non-empty improved JSON
// and asks the GitHub client to commit + push + open a PR. Returns the PR
// URL (or empty + nil if there were no changes to push).
func (r *Runner) openPR(repo *github.Repo, result *Result, opts Options) (string, error) {
	files := make([]ImprovedFile, 0, len(result.Features))
	improved := make([]string, 0, len(result.Features))
	for _, fr := range result.Features {
		if len(fr.ImprovedJSON) == 0 {
			continue
		}
		rel := filepath.Join(".planwerk", preparedSubdir, fr.FeatureFile)
		formatted, err := indentJSON(fr.ImprovedJSON)
		if err != nil {
			return "", fmt.Errorf("formatting improved JSON for %s: %w", fr.FeatureID, err)
		}
		files = append(files, ImprovedFile{
			RelativePath: rel,
			Content:      ensureTrailingNewline(formatted),
		})
		improved = append(improved, fr.FeatureID)
	}
	if len(files) == 0 {
		slog.Info("no spec improvements to commit — skipping PR")
		return "", nil
	}

	branch := opts.PRBranch
	if branch == "" {
		branch = DefaultPRBranch
	}

	title := fmt.Sprintf("Improve prepared feature spec(s): %s", strings.Join(improved, ", "))
	if len(improved) > 3 {
		title = fmt.Sprintf("Improve %d prepared feature specs", len(improved))
	}
	commit := title + "\n\nGenerated by planwerk-review review-prepared."

	body := buildPRBody(result, opts.Version)

	url, err := r.GitHub.OpenImprovementPR(repo, PROptions{
		Branch: branch,
		Base:   opts.PRBase,
		Title:  title,
		Body:   body,
		Commit: commit,
		Files:  files,
	})
	if err != nil {
		return "", fmt.Errorf("opening PR: %w", err)
	}
	return url, nil
}

// ensureTrailingNewline keeps the JSON files POSIX-friendly: a final \n
// matches what most editors and pre-commit hooks expect.
func ensureTrailingNewline(b []byte) []byte {
	if len(b) == 0 || b[len(b)-1] == '\n' {
		return b
	}
	out := make([]byte, len(b)+1)
	copy(out, b)
	out[len(b)] = '\n'
	return out
}

// indentJSON pretty-prints raw JSON with two-space indentation, matching the
// formatting Planwerk feature files use on disk so the resulting diff is
// limited to actual content changes, not whitespace.
func indentJSON(raw json.RawMessage) ([]byte, error) {
	var buf bytes.Buffer
	if err := json.Indent(&buf, raw, "", "  "); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// collectPatternDirs assembles the list of pattern directories to load from.
// Mirrors the helpers in gapanalysis / audit / propose.
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

// buildPRBody assembles the Markdown body for the improvement PR — a
// per-feature summary plus the finding table the CLI would render to stdout.
func buildPRBody(result *Result, version string) string {
	var sb strings.Builder
	sb.WriteString("This PR rewrites one or more prepared Planwerk feature specifications based on a quality review.\n\n")
	if version != "" {
		fmt.Fprintf(&sb, "_Generated by planwerk-review %s — review-prepared_\n\n", version)
	}
	if result == nil {
		return sb.String()
	}
	if result.Overview != "" {
		sb.WriteString("## Overview\n\n")
		sb.WriteString(strings.TrimSpace(result.Overview))
		sb.WriteString("\n\n")
	}
	for _, fr := range result.Features {
		if len(fr.ImprovedJSON) == 0 {
			continue
		}
		fmt.Fprintf(&sb, "## %s — %s\n\n", fr.FeatureID, fr.Title)
		if fr.Summary != "" {
			sb.WriteString(strings.TrimSpace(fr.Summary))
			sb.WriteString("\n\n")
		}
		if len(fr.Findings) > 0 {
			sb.WriteString("Findings addressed by this rewrite:\n\n")
			for _, f := range fr.Findings {
				fmt.Fprintf(&sb, "- **%s** [%s/%s] %s\n",
					strings.ToUpper(string(f.Severity)), f.ID, f.Category, f.Title)
			}
			sb.WriteString("\n")
		}
	}
	sb.WriteString("---\n\n")
	sb.WriteString("Review the diff carefully — Claude rewrote the JSON in full, so unchanged sections may show up as no-ops only because the formatter normalised them.\n")
	return sb.String()
}
