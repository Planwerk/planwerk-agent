package patterns

import (
	"bytes"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fileInTempDir creates a regular file inside a fresh t.TempDir and returns
// the directory and the file's path. Pointing TMPDIR at the file makes
// os.MkdirTemp fail.
func fileInTempDir(t *testing.T) (dir, file string) {
	t.Helper()
	dir = t.TempDir()
	file = filepath.Join(dir, "not-a-dir")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir, file
}

// assertDirEntries fails the test unless dir holds exactly the named entries.
func assertDirEntries(t *testing.T, dir string, want ...string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}
	var got []string
	for _, e := range entries {
		got = append(got, e.Name())
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("%s holds %v, want %v", dir, got, want)
	}
}

func TestMaterialize(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	pats := []Pattern{
		{
			Name: "Go: Errors", ReviewArea: "reliability", Severity: "WARNING", Category: "technology",
			DetectionHint: "unwrapped errors", Sources: []Source{{Title: "Effective Go"}},
			Body: "## Summary\n\nWrap errors with %w.",
		},
		{
			Name: "Missing context.Context parameter", ReviewArea: "design", Severity: "INFO", Category: "design-principle",
			DetectionHint: "blocking call without a context", Sources: []Source{{Title: "Go blog", URL: "https://go.dev/blog/context"}},
			Body: "## Summary\n\nAccept a context.",
		},
	}

	cat, cleanup, err := Materialize(pats)
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}

	if !filepath.IsAbs(cat.Dir) {
		t.Errorf("Dir = %q, want an absolute path", cat.Dir)
	}
	if !strings.HasPrefix(filepath.Base(cat.Dir), CatalogDirPrefix) {
		t.Errorf("Dir = %q, want a basename starting with %s", cat.Dir, CatalogDirPrefix)
	}
	wantFiles := []string{"go-errors.md", "missing-context-context-parameter.md"}
	if len(cat.Entries) != len(pats) {
		t.Fatalf("got %d entries, want %d", len(cat.Entries), len(pats))
	}
	for i, e := range cat.Entries {
		if e.Pattern.Name != pats[i].Name || e.File != wantFiles[i] {
			t.Errorf("entry %d = {%q, %q}, want {%q, %q}", i, e.Pattern.Name, e.File, pats[i].Name, wantFiles[i])
		}
		data, err := os.ReadFile(filepath.Join(cat.Dir, e.File))
		if err != nil {
			t.Fatalf("reading %s: %v", e.File, err)
		}
		if string(data) != pats[i].FormatForPrompt() {
			t.Errorf("%s =\n%q\nwant\n%q", e.File, data, pats[i].FormatForPrompt())
		}
	}
	assertDirEntries(t, cat.Dir, wantFiles...)

	cleanup()
	if _, err := os.Stat(cat.Dir); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("after cleanup, stat %s = %v, want not exist", cat.Dir, err)
	}
	cleanup() // a second call must be harmless
}

