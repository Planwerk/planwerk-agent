package main

import (
	"bytes"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/planwerk/planwerk-agent/internal/cli"
	"github.com/planwerk/planwerk-agent/internal/fix"
	"github.com/planwerk/planwerk-agent/internal/implement"
	"github.com/planwerk/planwerk-agent/internal/patterns"
)

// runShipCmd executes the ship subcommand hermetically: it exercises only the
// RunE validation and argument wiring. The abort cases return before any
// gh/Claude call, so no real backend is touched.
func runShipCmd(t *testing.T, args ...string) ([]byte, error) {
	t.Helper()
	var out, errBuf bytes.Buffer
	cmd := newShipCmd(&runtimeDeps{version: "test"})
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.Bytes(), err
}

func TestShipCmd_RequiresExactlyOneArg(t *testing.T) {
	if _, err := runShipCmd(t); err == nil {
		t.Fatalf("expected an error when no issue ref is given")
	}
	if _, err := runShipCmd(t, "a/b#1", "a/b#2"); err == nil {
		t.Fatalf("expected an error when two issue refs are given")
	}
}

func TestShipCmd_UnknownMergeMethod(t *testing.T) {
	_, err := runShipCmd(t, "--merge-method", "fast-forward", "acme/widgets#42")
	if err == nil || !strings.Contains(err.Error(), "unknown --merge-method") {
		t.Fatalf("expected an unknown-merge-method error, got %v", err)
	}
}

func TestShipCmd_RejectsNonPositiveInterval(t *testing.T) {
	_, err := runShipCmd(t, "--interval", "0", "acme/widgets#42")
	if err == nil || !strings.Contains(err.Error(), "--interval must be > 0") {
		t.Fatalf("expected an interval error, got %v", err)
	}
}

func TestShipCmd_RejectsNonPositiveMaxFixIterations(t *testing.T) {
	_, err := runShipCmd(t, "--max-fix-iterations", "0", "acme/widgets#42")
	if err == nil || !strings.Contains(err.Error(), "--max-fix-iterations must be > 0") {
		t.Fatalf("expected a max-fix-iterations error, got %v", err)
	}
}

func TestShipCmd_RejectsNegativeStartAt(t *testing.T) {
	_, err := runShipCmd(t, "--start-at", "-3", "acme/widgets#42")
	if err == nil || !strings.Contains(err.Error(), "--start-at") {
		t.Fatalf("expected a start-at error, got %v", err)
	}
}

func TestShipCmd_RegistersWikiAndCaptureFlags(t *testing.T) {
	cmd := newShipCmd(&runtimeDeps{})
	for _, name := range []string{"wiki", "no-wiki", "wiki-ref", "capture-wiki", "no-capture", "yes"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("ship must expose --%s", name)
		}
	}
}

// TestShipCmd_BrainFlagsResolveIntoTheImplementOptions follows --brain from the
// flag set of the ship command to the options of an implement run: the flags
// ship registered resolve to the setting, and shipImplementOptions carries the
// setting into every run. shipFixOptions has no such setting to carry: a fix
// run gets no search.
func TestShipCmd_BrainFlagsResolveIntoTheImplementOptions(t *testing.T) {
	t.Setenv(envBrain, "")
	for _, tc := range []struct {
		name string
		args []string
		want bool
	}{
		{"--brain", []string{"--brain"}, true},
		{"--brain with --no-brain", []string{"--brain", "--no-brain"}, false},
		{"no flag", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			flags := newShipCmd(&runtimeDeps{}).Flags()
			if err := flags.Parse(tc.args); err != nil {
				t.Fatalf("parsing %v: %v", tc.args, err)
			}
			enable, err := flags.GetBool("brain")
			if err != nil {
				t.Fatal(err)
			}
			disable, err := flags.GetBool("no-brain")
			if err != nil {
				t.Fatal(err)
			}
			base := implement.Options{Brain: resolveBrain(enable, disable, flags.Changed("brain"), flags.Changed("no-brain"), cli.BrainFileConfig{})}
			if got := shipImplementOptions(base, &runtimeDeps{}, "acme/widgets#7", "", ""); got.Brain != tc.want {
				t.Errorf("implement options carry Brain = %v, want %v", got.Brain, tc.want)
			}
		})
	}
}

