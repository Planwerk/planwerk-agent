package main

import (
	"bytes"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/planwerk/planwerk-agent/internal/attribution"
	"github.com/planwerk/planwerk-agent/internal/cache"
	"github.com/planwerk/planwerk-agent/internal/claude"
	"github.com/planwerk/planwerk-agent/internal/claudeplugin"
	"github.com/planwerk/planwerk-agent/internal/elaborate"
	"github.com/planwerk/planwerk-agent/internal/prompt"
	"github.com/planwerk/planwerk-agent/internal/review"
)

func TestElaborateUpdateMode(t *testing.T) {
	cases := []struct {
		updateIssue, postComment bool
		want                     elaborate.UpdateMode
	}{
		{false, false, elaborate.UpdateNone},
		{true, false, elaborate.UpdateReplace},
		{false, true, elaborate.UpdateComment},
	}
	for _, c := range cases {
		if got := elaborateUpdateMode(c.updateIssue, c.postComment); got != c.want {
			t.Errorf("elaborateUpdateMode(%v, %v) = %v, want %v", c.updateIssue, c.postComment, got, c.want)
		}
	}
}

func TestPromptMode(t *testing.T) {
	cases := map[string]prompt.Mode{"auto": prompt.ModeAuto, "fix": prompt.ModeFix, "implement": prompt.ModeImplement}
	for in, want := range cases {
		if got := promptMode(in); got != want {
			t.Errorf("promptMode(%q) = %v, want %v", in, got, want)
		}
	}
}

// TestShipCarriesNoSpecialistsSwitch documents that ship has no
// --no-specialists control: a ship-driven implement run always inherits the
// default-on first-round fan-out, and --no-review remains ship's whole-pass
// switch.
func TestShipCarriesNoSpecialistsSwitch(t *testing.T) {
	cmd := newShipCmd(&runtimeDeps{})
	if cmd.Flags().Lookup("no-specialists") != nil {
		t.Error("ship must not expose --no-specialists")
	}
	if cmd.Flags().Lookup("no-review") == nil {
		t.Error("ship must expose --no-review, its whole-pass switch")
	}
}

// TestImplementAndShipRegisterNoPlanReview pins the plan review's off switch on
// both commands that plan: default false, so the review runs unless asked not to.
func TestImplementAndShipRegisterNoPlanReview(t *testing.T) {
	for name, cmd := range map[string]*cobra.Command{
		"implement": newImplementCmd(&runtimeDeps{}),
		"ship":      newShipCmd(&runtimeDeps{}),
	} {
		off, err := cmd.Flags().GetBool("no-plan-review")
		if err != nil {
			t.Errorf("%s must expose the boolean --no-plan-review: %v", name, err)
			continue
		}
		if off {
			t.Errorf("%s --no-plan-review defaults to true, want false", name)
		}
	}
}

// TestCommandsRegisterTheWikiFlags pins the wiki opt-in on the commands that
// read the project memory beside review, audit, propose, and implement, and
// its absence on a command that reads none.
func TestCommandsRegisterTheWikiFlags(t *testing.T) {
	deps := &runtimeDeps{}
	readers := map[string]*cobra.Command{
		"elaborate": newElaborateCmd(deps),
		"fix":       newFixCmd(deps),
		"address":   newAddressCmd(deps),
	}
	for name, cmd := range readers {
		for _, flag := range []string{"wiki", "no-wiki", "wiki-ref"} {
			if cmd.Flags().Lookup(flag) == nil {
				t.Errorf("%s must expose --%s", name, flag)
			}
		}
	}
	if newRebaseCmd(deps).Flags().Lookup("wiki") != nil {
		t.Error("rebase reads no project memory and must not expose --wiki")
	}
}

// TestCommandsRegisterTheBrainFlags pins the search opt-in on the commands
// whose read-only sessions plan and judge, and its absence on the commands
// whose sessions edit, commit, and push.
func TestCommandsRegisterTheBrainFlags(t *testing.T) {
	deps := &runtimeDeps{}
	searchers := map[string]*cobra.Command{
		"review":    newRootCmd(deps),
		"audit":     newAuditCmd(deps),
		"propose":   newProposeCmd(deps),
		"implement": newImplementCmd(deps),
		"elaborate": newElaborateCmd(deps),
		"ship":      newShipCmd(deps),
	}
	for name, cmd := range searchers {
		for _, flag := range []string{"brain", "no-brain"} {
			// The flags are the command's own, not persistent ones a
			// subcommand would inherit.
			if cmd.Flags().Lookup(flag) == nil || cmd.PersistentFlags().Lookup(flag) != nil {
				t.Errorf("%s must expose --%s as a flag of its own", name, flag)
			}
		}
	}
	others := map[string]*cobra.Command{
		"fix":     newFixCmd(deps),
		"address": newAddressCmd(deps),
		"rebase":  newRebaseCmd(deps),
	}
	for name, cmd := range others {
		for _, flag := range []string{"brain", "no-brain"} {
			if cmd.Flags().Lookup(flag) != nil || cmd.InheritedFlags().Lookup(flag) != nil {
				t.Errorf("%s must not expose --%s: its session gets no search", name, flag)
			}
		}
	}
}

