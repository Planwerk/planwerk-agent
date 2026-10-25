package main

import (
	"bytes"
	"log/slog"
	"testing"
)

// TestRootCmd_RegistersDryRunAndPrintPrompt locks that review registers
// --dry-run and --print-prompt, off by default, as flags of its own rather
// than persistent ones, so fix, address, rebase and implement keep theirs
// (decision 143).
func TestRootCmd_RegistersDryRunAndPrintPrompt(t *testing.T) {
	root := newRootCmd(&runtimeDeps{})
	for _, name := range []string{"dry-run", "print-prompt"} {
		if root.Flags().Lookup(name) == nil {
			t.Errorf("review registers no --%s flag", name)
			continue
		}
		if on, err := root.Flags().GetBool(name); err != nil || on {
			t.Errorf("--%s = %v, %v; want a bool flag off by default", name, on, err)
		}
		if root.PersistentFlags().Lookup(name) != nil {
			t.Errorf("--%s is a persistent flag; it belongs to review alone", name)
		}
	}
}

// TestRootCmd_DryRunAndPrintPromptAreMutuallyExclusive runs the root command
// with both modes. It must fail on the combination before any review starts:
// a review would first fetch the pull request and fail on that instead.
func TestRootCmd_DryRunAndPrintPromptAreMutuallyExclusive(t *testing.T) {
	t.Chdir(t.TempDir())
	// The root command sets the default logger up.
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })

	root := newRootCmd(&runtimeDeps{version: "test"})
	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs([]string{"--dry-run", "--print-prompt", testRepoRef + "#1"})
	err := root.Execute()
	if err == nil || err.Error() != "--dry-run and --print-prompt are mutually exclusive" {
		t.Fatalf("review --dry-run --print-prompt = %v, want the mutually-exclusive error", err)
	}
	if out.Len() != 0 {
		t.Errorf("the refused run wrote %q to stdout, want nothing", out.String())
	}
}
