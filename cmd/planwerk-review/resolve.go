package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/planwerk/planwerk-review/internal/claude"
	"github.com/planwerk/planwerk-review/internal/patterns"
)

// envMaxPatterns is the environment variable used to override the default
// maximum number of review patterns injected into the prompt.
const envMaxPatterns = "PLANWERK_MAX_PATTERNS"

// envRemotePatternsTTL is the environment variable used to override the
// default refresh TTL for remotely-fetched pattern sources.
const envRemotePatternsTTL = "PLANWERK_REMOTE_PATTERNS_TTL"

// envShowClaudeOutput toggles live streaming of Claude Code output. Any
// truthy value (1, true, yes, on; case-insensitive) enables it; the CLI
// flag --show-claude-output takes precedence when explicitly set.
const envShowClaudeOutput = "PLANWERK_SHOW_CLAUDE_OUTPUT"

// envClaudeTimeout overrides the per-invocation Claude Code timeout used by
// every subcommand. Value is parsed with time.ParseDuration (e.g. "20m",
// "1h30m"); a non-positive value is rejected. The --claude-timeout CLI
// flag takes precedence when explicitly set.
const envClaudeTimeout = "PLANWERK_CLAUDE_TIMEOUT"

// envClaudeModel overrides the model passed to Claude Code via --model for
// every subcommand (e.g. "fable", "claude-fable-5", "sonnet"). The
// --claude-model CLI flag takes precedence when explicitly set.
const envClaudeModel = "PLANWERK_CLAUDE_MODEL"

// envClaudeEffort overrides the reasoning effort passed to Claude Code via
// --effort for every subcommand (low, medium, high, xhigh, max). The
// --claude-effort CLI flag takes precedence when explicitly set.
const envClaudeEffort = "PLANWERK_CLAUDE_EFFORT"

// envPlanModel overrides the model used by the implement command's
// planning session (e.g. "fable", "opus"). The --plan-model CLI flag takes
// precedence when explicitly set.
const envPlanModel = "PLANWERK_PLAN_MODEL"

// envPlanEffort overrides the reasoning effort used by the implement
// command's planning session (low, medium, high, xhigh, max). The
// --plan-effort CLI flag takes precedence when explicitly set.
const envPlanEffort = "PLANWERK_PLAN_EFFORT"

// envClaudeInheritUserConfig opts orchestrated Claude sessions out of hermetic
// mode, letting them load the invoking user's global ~/.claude settings and
// MCP servers. Any truthy value (1, true, yes, on; case-insensitive) enables
// inheritance; the --claude-inherit-user-config CLI flag takes precedence when
// explicitly set. Off by default so reviews stay reproducible across machines.
const envClaudeInheritUserConfig = "PLANWERK_CLAUDE_INHERIT_USER_CONFIG"

// Output format identifiers accepted by the --format flag.
const (
	formatMarkdown = "markdown"
	formatJSON     = "json"
	formatIssues   = "issues"
)

// resolveShowClaudeOutput returns the effective streaming toggle.
// Precedence: explicit CLI flag, then PLANWERK_SHOW_CLAUDE_OUTPUT, then
// off by default.
func resolveShowClaudeOutput(flagValue, flagSet bool) bool {
	if flagSet {
		return flagValue
	}
	if raw, ok := os.LookupEnv(envShowClaudeOutput); ok && raw != "" {
		switch strings.ToLower(strings.TrimSpace(raw)) {
		case "1", "true", "yes", "on":
			return true
		}
	}
	return false
}

// resolveClaudeInheritUserConfig returns whether orchestrated Claude sessions
// should inherit the invoking user's global ~/.claude settings and MCP servers.
// Precedence: explicit CLI flag, then PLANWERK_CLAUDE_INHERIT_USER_CONFIG, then
// off by default (hermetic, for reproducible output). It mirrors the truthy
// parsing of resolveShowClaudeOutput.
func resolveClaudeInheritUserConfig(flagValue, flagSet bool) bool {
	if flagSet {
		return flagValue
	}
	if raw, ok := os.LookupEnv(envClaudeInheritUserConfig); ok && raw != "" {
		switch strings.ToLower(strings.TrimSpace(raw)) {
		case "1", "true", "yes", "on":
			return true
		}
	}
	return false
}

