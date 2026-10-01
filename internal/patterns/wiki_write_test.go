package patterns

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// stubPushWiki swaps the package-level pushWiki seam with a test fake and returns
// a restore function, mirroring stubFetch.
func stubPushWiki(fn func(dir string, relPaths []string, commitMsg, token string) error) func() {
	old := pushWiki
	pushWiki = fn
	return func() { pushWiki = old }
}

// stubPushWikiAdditions swaps the package-level pushWikiAdditions seam with a
// test fake and returns a restore function, mirroring stubPushWiki.
func stubPushWikiAdditions(fn func(dir string, files []WikiFile, commitMsg, token string) error) func() {
	old := pushWikiAdditions
	pushWikiAdditions = fn
	return func() { pushWikiAdditions = old }
}

func TestCloneWikiAuthenticated_DerivesWikiURLAndHead(t *testing.T) {
	var gotScheme, gotURL, gotRef string
	restore := stubFetch(func(p parsedURI, dest string) error {
		gotScheme, gotURL, gotRef = p.scheme, p.cloneURL, p.ref
		mustWrite(t, filepath.Join(dest, "review_patterns", "rule.md"), wikiPatternMarkdown)
		gitInitWithCommit(t, dest)
		return nil
	})
	defer restore()

	dir, head, cleanup, err := CloneWikiAuthenticated("acme/widgets", "main")
	if err != nil {
		t.Fatalf("CloneWikiAuthenticated: %v", err)
	}
	defer cleanup()

	if gotScheme != schemeWiki {
		t.Errorf("clone scheme = %q, want wiki", gotScheme)
	}
	if want := "https://github.com/acme/widgets.wiki.git"; gotURL != want {
		t.Errorf("clone URL = %q, want %q", gotURL, want)
	}
	if gotRef != "main" {
		t.Errorf("clone ref = %q, want main", gotRef)
	}
	if dir == "" {
		t.Error("clone dir must be non-empty")
	}
	if head == "" {
		t.Error("HEAD commit should be resolved from the fresh clone")
	}

	// cleanup removes the temp workspace.
	cleanup()
	if _, err := os.Stat(filepath.Dir(dir)); !os.IsNotExist(err) {
		t.Errorf("cleanup should remove the temp workspace, stat err = %v", err)
	}
}

func TestCloneWikiAuthenticated_FetchFailureCleansUp(t *testing.T) {
	restore := stubFetch(func(parsedURI, string) error {
		return os.ErrPermission
	})
	defer restore()

	_, _, cleanup, err := CloneWikiAuthenticated("acme/widgets", "")
	if err == nil {
		t.Fatal("expected an error when the clone fails")
	}
	if cleanup == nil {
		t.Fatal("cleanup must never be nil, even on error")
	}
	cleanup() // must be safe to call
}

func TestPushWikiDeletions_ForwardsToSeam(t *testing.T) {
	var gotDir, gotMsg string
	var gotPaths []string
	restore := stubPushWiki(func(dir string, relPaths []string, commitMsg, token string) error {
		gotDir, gotPaths, gotMsg = dir, relPaths, commitMsg
		return nil
	})
	defer restore()

	paths := []string{"review_patterns/stale.md", "memory/old.md"}
	if err := PushWikiDeletions("/tmp/wiki", paths, "prune stale entries"); err != nil {
		t.Fatalf("PushWikiDeletions: %v", err)
	}
	if gotDir != "/tmp/wiki" || gotMsg != "prune stale entries" {
		t.Errorf("forwarded dir=%q msg=%q, want /tmp/wiki / prune stale entries", gotDir, gotMsg)
	}
	if strings.Join(gotPaths, ",") != strings.Join(paths, ",") {
		t.Errorf("forwarded paths = %v, want %v", gotPaths, paths)
	}
}

func TestPushWikiDeletions_EmptyPathsIsError(t *testing.T) {
	restore := stubPushWiki(func(string, []string, string, string) error {
		t.Fatal("the seam must not run when there is nothing to delete")
		return nil
	})
	defer restore()

	if err := PushWikiDeletions("/tmp/wiki", nil, "noop"); err == nil {
		t.Fatal("expected an error for an empty deletion list")
	}
}

