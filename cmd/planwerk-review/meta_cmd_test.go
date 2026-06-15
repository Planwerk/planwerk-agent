package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/planwerk/planwerk-review/internal/cli"
)

// runMetaCmd executes the meta subcommand hermetically: it exercises only the
// RunE validation and argument wiring. The abort cases return before any
// gh/Claude call, so no real backend is touched.
func runMetaCmd(t *testing.T, args ...string) ([]byte, error) {
	t.Helper()
	var out, errBuf bytes.Buffer
	cmd := newMetaCmd(&runtimeDeps{version: "test"})
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.Bytes(), err
}

func TestMetaCmd_UnknownFormat(t *testing.T) {
	_, err := runMetaCmd(t, "--format", "yaml", "acme/widgets#42")
	if err == nil || !strings.Contains(err.Error(), "unknown format") {
		t.Fatalf("expected an unknown-format error, got %v", err)
	}
}

func TestMetaCmd_RequiresExactlyOneArg(t *testing.T) {
	if _, err := runMetaCmd(t); err == nil {
		t.Fatalf("expected an error when no issue ref is given")
	}
	if _, err := runMetaCmd(t, "a/b#1", "a/b#2"); err == nil {
		t.Fatalf("expected an error when two issue refs are given")
	}
}

func TestMetaConfig_NoCreateMapsToDryRun(t *testing.T) {
	opts := cli.MetaConfig{NoCreate: true}.ToMetaOptions("test")
	if !opts.DryRun {
		t.Fatalf("--no-create should map to DryRun, got %+v", opts)
	}
}
