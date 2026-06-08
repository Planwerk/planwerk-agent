package propose

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/planwerk/planwerk-review/internal/cache"
	"github.com/planwerk/planwerk-review/internal/github"
)

// fakeGitHub is a test GitHubClient whose CloneRepo / DefaultBranchHEAD /
// ListExistingIssues behavior is configured per-test via closures. A nil
// listExistingIssues returns no existing issues so dedupe is a no-op.
type fakeGitHub struct {
	cloneRepo          func(ref string) (*github.Repo, error)
	defaultBranchHEAD  func(owner, name string) (string, error)
	listExistingIssues func(owner, name string) ([]github.ExistingIssue, error)

	cloneCalls      atomic.Int32
	cloneLocalCalls atomic.Int32
}

func (f *fakeGitHub) CloneRepo(ref string) (*github.Repo, error) {
	f.cloneCalls.Add(1)
	return f.cloneRepo(ref)
}

// CloneRepoLocal mirrors github.UseLocalRepo: it returns a Local repo rooted
// at the same dir the temp-clone factory would, so Cleanup is a no-op.
func (f *fakeGitHub) CloneRepoLocal(ref string, _ github.LocalOptions) (*github.Repo, error) {
	f.cloneLocalCalls.Add(1)
	repo, err := f.cloneRepo(ref)
	if err != nil {
		return nil, err
	}
	repo.Local = true
	return repo, nil
}

func (f *fakeGitHub) DefaultBranchHEAD(owner, name string) (string, error) {
	return f.defaultBranchHEAD(owner, name)
}

func (f *fakeGitHub) ListExistingIssues(owner, name string) ([]github.ExistingIssue, error) {
	if f.listExistingIssues == nil {
		return nil, nil
	}
	return f.listExistingIssues(owner, name)
}

// fakeClaude is a test ClaudeAnalyzer tracking call count so cache-hit tests
// can assert Claude was skipped.
type fakeClaude struct {
	calls   int32
	lastCtx AnalysisContext
	fn      func(dir string, ctx AnalysisContext) (*ProposalResult, error)
}

func (f *fakeClaude) Analyze(dir string, ctx AnalysisContext) (*ProposalResult, error) {
	atomic.AddInt32(&f.calls, 1)
	f.lastCtx = ctx
	if f.fn == nil {
		return &ProposalResult{RepositoryOverview: "ok"}, nil
	}
	return f.fn(dir, ctx)
}

func fakeRepo(t *testing.T, owner, name string) *github.Repo {
	t.Helper()
	return &github.Repo{
		Owner: owner,
		Name:  name,
		Dir:   t.TempDir(),
	}
}

func baseProposeOpts() Options {
	return Options{
		RepoRef: "owner/repo",
		Format:  "markdown",
		Version: "test",
	}
}

func TestProposeRun_CacheMissThenHit(t *testing.T) {
	// Not t.Parallel(): cache.SetDir mutates a package-level variable.
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	repo := fakeRepo(t, "acme", "widgets")
	gh := &fakeGitHub{
		cloneRepo:         func(ref string) (*github.Repo, error) { return repo, nil },
		defaultBranchHEAD: func(owner, name string) (string, error) { return "sha-propose-1", nil },
	}
	claudeMock := &fakeClaude{
		fn: func(dir string, _ AnalysisContext) (*ProposalResult, error) {
			return &ProposalResult{
				RepositoryOverview: "Proposal overview",
				Proposals: []Proposal{
					{ID: "H-001", Priority: "HIGH", Category: "feature", Title: "Add thing", Description: "d", Motivation: "m", Scope: "Small"},
				},
			}, nil
		},
	}
	runner := &Runner{Claude: claudeMock, GitHub: gh}

	var out bytes.Buffer
	if err := runner.Run(&out, baseProposeOpts()); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if claudeMock.calls != 1 {
		t.Errorf("Analyze calls = %d, want 1", claudeMock.calls)
	}
	if !strings.Contains(out.String(), "Proposal overview") {
		t.Errorf("output missing overview, got:\n%s", out.String())
	}

	// Second run: cache should short-circuit Claude entirely.
	claudeMock2 := &fakeClaude{
		fn: func(dir string, _ AnalysisContext) (*ProposalResult, error) {
			t.Fatal("Analyze must not be called on cache hit")
			return nil, nil
		},
	}
	runner2 := &Runner{Claude: claudeMock2, GitHub: gh}
	var out2 bytes.Buffer
	if err := runner2.Run(&out2, baseProposeOpts()); err != nil {
		t.Fatalf("cache-hit Run returned error: %v", err)
	}
	if !strings.Contains(out2.String(), "Proposal overview") {
		t.Errorf("cache-hit output missing overview, got:\n%s", out2.String())
	}
}

