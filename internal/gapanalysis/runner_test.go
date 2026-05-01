package gapanalysis

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/planwerk/planwerk-review/internal/cache"
	"github.com/planwerk/planwerk-review/internal/github"
	"github.com/planwerk/planwerk-review/internal/planwerk"
	"github.com/planwerk/planwerk-review/internal/report"
)

const testFeatureID = "CC-0001"

// fakeGitHub provides per-test stubs for the GitHubClient interface.
type fakeGitHub struct {
	cloneRepo          func(ref string) (*github.Repo, error)
	defaultBranchHEAD  func(owner, name string) (string, error)
	listExistingIssues func(owner, name string) ([]github.ExistingIssue, error)
}

func (f *fakeGitHub) CloneRepo(ref string) (*github.Repo, error) {
	return f.cloneRepo(ref)
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

// fakeClaude records call count so cache-hit tests can assert no Claude run.
type fakeClaude struct {
	calls int32
	fn    func(dir string, ctx AnalysisContext) (*Result, error)
}

func (f *fakeClaude) GapAnalysis(dir string, ctx AnalysisContext) (*Result, error) {
	atomic.AddInt32(&f.calls, 1)
	if f.fn == nil {
		return &Result{Features: []FeatureGaps{}}, nil
	}
	return f.fn(dir, ctx)
}

// repoFactory builds a fresh *github.Repo on every call so tests that
// invoke runner.Run twice work — the runner's defer repo.Cleanup() wipes
// the dir at the end of each call. Returning a new dir each time mirrors
// what a real CloneRepo does.
func repoFactory(t *testing.T, owner, name string, features map[string]planwerk.Feature) func() *github.Repo {
	t.Helper()
	return func() *github.Repo {
		dir := t.TempDir()
		completed := filepath.Join(dir, ".planwerk", "completed")
		if err := os.MkdirAll(completed, 0o750); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		for filename, f := range features {
			data, err := json.Marshal(f)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if err := os.WriteFile(filepath.Join(completed, filename), data, 0o600); err != nil {
				t.Fatalf("write feature: %v", err)
			}
		}
		return &github.Repo{Owner: owner, Name: name, Dir: dir}
	}
}

func baseGapOpts() Options {
	return Options{
		RepoRef:         "owner/repo",
		NoLocalPatterns: true,
		NoRepoPatterns:  true,
		Format:          "markdown",
		Version:         "test",
	}
}

func TestGapRun_CacheMissThenHit(t *testing.T) {
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	makeRepo := repoFactory(t, "acme", "widgets", map[string]planwerk.Feature{
		"CC-0001-foo.json": {FeatureID: testFeatureID, Title: "Foo"},
	})
	gh := &fakeGitHub{
		cloneRepo:         func(string) (*github.Repo, error) { return makeRepo(), nil },
		defaultBranchHEAD: func(string, string) (string, error) { return "sha-1", nil },
	}
	claudeMock := &fakeClaude{
		fn: func(dir string, ctx AnalysisContext) (*Result, error) {
			if len(ctx.Features) != 1 || ctx.Features[0].FeatureID != testFeatureID {
				t.Errorf("ctx.Features = %+v, want [CC-0001]", ctx.Features)
			}
			return &Result{
				RepoFullName: "acme/widgets",
				Features: []FeatureGaps{{
					FeatureID:   testFeatureID,
					FeatureFile: "CC-0001-foo.json",
					Title:       "Foo",
					Gaps: []Gap{{
						FeatureID: testFeatureID,
						Type:      GapMissingCriterion,
						Severity:  report.SeverityWarning,
						Title:     "Sample gap",
					}},
				}},
			}, nil
		},
	}
	runner := &Runner{Claude: claudeMock, GitHub: gh}

	var out bytes.Buffer
	if err := runner.Run(&out, baseGapOpts()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if claudeMock.calls != 1 {
		t.Errorf("calls = %d, want 1", claudeMock.calls)
	}
	if !strings.Contains(out.String(), "Sample gap") {
		t.Errorf("output missing gap title:\n%s", out.String())
	}

	// Second run with same SHA → cache hit, Claude must not run.
	claude2 := &fakeClaude{fn: func(string, AnalysisContext) (*Result, error) {
		t.Fatal("Claude must not be called on cache hit")
		return nil, nil
	}}
	runner2 := &Runner{Claude: claude2, GitHub: gh}
	var out2 bytes.Buffer
	if err := runner2.Run(&out2, baseGapOpts()); err != nil {
		t.Fatalf("cache-hit Run: %v", err)
	}
	if !strings.Contains(out2.String(), "Sample gap") {
		t.Errorf("cache-hit output missing gap:\n%s", out2.String())
	}
}

func TestGapRun_FilterFeatureUsesDifferentCacheKey(t *testing.T) {
	// Cache must keep filtered runs separate from full-repo runs so a
	// single-feature analysis doesn't render stale full-repo output.
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	makeRepo := repoFactory(t, "acme", "widgets", map[string]planwerk.Feature{
		"CC-0001-foo.json": {FeatureID: testFeatureID},
		"CC-0002-bar.json": {FeatureID: "CC-0002"},
	})
	gh := &fakeGitHub{
		cloneRepo:         func(string) (*github.Repo, error) { return makeRepo(), nil },
		defaultBranchHEAD: func(string, string) (string, error) { return "sha-2", nil },
	}
	claudeMock := &fakeClaude{
		fn: func(dir string, ctx AnalysisContext) (*Result, error) {
			return &Result{Features: []FeatureGaps{{FeatureID: ctx.Features[0].FeatureID, Gaps: []Gap{{Title: "g"}}}}}, nil
		},
	}
	runner := &Runner{Claude: claudeMock, GitHub: gh}

	if err := runner.Run(&bytes.Buffer{}, baseGapOpts()); err != nil {
		t.Fatalf("full run: %v", err)
	}

	// Now filter to one feature — should be a cache miss because the
	// filter participates in the cache key.
	opts := baseGapOpts()
	opts.FeatureID = testFeatureID
	if err := runner.Run(&bytes.Buffer{}, opts); err != nil {
		t.Fatalf("filtered run: %v", err)
	}
	if claudeMock.calls != 2 {
		t.Errorf("expected separate cache keys (2 calls), got %d", claudeMock.calls)
	}
}

func TestGapRun_NoCacheBypass(t *testing.T) {
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	makeRepo := repoFactory(t, "acme", "widgets", map[string]planwerk.Feature{
		"CC-0001-foo.json": {FeatureID: testFeatureID},
	})
	gh := &fakeGitHub{
		cloneRepo:         func(string) (*github.Repo, error) { return makeRepo(), nil },
		defaultBranchHEAD: func(string, string) (string, error) { return "sha-3", nil },
	}

	cacheKey := buildCacheKey("owner", "repo", "sha-3", baseGapOpts())
	if err := cache.PutRaw(cacheKey, CommandGapAnalysis, []byte(`{"features":[{"feature_id":"CC-0001","gaps":[{"title":"CACHED"}]}]}`)); err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	claudeMock := &fakeClaude{
		fn: func(string, AnalysisContext) (*Result, error) {
			return &Result{Features: []FeatureGaps{{FeatureID: testFeatureID, Gaps: []Gap{{Title: "FRESH"}}}}}, nil
		},
	}
	runner := &Runner{Claude: claudeMock, GitHub: gh}

	opts := baseGapOpts()
	opts.NoCache = true
	var out bytes.Buffer
	if err := runner.Run(&out, opts); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.Contains(out.String(), "CACHED") {
		t.Error("--no-cache rendered cached sentinel")
	}
	if !strings.Contains(out.String(), "FRESH") {
		t.Errorf("output missing FRESH:\n%s", out.String())
	}
}

func TestGapRun_HEADFailureDisablesCaching(t *testing.T) {
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	makeRepo := repoFactory(t, "acme", "widgets", map[string]planwerk.Feature{
		"CC-0001-foo.json": {FeatureID: testFeatureID},
	})
	gh := &fakeGitHub{
		cloneRepo:         func(string) (*github.Repo, error) { return makeRepo(), nil },
		defaultBranchHEAD: func(string, string) (string, error) { return "", errors.New("offline") },
	}
	claudeMock := &fakeClaude{}
	runner := &Runner{Claude: claudeMock, GitHub: gh}

	if err := runner.Run(&bytes.Buffer{}, baseGapOpts()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if err := runner.Run(&bytes.Buffer{}, baseGapOpts()); err != nil {
		t.Fatalf("Run (second): %v", err)
	}
	if claudeMock.calls != 2 {
		t.Errorf("HEAD failure should disable caching, got %d calls (want 2)", claudeMock.calls)
	}
}

func TestGapRun_LoaderErrorWhenNoFeatures(t *testing.T) {
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	dir := t.TempDir()
	gh := &fakeGitHub{
		cloneRepo:         func(string) (*github.Repo, error) { return &github.Repo{Owner: "owner", Name: "repo", Dir: dir}, nil },
		defaultBranchHEAD: func(string, string) (string, error) { return "sha", nil },
	}
	runner := &Runner{Claude: &fakeClaude{}, GitHub: gh}

	err := runner.Run(&bytes.Buffer{}, baseGapOpts())
	if err == nil || !strings.Contains(err.Error(), "loading completed features") {
		t.Fatalf("expected loader error, got: %v", err)
	}
}

func TestGapRun_DedupeAgainstExistingIssues(t *testing.T) {
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	makeRepo := repoFactory(t, "acme", "widgets", map[string]planwerk.Feature{
		"CC-0001-foo.json": {FeatureID: testFeatureID},
	})

	const trackedTitle = "Implement <criterion> for CC-0001"
	gh := &fakeGitHub{
		cloneRepo:         func(string) (*github.Repo, error) { return makeRepo(), nil },
		defaultBranchHEAD: func(string, string) (string, error) { return "sha-dedupe", nil },
		listExistingIssues: func(string, string) ([]github.ExistingIssue, error) {
			return []github.ExistingIssue{{Title: trackedTitle, URL: "https://example/1"}}, nil
		},
	}
	claudeMock := &fakeClaude{fn: func(string, AnalysisContext) (*Result, error) {
		return &Result{Features: []FeatureGaps{{
			FeatureID: testFeatureID,
			Gaps: []Gap{
				{Title: "kept gap", Suggested: IssueSuggestion{Title: "Add fresh thing for CC-0001"}},
				{Title: "dropped gap", Suggested: IssueSuggestion{Title: trackedTitle}},
			},
		}}}, nil
	}}
	runner := &Runner{Claude: claudeMock, GitHub: gh}

	var out bytes.Buffer
	if err := runner.Run(&out, baseGapOpts()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	s := out.String()
	if strings.Contains(s, "dropped gap") {
		t.Errorf("dedupe failed, dropped gap still present:\n%s", s)
	}
	if !strings.Contains(s, "kept gap") {
		t.Errorf("non-duplicate gap missing:\n%s", s)
	}
}

func TestGapRun_DedupeDisabledByFlag(t *testing.T) {
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	makeRepo := repoFactory(t, "acme", "widgets", map[string]planwerk.Feature{
		"CC-0001-foo.json": {FeatureID: testFeatureID},
	})
	listerCalled := false
	gh := &fakeGitHub{
		cloneRepo:         func(string) (*github.Repo, error) { return makeRepo(), nil },
		defaultBranchHEAD: func(string, string) (string, error) { return "sha-nodedupe", nil },
		listExistingIssues: func(string, string) ([]github.ExistingIssue, error) {
			listerCalled = true
			return nil, nil
		},
	}
	claudeMock := &fakeClaude{fn: func(string, AnalysisContext) (*Result, error) {
		return &Result{Features: []FeatureGaps{{FeatureID: testFeatureID, Gaps: []Gap{{Title: "g"}}}}}, nil
	}}
	runner := &Runner{Claude: claudeMock, GitHub: gh}

	opts := baseGapOpts()
	opts.NoIssueDedupe = true
	if err := runner.Run(&bytes.Buffer{}, opts); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if listerCalled {
		t.Error("ListExistingIssues must not be called when NoIssueDedupe=true")
	}
}

func TestAssignIDs_SeverityPrefixes(t *testing.T) {
	res := &Result{Features: []FeatureGaps{
		{FeatureID: "B", Gaps: []Gap{
			{Severity: report.SeverityCritical, Title: "c1"},
			{Severity: "warning", Title: "w1"}, // lowercase, must be normalized
		}},
		{FeatureID: "A", Gaps: []Gap{
			{Severity: report.SeverityInfo, Title: "i1"},
		}},
	}}
	assignIDs(res)

	// Features must now be sorted by FeatureID.
	if res.Features[0].FeatureID != "A" || res.Features[1].FeatureID != "B" {
		t.Errorf("features not sorted: %+v", res.Features)
	}
	// IDs must use severity prefix and increment within severity.
	want := map[string]string{"i1": "I-001", "c1": "C-001", "w1": "W-001"}
	for _, fg := range res.Features {
		for _, g := range fg.Gaps {
			if got := want[g.Title]; got != g.ID {
				t.Errorf("gap %q ID = %q, want %q", g.Title, g.ID, got)
			}
		}
	}
}
