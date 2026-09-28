package eval

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/planwerk/planwerk-agent/internal/detect"
)

func TestEnsureGoMod(t *testing.T) {
	t.Run("writes evalGoMod into a dir without one", func(t *testing.T) {
		dir := t.TempDir()
		if err := ensureGoMod(dir); err != nil {
			t.Fatalf("ensureGoMod: %v", err)
		}
		path := filepath.Join(dir, "go.mod")
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading go.mod: %v", err)
		}
		if string(got) != evalGoMod {
			t.Errorf("go.mod = %q, want %q", got, evalGoMod)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat go.mod: %v", err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Errorf("go.mod mode = %o, want 600", perm)
		}
		if tags := detect.Technologies(dir); !slices.Contains(tags, "go") {
			t.Errorf("detect.Technologies = %v, want it to include go", tags)
		}
	})

	t.Run("keeps an existing go.mod", func(t *testing.T) {
		dir := t.TempDir()
		const existing = "module example.com/own\n\ngo 1.26\n"
		writeFile(t, filepath.Join(dir, "go.mod"), existing)
		if err := ensureGoMod(dir); err != nil {
			t.Fatalf("ensureGoMod: %v", err)
		}
		got, err := os.ReadFile(filepath.Join(dir, "go.mod"))
		if err != nil {
			t.Fatalf("reading go.mod: %v", err)
		}
		if string(got) != existing {
			t.Errorf("go.mod = %q, want the existing %q", got, existing)
		}
	})

	t.Run("a dir under a regular file fails", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "file")
		writeFile(t, file, "not a dir\n")
		err := ensureGoMod(filepath.Join(file, "sub"))
		if err == nil || !strings.Contains(err.Error(), "writing go.mod") {
			t.Fatalf("ensureGoMod under a file = %v, want a writing go.mod error", err)
		}
	})
}

// TestSetupRepoCommitsGoModInBase locks that the harness-written go.mod lands in
// the base commit, so it never shows up among the files the PR changes.
func TestSetupRepoCommitsGoModInBase(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	c, err := LoadCase(writeValidCase(t, t.TempDir(), "sample"))
	if err != nil {
		t.Fatalf("LoadCase: %v", err)
	}
	repo := t.TempDir()
	changed, err := setupRepo(repo, c)
	if err != nil {
		t.Fatalf("setupRepo: %v", err)
	}
	if !slices.Equal(changed, []string{"main.go"}) {
		t.Errorf("changed = %v, want [main.go]", changed)
	}

	gitOut := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("git %s: %v", strings.Join(args, " "), err)
		}
		return string(out)
	}
	if got := gitOut("show", evalBaseBranch+":go.mod"); got != evalGoMod {
		t.Errorf("go.mod on %s = %q, want %q", evalBaseBranch, got, evalGoMod)
	}
	if got := strings.TrimSpace(gitOut("diff", "--name-only", evalBaseBranch, "HEAD")); got != "main.go" {
		t.Errorf("files changed against %s = %q, want main.go", evalBaseBranch, got)
	}
}
