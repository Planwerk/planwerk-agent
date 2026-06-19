package elaborate

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/planwerk/planwerk-review/internal/cache"
	"github.com/planwerk/planwerk-review/internal/detect"
	"github.com/planwerk/planwerk-review/internal/github"
	"github.com/planwerk/planwerk-review/internal/patterns"
	"github.com/planwerk/planwerk-review/internal/workspace"
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
	// Review enables the optional reviewer gate: after the draft is produced,
	// a reviewer pass checks it for executability and a bounded refine loop
	// closes any gaps before the elaboration is rendered or published.
	Review              bool
	MaxReviewIterations int
	Local               bool // operate on the current working directory instead of cloning
	Force               bool // with Local, skip the dirty-working-tree confirmation prompt
	// Remote configures how remote pattern URIs (--patterns github:..., git+...)
	// resolve into local directories; carries the --remote-patterns-ttl value.
	Remote patterns.RemoteOptions
}

// defaultMaxReviewIterations bounds the reviewer refine loop so a reviewer and
// elaborator that keep disagreeing cannot spin forever. Unresolved gaps after
// this many rounds are surfaced in the output instead.
const defaultMaxReviewIterations = 3

// passingReviewScore is the executability bar (0-10) a draft must clear for the
// reviewer refine loop to stop. Below it, the loop refines the draft until it
// clears the bar or the iteration budget runs out. It is a package constant —
// not a flag — because the issue does not ask for a configurable threshold.
const passingReviewScore = 8

// Runner executes the elaborate pipeline using injected Claude and GitHub
// clients.
type Runner struct {
	Claude ClaudeElaborator
	GitHub GitHubClient
	// Reviewer is the optional elaboration reviewer. When nil (or opts.Review
	// is false) the reviewer gate is skipped entirely.
	Reviewer ElaborationReviewer
}

// NewRunner returns a Runner wired with the production GitHub backend, the
// given Claude elaborate function, and an optional reviewer function. A nil
// reviewFn leaves the reviewer gate disabled.
func NewRunner(fn ElaborateFn, reviewFn ReviewFn) *Runner {
	r := &Runner{
		Claude: elaborateFnAdapter{fn: fn},
		GitHub: defaultGitHubClient{},
	}
	if reviewFn != nil {
		r.Reviewer = reviewFnAdapter{fn: reviewFn}
	}
	return r
}

// Run is a package-level convenience that delegates to NewRunner(fn, reviewFn).Run.
func Run(w io.Writer, opts Options, fn ElaborateFn, reviewFn ReviewFn) error {
	return NewRunner(fn, reviewFn).Run(w, opts)
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

	// Resolve the issue's Meta/Sub-Issue neighborhood so a Sub Issue is
	// elaborated against its Meta Issue and sibling Sub Issues, not in
	// isolation. Best-effort: a repo without sub-issue relationships, a token
	// lacking the scope, or an older GHES surfaces an error here and degrades to
	// "no relations" rather than aborting the elaboration.
	relations, err := r.GitHub.GetIssueRelations(owner, name, number)
	if err != nil {
		slog.Warn("could not fetch sub-issue relations; elaborating without Meta/sibling context", "issue", number, "err", err)
		relations = &github.IssueRelations{}
	}
	if relations.Parent != nil {
		slog.Info("issue is a sub-issue; including Meta and sibling context", "issue", number, "meta", relations.Parent.Number, "siblings", len(relations.Siblings))
	}

	headSHA, err := r.GitHub.DefaultBranchHEAD(owner, name)
	if err != nil {
		slog.Warn("could not fetch HEAD SHA, caching disabled", "err", err)
		opts.NoCache = true
		headSHA = ""
	}

	cacheKey := elaborateCacheKey(owner, name, number, issue, relations, headSHA, opts.Review)

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

	repo, err := r.openRepo(opts, fmt.Sprintf("%s/%s", owner, name))
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
	} else {
		slog.Warn("no review patterns loaded — elaboration will not be grounded in the pattern catalog")
	}

	slog.Info("elaborating issue with Claude")
	baseCtx := Context{
		Patterns:      pats,
		MaxPatterns:   opts.MaxPatterns,
		RepoName:      repo.FullName(),
		Issue:         issue,
		MetaIssue:     relations.Parent,
		SiblingIssues: relations.Siblings,
		ChildIssues:   relations.Children,
	}
	result, err := r.Claude.Elaborate(repo.Dir, baseCtx)
	if err != nil {
		return fmt.Errorf("claude elaborate: %w", err)
	}
	if result.Title == "" {
		result.Title = issue.Title
	}
	result.Body = BuildIssueBody(result)

	if opts.Review && r.Reviewer != nil {
		result = r.runReviewLoop(repo.Dir, baseCtx, result, opts)
	}

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

