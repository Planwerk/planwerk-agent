package github

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
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

	repo, err := UseLocalRepo("", LocalOptions{})
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
	repo, err := UseLocalRepo("acme/widgets", LocalOptions{})
	if err != nil {
		t.Fatalf("UseLocalRepo with matching ref: %v", err)
	}
	if repo.Owner != "acme" || repo.Name != "widgets" {
		t.Errorf("owner/name = %s/%s, want acme/widgets", repo.Owner, repo.Name)
	}
}

func TestUseLocalRepoOriginMismatch(t *testing.T) {
	initLocalRepo(t, "git@github.com:acme/widgets.git")
	_, err := UseLocalRepo("other/repo", LocalOptions{})
	if !errors.Is(err, ErrOriginMismatch) {
		t.Fatalf("UseLocalRepo with mismatched ref = %v, want ErrOriginMismatch", err)
	}
}

func TestUseLocalRepoMissingOrigin(t *testing.T) {
	initLocalRepo(t, "")
	if _, err := UseLocalRepo("", LocalOptions{}); err == nil {
		t.Fatal("UseLocalRepo without an origin remote should error")
	}
}

func TestOpenLocalPROriginMismatch(t *testing.T) {
	// An explicit ref whose owner/repo differs from origin must be rejected
	// before any gh invocation, so this is safe to run without gh installed.
	initLocalRepo(t, "git@github.com:acme/widgets.git")
	_, err := OpenLocalPR("other/repo#1", LocalOptions{})
	if !errors.Is(err, ErrOriginMismatch) {
		t.Fatalf("OpenLocalPR with mismatched ref = %v, want ErrOriginMismatch", err)
	}
}
