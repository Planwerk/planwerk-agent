package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// runDraftCmd executes the draft subcommand hermetically: it exercises only the
// RunE validation and argument wiring. The abort cases return before any
// git/gh/Claude call, so no real backend is touched.
func runDraftCmd(t *testing.T, args ...string) ([]byte, error) {
	t.Helper()
	var out, errBuf bytes.Buffer
	cmd := newDraftCmd(&runtimeDeps{version: "test"})
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.Bytes(), err
}

func TestDraftCmd_UnknownFormat(t *testing.T) {
	_, err := runDraftCmd(t, "--format", "yaml", "acme/widgets", "an idea")
	if err == nil || !strings.Contains(err.Error(), "unknown format") {
		t.Fatalf("expected an unknown-format error, got %v", err)
	}
}

func TestDraftCmd_PrintModesMutuallyExclusive(t *testing.T) {
	_, err := runDraftCmd(t, "--print-prompt", "--print-bare-prompt", "an idea")
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("expected a mutually-exclusive error, got %v", err)
	}
}

func TestDraftCmd_NonLocalRequiresRepoRef(t *testing.T) {
	_, err := runDraftCmd(t)
	if err == nil || !strings.Contains(err.Error(), "requires a repository reference") {
		t.Fatalf("expected a requires-repo-ref error, got %v", err)
	}
}

func TestDraftCmd_NonInteractiveWithoutSeedAborts(t *testing.T) {
	// A repo-ref but no idea, with --no-interactive: there is no way to obtain
	// a seed, so the run must abort before drafting. TTY-independent.
	_, err := runDraftCmd(t, "--no-interactive", "acme/widgets")
	if err == nil || !strings.Contains(err.Error(), "--no-interactive") {
		t.Fatalf("expected a --no-interactive seed abort, got %v", err)
	}
}

func TestDraftCmd_NoSeedNonTTYAborts(t *testing.T) {
	// Redirect stdin to a non-TTY pipe so the TTY check is deterministic
	// regardless of how `go test` is invoked.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	orig := os.Stdin
	os.Stdin = r
	t.Cleanup(func() {
		os.Stdin = orig
		_ = r.Close()
		_ = w.Close()
	})

	_, err = runDraftCmd(t, "acme/widgets")
	if err == nil || !strings.Contains(err.Error(), "stdin is not a TTY") {
		t.Fatalf("expected a non-TTY seed abort, got %v", err)
	}
}

func TestDraftCmd_PrintPromptRequiresSeed(t *testing.T) {
	_, err := runDraftCmd(t, "--print-prompt")
	if err == nil || !strings.Contains(err.Error(), "requires an idea") {
		t.Fatalf("expected a print-requires-idea error, got %v", err)
	}
}

func TestDraftCmd_PrintPromptRendersFromIdea(t *testing.T) {
	out, err := runDraftCmd(t, "--print-bare-prompt", "add a dark mode toggle")
	if err != nil {
		t.Fatalf("print-bare-prompt should succeed, got %v", err)
	}
	if !strings.Contains(string(out), "add a dark mode toggle") {
		t.Fatalf("rendered prompt missing the idea:\n%s", out)
	}
}
