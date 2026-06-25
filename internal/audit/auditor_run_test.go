package audit

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/planwerk/planwerk-review/internal/cache"
	"github.com/planwerk/planwerk-review/internal/capture"
	"github.com/planwerk/planwerk-review/internal/github"
	"github.com/planwerk/planwerk-review/internal/patterns"
	"github.com/planwerk/planwerk-review/internal/report"
)

const testDefaultBranchSHA = "sha"

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

// CloneRepoLocal mirrors github.UseLocalRepo: it returns a Local repo so
// Cleanup is a no-op.
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

// fakeClaude is a test ClaudeAuditor. Audit records how many times it was
// called so cache-hit tests can assert Claude was skipped.
type fakeClaude struct {
	calls int32
	fn    func(dir string, ctx AuditContext) (*report.ReviewResult, error)
}

func (f *fakeClaude) Audit(dir string, ctx AuditContext) (*report.ReviewResult, error) {
	atomic.AddInt32(&f.calls, 1)
	if f.fn == nil {
		return &report.ReviewResult{Summary: "audit ok"}, nil
	}
	return f.fn(dir, ctx)
}

// seedPatternDir writes a single minimal pattern file into dir so
// patterns.LoadFiltered returns at least one pattern (audit.Run errors if the
// pattern set is empty).
func seedPatternDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	body := "# Review Pattern: Test Pattern\n\n**Review-Area**: testing\n\n## What to check\n\n- presence\n"
	if err := os.WriteFile(filepath.Join(dir, "test.md"), []byte(body), 0o600); err != nil {
		t.Fatalf("writing pattern: %v", err)
	}
	return dir
}

// fakeRepo returns a *github.Repo whose Dir is a real temp directory so
// detect.Technologies and pattern loading can walk it safely.
func fakeRepo(t *testing.T, owner, name string) *github.Repo {
	t.Helper()
	return &github.Repo{
		Owner: owner,
		Name:  name,
		Dir:   t.TempDir(),
	}
}

func baseAuditOpts(patternDir string) Options {
	return Options{
		RepoRef:         "owner/repo",
		NoLocalPatterns: true,
		NoRepoPatterns:  true,
		PatternDirs:     []string{patternDir},
		Format:          "markdown",
		Version:         "test",
		MinSeverity:     report.SeverityInfo,
	}
}

func TestAuditRun_CacheMissThenHit(t *testing.T) {
	// Not t.Parallel(): cache.SetDir mutates a package-level variable.
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	patternDir := seedPatternDir(t)
	repo := fakeRepo(t, "acme", "widgets")

	gh := &fakeGitHub{
		cloneRepo:         func(ref string) (*github.Repo, error) { return repo, nil },
		defaultBranchHEAD: func(owner, name string) (string, error) { return "sha-audit-1", nil },
	}
	claudeMock := &fakeClaude{
		fn: func(dir string, ctx AuditContext) (*report.ReviewResult, error) {
			return &report.ReviewResult{Summary: "Audit summary", Findings: []report.Finding{
				{ID: "W-001", Severity: report.SeverityWarning, Title: "t", File: "f.go", Line: 1, Problem: "p", Action: "a"},
			}}, nil
		},
	}
	runner := &Runner{Claude: claudeMock, GitHub: gh}

	var out bytes.Buffer
	if err := runner.Run(&out, baseAuditOpts(patternDir)); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if claudeMock.calls != 1 {
		t.Errorf("Audit calls = %d, want 1", claudeMock.calls)
	}
	if !strings.Contains(out.String(), "Audit summary") {
		t.Errorf("output missing summary, got:\n%s", out.String())
	}

	// Second Run with the same owner/repo/headSHA must hit the cache.
	claudeMock2 := &fakeClaude{
		fn: func(dir string, ctx AuditContext) (*report.ReviewResult, error) {
			t.Fatal("Audit must not be called on cache hit")
			return nil, nil
		},
	}
	runner2 := &Runner{Claude: claudeMock2, GitHub: gh}
	var out2 bytes.Buffer
	if err := runner2.Run(&out2, baseAuditOpts(patternDir)); err != nil {
		t.Fatalf("cache-hit Run returned error: %v", err)
	}
	if !strings.Contains(out2.String(), "Audit summary") {
		t.Errorf("cache-hit output missing summary, got:\n%s", out2.String())
	}
}