func TestWikiPushEnv_TokenStaysInEnvNotCleartext(t *testing.T) {
	const token = "ghs_supersecrettoken"
	env := wikiPushEnv(token)

	var countSet, keySet, valueSet bool
	for _, e := range env {
		switch e {
		case "GIT_CONFIG_COUNT=1":
			countSet = true
		case "GIT_CONFIG_KEY_0=http.extraHeader":
			keySet = true
		case "GIT_CONFIG_VALUE_0=" + wikiAuthHeader(token):
			valueSet = true
		}
		if strings.Contains(e, token) {
			t.Errorf("raw token leaked into env entry: %q", e)
		}
	}
	if !countSet || !keySet || !valueSet {
		t.Errorf("token must be injected via GIT_CONFIG_*; count=%v key=%v value=%v", countSet, keySet, valueSet)
	}

	if wikiPushEnv("") != nil {
		t.Error("no token should leave the env nil (inherited), the anonymous push path")
	}
}

// TestPushWiki_RemovesCommitsAndPushes exercises the real git rm/commit/push
// against a local bare remote (no network), proving the deletion reaches the
// remote's HEAD tree.
func TestPushWiki_RemovesCommitsAndPushes(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	bare := t.TempDir()
	git(t, bare, "init", "--bare", "-q")

	// Seed the bare remote with two pages via a throwaway clone.
	seed := t.TempDir()
	git(t, seed, "clone", "-q", bare, ".")
	git(t, seed, "config", "user.email", "t@example.com")
	git(t, seed, "config", "user.name", "tester")
	mustWrite(t, filepath.Join(seed, "review_patterns", "stale.md"), "stale\n")
	mustWrite(t, filepath.Join(seed, "review_patterns", "keep.md"), "keep\n")
	git(t, seed, "add", "-A")
	git(t, seed, "commit", "-q", "-m", "seed")
	git(t, seed, "push", "-q", "origin", "HEAD")

	// Fresh clone the deletion is applied to.
	clone := t.TempDir()
	git(t, clone, "clone", "-q", bare, ".")

	if err := pushWiki(clone, []string{"review_patterns/stale.md"}, "prune stale", ""); err != nil {
		t.Fatalf("pushWiki: %v", err)
	}

	tree := gitOut(t, bare, "ls-tree", "-r", "--name-only", "HEAD")
	if strings.Contains(tree, "stale.md") {
		t.Errorf("stale.md should be gone from the remote HEAD tree:\n%s", tree)
	}
	if !strings.Contains(tree, "keep.md") {
		t.Errorf("keep.md should still be present in the remote HEAD tree:\n%s", tree)
	}
}

func TestPushWikiAdditions_ForwardsToSeam(t *testing.T) {
	var gotDir, gotMsg string
	var gotFiles []WikiFile
	restore := stubPushWikiAdditions(func(dir string, files []WikiFile, commitMsg, token string) error {
		gotDir, gotFiles, gotMsg = dir, files, commitMsg
		return nil
	})
	defer restore()

	files := []WikiFile{
		{Path: "review_patterns/new.md", Content: "pattern body\n"},
		{Path: "memory/decision.md", Content: "memory body\n"},
	}
	if err := PushWikiAdditions("/tmp/wiki", files, "capture two pages"); err != nil {
		t.Fatalf("PushWikiAdditions: %v", err)
	}
	if gotDir != "/tmp/wiki" || gotMsg != "capture two pages" {
		t.Errorf("forwarded dir=%q msg=%q, want /tmp/wiki / capture two pages", gotDir, gotMsg)
	}
	if len(gotFiles) != 2 || gotFiles[0].Path != files[0].Path || gotFiles[1].Content != files[1].Content {
		t.Errorf("forwarded files = %+v, want %+v", gotFiles, files)
	}
}

