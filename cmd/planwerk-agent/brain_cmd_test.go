package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/pflag"

	"github.com/planwerk/planwerk-agent/internal/claude"
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

// The compiled-in review tier, pinned by value so a changed default fails here.
const (
	wantReviewModel  = "fable"
	wantReviewEffort = "high"
)

func TestBrainBootstrapCmd_RegistersItsFlags(t *testing.T) {
	bootstrapCmd, _, err := newBrainCmd(&runtimeDeps{}).Find([]string{"bootstrap"})
	if err != nil {
		t.Fatalf("finding the bootstrap command: %v", err)
	}
	for _, name := range []string{"dry-run", "max-units", "write-wiki", "wiki-ref", "review-model", "review-effort", "decision-docs", "no-decision-docs"} {
		if bootstrapCmd.Flags().Lookup(name) == nil {
			t.Errorf("brain bootstrap must expose --%s", name)
		}
	}
	// Running the command is the wiki opt-in, and the push has no flag that
	// skips its confirmation.
	for _, name := range []string{"wiki", "no-wiki", "yes"} {
		if bootstrapCmd.Flags().Lookup(name) != nil {
			t.Errorf("brain bootstrap must not expose --%s", name)
		}
	}
	if got := bootstrapCmd.Flags().Lookup("review-model").DefValue; got != wantReviewModel {
		t.Errorf("--review-model defaults to %q, want %s", got, wantReviewModel)
	}
	if got := bootstrapCmd.Flags().Lookup("review-effort").DefValue; got != wantReviewEffort {
		t.Errorf("--review-effort defaults to %q, want %s", got, wantReviewEffort)
	}
}

func TestBrainBootstrapCmd_ArgCount(t *testing.T) {
	if _, err := runBrainCmd(t, "bootstrap"); err == nil {
		t.Error("expected an error when no repository reference is given")
	}
	if _, err := runBrainCmd(t, "bootstrap", testRepoRef, "other/repo"); err == nil {
		t.Error("expected an error when two arguments are given")
	}
}

// TestBrainBootstrapCmd_RejectsBadFlagsBeforeAnyWork proves each flag error is
// returned before the command reaches GitHub, git, or Claude: every tool the
// run would call is replaced by a script that leaves a mark.
func TestBrainBootstrapCmd_RejectsBadFlagsBeforeAnyWork(t *testing.T) {
	bin := t.TempDir()
	mark := filepath.Join(bin, "called")
	for _, tool := range []string{"gh", "git", "claude"} {
		script := "#!/bin/sh\necho " + tool + " >> " + mark + "\nexit 1\n"
		if err := os.WriteFile(filepath.Join(bin, tool), []byte(script), 0o755); err != nil {
			t.Fatalf("writing fake %s: %v", tool, err)
		}
	}
	t.Setenv("PATH", bin)
	t.Setenv(envBrainReviewEffort, "")
	t.Chdir(t.TempDir())

	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"dry run with write", []string{"--dry-run", "--write-wiki"}, "--dry-run and --write-wiki are mutually exclusive"},
		{"documents with none", []string{"--decision-docs", "a.md", "--no-decision-docs"}, "--decision-docs and --no-decision-docs are mutually exclusive"},
		{"negative unit count", []string{"--max-units", "-1"}, "--max-units must be >= 0, got -1"},
		{"unknown effort", []string{"--review-effort", "huge"}, `invalid --review-effort "huge": must be one of low, medium, high, xhigh, max (env: PLANWERK_BRAIN_REVIEW_EFFORT)`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := runBrainCmd(t, append([]string{"bootstrap", testRepoRef}, tc.args...)...)
			if err == nil || err.Error() != tc.want {
				t.Errorf("err = %v, want %q", err, tc.want)
			}
			if len(out) != 0 {
				t.Errorf("stdout = %q, want nothing beside the error", out)
			}
		})
	}
	if called, err := os.ReadFile(mark); err == nil {
		t.Errorf("a flag error must come before any tool runs; ran: %s", called)
	}
	if _, err := os.Stat(".planwerk-brain-sync"); err == nil {
		t.Error("a flag error must not create the state directory")
	}
}

func TestResolveBrainReviewTier(t *testing.T) {
	t.Run("defaults without a flag or a variable", func(t *testing.T) {
		t.Setenv(envBrainReviewModel, "")
		t.Setenv(envBrainReviewEffort, "")
		m, e, err := resolveBrainReviewTier(wantReviewModel, false, wantReviewEffort, false)
		if err != nil || m != wantReviewModel || e != wantReviewEffort {
			t.Errorf("tier = %q/%q, %v, want %s/%s", m, e, err, wantReviewModel, wantReviewEffort)
		}
	})

	t.Run("the variables replace the defaults", func(t *testing.T) {
		t.Setenv(envBrainReviewModel, " opus ")
		t.Setenv(envBrainReviewEffort, "max")
		m, e, err := resolveBrainReviewTier(wantReviewModel, false, wantReviewEffort, false)
		if err != nil || m != "opus" || e != "max" {
			t.Errorf("tier = %q/%q, %v, want opus/max", m, e, err)
		}
	})

	t.Run("a flag wins over its variable", func(t *testing.T) {
		t.Setenv(envBrainReviewModel, "opus")
		t.Setenv(envBrainReviewEffort, "max")
		const flagModel, flagEffort = "sonnet", "low"
		m, e, err := resolveBrainReviewTier(flagModel, true, flagEffort, true)
		if err != nil || m != flagModel || e != flagEffort {
			t.Fatalf("tier = %q/%q, %v, want %s/%s", m, e, err, flagModel, flagEffort)
		}
		client := claude.NewClient(claude.WithBrainReviewModel(m), claude.WithBrainReviewEffort(e))
		if gotModel, gotEffort := client.BrainReviewTier(); gotModel != flagModel || gotEffort != flagEffort {
			t.Errorf("client tier = %q/%q, want %s/%s", gotModel, gotEffort, flagModel, flagEffort)
		}
	})

	t.Run("an unknown effort from the variable is rejected", func(t *testing.T) {
		t.Setenv(envBrainReviewEffort, "huge")
		_, _, err := resolveBrainReviewTier(wantReviewModel, false, wantReviewEffort, false)
		if err == nil || !strings.Contains(err.Error(), "--review-effort") || !strings.Contains(err.Error(), "PLANWERK_BRAIN_REVIEW_EFFORT") {
			t.Errorf("err = %v, want it to name the flag and the variable", err)
		}
	})
}
