package patterns

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitInitWithCommit makes dir a git repo with one (possibly empty) commit so
// wikiHeadSHA can resolve HEAD offline.
func gitInitWithCommit(t *testing.T, dir string) {
	t.Helper()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	run("config", "user.email", "t@example.com")
	run("config", "user.name", "tester")
	run("add", "-A")
	run("commit", "-q", "-m", "wiki", "--allow-empty")
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

const wikiPatternMarkdown = `# Review Pattern: Wiki Rule

**Review-Area**: quality
**Severity**: WARNING

## What to check

Wiki-supplied rule body.
`

func TestResolveWiki(t *testing.T) {
	t.Run("resolves patterns dir, memory, and commit", func(t *testing.T) {
		cacheDir := t.TempDir()
		restore := stubFetch(func(p parsedURI, dest string) error {
			mustWrite(t, filepath.Join(dest, "review_patterns", "rule.md"), wikiPatternMarkdown)
			mustWrite(t, filepath.Join(dest, "memory", "decisions.md"), "We pin every dependency.")
			gitInitWithCommit(t, dest)
			return nil
		})
		defer restore()

		rw := ResolveWiki("acme", "widgets", WikiOptions{Enabled: true}, RemoteOptions{CacheDir: cacheDir})
		if rw.Repo != "acme/widgets" {
			t.Errorf("Repo = %q, want acme/widgets", rw.Repo)
		}
		if rw.CommitSHA == "" {
			t.Error("CommitSHA should be resolved from the wiki HEAD")
		}
		if rw.PatternsDir == "" {
			t.Error("PatternsDir should point at the wiki review_patterns dir")
		}
		if rw.Dir == "" {
			t.Error("Dir should point at the wiki clone root")
		}
		// PatternsDir is a subdirectory of the clone root, so per-entry consumers
		// can enumerate both review_patterns/ and memory/ from Dir.
		if filepath.Dir(rw.PatternsDir) != rw.Dir {
			t.Errorf("PatternsDir %q is not under the clone root Dir %q", rw.PatternsDir, rw.Dir)
		}
		if !strings.Contains(rw.Memory, "We pin every dependency.") {
			t.Errorf("Memory should carry the wiki memory page, got %q", rw.Memory)
		}
	})

	t.Run("disabled returns the zero value without fetching", func(t *testing.T) {
		restore := stubFetch(func(parsedURI, string) error {
			t.Fatal("fetchRemote must not run when the wiki is disabled")
			return nil
		})
		defer restore()

		rw := ResolveWiki("acme", "widgets", WikiOptions{Enabled: false}, RemoteOptions{CacheDir: t.TempDir()})
		if rw != (ResolvedWiki{}) {
			t.Errorf("disabled wiki = %+v, want zero", rw)
		}
	})

	t.Run("clone failure degrades to the zero value", func(t *testing.T) {
		restore := stubFetch(func(parsedURI, string) error {
			return errors.New("offline: wiki not reachable")
		})
		defer restore()

		rw := ResolveWiki("acme", "widgets", WikiOptions{Enabled: true}, RemoteOptions{CacheDir: t.TempDir()})
		if rw != (ResolvedWiki{}) {
			t.Errorf("failed wiki = %+v, want zero", rw)
		}
	})

	t.Run("missing review_patterns subdir leaves PatternsDir empty", func(t *testing.T) {
		cacheDir := t.TempDir()
		restore := stubFetch(func(p parsedURI, dest string) error {
			mustWrite(t, filepath.Join(dest, "memory", "a.md"), "a note")
			gitInitWithCommit(t, dest)
			return nil
		})
		defer restore()

		rw := ResolveWiki("acme", "widgets", WikiOptions{Enabled: true}, RemoteOptions{CacheDir: cacheDir})
		if rw.PatternsDir != "" {
			t.Errorf("PatternsDir = %q, want empty when the wiki has no review_patterns dir", rw.PatternsDir)
		}
		if !strings.Contains(rw.Memory, "a note") {
			t.Error("memory should still load when the patterns dir is absent")
		}
	})
}

// TestResolveWiki_RepoPatternsOverrideWiki proves the security-driven
// precedence: the repo's committed (reviewed, branch-protected) .planwerk
// pattern overrides a same-named, world-editable wiki pattern, because Resolve
// places the wiki slot before the repo slot and the loader lets later sources
// win.
func TestResolveWiki_RepoPatternsOverrideWiki(t *testing.T) {
	const repoVersion = "# Review Pattern: Shared Rule\n\n**Severity**: WARNING\n\n## What to check\n\nREPO-VERSION body.\n"
	const wikiVersion = "# Review Pattern: Shared Rule\n\n**Severity**: WARNING\n\n## What to check\n\nWIKI-VERSION body.\n"

	repoDir := t.TempDir()
	mustWrite(t, filepath.Join(repoDir, ".planwerk", "review_patterns", "shared.md"), repoVersion)

	cacheDir := t.TempDir()
	restore := stubFetch(func(p parsedURI, dest string) error {
		mustWrite(t, filepath.Join(dest, "review_patterns", "shared.md"), wikiVersion)
		gitInitWithCommit(t, dest)
		return nil
	})
	defer restore()

	rw := ResolveWiki("acme", "widgets", WikiOptions{Enabled: true}, RemoteOptions{CacheDir: cacheDir})
	if rw.PatternsDir == "" {
		t.Fatal("expected the wiki to expose a review_patterns dir")
	}

	dirs, err := Resolve(ResolveOptions{RepoDir: repoDir, Wiki: rw.PatternsDir})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	pats, err := LoadFilteredWithOptions(LoadOptions{NoEmbedded: true}, nil, dirs...)
	if err != nil {
		t.Fatalf("LoadFilteredWithOptions: %v", err)
	}

	var found *Pattern
	for i := range pats {
		if pats[i].Name == "Shared Rule" {
			found = &pats[i]
		}
	}
	if found == nil {
		t.Fatal(`expected a "Shared Rule" pattern to load`)
	}
	if !strings.Contains(found.Body, "REPO-VERSION") {
		t.Errorf("committed repo pattern should override the wiki pattern of the same name; body = %q", found.Body)
	}
}

func TestLoadMemory(t *testing.T) {
	t.Run("concatenates md pages in sorted order with headers", func(t *testing.T) {
		dir := t.TempDir()
		mustWrite(t, filepath.Join(dir, "02-second.md"), "Second body.")
		mustWrite(t, filepath.Join(dir, "01-first.md"), "First body.")
		mustWrite(t, filepath.Join(dir, "notes.txt"), "ignored, not markdown")
		mustWrite(t, filepath.Join(dir, "empty.md"), "   \n")

		got := LoadMemory(dir)
		first := strings.Index(got, "### 01-first")
		second := strings.Index(got, "### 02-second")
		if first < 0 || second < 0 {
			t.Fatalf("missing page headers in %q", got)
		}
		if first > second {
			t.Error("pages should be concatenated in sorted filename order")
		}
		if strings.Contains(got, "ignored") {
			t.Error("non-.md files must be ignored")
		}
		if strings.Contains(got, "### empty") {
			t.Error("whitespace-only pages must be skipped")
		}
	})

	t.Run("absent directory yields empty", func(t *testing.T) {
		if got := LoadMemory(filepath.Join(t.TempDir(), "no-such-dir")); got != "" {
			t.Errorf("LoadMemory(absent) = %q, want empty", got)
		}
	})

	t.Run("caps the total at maxMemoryBytes", func(t *testing.T) {
		dir := t.TempDir()
		half := strings.Repeat("x", maxMemoryBytes/2)
		mustWrite(t, filepath.Join(dir, "a.md"), half)
		mustWrite(t, filepath.Join(dir, "b.md"), half)
		mustWrite(t, filepath.Join(dir, "c.md"), half)

		got := LoadMemory(dir)
		if len(got) > maxMemoryBytes {
			t.Errorf("memory length = %d, want <= %d", len(got), maxMemoryBytes)
		}
		if strings.Contains(got, "### c") {
			t.Error("pages past the size cap must be skipped")
		}
	})

	t.Run("an oversized early page is skipped without suppressing later pages", func(t *testing.T) {
		dir := t.TempDir()
		// 000-huge.md sorts first and exceeds the per-file cap on its own. It must
		// be skipped (not read whole into memory, and not allowed to suppress the
		// legitimate page that sorts after it).
		mustWrite(t, filepath.Join(dir, "000-huge.md"), strings.Repeat("x", maxMemoryBytes+1))
		mustWrite(t, filepath.Join(dir, "001-real.md"), "Legitimate memory page.")

		got := LoadMemory(dir)
		if strings.Contains(got, "### 000-huge") {
			t.Error("a page exceeding the per-file cap must be skipped")
		}
		if !strings.Contains(got, "Legitimate memory page.") {
			t.Error("a legitimate page sorting after an oversized one must still load")
		}
	})

	t.Run("symlinked pages are not followed", func(t *testing.T) {
		secretDir := t.TempDir()
		secret := filepath.Join(secretDir, "credentials")
		mustWrite(t, secret, "AKIA-SUPER-SECRET-KEY")

		dir := t.TempDir()
		mustWrite(t, filepath.Join(dir, "real.md"), "A real page.")
		if err := os.Symlink(secret, filepath.Join(dir, "leak.md")); err != nil {
			t.Skipf("symlinks unsupported on this platform: %v", err)
		}

		got := LoadMemory(dir)
		if strings.Contains(got, "SUPER-SECRET") {
			t.Error("LoadMemory must not follow a *.md symlink and read its target")
		}
		if !strings.Contains(got, "A real page.") {
			t.Error("legitimate pages must still load alongside a skipped symlink")
		}
	})
}