// TestRootRegistersTheDiffExclusionsFlag pins --diff-exclusions as a
// persistent flag that is off by default and names its environment variable.
func TestRootRegistersTheDiffExclusionsFlag(t *testing.T) {
	flag := newRootCmd(&runtimeDeps{}).PersistentFlags().Lookup("diff-exclusions")
	if flag == nil {
		t.Fatal("the root command must expose --diff-exclusions as a persistent flag")
	}
	if on, err := strconv.ParseBool(flag.DefValue); err != nil || on {
		t.Errorf("--diff-exclusions defaults to %q, want off", flag.DefValue)
	}
	if !strings.Contains(flag.Usage, "env: "+envDiffExclusions) {
		t.Errorf("--diff-exclusions usage %q does not name %s", flag.Usage, envDiffExclusions)
	}
}

// TestRoot_DiffExclusionsReachTheClientAndTheCacheKey runs the root command's
// pre-run through --version, which touches no backend, and checks that the
// flag and PLANWERK_DIFF_EXCLUSIONS turn the exclusions on in the shared
// Client and scope the cache key, while a run without them keys as before.
func TestRoot_DiffExclusionsReachTheClientAndTheCacheKey(t *testing.T) {
	// Not t.Parallel(): the pre-run sets the default logger, the attribution
	// version and the cache fingerprint, which the cleanups restore.
	prevLogger, prevFingerprint := slog.Default(), cache.RunFingerprint()
	t.Cleanup(func() {
		slog.SetDefault(prevLogger)
		cache.SetRunFingerprint(prevFingerprint)
		attribution.SetVersion("")
	})

	for _, tc := range []struct {
		name string
		args []string
		env  string
		want bool
	}{
		{"off by default", nil, "", false},
		{"the flag", []string{"--diff-exclusions"}, "", true},
		{"the environment", nil, "yes", true},
		{"the flag wins over the environment", []string{"--diff-exclusions=false"}, "1", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(envDiffExclusions, tc.env)
			t.Setenv(envFinderTools, "")
			deps := &runtimeDeps{version: "test"}
			cmd := newRootCmd(deps)
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetArgs(append(tc.args, "--version"))
			if err := cmd.Execute(); err != nil {
				t.Fatalf("Execute: %v", err)
			}
			if got := deps.claude.DiffExclusions(); got != tc.want {
				t.Errorf("Client.DiffExclusions() = %v, want %v", got, tc.want)
			}
			if got := strings.HasSuffix(cache.RunFingerprint(), "|diff-exclusions"); got != tc.want {
				t.Errorf("cache fingerprint %q carries diff-exclusions = %v, want %v", cache.RunFingerprint(), got, tc.want)
			}
		})
	}
}

// TestRootRegistersTheKeepTranscriptsFlag pins --keep-transcripts as a
// persistent flag that is off by default and names its environment variable
// (decision 161).
func TestRootRegistersTheKeepTranscriptsFlag(t *testing.T) {
	flag := newRootCmd(&runtimeDeps{}).PersistentFlags().Lookup("keep-transcripts")
	if flag == nil {
		t.Fatal("the root command must expose --keep-transcripts as a persistent flag")
	}
	if on, err := strconv.ParseBool(flag.DefValue); err != nil || on {
		t.Errorf("--keep-transcripts defaults to %q, want off", flag.DefValue)
	}
	if !strings.Contains(flag.Usage, "env: "+envKeepTranscripts) {
		t.Errorf("--keep-transcripts usage %q does not name %s", flag.Usage, envKeepTranscripts)
	}
}