func TestAuditRun_NoCacheRefreshesResult(t *testing.T) {
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	patternDir := seedPatternDir(t)
	repo := fakeRepo(t, "acme", "widgets")
	gh := &fakeGitHub{
		cloneRepo:         func(ref string) (*github.Repo, error) { return repo, nil },
		defaultBranchHEAD: func(owner, name string) (string, error) { return "sha-audit-nocache", nil },
	}

	// Seed cache with sentinel to confirm NoCache bypasses it. Cache key owner/name
	// follow ParseRepoRef(opts.RepoRef) = ("owner", "repo").
	cacheKey := cache.AuditKey("owner", "repo", "sha-audit-nocache", "min="+string(report.SeverityInfo))
	if err := cache.PutRaw(cacheKey, cache.CommandAudit, []byte(`{"summary":"CACHED SENTINEL"}`)); err != nil {
		t.Fatalf("seeding cache: %v", err)
	}

	claudeMock := &fakeClaude{
		fn: func(dir string, ctx AuditContext) (*report.ReviewResult, error) {
			return &report.ReviewResult{Summary: "Fresh audit"}, nil
		},
	}
	runner := &Runner{Claude: claudeMock, GitHub: gh}

	opts := baseAuditOpts(patternDir)
	opts.NoCache = true
	var out bytes.Buffer
	if err := runner.Run(&out, opts); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if claudeMock.calls != 1 {
		t.Errorf("Audit should run with NoCache=true, got %d calls", claudeMock.calls)
	}
	if strings.Contains(out.String(), "CACHED SENTINEL") {
		t.Error("NoCache run rendered cached sentinel; expected fresh output")
	}
	if !strings.Contains(out.String(), "Fresh audit") {
		t.Errorf("output missing fresh summary, got:\n%s", out.String())
	}
}

func TestAuditRun_HEADFailureDisablesCaching(t *testing.T) {
	// When DefaultBranchHEAD fails, caching is disabled but the audit still
	// runs. Subsequent runs must re-execute Claude (no cache to hit).
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	patternDir := seedPatternDir(t)
	gh := &fakeGitHub{
		cloneRepo: func(ref string) (*github.Repo, error) { return fakeRepo(t, "acme", "widgets"), nil },
		defaultBranchHEAD: func(owner, name string) (string, error) {
			return "", errors.New("network unreachable")
		},
	}
	claudeMock := &fakeClaude{}
	runner := &Runner{Claude: claudeMock, GitHub: gh}

	var out bytes.Buffer
	if err := runner.Run(&out, baseAuditOpts(patternDir)); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if claudeMock.calls != 1 {
		t.Errorf("Audit calls = %d, want 1", claudeMock.calls)
	}

	// Second run must also call Claude since nothing was cached.
	var out2 bytes.Buffer
	if err := runner.Run(&out2, baseAuditOpts(patternDir)); err != nil {
		t.Fatalf("Run (second) returned error: %v", err)
	}
	if claudeMock.calls != 2 {
		t.Errorf("Audit calls after HEAD failure = %d, want 2 (no caching)", claudeMock.calls)
	}
}

func TestAuditRun_CloneErrorFailsFast(t *testing.T) {
	// With the reordered flow, HEAD resolution runs before clone so the cache
	// can short-circuit a hit; clone failure must still bubble up when the
	// cache misses.
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	gh := &fakeGitHub{
		cloneRepo:         func(ref string) (*github.Repo, error) { return nil, errors.New("clone failed") },
		defaultBranchHEAD: func(owner, name string) (string, error) { return testDefaultBranchSHA, nil },
	}
	runner := &Runner{Claude: &fakeClaude{}, GitHub: gh}

	var out bytes.Buffer
	err := runner.Run(&out, baseAuditOpts(t.TempDir()))
	if err == nil {
		t.Fatal("expected error when CloneRepo fails")
	}
	if !strings.Contains(err.Error(), "cloning repo") {
		t.Errorf("error should wrap clone failure, got: %v", err)
	}
}

