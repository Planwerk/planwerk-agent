package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/planwerk/planwerk-agent/internal/cache"
	"github.com/planwerk/planwerk-agent/internal/claude"
	"github.com/planwerk/planwerk-agent/internal/patterns"
)

// testRepoRef is the canonical repository reference shared across the
// command-level tests in this package.
const testRepoRef = "acme/widgets"

// Repeated effort and model literals used across the resolve* tests,
// extracted into constants so they live in one place (and to satisfy
// goconst, which flags strings that recur three or more times).
const (
	effortHigh = "high"
	modelOpus  = "opus"
)

func TestResolveBuildInfoUsesLdflagsVersion(t *testing.T) {
	bi := resolveBuildInfo("v1.2.3")
	if bi.Version != "v1.2.3" {
		t.Fatalf("Version = %q, want v1.2.3", bi.Version)
	}
	if bi.IsDev {
		t.Fatalf("IsDev = true, want false for tagged version")
	}
}

func TestResolveBuildInfoFallsBackWhenLdflagsDev(t *testing.T) {
	bi := resolveBuildInfo(devVersion)
	// When tests run under `go test`, debug.ReadBuildInfo is available but
	// Main.Version is "(devel)" which is filtered out, so Version remains
	// "dev". In binaries installed via `go install <pkg>@v1.2.3`, the
	// fallback promotes Main.Version to the resolved version.
	if bi.Version == "" {
		t.Fatalf("Version must not be empty after fallback")
	}
	if bi.GoVersion == "" {
		t.Fatalf("GoVersion must be populated from debug.ReadBuildInfo")
	}
}

func TestWriteVersionDefault(t *testing.T) {
	var buf bytes.Buffer
	writeVersion(&buf, buildInfo{Version: "v1.2.3"}, false)
	out := buf.String()
	if !strings.Contains(out, "planwerk-agent version v1.2.3") {
		t.Fatalf("missing version line: %q", out)
	}
	if strings.Contains(out, "commit:") || strings.Contains(out, "built:") || strings.Contains(out, "go:") {
		t.Fatalf("non-verbose output must not include build metadata: %q", out)
	}
	if strings.Contains(out, "warning:") {
		t.Fatalf("non-dev build must not warn: %q", out)
	}
}

func TestWriteVersionVerbose(t *testing.T) {
	var buf bytes.Buffer
	writeVersion(&buf, buildInfo{
		Version:   "v1.2.3",
		Commit:    "abc123",
		BuildDate: "2026-04-17T11:07:47Z",
		GoVersion: "go1.26.1",
	}, true)
	out := buf.String()
	for _, want := range []string{
		"planwerk-agent version v1.2.3",
		"commit: abc123",
		"built: 2026-04-17T11:07:47Z",
		"go: go1.26.1",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("verbose output missing %q:\n%s", want, out)
		}
	}
}

func TestWriteVersionDevWarning(t *testing.T) {
	var buf bytes.Buffer
	writeVersion(&buf, buildInfo{Version: devVersion, IsDev: true}, false)
	if !strings.Contains(buf.String(), "warning:") {
		t.Fatalf("dev build must emit warning: %q", buf.String())
	}
}

func intPtr(i int) *int { return &i }

func TestResolveMaxPatternsFlagWins(t *testing.T) {
	t.Setenv(envMaxPatterns, "99")
	got, err := resolveMaxPatterns(7, true, intPtr(42))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 7 {
		t.Fatalf("got %d, want 7 (flag value)", got)
	}
}

func TestResolveMaxPatternsFileBeatsEnv(t *testing.T) {
	t.Setenv(envMaxPatterns, "99")
	got, err := resolveMaxPatterns(0, false, intPtr(42))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 42 {
		t.Fatalf("got %d, want 42 (file value)", got)
	}
}

