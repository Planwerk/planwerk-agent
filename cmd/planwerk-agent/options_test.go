package main

import (
	"bytes"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/planwerk/planwerk-agent/internal/attribution"
	"github.com/planwerk/planwerk-agent/internal/cache"
	"github.com/planwerk/planwerk-agent/internal/claude"
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
