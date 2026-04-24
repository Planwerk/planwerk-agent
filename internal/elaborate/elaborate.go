package elaborate

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
)

// CommandElaborate is the cache scope identifier for elaboration entries.
const CommandElaborate = "elaborate"

// UpdateMode controls how the rendered body is written back to GitHub.
type UpdateMode int

const (
	// UpdateNone leaves the issue untouched and only writes to the local writer.
	UpdateNone UpdateMode = iota
	// UpdateReplace overwrites the issue body with the elaborated body.
	UpdateReplace
	// UpdateComment posts the elaborated body as a new comment on the issue.
	UpdateComment
)

// Options configures the elaboration pipeline.
type Options struct {
	IssueRef        string
	PatternDirs     []string
	NoRepoPatterns  bool
	NoLocalPatterns bool
	NoCache         bool
	Format          string // "markdown" or "json"
	Version         string
	MaxPatterns     int
	UpdateMode      UpdateMode
	CacheMaxAge     time.Duration
}

// Runner executes the elaborate pipeline using injected Claude and GitHub
// clients.
type Runner struct {
	Claude ClaudeElaborator
	GitHub GitHubClient
}

// NewRunner returns a Runner wired with the production GitHub backend and
// the given Claude elaborate function.
func NewRunner(fn ElaborateFn) *Runner {
	return &Runner{
		Claude: elaborateFnAdapter{fn: fn},
		GitHub: defaultGitHubClient{},
	}
}

// Run is a package-level convenience that delegates to NewRunner(fn).Run.
func Run(w io.Writer, opts Options, fn ElaborateFn) error {
	return NewRunner(fn).Run(w, opts)
}

// Run executes the full elaboration pipeline: parse the issue ref, fetch
// the issue, resolve HEAD, check the cache, on miss clone the repo, load
// patterns, ask Claude to elaborate, render and (optionally) write the
// elaborated body back to GitHub.
func (r *Runner) Run(w io.Writer, opts Options) error {
	owner, name, number, err := github.ParseIssueRef(opts.IssueRef)
	if err != nil {
		return fmt.Errorf("parsing issue ref: %w", err)
	}

	issue, err := r.GitHub.GetIssue(owner, name, number)
	if err != nil {
		return fmt.Errorf("fetching issue: %w", err)
	}
	slog.Info("fetched issue", "repo", fmt.Sprintf("%s/%s", owner, name), "issue", number, "title", issue.Title)

	headSHA, err := r.GitHub.DefaultBranchHEAD(owner, name)
	if err != nil {
		slog.Warn("could not fetch HEAD SHA, caching disabled", "err", err)
		opts.NoCache = true
		headSHA = ""
	}

	cacheKey := elaborateCacheKey(owner, name, number, issue, headSHA)

	if !opts.NoCache && headSHA != "" {
		if data, ok := cache.GetRaw(cacheKey, opts.CacheMaxAge); ok {
			var result Result
			if err := json.Unmarshal(data, &result); err == nil {
				slog.Info("using cached elaboration — skipping clone", "issue", number)
				return r.finish(w, &result, owner, name, number, opts)
			}
			slog.Warn("cache corrupted, running fresh elaboration")
		}
	}

	slog.Info("cloning repository for elaboration", "repo", fmt.Sprintf("%s/%s", owner, name))
	repo, err := r.GitHub.CloneRepo(fmt.Sprintf("%s/%s", owner, name))
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
		slog.Warn("no review patterns loaded — elaboration will not be grounded in the pattern catalog")
	}

	slog.Info("elaborating issue with Claude")
	result, err := r.Claude.Elaborate(repo.Dir, Context{
		Patterns:    pats,
		MaxPatterns: opts.MaxPatterns,
		RepoName:    repo.FullName(),
		Issue:       issue,
	})
	if err != nil {
		return fmt.Errorf("claude elaborate: %w", err)
	}
	if result.Title == "" {
		result.Title = issue.Title
	}
	result.Body = BuildIssueBody(result)

	if !opts.NoCache && headSHA != "" {
		if data, err := json.Marshal(result); err == nil {
			if err := cache.PutRaw(cacheKey, CommandElaborate, data); err != nil {
				slog.Warn("could not cache result", "err", err)
			}
		}
	}

	slog.Info("elaboration complete")
	return r.finish(w, result, owner, name, number, opts)
}

// finish renders the elaborated result and applies the configured update
// mode to the upstream issue.
func (r *Runner) finish(w io.Writer, result *Result, owner, name string, number int, opts Options) error {
	switch opts.Format {
	case "json":
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		if err := enc.Encode(result); err != nil {
			return err
		}
	default:
		RenderMarkdown(w, fmt.Sprintf("%s/%s", owner, name), number, opts.Version, result)
	}

	switch opts.UpdateMode {
	case UpdateReplace:
		if err := r.GitHub.EditIssueBody(owner, name, number, result.Body); err != nil {
			return fmt.Errorf("updating issue body: %w", err)
		}
		slog.Info("updated issue body", "issue", number)
	case UpdateComment:
		url, err := r.GitHub.AddIssueComment(owner, name, number, result.Body)
		if err != nil {
			return fmt.Errorf("posting issue comment: %w", err)
		}
		slog.Info("posted elaboration comment", "issue", number, "url", url)
	}
	return nil
}

// elaborateCacheKey scopes the cache by repo, issue number, head SHA, and a
// short fingerprint of the issue body so the cache invalidates whenever the
// upstream issue is edited or the repo head moves.
func elaborateCacheKey(owner, name string, number int, issue *github.Issue, headSHA string) string {
	flags := []string{
		fmt.Sprintf("issue=%d", number),
		"body=" + issueFingerprint(issue),
	}
	return cache.AuditKey(owner, name, "elaborate@"+headSHA, flags...)
}

// issueFingerprint returns a stable short hash of the issue's title+body so
// cache entries invalidate when the upstream issue is edited.
func issueFingerprint(issue *github.Issue) string {
	if issue == nil {
		return ""
	}
	return shortHash(issue.Title + "\n" + issue.Body)
}

// collectPatternDirs mirrors the audit/propose helper of the same name: it
// folds in local + repo + explicit pattern directories per the
// --no-local-patterns / --no-repo-patterns toggles.
func collectPatternDirs(opts Options, repoDir string) []string {
	var dirs []string
	if !opts.NoLocalPatterns {
		if exe, err := os.Executable(); err == nil {
			localPatterns := filepath.Join(filepath.Dir(exe), "..", "patterns")
			if info, err := os.Stat(localPatterns); err == nil && info.IsDir() {
				dirs = append(dirs, localPatterns)
			}
		}
		if info, err := os.Stat("patterns"); err == nil && info.IsDir() {
			dirs = append(dirs, "patterns")
		}
	}
	if !opts.NoRepoPatterns {
		repoPatterns := filepath.Join(repoDir, ".planwerk", "review_patterns")
		if info, err := os.Stat(repoPatterns); err == nil && info.IsDir() {
			dirs = append(dirs, repoPatterns)
		}
	}
	dirs = append(dirs, opts.PatternDirs...)
	return dirs
}