// TestRoot_KeepTranscriptsReachTheClient runs the root command's pre-run
// through --version, which touches no backend, and checks that the flag and
// PLANWERK_KEEP_TRANSCRIPTS reach the shared Client, the flag winning when
// set, while the cache key stays the same: what a session saves to disk
// changes nothing it returns.
func TestRoot_KeepTranscriptsReachTheClient(t *testing.T) {
	// Not t.Parallel(): the pre-run sets the default logger, the attribution
	// version and the cache fingerprint, which the cleanups restore.
	prevLogger, prevFingerprint := slog.Default(), cache.RunFingerprint()
	t.Cleanup(func() {
		slog.SetDefault(prevLogger)
		cache.SetRunFingerprint(prevFingerprint)
		attribution.SetVersion("")
	})

	var fingerprints []string
	for _, tc := range []struct {
		name string
		args []string
		env  string
		want bool
	}{
		{"off by default", nil, "", false},
		{"the flag", []string{"--keep-transcripts"}, "", true},
		{"the environment", nil, "yes", true},
		{"the flag wins over the environment", []string{"--keep-transcripts=false"}, "1", false},
		{"an unrecognized value is off", nil, "maybe", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(envKeepTranscripts, tc.env)
			t.Setenv(envDiffExclusions, "")
			t.Setenv(envFinderTools, "")
			deps := &runtimeDeps{version: "test"}
			cmd := newRootCmd(deps)
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetArgs(append(tc.args, "--version"))
			if err := cmd.Execute(); err != nil {
				t.Fatalf("Execute: %v", err)
			}
			if got := deps.claude.KeepTranscripts(); got != tc.want {
				t.Errorf("Client.KeepTranscripts() = %v, want %v", got, tc.want)
			}
			fingerprints = append(fingerprints, cache.RunFingerprint())
		})
	}
	for i, fp := range fingerprints {
		if fp != fingerprints[0] {
			t.Errorf("row %d keyed the cache %q, want %q: the flag must not change the key", i, fp, fingerprints[0])
		}
	}
}

// TestReviewRegistersScopePatterns pins --scope-patterns as a flag of the
// review command alone: off by default, naming its environment variable, and
// neither persistent nor exposed by another command (decision 141).
func TestReviewRegistersScopePatterns(t *testing.T) {
	deps := &runtimeDeps{}
	root := newRootCmd(deps)
	flag := root.Flags().Lookup("scope-patterns")
	if flag == nil {
		t.Fatal("the review command must expose --scope-patterns")
	}
	if on, err := strconv.ParseBool(flag.DefValue); err != nil || on {
		t.Errorf("--scope-patterns defaults to %q, want off", flag.DefValue)
	}
	if !strings.Contains(flag.Usage, "env: "+envScopePatterns) || envScopePatterns != "PLANWERK_SCOPE_PATTERNS" {
		t.Errorf("--scope-patterns usage %q does not name PLANWERK_SCOPE_PATTERNS", flag.Usage)
	}
	if root.PersistentFlags().Lookup("scope-patterns") != nil {
		t.Error("--scope-patterns must not be persistent: no subcommand runs the review prompt")
	}
	audit := newAuditCmd(deps)
	root.AddCommand(audit)
	if audit.Flags().Lookup("scope-patterns") != nil || audit.InheritedFlags().Lookup("scope-patterns") != nil {
		t.Error("audit must not expose --scope-patterns")
	}
}

// TestRoot_ScopePatternsReachTheReviewOptions runs the review command on a pull
// request with a review that only records its options, and checks that the
// flag and PLANWERK_SCOPE_PATTERNS turn --scope-patterns on in them, and that
// an explicit flag wins over the environment. A --version run would not do:
// it returns before the review options are resolved.
func TestRoot_ScopePatternsReachTheReviewOptions(t *testing.T) {
	// Not t.Parallel(): the pre-run sets the default logger, the attribution
	// version and the cache fingerprint, which the cleanups restore, and the
	// test changes the working directory so no config file is read.
	t.Chdir(t.TempDir())
	prevLogger, prevFingerprint := slog.Default(), cache.RunFingerprint()
	t.Cleanup(func() {
		slog.SetDefault(prevLogger)
		cache.SetRunFingerprint(prevFingerprint)
		attribution.SetVersion("")
	})

	for _, tc := range []struct {
		name string
		args []string
		env  string
		want bool
	}{
		{"off by default", nil, "", false},
		{"the flag", []string{"--scope-patterns"}, "", true},
		{"the environment", nil, "1", true},
		{"the flag wins over the environment", []string{"--scope-patterns=false"}, "1", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(envScopePatterns, tc.env)
			var got []review.Options
			deps := &runtimeDeps{version: "test", runReview: func(_ io.Writer, opts review.Options, _ *claude.Client) error {
				got = append(got, opts)
				return nil
			}}
			cmd := newRootCmd(deps)
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetArgs(append(tc.args, testRepoRef+"#1"))
			if err := cmd.Execute(); err != nil {
				t.Fatalf("Execute: %v", err)
			}
			if len(got) != 1 {
				t.Fatalf("the review ran %d times, want once", len(got))
			}
			if got[0].ScopePatterns != tc.want {
				t.Errorf("review options carry ScopePatterns = %v, want %v", got[0].ScopePatterns, tc.want)
			}
		})
	}
}

