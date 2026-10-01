package patterns

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
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
		if len(rw.MemoryPages) != 1 || rw.MemoryPages[0].Name != "decisions.md" || rw.MemoryPages[0].Body != "We pin every dependency." {
			t.Errorf("MemoryPages should carry the one wiki memory page, got %+v", rw.MemoryPages)
		}
	})

	t.Run("disabled returns the zero value without fetching", func(t *testing.T) {
		restore := stubFetch(func(parsedURI, string) error {
			t.Fatal("fetchRemote must not run when the wiki is disabled")
			return nil
		})
		defer restore()

		rw := ResolveWiki("acme", "widgets", WikiOptions{Enabled: false}, RemoteOptions{CacheDir: t.TempDir()})
		if !reflect.DeepEqual(rw, ResolvedWiki{}) {
			t.Errorf("disabled wiki = %+v, want zero", rw)
		}
		if rw.MemoryPages != nil {
			t.Errorf("disabled wiki MemoryPages = %+v, want nil", rw.MemoryPages)
		}
	})

	t.Run("clone failure degrades to the zero value", func(t *testing.T) {
		restore := stubFetch(func(parsedURI, string) error {
			return errors.New("offline: wiki not reachable")
		})
		defer restore()

		rw := ResolveWiki("acme", "widgets", WikiOptions{Enabled: true}, RemoteOptions{CacheDir: t.TempDir()})
		if !reflect.DeepEqual(rw, ResolvedWiki{}) {
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
		if len(rw.MemoryPages) != 1 || rw.MemoryPages[0].Body != "a note" {
			t.Errorf("memory should still load when the patterns dir is absent, got %+v", rw.MemoryPages)
		}
	})

	t.Run("a symlinked review_patterns directory leaves PatternsDir empty", func(t *testing.T) {
		logBuf := captureLogs(t)
		target := t.TempDir()
		mustWrite(t, filepath.Join(target, "rule.md"), wikiPatternMarkdown)

		restore := stubFetch(func(p parsedURI, dest string) error {
			mustWrite(t, filepath.Join(dest, "memory", "a.md"), "a note")
			if err := os.Symlink(target, filepath.Join(dest, "review_patterns")); err != nil {
				t.Skipf("symlinks unsupported on this platform: %v", err)
			}
			gitInitWithCommit(t, dest)
			return nil
		})
		defer restore()

		rw := ResolveWiki("acme", "widgets", WikiOptions{Enabled: true}, RemoteOptions{CacheDir: t.TempDir()})
		if rw.PatternsDir != "" {
			t.Errorf("PatternsDir = %q, want empty: a consumer would list the link's target", rw.PatternsDir)
		}
		if len(rw.MemoryPages) != 1 || rw.MemoryPages[0].Body != "a note" {
			t.Errorf("memory should still load when the patterns dir is a symlink, got %+v", rw.MemoryPages)
		}
		if !strings.Contains(logBuf.String(), "wiki review patterns directory is a symlink; skipping") {
			t.Errorf("expected the symlinked-directory warning, got:\n%s", logBuf.String())
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

// pageNames returns the file names of pages, in order.
func pageNames(pages []MemoryPage) []string {
	names := make([]string, 0, len(pages))
	for _, p := range pages {
		names = append(names, p.Name)
	}
	return names
}

// TestLoadMemoryPages covers the page filters, the warnings, and the title
// and summary an index line shows. Its subtests capture the default logger, so
// none of them runs in parallel.
func TestLoadMemoryPages(t *testing.T) {
	t.Run("returns the md pages sorted by file name", func(t *testing.T) {
		dir := t.TempDir()
		mustWrite(t, filepath.Join(dir, "02-b.md"), "Second body.\n")
		mustWrite(t, filepath.Join(dir, "01-a.md"), "  First body.  ")
		mustWrite(t, filepath.Join(dir, "notes.txt"), "ignored, not markdown")
		mustWrite(t, filepath.Join(dir, "sub", "nested.md"), "ignored, in a subdirectory")
		mustWrite(t, filepath.Join(dir, "empty.md"), "   \n")

		got := LoadMemoryPages(dir)
		want := []MemoryPage{
			{Name: "01-a.md", Title: "01-a", Body: "First body."},
			{Name: "02-b.md", Title: "02-b", Body: "Second body."},
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("LoadMemoryPages = %+v, want %+v", got, want)
		}
	})

	t.Run("absent directory yields nil and logs nothing", func(t *testing.T) {
		logBuf := captureLogs(t)
		if got := LoadMemoryPages(filepath.Join(t.TempDir(), "no-such-dir")); got != nil {
			t.Errorf("LoadMemoryPages(absent) = %+v, want nil", got)
		}
		if logBuf.Len() != 0 {
			t.Errorf("an absent directory must log nothing, got:\n%s", logBuf.String())
		}
	})

	t.Run("a directory without md files yields nil", func(t *testing.T) {
		dir := t.TempDir()
		mustWrite(t, filepath.Join(dir, "notes.txt"), "not markdown")
		if got := LoadMemoryPages(dir); got != nil {
			t.Errorf("LoadMemoryPages = %+v, want nil", got)
		}
	})

	t.Run("an oversized page is skipped without suppressing later pages", func(t *testing.T) {
		logBuf := captureLogs(t)
		dir := t.TempDir()
		// 000-huge.md sorts first and exceeds the per-file cap on its own. It must
		// be skipped (not read whole into memory, and not allowed to suppress the
		// legitimate page that sorts after it).
		mustWrite(t, filepath.Join(dir, "000-huge.md"), strings.Repeat("x", maxMemoryBytes+1))
		mustWrite(t, filepath.Join(dir, "001-real.md"), "Legitimate memory page.")

		got := LoadMemoryPages(dir)
		if names := pageNames(got); !reflect.DeepEqual(names, []string{"001-real.md"}) {
			t.Errorf("pages = %v, want only 001-real.md", names)
		}
		log := logBuf.String()
		if !strings.Contains(log, "project memory page exceeds size cap; skipping") || !strings.Contains(log, "page=000-huge.md") || !strings.Contains(log, "cap=65536") {
			t.Errorf("expected the oversized-page warning naming the page and the cap, got:\n%s", log)
		}
	})

	t.Run("an unreadable page is skipped without a warning", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("running as root: mode bits do not deny the read")
		}
		logBuf := captureLogs(t)
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "a-unreadable.md"), []byte("secret"), 0o000); err != nil {
			t.Fatal(err)
		}
		mustWrite(t, filepath.Join(dir, "b-fine.md"), "A readable page.")

		got := LoadMemoryPages(dir)
		if names := pageNames(got); !reflect.DeepEqual(names, []string{"b-fine.md"}) {
			t.Errorf("pages = %v, want only b-fine.md", names)
		}
		if logBuf.Len() != 0 {
			t.Errorf("an unreadable page must be skipped silently, got:\n%s", logBuf.String())
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

		got := LoadMemoryPages(dir)
		if names := pageNames(got); !reflect.DeepEqual(names, []string{"real.md"}) {
			t.Errorf("pages = %v, want only real.md", names)
		}
		for _, p := range got {
			if strings.Contains(p.Body, "SUPER-SECRET") {
				t.Errorf("LoadMemoryPages must not follow a *.md symlink; %s carries its target", p.Name)
			}
		}
	})

	t.Run("a symlinked memory directory is not followed", func(t *testing.T) {
		logBuf := captureLogs(t)
		target := t.TempDir()
		mustWrite(t, filepath.Join(target, "notes.md"), "PRIVATE-NOTES outside the wiki")

		dir := filepath.Join(t.TempDir(), "memory")
		if err := os.Symlink(target, dir); err != nil {
			t.Skipf("symlinks unsupported on this platform: %v", err)
		}

		if got := LoadMemoryPages(dir); got != nil {
			t.Errorf("LoadMemoryPages must not list the target of a symlinked directory, got %v", pageNames(got))
		}
		if !strings.Contains(logBuf.String(), "project memory directory is a symlink; skipping") {
			t.Errorf("expected the symlinked-directory warning, got:\n%s", logBuf.String())
		}
	})

	t.Run("the pages past the total cap are not read", func(t *testing.T) {
		logBuf := captureLogs(t)
		dir := t.TempDir()
		// Each page is at the per-page cap, so fits of them fill the total cap
		// exactly and the two that sort after them no longer fit.
		const fits = maxMemoryTotalBytes / maxMemoryBytes
		body := strings.Repeat("x", maxMemoryBytes)
		for i := range fits + 2 {
			mustWrite(t, filepath.Join(dir, fmt.Sprintf("%03d.md", i)), body)
		}

		got := LoadMemoryPages(dir)
		if len(got) != fits {
			t.Fatalf("got %d pages, want the %d that fit the total cap", len(got), fits)
		}
		if last, want := got[len(got)-1].Name, fmt.Sprintf("%03d.md", fits-1); last != want {
			t.Errorf("last page = %s, want %s: the kept pages are the first ones in file-name order", last, want)
		}
		log := logBuf.String()
		if !strings.Contains(log, "project memory exceeds total size cap; skipping the remaining pages") || !strings.Contains(log, "remaining=2") || !strings.Contains(log, "cap=16777216") {
			t.Errorf("expected the total-cap warning with the number of remaining pages and the cap, got:\n%s", log)
		}
	})

	t.Run("a page name with a control character is skipped with a warning", func(t *testing.T) {
		logBuf := captureLogs(t)
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "bad\x01.md"), []byte("An injected index line."), 0o600); err != nil {
			t.Skipf("the file system refuses a control character in a file name: %v", err)
		}
		mustWrite(t, filepath.Join(dir, "good.md"), "A real page.")

		got := LoadMemoryPages(dir)
		if names := pageNames(got); !reflect.DeepEqual(names, []string{"good.md"}) {
			t.Errorf("pages = %q, want only good.md", names)
		}
		if !strings.Contains(logBuf.String(), "project memory page name contains a control character; skipping") {
			t.Errorf("expected the control-character warning, got:\n%s", logBuf.String())
		}
	})

	t.Run("title and summary below a provenance comment", func(t *testing.T) {
		dir := t.TempDir()
		mustWrite(t, filepath.Join(dir, "pin-dependencies.md"), "<!-- planwerk-agent: captured from acme/widgets#7 -->\n\n"+
			"# Pin every dependency\n\n"+
			"**Summary**: Dependencies are pinned to exact versions.\n\n"+
			"A floating range broke the release build twice.\n")

		got := LoadMemoryPages(dir)
		if len(got) != 1 {
			t.Fatalf("got %d pages, want 1", len(got))
		}
		if got[0].Title != "Pin every dependency" || got[0].Summary != "Dependencies are pinned to exact versions." {
			t.Errorf("title, summary = %q, %q", got[0].Title, got[0].Summary)
		}
		if !strings.HasPrefix(got[0].Body, "<!-- planwerk-agent: captured from acme/widgets#7 -->") {
			t.Errorf("Body lost the provenance comment: %q", got[0].Body)
		}
	})

	t.Run("a page without a heading or a summary line", func(t *testing.T) {
		dir := t.TempDir()
		mustWrite(t, filepath.Join(dir, "conventions.md"), "## Errors\n\nAll HTTP errors use Problem Details.\n")

		got := LoadMemoryPages(dir)
		if len(got) != 1 {
			t.Fatalf("got %d pages, want 1", len(got))
		}
		if got[0].Title != "conventions" || got[0].Summary != "" {
			t.Errorf("title, summary = %q, %q; want the file name and no summary", got[0].Title, got[0].Summary)
		}
	})

	for _, fence := range []string{"```", "~~~"} {
		t.Run("lines inside a "+fence+" fence give no title and no summary", func(t *testing.T) {
			dir := t.TempDir()
			mustWrite(t, filepath.Join(dir, "shell.md"), "Run this:\n\n"+
				fence+"sh\n# comment\n**Summary**: not a summary\n"+fence+"\n\n"+
				"# Real title\n\n**Summary**: Real summary.\n")

			got := LoadMemoryPages(dir)
			if len(got) != 1 {
				t.Fatalf("got %d pages, want 1", len(got))
			}
			if got[0].Title != "Real title" || got[0].Summary != "Real summary." {
				t.Errorf("title, summary = %q, %q; want the lines after the fence", got[0].Title, got[0].Summary)
			}
		})
	}

	for _, tc := range []struct{ fence, other string }{
		{"```", "~~~"},
		{"~~~", "```"},
		// A shorter run of the same marker, or one with an info string, is code too.
		{"````", "```"},
		{"````", "````sh"},
	} {
		t.Run("a "+tc.other+" line inside a "+tc.fence+" fence does not close it", func(t *testing.T) {
			dir := t.TempDir()
			mustWrite(t, filepath.Join(dir, "mixed.md"),
				tc.fence+"\n"+tc.other+"\n# not a title\n**Summary**: not a summary\n"+tc.fence+"\n\n"+
					"# Real title\n\n**Summary**: Real summary.\n")

			got := LoadMemoryPages(dir)
			if len(got) != 1 {
				t.Fatalf("got %d pages, want 1", len(got))
			}
			if got[0].Title != "Real title" || got[0].Summary != "Real summary." {
				t.Errorf("title, summary = %q, %q; want the lines after the fence", got[0].Title, got[0].Summary)
			}
		})
	}

	t.Run("a line that starts with an inline code span opens no fence", func(t *testing.T) {
		dir := t.TempDir()
		mustWrite(t, filepath.Join(dir, "inline.md"), "```cmd``` runs it\n# Title\n**Summary**: s\n")

		got := LoadMemoryPages(dir)
		if len(got) != 1 {
			t.Fatalf("got %d pages, want 1", len(got))
		}
		if got[0].Title != "Title" || got[0].Summary != "s" {
			t.Errorf("title, summary = %q, %q; want the lines after the inline span", got[0].Title, got[0].Summary)
		}
	})

	t.Run("a fence that hides the only heading leaves the file-name title", func(t *testing.T) {
		dir := t.TempDir()
		mustWrite(t, filepath.Join(dir, "fenced.md"), "Example:\n\n```\n# comment\n**Summary**: hidden\n```\n")

		got := LoadMemoryPages(dir)
		if len(got) != 1 {
			t.Fatalf("got %d pages, want 1", len(got))
		}
		if got[0].Title != "fenced" || got[0].Summary != "" {
			t.Errorf("title, summary = %q, %q; want the file name and no summary", got[0].Title, got[0].Summary)
		}
	})
}
