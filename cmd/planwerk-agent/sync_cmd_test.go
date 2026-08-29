package main

import (
	"bytes"
	"strings"
	"testing"
)

// runSyncCmd executes the sync subcommand hermetically: it exercises only the
// RunE validation and argument wiring. The abort cases return before any
// git/gh/wiki call, so no real backend is touched.
func runSyncCmd(t *testing.T, args ...string) ([]byte, error) {
	t.Helper()
	var out, errBuf bytes.Buffer
	cmd := newSyncCmd(&runtimeDeps{version: "test"})
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.Bytes(), err
}

func TestSyncCmd_DryRunAndPruneMutuallyExclusive(t *testing.T) {
	_, err := runSyncCmd(t, "--dry-run", "--prune", testRepoRef)
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("expected a mutually-exclusive error, got %v", err)
	}
}

func TestSyncCmd_DryRunAndApplyMutuallyExclusive(t *testing.T) {
	_, err := runSyncCmd(t, "--dry-run", "--apply", testRepoRef)
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("expected a mutually-exclusive error for --apply, got %v", err)
	}
}

func TestSyncCmd_RequiresRepoRef(t *testing.T) {
	_, err := runSyncCmd(t)
	if err == nil {
		t.Fatal("expected an error when no repository reference is given")
	}
}

func TestSyncCmd_RejectsUnknownFormat(t *testing.T) {
	_, err := runSyncCmd(t, "--format", "yaml", testRepoRef)
	if err == nil || !strings.Contains(err.Error(), "unknown format") {
		t.Fatalf("expected an unknown-format error, got %v", err)
	}
}
