package main

import (
	"bytes"
	"strings"
	"testing"
)

// runBrainCmd executes the brain command hermetically: with the wiki off,
// "brain memory" resolves nothing, so no backend is touched.
func runBrainCmd(t *testing.T, args ...string) ([]byte, error) {
	t.Helper()
	var out, errBuf bytes.Buffer
	cmd := newBrainCmd(&runtimeDeps{version: "test"})
	// The root command silences cobra's usage print, so a failing call writes
	// nothing to stdout; the bare command needs the same setting to match.
	cmd.SilenceUsage = true
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.Bytes(), err
}

func TestBrainMemoryCmd_ArgCount(t *testing.T) {
	if _, err := runBrainCmd(t, "memory"); err == nil {
		t.Error("expected an error when no repository reference is given")
	}
	if _, err := runBrainCmd(t, "memory", testRepoRef, "a.md", "b.md"); err == nil {
		t.Error("expected an error when three arguments are given")
	}
}

func TestBrainMemoryCmd_RegistersWikiFlags(t *testing.T) {
	memoryCmd, _, err := newBrainCmd(&runtimeDeps{}).Find([]string{"memory"})
	if err != nil {
		t.Fatalf("finding the memory command: %v", err)
	}
	for _, name := range []string{"wiki", "no-wiki", "wiki-ref"} {
		if memoryCmd.Flags().Lookup(name) == nil {
			t.Errorf("brain memory must expose --%s", name)
		}
	}
}

func TestBrainMemoryCmd_DisabledWikiPrintsNothing(t *testing.T) {
	// No flag, no config file, and an empty PLANWERK_WIKI: the wiki is off, so
	// the command resolves nothing and never reaches the network.
	t.Setenv(envWiki, "")
	out, err := runBrainCmd(t, "memory", testRepoRef)
	if err != nil {
		t.Fatalf("brain memory returned error: %v", err)
	}
	if len(out) != 0 {
		t.Errorf("stdout = %q, want nothing for a disabled wiki", out)
	}
}

func TestBrainMemoryCmd_UnknownPageOfADisabledWikiFails(t *testing.T) {
	t.Setenv(envWiki, "")
	out, err := runBrainCmd(t, "memory", testRepoRef, "a.md")
	if err == nil {
		t.Fatal("expected an error for a page the memory does not hold")
	}
	for _, want := range []string{"no project memory page named", `"a.md"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to contain %q", err, want)
		}
	}
	if len(out) != 0 {
		t.Errorf("stdout = %q, want nothing beside the error", out)
	}
}
