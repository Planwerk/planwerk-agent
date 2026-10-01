package patterns

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

// captureLogs routes the default logger into the returned buffer for the rest
// of the test. A test that calls it must not run in parallel.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var logBuf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &logBuf
}

func TestFormatMemoryBodies(t *testing.T) {
	t.Run("renders each page under a header from its file name", func(t *testing.T) {
		got := FormatMemoryBodies([]MemoryPage{
			{Name: "01-first.md", Title: "First", Body: "First body."},
			{Name: "02-second.md", Title: "Second", Summary: "ignored here", Body: "Second body."},
		})
		// Byte for byte the block a prompt carried for these two pages when
		// the bodies were its only form.
		const want = "### 01-first\n\nFirst body.\n\n### 02-second\n\nSecond body."
		if got != want {
			t.Errorf("FormatMemoryBodies =\n%q\nwant\n%q", got, want)
		}
	})

	t.Run("skips pages past the total cap with a warning", func(t *testing.T) {
		logBuf := captureLogs(t)
		half := strings.Repeat("x", maxMemoryBytes/2)
		got := FormatMemoryBodies([]MemoryPage{
			{Name: "a.md", Body: half},
			{Name: "b.md", Body: half},
			{Name: "c.md", Body: "small"},
		})
		if len(got) > maxMemoryBytes {
			t.Errorf("memory length = %d, want <= %d", len(got), maxMemoryBytes)
		}
		if strings.Contains(got, "### b") {
			t.Error("a page past the total cap must be skipped")
		}
		if !strings.Contains(got, "### c\n\nsmall") {
			t.Error("a page that still fits after a skipped one must render")
		}
		log := logBuf.String()
		if !strings.Contains(log, "project memory exceeds total size cap; skipping page") || !strings.Contains(log, "page=b.md") {
			t.Errorf("expected the total-cap warning for b.md, got:\n%s", log)
		}
	})

	t.Run("no pages render the empty string", func(t *testing.T) {
		for _, pages := range [][]MemoryPage{nil, {}} {
			if got := FormatMemoryBodies(pages); got != "" {
				t.Errorf("FormatMemoryBodies(%v) = %q, want empty", pages, got)
			}
		}
	})
}

// twoMemoryPages is the page set the MaterializeMemory tests write.
func twoMemoryPages() []MemoryPage {
	return []MemoryPage{
		{Name: "a.md", Title: "Pin every dependency", Summary: "Dependencies are pinned.", Body: "# Pin every dependency\n\n**Summary**: Dependencies are pinned."},
		{Name: "b.md", Title: "Conventions", Body: "# Conventions\n\nAll HTTP errors use Problem Details."},
	}
}

// unwritablePages is a page set MaterializeMemory cannot write: the name of
// its page points into a subdirectory that does not exist.
func unwritablePages() []MemoryPage {
	return []MemoryPage{{Name: "a/b.md", Title: "Nested", Body: "body"}}
}

func TestMaterializeMemory(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	pages := twoMemoryPages()

	cat, cleanup, err := MaterializeMemory(pages)
	if err != nil {
		t.Fatalf("MaterializeMemory: %v", err)
	}
	if !filepath.IsAbs(cat.Dir) {
		t.Errorf("Dir = %q, want an absolute path", cat.Dir)
	}
	if !strings.HasPrefix(filepath.Base(cat.Dir), "planwerk-agent-memory-") {
		t.Errorf("Dir = %q, want a basename starting with planwerk-agent-memory-", cat.Dir)
	}
	if !reflect.DeepEqual(cat.Pages, pages) {
		t.Errorf("Pages = %+v, want the input pages", cat.Pages)
	}
	assertDirEntries(t, cat.Dir, "a.md", "b.md")
	for _, p := range pages {
		path := filepath.Join(cat.Dir, p.Name)
		info, err := os.Lstat(path)
		if err != nil {
			t.Fatalf("stat %s: %v", p.Name, err)
		}
		if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
			t.Errorf("%s has mode %v, want a regular file with mode 0600", p.Name, info.Mode())
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v", p.Name, err)
		}
		if string(data) != p.Body+"\n" {
			t.Errorf("%s = %q, want the body and a trailing newline", p.Name, data)
		}
	}

	cleanup()
	if _, err := os.Stat(cat.Dir); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("after cleanup, stat %s = %v, want not exist", cat.Dir, err)
	}
	cleanup() // a second call must be harmless
}

