package patterns

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// chdirWithLocalCatalog switches the working directory to a fresh temp dir and,
// when withLocal is true, creates a ./patterns subdir there so LocalPatternDir's
// cwd fallback has something to find. It returns the temp dir. The exe-relative
// candidate is assumed absent in the test environment, matching the runner-level
// tests' assumptions.
func chdirWithLocalCatalog(t *testing.T, withLocal bool) string {
	t.Helper()
	dir := t.TempDir()
	if withLocal {
		if err := os.MkdirAll(filepath.Join(dir, "patterns"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(dir)
	return dir
}

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
	t.Run("orders local, repo, then explicit dirs", func(t *testing.T) {
		chdirWithLocalCatalog(t, true)
		repoDir, repoPatterns := makeRepoPatterns(t)

		dirs, err := Resolve(ResolveOptions{
			RepoDir: repoDir,
			Extra:   []string{"/explicit/a", "/explicit/b"},
		})
		if err != nil {
			t.Fatalf("Resolve returned error: %v", err)
		}
		want := []string{"patterns", repoPatterns, "/explicit/a", "/explicit/b"}
		if !slices.Equal(dirs, want) {
			t.Errorf("dirs = %v, want %v", dirs, want)
		}
	})

	t.Run("NoLocal drops the bundled local catalog", func(t *testing.T) {
		chdirWithLocalCatalog(t, true)
		repoDir, repoPatterns := makeRepoPatterns(t)

		dirs, err := Resolve(ResolveOptions{
			NoLocal: true,
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
		chdirWithLocalCatalog(t, true)
		repoDir, _ := makeRepoPatterns(t)

		dirs, err := Resolve(ResolveOptions{
			NoRepo:  true,
			RepoDir: repoDir,
			Extra:   []string{"/explicit"},
		})
		if err != nil {
			t.Fatalf("Resolve returned error: %v", err)
		}
		want := []string{"patterns", "/explicit"}
		if !slices.Equal(dirs, want) {
			t.Errorf("dirs = %v, want %v", dirs, want)
		}
	})

	t.Run("both flags set returns only explicit dirs", func(t *testing.T) {
		chdirWithLocalCatalog(t, true)
		repoDir, _ := makeRepoPatterns(t)

		dirs, err := Resolve(ResolveOptions{
			NoLocal: true,
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

	t.Run("both flags set and no explicit dirs returns empty", func(t *testing.T) {
		chdirWithLocalCatalog(t, true)
		repoDir, _ := makeRepoPatterns(t)

		dirs, err := Resolve(ResolveOptions{
			NoLocal: true,
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
		chdirWithLocalCatalog(t, true)

		dirs, err := Resolve(ResolveOptions{
			RepoDir: filepath.Join(t.TempDir(), "no-such-repo"),
		})
		if err != nil {
			t.Fatalf("Resolve returned error: %v", err)
		}
		want := []string{"patterns"}
		if !slices.Equal(dirs, want) {
			t.Errorf("dirs = %v, want %v", dirs, want)
		}
	})

	t.Run("skips a missing local catalog", func(t *testing.T) {
		chdirWithLocalCatalog(t, false)
		repoDir, repoPatterns := makeRepoPatterns(t)

		dirs, err := Resolve(ResolveOptions{
			RepoDir: repoDir,
		})
		if err != nil {
			t.Fatalf("Resolve returned error: %v", err)
		}
		want := []string{repoPatterns}
		if !slices.Equal(dirs, want) {
			t.Errorf("dirs = %v, want %v", dirs, want)
		}
	})
}

func TestLocalPatternDir(t *testing.T) {
	t.Run("returns cwd patterns dir when present", func(t *testing.T) {
		chdirWithLocalCatalog(t, true)
		if got := LocalPatternDir(false); got != "patterns" {
			t.Errorf("LocalPatternDir(false) = %q, want %q", got, "patterns")
		}
	})

	t.Run("returns empty when no candidate exists", func(t *testing.T) {
		chdirWithLocalCatalog(t, false)
		if got := LocalPatternDir(false); got != "" {
			t.Errorf("LocalPatternDir(false) = %q, want empty", got)
		}
	})

	t.Run("returns empty when noLocal is set", func(t *testing.T) {
		chdirWithLocalCatalog(t, true)
		if got := LocalPatternDir(true); got != "" {
			t.Errorf("LocalPatternDir(true) = %q, want empty", got)
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