func TestPushWikiAdditions_EmptyFilesIsError(t *testing.T) {
	restore := stubPushWikiAdditions(func(string, []WikiFile, string, string) error {
		t.Fatal("the seam must not run when there is nothing to write")
		return nil
	})
	defer restore()

	if err := PushWikiAdditions("/tmp/wiki", nil, "noop"); err == nil {
		t.Fatal("expected an error for an empty additions list")
	}
}

// TestPushWikiAdditions_WritesCommitsAndPushes exercises the real git
// add/commit/push against a local bare remote (no network), proving the new and
// updated pages reach the remote's HEAD tree with their content.
func TestPushWikiAdditions_WritesCommitsAndPushes(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	bare := t.TempDir()
	git(t, bare, "init", "--bare", "-q")

	// Seed the bare remote with one page so the test also covers updating an
	// existing page in place (the stable-slug convention).
	seed := t.TempDir()
	git(t, seed, "clone", "-q", bare, ".")
	git(t, seed, "config", "user.email", "t@example.com")
	git(t, seed, "config", "user.name", "tester")
	mustWrite(t, filepath.Join(seed, "review_patterns", "existing.md"), "old\n")
	git(t, seed, "add", "-A")
	git(t, seed, "commit", "-q", "-m", "seed")
	git(t, seed, "push", "-q", "origin", "HEAD")

	// Fresh clone the additions are applied to.
	clone := t.TempDir()
	git(t, clone, "clone", "-q", bare, ".")

	files := []WikiFile{
		{Path: "review_patterns/existing.md", Content: "updated\n"},
		{Path: "memory/fresh.md", Content: "brand new page\n"},
	}
	if err := pushWikiAdditions(clone, files, "capture pages", ""); err != nil {
		t.Fatalf("pushWikiAdditions: %v", err)
	}

	tree := gitOut(t, bare, "ls-tree", "-r", "--name-only", "HEAD")
	if !strings.Contains(tree, "memory/fresh.md") {
		t.Errorf("memory/fresh.md should be in the remote HEAD tree:\n%s", tree)
	}
	if got := gitOut(t, bare, "show", "HEAD:review_patterns/existing.md"); got != "updated\n" {
		t.Errorf("existing.md should be updated in place, got %q", got)
	}
	if got := gitOut(t, bare, "show", "HEAD:memory/fresh.md"); got != "brand new page\n" {
		t.Errorf("fresh.md content = %q, want the authored bytes", got)
	}
}

// TestPushWikiAdditions_CreatesInitialCommitOnEmptyWiki proves the
// uninitialized-wiki case: a clone of a bare remote that has never been written
// has no commits, so the first page must create the wiki's initial commit and
// the push must create its default branch.
func TestPushWikiAdditions_CreatesInitialCommitOnEmptyWiki(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	bare := t.TempDir()
	git(t, bare, "init", "--bare", "-q")

	// Clone the empty remote — no seed commit, so the clone has an unborn branch.
	clone := t.TempDir()
	git(t, clone, "clone", "-q", bare, ".")

	files := []WikiFile{{Path: "memory/first.md", Content: "the very first page\n"}}
	if err := pushWikiAdditions(clone, files, "capture first page", ""); err != nil {
		t.Fatalf("pushWikiAdditions on empty wiki: %v", err)
	}

	if got := gitOut(t, bare, "show", "HEAD:memory/first.md"); got != "the very first page\n" {
		t.Errorf("first.md content = %q, want the authored bytes on the wiki's first commit", got)
	}
}

