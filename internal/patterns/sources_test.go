package patterns

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// makeRepoPatterns creates a repo checkout with a .planwerk/review_patterns
// directory and returns both the repo root and the expected pattern dir.
func makeRepoPatterns(t *testing.T) (repoDir, patternDir string) {
	t.Helper()
	repoDir = t.TempDir()
	patternDir = filepath.Join(repoDir, ".planwerk", "review_patterns")
	if err := os.MkdirAll(patternDir, 0o755); err != nil {
		t.Fatal(err)
	}
	return repoDir, patternDir
}

func TestResolve(t *testing.T) {
	t.Run("orders repo, then explicit dirs", func(t *testing.T) {
		repoDir, repoPatterns := makeRepoPatterns(t)

		dirs, err := Resolve(ResolveOptions{
			RepoDir: repoDir,
			Extra:   []string{"/explicit/a", "/explicit/b"},
		})
		if err != nil {
			t.Fatalf("Resolve returned error: %v", err)
		}
		want := []string{repoPatterns, "/explicit/a", "/explicit/b"}
		if !slices.Equal(dirs, want) {
			t.Errorf("dirs = %v, want %v", dirs, want)
		}
	})

	t.Run("wiki slot sits below the repo dir", func(t *testing.T) {
		repoDir, repoPatterns := makeRepoPatterns(t)

		dirs, err := Resolve(ResolveOptions{
			RepoDir: repoDir,
			Wiki:    "/wiki/patterns",
			Extra:   []string{"/explicit/a"},
		})
		if err != nil {
			t.Fatalf("Resolve returned error: %v", err)
		}
		// The wiki ranks below the committed repo patterns: the loader lets later
		// dirs win, so the repo's reviewed patterns override the world-editable
		// wiki on a name collision.
		want := []string{"/wiki/patterns", repoPatterns, "/explicit/a"}
		if !slices.Equal(dirs, want) {
			t.Errorf("dirs = %v, want %v", dirs, want)
		}
	})

	t.Run("empty wiki adds no slot", func(t *testing.T) {
		repoDir, repoPatterns := makeRepoPatterns(t)

		dirs, err := Resolve(ResolveOptions{
			RepoDir: repoDir,
			Extra:   []string{"/explicit"},
		})
		if err != nil {
			t.Fatalf("Resolve returned error: %v", err)
		}
		want := []string{repoPatterns, "/explicit"}
		if !slices.Equal(dirs, want) {
			t.Errorf("dirs = %v, want %v", dirs, want)
		}
	})

	t.Run("NoRepo drops the repo catalog", func(t *testing.T) {
		repoDir, _ := makeRepoPatterns(t)

		dirs, err := Resolve(ResolveOptions{
			NoRepo:  true,
			RepoDir: repoDir,
			Extra:   []string{"/explicit"},
		})
		if err != nil {
			t.Fatalf("Resolve returned error: %v", err)
		}
		want := []string{"/explicit"}
		if !slices.Equal(dirs, want) {
			t.Errorf("dirs = %v, want %v", dirs, want)
		}
	})

	t.Run("NoRepo set and no explicit dirs returns empty", func(t *testing.T) {
		repoDir, _ := makeRepoPatterns(t)

		dirs, err := Resolve(ResolveOptions{
			NoRepo:  true,
			RepoDir: repoDir,
		})
		if err != nil {
			t.Fatalf("Resolve returned error: %v", err)
		}
		if len(dirs) != 0 {
			t.Errorf("dirs = %v, want empty", dirs)
		}
	})

	t.Run("skips a repo dir that does not exist", func(t *testing.T) {
		dirs, err := Resolve(ResolveOptions{
			RepoDir: filepath.Join(t.TempDir(), "no-such-repo"),
		})
		if err != nil {
			t.Fatalf("Resolve returned error: %v", err)
		}
		if len(dirs) != 0 {
			t.Errorf("dirs = %v, want empty", dirs)
		}
	})
}

func TestRepoPatternDir(t *testing.T) {
	t.Run("returns the repo pattern dir when present", func(t *testing.T) {
		repoDir, repoPatterns := makeRepoPatterns(t)
		if got := RepoPatternDir(false, repoDir); got != repoPatterns {
			t.Errorf("RepoPatternDir(false, %q) = %q, want %q", repoDir, got, repoPatterns)
		}
	})

	t.Run("returns empty when the repo dir has no patterns", func(t *testing.T) {
		if got := RepoPatternDir(false, t.TempDir()); got != "" {
			t.Errorf("RepoPatternDir(false, ...) = %q, want empty", got)
		}
	})

	t.Run("returns empty when noRepo is set", func(t *testing.T) {
		repoDir, _ := makeRepoPatterns(t)
		if got := RepoPatternDir(true, repoDir); got != "" {
			t.Errorf("RepoPatternDir(true, %q) = %q, want empty", repoDir, got)
		}
	})
}
