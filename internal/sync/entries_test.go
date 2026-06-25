package sync

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// writeWikiFile writes content under the temp wiki dir, creating parents.
func writeWikiFile(t *testing.T, root, rel, content string) string {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
		t.Fatalf("mkdir for %s: %v", rel, err)
	}
	if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
	return full
}

func TestReadWikiEntries_EnumeratesBothKinds(t *testing.T) {
	root := t.TempDir()
	writeWikiFile(t, root, wikiPatternPath, "# Review Pattern: No raw SQL\n")
	writeWikiFile(t, root, "review_patterns/bounded-retries.md", "# Review Pattern: Bounded retries\n")
	writeWikiFile(t, root, "memory/decisions.md", "We pin every dependency.\n")
	// Navigation and non-markdown pages must be skipped.
	writeWikiFile(t, root, "review_patterns/Home.md", "# Home\n")
	writeWikiFile(t, root, "review_patterns/_Sidebar.md", "nav\n")
	writeWikiFile(t, root, "memory/notes.txt", "not markdown\n")

	entries, err := readWikiEntries(root)
	if err != nil {
		t.Fatalf("readWikiEntries: %v", err)
	}

	gotKind := map[string]string{}
	for _, e := range entries {
		gotKind[e.Path] = e.Kind
	}
	want := map[string]string{
		"memory/decisions.md":                KindMemory,
		"review_patterns/bounded-retries.md": KindPattern,
		wikiPatternPath:                      KindPattern,
	}
	if len(gotKind) != len(want) {
		t.Fatalf("enumerated %d entries, want %d: %+v", len(gotKind), len(want), entries)
	}
	for path, kind := range want {
		if gotKind[path] != kind {
			t.Errorf("%s kind = %q, want %q", path, gotKind[path], kind)
		}
	}
	// Sorted by path: memory/ sorts before review_patterns/.
	if entries[0].Path != "memory/decisions.md" {
		t.Errorf("entries not sorted by path, first = %q", entries[0].Path)
	}
}

func TestReadWikiEntries_AbsentSubdirIsSkipped(t *testing.T) {
	root := t.TempDir()
	// Only memory/ exists; review_patterns/ is absent.
	writeWikiFile(t, root, "memory/a.md", "a note\n")

	entries, err := readWikiEntries(root)
	if err != nil {
		t.Fatalf("readWikiEntries: %v", err)
	}
	if len(entries) != 1 || entries[0].Path != "memory/a.md" {
		t.Fatalf("want only memory/a.md, got %+v", entries)
	}
}

func TestReadWikiEntries_SymlinkNotFollowed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on Windows")
	}
	root := t.TempDir()
	secret := writeWikiFile(t, root, "secret.md", "TOP SECRET CREDENTIALS\n")
	writeWikiFile(t, root, "review_patterns/real.md", "# Review Pattern: Real\n")

	// A *.md symlink inside review_patterns/ pointing at the secret outside it.
	link := filepath.Join(root, "review_patterns", "leak.md")
	if err := os.Symlink(secret, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	entries, err := readWikiEntries(root)
	if err != nil {
		t.Fatalf("readWikiEntries: %v", err)
	}
	for _, e := range entries {
		if strings.Contains(e.Raw, "TOP SECRET") {
			t.Fatalf("symlink was followed and its target leaked: %+v", e)
		}
		if strings.HasSuffix(e.Path, "leak.md") {
			t.Fatalf("symlinked entry should be skipped, got %q", e.Path)
		}
	}
	if len(entries) != 1 {
		t.Fatalf("want only the real pattern, got %+v", entries)
	}
}

func TestReadWikiEntries_OversizedPageSkipped(t *testing.T) {
	root := t.TempDir()
	writeWikiFile(t, root, "memory/ok.md", "small\n")
	writeWikiFile(t, root, "memory/huge.md", strings.Repeat("x", maxEntryBytes+1))

	entries, err := readWikiEntries(root)
	if err != nil {
		t.Fatalf("readWikiEntries: %v", err)
	}
	if len(entries) != 1 || entries[0].Path != "memory/ok.md" {
		t.Fatalf("oversized page should be skipped, got %+v", entries)
	}
}