// TestMaterialize_RelativeTempDirYieldsAbsoluteDir locks that Dir resolves
// even from another working directory when TMPDIR is relative.
func TestMaterialize_RelativeTempDirYieldsAbsoluteDir(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.Mkdir("tmp", 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", "tmp")

	cat, cleanup, err := Materialize([]Pattern{{Name: "Go: Errors"}})
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	defer cleanup()
	if !filepath.IsAbs(cat.Dir) {
		t.Fatalf("Dir = %q, want an absolute path under a relative TMPDIR", cat.Dir)
	}
	t.Chdir(t.TempDir())
	if _, err := os.Stat(filepath.Join(cat.Dir, cat.Entries[0].File)); err != nil {
		t.Errorf("catalog file does not resolve from another working directory: %v", err)
	}
}

func TestMaterialize_EmptyInputCreatesNothing(t *testing.T) {
	tests := []struct {
		name string
		pats []Pattern
	}{
		{"nil", nil},
		{"empty", []Pattern{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmp := t.TempDir()
			t.Setenv("TMPDIR", tmp)

			cat, cleanup, err := Materialize(tt.pats)
			if err != nil {
				t.Fatalf("Materialize: %v", err)
			}
			cleanup()
			if cat.Dir != "" || len(cat.Entries) != 0 {
				t.Errorf("Materialize(%s) = %+v, want the zero Catalog", tt.name, cat)
			}
			assertDirEntries(t, tmp)
		})
	}
}

func TestCatalogFileName(t *testing.T) {
	tests := []struct {
		name  string
		names []string // derived in order against one used map
		want  []string
	}{
		{"punctuation and spaces collapse to one dash", []string{"Go: Errors"}, []string{"go-errors.md"}},
		{"dots separate words", []string{"Missing context.Context parameter"}, []string{"missing-context-context-parameter.md"}},
		{"nothing left falls back to pattern", []string{"!!!"}, []string{"pattern.md"}},
		{
			"a taken name gets the next free suffix",
			[]string{"Go: Errors", "Go Errors", "go errors!"},
			[]string{"go-errors.md", "go-errors-2.md", "go-errors-3.md"},
		},
		{
			"a suffix skips a name another slug already took",
			[]string{"A", "A!", "A 2"},
			[]string{"a.md", "a-2.md", "a-2-2.md"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			used := map[string]bool{}
			for i, n := range tt.names {
				if got := catalogFileName(n, used); got != tt.want[i] {
					t.Errorf("catalogFileName(%q) = %q, want %q", n, got, tt.want[i])
				}
			}
			for _, w := range tt.want {
				if !used[w] {
					t.Errorf("used does not mark %q", w)
				}
			}
		})
	}
}

func TestMaterialize_UnusableTempDir(t *testing.T) {
	parent, notADir := fileInTempDir(t)
	t.Setenv("TMPDIR", notADir)

	cat, cleanup, err := Materialize([]Pattern{{Name: "Go: Errors"}})
	cleanup()
	if err == nil {
		t.Fatal("Materialize with a regular file as TMPDIR returned no error")
	}
	if !strings.Contains(err.Error(), "creating the pattern catalog directory") {
		t.Errorf("err = %v, want it to name the directory step", err)
	}
	var pathErr *fs.PathError
	if !errors.As(err, &pathErr) {
		t.Errorf("err = %v, want it to wrap the os.MkdirTemp *fs.PathError", err)
	}
	if cat.Dir != "" || len(cat.Entries) != 0 {
		t.Errorf("Catalog = %+v, want the zero Catalog on error", cat)
	}
	assertDirEntries(t, parent, "not-a-dir")
}

func TestMaterialize_WriteFailureRemovesTheDirectory(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	// The first file is written before the second fails on a name past the
	// file system's limit, so the directory is not empty when it is removed.
	pats := []Pattern{{Name: "Go: Errors"}, {Name: strings.Repeat("a", 300)}}

	cat, cleanup, err := Materialize(pats)
	cleanup()
	if err == nil {
		t.Fatal("Materialize with an overlong file name returned no error")
	}
	if !strings.Contains(err.Error(), "writing pattern catalog file") {
		t.Errorf("err = %v, want it to name the write step", err)
	}
	if cat.Dir != "" || len(cat.Entries) != 0 {
		t.Errorf("Catalog = %+v, want the zero Catalog on error", cat)
	}
	assertDirEntries(t, tmp)
}

func TestFormatCatalogIndex(t *testing.T) {
	tests := []struct {
		name string
		cat  Catalog
		want string
	}{
		{
			"severity is shown and the hint trimmed",
			Catalog{Dir: "/tmp/x", Entries: []CatalogEntry{{
				File:    "go-errors.md",
				Pattern: Pattern{Name: "Go: Errors", ReviewArea: "reliability", Severity: "WARNING", DetectionHint: "  unwrapped errors  "},
			}}},
			"- go-errors.md: Go: Errors (reliability, WARNING): unwrapped errors\n",
		},
		{
			"an empty severity drops its clause",
			Catalog{Dir: "/tmp/x", Entries: []CatalogEntry{{
				File:    "tdd.md",
				Pattern: Pattern{Name: "TDD", ReviewArea: "testing", DetectionHint: "code without a test", Body: "a long body"},
			}}},
			"- tdd.md: TDD (testing): code without a test\n",
		},
		{
			"an empty hint asks for the file to be read up front",
			Catalog{Dir: "/tmp/x", Entries: []CatalogEntry{{
				File:    "no-raw-sql.md",
				Pattern: Pattern{Name: "No raw SQL", ReviewArea: "security", Severity: "BLOCKING", DetectionHint: "  "},
			}}},
			"- no-raw-sql.md: No raw SQL (security, BLOCKING): (no detection hint: read this file in full before you start)\n",
		},
		{"the zero catalog renders nothing", Catalog{}, ""},
		{"a catalog without entries renders nothing", Catalog{Dir: "/tmp/x"}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FormatCatalogIndex(tt.cat, DefaultMaxPatternsInPrompt); got != tt.want {
				t.Errorf("FormatCatalogIndex =\n%q\nwant\n%q", got, tt.want)
			}
		})
	}
}

