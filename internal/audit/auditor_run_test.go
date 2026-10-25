package audit

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/planwerk/planwerk-agent/internal/cache"
	"github.com/planwerk/planwerk-agent/internal/capture"
	"github.com/planwerk/planwerk-agent/internal/github"
	"github.com/planwerk/planwerk-agent/internal/github/githubtest"
	"github.com/planwerk/planwerk-agent/internal/hygiene"
	"github.com/planwerk/planwerk-agent/internal/patterns"
	"github.com/planwerk/planwerk-agent/internal/report"
	"github.com/planwerk/planwerk-agent/internal/search"
)

const testDefaultBranchSHA = "sha"

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

// auditPatternFlag mirrors the pattern-source component Run folds into the
// cache key, so a test that seeds the cache by hand keys the same entry.
func auditPatternFlag(o Options) string {
	return "patterns=" + patterns.Fingerprint(o.PatternDirs, o.NoRepoPatterns, o.NoLocalPatterns, o.MaxPatterns)
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

	gh := &githubtest.Fake{
		CloneRepoFn:         func(ref string) (*github.Repo, error) { return repo, nil },
		DefaultBranchHEADFn: func(owner, name string) (string, error) { return "sha-audit-1", nil },
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
	gh := &githubtest.Fake{
		CloneRepoFn:         func(ref string) (*github.Repo, error) { return repo, nil },
		DefaultBranchHEADFn: func(owner, name string) (string, error) { return "sha-audit-nocache", nil },
	}

	// Seed cache with sentinel to confirm NoCache bypasses it. Cache key owner/name
	// follow ParseRepoRef(opts.RepoRef) = ("owner", "repo").
	cacheKey := cache.AuditKey("owner", "repo", "sha-audit-nocache")
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
	gh := &githubtest.Fake{
		CloneRepoFn: func(ref string) (*github.Repo, error) { return fakeRepo(t, "acme", "widgets"), nil },
		DefaultBranchHEADFn: func(owner, name string) (string, error) {
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

	gh := &githubtest.Fake{
		CloneRepoFn:         func(ref string) (*github.Repo, error) { return nil, errors.New("clone failed") },
		DefaultBranchHEADFn: func(owner, name string) (string, error) { return testDefaultBranchSHA, nil },
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
	// must match ParseRepoRef("owner/repo") = ("owner", "repo"). The pattern
	// sources are part of the key, so seed with the very options Run gets.
	opts := baseAuditOpts(t.TempDir())
	cacheKey := cache.AuditKey("owner", "repo", "sha-skip-clone", auditPatternFlag(opts))
	if err := cache.PutRaw(cacheKey, cache.CommandAudit, []byte(`{"summary":"Cached audit"}`)); err != nil {
		t.Fatalf("seeding cache: %v", err)
	}

	gh := &githubtest.Fake{
		CloneRepoFn: func(ref string) (*github.Repo, error) {
			t.Fatal("CloneRepo must not be called on cache hit")
			return nil, nil
		},
		DefaultBranchHEADFn: func(owner, name string) (string, error) { return "sha-skip-clone", nil },
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
	var out bytes.Buffer
	if err := runner.Run(&out, opts); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !strings.Contains(out.String(), "Cached audit") {
		t.Errorf("output missing cached summary, got:\n%s", out.String())
	}
}

func TestAuditRun_InvalidRepoRefFailsBeforeHEAD(t *testing.T) {
	gh := &githubtest.Fake{
		CloneRepoFn: func(ref string) (*github.Repo, error) {
			t.Fatal("CloneRepo must not be called for invalid repo ref")
			return nil, nil
		},
		DefaultBranchHEADFn: func(owner, name string) (string, error) {
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
	gh := &githubtest.Fake{
		CloneRepoFn:         func(ref string) (*github.Repo, error) { return fakeRepo(t, "acme", "widgets"), nil },
		DefaultBranchHEADFn: func(owner, name string) (string, error) { return testDefaultBranchSHA, nil },
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

	gh := &githubtest.Fake{
		CloneRepoFn:         func(ref string) (*github.Repo, error) { return repo, nil },
		DefaultBranchHEADFn: func(owner, name string) (string, error) { return "sha-audit-dedupe", nil },
		ListAllIssuesFn: func(owner, name string) ([]github.ExistingIssue, error) {
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
	gh := &githubtest.Fake{
		CloneRepoFn:         func(ref string) (*github.Repo, error) { return repo, nil },
		DefaultBranchHEADFn: func(owner, name string) (string, error) { return "sha-audit-nodedupe", nil },
		ListAllIssuesFn: func(owner, name string) ([]github.ExistingIssue, error) {
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
		t.Error("ListAllIssues must not be called when NoIssueDedupe=true")
	}
}

func TestAuditRun_DedupeListerErrorIsNonFatal(t *testing.T) {
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	patternDir := seedPatternDir(t)
	repo := fakeRepo(t, "acme", "widgets")

	gh := &githubtest.Fake{
		CloneRepoFn:         func(ref string) (*github.Repo, error) { return repo, nil },
		DefaultBranchHEADFn: func(owner, name string) (string, error) { return "sha-audit-listerr", nil },
		ListAllIssuesFn: func(owner, name string) ([]github.ExistingIssue, error) {
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

	gh := &githubtest.Fake{
		CloneRepoFn:         func(ref string) (*github.Repo, error) { return fakeRepo(t, "acme", "widgets"), nil },
		DefaultBranchHEADFn: func(owner, name string) (string, error) { return testDefaultBranchSHA, nil },
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
func captureAuditRunner(t *testing.T, gh *githubtest.Fake, cl *fakeClaude, cp *fakeCapturer) *Runner {
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
	gh := &githubtest.Fake{
		CloneRepoFn:         func(ref string) (*github.Repo, error) { return fakeRepo(t, "acme", "widgets"), nil },
		DefaultBranchHEADFn: func(owner, name string) (string, error) { return "sha-cap-propose", nil },
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

// TestAuditRun_SARIFIsTheWholeOutput: under --format sarif the audit prints one
// SARIF log and nothing else; the capture pass still runs, but its printed
// proposals are discarded, as under json (decision 134).
func TestAuditRun_SARIFIsTheWholeOutput(t *testing.T) {
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	patternDir := seedPatternDir(t)
	gh := &githubtest.Fake{
		CloneRepoFn:         func(ref string) (*github.Repo, error) { return fakeRepo(t, "acme", "widgets"), nil },
		DefaultBranchHEADFn: func(owner, name string) (string, error) { return "sha-cap-sarif", nil },
	}
	cp := &fakeCapturer{result: onePatternProposal()}
	runner := captureAuditRunner(t, gh, findingAuditClaude(), cp)
	opts := baseAuditOpts(patternDir)
	opts.Format = "sarif"

	var out bytes.Buffer
	if err := runner.Run(&out, opts); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if cp.calls.Load() != 1 {
		t.Fatalf("capturer called %d times, want 1: capture still runs under sarif", cp.calls.Load())
	}
	if strings.Contains(out.String(), "Captured knowledge proposals:") {
		t.Errorf("the capture render must not follow the SARIF log:\n%s", out.String())
	}
	var doc struct {
		Runs []struct {
			Results []struct {
				Locations []struct {
					PhysicalLocation struct {
						ArtifactLocation struct {
							URI string `json:"uri"`
						} `json:"artifactLocation"`
						Region struct {
							StartLine int `json:"startLine"`
						} `json:"region"`
					} `json:"physicalLocation"`
				} `json:"locations"`
			} `json:"results"`
		} `json:"runs"`
	}
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", err, out.String())
	}
	if len(doc.Runs) != 1 || len(doc.Runs[0].Results) != 1 || len(doc.Runs[0].Results[0].Locations) != 1 {
		t.Fatalf("got %+v, want one run with one located result", doc.Runs)
	}
	loc := doc.Runs[0].Results[0].Locations[0].PhysicalLocation
	if loc.ArtifactLocation.URI != "db.go" || loc.Region.StartLine != 3 {
		t.Errorf("location = %+v, want db.go at line 3", loc)
	}
}

func TestAuditRun_CaptureSkippedWithoutWiki(t *testing.T) {
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	patternDir := seedPatternDir(t)
	gh := &githubtest.Fake{
		CloneRepoFn:         func(ref string) (*github.Repo, error) { return fakeRepo(t, "acme", "widgets"), nil },
		DefaultBranchHEADFn: func(owner, name string) (string, error) { return "sha-cap-nowiki", nil },
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

// TestAuditRun_MemoryDirectoryLivesForTheAudit locks the lifetime of the
// project memory directory: the audit session can read the page while it runs,
// and the directory is gone when Run returns.
func TestAuditRun_MemoryDirectoryLivesForTheAudit(t *testing.T) {
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	patternDir := seedPatternDir(t)
	gh := &githubtest.Fake{
		CloneRepoFn:         func(ref string) (*github.Repo, error) { return fakeRepo(t, "acme", "widgets"), nil },
		DefaultBranchHEADFn: func(owner, name string) (string, error) { return "sha-memory", nil },
	}
	var gotMemory patterns.MemoryCatalog
	cl := &fakeClaude{
		fn: func(dir string, ctx AuditContext) (*report.ReviewResult, error) {
			gotMemory = ctx.Memory
			data, err := os.ReadFile(filepath.Join(ctx.Memory.Dir, "pin-dependencies.md"))
			if err != nil {
				t.Errorf("the memory page must be readable while the audit runs: %v", err)
			}
			if string(data) != "# Pin every dependency\n" {
				t.Errorf("memory page = %q, want the page body and a newline", data)
			}
			return &report.ReviewResult{Summary: "s"}, nil
		},
	}
	runner := &Runner{Claude: cl, GitHub: gh}
	// A wiki with one memory page and no clone directory, so capture stays off.
	runner.ResolveWiki = func(_, _ string, _ patterns.WikiOptions, _ patterns.RemoteOptions) patterns.ResolvedWiki {
		return patterns.ResolvedWiki{Repo: "acme/widgets.wiki", CommitSHA: "wikisha", MemoryPages: []patterns.MemoryPage{
			{Name: "pin-dependencies.md", Title: "Pin every dependency", Body: "# Pin every dependency"},
		}}
	}

	if err := runner.Run(&bytes.Buffer{}, baseAuditOpts(patternDir)); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if atomic.LoadInt32(&cl.calls) != 1 {
		t.Fatalf("Audit calls = %d, want 1", cl.calls)
	}
	if !strings.HasPrefix(filepath.Base(gotMemory.Dir), patterns.MemoryDirPrefix) || len(gotMemory.Pages) != 1 {
		t.Fatalf("AuditContext.Memory = %+v, want a memory directory and the one page", gotMemory)
	}
	if _, err := os.Stat(gotMemory.Dir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("after Run, stat %s = %v, want not exist", gotMemory.Dir, err)
	}
}

func TestAuditRun_CaptureSkippedByNoCapture(t *testing.T) {
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	patternDir := seedPatternDir(t)
	gh := &githubtest.Fake{
		CloneRepoFn:         func(ref string) (*github.Repo, error) { return fakeRepo(t, "acme", "widgets"), nil },
		DefaultBranchHEADFn: func(owner, name string) (string, error) { return "sha-cap-nocap", nil },
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
	gh := &githubtest.Fake{
		CloneRepoFn:         func(ref string) (*github.Repo, error) { return fakeRepo(t, "acme", "widgets"), nil },
		DefaultBranchHEADFn: func(owner, name string) (string, error) { return "sha-cap-write", nil },
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
	gh := &githubtest.Fake{
		CloneRepoFn:         func(ref string) (*github.Repo, error) { return fakeRepo(t, "acme", "widgets"), nil },
		DefaultBranchHEADFn: func(owner, name string) (string, error) { return "sha-cap-cachewrite", nil },
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
	gh := &githubtest.Fake{
		CloneRepoFn:         func(ref string) (*github.Repo, error) { return fakeRepo(t, "acme", "widgets"), nil },
		DefaultBranchHEADFn: func(owner, name string) (string, error) { return "sha-cap-pushfail", nil },
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
	gh := &githubtest.Fake{
		CloneRepoFn: func(ref string) (*github.Repo, error) {
			return &github.Repo{Owner: "acme", Name: "widgets", Dir: dir}, nil
		},
		DefaultBranchHEADFn: func(owner, name string) (string, error) { return "sha-local-audit", nil },
	}
	runner := &Runner{Claude: &fakeClaude{}, GitHub: gh}

	opts := baseAuditOpts(patternDir)
	opts.Local = true
	opts.NoCache = true

	var out bytes.Buffer
	if err := runner.Run(&out, opts); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if gh.Count("UseLocalRepo") != 1 {
		t.Errorf("UseLocalRepo calls = %d, want 1", gh.Count("UseLocalRepo"))
	}
	if gh.Count("CloneRepo") != 0 {
		t.Errorf("CloneRepo calls = %d, want 0 in local mode", gh.Count("CloneRepo"))
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("local checkout must survive the run: %v", err)
	}
}

// TestAuditRun_MinSeverityDoesNotRekey pins that the severity threshold is a
// render-time filter, not part of what was analyzed. The cached payload is the
// unfiltered result and renderAudit applies the threshold to it, so asking the
// same commit for a stricter view must reuse that payload instead of paying for
// a second audit of the same code.
func TestAuditRun_MinSeverityDoesNotRekey(t *testing.T) {
	// Not t.Parallel(): cache.SetDir mutates a package-level variable.
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	patternDir := seedPatternDir(t)
	repo := fakeRepo(t, "acme", "widgets")
	gh := &githubtest.Fake{
		CloneRepoFn:         func(ref string) (*github.Repo, error) { return repo, nil },
		DefaultBranchHEADFn: func(owner, name string) (string, error) { return "sha-threshold", nil },
	}
	claudeMock := &fakeClaude{
		fn: func(dir string, ctx AuditContext) (*report.ReviewResult, error) {
			return &report.ReviewResult{Summary: "Audit summary", Findings: []report.Finding{
				{ID: "W-001", Severity: report.SeverityWarning, Confidence: report.ConfidenceVerified, Title: "kept above the threshold", File: "w.go", Line: 1, Problem: "p", Action: "a"},
				{ID: "I-001", Severity: report.SeverityInfo, Confidence: report.ConfidenceVerified, Title: "filtered by the threshold", File: "i.go", Line: 2, Problem: "p", Action: "a"},
			}}, nil
		},
	}

	opts := baseAuditOpts(patternDir)
	opts.MinSeverity = report.SeverityInfo
	var first bytes.Buffer
	if err := (&Runner{Claude: claudeMock, GitHub: gh}).Run(&first, opts); err != nil {
		t.Fatalf("first Run returned error: %v", err)
	}
	if !strings.Contains(first.String(), "filtered by the threshold") {
		t.Errorf("the INFO finding should render at --min-severity info, got:\n%s", first.String())
	}

	// Same repo, same commit, stricter threshold: the analysis is identical, so
	// only the rendering may differ.
	opts.MinSeverity = report.SeverityWarning
	var second bytes.Buffer
	if err := (&Runner{Claude: claudeMock, GitHub: gh}).Run(&second, opts); err != nil {
		t.Fatalf("second Run returned error: %v", err)
	}
	if claudeMock.calls != 1 {
		t.Errorf("Audit calls = %d, want 1 — a changed threshold must not re-run the audit", claudeMock.calls)
	}
	if !strings.Contains(second.String(), "kept above the threshold") {
		t.Errorf("the WARNING finding should survive --min-severity warning, got:\n%s", second.String())
	}
	if strings.Contains(second.String(), "filtered by the threshold") {
		t.Errorf("the INFO finding should be filtered at --min-severity warning, got:\n%s", second.String())
	}
}

// brainSurface returns an enabled search surface whose mirror stands at
// revision.
func brainSurface(revision string) search.Surface {
	return search.Surface{
		Command:  "/usr/local/bin/planwerk-agent brain search acme/widgets",
		SyncedAt: "2026-10-02T09:00:00Z",
		Revision: revision,
	}
}

// TestAuditRun_BrainSurfaceReachesTheAuditAndTheCacheKey runs the audit with
// and without the brain. The surface is resolved only for a run that opted in
// and reaches the audit session. The mirror's revision is part of the cache
// key, and a run with the brain off, or without a finished mirror, keeps the
// key an audit has without the brain.
func TestAuditRun_BrainSurfaceReachesTheAuditAndTheCacheKey(t *testing.T) {
	// Not t.Parallel(): cache.SetDir mutates a package-level variable.
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	const headSHA = "sha-audit-brain"
	patternDir := seedPatternDir(t)
	gh := &githubtest.Fake{
		CloneRepoFn:         func(ref string) (*github.Repo, error) { return fakeRepo(t, "owner", "repo"), nil },
		DefaultBranchHEADFn: func(owner, name string) (string, error) { return headSHA, nil },
	}
	var sessions []search.Surface // the surface of every audit session, in order
	claudeMock := &fakeClaude{fn: func(dir string, ctx AuditContext) (*report.ReviewResult, error) {
		sessions = append(sessions, ctx.Brain)
		return &report.ReviewResult{Summary: "audit ok"}, nil
	}}
	var surface search.Surface
	resolved := 0
	runner := &Runner{Claude: claudeMock, GitHub: gh, ResolveBrain: func(owner, name string) search.Surface {
		resolved++
		if owner != "owner" || name != "repo" {
			t.Errorf("ResolveBrain(%q, %q), want owner and repo", owner, name)
		}
		return surface
	}}

	off := baseAuditOpts(patternDir)
	on := off
	on.Brain = true
	onNoCache := on
	onNoCache.NoCache = true
	plainKeyHolds := func() bool {
		_, ok := cache.GetRaw(cache.AuditKey("owner", "repo", headSHA, auditPatternFlag(off)), 0)
		return ok
	}
	rev1, rev2 := brainSurface("rev-1"), brainSurface("rev-2")
	steps := []struct {
		why          string
		opts         Options
		surface      search.Surface // what the seam answers
		wantSessions int
		wantResolved int
		wantPlainKey bool // the key of a run without the brain holds a result
	}{
		{"the first run with the brain starts a session", on, rev1, 1, 1, false},
		{"the same revision is served from the cache", on, rev1, 1, 2, false},
		{"another revision starts a session again", on, rev2, 2, 3, false},
		{"a run with the brain off starts a session and resolves nothing", off, rev2, 3, 3, true},
		{"a run with the brain off is served from its own entry", off, rev2, 3, 3, true},
		{"a run without a finished mirror has the key of a run with the brain off", on, search.Surface{}, 3, 4, true},
		{"without the cache, that run hands its session no search", onNoCache, search.Surface{}, 4, 5, true},
	}
	for _, step := range steps {
		surface = step.surface
		if err := runner.Run(&bytes.Buffer{}, step.opts); err != nil {
			t.Fatalf("%s: Run returned error: %v", step.why, err)
		}
		if len(sessions) != step.wantSessions || resolved != step.wantResolved {
			t.Fatalf("%s: %d sessions and %d resolves, want %d and %d", step.why, len(sessions), resolved, step.wantSessions, step.wantResolved)
		}
		if got := plainKeyHolds(); got != step.wantPlainKey {
			t.Fatalf("%s: the key of a run without the brain holds a result = %v, want %v", step.why, got, step.wantPlainKey)
		}
	}
	if want := []search.Surface{rev1, rev2, {}, {}}; !slices.Equal(sessions, want) {
		t.Errorf("the sessions were handed %+v, want %+v", sessions, want)
	}
}

// writeRepoFile writes content to rel under a fake repo's checkout, so the
// finding-hygiene gates have code to search.
func writeRepoFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	path := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// handlerGo is a checkout file whose line 4 the gate tests' findings quote.
const handlerGo = "package svc\n\nfunc Handle(r Request) error {\n\treturn db.Exec(r.Query)\n}\n"

// formatJSON is the --format value that renders the audit as JSON.
const formatJSON = "json"

// cachedAudit reads the audit result Run cached for sha under opts.
func cachedAudit(t *testing.T, sha string, opts Options) report.ReviewResult {
	t.Helper()
	data, ok := cache.GetRaw(cache.AuditKey("owner", "repo", sha, auditPatternFlag(opts)), 0)
	if !ok {
		t.Fatal("the audit result was not cached")
	}
	var result report.ReviewResult
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("decoding the cached audit: %v", err)
	}
	return result
}

// TestAuditRun_GatesRunBeforeCache locks the finding hygiene an audit runs:
// the line anchor, the snippet gate over the whole checkout, and claim
// verification of the BLOCKING and CRITICAL findings, all before the cache
// write, so a cache hit, in Markdown or JSON, serves the gated findings and the
// run's gate counts without running a gate again.
func TestAuditRun_GatesRunBeforeCache(t *testing.T) {
	// Not t.Parallel(): cache.SetDir mutates a package-level variable.
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	const sha = "sha-audit-gates"
	patternDir := seedPatternDir(t)
	repo := fakeRepo(t, "acme", "widgets")
	writeRepoFile(t, repo.Dir, "svc/handler.go", handlerGo)
	gh := &githubtest.Fake{
		CloneRepoFn:         func(ref string) (*github.Repo, error) { return repo, nil },
		DefaultBranchHEADFn: func(owner, name string) (string, error) { return sha, nil },
	}
	claudeMock := &fakeClaude{fn: func(dir string, ctx AuditContext) (*report.ReviewResult, error) {
		return &report.ReviewResult{Summary: "s", Findings: []report.Finding{
			{ID: "B-001", Severity: report.SeverityBlocking, Confidence: report.ConfidenceLikely, Title: "raw query", File: "svc/handler.go", Line: 4, CodeSnippet: "return db.Exec(r.Query)", Problem: "p", Action: "a"},
			{ID: "C-001", Severity: report.SeverityCritical, Confidence: report.ConfidenceLikely, Title: "unchecked input", File: "svc/handler.go", Line: 4, CodeSnippet: "return db.Exec(r.Query)", Problem: "p", Action: "a"},
			{ID: "W-001", Severity: report.SeverityWarning, Confidence: report.ConfidenceLikely, Title: "made-up call", File: "svc/handler.go", Line: 4, CodeSnippet: "user.DeleteAllRecords(ctx)", Problem: "p", Action: "a"},
			{ID: "W-002", Severity: report.SeverityWarning, Confidence: report.ConfidenceLikely, Title: "no changelog", File: "CHANGELOG.md", Problem: "p", Action: "a"},
		}}, nil
	}}
	var sent [][]string // the finding IDs of every verifier call, in order
	verify := func(dir string, findings []report.Finding) ([]hygiene.ClaimVerdict, error) {
		if dir != repo.Dir {
			t.Errorf("verifier dir = %q, want the checkout %q", dir, repo.Dir)
		}
		ids := make([]string, len(findings))
		for i, f := range findings {
			ids[i] = f.ID
		}
		sent = append(sent, ids)
		return []hygiene.ClaimVerdict{
			{Index: 0, Verdict: "confirmed"},
			{Index: 1, Verdict: "refuted", Reason: "guarded upstream"},
		}, nil
	}
	opts := baseAuditOpts(patternDir)

	var out bytes.Buffer
	if err := (&Runner{Claude: claudeMock, GitHub: gh, VerifyClaims: verify}).Run(&out, opts); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if want := [][]string{{"B-001", "C-001"}}; !reflect.DeepEqual(sent, want) {
		t.Errorf("verifier calls = %v, want one call with only the BLOCKING and CRITICAL findings %v", sent, want)
	}

	cached := cachedAudit(t, sha, opts)
	wantGates := report.GateStats{
		Anchor:  &report.AnchorGateStats{Examined: 3, Unanchored: 1},
		Snippet: &report.SnippetGateStats{Examined: 4, Demoted: 1},
		Claim:   &report.ClaimGateStats{Sent: 2, Verdicts: 2, Refuted: 1},
	}
	if cached.Gates == nil || !reflect.DeepEqual(*cached.Gates, wantGates) {
		t.Fatalf("cached gates = %+v, want %+v", cached.Gates, wantGates)
	}
	byID := make(map[string]report.Finding)
	for _, f := range cached.Findings {
		byID[f.ID] = f
	}
	if f := byID["W-001"]; f.Confidence != report.ConfidenceUncertain || !strings.HasPrefix(f.SnippetCheck, "demoted:") {
		t.Errorf("W-001 (quote found nowhere): confidence %q, snippet check %q; want uncertain and a demotion", f.Confidence, f.SnippetCheck)
	}
	if f := byID["W-002"]; f.Confidence != report.ConfidenceLikely || !strings.HasPrefix(f.SnippetCheck, "verified:") {
		t.Errorf("W-002 (missing file): confidence %q, snippet check %q; want likely and a verification", f.Confidence, f.SnippetCheck)
	}
	if f := byID["C-001"]; f.ClaimCheck != report.ClaimCheckRefuted || f.VerificationNote != "refuted: guarded upstream" {
		t.Errorf("C-001: claim check %q, note %q; want refuted with the verifier's reason", f.ClaimCheck, f.VerificationNote)
	}
	if f := byID["B-001"]; f.ClaimCheck != report.ClaimCheckConfirmed || f.SnippetCheck != report.SnippetCheckPassed || f.LineCheck != report.LineCheckAnchored {
		t.Errorf("B-001: claim %q, snippet %q, line %q; want confirmed, passed, anchored", f.ClaimCheck, f.SnippetCheck, f.LineCheck)
	}
	const note = "**Claim check**: refuted: guarded upstream"
	if !strings.Contains(out.String(), note) {
		t.Errorf("the audit report lacks %q:\n%s", note, out.String())
	}

	// A cache hit serves the gated result: no audit session and no verifier.
	hit := &Runner{
		Claude: &fakeClaude{fn: func(dir string, ctx AuditContext) (*report.ReviewResult, error) {
			t.Fatal("Audit must not be called on a cache hit")
			return nil, nil
		}},
		GitHub: gh,
		VerifyClaims: func(dir string, findings []report.Finding) ([]hygiene.ClaimVerdict, error) {
			t.Fatal("the claim verifier must not run on a cache hit")
			return nil, nil
		},
	}
	var hitOut bytes.Buffer
	if err := hit.Run(&hitOut, opts); err != nil {
		t.Fatalf("cache-hit Run returned error: %v", err)
	}
	if !strings.Contains(hitOut.String(), note) {
		t.Errorf("the cache-hit report lacks %q:\n%s", note, hitOut.String())
	}

	jsonOpts := opts
	jsonOpts.Format = formatJSON
	var jsonOut bytes.Buffer
	if err := hit.Run(&jsonOut, jsonOpts); err != nil {
		t.Fatalf("cache-hit JSON Run returned error: %v", err)
	}
	var rendered report.ReviewResult
	if err := json.Unmarshal(jsonOut.Bytes(), &rendered); err != nil {
		t.Fatalf("decoding --format json output: %v\n%s", err, jsonOut.String())
	}
	if !reflect.DeepEqual(rendered.Gates, cached.Gates) {
		t.Errorf("--format json gates = %+v, want the cached %+v", rendered.Gates, cached.Gates)
	}
}

// TestAuditRun_AnchorsQuotedLine: a finding whose quote sits on another line of
// the file it cites is moved to that line before the claim verifier sees it,
// as the review orders its gates, and the result is cached with that line.
func TestAuditRun_AnchorsQuotedLine(t *testing.T) {
	// Not t.Parallel(): cache.SetDir mutates a package-level variable.
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	const sha = "sha-audit-anchor"
	patternDir := seedPatternDir(t)
	repo := fakeRepo(t, "acme", "widgets")
	writeRepoFile(t, repo.Dir, "svc/handler.go", handlerGo)
	gh := &githubtest.Fake{
		CloneRepoFn:         func(ref string) (*github.Repo, error) { return repo, nil },
		DefaultBranchHEADFn: func(owner, name string) (string, error) { return sha, nil },
	}
	claudeMock := &fakeClaude{fn: func(dir string, ctx AuditContext) (*report.ReviewResult, error) {
		return &report.ReviewResult{Summary: "s", Findings: []report.Finding{
			{ID: "C-001", Severity: report.SeverityCritical, Confidence: report.ConfidenceLikely, Title: "raw query", File: "svc/handler.go", Line: 40, CodeSnippet: "return db.Exec(r.Query)", Problem: "p", Action: "a"},
		}}, nil
	}}
	// The place each finding had when the claim verifier was handed it.
	type place struct {
		line  int
		check string
	}
	var verified []place
	verify := func(dir string, findings []report.Finding) ([]hygiene.ClaimVerdict, error) {
		for _, f := range findings {
			verified = append(verified, place{f.Line, f.LineCheck})
		}
		return []hygiene.ClaimVerdict{{Index: 0, Verdict: "confirmed"}}, nil
	}
	opts := baseAuditOpts(patternDir)
	if err := (&Runner{Claude: claudeMock, GitHub: gh, VerifyClaims: verify}).Run(&bytes.Buffer{}, opts); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if want := []place{{4, report.LineCheckReanchored}}; !reflect.DeepEqual(verified, want) {
		t.Errorf("the claim verifier was handed %+v, want %+v: anchoring runs before the claim gate", verified, want)
	}

	cached := cachedAudit(t, sha, opts)
	if len(cached.Findings) != 1 {
		t.Fatalf("cached findings = %+v, want one", cached.Findings)
	}
	f := cached.Findings[0]
	if f.File != "svc/handler.go" || f.Line != 4 || f.LineEnd != 0 || f.LineCheck != report.LineCheckReanchored {
		t.Errorf("cached finding at %s:%d-%d (%s), want svc/handler.go:4 reanchored", f.File, f.Line, f.LineEnd, f.LineCheck)
	}
}

// TestAuditRun_NilVerifierRecordsNoClaimGate: a Runner with no claim verifier
// still runs the snippet gate, but records no claim gate, so the result reads
// as "never ran" rather than "nothing refuted".
func TestAuditRun_NilVerifierRecordsNoClaimGate(t *testing.T) {
	// Not t.Parallel(): cache.SetDir mutates a package-level variable.
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	const sha = "sha-audit-noverifier"
	patternDir := seedPatternDir(t)
	repo := fakeRepo(t, "acme", "widgets")
	writeRepoFile(t, repo.Dir, "svc/handler.go", handlerGo)
	gh := &githubtest.Fake{
		CloneRepoFn:         func(ref string) (*github.Repo, error) { return repo, nil },
		DefaultBranchHEADFn: func(owner, name string) (string, error) { return sha, nil },
	}
	claudeMock := &fakeClaude{fn: func(dir string, ctx AuditContext) (*report.ReviewResult, error) {
		return &report.ReviewResult{Summary: "s", Findings: []report.Finding{
			{ID: "C-001", Severity: report.SeverityCritical, Confidence: report.ConfidenceLikely, Title: "raw query", File: "svc/handler.go", Line: 4, CodeSnippet: "return db.Exec(r.Query)", Problem: "p", Action: "a"},
		}}, nil
	}}
	opts := baseAuditOpts(patternDir)
	if err := (&Runner{Claude: claudeMock, GitHub: gh}).Run(&bytes.Buffer{}, opts); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	cached := cachedAudit(t, sha, opts)
	if cached.Gates == nil || cached.Gates.Snippet == nil {
		t.Fatalf("cached gates = %+v, want the snippet gate recorded", cached.Gates)
	}
	if got := *cached.Gates.Snippet; got != (report.SnippetGateStats{Examined: 1}) {
		t.Errorf("snippet stats = %+v, want {Examined:1 Demoted:0}", got)
	}
	if cached.Gates.Claim != nil {
		t.Errorf("claim stats = %+v, want nil without a verifier", cached.Gates.Claim)
	}
	if got := cached.Findings[0].ClaimCheck; got != "" {
		t.Errorf("claim check = %q, want unstamped without a verifier", got)
	}
}