func TestProposeRun_NoCacheBypassesCachedEntry(t *testing.T) {
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	gh := &fakeGitHub{
		cloneRepo:         func(ref string) (*github.Repo, error) { return fakeRepo(t, "acme", "widgets"), nil },
		defaultBranchHEAD: func(owner, name string) (string, error) { return "sha-propose-nocache", nil },
	}

	// Seed the cache with a sentinel that NoCache must ignore. Cache key owner/name
	// follow ParseRepoRef(opts.RepoRef) = ("owner", "repo").
	cacheKey := cache.RepoKey("owner", "repo", "sha-propose-nocache")
	if err := cache.PutRaw(cacheKey, cache.CommandPropose, []byte(`{"repository_overview":"CACHED SENTINEL"}`)); err != nil {
		t.Fatalf("seeding cache: %v", err)
	}

	claudeMock := &fakeClaude{
		fn: func(dir string, _ AnalysisContext) (*ProposalResult, error) {
			return &ProposalResult{RepositoryOverview: "Fresh overview"}, nil
		},
	}
	runner := &Runner{Claude: claudeMock, GitHub: gh}

	opts := baseProposeOpts()
	opts.NoCache = true
	var out bytes.Buffer
	if err := runner.Run(&out, opts); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if claudeMock.calls != 1 {
		t.Errorf("Analyze should run with NoCache=true, got %d calls", claudeMock.calls)
	}
	if strings.Contains(out.String(), "CACHED SENTINEL") {
		t.Error("NoCache run rendered cached sentinel; expected fresh output")
	}
	if !strings.Contains(out.String(), "Fresh overview") {
		t.Errorf("output missing fresh overview, got:\n%s", out.String())
	}
}

func TestProposeRun_HEADFailureDisablesCaching(t *testing.T) {
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	gh := &fakeGitHub{
		cloneRepo: func(ref string) (*github.Repo, error) { return fakeRepo(t, "acme", "widgets"), nil },
		defaultBranchHEAD: func(owner, name string) (string, error) {
			return "", errors.New("network unreachable")
		},
	}
	claudeMock := &fakeClaude{}
	runner := &Runner{Claude: claudeMock, GitHub: gh}

	var out bytes.Buffer
	if err := runner.Run(&out, baseProposeOpts()); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if claudeMock.calls != 1 {
		t.Errorf("Analyze calls = %d, want 1", claudeMock.calls)
	}

	// Running again still calls Claude since nothing was cached.
	if err := runner.Run(&bytes.Buffer{}, baseProposeOpts()); err != nil {
		t.Fatalf("Run (second) returned error: %v", err)
	}
	if claudeMock.calls != 2 {
		t.Errorf("Analyze calls after HEAD failure = %d, want 2", claudeMock.calls)
	}
}

func TestProposeRun_CloneErrorFailsFast(t *testing.T) {
	// With the reordered flow, HEAD resolution runs before clone so the cache
	// can short-circuit a hit; clone failure must still bubble up when the
	// cache misses.
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	gh := &fakeGitHub{
		cloneRepo:         func(ref string) (*github.Repo, error) { return nil, errors.New("clone failed") },
		defaultBranchHEAD: func(owner, name string) (string, error) { return "sha", nil },
	}
	runner := &Runner{Claude: &fakeClaude{}, GitHub: gh}

	var out bytes.Buffer
	err := runner.Run(&out, baseProposeOpts())
	if err == nil {
		t.Fatal("expected error when CloneRepo fails")
	}
	if !strings.Contains(err.Error(), "cloning repo") {
		t.Errorf("error should wrap clone failure, got: %v", err)
	}
}