// TestFormatCatalogIndex_HonorsBudget locks that the index respects
// --max-patterns with the same severity-first priority FormatIndexForPrompt uses.
func TestFormatCatalogIndex_HonorsBudget(t *testing.T) {
	cat := Catalog{Dir: "/tmp/x", Entries: []CatalogEntry{
		{File: "low.md", Pattern: Pattern{Name: "Low", ReviewArea: "quality", Severity: "INFO", DetectionHint: "x"}},
		{File: "high.md", Pattern: Pattern{Name: "High", ReviewArea: "security", Severity: "BLOCKING", DetectionHint: "y"}},
	}}
	got := FormatCatalogIndex(cat, 1)
	if want := "- high.md: High (security, BLOCKING): y\n"; got != want {
		t.Errorf("budget of 1 should keep the highest severity only, got %q, want %q", got, want)
	}
}

// TestFormatCatalogIndex_RepeatedNameKeepsItsFile locks that entries sharing a
// name stay paired with their own files when the budget reorders them.
func TestFormatCatalogIndex_RepeatedNameKeepsItsFile(t *testing.T) {
	cat := Catalog{Dir: "/tmp/x", Entries: []CatalogEntry{
		{File: "dup.md", Pattern: Pattern{Name: "Dup", ReviewArea: "quality", Severity: "INFO", DetectionHint: "x"}},
		{File: "dup-2.md", Pattern: Pattern{Name: "Dup", ReviewArea: "security", Severity: "BLOCKING", DetectionHint: "y"}},
	}}
	if got, want := FormatCatalogIndex(cat, 1), "- dup-2.md: Dup (security, BLOCKING): y\n"; got != want {
		t.Errorf("with a budget of 1, got %q, want %q", got, want)
	}
	want := "- dup.md: Dup (quality, INFO): x\n- dup-2.md: Dup (security, BLOCKING): y\n"
	if got := FormatCatalogIndex(cat, DefaultMaxPatternsInPrompt); got != want {
		t.Errorf("without a budget, got %q, want %q", got, want)
	}
}

func TestMaterializeOrWarn_FallsBack(t *testing.T) {
	_, notADir := fileInTempDir(t)
	t.Setenv("TMPDIR", notADir)

	var logBuf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	cat, cleanup := MaterializeOrWarn([]Pattern{{Name: "Go: Errors"}})
	cleanup()
	if cat.Dir != "" || len(cat.Entries) != 0 {
		t.Errorf("Catalog = %+v, want the zero Catalog on fallback", cat)
	}
	if !strings.Contains(logBuf.String(), "writing the pattern catalog to disk failed") {
		t.Errorf("expected a fallback warning, got:\n%s", logBuf.String())
	}
}
