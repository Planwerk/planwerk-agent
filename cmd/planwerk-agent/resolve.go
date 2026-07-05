package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/pflag"

	"github.com/planwerk/planwerk-agent/internal/claude"
	"github.com/planwerk/planwerk-agent/internal/cli"
	"github.com/planwerk/planwerk-agent/internal/patterns"
)

// addWikiFlags registers the --wiki / --no-wiki / --wiki-ref flags on a
// subcommand's flag set, binding them to the given variables. It is shared by
// the review, audit, propose, and implement commands so the flag names, default
// (wiki off), and help text cannot drift between them. The wiki is off by
// default and requires an explicit per-repo opt-in: a GitHub Wiki is a separate
// permission surface (often world-editable, never gated by branch protection or
// PR review), so enabling it grants its unreviewed editors influence over the
// agent's prompts.
func addWikiFlags(flags *pflag.FlagSet, enable, disable *bool, ref *string) {
	flags.BoolVar(enable, "wiki", false, "Use the target repo's GitHub Wiki as a knowledge source (review patterns + project memory; off by default — enabling trusts the wiki's unreviewed editors; env: "+envWiki+")")
	flags.BoolVar(disable, "no-wiki", false, "Do not use the target repo's GitHub Wiki (overrides --wiki)")
	flags.StringVar(ref, "wiki-ref", "", "Pin the wiki to a branch, tag, or commit (env: "+envWikiRef+"; empty uses the wiki's default branch)")
}

// envMaxPatterns is the environment variable used to override the default
// maximum number of review patterns injected into the prompt.
const envMaxPatterns = "PLANWERK_MAX_PATTERNS"

// envRemotePatternsTTL is the environment variable used to override the
// default refresh TTL for remotely-fetched pattern sources.
const envRemotePatternsTTL = "PLANWERK_REMOTE_PATTERNS_TTL"

// envWiki toggles using the target repo's GitHub Wiki as a knowledge source.
// Any truthy value (1, true, yes, on) enables it and any falsy value (0, false,
// no, off) disables it; the --wiki/--no-wiki CLI flags take precedence.
const envWiki = "PLANWERK_WIKI"

// envWikiRef pins the wiki to a branch, tag, or commit. The --wiki-ref CLI flag
// takes precedence when explicitly set.
const envWikiRef = "PLANWERK_WIKI_REF"

// envCaptureWiki gates the capture write-back shared by the implement, review,
// and audit commands: whether the accepted proposal pages are pushed to the
// wiki. Any truthy value (1, true, yes, on) enables it and any falsy value (0,
// false, no, off) disables it; the --capture-wiki CLI flag takes precedence.
const envCaptureWiki = "PLANWERK_CAPTURE_WIKI"

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

// envImplementModel overrides the model used by the implement session only —
// the code-writing phase of the implement and ship commands (e.g. "fable",
// "sonnet"). Empty or unset inherits --claude-model. The --implement-model
// CLI flag takes precedence when explicitly set.
const envImplementModel = "PLANWERK_IMPLEMENT_MODEL"

// envStructureModel overrides the model used by the JSON-structuring passes
// for every subcommand (e.g. "sonnet", "opus"). The --structure-model CLI
// flag takes precedence when explicitly set.
const envStructureModel = "PLANWERK_STRUCTURE_MODEL"

// envStructureEffort overrides the reasoning effort used by the
// JSON-structuring passes for every subcommand (low, medium, high, xhigh,
// max). The --structure-effort CLI flag takes precedence when explicitly set.
const envStructureEffort = "PLANWERK_STRUCTURE_EFFORT"

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
	v, _ := lookupBoolEnv(envShowClaudeOutput)
	return v
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
	v, _ := lookupBoolEnv(envClaudeInheritUserConfig)
	return v
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

