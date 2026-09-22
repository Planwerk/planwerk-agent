// Package gitreftest builds throwaway git repositories for tests of code that
// reads content at a ref.
package gitreftest

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Repo commits files into a fresh repository on branch main and returns its
// root. Tests then edit the working tree to stand in for a pull request's head.
func Repo(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q", "-b", "main")
	for rel, body := range files {
		Write(t, dir, rel, body)
	}
	git("add", "-A")
	git("commit", "-q", "-m", "base")
	return dir
}

// Write creates or overwrites rel under dir in the working tree only.
func Write(t *testing.T, dir, rel, body string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}