func TestProposeRun_CacheHitSkipsClone(t *testing.T) {
	// Cache hits must not invoke CloneRepo — that's the whole point of the
	// ls-remote-first reordering.
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	// baseProposeOpts uses RepoRef "owner/repo", so the cache key owner/name
	// must match ParseRepoRef("owner/repo") = ("owner", "repo").
	cacheKey := cache.RepoKey("owner", "repo", "sha-skip-clone")
	if err := cache.PutRaw(cacheKey, cache.CommandPropose, []byte(`{"repository_overview":"Cached overview"}`)); err != nil {
		t.Fatalf("seeding cache: %v", err)
	}

	gh := &fakeGitHub{
		cloneRepo: func(ref string) (*github.Repo, error) {
			t.Fatal("CloneRepo must not be called on cache hit")
			return nil, nil
		},
		defaultBranchHEAD: func(owner, name string) (string, error) { return "sha-skip-clone", nil },
	}
	claudeMock := &fakeClaude{
		fn: func(dir string, _ AnalysisContext) (*ProposalResult, error) {
			t.Fatal("Analyze must not be called on cache hit")
			return nil, nil
		},
	}
	runner := &Runner{Claude: claudeMock, GitHub: gh}

	var out bytes.Buffer
	if err := runner.Run(&out, baseProposeOpts()); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !strings.Contains(out.String(), "Cached overview") {
		t.Errorf("output missing cached overview, got:\n%s", out.String())
	}
}

func TestProposeRun_InvalidRepoRefFailsBeforeHEAD(t *testing.T) {
	gh := &fakeGitHub{
		cloneRepo: func(ref string) (*github.Repo, error) {
			t.Fatal("CloneRepo must not be called for invalid repo ref")
			return nil, nil
		},
		defaultBranchHEAD: func(owner, name string) (string, error) {
			t.Fatal("DefaultBranchHEAD must not be called for invalid repo ref")
			return "", nil
		},
	}
	runner := &Runner{Claude: &fakeClaude{}, GitHub: gh}

	opts := baseProposeOpts()
	opts.RepoRef = "not a valid ref"
	var out bytes.Buffer
	err := runner.Run(&out, opts)
	if err == nil {
		t.Fatal("expected error for invalid repo ref")
	}
	if !strings.Contains(err.Error(), "parsing repo ref") {
		t.Errorf("error should wrap parse failure, got: %v", err)
	}
}

func TestProposeRun_AnalyzeErrorPropagates(t *testing.T) {
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	gh := &fakeGitHub{
		cloneRepo:         func(ref string) (*github.Repo, error) { return fakeRepo(t, "acme", "widgets"), nil },
		defaultBranchHEAD: func(owner, name string) (string, error) { return "sha", nil },
	}
	claudeMock := &fakeClaude{
		fn: func(dir string, _ AnalysisContext) (*ProposalResult, error) {
			return nil, errors.New("claude exploded")
		},
	}
	runner := &Runner{Claude: claudeMock, GitHub: gh}

	var out bytes.Buffer
	err := runner.Run(&out, baseProposeOpts())
	if err == nil {
		t.Fatal("expected error when Analyze fails")
	}
	if !strings.Contains(err.Error(), "claude analysis") {
		t.Errorf("error should wrap claude analysis failure, got: %v", err)
	}
}

func TestProposeRun_FiltersProposalsMatchingExistingIssues(t *testing.T) {
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	repo := fakeRepo(t, "acme", "widgets")
	gh := &fakeGitHub{
		cloneRepo:         func(ref string) (*github.Repo, error) { return repo, nil },
		defaultBranchHEAD: func(owner, name string) (string, error) { return "sha-dedupe", nil },
		listExistingIssues: func(owner, name string) ([]github.ExistingIssue, error) {
			return []github.ExistingIssue{
				{Title: "Add LOGGING.", URL: "https://example/1"},
			}, nil
		},
	}
	claudeMock := &fakeClaude{
		fn: func(dir string, _ AnalysisContext) (*ProposalResult, error) {
			return &ProposalResult{
				RepositoryOverview: "overview",
				Proposals: []Proposal{
					{ID: "P-1", Priority: "HIGH", Title: "Add logging", Description: "d", Motivation: "m"},
					{ID: "P-2", Priority: "HIGH", Title: "Add metrics", Description: "d", Motivation: "m"},
				},
			}, nil
		},
	}
	runner := &Runner{Claude: claudeMock, GitHub: gh}

	var out bytes.Buffer
	if err := runner.Run(&out, baseProposeOpts()); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	s := out.String()
	if strings.Contains(s, "Add logging") {
		t.Errorf("deduped proposal still present in output:\n%s", s)
	}
	if !strings.Contains(s, "Add metrics") {
		t.Errorf("non-duplicate proposal missing from output:\n%s", s)
	}
}