// captureDefaultLog swaps the default logger for one that writes into the
// returned buffer and restores it when the test ends. A test that calls it
// must not run in parallel.
func captureDefaultLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var logBuf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &logBuf
}

// captureWriteWarning is part of the line shipCaptureWrite logs for a
// write-back that is enabled without --yes.
const captureWriteWarning = "cannot confirm a wiki write"

// TestShipWikiAndCapture pins the order the two settings are resolved in: the
// capture gate sees the wiki this call was given, so a run whose wiki is on
// and whose write-back is enabled without --yes gets the start-up warning.
func TestShipWikiAndCapture(t *testing.T) {
	wiki := patterns.WikiOptions{Enabled: true, Repo: "acme/handbook", Ref: "v1"}

	t.Run("enabled without --yes stays propose-only and warns", func(t *testing.T) {
		logBuf := captureDefaultLog(t)
		got := shipWikiAndCapture(implement.Options{NoSimplify: true}, wiki, true)
		if got.Wiki != wiki || got.CaptureWiki || !got.NoSimplify {
			t.Errorf("options = %+v, want the given wiki, no write-back, and the base unchanged otherwise", got)
		}
		if !strings.Contains(logBuf.String(), captureWriteWarning) {
			t.Errorf("no warning for an enabled write-back without --yes; log:\n%s", logBuf.String())
		}
	})

	t.Run("enabled and confirmed pushes", func(t *testing.T) {
		logBuf := captureDefaultLog(t)
		got := shipWikiAndCapture(implement.Options{Yes: true}, wiki, true)
		if got.Wiki != wiki || !got.CaptureWiki {
			t.Errorf("options = %+v, want the given wiki and the write-back on", got)
		}
		if strings.Contains(logBuf.String(), captureWriteWarning) {
			t.Errorf("a confirmed write-back must not warn; log:\n%s", logBuf.String())
		}
	})
}

// TestShipCaptureWrite pins the rule that replaces the confirmation prompt in
// an unattended run: ship pushes capture pages only when the write-back is
// enabled and --yes confirmed it, and says so once when it is enabled without.
// It swaps the default logger, so it must not run in parallel.
func TestShipCaptureWrite(t *testing.T) {
	wikiOn := patterns.WikiOptions{Enabled: true}

	cases := []struct {
		name     string
		opts     implement.Options
		enabled  bool
		want     bool
		wantWarn bool
	}{
		{"enabled and confirmed pushes", implement.Options{Wiki: wikiOn, Yes: true}, true, true, false},
		{"enabled without --yes stays propose-only and warns", implement.Options{Wiki: wikiOn}, true, false, true},
		{"--yes alone enables nothing", implement.Options{Wiki: wikiOn, Yes: true}, false, false, false},
		{"neither enabled nor confirmed", implement.Options{Wiki: wikiOn}, false, false, false},
		{"no warning with the wiki disabled", implement.Options{}, true, false, false},
		{"no warning with --no-capture", implement.Options{Wiki: wikiOn, NoCapture: true}, true, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			logBuf := captureDefaultLog(t)

			if got := shipCaptureWrite(tc.opts, tc.enabled); got != tc.want {
				t.Errorf("shipCaptureWrite = %v, want %v", got, tc.want)
			}
			if warned := strings.Contains(logBuf.String(), captureWriteWarning); warned != tc.wantWarn {
				t.Errorf("warning logged = %v, want %v; log:\n%s", warned, tc.wantWarn, logBuf.String())
			}
		})
	}
}