// TestReviewRegistersPartitionFiles pins --partition-files as a flag of the
// review command alone: 0, one session, by default, and neither persistent nor
// exposed by another command (decision 142).
func TestReviewRegistersPartitionFiles(t *testing.T) {
	deps := &runtimeDeps{}
	root := newRootCmd(deps)
	flag := root.Flags().Lookup("partition-files")
	if flag == nil {
		t.Fatal("the review command must expose --partition-files")
	}
	if flag.DefValue != "0" {
		t.Errorf("--partition-files defaults to %q, want 0", flag.DefValue)
	}
	if root.PersistentFlags().Lookup("partition-files") != nil {
		t.Error("--partition-files must not be persistent: no subcommand partitions a review")
	}
	audit := newAuditCmd(deps)
	root.AddCommand(audit)
	if audit.Flags().Lookup("partition-files") != nil || audit.InheritedFlags().Lookup("partition-files") != nil {
		t.Error("audit must not expose --partition-files")
	}
}

func TestRootRegistersTheClaudePluginFlag(t *testing.T) {
	flag := newRootCmd(&runtimeDeps{}).PersistentFlags().Lookup("claude-plugin")
	if flag == nil {
		t.Fatal("the root command must expose --claude-plugin as a persistent flag")
	}
	if flag.Value.Type() != "stringArray" {
		t.Errorf("--claude-plugin is a %s, want a repeatable stringArray that never splits a value on commas", flag.Value.Type())
	}
	if !strings.Contains(flag.Usage, "env: "+envClaudePlugins) {
		t.Errorf("--claude-plugin usage %q does not name %s", flag.Usage, envClaudePlugins)
	}
}

// writePlugin writes a plugin root named name at version into a new
// directory and returns it.
func writePlugin(t *testing.T, name, version string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".claude-plugin"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name": "` + name + `", "version": "` + version + `"}`
	if err := os.WriteFile(filepath.Join(dir, ".claude-plugin", "plugin.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestRoot_ClaudePluginsReachTheClientAndTheCacheKey runs the root command's
// pre-run through a review whose run is stubbed and checks that --claude-plugin and
// PLANWERK_CLAUDE_PLUGINS resolve installed ids and directories into the
// shared Client and scope the cache key by name and version, that the flag
// wins over the environment, and that a value naming no plugin fails the run
// (decision 153).
func TestRoot_ClaudePluginsReachTheClientAndTheCacheKey(t *testing.T) {
	// Not t.Parallel(): the pre-run sets the default logger, the attribution
	// version and the cache fingerprint, which the cleanups restore.
	prevLogger, prevFingerprint := slog.Default(), cache.RunFingerprint()
	t.Cleanup(func() {
		slog.SetDefault(prevLogger)
		cache.SetRunFingerprint(prevFingerprint)
		attribution.SetVersion("")
	})
	design := writePlugin(t, "impeccable", "4.7.1")
	local := writePlugin(t, "local", "")
	list := func() ([]claudeplugin.Installed, error) {
		return []claudeplugin.Installed{{ID: "impeccable@impeccable", Version: "4.7.1", InstallPath: design}}, nil
	}

	for _, tc := range []struct {
		name    string
		args    []string
		env     string
		want    []string // plugin labels, in order
		wantErr string
	}{
		{name: "none by default"},
		{name: "an installed id and a directory", args: []string{"--claude-plugin", "impeccable@impeccable", "--claude-plugin", local}, want: []string{"impeccable@4.7.1", "local"}},
		{name: "the environment", env: "impeccable@impeccable," + local, want: []string{"impeccable@4.7.1", "local"}},
		{name: "the flag wins over the environment", args: []string{"--claude-plugin", local}, env: "impeccable@impeccable", want: []string{"local"}},
		{name: "an id that is not installed", args: []string{"--claude-plugin", "nope@market"}, wantErr: "not installed; installed: impeccable@impeccable"},
		{name: "a bad environment value names the variable", env: "nope", wantErr: "(env: " + envClaudePlugins + ")"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(envClaudePlugins, tc.env)
			t.Setenv(envFinderTools, "")
			t.Setenv(envDiffExclusions, "")
			deps := &runtimeDeps{version: "test", listPlugins: list, runReview: func(io.Writer, review.Options, *claude.Client) error { return nil }}
			cmd := newRootCmd(deps)
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetArgs(append(tc.args, testRepoRef+"#1"))
			err := cmd.Execute()
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("Execute error = %v, want one containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Execute: %v", err)
			}
			var got []string
			for _, p := range deps.claude.Plugins() {
				got = append(got, p.Label())
			}
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Errorf("Client.Plugins() = %v, want %v", got, tc.want)
			}
			wantSuffix := ""
			if len(tc.want) > 0 {
				wantSuffix = "|plugins=" + strings.Join(tc.want, ",")
			}
			if fp := cache.RunFingerprint(); (wantSuffix == "" && strings.Contains(fp, "plugins=")) || !strings.HasSuffix(fp, wantSuffix) {
				t.Errorf("cache fingerprint %q, want it to end in %q", fp, wantSuffix)
			}
		})
	}
}

