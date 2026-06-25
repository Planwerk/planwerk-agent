package audit

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/planwerk/planwerk-review/internal/cache"
	"github.com/planwerk/planwerk-review/internal/capture"
	"github.com/planwerk/planwerk-review/internal/detect"
	"github.com/planwerk/planwerk-review/internal/github"
	"github.com/planwerk/planwerk-review/internal/patterns"
	"github.com/planwerk/planwerk-review/internal/report"
	"github.com/planwerk/planwerk-review/internal/workspace"
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
	Local            bool            // operate on the current working directory instead of cloning
	Force            bool            // with Local, skip the dirty-working-tree confirmation prompt
	// Remote configures how remote pattern URIs (--patterns github:..., git+...)
	// resolve into local directories; carries the --remote-patterns-ttl value.
	Remote patterns.RemoteOptions
	// Wiki configures the target repo's GitHub Wiki as a knowledge source
	// (review patterns + project memory); carries the --wiki/--no-wiki/--wiki-ref
	// values.
	Wiki patterns.WikiOptions
	// NoCapture disables the read-only capture pass that, after the audit,
	// proposes new wiki review patterns from the audit findings — writing
	// nothing. On by default but only when a wiki is resolved (i.e. with --wiki);
	// without one it is a no-op regardless of this flag.
	NoCapture bool
	// CaptureWiki gates the capture write-back: with it set, the capture pass
	// pushes the accepted proposal pages to the wiki. Default off keeps a run
	// propose-only.
	CaptureWiki bool
	// Yes skips the capture write-back's interactive confirmation, for a
	// non-interactive (CI) --capture-wiki run.
	Yes bool
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
	// Memory is the target repo's project memory from its GitHub Wiki; empty
	// when the repo has no wiki memory.
	Memory string
}

// Runner executes the audit pipeline using injected Claude and GitHub
// clients. Constructing a Runner per invocation keeps dependencies explicit
// and allows tests to run in parallel without mutating package-level state.
type Runner struct {
	Claude ClaudeAuditor
	GitHub GitHubClient
	// Capturer runs the read-only capture pass after the audit: it proposes new
	// wiki review patterns from the audit findings, writing nothing. Set by
	// NewRunner; nil (or a wiki that did not resolve, or opts.NoCapture) leaves
	// the pass disabled.
	Capturer capture.Proposer
	// ResolveWiki resolves the target repo's wiki. Defaults to
	// patterns.ResolveWiki; a Runner seam so the capture pass can be exercised
	// against a temp wiki without cloning a real one.
	ResolveWiki resolveWikiFn
	// CaptureWriter performs the gated capture write-back. Defaults to
	// capture.DefaultWikiWriter; a Runner seam so the write-back can be exercised
	// without cloning or pushing a real wiki.
	CaptureWriter capture.WikiWriter
	// In is the stream the capture write-back's confirmation reads from. Defaults
	// to os.Stdin.
	In io.Reader
	// IsTTY reports whether the capture write-back may prompt interactively.
	// Defaults to workspace.IsStdinTTY.
	IsTTY func() bool
}

// resolveWikiFn resolves the target repo's wiki. A Runner seam so the capture
// pass can be exercised against a temp wiki without cloning a real one. Mirrors
// implement.resolveWikiFn.
type resolveWikiFn func(owner, name string, wopts patterns.WikiOptions, ropts patterns.RemoteOptions) patterns.ResolvedWiki

// NewRunner returns a Runner wired with the production GitHub (git/gh CLI)
// backend, the given Claude audit function, and the proposer that backs the
// read-only capture pass (the Claude client). A nil proposer leaves capture
// disabled.
func NewRunner(auditFn AuditFn, proposer capture.Proposer) *Runner {
	return &Runner{
		Claude:   auditFnAdapter{fn: auditFn},
		GitHub:   defaultGitHubClient{},
		Capturer: proposer,
	}
}

