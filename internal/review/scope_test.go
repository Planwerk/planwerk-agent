package review

import (
	"bytes"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/planwerk/planwerk-agent/internal/patterns"
)

// scopeTestPatterns is a loaded catalog with an untagged, a Go and a
// Kubernetes pattern.
func scopeTestPatterns() []patterns.Pattern {
	return []patterns.Pattern{
		{Name: "Untagged", Category: "design-principle", Body: "## Rule\nKeep it simple."},
		{Name: "Go Errors", Category: "technology", AppliesWhen: []string{"go"}, Body: "## Rule\nWrap errors."},
		{Name: "Pod Limits", Category: "technology", AppliesWhen: []string{"kubernetes"}, Body: "## Rule\nSet limits."},
	}
}

// catalogDirsIn returns the entries of dir whose name starts with
// patterns.CatalogDirPrefix.
func catalogDirsIn(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}
	var found []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), patterns.CatalogDirPrefix) {
			found = append(found, e.Name())
		}
	}
	return found
}

// captureLog points the default logger at a buffer until the test ends.
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

func TestScopeCatalog_Off(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	for _, tc := range []struct {
		name string
		on   bool
		pats []patterns.Pattern
	}{
		{"the flag is off", false, scopeTestPatterns()},
		{"no pattern is loaded", true, nil},
	} {
		scope, cat, cleanup := scopeCatalog(tc.on, t.TempDir(), []string{"main.go"}, tc.pats)
		cleanup()
		if scope != nil || cat.Dir != "" || len(cat.Entries) != 0 {
			t.Errorf("%s: scope %+v, catalog %+v; want neither", tc.name, scope, cat)
		}
	}
	if left := catalogDirsIn(t, tmp); len(left) != 0 {
		t.Errorf("TMPDIR holds %v, want no catalog directory", left)
	}
}

func TestScopeCatalog_NoChangedFiles(t *testing.T) {
	logBuf := captureLog(t)
	scope, cat, cleanup := scopeCatalog(true, t.TempDir(), nil, scopeTestPatterns())
	cleanup()
	if scope != nil || cat.Dir != "" {
		t.Errorf("scope %+v, catalog %+v; want neither without changed files", scope, cat)
	}
	if !strings.Contains(logBuf.String(), "the changed files are not listed, so the review carries every pattern body") {
		t.Errorf("want a warning that the review carries every body, got log:\n%s", logBuf.String())
	}
}

func TestScopeCatalog_NothingLeftOut(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	pats := scopeTestPatterns()[:2]
	dir := t.TempDir()
	writeTestFile(t, dir, "main.go", "package main\n")

	scope, cat, cleanup := scopeCatalog(true, dir, []string{"main.go"}, pats)
	defer cleanup()
	if scope == nil || len(scope.Inline) != 2 || len(scope.Rest) != 0 {
		t.Fatalf("scope = %+v, want both patterns inline and none left out", scope)
	}
	if cat.Dir != "" || len(cat.Entries) != 0 {
		t.Errorf("catalog = %+v, want none: no pattern is indexed", cat)
	}
	if left := catalogDirsIn(t, tmp); len(left) != 0 {
		t.Errorf("TMPDIR holds %v, want no catalog directory", left)
	}
}

func TestScopeCatalog_WritesTheWholeCatalog(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	pats := scopeTestPatterns()

	scope, cat, cleanup := scopeCatalog(true, t.TempDir(), []string{"main.go"}, pats)
	if scope == nil {
		t.Fatal("scope = nil, want the split")
	}
	if got := len(scope.Rest); got != 1 || scope.Rest[0].Name != "Pod Limits" {
		t.Errorf("Rest = %+v, want the Kubernetes pattern alone", scope.Rest)
	}
	if len(cat.Entries) != len(pats) {
		t.Errorf("catalog holds %d entries, want one per loaded pattern (%d)", len(cat.Entries), len(pats))
	}
	for _, e := range cat.Entries {
		if _, err := os.Stat(filepath.Join(cat.Dir, e.File)); err != nil {
			t.Errorf("catalog file %s: %v", e.File, err)
		}
	}
	cleanup()
	if _, err := os.Stat(cat.Dir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("after cleanup, stat %s = %v, want not exist", cat.Dir, err)
	}
}

// TestScopeCatalog_WorkflowChangeInlinesTheWorkflowPattern pins the shipped
// GitHub Actions Workflow Security pattern to the files it governs: a change
// to a workflow or to a composite action's action.yml carries its body,
// matched to that file, and a Go-only change lists it in the index.
func TestScopeCatalog_WorkflowChangeInlinesTheWorkflowPattern(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	pats, err := patterns.LoadFilteredWithOptions(patterns.LoadOptions{}, []string{"go", "github-actions"})
	if err != nil {
		t.Fatalf("loading the embedded catalog: %v", err)
	}
	const name = "GitHub Actions Workflow Security"
	isWorkflowPattern := func(p patterns.Pattern) bool { return p.Name == name }
	if !slices.ContainsFunc(pats, isWorkflowPattern) {
		t.Fatalf("the embedded catalog has no %q for the github-actions tag", name)
	}
	dir := t.TempDir()
	for _, tc := range []struct {
		name    string
		files   []string
		matched []string // the files the pattern's block names; nil: indexed
	}{
		{"a workflow change", []string{".github/workflows/ci.yml", "main.go"}, []string{".github/workflows/ci.yml"}},
		{"a composite action change", []string{"action.yml"}, []string{"action.yml"}},
		{"a Go-only change", []string{"main.go"}, nil},
	} {
		scope, _, cleanup := scopeCatalog(true, dir, tc.files, pats)
		cleanup()
		if scope == nil {
			t.Fatalf("%s: scope = nil, want the split", tc.name)
		}
		inline := slices.ContainsFunc(scope.Inline, isWorkflowPattern)
		if want := tc.matched != nil; inline != want {
			t.Errorf("%s: %q inline = %v, want %v", tc.name, name, inline, want)
		}
		if got := scope.Files[name]; !slices.Equal(got, tc.matched) {
			t.Errorf("%s: Files[%q] = %v, want %v", tc.name, name, got, tc.matched)
		}
	}
}

// TestScopeCatalog_UnwritableCatalogCarriesEveryBody covers a catalog write
// that fails: the scope is dropped, so the prompt carries every body rather
// than an index that names no file.
func TestScopeCatalog_UnwritableCatalogCarriesEveryBody(t *testing.T) {
	notADir := filepath.Join(t.TempDir(), "not-a-dir")
	writeTestFile(t, filepath.Dir(notADir), filepath.Base(notADir), "x")
	t.Setenv("TMPDIR", notADir)
	logBuf := captureLog(t)

	scope, cat, cleanup := scopeCatalog(true, t.TempDir(), []string{"main.go"}, scopeTestPatterns())
	cleanup()
	if scope != nil || cat.Dir != "" {
		t.Errorf("scope %+v, catalog %+v; want neither when the catalog cannot be written", scope, cat)
	}
	if !strings.Contains(logBuf.String(), "the sessions carry the pattern bodies instead") {
		t.Errorf("want MaterializeOrWarn's warning, got log:\n%s", logBuf.String())
	}
}

// writeTestFile writes content to dir/name.
func writeTestFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