// openRepo returns the working tree to ground the elaboration in: the user's
// cwd when --local is set (no clone, Cleanup is a no-op), otherwise a fresh
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
	slog.Info("cloning repository for elaboration", "repo", fullName)
	return r.GitHub.CloneRepo(fullName)
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
// upstream issue is edited or the repo head moves. When the issue is a Sub
// Issue (or itself a Meta Issue), a fingerprint of the Meta and sibling/child
// issues is folded in too, so editing the Meta Issue or a sibling re-elaborates.
// The relations flag is appended only when relations exist, so a plain issue's
// key stays stable.
func elaborateCacheKey(owner, name string, number int, issue *github.Issue, relations *github.IssueRelations, headSHA string, review bool) string {
	flags := []string{
		fmt.Sprintf("issue=%d", number),
		"body=" + issueFingerprint(issue),
	}
	if fp := relationsFingerprint(relations); fp != "" {
		flags = append(flags, "relations="+fp)
	}
	if review {
		flags = append(flags, "review")
	}
	return cache.AuditKey(owner, name, "elaborate@"+headSHA, flags...)
}

// runReviewLoop runs the optional reviewer gate: it asks the reviewer to score
// the current draft for executability (0-10) and, while the score stays below
// passingReviewScore, refines the draft to close the reported gaps — bounded by
// opts.MaxReviewIterations. The final score is attached to the result so
// near-misses are surfaced; gaps and the "what a 10 looks like" target that
// survive the budget are surfaced too rather than silently published. Any
// reviewer or refinement error keeps the best draft so far.
func (r *Runner) runReviewLoop(dir string, baseCtx Context, result *Result, opts Options) *Result {
	maxIter := opts.MaxReviewIterations
	if maxIter <= 0 {
		maxIter = defaultMaxReviewIterations
	}

	for i := 1; i <= maxIter; i++ {
		review, err := r.Reviewer.ReviewElaboration(dir, baseCtx, result.Body)
		if err != nil {
			slog.Warn("elaboration review failed; keeping current draft", "iteration", i, "err", err)
			return result
		}
		score := review.Score
		result.ReviewScore = &score
		if review.Score >= passingReviewScore {
			slog.Info("elaboration cleared the executability bar", "iteration", i, "score", review.Score)
			result.Body = BuildIssueBody(result)
			return result
		}
		slog.Info("reviewer scored the elaboration below the bar; refining", "iteration", i, "score", review.Score, "gaps", len(review.Gaps))

		if i == maxIter {
			// Out of budget — surface the score, surviving gaps, and target.
			slog.Warn("elaboration still below the executability bar after max iterations", "score", review.Score, "gaps", len(review.Gaps))
			result.UnresolvedGaps = review.Gaps
			result.ReviewTarget = review.ToReachTen
			result.Body = BuildIssueBody(result)
			return result
		}

		refineCtx := baseCtx
		refineCtx.PriorDraft = result.Body
		refineCtx.ReviewGaps = review.Gaps
		refineCtx.ReviewTarget = review.ToReachTen
		refined, err := r.Claude.Elaborate(dir, refineCtx)
		if err != nil {
			slog.Warn("elaboration refinement failed; keeping current draft", "iteration", i, "err", err)
			result.Body = BuildIssueBody(result)
			return result
		}
		if refined.Title == "" {
			refined.Title = result.Title
		}
		refined.Body = BuildIssueBody(refined)
		result = refined
	}
	return result
}

// issueFingerprint returns a stable short hash of the issue's title+body so
// cache entries invalidate when the upstream issue is edited.
func issueFingerprint(issue *github.Issue) string {
	if issue == nil {
		return ""
	}
	return shortHash(issue.Title + "\n" + issue.Body)
}

// relationsFingerprint returns a stable short hash over the Meta Issue and the
// sibling/child Sub Issues (number, title, body, state of each) so the cache
// invalidates when any of them is edited. It returns "" when the issue has no
// relations, so elaborateCacheKey can leave a plain issue's key untouched.
func relationsFingerprint(relations *github.IssueRelations) string {
	if relations == nil || (relations.Parent == nil && len(relations.Children) == 0) {
		return ""
	}
	var sb strings.Builder
	writeIssueFingerprint(&sb, relations.Parent)
	for i := range relations.Siblings {
		writeIssueFingerprint(&sb, &relations.Siblings[i])
	}
	for i := range relations.Children {
		writeIssueFingerprint(&sb, &relations.Children[i])
	}
	return shortHash(sb.String())
}

// writeIssueFingerprint appends an issue's identifying fields to sb for the
// relations fingerprint. A nil issue contributes nothing.
func writeIssueFingerprint(sb *strings.Builder, issue *github.Issue) {
	if issue == nil {
		return
	}
	fmt.Fprintf(sb, "%d\n%s\n%s\n%s\n", issue.Number, issue.Title, issue.State, issue.Body)
}