// TestRoot_CommandsWithoutASessionResolveNoPlugin: a command that starts no
// session, and the root's version and cache actions, never list the installed
// plugins, so PLANWERK_CLAUDE_PLUGINS cannot make them depend on the claude
// CLI or fail them (decision 153).
func TestRoot_CommandsWithoutASessionResolveNoPlugin(t *testing.T) {
	// Not t.Parallel(): the pre-run sets the default logger, the attribution
	// version and the cache fingerprint, which the cleanups restore.
	prevLogger, prevFingerprint := slog.Default(), cache.RunFingerprint()
	t.Cleanup(func() {
		slog.SetDefault(prevLogger)
		cache.SetRunFingerprint(prevFingerprint)
		attribution.SetVersion("")
	})
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)
	t.Setenv(envClaudePlugins, "impeccable@impeccable")
	t.Setenv(envFinderTools, "")
	t.Setenv(envDiffExclusions, "")

	for _, args := range [][]string{
		{"--version"},
		{"--cache-stats"},
		{"schema", "review"},
		{"cache", "stats"},
		{"help"},
		{"completion", "bash"},
		{"__complete", "sch"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			deps := &runtimeDeps{version: "test", listPlugins: func() ([]claudeplugin.Installed, error) {
				t.Errorf("%v listed the installed plugins", args)
				return nil, nil
			}}
			cmd := newRootCmd(deps)
			cmd.AddCommand(newSchemaCmd(deps), newCacheCmd(deps))
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetArgs(args)
			if err := cmd.Execute(); err != nil {
				t.Fatalf("Execute(%v): %v", args, err)
			}
		})
	}
}

// TestStartsNoSession_MarksTheCommandsThatRunNoSession pins which commands
// carry the mark that keeps the pre-run from resolving a plugin: the ones that
// only read GitHub, the mirror, or the cache, or print a prompt for another
// session, and cobra's own. The commands that start a session are not marked;
// neither is brain search, which the pre-run leaves before resolving anything
// because it is mirror-only.
func TestStartsNoSession_MarksTheCommandsThatRunNoSession(t *testing.T) {
	deps := &runtimeDeps{version: "test"}
	root := newRootCmd(deps)
	root.AddCommand(newSchemaCmd(deps), newCacheCmd(deps), newPromptCmd(deps), newBrainCmd(deps), newImplementCmd(deps), newExtractCmd(deps))
	root.InitDefaultHelpCmd()
	root.InitDefaultCompletionCmd()
	for path, want := range map[string]bool{
		"schema":          true,
		"cache stats":     true,
		"prompt":          true,
		"brain memory":    true,
		"brain sync":      true,
		"extract":         true,
		"help":            true,
		"completion bash": true,
		"brain bootstrap": false,
		"brain search":    false,
		"implement":       false,
		"":                false,
	} {
		cmd, _, err := root.Find(strings.Fields(path))
		if err != nil {
			t.Fatalf("Find(%q): %v", path, err)
		}
		if got := startsNoSession(cmd); got != want {
			t.Errorf("startsNoSession(%q) = %v, want %v", path, got, want)
		}
	}
}