func TestResolveMaxPatternsEnvBeatsDefault(t *testing.T) {
	t.Setenv(envMaxPatterns, "17")
	got, err := resolveMaxPatterns(0, false, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 17 {
		t.Fatalf("got %d, want 17 (env value)", got)
	}
}

func TestResolveMaxPatternsDefault(t *testing.T) {
	t.Setenv(envMaxPatterns, "")
	got, err := resolveMaxPatterns(0, false, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != patterns.DefaultMaxPatternsInPrompt {
		t.Fatalf("got %d, want default %d", got, patterns.DefaultMaxPatternsInPrompt)
	}
	if got > 0 {
		t.Fatalf("default must disable truncation (<=0), got %d", got)
	}
}

func TestResolveMaxPatternsInvalidEnv(t *testing.T) {
	t.Setenv(envMaxPatterns, "not-a-number")
	_, err := resolveMaxPatterns(0, false, nil)
	if err == nil {
		t.Fatalf("expected error for invalid env, got nil")
	}
}

func TestResolveShowClaudeOutputFlagWins(t *testing.T) {
	t.Setenv(envShowClaudeOutput, "1")
	if resolveShowClaudeOutput(false, true) != false {
		t.Fatalf("explicit --show-claude-output=false must beat env var")
	}
	if resolveShowClaudeOutput(true, true) != true {
		t.Fatalf("explicit --show-claude-output=true must take effect")
	}
}

func TestResolveShowClaudeOutputEnvVariants(t *testing.T) {
	for _, raw := range []string{"1", "true", "TRUE", "yes", "On", " 1 "} {
		t.Run("enabled-"+raw, func(t *testing.T) {
			t.Setenv(envShowClaudeOutput, raw)
			if !resolveShowClaudeOutput(false, false) {
				t.Errorf("env=%q should enable streaming", raw)
			}
		})
	}
	for _, raw := range []string{"0", "false", "no", "off", "", "garbage"} {
		t.Run("disabled-"+raw, func(t *testing.T) {
			t.Setenv(envShowClaudeOutput, raw)
			if resolveShowClaudeOutput(false, false) {
				t.Errorf("env=%q should leave streaming off", raw)
			}
		})
	}
}

func TestResolveClaudeInheritUserConfigFlagWins(t *testing.T) {
	t.Setenv(envClaudeInheritUserConfig, "1")
	if resolveClaudeInheritUserConfig(false, true) != false {
		t.Fatalf("explicit --claude-inherit-user-config=false must beat env var")
	}
	if resolveClaudeInheritUserConfig(true, true) != true {
		t.Fatalf("explicit --claude-inherit-user-config=true must take effect")
	}
}

func TestResolveClaudeInheritUserConfigEnvVariants(t *testing.T) {
	for _, raw := range []string{"1", "true", "TRUE", "yes", "On", " 1 "} {
		t.Run("enabled-"+raw, func(t *testing.T) {
			t.Setenv(envClaudeInheritUserConfig, raw)
			if !resolveClaudeInheritUserConfig(false, false) {
				t.Errorf("env=%q should enable inheritance", raw)
			}
		})
	}
	for _, raw := range []string{"0", "false", "no", "off", "", "garbage"} {
		t.Run("disabled-"+raw, func(t *testing.T) {
			t.Setenv(envClaudeInheritUserConfig, raw)
			if resolveClaudeInheritUserConfig(false, false) {
				t.Errorf("env=%q should leave the session hermetic", raw)
			}
		})
	}
}

func TestResolveClaudeTimeoutFlagWins(t *testing.T) {
	t.Setenv(envClaudeTimeout, "30m")
	got, err := resolveClaudeTimeout(20*time.Minute, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 20*time.Minute {
		t.Fatalf("got %s, want 20m0s (flag value)", got)
	}
}

func TestResolveClaudeTimeoutEnvBeatsDefault(t *testing.T) {
	t.Setenv(envClaudeTimeout, "45m")
	got, err := resolveClaudeTimeout(0, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 45*time.Minute {
		t.Fatalf("got %s, want 45m0s (env value)", got)
	}
}

func TestResolveClaudeTimeoutDefault(t *testing.T) {
	t.Setenv(envClaudeTimeout, "")
	got, err := resolveClaudeTimeout(0, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != claude.DefaultClaudeTimeout {
		t.Fatalf("got %s, want default %s", got, claude.DefaultClaudeTimeout)
	}
}

func TestResolveClaudeTimeoutInvalidEnv(t *testing.T) {
	t.Setenv(envClaudeTimeout, "not-a-duration")
	if _, err := resolveClaudeTimeout(0, false); err == nil {
		t.Fatalf("expected error for invalid env, got nil")
	}
}

func TestResolveClaudeTimeoutRejectsNonPositive(t *testing.T) {
	t.Setenv(envClaudeTimeout, "")
	if _, err := resolveClaudeTimeout(0, true); err == nil {
		t.Fatalf("expected error for --claude-timeout=0, got nil")
	}
	if _, err := resolveClaudeTimeout(-1*time.Minute, true); err == nil {
		t.Fatalf("expected error for negative --claude-timeout, got nil")
	}

	t.Setenv(envClaudeTimeout, "0s")
	if _, err := resolveClaudeTimeout(0, false); err == nil {
		t.Fatalf("expected error for PLANWERK_CLAUDE_TIMEOUT=0s, got nil")
	}
}

func TestResolveClaudeModelFlagWins(t *testing.T) {
	t.Setenv(envClaudeModel, "sonnet")
	if got := resolveClaudeModel("fable", true); got != "fable" {
		t.Fatalf("got %q, want flag value %q", got, "fable")
	}
}

func TestResolveClaudeModelEnvBeatsDefault(t *testing.T) {
	t.Setenv(envClaudeModel, "  fable  ")
	if got := resolveClaudeModel("", false); got != "fable" {
		t.Fatalf("got %q, want trimmed env value %q", got, "fable")
	}
}

func TestResolveClaudeModelDefault(t *testing.T) {
	t.Setenv(envClaudeModel, "")
	if got := resolveClaudeModel("", false); got != claude.DefaultClaudeModel {
		t.Fatalf("got %q, want default %q", got, claude.DefaultClaudeModel)
	}
	// An explicitly-set-but-empty flag falls through to the default too.
	if got := resolveClaudeModel("", true); got != claude.DefaultClaudeModel {
		t.Fatalf("got %q for empty flag, want default %q", got, claude.DefaultClaudeModel)
	}
}

func TestResolveClaudeEffortFlagWins(t *testing.T) {
	t.Setenv(envClaudeEffort, effortHigh)
	if got := resolveClaudeEffort("xhigh", true); got != "xhigh" {
		t.Fatalf("got %q, want flag value %q", got, "xhigh")
	}
}

func TestResolveClaudeEffortEnvBeatsDefault(t *testing.T) {
	t.Setenv(envClaudeEffort, "  high  ")
	if got := resolveClaudeEffort("", false); got != effortHigh {
		t.Fatalf("got %q, want trimmed env value %q", got, effortHigh)
	}
}

func TestResolveClaudeEffortDefault(t *testing.T) {
	t.Setenv(envClaudeEffort, "")
	if got := resolveClaudeEffort("", false); got != claude.DefaultClaudeEffort {
		t.Fatalf("got %q, want default %q", got, claude.DefaultClaudeEffort)
	}
}

func TestResolvePlanModelFlagWins(t *testing.T) {
	t.Setenv(envPlanModel, "sonnet")
	if got := resolvePlanModel(modelOpus, true); got != modelOpus {
		t.Fatalf("got %q, want flag value %q", got, modelOpus)
	}
}

func TestResolvePlanModelEnvBeatsDefault(t *testing.T) {
	t.Setenv(envPlanModel, "  opus  ")
	if got := resolvePlanModel("", false); got != modelOpus {
		t.Fatalf("got %q, want trimmed env value %q", got, modelOpus)
	}
}

func TestResolvePlanModelDefault(t *testing.T) {
	t.Setenv(envPlanModel, "")
	if got := resolvePlanModel("", false); got != claude.DefaultPlanModel {
		t.Fatalf("got %q, want default %q", got, claude.DefaultPlanModel)
	}
	// An explicitly-set-but-empty flag falls through to the default too.
	if got := resolvePlanModel("", true); got != claude.DefaultPlanModel {
		t.Fatalf("got %q for empty flag, want default %q", got, claude.DefaultPlanModel)
	}
}

func TestResolveImplementModelFlagWins(t *testing.T) {
	t.Setenv(envImplementModel, "sonnet")
	if got := resolveImplementModel(modelOpus, true); got != modelOpus {
		t.Fatalf("got %q, want flag value %q", got, modelOpus)
	}
}

func TestResolveImplementModelEnvBeatsDefault(t *testing.T) {
	t.Setenv(envImplementModel, "  opus  ")
	if got := resolveImplementModel("", false); got != modelOpus {
		t.Fatalf("got %q, want trimmed env value %q", got, modelOpus)
	}
}

// TestResolveImplementModelDefaultEmpty pins that with neither flag nor env
// set the resolver yields "" — the inherit---claude-model sentinel the Client
// maps to its main model — rather than some compiled-in model of its own.
func TestResolveImplementModelDefaultEmpty(t *testing.T) {
	t.Setenv(envImplementModel, "")
	if got := resolveImplementModel("", false); got != "" {
		t.Fatalf("got %q, want empty (inherit --claude-model)", got)
	}
	// An explicitly-set-but-empty flag falls through to the empty default too.
	if got := resolveImplementModel("", true); got != "" {
		t.Fatalf("got %q for empty flag, want empty (inherit --claude-model)", got)
	}
}

func TestResolveImplementWorkerModelFlagWins(t *testing.T) {
	t.Setenv(envImplementWorkerModel, "sonnet")
	if got := resolveImplementWorkerModel(modelOpus, true); got != modelOpus {
		t.Fatalf("got %q, want flag value %q", got, modelOpus)
	}
}

func TestResolveImplementWorkerModelEnvBeatsDefault(t *testing.T) {
	t.Setenv(envImplementWorkerModel, "  opus  ")
	if got := resolveImplementWorkerModel("", false); got != modelOpus {
		t.Fatalf("got %q, want trimmed env value %q", got, modelOpus)
	}
}

// TestResolveImplementWorkerModelDefaultEmpty pins that with neither flag nor
// env set the resolver yields "" — the orchestrator-mode-off sentinel the
// claude package maps to the historical single-session behavior — rather than
// some compiled-in worker model of its own.
func TestResolveImplementWorkerModelDefaultEmpty(t *testing.T) {
	t.Setenv(envImplementWorkerModel, "")
	if got := resolveImplementWorkerModel("", false); got != "" {
		t.Fatalf("got %q, want empty (orchestrator mode off)", got)
	}
	// An explicitly-set-but-empty flag falls through to the empty default too.
	if got := resolveImplementWorkerModel("", true); got != "" {
		t.Fatalf("got %q for empty flag, want empty (orchestrator mode off)", got)
	}
}

func TestResolveImplementWorkerEffortFlagWins(t *testing.T) {
	t.Setenv(envImplementWorkerEffort, effortHigh)
	if got := resolveImplementWorkerEffort("max", true); got != "max" {
		t.Fatalf("got %q, want flag value %q", got, "max")
	}
}

func TestResolveImplementWorkerEffortEnvBeatsDefault(t *testing.T) {
	t.Setenv(envImplementWorkerEffort, "  high  ")
	if got := resolveImplementWorkerEffort("", false); got != effortHigh {
		t.Fatalf("got %q, want trimmed env value %q", got, effortHigh)
	}
}

func TestResolveImplementWorkerEffortDefault(t *testing.T) {
	t.Setenv(envImplementWorkerEffort, "")
	if got := resolveImplementWorkerEffort("", false); got != claude.DefaultImplementWorkerEffort {
		t.Fatalf("got %q, want default %q", got, claude.DefaultImplementWorkerEffort)
	}
}

func TestResolvePlanEffortFlagWins(t *testing.T) {
	t.Setenv(envPlanEffort, effortHigh)
	if got := resolvePlanEffort("max", true); got != "max" {
		t.Fatalf("got %q, want flag value %q", got, "max")
	}
}

func TestResolvePlanEffortEnvBeatsDefault(t *testing.T) {
	t.Setenv(envPlanEffort, "  high  ")
	if got := resolvePlanEffort("", false); got != effortHigh {
		t.Fatalf("got %q, want trimmed env value %q", got, effortHigh)
	}
}

func TestResolvePlanEffortDefault(t *testing.T) {
	t.Setenv(envPlanEffort, "")
	if got := resolvePlanEffort("", false); got != claude.DefaultPlanEffort {
		t.Fatalf("got %q, want default %q", got, claude.DefaultPlanEffort)
	}
	// An explicitly-set-but-empty flag falls through to the default too.
	if got := resolvePlanEffort("", true); got != claude.DefaultPlanEffort {
		t.Fatalf("got %q for empty flag, want default %q", got, claude.DefaultPlanEffort)
	}
}

func TestResolveStructureModelFlagWins(t *testing.T) {
	t.Setenv(envStructureModel, modelOpus)
	if got := resolveStructureModel("sonnet", true); got != "sonnet" {
		t.Fatalf("got %q, want flag value %q", got, "sonnet")
	}
}

func TestResolveStructureModelEnvBeatsDefault(t *testing.T) {
	t.Setenv(envStructureModel, "  opus  ")
	if got := resolveStructureModel("", false); got != modelOpus {
		t.Fatalf("got %q, want trimmed env value %q", got, modelOpus)
	}
}

func TestResolveStructureModelDefault(t *testing.T) {
	t.Setenv(envStructureModel, "")
	if got := resolveStructureModel("", false); got != claude.DefaultStructureModel {
		t.Fatalf("got %q, want default %q", got, claude.DefaultStructureModel)
	}
	// An explicitly-set-but-empty flag falls through to the default too.
	if got := resolveStructureModel("", true); got != claude.DefaultStructureModel {
		t.Fatalf("got %q for empty flag, want default %q", got, claude.DefaultStructureModel)
	}
}

func TestResolveStructureEffortFlagWins(t *testing.T) {
	t.Setenv(envStructureEffort, "medium")
	got, err := resolveStructureEffort("xhigh", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "xhigh" {
		t.Fatalf("got %q, want flag value %q", got, "xhigh")
	}
}

func TestResolveStructureEffortEnvBeatsDefault(t *testing.T) {
	t.Setenv(envStructureEffort, "  high  ")
	got, err := resolveStructureEffort("", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != effortHigh {
		t.Fatalf("got %q, want trimmed env value %q", got, effortHigh)
	}
}

func TestResolveStructureEffortDefault(t *testing.T) {
	t.Setenv(envStructureEffort, "")
	got, err := resolveStructureEffort("", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != claude.DefaultStructureEffort {
		t.Fatalf("got %q, want default %q", got, claude.DefaultStructureEffort)
	}
	// An explicitly-set-but-empty flag falls through to the default too.
	got, err = resolveStructureEffort("", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != claude.DefaultStructureEffort {
		t.Fatalf("got %q for empty flag, want default %q", got, claude.DefaultStructureEffort)
	}
}

// TestResolveStructureEffortRejectsInvalid pins the fail-fast guard: an
// off-vocabulary effort from the flag or the env is rejected up front, before
// any claude call, instead of passing through to fail only after the expensive
// upstream reasoning pass.
func TestResolveStructureEffortRejectsInvalid(t *testing.T) {
	t.Setenv(envStructureEffort, "")
	if _, err := resolveStructureEffort("maximum", true); err == nil {
		t.Fatal("expected an error for an invalid --structure-effort flag value")
	}

	t.Setenv(envStructureEffort, "maximum")
	if _, err := resolveStructureEffort("", false); err == nil {
		t.Fatal("expected an error for an invalid PLANWERK_STRUCTURE_EFFORT value")
	}
}

func TestRunCacheStatsEmpty(t *testing.T) {
	dir := t.TempDir()
	t.Cleanup(cache.SetDir(dir))

	var buf bytes.Buffer
	if err := runCacheStats(&buf); err != nil {
		t.Fatalf("runCacheStats: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "entries:   0") {
		t.Fatalf("expected zero-entry summary, got:\n%s", out)
	}
}

func TestRunCacheStatsAndInspectPopulated(t *testing.T) {
	dir := t.TempDir()
	t.Cleanup(cache.SetDir(dir))

	if err := cache.PutRaw("abc123", cache.CommandReview, []byte(`{"hello":"world"}`)); err != nil {
		t.Fatalf("PutRaw: %v", err)
	}

	var statsBuf bytes.Buffer
	if err := runCacheStats(&statsBuf); err != nil {
		t.Fatalf("runCacheStats: %v", err)
	}
	statsOut := statsBuf.String()
	for _, want := range []string{"entries:   1", "review", "abc123"} {
		if !strings.Contains(statsOut, want) {
			t.Fatalf("stats output missing %q:\n%s", want, statsOut)
		}
	}

	var inspectBuf bytes.Buffer
	if err := runCacheInspect(&inspectBuf, "abc123"); err != nil {
		t.Fatalf("runCacheInspect: %v", err)
	}
	inspectOut := inspectBuf.String()
	for _, want := range []string{"key:       abc123", "command:   review", "\"hello\": \"world\""} {
		if !strings.Contains(inspectOut, want) {
			t.Fatalf("inspect output missing %q:\n%s", want, inspectOut)
		}
	}
}

func TestRunCacheInspectMissingKey(t *testing.T) {
	dir := t.TempDir()
	t.Cleanup(cache.SetDir(dir))

	var buf bytes.Buffer
	err := runCacheInspect(&buf, "does-not-exist")
	if err == nil {
		t.Fatalf("expected error for missing key, got nil")
	}
	if !strings.Contains(err.Error(), "no cache entry for key") {
		t.Fatalf("error = %v, want friendly not-found message", err)
	}
}

func TestResolveMaxPatternsFileZeroDisablesTruncation(t *testing.T) {
	t.Setenv(envMaxPatterns, "50")
	got, err := resolveMaxPatterns(0, false, intPtr(0))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 0 {
		t.Fatalf("got %d, want 0 (file value disables truncation)", got)
	}
}