func TestShipImplementOptions(t *testing.T) {
	base := implement.Options{
		Wiki:        patterns.WikiOptions{Enabled: true, Repo: "acme/handbook", Ref: "v1"},
		CaptureWiki: true,
		NoCapture:   true,
		Yes:         true,
		NoSimplify:  true,
		PatternDirs: []string{"extra"},
		Brain:       true,
	}
	deps := &runtimeDeps{version: "v9", remoteOpts: patterns.RemoteOptions{TTL: time.Hour}}

	got := shipImplementOptions(base, deps, "acme/widgets#7", "opus", "high")

	// The brain setting ship resolved from --brain reaches every implement run
	// it drives.
	if !got.Brain {
		t.Error("a ship run with the brain on must plan every implement run with the search")
	}

	if got.Wiki != base.Wiki || !got.CaptureWiki || !got.NoCapture || !got.Yes {
		t.Errorf("wiki and capture settings = %+v, %v, %v, %v; want them unchanged from the base", got.Wiki, got.CaptureWiki, got.NoCapture, got.Yes)
	}
	if !got.NoSimplify || !reflect.DeepEqual(got.PatternDirs, base.PatternDirs) {
		t.Errorf("the other base options were not carried over: %+v", got)
	}
	if !got.AllowUnelaborated {
		t.Error("a ship-driven implement run must allow an unelaborated Sub Issue")
	}
	if got.IssueRef != "acme/widgets#7" || got.Version != "v9" || got.Remote.TTL != time.Hour {
		t.Errorf("IssueRef, Version, Remote = %q, %q, %+v; want the run's own values", got.IssueRef, got.Version, got.Remote)
	}
	if got.WorkerModel != "opus" || got.WorkerEffort != "high" {
		t.Errorf("worker tier = %q, %q; want opus, high", got.WorkerModel, got.WorkerEffort)
	}
}

// TestShipImplementOptions_ZeroBase covers a ship run without any wiki or
// capture flag: the per-run options still carry the run's own values, and the
// wiki stays off.
func TestShipImplementOptions_ZeroBase(t *testing.T) {
	got := shipImplementOptions(implement.Options{}, &runtimeDeps{}, "acme/widgets#7", "", "")
	if got.Wiki.Enabled || got.CaptureWiki || got.NoCapture || got.Yes || got.Brain {
		t.Errorf("a zero base must leave the wiki, capture, and brain settings off: %+v", got)
	}
	if !got.AllowUnelaborated || got.IssueRef != "acme/widgets#7" {
		t.Errorf("AllowUnelaborated, IssueRef = %v, %q; want true and the given reference", got.AllowUnelaborated, got.IssueRef)
	}
}

func TestShipFixOptions(t *testing.T) {
	base := fix.Options{MaxIterations: 3, PollInterval: time.Minute}
	impl := implement.Options{
		Wiki:            patterns.WikiOptions{Enabled: true, Repo: "acme/handbook", Ref: "v1"},
		PatternDirs:     []string{"extra"},
		NoRepoPatterns:  true,
		NoLocalPatterns: true,
		MaxPatterns:     7,
	}
	deps := &runtimeDeps{version: "v9", remoteOpts: patterns.RemoteOptions{TTL: time.Hour}}

	got := shipFixOptions(base, impl, deps, "acme/widgets#12")

	if got.Wiki != impl.Wiki {
		t.Errorf("Wiki = %+v, want the implement options' %+v", got.Wiki, impl.Wiki)
	}
	if !reflect.DeepEqual(got.PatternDirs, impl.PatternDirs) || !got.NoRepoPatterns || !got.NoLocalPatterns || got.MaxPatterns != 7 {
		t.Errorf("pattern settings = %v, %v, %v, %d; want the implement options'", got.PatternDirs, got.NoRepoPatterns, got.NoLocalPatterns, got.MaxPatterns)
	}
	if got.PRRef != "acme/widgets#12" || got.Version != "v9" || got.Remote.TTL != time.Hour {
		t.Errorf("PRRef, Version, Remote = %q, %q, %+v; want the run's own values", got.PRRef, got.Version, got.Remote)
	}
	if got.MaxIterations != 3 || got.PollInterval != time.Minute {
		t.Errorf("the fix loop settings of the base were not carried over: %+v", got)
	}
}

// TestShipFixOptions_DisabledWiki covers a ship run with the wiki off: the fix
// loop gets the zero WikiOptions and so resolves no wiki.
func TestShipFixOptions_DisabledWiki(t *testing.T) {
	got := shipFixOptions(fix.Options{}, implement.Options{}, &runtimeDeps{}, "acme/widgets#12")
	if got.Wiki != (patterns.WikiOptions{}) {
		t.Errorf("Wiki = %+v, want the zero WikiOptions", got.Wiki)
	}
}
