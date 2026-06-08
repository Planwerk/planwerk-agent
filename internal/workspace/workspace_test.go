package workspace

import (
	"bytes"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// initGitRepo creates a fresh git repo in a temp dir with deterministic
// identity config so status/remote commands behave the same everywhere.
func initGitRepo(t *testing.T) string {
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
	return dir
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

// stubStdinTTY overrides stdinIsTerminalFn for the duration of a test.
func stubStdinTTY(t *testing.T, isTTY bool) {
	t.Helper()
	prev := stdinIsTerminalFn
	stdinIsTerminalFn = func() bool { return isTTY }
	t.Cleanup(func() { stdinIsTerminalFn = prev })
}

type countingPrompter struct {
	answer bool
	calls  int
	asked  []string
}

func (p *countingPrompter) Confirm(message string) (bool, error) {
	p.calls++
	p.asked = append(p.asked, message)
	return p.answer, nil
}

func TestEnsureCleanCleanTree(t *testing.T) {
	dir := initGitRepo(t)
	p := &countingPrompter{}
	if err := EnsureClean(dir, false, p); err != nil {
		t.Fatalf("EnsureClean on clean tree = %v, want nil", err)
	}
	if p.calls != 0 {
		t.Errorf("clean tree must not prompt; got %d calls", p.calls)
	}
}

func TestEnsureCleanDirtyForce(t *testing.T) {
	dir := initGitRepo(t)
	writeFile(t, dir, "dirty.txt", "x")

	// Capture slog so we can assert the warning is emitted.
	var logBuf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	p := &countingPrompter{}
	if err := EnsureClean(dir, true, p); err != nil {
		t.Fatalf("EnsureClean with force on dirty tree = %v, want nil", err)
	}
	if p.calls != 0 {
		t.Errorf("--force must not prompt; got %d calls", p.calls)
	}
	if !strings.Contains(logBuf.String(), "proceeding on dirty working tree") {
		t.Errorf("expected a warning log on forced dirty tree, got:\n%s", logBuf.String())
	}
}

func TestEnsureCleanDirtyPromptYes(t *testing.T) {
	dir := initGitRepo(t)
	writeFile(t, dir, "dirty.txt", "x")
	stubStdinTTY(t, true)

	p := &countingPrompter{answer: true}
	if err := EnsureClean(dir, false, p); err != nil {
		t.Fatalf("EnsureClean with yes answer = %v, want nil", err)
	}
	if p.calls != 1 {
		t.Fatalf("expected exactly one prompt, got %d", p.calls)
	}
	if !strings.Contains(p.asked[0], "uncommitted changes") {
		t.Errorf("prompt = %q, want it to mention uncommitted changes", p.asked[0])
	}
}

func TestEnsureCleanDirtyPromptNo(t *testing.T) {
	dir := initGitRepo(t)
	writeFile(t, dir, "dirty.txt", "x")
	stubStdinTTY(t, true)

	p := &countingPrompter{answer: false}
	err := EnsureClean(dir, false, p)
	if !errors.Is(err, ErrDirtyTreeDeclined) {
		t.Fatalf("EnsureClean with no answer = %v, want ErrDirtyTreeDeclined", err)
	}
	if p.calls != 1 {
		t.Errorf("expected exactly one prompt, got %d", p.calls)
	}
}

func TestEnsureCleanDirtyNoTTYAborts(t *testing.T) {
	dir := initGitRepo(t)
	writeFile(t, dir, "dirty.txt", "x")
	stubStdinTTY(t, false)

	p := &countingPrompter{}
	err := EnsureClean(dir, false, p)
	if !errors.Is(err, ErrDirtyTreeNoTTY) {
		t.Fatalf("EnsureClean with no TTY = %v, want ErrDirtyTreeNoTTY", err)
	}
	if !strings.Contains(err.Error(), "--force") {
		t.Errorf("no-TTY error must hint at --force, got: %v", err)
	}
	if p.calls != 0 {
		t.Errorf("no-TTY path must not prompt; got %d calls", p.calls)
	}
}

func TestDetectOriginShortAndURL(t *testing.T) {
	tests := []struct {
		name      string
		remote    string
		wantOwner string
		wantName  string
	}{
		{"scp ssh", "git@github.com:acme/widgets.git", "acme", "widgets"},
		{"ssh url", "ssh://git@github.com/acme/widgets.git", "acme", "widgets"},
		{"https", "https://github.com/acme/widgets.git", "acme", "widgets"},
		{"https no .git", "https://github.com/acme/widgets", "acme", "widgets"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := initGitRepo(t)
			cmd := exec.Command("git", "remote", "add", "origin", tt.remote)
			cmd.Dir = dir
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("git remote add: %v\n%s", err, out)
			}
			owner, name, err := DetectOrigin(dir)
			if err != nil {
				t.Fatalf("DetectOrigin(%q) error: %v", tt.remote, err)
			}
			if owner != tt.wantOwner || name != tt.wantName {
				t.Errorf("DetectOrigin(%q) = (%q, %q), want (%q, %q)",
					tt.remote, owner, name, tt.wantOwner, tt.wantName)
			}
		})
	}
}

func TestDetectOriginMissingRemote(t *testing.T) {
	dir := initGitRepo(t)
	if _, _, err := DetectOrigin(dir); err == nil {
		t.Fatal("DetectOrigin without an origin remote should error")
	}
}

func TestParseOriginURLBareForm(t *testing.T) {
	owner, name, ok := parseOriginURL("acme/widgets")
	if !ok || owner != "acme" || name != "widgets" {
		t.Fatalf("parseOriginURL bare form = (%q, %q, %v), want acme/widgets/true", owner, name, ok)
	}
}
