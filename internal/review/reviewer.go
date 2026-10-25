package review

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"

	"golang.org/x/sync/errgroup"

	"github.com/planwerk/planwerk-agent/internal/cache"
	"github.com/planwerk/planwerk-agent/internal/capture"
	"github.com/planwerk/planwerk-agent/internal/checklist"
	"github.com/planwerk/planwerk-agent/internal/claude"
	"github.com/planwerk/planwerk-agent/internal/detect"
	"github.com/planwerk/planwerk-agent/internal/diffscope"
	"github.com/planwerk/planwerk-agent/internal/doccheck"
	"github.com/planwerk/planwerk-agent/internal/github"
	"github.com/planwerk/planwerk-agent/internal/glossary"
	"github.com/planwerk/planwerk-agent/internal/hygiene"
	"github.com/planwerk/planwerk-agent/internal/patterns"
	"github.com/planwerk/planwerk-agent/internal/planwerk"
	"github.com/planwerk/planwerk-agent/internal/redact"
	"github.com/planwerk/planwerk-agent/internal/report"
	"github.com/planwerk/planwerk-agent/internal/search"
	"github.com/planwerk/planwerk-agent/internal/todocheck"
	"github.com/planwerk/planwerk-agent/internal/workspace"
)

// formatSARIF is the Options.Format under which the review writes one SARIF
// log as the whole of stdout and posts nothing (decision 134).
const formatSARIF = "sarif"

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
	// ResolveOutdated resolves, after an inline review is posted, the review
	// threads earlier runs opened that GitHub marks outdated, under the guards
	// resolveOutdatedThreads lists. It requires InlineReview and is off by
	// default. It is not part of the cache key, because it changes posting
	// only.
	ResolveOutdated bool
	Thorough        bool
	Specialists     bool // run the domain-specialist fan-out and merge its findings
	CoverageMap     bool
	MaxPatterns     int           // max patterns to inject into prompt; <= 0 disables truncation
	MaxFindings     int           // cap on findings Claude returns; <= 0 disables cap
	PartitionFiles  int           // split a PR changing more than this many files into partitions of at most this many, one primary-review session each (decision 142); <= 0 reviews it in one session
	CacheMaxAge     time.Duration // reject cache entries older than this; <= 0 disables the TTL
	Local           bool          // operate on the current working directory instead of cloning
	Force           bool          // with Local, skip the dirty-working-tree confirmation prompt
	// Remote configures how remote pattern URIs (--patterns github:..., git+...)
	// resolve into local directories; carries the --remote-patterns-ttl value.
	Remote patterns.RemoteOptions
	// Wiki configures the target repo's GitHub Wiki as a knowledge source
	// (review patterns + project memory); carries the --wiki/--no-wiki/--wiki-ref
	// values.
	Wiki patterns.WikiOptions
	// NoCapture disables the read-only capture pass that, after the review,
	// proposes new wiki review patterns from the review findings — writing
	// nothing. On by default but only when a wiki is resolved (i.e. with --wiki);
	// without one it is a no-op regardless of this flag.
	NoCapture bool
	// Brain lets the review session search the local mirror of the repository's
	// issues, pull requests, and wiki (`brain search`); carries the
	// --brain/--no-brain value. Off by default.
	Brain bool
	// DiffExclusions leaves the changed lock, vendored and generated files out
	// of the diff the review session reads, and the report lists them. The CLI
	// sets it from claude.Client.DiffExclusions, so the review session and the
	// Client's other finder passes leave out the same files (decision 137).
	DiffExclusions bool
	// ScopePatterns carries in the review prompt only the pattern bodies the
	// changed files match and lists the rest as an index the review session
	// reads from disk (--scope-patterns); off by default. The adversarial
	// pass and the specialists keep the whole catalog (decision 141).
	ScopePatterns bool
	// DryRun reports the run a review would make, from the review's own
	// preparation, and stops before the first session: no Claude call, no
	// cache write, no post, no capture pass. A cache hit is reported, not
	// served (decision 143).
	DryRun bool
	// PrintPrompt writes the review session's prompt to the writer and stops,
	// with the same guarantees as DryRun (decision 143).
	PrintPrompt bool
}

// Runner executes the review pipeline using injected Claude and GitHub
// clients. Constructing a Runner per invocation keeps dependencies explicit
// and allows tests to run in parallel without mutating package-level state.
type Runner struct {
	Claude ClaudeRunner
	GitHub GitHubClient
	// Capturer runs the read-only capture pass after the review: it proposes new
	// wiki review patterns from the review findings, writing nothing. Set to the
	// Claude client by NewRunner; nil (or a wiki that did not resolve, or
	// opts.NoCapture) leaves the pass disabled.
	Capturer capture.Proposer
	// ResolveWiki resolves the target repo's wiki. Defaults to
	// patterns.ResolveWiki; a Runner seam so the capture pass can be exercised
	// against a temp wiki without cloning a real one.
	ResolveWiki resolveWikiFn
	// ResolveBrain resolves the search surface the review session is handed when
	// opts.Brain is set. Defaults to search.ResolveSurface; a Runner seam so the
	// wiring can be exercised without a mirror.
	ResolveBrain func(owner, name string) search.Surface
	// Pause waits between two resolve mutations of resolveOutdatedThreads. nil
	// sleeps with time.Sleep; tests pass a no-op.
	Pause func(time.Duration)
}

// resolveWikiFn resolves the target repo's wiki. A Runner seam so the capture
// pass can be exercised against a temp wiki without cloning a real one. Mirrors
// implement.resolveWikiFn.
type resolveWikiFn func(owner, name string, wopts patterns.WikiOptions, ropts patterns.RemoteOptions) patterns.ResolvedWiki

// NewRunner returns a Runner wired with the production Claude Code (driven by
// the given client) and GitHub (gh CLI) backends. The same client backs the
// capture pass's read-only proposal call.
func NewRunner(client *claude.Client) *Runner {
	return &Runner{
		Claude:   defaultClaudeRunner{client: client},
		GitHub:   github.Client{},
		Capturer: client,
	}
}

// Run is a package-level convenience that delegates to NewRunner(client).Run.
// Callers that need to inject alternative Claude or GitHub backends should
// construct a Runner directly.
func Run(w io.Writer, opts Options, client *claude.Client) error {
	return NewRunner(client).Run(w, opts)
}