// resolveClaudeTimeout returns the effective per-invocation Claude Code
// timeout. Precedence: explicit CLI flag, then PLANWERK_CLAUDE_TIMEOUT,
// then the compiled-in default. A non-positive value is rejected because
// disabling the timeout would let a stuck claude process hang the CLI
// indefinitely; users who want longer runs should pass an explicit
// large duration.
func resolveClaudeTimeout(flagValue time.Duration, flagSet bool) (time.Duration, error) {
	if flagSet {
		if flagValue <= 0 {
			return 0, fmt.Errorf("--claude-timeout must be > 0, got %s", flagValue)
		}
		return flagValue, nil
	}
	if raw, ok := os.LookupEnv(envClaudeTimeout); ok && raw != "" {
		v, err := time.ParseDuration(raw)
		if err != nil {
			return 0, fmt.Errorf("invalid %s=%q: %w", envClaudeTimeout, raw, err)
		}
		if v <= 0 {
			return 0, fmt.Errorf("%s must be > 0, got %s", envClaudeTimeout, v)
		}
		return v, nil
	}
	return claude.DefaultClaudeTimeout, nil
}

// resolveClaudeModel returns the effective model passed to Claude Code via
// --model. Precedence: explicit CLI flag, then PLANWERK_CLAUDE_MODEL, then the
// compiled-in default. The value is passed through verbatim — model names are
// validated by Claude Code itself, so an unknown name surfaces as a claude
// error rather than being rejected here.
func resolveClaudeModel(flagValue string, flagSet bool) string {
	if flagSet && flagValue != "" {
		return flagValue
	}
	if raw, ok := os.LookupEnv(envClaudeModel); ok {
		if v := strings.TrimSpace(raw); v != "" {
			return v
		}
	}
	return claude.DefaultClaudeModel
}

// resolveClaudeEffort returns the effective reasoning effort passed to Claude
// Code via --effort. Precedence: explicit CLI flag, then PLANWERK_CLAUDE_EFFORT,
// then the compiled-in default. The value is passed through verbatim — the
// accepted effort levels are validated by Claude Code itself, so an unknown
// level surfaces as a claude error rather than being rejected here.
func resolveClaudeEffort(flagValue string, flagSet bool) string {
	if flagSet && flagValue != "" {
		return flagValue
	}
	if raw, ok := os.LookupEnv(envClaudeEffort); ok {
		if v := strings.TrimSpace(raw); v != "" {
			return v
		}
	}
	return claude.DefaultClaudeEffort
}

// resolvePlanModel returns the effective model for the implement command's
// planning session. Precedence: explicit CLI flag, then PLANWERK_PLAN_MODEL,
// then the compiled-in default. The value is passed through verbatim — model
// names are validated by Claude Code itself, so an unknown name surfaces as
// a claude error rather than being rejected here.
func resolvePlanModel(flagValue string, flagSet bool) string {
	if flagSet && flagValue != "" {
		return flagValue
	}
	if raw, ok := os.LookupEnv(envPlanModel); ok {
		if v := strings.TrimSpace(raw); v != "" {
			return v
		}
	}
	return claude.DefaultPlanModel
}

// resolvePlanEffort returns the effective reasoning effort for the implement
// command's planning session. Precedence: explicit CLI flag, then
// PLANWERK_PLAN_EFFORT, then the compiled-in default. The value is passed
// through verbatim — the accepted effort levels are validated by Claude Code
// itself, so an unknown level surfaces as a claude error rather than being
// rejected here.
func resolvePlanEffort(flagValue string, flagSet bool) string {
	if flagSet && flagValue != "" {
		return flagValue
	}
	if raw, ok := os.LookupEnv(envPlanEffort); ok {
		if v := strings.TrimSpace(raw); v != "" {
			return v
		}
	}
	return claude.DefaultPlanEffort
}

// resolveRemotePatternsTTL returns the effective remote-patterns TTL.
// Precedence: explicit CLI flag, then PLANWERK_REMOTE_PATTERNS_TTL, then the
// compiled-in default. A value of 0 or negative disables refresh.
func resolveRemotePatternsTTL(flagValue time.Duration, flagSet bool) (time.Duration, error) {
	if flagSet {
		return flagValue, nil
	}
	if raw, ok := os.LookupEnv(envRemotePatternsTTL); ok && raw != "" {
		v, err := time.ParseDuration(raw)
		if err != nil {
			return 0, fmt.Errorf("invalid %s=%q: %w", envRemotePatternsTTL, raw, err)
		}
		return v, nil
	}
	return patterns.DefaultRemoteTTL, nil
}

// resolveMaxPatterns returns the effective max-patterns limit. Precedence:
// explicit CLI flag, then .planwerk/config.yaml, then PLANWERK_MAX_PATTERNS,
// then the compiled-in default. A value of 0 or negative disables truncation.
func resolveMaxPatterns(flagValue int, flagSet bool, fileValue *int) (int, error) {
	if flagSet {
		return flagValue, nil
	}
	if fileValue != nil {
		return *fileValue, nil
	}
	if raw, ok := os.LookupEnv(envMaxPatterns); ok && raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil {
			return 0, fmt.Errorf("invalid %s=%q: %w", envMaxPatterns, raw, err)
		}
		return v, nil
	}
	return patterns.DefaultMaxPatternsInPrompt, nil
}