// Run is a package-level convenience that delegates to
// NewRunner(auditFn, proposer).Run. Callers that need to inject alternative
// Claude or GitHub backends should construct a Runner directly.
func Run(w io.Writer, opts Options, auditFn AuditFn, proposer capture.Proposer) error {
	return NewRunner(auditFn, proposer).Run(w, opts)
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

	// Resolve the target repo's GitHub Wiki (best-effort) before the cache key,
	// so the resolved wiki commit folds into the key and can be recorded in the
	// report header. An absent, disabled, or offline wiki returns the zero value
	// and leaves the run unchanged. The seam (defaulting to patterns.ResolveWiki)
	// lets the capture pass be exercised against a temp wiki without cloning a
	// real one.
	resolveWiki := r.ResolveWiki
	if resolveWiki == nil {
		resolveWiki = patterns.ResolveWiki
	}
	wiki := resolveWiki(owner, name, opts.Wiki, opts.Remote)

	// Build cache key (includes min-severity so filtered caches don't leak).
	var cacheFlags []string
	if opts.MinSeverity != "" {
		cacheFlags = append(cacheFlags, "min="+string(opts.MinSeverity))
	}
	if wiki.CommitSHA != "" {
		cacheFlags = append(cacheFlags, "wiki="+wiki.CommitSHA)
	}
	cacheKey := cache.AuditKey(owner, name, headSHA, cacheFlags...)

	// A --capture-wiki run must reach the write-back, but capture runs only on a
	// cache miss (a cache hit returns before runCapture) and the capture-gating
	// flags are not part of the cache key. So an otherwise-identical earlier run
	// would hit the cache, return early, and silently skip the write — leaving the
	// wiki unchanged while the build goes green. Bypass the cache hit when the
	// capture will actually write so the requested push is never a silent no-op.
	captureWillWrite := opts.CaptureWiki && !opts.NoCapture && wiki.Dir != ""

	if !opts.NoCache && headSHA != "" && !captureWillWrite {
		if data, ok := cache.GetRaw(cacheKey, opts.CacheMaxAge); ok {
			var result report.ReviewResult
			if err := json.Unmarshal(data, &result); err == nil {
				slog.Info("using cached audit result — skipping clone", "repo", opts.RepoRef)
				result.WikiRepo = wiki.Repo
				result.WikiCommit = wiki.CommitSHA
				r.applyIssueDedupe(&result, owner, name, opts)
				return renderAudit(w, &result, &github.Repo{Owner: owner, Name: name}, opts)
			}
			slog.Warn("cache corrupted, running fresh audit")
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

	patternDirs, err := patterns.Resolve(patterns.ResolveOptions{
		NoLocal: opts.NoLocalPatterns,
		NoRepo:  opts.NoRepoPatterns,
		RepoDir: repo.Dir,
		Wiki:    wiki.PatternsDir,
		Extra:   opts.PatternDirs,
	})
	if err != nil {
		return fmt.Errorf("resolving pattern sources: %w", err)
	}
	pats, err := patterns.LoadFilteredWithOptions(patterns.LoadOptions{Remote: opts.Remote, NoEmbedded: opts.NoLocalPatterns}, techTags, patternDirs...)
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
		Memory:      wiki.Memory,
	})
	if err != nil {
		return fmt.Errorf("claude audit: %w", err)
	}
	result.WikiRepo = wiki.Repo
	result.WikiCommit = wiki.CommitSHA

	if !opts.NoCache && headSHA != "" {
		if data, err := json.Marshal(result); err == nil {
			if err := cache.PutRaw(cacheKey, cache.CommandAudit, data); err != nil {
				slog.Warn("could not cache result", "err", err)
			}
		}
	}

	slog.Info("audit complete")
	r.applyIssueDedupe(result, repo.Owner, repo.Name, opts)
	if err := renderAudit(w, result, repo, opts); err != nil {
		return err
	}

	// Capture pass: read-only proposal of new wiki review patterns from the audit
	// findings — writing nothing by default. Runs on a cache miss only (a cache hit
	// returned above before the catalog was loaded), and only when a wiki resolved.
	// An explicitly-requested --capture-wiki push that fails is returned (fatal) so
	// a CI write that left the wiki unchanged surfaces as a non-zero exit.
	return r.runCapture(w, repo, opts, result, pats, wiki)
}

// runCapture runs the read-only capture pass after the audit renders: it mines
// the audit findings for generalizable review patterns and proposes them as new
// wiki pages, deduplicated against the wiki and the catalog, writing nothing by
// default. Gated on a resolved wiki and a wired Capturer (and not opts.NoCapture).
//
// Unlike review there is no PR or issue to comment on, so the proposals go to
// stdout only; under --capture-wiki the accepted pages are pushed to the wiki.
// Audit clones the repo's own default branch, a trusted source, so unlike review
// it may push. The propose half is non-fatal — a failure is surfaced but never
// fails the audit, which is already rendered — but an explicitly-requested
// --capture-wiki push that fails is returned so the operator sees a non-zero exit
// rather than a green build that left the wiki unchanged.
func (r *Runner) runCapture(w io.Writer, repo *github.Repo, opts Options, result *report.ReviewResult, pats []patterns.Pattern, wiki patterns.ResolvedWiki) error {
	if opts.NoCapture || r.Capturer == nil || wiki.Dir == "" {
		return nil
	}

	// Under --format json the human-readable capture render would corrupt the JSON
	// on stdout, so discard it; the gated write still occurs.
	out := w
	if opts.Format == "json" {
		out = io.Discard
	}

	pass := capture.Pass{
		Propose: r.Capturer,
		Writer:  r.CaptureWriter,
		In:      r.In,
		IsTTY:   r.IsTTY,
		// PostComment nil: an audit has no PR or issue to post the proposals to.
	}
	return pass.Run(out, capture.Request{
		Dir:         repo.Dir,
		Command:     "audit",
		Repo:        repo.FullName(),
		Findings:    result.Findings,
		Patterns:    pats,
		Wiki:        wiki,
		WikiRef:     opts.Wiki.Ref,
		CaptureWiki: opts.CaptureWiki,
		Yes:         opts.Yes,
		Version:     opts.Version,
	})
}

// openRepo returns the working tree to audit: the user's cwd when --local is
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
