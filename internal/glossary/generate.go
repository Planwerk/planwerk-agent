package glossary

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/planwerk/planwerk-review/internal/cache"
	"github.com/planwerk/planwerk-review/internal/github"
	"github.com/planwerk/planwerk-review/internal/workspace"
)

// Options configures the glossary-generation pipeline.
type Options struct {
	RepoRef     string
	NoCache     bool
	CacheMaxAge time.Duration // reject cache entries older than this; <= 0 disables the TTL
	Local       bool          // operate on the current working directory instead of cloning
	Force       bool          // with Local, skip the dirty-working-tree confirmation prompt
}

// Runner executes the glossary pipeline using injected Claude and GitHub
// clients. Constructing a Runner per invocation keeps dependencies explicit and
// lets tests run in parallel without mutating package-level state.
type Runner struct {
	Claude GlossaryGenerator
	GitHub GitHubClient
}

// NewRunner returns a Runner wired with the production GitHub (git/gh CLI)
// backend and the given Claude generate function.
func NewRunner(generateFn GenerateFn) *Runner {
	return &Runner{
		Claude: generateFnAdapter{fn: generateFn},
		GitHub: defaultGitHubClient{},
	}
}

// Run is a package-level convenience that delegates to NewRunner(generateFn).Run.
func Run(w io.Writer, opts Options, generateFn GenerateFn) error {
	return NewRunner(generateFn).Run(w, opts)
}

// Run executes the full glossary pipeline: resolve HEAD SHA, check cache, and on
// a miss clone the repo, generate the CONTEXT.md, cache it, and write it to w. A
// cache hit skips the clone entirely. The CONTEXT.md is written clean (no
// attribution footer) so it can be redirected straight into a repo's CONTEXT.md;
// the token-usage summary goes to stderr via the shared Claude client.
func (r *Runner) Run(w io.Writer, opts Options) error {
	owner, name, err := github.ParseRepoRef(opts.RepoRef)
	if err != nil {
		return fmt.Errorf("parsing repo ref: %w", err)
	}

	// Resolve HEAD SHA before cloning so a cache hit can short-circuit the
	// clone entirely. A failure disables caching rather than failing the run.
	headSHA, err := r.GitHub.DefaultBranchHEAD(owner, name)
	if err != nil {
		slog.Warn("could not fetch HEAD SHA, caching disabled", "err", err)
		opts.NoCache = true
		headSHA = ""
	}

	cacheKey := cache.GlossaryKey(owner, name, headSHA)
	if !opts.NoCache && headSHA != "" {
		if data, ok := cache.GetRaw(cacheKey, opts.CacheMaxAge); ok {
			var markdown string
			if err := json.Unmarshal(data, &markdown); err == nil {
				slog.Info("using cached glossary — skipping clone", "repo", opts.RepoRef)
				return writeGlossary(w, markdown)
			}
			slog.Warn("cache corrupted, regenerating glossary")
		}
	}

	repo, err := r.openRepo(opts)
	if err != nil {
		return fmt.Errorf("cloning repo: %w", err)
	}
	defer repo.Cleanup()

	slog.Info("generating domain glossary with Claude", "dir", repo.Dir)
	markdown, _, err := r.Claude.GenerateGlossary(repo.Dir, GenerateContext{RepoName: repo.FullName()})
	if err != nil {
		return fmt.Errorf("claude glossary generation: %w", err)
	}

	// Reject a degenerate generation BEFORE caching or emitting it. A transient
	// empty or fence-only response sanitizes down to "" or a heading-less
	// fragment; caching that under the HEAD-SHA key would poison every later run
	// on this commit for the full TTL. Failing loud forces a regenerate instead.
	if doc := strings.TrimSpace(markdown); doc == "" || !strings.HasPrefix(doc, "# ") {
		return fmt.Errorf("glossary generation produced no CONTEXT.md document")
	}

	if !opts.NoCache && headSHA != "" {
		// The cache envelope stores its payload as JSON, so the Markdown is
		// JSON-encoded as a string rather than written as raw bytes.
		if data, err := json.Marshal(markdown); err == nil {
			if err := cache.PutRaw(cacheKey, cache.CommandGlossary, data); err != nil {
				slog.Warn("could not cache glossary", "err", err)
			}
		}
	}

	slog.Info("glossary generation complete")
	return writeGlossary(w, markdown)
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

// writeGlossary writes the CONTEXT.md to w with exactly one trailing newline, so
// the cache-hit and fresh paths emit byte-identical output and a redirected file
// ends cleanly.
func writeGlossary(w io.Writer, markdown string) error {
	if _, err := io.WriteString(w, strings.TrimRight(markdown, "\n")+"\n"); err != nil {
		return fmt.Errorf("writing glossary: %w", err)
	}
	return nil
}
