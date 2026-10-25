package github

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// initLocalRepo creates a temp git repo whose origin points at the given
// remote URL, and chdirs into it for the duration of the test (UseLocalRepo /
// OpenLocalPR operate on os.Getwd).
func initLocalRepo(t *testing.T, remote string) string {
	t.Helper()
	dir := t.TempDir()
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
	if remote != "" {
		run("remote", "add", "origin", remote)
	}

	prev, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	// macOS temp dirs are symlinks (/var -> /private/var); resolve so the
	// Dir comparison below matches os.Getwd's resolved form.
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("evalsymlinks: %v", err)
	}
	if err := os.Chdir(resolved); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })
	return resolved
}

func TestUseLocalRepoInfersOrigin(t *testing.T) {
	dir := initLocalRepo(t, "git@github.com:acme/widgets.git")

	repo, err := client.UseLocalRepo("", LocalOptions{})
	if err != nil {
		t.Fatalf("UseLocalRepo: %v", err)
	}
	if repo.Owner != "acme" || repo.Name != "widgets" {
		t.Errorf("owner/name = %s/%s, want acme/widgets", repo.Owner, repo.Name)
	}
	if !repo.Local {
		t.Error("Local must be true")
	}
	if repo.Dir != dir {
		t.Errorf("Dir = %q, want %q", repo.Dir, dir)
	}

	// Cleanup must leave the working tree on disk.
	repo.Cleanup()
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("Cleanup removed the local checkout: %v", err)
	}
}

func TestUseLocalRepoMatchingRefAccepted(t *testing.T) {
	initLocalRepo(t, "https://github.com/acme/widgets.git")
	repo, err := client.UseLocalRepo("acme/widgets", LocalOptions{})
	if err != nil {
		t.Fatalf("UseLocalRepo with matching ref: %v", err)
	}
	if repo.Owner != "acme" || repo.Name != "widgets" {
		t.Errorf("owner/name = %s/%s, want acme/widgets", repo.Owner, repo.Name)
	}
}

func TestUseLocalRepoOriginMismatch(t *testing.T) {
	initLocalRepo(t, "git@github.com:acme/widgets.git")
	_, err := client.UseLocalRepo("other/repo", LocalOptions{})
	if !errors.Is(err, ErrOriginMismatch) {
		t.Fatalf("UseLocalRepo with mismatched ref = %v, want ErrOriginMismatch", err)
	}
}

func TestUseLocalRepoMissingOrigin(t *testing.T) {
	initLocalRepo(t, "")
	if _, err := client.UseLocalRepo("", LocalOptions{}); err == nil {
		t.Fatal("UseLocalRepo without an origin remote should error")
	}
}

func TestOpenLocalPROriginMismatch(t *testing.T) {
	// An explicit ref whose owner/repo differs from origin must be rejected
	// before any gh invocation, so this is safe to run without gh installed.
	initLocalRepo(t, "git@github.com:acme/widgets.git")
	_, err := client.OpenLocalPR("other/repo#1", LocalOptions{})
	if !errors.Is(err, ErrOriginMismatch) {
		t.Fatalf("OpenLocalPR with mismatched ref = %v, want ErrOriginMismatch", err)
	}
}

// initLocalPRRepo is initLocalRepo for acme/widgets with two commits. It
// returns them as first, the PR head the fake gh reports, and head, a commit
// the PR does not have, at which the checkout stays.
func initLocalPRRepo(t *testing.T) (dir, first, head string) {
	t.Helper()
	dir = initLocalRepo(t, "git@github.com:acme/widgets.git")
	for _, msg := range []string{"pushed", "unpushed"} {
		if out, err := exec.Command("git", "-C", dir, "commit", "-q", "--allow-empty", "-m", msg).CombinedOutput(); err != nil {
			t.Fatalf("git commit: %v\n%s", err, out)
		}
	}
	head, err := gitOutput(dir, "rev-parse", "HEAD")
	if err != nil {
		t.Fatalf("rev-parse HEAD: %v", err)
	}
	first, err = gitOutput(dir, "rev-parse", "HEAD~1")
	if err != nil {
		t.Fatalf("rev-parse HEAD~1: %v", err)
	}
	return dir, first, head
}

// TestOpenLocalPRPinsHead locks the head check OpenLocalPR runs after gh pr
// checkout. A branch ahead of the head GitHub reports, which gh's fast-forward
// leaves where it is, stops with ErrHeadMismatch and the --local remedy and
// keeps its commits; a confirmed move is taken. Either way the head is re-read
// once, with the query that read it first: the ref-less one, or the explicit
// ref's --repo one.
func TestOpenLocalPRPinsHead(t *testing.T) {
	t.Run("ahead branch stops with the remedy", func(t *testing.T) {
		dir, first, head := initLocalPRRepo(t)
		argvFile := fakeCheckoutGH(t, "", first, first)

		pr, err := client.OpenLocalPR("", LocalOptions{})
		if !errors.Is(err, ErrHeadMismatch) {
			t.Fatalf("err = %v, want ErrHeadMismatch", err)
		}
		if pr != nil {
			t.Errorf("pr = %+v, want nil on a pin failure", pr)
		}
		if !strings.Contains(err.Error(), "gh pr checkout --force 7") {
			t.Errorf("error %q does not name the gh pr checkout --force remedy", err)
		}
		if got, err := gitOutput(dir, "rev-parse", "HEAD"); err != nil || got != head {
			t.Errorf("HEAD = %s (%v), want the unpushed %s kept", got, err, head)
		}
		views := ghCalls(t, argvFile, "pr", "view")
		if len(views) != 2 {
			t.Fatalf("pr view calls = %q, want the read and one re-read", views)
		}
		if !slices.Equal(views[1], views[0]) || slices.Contains(views[0], "--repo") {
			t.Errorf("pr view calls = %q, want the ref-less query twice", views)
		}
	})

	t.Run("explicit ref takes a moved head", func(t *testing.T) {
		_, first, head := initLocalPRRepo(t)
		argvFile := fakeCheckoutGH(t, "", first, head)

		pr, err := client.OpenLocalPR("acme/widgets#7", LocalOptions{})
		if err != nil {
			t.Fatalf("OpenLocalPR: %v", err)
		}
		if pr.HeadSHA != head {
			t.Errorf("HeadSHA = %s, want the checked-out %s", pr.HeadSHA, head)
		}
		views := ghCalls(t, argvFile, "pr", "view")
		if len(views) != 2 {
			t.Fatalf("pr view calls = %q, want the read and one re-read", views)
		}
		i := slices.Index(views[0], "--repo")
		if !slices.Equal(views[1], views[0]) || i < 0 || i+1 >= len(views[0]) || views[0][i+1] != "acme/widgets" {
			t.Errorf("pr view calls = %q, want the --repo acme/widgets query twice", views)
		}
	})
}
