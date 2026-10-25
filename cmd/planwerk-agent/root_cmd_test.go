package main

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

// runRootCmd executes the root (review) command hermetically: the
// flag-validation guards return before review.Run, so a refused combination
// touches no git, gh or claude backend. It runs in an empty directory, so no
// .planwerk/config.yaml is read.
func runRootCmd(t *testing.T, args ...string) error {
	t.Helper()
	t.Chdir(t.TempDir())
	// The root command sets the default logger up.
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })

	var out, errBuf bytes.Buffer
	cmd := newRootCmd(&runtimeDeps{version: "test"})
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	cmd.SetArgs(args)
	return cmd.Execute()
}

func TestRootCmd_ResolveOutdatedRequiresInline(t *testing.T) {
	// --resolve-outdated acts on the threads an inline review opened, so it
	// is refused at parse time without --inline, --post-review alone included.
	for _, args := range [][]string{
		{"--resolve-outdated", testRepoRef + "#1"},
		{"--resolve-outdated", "--post-review", testRepoRef + "#1"},
	} {
		err := runRootCmd(t, args...)
		if err == nil || !strings.Contains(err.Error(), "--resolve-outdated requires --inline") {
			t.Errorf("%q: expected the resolve-outdated guard error, got %v", args, err)
		}
	}
}
