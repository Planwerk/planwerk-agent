package reviewprepared

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/planwerk/planwerk-review/internal/github"
	"github.com/planwerk/planwerk-review/internal/planwerk"
	"github.com/planwerk/planwerk-review/internal/report"
)

// fakeClaude is a scripted ClaudeReviewer for full Run tests.
type fakeClaude struct {
	calls atomic.Int32
	fn    func(dir string, ctx AnalysisContext) (*Result, error)
}

func (f *fakeClaude) ReviewPrepared(dir string, ctx AnalysisContext) (*Result, error) {
	f.calls.Add(1)
	if f.fn == nil {
		return &Result{}, nil
	}
	return f.fn(dir, ctx)
}

func TestAssignIDs_SeverityPrefixesAndCounters(t *testing.T) {
	r := &Result{
		Features: []FeatureReview{
			{
				FeatureID: "PX-0001",
				Findings: []Finding{
					{Severity: report.SeverityCritical, Title: "a"},
					{Severity: report.SeverityWarning, Title: "b"},
				},
			},
			{
				FeatureID: "PX-0002",
				Findings: []Finding{
					{Severity: report.SeverityCritical, Title: "c"},
					{Severity: "info", Title: "d"}, // lowercase normalisation
				},
			},
		},
	}
	assignIDs(r)

	got := []string{}
	for _, fr := range r.Features {
		for _, f := range fr.Findings {
			got = append(got, string(f.Severity)+":"+f.ID)
		}
	}
	want := []string{"CRITICAL:C-001", "WARNING:W-001", "CRITICAL:C-002", "INFO:I-001"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("ids = %v, want %v", got, want)
	}
}

func TestFilterBySeverity_DropsBelowThreshold(t *testing.T) {
	r := &Result{
		Features: []FeatureReview{{
			Findings: []Finding{
				{Severity: report.SeverityInfo, Title: "info"},
				{Severity: report.SeverityWarning, Title: "warn"},
				{Severity: report.SeverityCritical, Title: "crit"},
			},
		}},
	}
	filterBySeverity(r, report.SeverityWarning)
	titles := []string{}
	for _, f := range r.Features[0].Findings {
		titles = append(titles, f.Title)
	}
	if strings.Join(titles, ",") != "warn,crit" {
		t.Errorf("after warning filter got %v, want [warn crit]", titles)
	}
}

func TestBuildCacheKey_DifferentiatesFilters(t *testing.T) {
	a := buildCacheKey("o", "r", "abc", Options{})
	b := buildCacheKey("o", "r", "abc", Options{FeatureID: "PX-0001"})
	c := buildCacheKey("o", "r", "abc", Options{FilePath: "/tmp/PX-0001-foo.json"})
	d := buildCacheKey("o", "r", "abc", Options{FeatureID: "PX-0001", FilePath: "/tmp/PX-0001-foo.json"})
	all := map[string]bool{a: true, b: true, c: true, d: true}
	if len(all) != 4 {
		t.Errorf("expected 4 distinct cache keys for distinct filter combos, got %v", all)
	}
}

func TestEnsureTrailingNewline(t *testing.T) {
	if got := string(ensureTrailingNewline([]byte(""))); got != "" {
		t.Errorf("empty input should stay empty, got %q", got)
	}
	if got := string(ensureTrailingNewline([]byte("x"))); got != "x\n" {
		t.Errorf("missing newline should be appended, got %q", got)
	}
	if got := string(ensureTrailingNewline([]byte("x\n"))); got != "x\n" {
		t.Errorf("existing newline should be preserved once, got %q", got)
	}
}

func TestIndentJSON_PrettyPrintsCompactInput(t *testing.T) {
	in := json.RawMessage(`{"feature_id":"PX-0001","title":"Foo"}`)
	out, err := indentJSON(in)
	if err != nil {
		t.Fatalf("indentJSON: %v", err)
	}
	want := "{\n  \"feature_id\": \"PX-0001\",\n  \"title\": \"Foo\"\n}"
	if string(out) != want {
		t.Errorf("indented JSON = %q, want %q", string(out), want)
	}
}

// fakeGitHub captures the PROptions passed to OpenImprovementPR so we can
// verify the runner only requests a PR when at least one feature carries an
// ImprovedJSON payload.
type fakeGitHub struct {
	prOpts *PROptions

	// repoDir, when set, is returned as the working tree from CloneRepo /
	// CloneRepoLocal so a full Run test can load prepared features from it.
	repoDir         string
	cloneCalls      atomic.Int32
	cloneLocalCalls atomic.Int32
}

