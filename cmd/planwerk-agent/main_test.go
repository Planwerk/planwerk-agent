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

func TestResolveString(t *testing.T) {
	const env = "PLANWERK_TEST_RESOLVE_STRING"
	cases := []struct {
		name string
		flag string
		set  bool
		env  string
		want string
	}{
		{"flag wins over env", "fable", true, "sonnet", "fable"},
		{"explicitly empty flag falls through to env", "", true, "sonnet", "sonnet"},
		{"env is trimmed", "", false, "  fable  ", "fable"},
		{"blank env falls through to the default", "", false, "   ", "default"},
		{"nothing set yields the default", "", false, "", "default"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv(env, c.env)
			if got := resolveString(c.flag, c.set, env, "default"); got != c.want {
				t.Errorf("resolveString(%q, %v, env=%q) = %q, want %q", c.flag, c.set, c.env, got, c.want)
			}
		})
	}
}

func TestResolveBool(t *testing.T) {
	const env = "PLANWERK_TEST_RESOLVE_BOOL"
	t.Setenv(env, "1")
	if resolveBool(false, true, env) {
		t.Error("an explicit false flag must beat the env var")
	}
	if !resolveBool(true, true, env) {
		t.Error("an explicit true flag must take effect")
	}
	for _, raw := range []string{"1", "true", "TRUE", "yes", "On", " 1 "} {
		t.Setenv(env, raw)
		if !resolveBool(false, false, env) {
			t.Errorf("env=%q should enable", raw)
		}
	}
	for _, raw := range []string{"0", "false", "no", "off", "", "garbage"} {
		t.Setenv(env, raw)
		if resolveBool(false, false, env) {
			t.Errorf("env=%q should leave it off", raw)
		}
	}
}

func TestResolveEffort(t *testing.T) {
	const env = "PLANWERK_TEST_RESOLVE_EFFORT"
	t.Setenv(env, "medium")
	if got, err := resolveEffort("xhigh", true, env, "high", false, "--test-effort"); err != nil || got != "xhigh" {
		t.Errorf("flag: got %q, %v; want xhigh", got, err)
	}
	t.Setenv(env, "  high  ")
	if got, err := resolveEffort("", false, env, "low", false, "--test-effort"); err != nil || got != "high" {
		t.Errorf("env: got %q, %v; want high", got, err)
	}
	t.Setenv(env, "")
	if got, err := resolveEffort("", false, env, "low", false, "--test-effort"); err != nil || got != "low" {
		t.Errorf("default: got %q, %v; want low", got, err)
	}
	if got, err := resolveEffort("", true, env, "low", false, "--test-effort"); err != nil || got != "low" {
		t.Errorf("explicitly empty flag: got %q, %v; want the default low", got, err)
	}
	// The finder efforts admit an empty result, which means "inherit".
	if got, err := resolveEffort("", false, env, "", true, "--finder-effort"); err != nil || got != "" {
		t.Errorf("allowEmpty: got %q, %v; want empty", got, err)
	}
	if _, err := resolveEffort("", false, env, "", false, "--test-effort"); err == nil {
		t.Error("an empty result must be rejected when empty is not allowed")
	}
	// A typo is rejected wherever it comes from, naming the flag and the env var.
	if _, err := resolveEffort("maximum", true, env, "low", false, "--test-effort"); err == nil || !strings.Contains(err.Error(), "--test-effort") || !strings.Contains(err.Error(), env) {
		t.Errorf("invalid flag value: got %v", err)
	}
	t.Setenv(env, "maximum")
	if _, err := resolveEffort("", false, env, "low", true, "--finder-effort"); err == nil {
		t.Error("invalid env value must be rejected even when empty is allowed")
	}
}