// Run executes the full review pipeline:
// fetch & checkout PR → load patterns → review session → structure → render report.
func (r *Runner) Run(w io.Writer, opts Options) error {
	// 1. Fetch and checkout PR
	slog.Info("fetching and checking out PR", "pr", opts.PRRef, "dry_run", opts.DryRun, "print_prompt", opts.PrintPrompt)
	var (
		pr  *github.PR
		err error
	)
	if opts.Local {
		pr, err = r.GitHub.OpenLocalPR(opts.PRRef, github.LocalOptions{Force: opts.Force, Prompter: workspace.NewStdinPrompter()})
	} else {
		pr, err = r.GitHub.FetchAndCheckout(opts.PRRef)
	}
	if err != nil {
		return fmt.Errorf("fetching PR: %w", err)
	}
	defer pr.Cleanup()

	slog.Info("checked out PR", "dir", pr.Dir, "head", pr.HeadSHA)
	if opts.Local {
		slog.Info("working tree left on PR branch", "branch", pr.HeadBranch, "dir", pr.Dir)
	}

	// 1b. Resolve the target repo's GitHub Wiki (best-effort) before the cache
	// key, so the resolved wiki commit folds into the key — a wiki edit then
	// busts the cache instead of being silently ignored on a cache hit — and so
	// the same commit can be recorded in the report header. An absent, disabled,
	// or offline wiki returns the zero value and leaves the run unchanged. The
	// seam (defaulting to patterns.ResolveWiki) lets the capture pass be exercised
	// against a temp wiki without cloning a real one.
	resolveWiki := r.ResolveWiki
	if resolveWiki == nil {
		resolveWiki = patterns.ResolveWiki
	}
	wiki := resolveWiki(pr.Owner, pr.Repo, opts.Wiki, opts.Remote)

	// The search of the local mirror is resolved before the cache key: the
	// mirror's revision is part of it (search.Surface.CacheFlag).
	surface := search.SurfaceFor(opts.Brain, r.ResolveBrain, pr.Owner, pr.Repo)

	// 2. Check cache (include flags that affect output in the cache key). A dry
	// run and a printed prompt read the entry but are never served it: they
	// stand for the run a review makes on a miss (decision 143).
	cacheKey := reviewCacheKey(opts, pr, wiki, surface)
	cacheState := previewCacheOff
	if !opts.NoCache {
		entry, ok := readCachedReview(cacheKey, opts.CacheMaxAge)
		cacheState = previewCacheMiss
		if ok {
			cacheState = previewCacheHit
		}
		if ok && !opts.DryRun && !opts.PrintPrompt {
			slog.Info("using cached review result")
			entry.Result.WikiRepo = wiki.Repo
			entry.Result.WikiCommit = wiki.CommitSHA
			return r.renderResult(w, entry.Result, pr, opts, entry.Coverage)
		}
	}

	// 3-8b. Prepare the review: everything the sessions are handed, read and
	// written once here, after the cache check, and removed when Run returns.
	in, cleanup, err := r.prepareReview(opts, pr, wiki, surface)
	if err != nil {
		return err
	}
	defer cleanup()

	// The two modes stop here, before the first session: no Claude call, no
	// cache write, no post, no capture pass (decision 143).
	if opts.PrintPrompt {
		return printReviewPrompt(w, in)
	}
	if opts.DryRun {
		return writePreview(w, opts.Format, buildPreview(opts, pr, wiki, surface, cacheKey, cacheState, in))
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
	if in.feature != nil {
		slog.Info("running feature compliance check")
	}

	var (
		// reviewParts holds the primary review's results unmerged: one per
		// partition session, or the single session's (decision 142).
		reviewParts      []*report.ReviewResult
		result           *report.ReviewResult
		advResult        *report.ReviewResult
		complianceResult *report.ReviewResult
		coverageResult   *report.CoverageResult
		advErr           error
		complianceErr    error
		covErr           error
	)
	// specialistResults[i] holds the findings from claude.Specialists[i] and
	// specialistStatus[i] how its pass ended; a nil result means that specialist
	// was gated out or failed (both non-fatal), is skipped at merge, and its
	// status says which.
	var (
		specialistResults []*report.ReviewResult
		specialistStatus  []report.PassStatus
	)

	var g errgroup.Group
	g.Go(func() error {
		if len(in.sessions) > 0 {
			parts, err := r.reviewPartitions(pr, in.plan, in.sessions, in.diff.Excluded)
			if err != nil {
				return fmt.Errorf("claude review: %w", err)
			}
			reviewParts = parts
			return nil
		}
		res, err := r.Claude.Review(pr.Dir, in.reviewCtx)
		if err != nil {
			return fmt.Errorf("claude review: %w", err)
		}
		reviewParts = []*report.ReviewResult{res}
		return nil
	})
	if opts.Thorough {
		g.Go(func() error {
			advResult, advErr = r.Claude.AdversarialReview(pr.Dir, pr.BaseBranch, "", in.pats, opts.MaxPatterns)
			return nil
		})
	}
	if opts.CoverageMap {
		g.Go(func() error {
			coverageResult, covErr = r.Claude.CoverageMap(pr.Dir, pr.BaseBranch)
			return nil
		})
	}
	if in.feature != nil {
		g.Go(func() error {
			complianceResult, complianceErr = r.Claude.FeatureCompliance(pr.Dir, pr.BaseBranch, in.feature)
			return nil
		})
	}
	if opts.Specialists {
		g.Go(func() error {
			specialistResults, specialistStatus = claude.RunSpecialistFanOut(pr.ChangedFiles, func(sp claude.Specialist) (*report.ReviewResult, error) {
				return r.Claude.SpecialistReview(pr.Dir, pr.BaseBranch, sp, in.pats, opts.MaxPatterns)
			})
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return err
	}

	// 11. Line anchoring: place each pass's findings on the code they quote
	// before the merges below match them by file and line. A failed pass is left
	// out, so the counts cover the passes the report draws on, each pass's
	// findings before they are folded; a nil entry (a gated-out or failed
	// specialist) is skipped (decision 139). Each partition of a partitioned
	// review counts as a pass of its own here, so its findings are placed
	// before the partitions merge (decision 142). A finding is relocated only
	// within the changed files the diff kept: no session was shown the rest,
	// and a large one left unread would stop every relocation (decision 137).
	secondaries := make([]*report.ReviewResult, 0, len(reviewParts)+1+len(specialistResults))
	secondaries = append(secondaries, reviewParts[1:]...)
	if advErr == nil {
		secondaries = append(secondaries, advResult)
	}
	if complianceErr == nil {
		secondaries = append(secondaries, complianceResult)
	}
	secondaries = append(secondaries, specialistResults...)
	hygiene.AnchorFindings(reviewParts[0], pr.Dir, diffscope.Reviewable(pr.ChangedFiles, in.diff.Excluded), secondaries...)

	// The partitions fold into one primary result before the secondary passes
	// merge into it, and that result carries the run's excluded files as the
	// single session's does (claude.Client.Review). The partition record is
	// the pipeline's, so a value decoded from a session's output never
	// survives (decision 142).
	if len(in.plan) > 0 {
		result = mergePartitions(reviewParts, in.plan)
		result.ExcludedFiles = in.diff.Excluded
	} else {
		result = reviewParts[0]
		result.Partitions = nil
	}

	if advErr != nil {
		slog.Warn("adversarial review failed", "err", advErr)
	} else if advResult != nil {
		hygiene.TagPass(result, hygiene.PassReview)
		hygiene.TagPass(advResult, hygiene.PassAdversarial)
		result = hygiene.MergeResults(result, advResult)
	}
	if complianceErr != nil {
		slog.Warn("feature compliance check failed", "err", complianceErr)
	} else if complianceResult != nil {
		hygiene.TagPass(result, hygiene.PassReview)
		hygiene.TagPass(complianceResult, hygiene.PassCompliance)
		result = hygiene.MergeResults(result, complianceResult)
	}
	if opts.Specialists {
		result = mergeSpecialists(result, specialistResults)
	}
	if covErr != nil {
		slog.Warn("coverage map failed", "err", covErr)
	}
	// Record how each requested secondary pass ended, so the report names a
	// failed one and the cache write below can refuse the result. Assigned
	// unconditionally, so a value decoded from finder output never survives
	// (decision 135).
	result.Passes = requestedPasses(opts, in.feature != nil, advErr, covErr, complianceErr, specialistStatus)

	// 11a. File-less dedup fallback: the fuzzy merge matcher can only anchor
	// findings that carry a file, so cross-pass duplicates among file-less
	// findings survive. When a secondary pass contributed, reconcile them with a
	// single cheap structure-tier grouping call. Non-fatal: on failure the
	// findings ship unmerged. The partitions of a partitioned review count as
	// contributors, since each may report the same file-less finding
	// (decision 142).
	if opts.Thorough || opts.Specialists || in.feature != nil || len(in.plan) > 0 {
		hygiene.DedupFileless(result, r.Claude.DedupFindings)
	}

	// 11b. Quote-or-demote gate: downgrade findings whose code snippet cannot
	// be located in the changed files so unverifiable claims land in the
	// Unverified section instead of next to confirmed bugs. The gate records
	// what it examined and demoted on the result, which the cache write below
	// then persists.
	if n := hygiene.VerifySnippets(result, pr.Dir, pr.ChangedFiles); n > 0 {
		slog.Info("demoted findings failing snippet verification", "count", n)
	}

	// 11c. Claim verification: re-check each BLOCKING/CRITICAL finding's claim
	// against the checkout and demote any the verifier refutes with quoted
	// counter-evidence. Runs before the cache write so the demotion — and the
	// per-finding and run-level records the gate writes onto the result — are
	// cached too. Fail-open: a failed verification publishes the findings
	// unchanged. See report.ClaimGateStats for why the refuted/sent ratio the
	// records now carry is worth watching.
	hygiene.VerifyClaims(result, pr.Dir, r.Claude.VerifyFindingClaims)

	// 11d. Changed-file check: compare the files the prompt listed with the
	// review session's account of them and record the outcome on the result,
	// before the cache write so a cache hit renders it too. A record, not a
	// gate: it moves no finding (decision 138).
	if n := hygiene.CheckFileLedger(result, ledgerPaths(in.ledger)); n > 0 {
		slog.Info("the review did not account for every changed file", "missing", n, "changed", len(in.ledger))
	}

	// 12. Cache the result together with the coverage map, so a later hit
	// renders the report this run rendered rather than a subset of it. A run in
	// which a requested pass failed is not cached: the key names the passes the
	// run asked for, so a cached entry would serve the incomplete review to
	// every later run of the same commit and flags instead of retrying the pass
	// (decision 135).
	if !opts.NoCache {
		if failed := result.FailedPasses(); len(failed) > 0 {
			slog.Warn("not caching the review: a requested pass failed, so the next run retries it", "passes", passNames(failed))
		} else if err := writeCachedReview(cacheKey, result, coverageResult); err != nil {
			slog.Warn("could not cache result", "err", err)
		}
	}

	// 13. Render. Record the resolved wiki provenance on the result (excluded
	// from the cached payload, so it is re-attached on a cache hit too).
	result.WikiRepo = wiki.Repo
	result.WikiCommit = wiki.CommitSHA
	slog.Info("review complete")
	if err := r.renderResult(w, result, pr, opts, coverageResult); err != nil {
		return err
	}

	// Capture pass: read-only proposal of new wiki review patterns from the review
	// findings — writing nothing by default. Runs on a cache miss only (a cache hit
	// returned above before the catalog was loaded), and only when a wiki resolved.
	r.runCapture(w, pr, opts, result, in.pats, wiki)
	return nil
}

// preparedReview is what the review sessions are handed, as prepareReview
// read and wrote it: the detected technologies, the loaded catalog, the
// detected feature file, the review session's diff, whose Excluded is the
// run's excluded set, the changed-file ledger, the partition plan, the review
// context, and one context per partition session (nil for one session).
type preparedReview struct {
	techTags  []string
	pats      []patterns.Pattern
	feature   *planwerk.Feature
	diff      diffscope.Diff
	ledger    []github.FileChange
	plan      []report.ReviewPartition
	reviewCtx claude.ReviewContext
	sessions  []claude.ReviewContext
}

// prepareReview runs every step of a review before its first session, in
// order: technology detection, the pattern, checklist and glossary loads
// from the base ref, the commit log, the documentation and TODO checks,
// feature detection, redaction, the project memory, the review diff, the
// changed-file ledger, the --scope-patterns catalog and the partition plan,
// then the review contexts. The live run, the dry run and the printed prompt
// share it, so each sees what the others do. With opts.PrintPrompt it writes
// nothing to disk and uses the forms that need no directory: the memory page
// bodies, a Diff that names no file but carries the run's excluded set
// (diffscope.Exclusions), and a nil Scope. The returned cleanup removes what it
// wrote and is never nil; the caller runs it once the sessions return
// (decision 143).
func (r *Runner) prepareReview(opts Options, pr *github.PR, wiki patterns.ResolvedWiki, surface search.Surface) (*preparedReview, func(), error) {
	var cleanups []func()
	cleanup := func() {
		for i := len(cleanups) - 1; i >= 0; i-- {
			cleanups[i]()
		}
	}

	// 3. Detect technologies in the reviewed repo
	techTags := detect.Technologies(pr.Dir)
	if len(techTags) > 0 {
		slog.Info("detected technologies", "technologies", strings.Join(techTags, ", "))
	}

	// 4. Load patterns (filtered by detected technologies)
	pats, err := patterns.LoadForRepo(patterns.RepoLoadOptions{
		RepoDir:    pr.Dir,
		RepoRef:    "origin/" + pr.BaseBranch,
		Wiki:       wiki.PatternsDir,
		Extra:      opts.PatternDirs,
		Tags:       techTags,
		NoEmbedded: opts.NoLocalPatterns,
		NoRepo:     opts.NoRepoPatterns,
		Remote:     opts.Remote,
	})
	if err != nil {
		return nil, func() {}, err
	}

	if len(pats) > 0 {
		slog.Info("loaded review patterns", "count", len(pats))
	}

	// 5. Load the checklist from the base ref, like the patterns above and the
	// glossary below: it is spliced into the prompt as instructions, so a pull
	// request must not be able to rewrite the checklist it is reviewed against.
	checklistContent := checklist.LoadFromRef(pr.Dir, "origin/"+pr.BaseBranch)

	// 5b. Load the repo's domain glossary (glossary.LoadBodyFromRef)
	// from the base ref, NOT pr.Dir (the PR head checkout). Reading origin/<base>
	// keeps the glossary maintainer-controlled: a PR cannot inject or rewrite a
	// glossary to smuggle instructions into the reviewer's prompt. Best-effort,
	// mirroring the out-of-scope load: an unreadable glossary proceeds rather
	// than failing the review.
	glossaryBody := glossary.LoadBodyFromRef(pr.Dir, "origin/"+pr.BaseBranch)

	// 6. Fetch commit log for scope drift detection
	commitLog, err := getCommitLog(pr.Dir, pr.BaseBranch)
	if err != nil {
		slog.Warn("fetching commit log failed; scope-drift detection will be degraded", "err", err)
	}

	// 7. Check for stale documentation
	staleDocs := doccheck.Check(pr.Dir, pr.BaseBranch)

	// 7b. Check for new features that may need documentation
	newFeatures := doccheck.CheckNewFeatures(pr.Dir, pr.BaseBranch)

	// 8. Load TODOS.md for cross-reference
	todoContent := todocheck.LoadFromRef(pr.Dir, "origin/"+pr.BaseBranch)

	// 8b. Detect Planwerk feature file for compliance checking
	feature, _ := planwerk.DetectFeature(pr.Dir, pr.Title, pr.Body, pr.HeadBranch, pr.ChangedFiles)
	if feature != nil {
		slog.Info("detected Planwerk feature file", "feature_id", feature.FeatureID, "title", feature.Title)
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

	// The project memory is written once here, after the cache check, and
	// removed when Run returns. A printed prompt outlives the run, so it
	// carries the page bodies instead (decision 143).
	mem := patterns.MemoryCatalog{Pages: wiki.MemoryPages}
	if !opts.PrintPrompt {
		var cleanupMemory func()
		mem, cleanupMemory = patterns.MaterializeMemoryOrWarn(wiki.MemoryPages)
		cleanups = append(cleanups, cleanupMemory)
	}

	// The review session's diff is written here too, and removed when Run
	// returns. Its Excluded list is the run's excluded set: the review result
	// records it, and the report lists it. A failed write leaves the zero
	// Diff, whose prompt keeps the git command (decision 137). A printed
	// prompt takes that form, which needs no file, but classifies the changed
	// files as the run does, so its changed-file list and partition plan are
	// the run's (decision 143).
	diffReq := diffscope.Request{
		RepoDir: pr.Dir,
		Revs:    []string{"origin/" + pr.BaseBranch + "...HEAD"},
		Exclude: opts.DiffExclusions,
	}
	var reviewDiff diffscope.Diff
	if opts.PrintPrompt {
		excluded, err := diffscope.Exclusions(diffReq)
		if err != nil {
			slog.Warn("classifying the changed files under --diff-exclusions failed; the printed prompt lists every one", "err", err)
		}
		reviewDiff.Excluded = excluded
	} else {
		var cleanupDiff func()
		reviewDiff, cleanupDiff = diffscope.MaterializeOrWarn(diffReq)
		cleanups = append(cleanups, cleanupDiff)
	}

	// The changed files the review prompt lists and the session accounts for:
	// the PR's file changes minus what the diff left out and the planning
	// artifacts (decision 138).
	ledger := reviewLedger(pr.FileChanges, reviewDiff.Excluded)

	// Under --scope-patterns the review prompt carries the bodies the files
	// the diff kept match and indexes the rest from a catalog written here,
	// which is removed when Run returns (decision 141). A printed prompt
	// carries every body, as a run whose catalog could not be written does
	// (decision 143).
	scope, cat, cleanupCatalog := scopeCatalog(opts.ScopePatterns && !opts.PrintPrompt, pr.Dir, diffscope.Reviewable(pr.ChangedFiles, reviewDiff.Excluded), pats)
	cleanups = append(cleanups, cleanupCatalog)

	// Under --partition-files a pull request whose diff keeps more files than
	// the flag, its planning artifacts aside, is split here, before any
	// session runs, into partitions that each get a primary-review session of
	// their own (decision 142).
	toPartition := filesToPartition(pr.ChangedFiles, pr.FileChanges, reviewDiff.Excluded)
	plan := planReviewPartitions(toPartition, pr.FileChanges, opts.PartitionFiles)
	if len(plan) > 0 {
		slog.Info("partitioning the review", "partitions", len(plan), "changed_files", len(toPartition), "max_files", opts.PartitionFiles)
	}

	// A session lists its own changed files, so the cap applies per session:
	// when one would list more than maxLedgerFiles, no session gets a list and
	// the run records none (decision 138, decision 142).
	if n := largestSessionLedger(ledger, plan); n > maxLedgerFiles {
		slog.Info("the pull request changes more files than the review lists; file coverage is not recorded", "files", n, "max", maxLedgerFiles)
		ledger = nil
	}

	reviewCtx := claude.ReviewContext{
		Patterns:     pats,
		MaxPatterns:  opts.MaxPatterns,
		MaxFindings:  opts.MaxFindings,
		BaseBranch:   pr.BaseBranch,
		PRTitle:      redactedTitle.Text,
		PRBody:       redactedBody.Text,
		Checklist:    checklistContent,
		CommitLog:    redactedCommitLog.Text,
		StaleDocs:    staleDocs,
		NewFeatures:  newFeatures,
		TodoContent:  todoContent,
		Glossary:     glossaryBody,
		Memory:       mem,
		Brain:        surface,
		Diff:         reviewDiff,
		ChangedFiles: ledger,
		Scope:        scope,
		Catalog:      cat,
	}
	return &preparedReview{
		techTags:  techTags,
		pats:      pats,
		feature:   feature,
		diff:      reviewDiff,
		ledger:    ledger,
		plan:      plan,
		reviewCtx: reviewCtx,
		// One context per partition session, each with its share of the
		// ledger; none when the review runs as one session on reviewCtx
		// (decision 142).
		sessions: partitionContexts(reviewCtx, plan, ledger),
	}, cleanup, nil
}

// reviewCacheKey returns the cache key of a review of pr: the pull request
// and its head commit, plus every option that changes the result, the
// resolved wiki commit, the mirror search's revision and the pattern
// sources. The run, the dry run and the printed prompt compute it alike
// (decision 143).
func reviewCacheKey(opts Options, pr *github.PR, wiki patterns.ResolvedWiki, surface search.Surface) string {
	var cacheFlags []string
	if opts.Thorough {
		cacheFlags = append(cacheFlags, "thorough")
	}
	if opts.Specialists {
		cacheFlags = append(cacheFlags, "specialists")
	}
	if opts.ScopePatterns {
		cacheFlags = append(cacheFlags, "scope-patterns")
	}
	if opts.CoverageMap {
		cacheFlags = append(cacheFlags, "coverage-map")
	}
	if opts.PartitionFiles > 0 {
		cacheFlags = append(cacheFlags, fmt.Sprintf("partition-files=%d", opts.PartitionFiles))
	}
	if wiki.CommitSHA != "" {
		cacheFlags = append(cacheFlags, "wiki="+wiki.CommitSHA)
	}
	if flag := surface.CacheFlag(); flag != "" {
		cacheFlags = append(cacheFlags, flag)
	}
	// The loaded catalog decides what the review looks for, so it belongs in
	// the key: --patterns ./new-rules must not be served a result from a run
	// that never saw those rules.
	cacheFlags = append(cacheFlags, "patterns="+patterns.Fingerprint(
		opts.PatternDirs, opts.NoRepoPatterns, opts.NoLocalPatterns, opts.MaxPatterns))
	return cache.Key(pr.Owner, pr.Repo, pr.Number, pr.HeadSHA, cacheFlags...)
}

// requestedPasses returns how each secondary pass the run requested ended, in
// launch order: the adversarial pass (--thorough), the coverage map
// (--coverage-map), the feature-compliance check (compliance, a detected
// feature file), then the specialists (--specialists) with the statuses the
// fan-out recorded, in registry order. Each entry is named by its session
// label. The primary review is never listed, since its failure fails the run,
// and the result is nil when no secondary pass was requested (decision 135).
func requestedPasses(opts Options, compliance bool, advErr, covErr, complianceErr error, specialists []report.PassStatus) []report.PassStatus {
	var passes []report.PassStatus
	if opts.Thorough {
		passes = append(passes, report.NewPassStatus(claude.PassLabelAdversarial, advErr))
	}
	if opts.CoverageMap {
		passes = append(passes, report.NewPassStatus(claude.PassLabelCoverage, covErr))
	}
	if compliance {
		passes = append(passes, report.NewPassStatus(claude.PassLabelCompliance, complianceErr))
	}
	if opts.Specialists {
		passes = append(passes, specialists...)
	}
	return passes
}

// passNames joins the labels of passes for a log line.
func passNames(passes []report.PassStatus) string {
	names := make([]string, len(passes))
	for i, p := range passes {
		names[i] = p.Pass
	}
	return strings.Join(names, ", ")
}

// cachedReview is what a review run stores: the findings, and the coverage map
// that was rendered beside them when the run was asked for one.
//
// The map is kept here rather than on report.ReviewResult because it is not part
// of the review — nothing merges, filters, suppresses or posts it with the
// findings — but it is part of the report. --coverage-map is part of the cache
// key, so the entry a coverage run writes is only ever served to another
// coverage run, and it has to be able to reproduce what that run would print.
type cachedReview struct {
	Result   *report.ReviewResult   `json:"result"`
	Coverage *report.CoverageResult `json:"coverage,omitempty"`
}

// writeCachedReview stores a run's result and coverage map under key. A nil
// coverage map — the flag was off, or the pass returned no map — is omitted, so
// a hit renders no coverage section either. Run never calls it for a result
// with a failed pass, a failed coverage map included (decision 135).
func writeCachedReview(key string, result *report.ReviewResult, coverage *report.CoverageResult) error {
	data, err := json.Marshal(cachedReview{Result: result, Coverage: coverage})
	if err != nil {
		return err
	}
	return cache.PutRaw(key, cache.CommandReview, data)
}

// readCachedReview loads a stored review, reporting a miss for anything it
// cannot use. An entry written before the coverage map was stored alongside the
// result is a bare ReviewResult: it decodes without error, because none of its
// keys match this envelope, and leaves Result nil. Treating that as a miss
// re-runs the review instead of rendering an empty report from it.
func readCachedReview(key string, maxAge time.Duration) (cachedReview, bool) {
	data, ok := cache.GetRaw(key, maxAge)
	if !ok {
		return cachedReview{}, false
	}
	var entry cachedReview
	if err := json.Unmarshal(data, &entry); err != nil {
		slog.Warn("cached review is unreadable; running a fresh review", "err", err)
		return cachedReview{}, false
	}
	if entry.Result == nil {
		slog.Info("cached review predates the stored coverage map; running a fresh review")
		return cachedReview{}, false
	}
	return entry, true
}

// runCapture runs the read-only capture pass after the review renders: it mines
// the review findings for generalizable review patterns and proposes them as new
// wiki pages, deduplicated against the wiki and the catalog, writing nothing by
// default. Gated on a resolved wiki and a wired Capturer (and not opts.NoCapture).
//
// The proposals always go to stdout. A PR comment is posted only with
// --post-review, so a plain `review --wiki` stays stdout-only. Unlike implement and
// audit, review is propose-only: it analyzes an untrusted pull-request head and the
// proposal pass reads attacker-controlled source, so it never pushes the (free-form,
// indirect-prompt-injectable) proposal pages to the wiki — doing so under
// --capture-wiki --yes in CI would let an external contributor poison the shared
// knowledge base. --capture-wiki is therefore ignored here (with a surfaced note);
// capture pattern pages from a trusted source instead (implement or audit). The pass
// is non-fatal throughout — a failure is surfaced but never fails the review, which
// is already rendered.
func (r *Runner) runCapture(w io.Writer, pr *github.PR, opts Options, result *report.ReviewResult, pats []patterns.Pattern, wiki patterns.ResolvedWiki) {
	if opts.NoCapture || r.Capturer == nil || wiki.Dir == "" {
		return
	}

	// Post the proposals as a PR comment only when the review itself is posted, so
	// a plain `review --wiki` does not comment on the PR. nil otherwise skips it.
	// The proposals get a comment of their own: through PostPRComment they
	// would replace the summary comment and its data block (decision 133).
	var poster capture.CommentPoster
	if opts.PostReview {
		poster = func(body string) (string, error) {
			return r.GitHub.AddPRComment(pr.Owner, pr.Repo, pr.Number, body)
		}
	}

	// Under --format json or sarif the human-readable capture render would
	// corrupt the document on stdout, so discard it.
	out := w
	if opts.Format == "json" || opts.Format == formatSARIF {
		out = io.Discard
	}

	pass := capture.Pass{
		Propose:     r.Capturer,
		PostComment: poster,
	}
	// The returned error is always nil here — review forces propose-only below, so
	// the write-back (the only fatal path) never runs.
	_ = pass.Run(out, capture.Request{
		Dir:         pr.Dir,
		Command:     "review",
		Repo:        pr.Owner + "/" + pr.Repo,
		Number:      pr.Number,
		BaseBranch:  pr.BaseBranch,
		Findings:    result.Findings,
		Patterns:    pats,
		Wiki:        wiki,
		WikiRef:     opts.Wiki.Ref,
		CaptureWiki: false, // propose-only: the proposal pages derive from an untrusted PR head
		Version:     opts.Version,
	})
}

func (r *Runner) renderResult(w io.Writer, result *report.ReviewResult, pr *github.PR, opts Options, coverage *report.CoverageResult) error {
	// Surface a generic, content-free verdict — the forced-recommendation prompt
	// rule should make the model name the specific blocking finding instead.
	if len(result.Findings) > 0 && report.IsBoilerplateRecommendation(result.Recommendation) {
		slog.Warn("review recommendation is generic — the model did not name a specific finding", "recommendation", result.Recommendation)
	}

	// SARIF is the whole of stdout, so it returns ahead of the suppression
	// read, the coverage map and every posting branch: the map and the
	// suppression note are Markdown, and the command refuses to post a SARIF
	// run (decision 134).
	if opts.Format == formatSARIF {
		return report.NewRenderer(w).RenderSARIF(*result, opts.MinSeverity, opts.MinConfidence, opts.Version)
	}

	// Persistent skip-suppression: when re-reviewing a PR, drop findings the user
	// already saw last time and did not act on, as long as their file is
	// unchanged since that review, and findings whose own inline thread someone
	// other than the poster resolved while the quoted code is still in the file
	// (decision 132). The rendered sections use the filtered set and the note
	// lists what it left out; the data block keeps the full set so the next
	// re-review can compare again. --resolve-outdated reuses the threads it
	// read.
	displayResult := result
	var (
		hidden  suppressed
		threads []github.ReviewThread
	)
	if opts.PostReview || opts.InlineReview {
		var (
			kept []report.Finding
			s    suppressed
		)
		kept, s, threads = r.skipSuppressed(result, pr, opts.MinSeverity, opts.MinConfidence)
		if s.total() > 0 {
			filtered := *result
			filtered.Findings = kept
			displayResult = &filtered
			hidden = s
			slog.Info("suppressed findings on re-review", "previously_reported", len(s.unchanged), "dismissed_in_thread", len(s.dismissed))
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

	if hidden.total() > 0 {
		writeSuppressionNote(output, hidden)
	}

	if !opts.PostReview && !opts.InlineReview {
		return nil
	}
	// The data block is built from the FULL result, so the next re-review sees
	// every finding, the suppressed ones included.
	summaryBody := buf.String() + report.RenderDataBlock(*result, pr.HeadSHA, r.Claude.UsageTotals())
	if !opts.InlineReview {
		slog.Info("posting review as PR comment (will update existing if found)")
		url, err := r.GitHub.PostPRComment(pr.Owner, pr.Repo, pr.Number, summaryBody)
		if err != nil {
			return fmt.Errorf("posting PR comment: %w", err)
		}
		slog.Info("review posted", "url", url)
		return nil
	}

	slog.Info("posting inline review with GitHub Review API")
	// Inline comments come from the display set (no new comments for
	// suppressed or dismissed findings) under the report's thresholds.
	if err := r.postInlineReview(displayResult, pr, opts.MinSeverity, opts.MinConfidence, summaryBody); err != nil {
		return err
	}
	// The overlap veto reads the full result: a finding the report or the
	// inline comments leave out is still live and keeps its old thread open,
	// an unanchored one by the line the model gave (decision 139).
	if opts.ResolveOutdated {
		r.resolveOutdatedThreads(pr, result.Findings, threads)
	}
	return nil
}

// maxInlineComments is the conservative limit for inline comments per review.
const maxInlineComments = 50

// inSeveritySection reports whether the rendered summary lists f in one of its
// severity sections under minSeverity and minConfidence: one below either
// threshold is in no section, and one Finding.Unverified reports is in the
// Unverified section. It decides both which findings get an inline comment and
// which of a prior review's findings a re-review counts as already reported, so
// the two cannot drift (decision 129).
func inSeveritySection(f report.Finding, minSeverity report.Severity, minConfidence report.Confidence) bool {
	return f.MeetsThresholds(minSeverity, minConfidence) && !f.Unverified()
}

// inlineReviewComments turns findings into inline comments on the right side of
// the diff, in order, at most maxInlineComments of them. A finding gets a
// comment only when inSeveritySection admits it, the line anchor did not
// stamp it unanchored, and its line is in diffMap. A skipped finding does not
// count against the cap (decisions 129 and 139).
func inlineReviewComments(findings []report.Finding, diffMap *github.DiffMap, minSeverity report.Severity, minConfidence report.Confidence) []github.ReviewComment {
	var comments []github.ReviewComment
	for _, f := range findings {
		// A finding whose quote pins no single place gets no comment on a line
		// it may not mean; the summary comment already lists it (decision 139).
		if !inSeveritySection(f, minSeverity, minConfidence) || f.LineCheck == report.LineCheckUnanchored {
			continue
		}
		if f.File == "" || f.Line <= 0 || !diffMap.Contains(f.File, f.Line) {
			continue
		}
		if len(comments) >= maxInlineComments {
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
		comments = append(comments, comment)
	}
	return comments
}

// postInlineReview posts summaryBody, the report and its data block, as the
// summary comment PostPRComment edits in place, and the findings of
// displayResult that inlineReviewComments places on a diff line as inline
// comments in a review of their own, posted only when there is one. The
// comment goes first, so on a first run it sits above the review in the
// timeline. When the comment fails, the review carries summaryBody instead and
// is posted even without inline comments, so the data block still reaches the
// PR. A failed diff fetch posts no inline comments. It returns an error only
// when neither the comment nor the review reached the PR (decision 133).
func (r *Runner) postInlineReview(displayResult *report.ReviewResult, pr *github.PR, minSeverity report.Severity, minConfidence report.Confidence, summaryBody string) error {
	summaryURL, summaryErr := r.GitHub.PostPRComment(pr.Owner, pr.Repo, pr.Number, summaryBody)
	if summaryErr != nil {
		slog.Warn("posting the summary comment failed; the review carries the summary", "err", summaryErr)
	} else {
		slog.Info("review summary posted", "url", summaryURL)
	}

	var comments []github.ReviewComment
	if rawDiff, err := r.GitHub.FetchDiff(pr.Owner, pr.Repo, pr.Number); err != nil {
		slog.Warn("fetching the diff failed; posting no inline comments", "err", err)
	} else {
		comments = inlineReviewComments(displayResult.Findings, github.ParseDiff(rawDiff), minSeverity, minConfidence)
	}

	body := summaryBody
	if summaryErr == nil {
		if len(comments) == 0 {
			return nil
		}
		body = inlineReviewBody(len(comments), pr.HeadSHA, summaryURL)
	}
	url, err := r.GitHub.SubmitPRReview(pr.Owner, pr.Repo, pr.Number, pr.HeadSHA, body, comments)
	if err != nil {
		if summaryErr != nil {
			return fmt.Errorf("posting the review: summary comment: %w; review: %w", summaryErr, err)
		}
		slog.Warn("posting the inline comments failed; the summary comment carries every finding", "err", err)
		return nil
	}
	slog.Info("inline review posted", "url", url, "comments", len(comments))
	return nil
}

// inlineReviewBody is the body of a review that carries inline comments only:
// their count, the commit they were posted at, and a pointer to the summary
// comment that holds the report and the data block. summaryURL becomes a link
// only when it starts with https:// and holds no whitespace; anything else gh
// printed is left out, and the body names the comment instead. SubmitPRReview
// appends the inline signature (decision 133).
func inlineReviewBody(comments int, headSHA, summaryURL string) string {
	summary := "the planwerk-agent comment on this pull request"
	if strings.HasPrefix(summaryURL, "https://") && !strings.ContainsFunc(summaryURL, unicode.IsSpace) {
		summary = "[the planwerk-agent comment](" + summaryURL + ")"
	}
	return fmt.Sprintf("planwerk-agent posted %d inline comment(s) at %s. The summary and every finding are in %s.",
		comments, report.ShortSHA(headSHA), summary)
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

// hexObjectName matches the commit_sha RenderDataBlock records: a hex git object
// name. skipSuppressed passes the prior block's SHA to git diff as an argument,
// so priorFindings refuses anything else, such as a forged "--output=<path>"
// that git would read as an option (decision 128).
var hexObjectName = regexp.MustCompile(`^[0-9a-f]{7,64}$`)

// suppressed holds the findings a re-review leaves out of the report sections,
// by reason. unchanged holds findings the prior data block carried whose file
// is unchanged since its commit; dismissed holds findings whose own inline
// thread another account resolved while the quoted code is still in the file.
type suppressed struct {
	unchanged, dismissed []report.Finding
}

// total is the number of findings left out for either reason.
func (s suppressed) total() int {
	return len(s.unchanged) + len(s.dismissed)
}

// skipSuppressed returns the current findings a posted re-review still lists,
// and the ones it leaves out, in two independent stages (decision 132):
//
//   - Stage 1 suppresses the findings the prior data block carried
//     (priorFindings) on files unchanged since that block's commit. When the
//     block cannot be read or the diff since its commit cannot be computed, it
//     suppresses nothing, so nothing is hidden on a bad signal.
//   - Stage 2 runs whether or not stage 1 did. It reads the PR's review threads
//     and dismisses the findings that dismissedFingerprints traces to a
//     resolved thread of planwerk-agent's own, while hygiene.SnippetInFile still
//     finds the whole quoted code in the file at pr.Dir, changed or not.
//
// Both stages leave out only a finding that meets minSeverity and
// minConfidence, one the report would show, so a repeat below them never takes
// the place of a listed one. An earlier finding leaves out at most one current
// finding across both stages: dismissedFingerprints counts it once however
// many runs posted it, and the data block carries the finding its resolved
// thread was posted for, so each finding stage 1 left out takes one dismissal
// off its fingerprint before stage 2 runs. threads is the list stage 2 fetched,
// nil when FetchReviewThreads failed; a failure is logged and dismisses
// nothing.
func (r *Runner) skipSuppressed(result *report.ReviewResult, pr *github.PR, minSeverity report.Severity, minConfidence report.Confidence) (kept []report.Finding, s suppressed, threads []github.ReviewThread) {
	listed := func(f report.Finding) bool { return f.MeetsThresholds(minSeverity, minConfidence) }
	kept = result.Findings
	if priorSHA, prior, ok := r.priorFindings(pr, minSeverity, minConfidence); ok {
		if changed, ok := changedFilesSince(pr.Dir, priorSHA); ok {
			kept, s.unchanged = report.FilterPreviouslyReported(kept, prior, listed, func(file string) bool {
				return !changed[file]
			})
		}
	}

	threads, err := r.GitHub.FetchReviewThreads(pr.Owner, pr.Repo, pr.Number)
	if err != nil {
		slog.Warn("could not fetch review threads; resolved threads dismiss nothing", "err", err)
		return kept, s, nil
	}
	fps := dismissedFingerprints(threads)
	// A finding stage 1 left out already spent the earlier finding its own
	// thread was posted for.
	for _, f := range s.unchanged {
		if fp := report.FindingFingerprint(f); fps[fp] > 0 {
			fps[fp]--
		}
	}
	if len(fps) > 0 {
		kept, s.dismissed = report.FilterDismissed(kept, fps, listed, func(f report.Finding) bool {
			return hygiene.SnippetInFile(pr.Dir, f.File, f.CodeSnippet)
		})
	}
	return kept, s, threads
}

// priorFindings loads the prior review comment's data block and returns the
// commit SHA it was posted at and the findings a re-review counts as already
// reported. The block carries every finding of the prior run, but only the ones
// inSeveritySection admits count as seen: one the thresholds hid, or one the
// summary listed as Unverified, was never reported as a finding to act on, so
// it suppresses nothing and is reported when it comes back confirmed (decision
// 129). The block does not record the prior run's thresholds; minSeverity and
// minConfidence, this run's, stand in for them.
// ok is false on any failure (no prior comment, unparseable data block, a
// commit_sha that is not a hex object name) and when no finding counts.
func (r *Runner) priorFindings(pr *github.PR, minSeverity report.Severity, minConfidence report.Confidence) (sha string, findings []report.Finding, ok bool) {
	body, found, err := r.GitHub.FetchReviewComment(pr.Owner, pr.Repo, pr.Number)
	if err != nil {
		slog.Warn("could not fetch prior review comment; previously reported findings are not suppressed", "err", err)
		return "", nil, false
	}
	if !found {
		return "", nil, false
	}
	sha, findings, ok = report.ParseDataBlock(body)
	findings = slices.DeleteFunc(findings, func(p report.Finding) bool {
		return !inSeveritySection(p, minSeverity, minConfidence)
	})
	if !ok || !hexObjectName.MatchString(sha) || len(findings) == 0 {
		return "", nil, false
	}
	return sha, findings, true
}

// dismissedFingerprints counts, per report.FindingFingerprint, the earlier
// findings that resolved review threads dismiss. A thread dismisses when it is
// planwerk-agent's own (github.IsOwnThread), its first comment carries a
// finding marker (report.ParseFindingMarker), and an account other than the
// one that posted that comment resolved it. A resolve never dismisses when it
// comes from the posting identity, compared case-insensitively, or when GitHub
// reports no resolver, as for the GitHub Actions bot: that rule is what keeps
// --resolve-outdated's own resolves (decision 133) from hiding live findings.
// An open own thread with the same marker does not cancel a dismissal, since
// that thread is still visible on the PR. A comment posted without a marker
// dismisses nothing.
//
// The count is the most dismissing threads any one review opened for the
// fingerprint, not their sum: a run posts a finding again when its file
// changed while its thread was still open, so threads from several reviews can
// stand for one earlier finding, while two findings on the same code in one
// review are two. A thread whose first comment GitHub names no review for
// counts as a review of its own, so it never adds to another (decision 132).
func dismissedFingerprints(threads []github.ReviewThread) map[string]int {
	type opened struct{ fp, review string }
	perReview := make(map[opened]int)
	counts := make(map[string]int)
	for _, t := range threads {
		if !github.IsOwnThread(t) || !t.IsResolved || t.ResolvedBy == "" {
			continue
		}
		first := t.Comments[0]
		if strings.EqualFold(t.ResolvedBy, first.Author) {
			continue
		}
		fp, ok := report.ParseFindingMarker(first.Body)
		if !ok {
			continue
		}
		n := 1
		if first.ReviewID != "" {
			k := opened{fp, first.ReviewID}
			perReview[k]++
			n = perReview[k]
		}
		counts[fp] = max(counts[fp], n)
	}
	return counts
}

// changedFilesSince returns the set of files changed between sha and HEAD. ok is
// false when the diff cannot be computed (e.g. the prior SHA is not in this
// checkout), in which case the caller must not suppress anything. Keys are
// the paths as stored, whitespace at either end included, so a non-ASCII path
// matches a finding's File (decision 131).
func changedFilesSince(dir, sha string) (map[string]bool, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), gitLogTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-c", "core.quotePath=false", "diff", "--name-only", sha, "HEAD")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return nil, false
	}
	changed := make(map[string]bool)
	for _, line := range strings.Split(string(out), "\n") {
		if line != "" {
			changed[line] = true
		}
	}
	return changed, true
}

// reasonPreviouslyReported and reasonDismissedInThread follow the count in the
// suppression note's group headers. The first keeps the wording of the note's
// single header from before resolved threads dismissed anything.
const (
	reasonPreviouslyReported = "suppressed as previously reported on unchanged files since the last review"
	reasonDismissedInThread  = "suppressed because their inline thread was resolved and the quoted code is unchanged"
)

// writeSuppressionNote renders one [!NOTE] alert listing the findings a
// re-review left out, so nothing is hidden silently. It holds a group per
// non-empty reason, previously reported first, separated by a bare ">" line.
func writeSuppressionNote(w io.Writer, s suppressed) {
	_, _ = fmt.Fprintln(w, "> [!NOTE]")
	groups := []struct {
		reason   string
		findings []report.Finding
	}{
		{reasonPreviouslyReported, s.unchanged},
		{reasonDismissedInThread, s.dismissed},
	}
	written := false
	for _, g := range groups {
		if len(g.findings) == 0 {
			continue
		}
		if written {
			_, _ = fmt.Fprintln(w, ">")
		}
		written = true
		_, _ = fmt.Fprintf(w, "> %d finding(s) %s:\n", len(g.findings), g.reason)
		for _, f := range g.findings {
			_, _ = fmt.Fprintf(w, "> - %s", f.Title)
			if f.File != "" {
				_, _ = fmt.Fprintf(w, " (%s)", f.File)
			}
			_, _ = fmt.Fprintln(w)
		}
	}
	_, _ = fmt.Fprintln(w)
}

// getCommitLog returns the one-line commit log between the base branch and HEAD.
// On subprocess failure it returns an empty string and an error wrapping git's
// stderr, so the caller can log the cause before degrading gracefully.
func getCommitLog(dir, baseBranch string) (string, error) {
	if baseBranch == "" {
		baseBranch = claude.DefaultBaseBranch
	}
	ctx, cancel := context.WithTimeout(context.Background(), gitLogTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "log", "origin/"+baseBranch+"..HEAD", "--oneline")
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git log origin/%s..HEAD: %w: %s", baseBranch, err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(string(out)), nil
}