// TestMaterializeMemory_RelativeTempDirYieldsAbsoluteDir locks that Dir
// resolves even from another working directory when TMPDIR is relative.
func TestMaterializeMemory_RelativeTempDirYieldsAbsoluteDir(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.Mkdir("tmp", 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", "tmp")

	cat, cleanup, err := MaterializeMemory(twoMemoryPages())
	if err != nil {
		t.Fatalf("MaterializeMemory: %v", err)
	}
	defer cleanup()
	if !filepath.IsAbs(cat.Dir) {
		t.Fatalf("Dir = %q, want an absolute path under a relative TMPDIR", cat.Dir)
	}
	t.Chdir(t.TempDir())
	if _, err := os.Stat(filepath.Join(cat.Dir, cat.Pages[0].Name)); err != nil {
		t.Errorf("memory page does not resolve from another working directory: %v", err)
	}
}

func TestMaterializeMemory_EmptyInputCreatesNothing(t *testing.T) {
	tests := []struct {
		name  string
		pages []MemoryPage
	}{
		{"nil", nil},
		{"empty", []MemoryPage{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmp := t.TempDir()
			t.Setenv("TMPDIR", tmp)

			cat, cleanup, err := MaterializeMemory(tt.pages)
			if err != nil {
				t.Fatalf("MaterializeMemory: %v", err)
			}
			cleanup()
			if cat.Dir != "" || cat.Pages != nil {
				t.Errorf("MaterializeMemory(%s) = %+v, want the zero MemoryCatalog", tt.name, cat)
			}
			assertDirEntries(t, tmp)
		})
	}
}

func TestMaterializeMemory_WriteFailureLeavesNothingBehind(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	pages := unwritablePages()

	cat, cleanup, err := MaterializeMemory(pages)
	cleanup()
	if err == nil || !strings.Contains(err.Error(), "writing project memory file a/b.md") {
		t.Fatalf("err = %v, want one naming the page that could not be written", err)
	}
	var pathErr *fs.PathError
	if !errors.As(err, &pathErr) {
		t.Errorf("err = %v, want it to wrap the *fs.PathError", err)
	}
	if cat.Dir != "" || !reflect.DeepEqual(cat.Pages, pages) {
		t.Errorf("catalog = %+v, want the pages and an empty Dir", cat)
	}
	assertDirEntries(t, tmp)
}

func TestMaterializeMemory_UnusableTempDir(t *testing.T) {
	_, notADir := fileInTempDir(t)
	t.Setenv("TMPDIR", notADir)
	pages := twoMemoryPages()

	cat, cleanup, err := MaterializeMemory(pages)
	cleanup()
	if err == nil || !strings.Contains(err.Error(), "creating the project memory directory") {
		t.Fatalf("err = %v, want the directory-creation error", err)
	}
	if cat.Dir != "" || !reflect.DeepEqual(cat.Pages, pages) {
		t.Errorf("catalog = %+v, want the pages and an empty Dir", cat)
	}
}

// overBudgetPages returns n pages whose index lines are as long as a line
// gets, named so that they sort in the order returned.
func overBudgetPages(n int) []MemoryPage {
	pages := make([]MemoryPage, n)
	for i := range pages {
		pages[i] = MemoryPage{
			Name:    fmt.Sprintf("page-%03d.md", i),
			Title:   strings.Repeat("t", 400),
			Summary: strings.Repeat("s", 400),
			Body:    "body",
		}
	}
	return pages
}

func TestMaterializeMemoryOrWarn(t *testing.T) {
	t.Run("a write failure falls back to the page bodies", func(t *testing.T) {
		tmp := t.TempDir()
		t.Setenv("TMPDIR", tmp)
		logBuf := captureLogs(t)
		pages := unwritablePages()

		cat, cleanup := MaterializeMemoryOrWarn(pages)
		cleanup()
		if cat.Dir != "" || !reflect.DeepEqual(cat.Pages, pages) {
			t.Errorf("catalog = %+v, want the pages and an empty Dir", cat)
		}
		if !strings.Contains(logBuf.String(), "writing the project memory to disk failed; the sessions carry the page bodies instead") {
			t.Errorf("expected the fallback warning, got:\n%s", logBuf.String())
		}
		assertDirEntries(t, tmp)
	})

	t.Run("a written memory is logged", func(t *testing.T) {
		t.Setenv("TMPDIR", t.TempDir())
		logBuf := captureLogs(t)

		cat, cleanup := MaterializeMemoryOrWarn(twoMemoryPages())
		defer cleanup()
		if cat.Dir == "" {
			t.Fatal("want a Dir on success")
		}
		log := logBuf.String()
		if !strings.Contains(log, "wrote the project memory for the sessions to read") || !strings.Contains(log, "pages=2") {
			t.Errorf("expected the info line with the page count, got:\n%s", log)
		}
		if strings.Contains(log, "exceeds its size budget") {
			t.Errorf("an index within its budget must not warn, got:\n%s", log)
		}
	})

	t.Run("no pages write and log nothing", func(t *testing.T) {
		tmp := t.TempDir()
		t.Setenv("TMPDIR", tmp)
		logBuf := captureLogs(t)

		cat, cleanup := MaterializeMemoryOrWarn(nil)
		cleanup()
		if cat.Dir != "" || cat.Pages != nil {
			t.Errorf("catalog = %+v, want the zero MemoryCatalog", cat)
		}
		if logBuf.Len() != 0 {
			t.Errorf("a run without memory must log nothing, got:\n%s", logBuf.String())
		}
		assertDirEntries(t, tmp)
	})

	t.Run("an index past its budget warns with the unlisted count", func(t *testing.T) {
		t.Setenv("TMPDIR", t.TempDir())
		logBuf := captureLogs(t)
		pages := overBudgetPages(200)

		cat, cleanup := MaterializeMemoryOrWarn(pages)
		defer cleanup()
		_, unlisted := FormatMemoryIndex(cat)
		if unlisted == 0 {
			t.Fatal("the fixture must exceed the index budget")
		}
		log := logBuf.String()
		want := fmt.Sprintf("listed=%d unlisted=%d cap=65536", len(pages)-unlisted, unlisted)
		if !strings.Contains(log, "the project memory index exceeds its size budget") || !strings.Contains(log, want) {
			t.Errorf("expected the budget warning with %q, got:\n%s", want, log)
		}
	})
}

func TestFormatMemoryIndex(t *testing.T) {
	t.Run("one line per page, the summary after a bar", func(t *testing.T) {
		cat := MemoryCatalog{Dir: "/tmp/x", Pages: []MemoryPage{
			{Name: "a.md", Title: "Pin every dependency", Summary: "Dependencies are pinned."},
			{Name: "b.md", Title: "Conventions"},
		}}
		index, unlisted := FormatMemoryIndex(cat)
		const want = "- a.md: Pin every dependency | Dependencies are pinned.\n- b.md: Conventions\n"
		if index != want || unlisted != 0 {
			t.Errorf("FormatMemoryIndex = %q, %d; want %q, 0", index, unlisted, want)
		}
	})

	t.Run("a catalog without a directory or without pages renders nothing", func(t *testing.T) {
		for name, cat := range map[string]MemoryCatalog{
			"zero":          {},
			"without Dir":   {Pages: []MemoryPage{{Name: "a.md", Title: "A"}}},
			"without pages": {Dir: "/tmp/x"},
		} {
			if index, unlisted := FormatMemoryIndex(cat); index != "" || unlisted != 0 {
				t.Errorf("%s: FormatMemoryIndex = %q, %d; want \"\", 0", name, index, unlisted)
			}
		}
	})

	t.Run("a newline in a title or a summary stays on one line", func(t *testing.T) {
		cat := MemoryCatalog{Dir: "/tmp/x", Pages: []MemoryPage{
			{Name: "a.md", Title: "Pin\nevery\r\n dependency", Summary: "First.\n- evil.md: Injected line"},
		}}
		index, _ := FormatMemoryIndex(cat)
		const want = "- a.md: Pin every dependency | First. - evil.md: Injected line\n"
		if index != want {
			t.Errorf("FormatMemoryIndex = %q, want %q", index, want)
		}
	})

	t.Run("a long summary is cut on a rune boundary", func(t *testing.T) {
		// 500 two-byte runes: byte 300 is a rune start, so shift by one byte to
		// put the cut inside a rune.
		summary := "x" + strings.Repeat("é", 500)
		if len(summary) < 1000 {
			t.Fatalf("fixture is %d bytes, want at least 1000", len(summary))
		}
		cat := MemoryCatalog{Dir: "/tmp/x", Pages: []MemoryPage{{Name: "a.md", Title: "T", Summary: summary}}}
		index, _ := FormatMemoryIndex(cat)
		got := strings.TrimSuffix(strings.TrimPrefix(index, "- a.md: T | "), "\n")
		if len(got) > 303 || !strings.HasSuffix(got, "...") {
			t.Errorf("summary is %d bytes and ends %q, want at most 303 bytes ending in ...", len(got), got[len(got)-3:])
		}
		if !utf8.ValidString(got) {
			t.Errorf("the cut split a rune: %q", got)
		}
		if want := "x" + strings.Repeat("é", 149) + "..."; got != want {
			t.Errorf("summary = %q, want %q", got, want)
		}
	})

	t.Run("an index past its budget lists a prefix and counts the rest", func(t *testing.T) {
		pages := overBudgetPages(200)
		index, unlisted := FormatMemoryIndex(MemoryCatalog{Dir: "/tmp/x", Pages: pages})
		if len(index) > maxMemoryIndexBytes {
			t.Errorf("index is %d bytes, want at most %d", len(index), maxMemoryIndexBytes)
		}
		lines := strings.Split(strings.TrimSuffix(index, "\n"), "\n")
		if unlisted == 0 || len(lines)+unlisted != len(pages) {
			t.Fatalf("listed %d and unlisted %d, want them to add up to %d with some unlisted", len(lines), unlisted, len(pages))
		}
		for i, line := range lines {
			if !strings.HasPrefix(line, "- "+pages[i].Name+": ") {
				t.Fatalf("line %d = %.40q, want the page %s: the listed pages are a prefix in order", i, line, pages[i].Name)
			}
		}
	})
}
