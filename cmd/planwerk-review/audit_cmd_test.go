package main

import (
	"bytes"
	"strings"
	"testing"
)

// runAuditCmd executes the audit subcommand hermetically: the flag-validation
// guards return before any git/gh/claude backend call, so the abort cases touch
// no real backend.
func runAuditCmd(t *testing.T, args ...string) ([]byte, error) {
	t.Helper()
	var out, errBuf bytes.Buffer
	cmd := newAuditCmd(&runtimeDeps{version: "test"})
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.Bytes(), err
}

func TestAuditCmd_CaptureWikiWithJSONRequiresYes(t *testing.T) {
	// Under --format json the capture render — including the interactive write
	// confirmation — is discarded so stdout stays valid JSON, so an interactive
	// --capture-wiki run would block on an invisible prompt. The guard rejects the
	// combination at parse time unless --yes (which skips the prompt) is also set.
	_, err := runAuditCmd(t, "--capture-wiki", "--format", "json", testRepoRef)
	if err == nil || !strings.Contains(err.Error(), "--capture-wiki cannot be used with --format json") {
		t.Fatalf("expected the capture-wiki/json guard error, got %v", err)
	}
}