// resolveImplementModel returns the effective model override for the implement
// session. Precedence: explicit CLI flag, then PLANWERK_IMPLEMENT_MODEL, then
// empty — unlike the plan/structure tiers there is no compiled-in default,
// because empty means "inherit --claude-model" (the Client falls back to its
// main model when the override is unset). The value is passed through verbatim —
// model names are validated by Claude Code itself, so an unknown name surfaces
// as a claude error rather than being rejected here.
func resolveImplementModel(flagValue string, flagSet bool) string {
	if flagSet && flagValue != "" {
		return flagValue
	}
	if raw, ok := os.LookupEnv(envImplementModel); ok {
		if v := strings.TrimSpace(raw); v != "" {
			return v
		}
	}
	return ""
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

// resolveStructureModel returns the effective model for the JSON-structuring
// passes. Precedence: explicit CLI flag, then PLANWERK_STRUCTURE_MODEL, then
// the compiled-in default. The value is passed through verbatim — model names
// are validated by Claude Code itself, so an unknown name surfaces as a claude
// error rather than being rejected here.
func resolveStructureModel(flagValue string, flagSet bool) string {
	if flagSet && flagValue != "" {
		return flagValue
	}
	if raw, ok := os.LookupEnv(envStructureModel); ok {
		if v := strings.TrimSpace(raw); v != "" {
			return v
		}
	}
	return claude.DefaultStructureModel
}

// validEfforts is the closed set of reasoning-effort levels Claude Code accepts.
var validEfforts = map[string]bool{"low": true, "medium": true, "high": true, "xhigh": true, "max": true}

// resolveStructureEffort returns the effective reasoning effort for the
// JSON-structuring passes. Precedence: explicit CLI flag, then
// PLANWERK_STRUCTURE_EFFORT, then the compiled-in default. Unlike
// --claude-effort — which governs the first, cheap call so a typo surfaces on
// the very first invocation — the structuring effort governs only the late
// structuring call that runs AFTER the expensive upstream reasoning pass. A
// typo there ("maximum") would let the opus review reason and burn its full
// token cost before claude rejects the bad effort, discarding the result. So
// the resolved value is validated against the closed effort set here, in
// PersistentPreRunE before any claude call, and a bad value is rejected fast.
func resolveStructureEffort(flagValue string, flagSet bool) (string, error) {
	effort := claude.DefaultStructureEffort
	if flagSet && flagValue != "" {
		effort = flagValue
	} else if raw, ok := os.LookupEnv(envStructureEffort); ok {
		if v := strings.TrimSpace(raw); v != "" {
			effort = v
		}
	}
	if !validEfforts[effort] {
		return "", fmt.Errorf("invalid --structure-effort %q: must be one of low, medium, high, xhigh, max (env: %s)", effort, envStructureEffort)
	}
	return effort, nil
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

// lookupBoolEnv parses a truthy/falsy boolean from the named environment
// variable. ok is false when the variable is unset, empty, or holds an
// unrecognized value, so the caller falls through to the next precedence tier.
// Truthy: 1/true/yes/on; falsy: 0/false/no/off (case-insensitive).
func lookupBoolEnv(name string) (value, ok bool) {
	raw, present := os.LookupEnv(name)
	if !present {
		return false, false
	}
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true, true
	case "0", "false", "no", "off":
		return false, true
	default:
		return false, false
	}
}

// resolveWikiOptions assembles the effective WikiOptions for the target repo's
// GitHub Wiki knowledge source. Enabled precedence (highest first): --no-wiki
// (overrides --wiki), an explicit --wiki, PLANWERK_WIKI, the config file, then
// the default-off behavior. The wiki is off by default and must be opted into
// per repo, because it is a separate, often world-editable permission surface.
// Ref precedence: --wiki-ref, PLANWERK_WIKI_REF, then the config file. The repo
// override comes from the config file only — the issue defines no flag for it.
func resolveWikiOptions(enable, disable, enableChanged, disableChanged bool, refFlag string, refChanged bool, fc cli.WikiFileConfig) patterns.WikiOptions {
	enabled := false
	switch {
	case disableChanged && disable:
		enabled = false
	case enableChanged:
		enabled = enable
	default:
		if v, ok := lookupBoolEnv(envWiki); ok {
			enabled = v
		} else if fc.Enabled != nil {
			enabled = *fc.Enabled
		}
	}

	var ref string
	switch {
	case refChanged:
		ref = refFlag
	default:
		if v := strings.TrimSpace(os.Getenv(envWikiRef)); v != "" {
			ref = v
		} else if fc.Ref != nil {
			ref = *fc.Ref
		}
	}

	opts := patterns.WikiOptions{Enabled: enabled, Ref: ref}
	if fc.Repo != nil {
		opts.Repo = *fc.Repo
	}
	return opts
}

// resolveCaptureWiki returns whether a command's capture pass should push the
// accepted proposal pages to the wiki. Shared by implement, review, and audit.
// Precedence (highest first): an explicit --capture-wiki flag,
// PLANWERK_CAPTURE_WIKI, the config file's capture.wiki, then the default-off
// behavior. Default off keeps a run propose-only: the
// write-back is an additive, outward-facing surface that must be opted into,
// mirroring the Enabled branch of resolveWikiOptions.
func resolveCaptureWiki(flagValue, flagChanged bool, fc cli.CaptureFileConfig) bool {
	if flagChanged {
		return flagValue
	}
	if v, ok := lookupBoolEnv(envCaptureWiki); ok {
		return v
	}
	if fc.Wiki != nil {
		return *fc.Wiki
	}
	return false
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