func TestProposeRun_DedupeDisabledByFlag(t *testing.T) {
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	repo := fakeRepo(t, "acme", "widgets")
	listerCalled := false
	gh := &fakeGitHub{
		cloneRepo:         func(ref string) (*github.Repo, error) { return repo, nil },
		defaultBranchHEAD: func(owner, name string) (string, error) { return "sha-nodedupe", nil },
		listExistingIssues: func(owner, name string) ([]github.ExistingIssue, error) {
			listerCalled = true
			return []github.ExistingIssue{{Title: "Add logging", URL: "u"}}, nil
		},
	}
	claudeMock := &fakeClaude{
		fn: func(dir string, _ AnalysisContext) (*ProposalResult, error) {
			return &ProposalResult{Proposals: []Proposal{
				{ID: "P-1", Priority: "HIGH", Title: "Add logging", Description: "d", Motivation: "m"},
			}}, nil
		},
	}
	runner := &Runner{Claude: claudeMock, GitHub: gh}

	opts := baseProposeOpts()
	opts.NoIssueDedupe = true
	var out bytes.Buffer
	if err := runner.Run(&out, opts); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if listerCalled {
		t.Error("ListExistingIssues must not be called when NoIssueDedupe=true")
	}
	if !strings.Contains(out.String(), "Add logging") {
		t.Errorf("proposal must be kept when dedupe is disabled:\n%s", out.String())
	}
}

func TestProposeRun_DedupeListerErrorIsNonFatal(t *testing.T) {
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	repo := fakeRepo(t, "acme", "widgets")
	gh := &fakeGitHub{
		cloneRepo:         func(ref string) (*github.Repo, error) { return repo, nil },
		defaultBranchHEAD: func(owner, name string) (string, error) { return "sha-listerr", nil },
		listExistingIssues: func(owner, name string) ([]github.ExistingIssue, error) {
			return nil, errors.New("gh boom")
		},
	}
	claudeMock := &fakeClaude{
		fn: func(dir string, _ AnalysisContext) (*ProposalResult, error) {
			return &ProposalResult{Proposals: []Proposal{
				{ID: "P-1", Priority: "HIGH", Title: "Add logging", Description: "d", Motivation: "m"},
			}}, nil
		},
	}
	runner := &Runner{Claude: claudeMock, GitHub: gh}

	var out bytes.Buffer
	if err := runner.Run(&out, baseProposeOpts()); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !strings.Contains(out.String(), "Add logging") {
		t.Errorf("proposal must survive dedupe-lister failure:\n%s", out.String())
	}
}

func TestProposeRun_LoadsPatternsIntoAnalysisContext(t *testing.T) {
	// Proposer.Run must load patterns from the pattern directories and forward
	// them into AnalysisContext so Claude's analysis prompt can ground
	// proposals in the pattern catalog.
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	patternDir := t.TempDir()
	patternFile := filepath.Join(patternDir, "sample.md")
	patternMD := "# Review Pattern: Sample Pattern\n" +
		"\n" +
		"**Review-Area**: testing\n" +
		"**Detection-Hint**: sample\n" +
		"**Severity**: INFO\n" +
		"**Category**: design-principle\n" +
		"\n" +
		"## Rule\nBe sampled.\n"
	if err := os.WriteFile(patternFile, []byte(patternMD), 0o644); err != nil {
		t.Fatalf("writing pattern file: %v", err)
	}

	gh := &fakeGitHub{
		cloneRepo:         func(ref string) (*github.Repo, error) { return fakeRepo(t, "acme", "widgets"), nil },
		defaultBranchHEAD: func(owner, name string) (string, error) { return "sha-patterns", nil },
	}
	claudeMock := &fakeClaude{}
	runner := &Runner{Claude: claudeMock, GitHub: gh}

	opts := baseProposeOpts()
	opts.PatternDirs = []string{patternDir}
	opts.NoLocalPatterns = true
	opts.NoRepoPatterns = true

	var out bytes.Buffer
	if err := runner.Run(&out, opts); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if len(claudeMock.lastCtx.Patterns) != 1 {
		t.Fatalf("AnalysisContext.Patterns len = %d, want 1", len(claudeMock.lastCtx.Patterns))
	}
	if got := claudeMock.lastCtx.Patterns[0].Name; got != "Sample Pattern" {
		t.Errorf("AnalysisContext.Patterns[0].Name = %q, want %q", got, "Sample Pattern")
	}
	if claudeMock.lastCtx.RepoName != "acme/widgets" {
		t.Errorf("AnalysisContext.RepoName = %q, want acme/widgets", claudeMock.lastCtx.RepoName)
	}
}