func (f *fakeGitHub) CloneRepo(string) (*github.Repo, error) {
	f.cloneCalls.Add(1)
	return &github.Repo{Owner: "o", Name: "n", Dir: f.repoDir}, nil
}

// CloneRepoLocal mirrors github.UseLocalRepo: it returns a Local repo so
// Cleanup is a no-op.
func (f *fakeGitHub) CloneRepoLocal(string, github.LocalOptions) (*github.Repo, error) {
	f.cloneLocalCalls.Add(1)
	return &github.Repo{Owner: "o", Name: "n", Dir: f.repoDir, Local: true}, nil
}
func (f *fakeGitHub) DefaultBranchHEAD(string, string) (string, error) { return "", nil }
func (f *fakeGitHub) OpenImprovementPR(_ *github.Repo, opts PROptions) (string, error) {
	f.prOpts = &opts
	return "https://example.test/pr/1", nil
}

func TestOpenPR_SkipsWhenNoImprovements(t *testing.T) {
	r := &Runner{GitHub: &fakeGitHub{}}
	url, err := r.openPR(&github.Repo{Owner: "o", Name: "n", Dir: t.TempDir()},
		&Result{Features: []FeatureReview{{FeatureID: "PX-0001", FeatureFile: "f.json"}}},
		Options{CreatePR: true})
	if err != nil {
		t.Fatalf("openPR: %v", err)
	}
	if url != "" {
		t.Errorf("expected empty URL when no improvements present, got %q", url)
	}
}

func TestOpenPR_PassesFormattedFiles(t *testing.T) {
	gh := &fakeGitHub{}
	r := &Runner{GitHub: gh}
	improved := json.RawMessage(`{"feature_id":"PX-0001","status":"prepared"}`)
	res := &Result{
		Features: []FeatureReview{{
			FeatureID:    "PX-0001",
			FeatureFile:  "PX-0001-foo.json",
			Title:        "Foo",
			ImprovedJSON: improved,
		}},
	}

	url, err := r.openPR(&github.Repo{Owner: "o", Name: "n", Dir: t.TempDir()}, res, Options{CreatePR: true})
	if err != nil {
		t.Fatalf("openPR: %v", err)
	}
	if url == "" {
		t.Fatalf("expected URL from fake gh client")
	}
	if gh.prOpts == nil || len(gh.prOpts.Files) != 1 {
		t.Fatalf("expected 1 file passed to gh client, got %+v", gh.prOpts)
	}
	got := gh.prOpts.Files[0]
	if got.RelativePath != ".planwerk/features/PX-0001-foo.json" {
		t.Errorf("unexpected path %q", got.RelativePath)
	}
	if !bytes.Contains(got.Content, []byte("\"feature_id\": \"PX-0001\"")) {
		t.Errorf("expected pretty-printed JSON, got %q", string(got.Content))
	}
	if got.Content[len(got.Content)-1] != '\n' {
		t.Errorf("expected trailing newline in file content")
	}
}

func TestRun_LocalWithCreatePR(t *testing.T) {
	dir := t.TempDir()
	writeFeature(t, dir, "PX-0001-prepared.json", planwerk.Feature{FeatureID: "PX-0001", Status: "prepared", Title: "Foo"})

	gh := &fakeGitHub{repoDir: dir}
	cl := &fakeClaude{fn: func(_ string, ctx AnalysisContext) (*Result, error) {
		if !ctx.IncludeImproved {
			t.Error("IncludeImproved must be set when --create-pr is on")
		}
		return &Result{Features: []FeatureReview{{
			FeatureID:    "PX-0001",
			FeatureFile:  "PX-0001-prepared.json",
			Title:        "Foo",
			ImprovedJSON: json.RawMessage(`{"feature_id":"PX-0001","status":"prepared"}`),
		}}}, nil
	}}
	r := &Runner{Claude: cl, GitHub: gh}

	opts := Options{
		RepoRef:         "o/n",
		NoLocalPatterns: true,
		NoRepoPatterns:  true,
		Format:          "markdown",
		Version:         "test",
		Local:           true,
		CreatePR:        true,
	}
	var out bytes.Buffer
	if err := r.Run(&out, opts); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if gh.cloneLocalCalls.Load() != 1 {
		t.Errorf("CloneRepoLocal calls = %d, want 1", gh.cloneLocalCalls.Load())
	}
	if gh.cloneCalls.Load() != 0 {
		t.Errorf("CloneRepo calls = %d, want 0 in local mode", gh.cloneCalls.Load())
	}
	if gh.prOpts == nil {
		t.Error("expected a PR to be opened when an improved feature is present")
	}
	// The local working tree must survive: every Cleanup is a no-op when Local.
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("local checkout must survive --local --create-pr: %v", err)
	}
}
