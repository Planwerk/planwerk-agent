package extract

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/planwerk/planwerk-agent/internal/github/githubtest"
	"github.com/planwerk/planwerk-agent/internal/patterns"
)

// wikiWith returns a ResolveWiki seam that resolves to a temp review_patterns
// directory seeded with the sample pattern.
func wikiWith(t *testing.T) (resolveWikiFn, patterns.ResolvedWiki) {
	t.Helper()
	patternsDir := writePatternDir(t, map[string]string{
		"sample-one.md": samplePattern,
		"Home.md":       "# Welcome\n",
	})
	resolved := patterns.ResolvedWiki{
		Repo:        "acme/widgets",
		CommitSHA:   "0123456789abcdef",
		PatternsDir: patternsDir,
	}
	return func(string, string, patterns.WikiOptions, patterns.RemoteOptions) patterns.ResolvedWiki {
		return resolved
	}, resolved
}

func TestRun_DefaultModeOpensPR(t *testing.T) {
	resolve, _ := wikiWith(t)
	gh := &githubtest.Fake{Dir: t.TempDir()}
	r := &Runner{GitHub: gh, ResolveWiki: resolve, IsTTY: func() bool { return false }}

	var w bytes.Buffer
	if err := r.Run(&w, Options{RepoRef: "acme/widgets", All: true, Version: "v1.2.3"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if gh.Count("OpenImprovementPR") != 1 {
		t.Fatalf("expected exactly one PR, got %d", gh.Count("OpenImprovementPR"))
	}
	opts := gh.ImprovementPRs()[0]
	if opts.Branch != DefaultPRBranch {
		t.Errorf("branch = %q, want %q", opts.Branch, DefaultPRBranch)
	}
	if len(opts.Files) != 1 || opts.Files[0].RelativePath != filepath.Join(".planwerk", "review_patterns", "sample-one.md") {
		t.Fatalf("unexpected PR files: %+v", opts.Files)
	}
	if string(opts.Files[0].Content) != samplePattern {
		t.Errorf("PR file content was not the verbatim pattern")
	}
	if !strings.Contains(opts.Body, "acme/widgets.wiki @ 0123456") {
		t.Errorf("PR body missing wiki provenance:\n%s", opts.Body)
	}
	if !strings.Contains(opts.Body, "v1.2.3") {
		t.Errorf("PR body missing build version in footer:\n%s", opts.Body)
	}
}

func TestRun_LocalWritesWorkingTreeNoPR(t *testing.T) {
	resolve, _ := wikiWith(t)
	dir := t.TempDir()
	gh := &githubtest.Fake{Dir: dir}
	r := &Runner{GitHub: gh, ResolveWiki: resolve, IsTTY: func() bool { return false }}

	var w bytes.Buffer
	if err := r.Run(&w, Options{Local: true, All: true}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if gh.Count("OpenImprovementPR") != 0 {
		t.Fatalf("--local must not open a PR, got %d calls", gh.Count("OpenImprovementPR"))
	}
	got, err := os.ReadFile(filepath.Join(dir, ".planwerk", "review_patterns", "sample-one.md"))
	if err != nil {
		t.Fatalf("expected the pattern written into the working tree: %v", err)
	}
	if string(got) != samplePattern {
		t.Errorf("working-tree file was not verbatim")
	}
}

func TestRun_ToCatalogNormalizesCategoryNoPR(t *testing.T) {
	resolve, _ := wikiWith(t)
	gh := &githubtest.Fake{}
	r := &Runner{GitHub: gh, ResolveWiki: resolve, IsTTY: func() bool { return false }}

	// --to-catalog writes relative to cwd, so run from a temp checkout that has
	// the catalog parent directory.
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, catalogParentDir), 0o750); err != nil {
		t.Fatalf("seeding catalog parent: %v", err)
	}
	withWorkdir(t, root)

	var w bytes.Buffer
	if err := r.Run(&w, Options{RepoRef: "acme/widgets", ToCatalog: true, All: true}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if gh.Count("OpenImprovementPR") != 0 || gh.Count("CloneRepo") != 0 {
		t.Fatalf("--to-catalog must neither clone nor open a PR, got clone=%d pr=%d", gh.Count("CloneRepo"), gh.Count("OpenImprovementPR"))
	}
	got, err := os.ReadFile(filepath.Join(root, catalogReviewSubdir, "sample-one.md"))
	if err != nil {
		t.Fatalf("expected the pattern written into the catalog: %v", err)
	}
	p, err := patterns.Parse(string(got))
	if err != nil {
		t.Fatalf("catalog file does not parse: %v", err)
	}
	if p.Category != categoryReview {
		t.Errorf("catalog category = %q, want %q", p.Category, categoryReview)
	}
}

func TestRun_ToCatalogErrorsOutsideCheckout(t *testing.T) {
	resolve, _ := wikiWith(t)
	r := &Runner{GitHub: &githubtest.Fake{}, ResolveWiki: resolve, IsTTY: func() bool { return false }}

	withWorkdir(t, t.TempDir()) // no internal/patterns/patterns here

	err := r.Run(&bytes.Buffer{}, Options{RepoRef: "acme/widgets", ToCatalog: true, All: true})
	if err == nil || !strings.Contains(err.Error(), "must run from a planwerk-agent checkout") {
		t.Fatalf("expected a checkout-guard error, got %v", err)
	}
}

func TestRun_EmptyPatternsDirIsError(t *testing.T) {
	resolve := func(string, string, patterns.WikiOptions, patterns.RemoteOptions) patterns.ResolvedWiki {
		return patterns.ResolvedWiki{Repo: "acme/widgets"} // PatternsDir == ""
	}
	r := &Runner{GitHub: &githubtest.Fake{}, ResolveWiki: resolve, IsTTY: func() bool { return false }}

	err := r.Run(&bytes.Buffer{}, Options{RepoRef: "acme/widgets", All: true})
	if err == nil || !strings.Contains(err.Error(), "no wiki review patterns to extract") {
		t.Fatalf("expected a missing-wiki error, got %v", err)
	}
}

func TestRun_NothingSelectedDoesNotOpenPR(t *testing.T) {
	resolve, _ := wikiWith(t)
	gh := &githubtest.Fake{Dir: t.TempDir()}
	r := &Runner{GitHub: gh, ResolveWiki: resolve, IsTTY: func() bool { return false }}

	var w bytes.Buffer
	// --pattern matching nothing selects zero entries.
	if err := r.Run(&w, Options{RepoRef: "acme/widgets", Patterns: []string{"nope"}}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if gh.Count("OpenImprovementPR") != 0 {
		t.Fatalf("no selection must not open a PR, got %d", gh.Count("OpenImprovementPR"))
	}
	if !strings.Contains(w.String(), "nothing extracted") {
		t.Errorf("expected a nothing-extracted message, got: %q", w.String())
	}
}

// withWorkdir changes into dir for the duration of the test and restores the
// previous working directory on cleanup.
func withWorkdir(t *testing.T, dir string) {
	t.Helper()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir %s: %v", dir, err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })
}
