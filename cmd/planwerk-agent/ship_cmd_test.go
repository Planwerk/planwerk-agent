package main

import (
	"bytes"
	"strings"
	"testing"
)

// runShipCmd executes the ship subcommand hermetically: it exercises only the
// RunE validation and argument wiring. The abort cases return before any
// gh/Claude call, so no real backend is touched.
func runShipCmd(t *testing.T, args ...string) ([]byte, error) {
	t.Helper()
	var out, errBuf bytes.Buffer
	cmd := newShipCmd(&runtimeDeps{version: "test"})
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.Bytes(), err
}

func TestShipCmd_RequiresExactlyOneArg(t *testing.T) {
	if _, err := runShipCmd(t); err == nil {
		t.Fatalf("expected an error when no issue ref is given")
	}
	if _, err := runShipCmd(t, "a/b#1", "a/b#2"); err == nil {
		t.Fatalf("expected an error when two issue refs are given")
	}
}

func TestShipCmd_UnknownMergeMethod(t *testing.T) {
	_, err := runShipCmd(t, "--merge-method", "fast-forward", "acme/widgets#42")
	if err == nil || !strings.Contains(err.Error(), "unknown --merge-method") {
		t.Fatalf("expected an unknown-merge-method error, got %v", err)
	}
}

func TestShipCmd_RejectsNonPositiveInterval(t *testing.T) {
	_, err := runShipCmd(t, "--interval", "0", "acme/widgets#42")
	if err == nil || !strings.Contains(err.Error(), "--interval must be > 0") {
		t.Fatalf("expected an interval error, got %v", err)
	}
}

func TestShipCmd_RejectsNonPositiveMaxFixIterations(t *testing.T) {
	_, err := runShipCmd(t, "--max-fix-iterations", "0", "acme/widgets#42")
	if err == nil || !strings.Contains(err.Error(), "--max-fix-iterations must be > 0") {
		t.Fatalf("expected a max-fix-iterations error, got %v", err)
	}
}

func TestShipCmd_RejectsNegativeStartAt(t *testing.T) {
	_, err := runShipCmd(t, "--start-at", "-3", "acme/widgets#42")
	if err == nil || !strings.Contains(err.Error(), "--start-at") {
		t.Fatalf("expected a start-at error, got %v", err)
	}
}
