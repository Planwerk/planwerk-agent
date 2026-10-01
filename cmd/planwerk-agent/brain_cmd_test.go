package main

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/spf13/pflag"
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

// sharedMemoryDoc is the plugin document that tells the skills how to call
// `brain memory`.
const sharedMemoryDoc = "../../plugins/planwerk/shared/memory.md"

// TestSharedMemoryDocMatchesCommand keeps the skills' instructions and the
// command they describe together: the document names the invocation, the page
// form that keeps a file name from being read as a flag or by the shell, the
// header the index opens with, and every flag the command registers. A flag
// added to the command, or a renamed header, otherwise leaves the skills
// following a description of a command that no longer exists.
func TestSharedMemoryDocMatchesCommand(t *testing.T) {
	raw, err := os.ReadFile(sharedMemoryDoc)
	if err != nil {
		t.Fatalf("reading %s: %v", sharedMemoryDoc, err)
	}
	doc := string(raw)

	want := []string{"planwerk-agent brain memory <owner/repo>", "planwerk-agent brain memory <owner/repo> -- '<file name>'", "Project memory from "}
	memoryCmd, _, err := newBrainCmd(&runtimeDeps{}).Find([]string{"memory"})
	if err != nil {
		t.Fatalf("finding the memory command: %v", err)
	}
	flags := 0
	memoryCmd.Flags().VisitAll(func(f *pflag.Flag) {
		flags++
		want = append(want, "--"+f.Name)
	})
	if flags == 0 {
		t.Fatal("the memory command registers no flag; the document describes three")
	}

	for _, w := range want {
		if !strings.Contains(doc, w) {
			t.Errorf("%s does not mention %q", sharedMemoryDoc, w)
		}
	}
}