func TestAuditRun_CacheHitSkipsClone(t *testing.T) {
	// Cache hits must not invoke CloneRepo — that's the whole point of the
	// ls-remote-first reordering. Patterns are also irrelevant on a hit.
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	// baseAuditOpts uses RepoRef "owner/repo", so the cache key owner/name
	// must match ParseRepoRef("owner/repo") = ("owner", "repo").
	cacheKey := cache.AuditKey("owner", "repo", "sha-skip-clone", "min="+string(report.SeverityInfo))
	if err := cache.PutRaw(cacheKey, cache.CommandAudit, []byte(`{"summary":"Cached audit"}`)); err != nil {
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
		fn: func(dir string, ctx AuditContext) (*report.ReviewResult, error) {
			t.Fatal("Audit must not be called on cache hit")
			return nil, nil
		},
	}
	runner := &Runner{Claude: claudeMock, GitHub: gh}

	// Pattern dir is empty — would normally error out, but we should never
	// reach the pattern-loading step on a cache hit.
	opts := baseAuditOpts(t.TempDir())
	var out bytes.Buffer
	if err := runner.Run(&out, opts); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !strings.Contains(out.String(), "Cached audit") {
		t.Errorf("output missing cached summary, got:\n%s", out.String())
	}
}

func TestAuditRun_InvalidRepoRefFailsBeforeHEAD(t *testing.T) {
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

	opts := baseAuditOpts(t.TempDir())
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

func TestAuditRun_ClaudeErrorPropagates(t *testing.T) {
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	patternDir := seedPatternDir(t)
	gh := &fakeGitHub{
		cloneRepo:         func(ref string) (*github.Repo, error) { return fakeRepo(t, "acme", "widgets"), nil },
		defaultBranchHEAD: func(owner, name string) (string, error) { return testDefaultBranchSHA, nil },
	}
	claudeMock := &fakeClaude{
		fn: func(dir string, ctx AuditContext) (*report.ReviewResult, error) {
			return nil, errors.New("claude exploded")
		},
	}
	runner := &Runner{Claude: claudeMock, GitHub: gh}

	var out bytes.Buffer
	err := runner.Run(&out, baseAuditOpts(patternDir))
	if err == nil {
		t.Fatal("expected error when Claude fails")
	}
	if !strings.Contains(err.Error(), "claude audit") {
		t.Errorf("error should wrap claude audit failure, got: %v", err)
	}
}

func TestAuditRun_FiltersFindingsMatchingExistingIssues(t *testing.T) {
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	patternDir := seedPatternDir(t)
	repo := fakeRepo(t, "acme", "widgets")

	// Two findings, one of which shares a group title with an existing issue.
	findings := []report.Finding{
		{ID: "W-1", Severity: report.SeverityWarning, Title: "Missing error check", Pattern: "err-check", File: "a.go", Line: 10, Problem: "p", Action: "a"},
		{ID: "W-2", Severity: report.SeverityWarning, Title: "Unused var", Pattern: "unused", File: "b.go", Line: 5, Problem: "p", Action: "a"},
	}
	// Pre-compute the title Audit would build for group #1 so the fake
	// existing-issues list has something that matches by normalization.
	g1 := GroupFindings([]report.Finding{findings[0]})[0]
	trackedTitle := buildGroupTitle(g1) + "!"

	gh := &fakeGitHub{
		cloneRepo:         func(ref string) (*github.Repo, error) { return repo, nil },
		defaultBranchHEAD: func(owner, name string) (string, error) { return "sha-audit-dedupe", nil },
		listExistingIssues: func(owner, name string) ([]github.ExistingIssue, error) {
			return []github.ExistingIssue{{Title: trackedTitle, URL: "https://example/1"}}, nil
		},
	}
	claudeMock := &fakeClaude{
		fn: func(dir string, ctx AuditContext) (*report.ReviewResult, error) {
			return &report.ReviewResult{Summary: "s", Findings: findings}, nil
		},
	}
	runner := &Runner{Claude: claudeMock, GitHub: gh}

	var out bytes.Buffer
	if err := runner.Run(&out, baseAuditOpts(patternDir)); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	s := out.String()
	if strings.Contains(s, "Missing error check") {
		t.Errorf("deduped finding still present in output:\n%s", s)
	}
	if !strings.Contains(s, "Unused var") {
		t.Errorf("non-duplicate finding missing from output:\n%s", s)
	}
}

func TestAuditRun_DedupeDisabledByFlag(t *testing.T) {
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	patternDir := seedPatternDir(t)
	repo := fakeRepo(t, "acme", "widgets")

	listerCalled := false
	gh := &fakeGitHub{
		cloneRepo:         func(ref string) (*github.Repo, error) { return repo, nil },
		defaultBranchHEAD: func(owner, name string) (string, error) { return "sha-audit-nodedupe", nil },
		listExistingIssues: func(owner, name string) ([]github.ExistingIssue, error) {
			listerCalled = true
			return nil, nil
		},
	}
	claudeMock := &fakeClaude{
		fn: func(dir string, ctx AuditContext) (*report.ReviewResult, error) {
			return &report.ReviewResult{Summary: "s", Findings: []report.Finding{
				{ID: "W-1", Severity: report.SeverityWarning, Title: "t", Pattern: "p", File: "f.go", Line: 1, Problem: "p", Action: "a"},
			}}, nil
		},
	}
	runner := &Runner{Claude: claudeMock, GitHub: gh}

	opts := baseAuditOpts(patternDir)
	opts.NoIssueDedupe = true
	var out bytes.Buffer
	if err := runner.Run(&out, opts); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if listerCalled {
		t.Error("ListExistingIssues must not be called when NoIssueDedupe=true")
	}
}

func TestAuditRun_DedupeListerErrorIsNonFatal(t *testing.T) {
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	patternDir := seedPatternDir(t)
	repo := fakeRepo(t, "acme", "widgets")

	gh := &fakeGitHub{
		cloneRepo:         func(ref string) (*github.Repo, error) { return repo, nil },
		defaultBranchHEAD: func(owner, name string) (string, error) { return "sha-audit-listerr", nil },
		listExistingIssues: func(owner, name string) ([]github.ExistingIssue, error) {
			return nil, errors.New("gh boom")
		},
	}
	claudeMock := &fakeClaude{
		fn: func(dir string, ctx AuditContext) (*report.ReviewResult, error) {
			return &report.ReviewResult{Summary: "s", Findings: []report.Finding{
				{ID: "W-1", Severity: report.SeverityWarning, Title: "kept", Pattern: "p", File: "f.go", Line: 1, Problem: "p", Action: "a"},
			}}, nil
		},
	}
	runner := &Runner{Claude: claudeMock, GitHub: gh}

	var out bytes.Buffer
	if err := runner.Run(&out, baseAuditOpts(patternDir)); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !strings.Contains(out.String(), "kept") {
		t.Errorf("finding must survive dedupe-lister failure:\n%s", out.String())
	}
}

func TestAuditRun_EmptyPatternsIsError(t *testing.T) {
	// Audit requires at least one pattern; otherwise it has nothing to
	// audit against and Run must return an error before calling Claude.
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	gh := &fakeGitHub{
		cloneRepo:         func(ref string) (*github.Repo, error) { return fakeRepo(t, "acme", "widgets"), nil },
		defaultBranchHEAD: func(owner, name string) (string, error) { return testDefaultBranchSHA, nil },
	}
	claudeMock := &fakeClaude{
		fn: func(dir string, ctx AuditContext) (*report.ReviewResult, error) {
			t.Fatal("Audit must not be called when no patterns are loaded")
			return nil, nil
		},
	}
	runner := &Runner{Claude: claudeMock, GitHub: gh}

	opts := Options{
		RepoRef:         "owner/repo",
		NoLocalPatterns: true,
		NoRepoPatterns:  true,
		Format:          "markdown",
		MinSeverity:     report.SeverityInfo,
	}

	var out bytes.Buffer
	err := runner.Run(&out, opts)
	if err == nil {
		t.Fatal("expected error when no patterns loaded")
	}
	if !strings.Contains(err.Error(), "no review patterns") {
		t.Errorf("error should mention missing patterns, got: %v", err)
	}
}

// fakeCapturer is a recording capture.Proposer: it counts calls and captures the
// context handed to the proposal pass, without invoking Claude.
type fakeCapturer struct {
	calls  atomic.Int32
	ctx    capture.CaptureContext
	result *capture.CaptureResult
	err    error
}

func (f *fakeCapturer) Capture(dir string, ctx capture.CaptureContext) (*capture.CaptureResult, error) {
	f.calls.Add(1)
	f.ctx = ctx
	return f.result, f.err
}

// fakeCaptureWriter is an offline capture.WikiWriter recording what the gated
// write-back would push, without touching git. A non-nil applyErr simulates a
// failed push (auth, non-fast-forward, network).
type fakeCaptureWriter struct {
	applyCalls atomic.Int32
	applyFiles []patterns.WikiFile
	applyErr   error
}

func (f *fakeCaptureWriter) Clone(repo, ref string) (string, string, func(), error) {
	return "/tmp/wiki-clone", "wikisha", func() {}, nil
}

func (f *fakeCaptureWriter) ApplyAdditions(dir string, files []patterns.WikiFile, msg string) error {
	f.applyCalls.Add(1)
	f.applyFiles = files
	return f.applyErr
}

// captureAuditRunner wires a Runner with the capturer and a ResolveWiki seam that
// resolves to a temp wiki dir (so the gate's wiki.Dir != "" check passes without
// cloning a real wiki). The fake Clone's HEAD matches the seam's commit so the
// write-back never treats the wiki as diverged.
func captureAuditRunner(t *testing.T, gh *fakeGitHub, cl *fakeClaude, cp *fakeCapturer) *Runner {
	t.Helper()
	r := &Runner{Claude: cl, GitHub: gh, Capturer: cp}
	wikiDir := t.TempDir()
	r.ResolveWiki = func(_, _ string, _ patterns.WikiOptions, _ patterns.RemoteOptions) patterns.ResolvedWiki {
		return patterns.ResolvedWiki{Repo: "acme/widgets.wiki", CommitSHA: "wikisha", Dir: wikiDir}
	}
	return r
}

func onePatternProposal() *capture.CaptureResult {
	return &capture.CaptureResult{
		Patterns: []capture.ProposedPage{
			{Path: "review_patterns/escape-untrusted-fences.md", Kind: capture.KindPattern, Title: "Escape untrusted fences", Body: "# Review Pattern: Escape untrusted fences\n\n## What to check\n...", Rationale: "recurs"},
		},
	}
}

func findingAuditClaude() *fakeClaude {
	return &fakeClaude{
		fn: func(dir string, ctx AuditContext) (*report.ReviewResult, error) {
			// Empty Pattern so the finding survives CandidateFindings.
			return &report.ReviewResult{Summary: "s", Findings: []report.Finding{
				{ID: "W-1", Severity: report.SeverityWarning, Title: "raw SQL", File: "db.go", Line: 3, Problem: "p", Action: "a"},
			}}, nil
		},
	}
}

func TestAuditRun_CaptureProposes(t *testing.T) {
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	patternDir := seedPatternDir(t)
	gh := &fakeGitHub{
		cloneRepo:         func(ref string) (*github.Repo, error) { return fakeRepo(t, "acme", "widgets"), nil },
		defaultBranchHEAD: func(owner, name string) (string, error) { return "sha-cap-propose", nil },
	}
	cl := findingAuditClaude()
	cp := &fakeCapturer{result: onePatternProposal()}
	runner := captureAuditRunner(t, gh, cl, cp)

	var out bytes.Buffer
	if err := runner.Run(&out, baseAuditOpts(patternDir)); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if cp.calls.Load() != 1 {
		t.Fatalf("capturer called %d times, want 1 — capture is on by default with a wiki", cp.calls.Load())
	}
	if len(cp.ctx.Findings) != 1 || cp.ctx.Findings[0].File != "db.go" {
		t.Errorf("capturer got findings %+v, want the single audit finding", cp.ctx.Findings)
	}
	if cp.ctx.RepoName != "acme/widgets" {
		t.Errorf("capturer got repo=%q, want acme/widgets", cp.ctx.RepoName)
	}
	if !strings.Contains(out.String(), "Captured knowledge proposals:") {
		t.Errorf("missing the capture proposals on stdout:\n%s", out.String())
	}
}

func TestAuditRun_CaptureSkippedWithoutWiki(t *testing.T) {
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	patternDir := seedPatternDir(t)
	gh := &fakeGitHub{
		cloneRepo:         func(ref string) (*github.Repo, error) { return fakeRepo(t, "acme", "widgets"), nil },
		defaultBranchHEAD: func(owner, name string) (string, error) { return "sha-cap-nowiki", nil },
	}
	cl := findingAuditClaude()
	cp := &fakeCapturer{result: onePatternProposal()}
	runner := captureAuditRunner(t, gh, cl, cp)
	// A wiki that did not resolve (no --wiki): capture has nowhere to propose to.
	runner.ResolveWiki = func(_, _ string, _ patterns.WikiOptions, _ patterns.RemoteOptions) patterns.ResolvedWiki {
		return patterns.ResolvedWiki{}
	}

	if err := runner.Run(&bytes.Buffer{}, baseAuditOpts(patternDir)); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if cp.calls.Load() != 0 {
		t.Errorf("capturer called %d times, want 0 without a resolved wiki", cp.calls.Load())
	}
}

func TestAuditRun_CaptureSkippedByNoCapture(t *testing.T) {
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	patternDir := seedPatternDir(t)
	gh := &fakeGitHub{
		cloneRepo:         func(ref string) (*github.Repo, error) { return fakeRepo(t, "acme", "widgets"), nil },
		defaultBranchHEAD: func(owner, name string) (string, error) { return "sha-cap-nocap", nil },
	}
	cl := findingAuditClaude()
	cp := &fakeCapturer{result: onePatternProposal()}
	runner := captureAuditRunner(t, gh, cl, cp)

	opts := baseAuditOpts(patternDir)
	opts.NoCapture = true

	if err := runner.Run(&bytes.Buffer{}, opts); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if cp.calls.Load() != 0 {
		t.Errorf("capturer called %d times, want 0 with --no-capture", cp.calls.Load())
	}
}

func TestAuditRun_CaptureWikiRoutesAcceptedPages(t *testing.T) {
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	patternDir := seedPatternDir(t)
	gh := &fakeGitHub{
		cloneRepo:         func(ref string) (*github.Repo, error) { return fakeRepo(t, "acme", "widgets"), nil },
		defaultBranchHEAD: func(owner, name string) (string, error) { return "sha-cap-write", nil },
	}
	cl := findingAuditClaude()
	cp := &fakeCapturer{result: onePatternProposal()}
	runner := captureAuditRunner(t, gh, cl, cp)
	writer := &fakeCaptureWriter{}
	runner.CaptureWriter = writer
	runner.In = strings.NewReader("")
	runner.IsTTY = func() bool { return false }

	opts := baseAuditOpts(patternDir)
	opts.CaptureWiki = true
	opts.Yes = true // skip the confirmation in a non-interactive test

	var out bytes.Buffer
	if err := runner.Run(&out, opts); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if writer.applyCalls.Load() != 1 {
		t.Fatalf("ApplyAdditions called %d times, want 1 under --capture-wiki", writer.applyCalls.Load())
	}
	if len(writer.applyFiles) != 1 || writer.applyFiles[0].Path != "review_patterns/escape-untrusted-fences.md" {
		t.Errorf("write-back routed the wrong pages: %+v", writer.applyFiles)
	}
}

func TestAuditRun_CaptureWikiBypassesCacheToWrite(t *testing.T) {
	// A --capture-wiki run must reach the write-back even when an identical earlier
	// result is cached. The capture-gating flags are not part of the cache key, so
	// without the bypass a second run would hit the cache, return before runCapture,
	// and silently skip the write — leaving the wiki unchanged while the build goes
	// green (issue #2).
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	patternDir := seedPatternDir(t)
	gh := &fakeGitHub{
		cloneRepo:         func(ref string) (*github.Repo, error) { return fakeRepo(t, "acme", "widgets"), nil },
		defaultBranchHEAD: func(owner, name string) (string, error) { return "sha-cap-cachewrite", nil },
	}

	// First run: a plain audit with a resolved wiki populates the cache for this
	// commit + wiki commit.
	cl1 := findingAuditClaude()
	runner1 := captureAuditRunner(t, gh, cl1, &fakeCapturer{result: onePatternProposal()})
	if err := runner1.Run(&bytes.Buffer{}, baseAuditOpts(patternDir)); err != nil {
		t.Fatalf("seeding run returned error: %v", err)
	}
	if cl1.calls != 1 {
		t.Fatalf("seeding run Audit calls = %d, want 1", cl1.calls)
	}

	// Second run: same commit and wiki, now with --capture-wiki. Assert the cache
	// is bypassed (Claude re-runs) and the page is actually pushed.
	cl2 := findingAuditClaude()
	runner2 := captureAuditRunner(t, gh, cl2, &fakeCapturer{result: onePatternProposal()})
	writer := &fakeCaptureWriter{}
	runner2.CaptureWriter = writer
	runner2.In = strings.NewReader("")
	runner2.IsTTY = func() bool { return false }

	opts := baseAuditOpts(patternDir)
	opts.CaptureWiki = true
	opts.Yes = true

	if err := runner2.Run(&bytes.Buffer{}, opts); err != nil {
		t.Fatalf("capture-wiki run returned error: %v", err)
	}
	if cl2.calls != 1 {
		t.Errorf("Audit calls = %d, want 1 — --capture-wiki must bypass the cache hit and re-run", cl2.calls)
	}
	if writer.applyCalls.Load() != 1 {
		t.Fatalf("ApplyAdditions called %d times, want 1 — the write-back must be reached despite a populated cache", writer.applyCalls.Load())
	}
}

func TestAuditRun_CaptureWikiPushFailureIsFatal(t *testing.T) {
	// An explicitly-requested --capture-wiki push that fails must make audit return
	// a non-nil error (non-zero exit), not a green build with the wiki unchanged
	// (issue #4).
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	patternDir := seedPatternDir(t)
	gh := &fakeGitHub{
		cloneRepo:         func(ref string) (*github.Repo, error) { return fakeRepo(t, "acme", "widgets"), nil },
		defaultBranchHEAD: func(owner, name string) (string, error) { return "sha-cap-pushfail", nil },
	}
	cl := findingAuditClaude()
	runner := captureAuditRunner(t, gh, cl, &fakeCapturer{result: onePatternProposal()})
	writer := &fakeCaptureWriter{applyErr: errors.New("push rejected")}
	runner.CaptureWriter = writer
	runner.In = strings.NewReader("")
	runner.IsTTY = func() bool { return false }

	opts := baseAuditOpts(patternDir)
	opts.CaptureWiki = true
	opts.Yes = true

	err := runner.Run(&bytes.Buffer{}, opts)
	if err == nil {
		t.Fatal("a failed --capture-wiki push must make audit return a non-nil error, got nil")
	}
	if !strings.Contains(err.Error(), "push rejected") {
		t.Errorf("error should wrap the push failure, got: %v", err)
	}
	if writer.applyCalls.Load() != 1 {
		t.Errorf("the write-back must have attempted the push: apply=%d", writer.applyCalls.Load())
	}
}

func TestAuditRun_LocalUsesCwd(t *testing.T) {
	// Not t.Parallel(): cache.SetDir mutates a package-level variable.
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	patternDir := seedPatternDir(t)
	dir := t.TempDir()
	gh := &fakeGitHub{
		cloneRepo: func(ref string) (*github.Repo, error) {
			return &github.Repo{Owner: "acme", Name: "widgets", Dir: dir}, nil
		},
		defaultBranchHEAD: func(owner, name string) (string, error) { return "sha-local-audit", nil },
	}
	runner := &Runner{Claude: &fakeClaude{}, GitHub: gh}

	opts := baseAuditOpts(patternDir)
	opts.Local = true
	opts.NoCache = true

	var out bytes.Buffer
	if err := runner.Run(&out, opts); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if gh.cloneLocalCalls.Load() != 1 {
		t.Errorf("CloneRepoLocal calls = %d, want 1", gh.cloneLocalCalls.Load())
	}
	if gh.cloneCalls.Load() != 0 {
		t.Errorf("CloneRepo calls = %d, want 0 in local mode", gh.cloneCalls.Load())
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("local checkout must survive the run: %v", err)
	}
}