func TestProposeRun_NoPatternsIsNonFatal(t *testing.T) {
	// Missing patterns must not fail the pipeline — propose should still run
	// with an empty Patterns slice, logging a warning.
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	gh := &fakeGitHub{
		cloneRepo:         func(ref string) (*github.Repo, error) { return fakeRepo(t, "acme", "widgets"), nil },
		defaultBranchHEAD: func(owner, name string) (string, error) { return "sha-nopat", nil },
	}
	claudeMock := &fakeClaude{}
	runner := &Runner{Claude: claudeMock, GitHub: gh}

	opts := baseProposeOpts()
	opts.NoLocalPatterns = true
	opts.NoRepoPatterns = true

	var out bytes.Buffer
	if err := runner.Run(&out, opts); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if len(claudeMock.lastCtx.Patterns) != 0 {
		t.Errorf("expected no patterns loaded, got %d", len(claudeMock.lastCtx.Patterns))
	}
	if claudeMock.calls != 1 {
		t.Errorf("Analyze calls = %d, want 1", claudeMock.calls)
	}
}

func TestProposeRun_CorruptedCacheFallsBackToFreshAnalysis(t *testing.T) {
	// When a cached entry is not valid JSON, Run must log a warning and
	// re-run the analysis rather than returning an error.
	cacheDir := t.TempDir()
	restore := cache.SetDir(cacheDir)
	t.Cleanup(restore)

	gh := &fakeGitHub{
		cloneRepo:         func(ref string) (*github.Repo, error) { return fakeRepo(t, "acme", "widgets"), nil },
		defaultBranchHEAD: func(owner, name string) (string, error) { return "sha-corrupt", nil },
	}
	// Write a corrupt file directly, bypassing the envelope-writing API so the
	// stored bytes cannot be unmarshalled. Cache key owner/name follow
	// ParseRepoRef(opts.RepoRef) = ("owner", "repo").
	cacheKey := cache.RepoKey("owner", "repo", "sha-corrupt")
	if err := os.WriteFile(filepath.Join(cacheDir, cacheKey+".json"), []byte("not valid json{"), 0o600); err != nil {
		t.Fatalf("seeding cache: %v", err)
	}

	claudeMock := &fakeClaude{
		fn: func(dir string, _ AnalysisContext) (*ProposalResult, error) {
			return &ProposalResult{RepositoryOverview: "Recovered overview"}, nil
		},
	}
	runner := &Runner{Claude: claudeMock, GitHub: gh}

	var out bytes.Buffer
	if err := runner.Run(&out, baseProposeOpts()); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if claudeMock.calls != 1 {
		t.Errorf("Analyze should run after corrupted cache, got %d calls", claudeMock.calls)
	}
	if !strings.Contains(out.String(), "Recovered overview") {
		t.Errorf("output missing fresh overview after cache corruption, got:\n%s", out.String())
	}
}

func TestProposeRun_LocalUsesCwdAndKeepsTree(t *testing.T) {
	// Not t.Parallel(): cache.SetDir mutates a package-level variable.
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	dir := t.TempDir()
	gh := &fakeGitHub{
		cloneRepo: func(ref string) (*github.Repo, error) {
			return &github.Repo{Owner: "acme", Name: "widgets", Dir: dir}, nil
		},
		defaultBranchHEAD: func(owner, name string) (string, error) { return "sha-local", nil },
	}
	runner := &Runner{Claude: &fakeClaude{}, GitHub: gh}

	opts := baseProposeOpts()
	opts.Local = true
	opts.NoCache = true

	if err := runner.Run(io.Discard, opts); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if gh.cloneLocalCalls.Load() != 1 {
		t.Errorf("CloneRepoLocal calls = %d, want 1", gh.cloneLocalCalls.Load())
	}
	if gh.cloneCalls.Load() != 0 {
		t.Errorf("CloneRepo (temp-dir clone) calls = %d, want 0 in local mode", gh.cloneCalls.Load())
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("local checkout must survive the run (Cleanup is a no-op): %v", err)
	}
}
