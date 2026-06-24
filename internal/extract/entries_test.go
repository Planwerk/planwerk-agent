package extract

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sampleStem = "sample-one"

const samplePattern = `# Review Pattern: Sample One

**Review-Area**: quality
**Severity**: WARNING
**Category**: technology

## What to check

1. Something.
`

// writePatternDir lays out a temp wiki review_patterns directory with the given
// files and returns its path.
func writePatternDir(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
	}
	return dir
}

func TestReadEntries_ParsesPatternsAndSkipsNonPatterns(t *testing.T) {
	dir := writePatternDir(t, map[string]string{
		sampleStem + ".md": samplePattern,
		// Home.md is a navigation page that does not parse as a pattern.
		"Home.md": "# Welcome\n\nNavigation only.\n",
		// SOURCES.md is the documentation catalog, never a pattern.
		"SOURCES.md": "# Sources\n",
		// A non-markdown file must be ignored.
		"notes.txt": "ignore me",
	})

	entries, err := readEntries(dir)
	if err != nil {
		t.Fatalf("readEntries: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 parsed pattern, got %d: %+v", len(entries), entries)
	}
	got := entries[0]
	if got.Stem != sampleStem {
		t.Errorf("stem = %q, want %s", got.Stem, sampleStem)
	}
	if got.Name != "Sample One" {
		t.Errorf("name = %q, want Sample One", got.Name)
	}
	if got.Severity != "WARNING" {
		t.Errorf("severity = %q, want WARNING", got.Severity)
	}
	if string(got.Raw) != samplePattern {
		t.Errorf("raw bytes were not preserved verbatim")
	}
}

func TestReadEntries_SkipsSymlinks(t *testing.T) {
	// The wiki is world-editable: a *.md symlink pointing at a secret outside the
	// directory must not be followed and committed. The target is pattern-shaped
	// so that, absent the symlink guard, readEntries would parse and include it.
	dir := writePatternDir(t, map[string]string{sampleStem + ".md": samplePattern})

	secret := filepath.Join(t.TempDir(), "secret.md")
	if err := os.WriteFile(secret, []byte(samplePattern), 0o600); err != nil {
		t.Fatalf("writing secret: %v", err)
	}
	if err := os.Symlink(secret, filepath.Join(dir, "leak.md")); err != nil {
		t.Skipf("symlinks unsupported on this platform: %v", err)
	}

	entries, err := readEntries(dir)
	if err != nil {
		t.Fatalf("readEntries: %v", err)
	}
	if len(entries) != 1 || entries[0].Stem != sampleStem {
		t.Fatalf("symlinked pattern must be skipped, got %+v", entries)
	}
}

func TestReadEntries_SkipsOversizedPattern(t *testing.T) {
	// A pattern-shaped file larger than the cap would, without the bounded read,
	// be loaded whole and parsed; it must be skipped instead.
	huge := samplePattern + strings.Repeat("x", maxPatternBytes)
	dir := writePatternDir(t, map[string]string{
		sampleStem + ".md": samplePattern,
		"oversized.md":     huge,
	})

	entries, err := readEntries(dir)
	if err != nil {
		t.Fatalf("readEntries: %v", err)
	}
	if len(entries) != 1 || entries[0].Stem != sampleStem {
		t.Fatalf("oversized pattern must be skipped, got %+v", entries)
	}
}

func TestReadEntries_MissingDirIsError(t *testing.T) {
	_, err := readEntries(filepath.Join(t.TempDir(), "does-not-exist"))
	if err == nil {
		t.Fatal("expected an error for a missing patterns directory")
	}
}

func TestReadEntries_EmptyDirReturnsNoEntries(t *testing.T) {
	entries, err := readEntries(t.TempDir())
	if err != nil {
		t.Fatalf("readEntries: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected no entries, got %d", len(entries))
	}
}
