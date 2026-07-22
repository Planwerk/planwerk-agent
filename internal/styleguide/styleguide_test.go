package styleguide

import (
	"os"
	"path/filepath"
	"testing"
)

// writeFile creates path (and its parent dirs) under root with dummy content.
func writeFile(t *testing.T, root, rel string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("creating dir for %s: %v", rel, err)
	}
	if err := os.WriteFile(path, []byte("# Style Guide\n"), 0o644); err != nil {
		t.Fatalf("writing %s: %v", rel, err)
	}
}

func TestFind(t *testing.T) {
	t.Run("empty repo dir yields empty path", func(t *testing.T) {
		if got := Find(""); got != "" {
			t.Errorf("Find(\"\") = %q, want empty", got)
		}
	})

	t.Run("repo without a style guide yields empty path", func(t *testing.T) {
		if got := Find(t.TempDir()); got != "" {
			t.Errorf("Find(empty repo) = %q, want empty", got)
		}
	})

	t.Run("root file is found", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, dir, "STYLE_GUIDE.md")
		if got := Find(dir); got != "STYLE_GUIDE.md" {
			t.Errorf("Find = %q, want STYLE_GUIDE.md", got)
		}
	})

	t.Run("fallback locations are found", func(t *testing.T) {
		for _, rel := range []string{".planwerk/STYLE_GUIDE.md", "docs/STYLE_GUIDE.md", ".github/STYLE_GUIDE.md"} {
			dir := t.TempDir()
			writeFile(t, dir, rel)
			if got := Find(dir); got != rel {
				t.Errorf("Find = %q, want %s", got, rel)
			}
		}
	})

	t.Run("root wins over every fallback", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, dir, "STYLE_GUIDE.md")
		writeFile(t, dir, ".planwerk/STYLE_GUIDE.md")
		writeFile(t, dir, "docs/STYLE_GUIDE.md")
		if got := Find(dir); got != "STYLE_GUIDE.md" {
			t.Errorf("Find = %q, want the root STYLE_GUIDE.md", got)
		}
	})

	t.Run(".planwerk wins over docs and .github", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, dir, ".planwerk/STYLE_GUIDE.md")
		writeFile(t, dir, "docs/STYLE_GUIDE.md")
		writeFile(t, dir, ".github/STYLE_GUIDE.md")
		if got := Find(dir); got != ".planwerk/STYLE_GUIDE.md" {
			t.Errorf("Find = %q, want .planwerk/STYLE_GUIDE.md", got)
		}
	})

	t.Run("a directory named STYLE_GUIDE.md is skipped", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, "STYLE_GUIDE.md"), 0o755); err != nil {
			t.Fatalf("creating decoy dir: %v", err)
		}
		writeFile(t, dir, "docs/STYLE_GUIDE.md")
		if got := Find(dir); got != "docs/STYLE_GUIDE.md" {
			t.Errorf("Find = %q, want docs/STYLE_GUIDE.md (the decoy directory must be skipped)", got)
		}
	})
}