// TestPushWikiAdditions_RebasesOnNonFastForward proves a concurrent run that
// advanced the wiki between our clone and our push does not drop this run's pages:
// the non-fast-forward push is rebased onto the updated remote HEAD and retried,
// so both the concurrent commit and our page end up in the remote HEAD tree.
func TestPushWikiAdditions_RebasesOnNonFastForward(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	bare := t.TempDir()
	git(t, bare, "init", "--bare", "-q")

	// Seed the bare remote so both clones share a base commit.
	seed := t.TempDir()
	git(t, seed, "clone", "-q", bare, ".")
	git(t, seed, "config", "user.email", "t@example.com")
	git(t, seed, "config", "user.name", "tester")
	mustWrite(t, filepath.Join(seed, "memory", "base.md"), "base\n")
	git(t, seed, "add", "-A")
	git(t, seed, "commit", "-q", "-m", "seed")
	git(t, seed, "push", "-q", "origin", "HEAD")

	// Our clone, based on the seed HEAD.
	clone := t.TempDir()
	git(t, clone, "clone", "-q", bare, ".")

	// A concurrent run advances the remote after we cloned, so our push will be a
	// non-fast-forward rejection.
	other := t.TempDir()
	git(t, other, "clone", "-q", bare, ".")
	git(t, other, "config", "user.email", "o@example.com")
	git(t, other, "config", "user.name", "other")
	mustWrite(t, filepath.Join(other, "memory", "concurrent.md"), "concurrent\n")
	git(t, other, "add", "-A")
	git(t, other, "commit", "-q", "-m", "concurrent")
	git(t, other, "push", "-q", "origin", "HEAD")

	files := []WikiFile{{Path: "memory/ours.md", Content: "our page\n"}}
	if err := pushWikiAdditions(clone, files, "capture our page", ""); err != nil {
		t.Fatalf("pushWikiAdditions should rebase and retry on non-fast-forward: %v", err)
	}

	tree := gitOut(t, bare, "ls-tree", "-r", "--name-only", "HEAD")
	for _, want := range []string{"memory/ours.md", "memory/concurrent.md", "memory/base.md"} {
		if !strings.Contains(tree, want) {
			t.Errorf("%s missing from the remote HEAD tree after rebase+retry:\n%s", want, tree)
		}
	}
}

// TestPushWikiAdditions_RefusesTraversalPath is the defence-in-depth guard: even
// if a "../" path slipped past the capture write phase, pushWikiAdditions refuses
// to write outside the clone root rather than letting os.WriteFile escape it.
func TestPushWikiAdditions_RefusesTraversalPath(t *testing.T) {
	dir := t.TempDir()
	files := []WikiFile{{Path: "../escape.md", Content: "should never be written\n"}}
	if err := pushWikiAdditions(dir, files, "capture", ""); err == nil {
		t.Fatal("pushWikiAdditions accepted a traversal path, want a refusal")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "escape.md")); err == nil {
		t.Error("a traversal path must not be written outside the clone root")
	}
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	full := append([]string{"-C", dir}, args...)
	if out, err := exec.Command("git", full...).CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	full := append([]string{"-C", dir}, args...)
	out, err := exec.Command("git", full...).Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return string(out)
}

// TestPushWikiAdditions_RefusesASymlinkOutOfTheClone covers a wiki that holds
// a symbolic link where a page is written: a page directory that points out
// of the clone, and a page file that does. Neither write may reach the
// link's target.
func TestPushWikiAdditions_RefusesASymlinkOutOfTheClone(t *testing.T) {
	for _, tc := range []struct {
		name string
		// link is the wiki-relative path of the symbolic link, and target what
		// it points at, relative to a directory outside the clone.
		link, target string
	}{
		{"page directory", "memory", "."},
		{"page file", "memory/page.md", "page.md"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clone, outside := t.TempDir(), t.TempDir()
			mustWrite(t, filepath.Join(outside, "page.md"), "the operator's file\n")
			link := filepath.Join(clone, filepath.FromSlash(tc.link))
			if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Join(outside, tc.target), link); err != nil {
				t.Fatal(err)
			}

			files := []WikiFile{{Path: "memory/page.md", Content: "a page from the wiki write\n"}, {Path: "memory/other.md", Content: "x\n"}}
			if err := pushWikiAdditions(clone, files, "capture", ""); err == nil {
				t.Fatal("pushWikiAdditions wrote through a symbolic link out of the clone, want a refusal")
			}
			got, err := os.ReadFile(filepath.Join(outside, "page.md"))
			if err != nil || string(got) != "the operator's file\n" {
				t.Errorf("the file outside the clone = %q, %v, want it untouched", got, err)
			}
			if _, err := os.Lstat(filepath.Join(outside, "other.md")); err == nil {
				t.Error("a page was created outside the clone")
			}
		})
	}
}
