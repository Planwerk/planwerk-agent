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

// envImplementWorkerModel overrides the model the implementer subagents run on
// in the implement/ship commands' orchestrated mode (e.g. "opus",
// "claude-opus-5"). Empty or unset keeps orchestrator mode OFF and the
// implement session writes the code itself, as before the worker tier existed.
// The --implement-worker-model CLI flag takes precedence when explicitly set.
const envImplementWorkerModel = "PLANWERK_IMPLEMENT_WORKER_MODEL"

// envImplementWorkerEffort overrides the reasoning effort the implementer
// subagents run at in orchestrated mode (low, medium, high, xhigh, max);
// ignored while orchestrator mode is off. The --implement-worker-effort CLI
// flag takes precedence when explicitly set.
const envImplementWorkerEffort = "PLANWERK_IMPLEMENT_WORKER_EFFORT"

// envFinderModel overrides the model used by the read-only finder passes (the
// adversarial pass, the domain specialists, coverage, compliance, the simplify
// finder, and claim verification) for every subcommand. The --finder-model CLI
// flag takes precedence when explicitly set. Unset leaves the finders on the
// main model.
const envFinderModel = "PLANWERK_FINDER_MODEL"

// envFinderEffort overrides the reasoning effort used by the finder passes
// (low, medium, high, xhigh, max). The --finder-effort CLI flag takes precedence
// when explicitly set. Unset leaves the finders on the main effort.
const envFinderEffort = "PLANWERK_FINDER_EFFORT"

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

// validEfforts is the closed set of reasoning-effort levels Claude Code accepts.
var validEfforts = map[string]bool{"low": true, "medium": true, "high": true, "xhigh": true, "max": true}

// resolveString returns the flag value when the flag was set to a non-empty
// value, else the trimmed value of the environment variable env when that is
// non-empty, else def. The model and effort resolvers all follow it; the value
// is passed through verbatim, because Claude Code validates model names and
// effort levels itself and an unknown one surfaces as a claude error.
func resolveString(flag string, set bool, env, def string) string {
	if set && flag != "" {
		return flag
	}
	if raw, ok := os.LookupEnv(env); ok {
		if v := strings.TrimSpace(raw); v != "" {
			return v
		}
	}
	return def
}

// resolveBool returns the flag value when the flag was set, else the truthy
// parse of the environment variable env, else false.
func resolveBool(flag, set bool, env string) bool {
	if set {
		return flag
	}
	v, _ := lookupBoolEnv(env)
	return v
}

// resolveEffort resolves an effort level like resolveString and validates the
// result against the closed set Claude Code accepts, in PersistentPreRunE
// before any claude call: the structuring and finder efforts govern passes
// that run after an expensive upstream pass, so a typo must fail before that
// pass burns its tokens. allowEmpty admits "" (the finder default), which
// means "inherit" rather than a level. flagName names the flag in the error.
func resolveEffort(flag string, set bool, env, def string, allowEmpty bool, flagName string) (string, error) {
	effort := resolveString(flag, set, env, def)
	if effort == "" && allowEmpty {
		return "", nil
	}
	if !validEfforts[effort] {
		return "", fmt.Errorf("invalid %s %q: must be one of low, medium, high, xhigh, max (env: %s)", flagName, effort, env)
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
// (overrides --wiki), an explicit --wiki, the config file, PLANWERK_WIKI, then
// the default-off behavior. The wiki is off by default and must be opted into
// per repo, because it is a separate, often world-editable permission surface.
// Ref precedence: --wiki-ref, the config file, then PLANWERK_WIKI_REF. The repo
// override comes from the config file only — the issue defines no flag for it.
//
// The config file outranks the environment, which is the order the
// configuration reference documents and resolveMaxPatterns implements. These
// two resolvers had it the other way around, so a repository that pinned
// wiki.enabled in .planwerk/config.yaml lost to whatever PLANWERK_WIKI a CI
// image happened to export for another job.
func resolveWikiOptions(enable, disable, enableChanged, disableChanged bool, refFlag string, refChanged bool, fc cli.WikiFileConfig) patterns.WikiOptions {
	enabled := false
	switch {
	case disableChanged && disable:
		enabled = false
	case enableChanged:
		enabled = enable
	default:
		if fc.Enabled != nil {
			enabled = *fc.Enabled
		} else if v, ok := lookupBoolEnv(envWiki); ok {
			enabled = v
		}
	}

	var ref string
	switch {
	case refChanged:
		ref = refFlag
	default:
		if fc.Ref != nil {
			ref = *fc.Ref
		} else if v := strings.TrimSpace(os.Getenv(envWikiRef)); v != "" {
			ref = v
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
// Precedence (highest first): an explicit --capture-wiki flag, the config
// file's capture.wiki, PLANWERK_CAPTURE_WIKI, then the default-off behavior —
// the order the configuration reference documents. Default off keeps a run propose-only: the
// write-back is an additive, outward-facing surface that must be opted into,
// mirroring the Enabled branch of resolveWikiOptions.
func resolveCaptureWiki(flagValue, flagChanged bool, fc cli.CaptureFileConfig) bool {
	if flagChanged {
		return flagValue
	}
	if fc.Wiki != nil {
		return *fc.Wiki
	}
	if v, ok := lookupBoolEnv(envCaptureWiki); ok {
		return v
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
