package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/planwerk/planwerk-review/internal/cli"
)

// runExtractCmd executes the extract subcommand hermetically: it exercises only
// the RunE validation and argument wiring. The abort cases return before any
// git/gh/wiki call, so no real backend is touched.
func runExtractCmd(t *testing.T, args ...string) ([]byte, error) {
	t.Helper()
	var out, errBuf bytes.Buffer
	cmd := newExtractCmd(&runtimeDeps{version: "test"})
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.Bytes(), err
}

func TestExtractCmd_ToCatalogAndLocalMutuallyExclusive(t *testing.T) {
	_, err := runExtractCmd(t, "--to-catalog", "--local", testRepoRef)
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("expected a mutually-exclusive error, got %v", err)
	}
}

func TestExtractCmd_AllAndPatternMutuallyExclusive(t *testing.T) {
	_, err := runExtractCmd(t, "--all", "--pattern", "foo", testRepoRef)
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("expected a mutually-exclusive error, got %v", err)
	}
}

func TestExtractCmd_NonLocalRequiresRepoRef(t *testing.T) {
	_, err := runExtractCmd(t)
	if err == nil || !strings.Contains(err.Error(), "requires a repository reference") {
		t.Fatalf("expected a missing-repo-ref error, got %v", err)
	}
}

func TestExtractCmd_ToCatalogRequiresExplicitRepoRef(t *testing.T) {
	_, err := runExtractCmd(t, "--to-catalog")
	if err == nil || !strings.Contains(err.Error(), "--to-catalog requires an explicit repository reference") {
		t.Fatalf("expected a to-catalog repo-ref error, got %v", err)
	}
}

func TestExtractConfig_ToExtractOptionsCarriesSelection(t *testing.T) {
	opts := cli.ExtractConfig{
		RepoRef:   testRepoRef,
		Patterns:  []string{"alpha"},
		ToCatalog: true,
	}.ToExtractOptions("test")
	if opts.RepoRef != testRepoRef || !opts.ToCatalog || len(opts.Patterns) != 1 {
		t.Fatalf("ToExtractOptions dropped fields: %+v", opts)
	}
}
