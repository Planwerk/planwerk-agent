package main

import (
	"bytes"
	"strings"
	"testing"
)

// runGlossaryCmd executes the glossary subcommand hermetically: it exercises
// only the RunE validation and flag wiring. The abort cases return before any
// gh/Claude call, so no real backend is touched.
func runGlossaryCmd(t *testing.T, args ...string) ([]byte, error) {
	t.Helper()
	var out, errBuf bytes.Buffer
	cmd := newGlossaryCmd(&runtimeDeps{version: "test"})
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.Bytes(), err
}

func TestGlossaryCmd_NonLocalRequiresRepoRef(t *testing.T) {
	_, err := runGlossaryCmd(t)
	if err == nil || !strings.Contains(err.Error(), "requires a repository reference") {
		t.Fatalf("expected a requires-repo-ref error, got %v", err)
	}
}

func TestGlossaryCmd_RejectsUnknownFlag(t *testing.T) {
	_, err := runGlossaryCmd(t, "--format", "json", "owner/repo")
	if err == nil || !strings.Contains(err.Error(), "unknown flag") {
		t.Fatalf("expected an unknown-flag error, got %v", err)
	}
}

func TestGlossaryCmd_RejectsTooManyArgs(t *testing.T) {
	if _, err := runGlossaryCmd(t, "owner/repo", "extra/arg"); err == nil {
		t.Fatal("expected an error when two repo refs are given")
	}
}
