package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/cobra/doc"

	"github.com/planwerk/planwerk-review/internal/audit"
	"github.com/planwerk/planwerk-review/internal/cache"
	"github.com/planwerk/planwerk-review/internal/claude"
	"github.com/planwerk/planwerk-review/internal/cli"
	"github.com/planwerk/planwerk-review/internal/elaborate"
	"github.com/planwerk/planwerk-review/internal/fix"
	"github.com/planwerk/planwerk-review/internal/logging"
	"github.com/planwerk/planwerk-review/internal/patterns"
	"github.com/planwerk/planwerk-review/internal/prompt"
	"github.com/planwerk/planwerk-review/internal/propose"
	"github.com/planwerk/planwerk-review/internal/report"
	"github.com/planwerk/planwerk-review/internal/review"
)

// envMaxPatterns is the environment variable used to override the default
// maximum number of review patterns injected into the prompt.
const envMaxPatterns = "PLANWERK_MAX_PATTERNS"

// envRemotePatternsTTL is the environment variable used to override the
// default refresh TTL for remotely-fetched pattern sources.
const envRemotePatternsTTL = "PLANWERK_REMOTE_PATTERNS_TTL"

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

// Output format identifiers accepted by the --format flag.
const (
	formatMarkdown = "markdown"
	formatJSON     = "json"
	formatIssues   = "issues"
)

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

// devVersion is the placeholder version string used when no release version
// has been injected via ldflags. Triggers the unreleased-build warning.
const devVersion = "dev"

var version = devVersion

// buildInfo holds resolved version and build metadata, populated either from
// ldflags (main.version) or from runtime/debug.ReadBuildInfo.
type buildInfo struct {
	Version   string
	Commit    string
	BuildDate string
	GoVersion string
	IsDev     bool
}

// resolveBuildInfo returns build metadata, preferring the ldflags-injected
// version and falling back to debug.ReadBuildInfo when it is unset.
func resolveBuildInfo(ldflagsVersion string) buildInfo {
	bi := buildInfo{Version: ldflagsVersion}

	if info, ok := debug.ReadBuildInfo(); ok {
		bi.GoVersion = info.GoVersion
		if bi.Version == "" || bi.Version == devVersion {
			if v := info.Main.Version; v != "" && v != "(devel)" {
				bi.Version = v
			}
		}
		for _, s := range info.Settings {
			switch s.Key {
			case "vcs.revision":
				bi.Commit = s.Value
			case "vcs.time":
				bi.BuildDate = s.Value
			}
		}
	}

	if bi.Version == "" {
		bi.Version = devVersion
	}
	bi.IsDev = bi.Version == devVersion
	return bi
}

// writeVersion renders the version line, optional verbose build details, and
// a warning when this is an unreleased development build.
func writeVersion(w io.Writer, bi buildInfo, verbose bool) {
	var sb strings.Builder
	fmt.Fprintf(&sb, "planwerk-review version %s\n", bi.Version)
	if verbose {
		if bi.Commit != "" {
			fmt.Fprintf(&sb, "commit: %s\n", bi.Commit)
		}
		if bi.BuildDate != "" {
			fmt.Fprintf(&sb, "built: %s\n", bi.BuildDate)
		}
		if bi.GoVersion != "" {
			fmt.Fprintf(&sb, "go: %s\n", bi.GoVersion)
		}
	}
	if bi.IsDev {
		sb.WriteString("warning: unreleased development build — version metadata unavailable\n")
	}
	_, _ = io.WriteString(w, sb.String())
}

// validateCacheScope rejects scope strings that don't match a known command.
// An empty scope means "all commands" and is always valid.
func validateCacheScope(scope string) error {
	switch scope {
	case "", cache.CommandReview, cache.CommandPropose, cache.CommandAudit, elaborate.CommandElaborate:
		return nil
	default:
		return fmt.Errorf("unknown cache scope %q, supported: review, propose, audit, elaborate", scope)
	}
}

// runCacheStats renders a human-readable summary of the cache directory.
func runCacheStats(w io.Writer) error {
	stats, err := cache.Stats()
	if err != nil {
		return err
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "cache dir: %s\n", stats.Dir)
	fmt.Fprintf(&sb, "entries:   %d\n", stats.Total)
	fmt.Fprintf(&sb, "size:      %s\n", humanBytes(stats.TotalSize))
	if stats.Total > 0 {
		fmt.Fprintln(&sb, "by command:")
		names := make([]string, 0, len(stats.ByCommand))
		for name := range stats.ByCommand {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			cs := stats.ByCommand[name]
			fmt.Fprintf(&sb, "  %-8s %3d entries  %s\n", name, cs.Count, humanBytes(cs.Size))
		}
		fmt.Fprintln(&sb, "age distribution:")
		fmt.Fprintf(&sb, "  <= 1 day:    %d\n", stats.Ages.LessThanDay)
		fmt.Fprintf(&sb, "  <= 1 week:   %d\n", stats.Ages.LessThanWeek)
		fmt.Fprintf(&sb, "  <= 1 month:  %d\n", stats.Ages.LessThanMonth)
		fmt.Fprintf(&sb, "  >  1 month:  %d\n", stats.Ages.OlderThanMonth)
		if stats.Newest != nil {
			fmt.Fprintf(&sb, "newest:   %s  %s  (%s ago)\n",
				stats.Newest.Key, stats.Newest.Command, stats.Newest.Age.Round(time.Second))
		}
		if stats.Oldest != nil && (stats.Newest == nil || stats.Newest.Key != stats.Oldest.Key) {
			fmt.Fprintf(&sb, "oldest:   %s  %s  (%s ago)\n",
				stats.Oldest.Key, stats.Oldest.Command, stats.Oldest.Age.Round(time.Second))
		}
	}
	_, err = io.WriteString(w, sb.String())
	return err
}

// runCacheInspect prints metadata plus the pretty-printed JSON payload for a
// single cache key.
func runCacheInspect(w io.Writer, key string) error {
	meta, payload, err := cache.Inspect(key)
	if err != nil {
		if errors.Is(err, cache.ErrNotFound) {
			return fmt.Errorf("no cache entry for key %q", key)
		}
		return err
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "key:       %s\n", meta.Key)
	fmt.Fprintf(&sb, "command:   %s\n", meta.Command)
	if !meta.WrittenAt.IsZero() {
		fmt.Fprintf(&sb, "writtenAt: %s\n", meta.WrittenAt.Format(time.RFC3339))
		fmt.Fprintf(&sb, "age:       %s\n", meta.Age.Round(time.Second))
	} else {
		fmt.Fprintln(&sb, "writtenAt: (unknown — legacy entry)")
	}
	fmt.Fprintf(&sb, "size:      %s\n", humanBytes(meta.Size))
	fmt.Fprintln(&sb, "payload:")
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, payload, "  ", "  "); err != nil {
		fmt.Fprintf(&sb, "  %s\n", string(payload))
	} else {
		fmt.Fprintf(&sb, "  %s\n", pretty.String())
	}
	_, err = io.WriteString(w, sb.String())
	return err
}

// humanBytes formats a byte count using binary units (KiB/MiB/...).
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

func main() {
	var cfg cli.Config
	var minSeverity string
	var showVersion, verbose bool
	var logFormat string
	var remotePatternsTTL time.Duration
	var fileCfg cli.FileConfig

	rootCmd := &cobra.Command{
		Use:   "planwerk-review <pr-ref>",
		Short: "AI-powered code review for GitHub Pull Requests",
		Long: `planwerk-review uses Claude Code to analyze GitHub PR changes and produces
structured, categorized review results as Markdown or JSON output.

PR reference can be a URL (https://github.com/owner/repo/pull/123)
or short form (owner/repo#123).`,
		Args: cobra.RangeArgs(0, 1),
		// Errors are reported via slog in main() so they honor --log-format;
		// silencing cobra's own error/usage print avoids duplicate output.
		SilenceErrors: true,
		SilenceUsage:  true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			format, err := logging.ParseFormat(logFormat)
			if err != nil {
				return err
			}
			if err := logging.Init(logging.Options{
				Writer:  os.Stderr,
				Format:  format,
				Verbose: verbose,
			}); err != nil {
				return err
			}
			loaded, _, err := cli.LoadFileConfig(cli.DefaultConfigPath)
			if err != nil {
				return err
			}
			fileCfg = loaded

			ttl, err := resolveRemotePatternsTTL(remotePatternsTTL, cmd.Flags().Changed("remote-patterns-ttl"))
			if err != nil {
				return err
			}
			patterns.SetRemoteOptions(patterns.RemoteOptions{TTL: ttl})
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if showVersion {
				writeVersion(cmd.OutOrStdout(), resolveBuildInfo(version), verbose)
				return nil
			}
			if cfg.CacheStats {
				return runCacheStats(cmd.OutOrStdout())
			}
			if cfg.CacheInspect != "" {
				return runCacheInspect(cmd.OutOrStdout(), cfg.CacheInspect)
			}
			if cfg.ClearCache || cfg.ClearCacheScope != "" {
				scope := cfg.ClearCacheScope
				if err := validateCacheScope(scope); err != nil {
					return err
				}
				return cache.Clear(scope)
			}

			if len(args) == 0 {
				return fmt.Errorf("requires a PR reference argument")
			}
			cfg.PRRef = args[0]

			fileCfg.ApplyReview(&cfg, &minSeverity, cmd.Flags().Changed)

			maxPatterns, err := resolveMaxPatterns(cfg.MaxPatterns, cmd.Flags().Changed("max-patterns"), fileCfg.Review.MaxPatterns)
			if err != nil {
				return err
			}
			cfg.MaxPatterns = maxPatterns

			if minSeverity != "" {
				sev, err := report.ParseSeverity(minSeverity)
				if err != nil {
					return err
				}
				cfg.MinSeverity = sev
			} else {
				cfg.MinSeverity = report.SeverityInfo
			}

			switch cfg.Format {
			case formatMarkdown, formatJSON:
			default:
				return fmt.Errorf("unknown format %q, supported: markdown, json", cfg.Format)
			}

			if cfg.PostReview && cfg.Format == formatJSON {
				return fmt.Errorf("--post-review cannot be used with --format json")
			}

			// --inline implies --post-review
			if cfg.InlineReview {
				cfg.PostReview = true
			}
			if cfg.InlineReview && cfg.Format == formatJSON {
				return fmt.Errorf("--inline cannot be used with --format json")
			}

			opts := cfg.ToReviewOptions(version)
			return review.Run(os.Stdout, opts)
		},
	}

	persistent := rootCmd.PersistentFlags()
	persistent.BoolVarP(&verbose, "verbose", "v", false, "Enable debug-level logging (and verbose build info with --version)")
	persistent.StringVar(&logFormat, "log-format", "text", "Log output format (text, json)")
	persistent.DurationVar(&remotePatternsTTL, "remote-patterns-ttl", patterns.DefaultRemoteTTL, "Refresh interval for remote pattern sources (env: "+envRemotePatternsTTL+"; <=0 disables refresh once cached)")

	flags := rootCmd.Flags()
	flags.StringSliceVar(&cfg.PatternDirs, "patterns", nil, "Additional pattern sources: local dirs, github:owner/repo[/sub][@ref], or git+https://...[#ref[:sub]]")
	flags.StringVar(&minSeverity, "min-severity", "", "Minimum severity level (info, warning, critical, blocking)")
	flags.BoolVar(&cfg.NoRepoPatterns, "no-repo-patterns", false, "Ignore repo-specific patterns")
	flags.BoolVar(&cfg.NoLocalPatterns, "no-local-patterns", false, "Ignore local patterns from the tool")
	flags.BoolVar(&cfg.NoCache, "no-cache", false, "Ignore cache, force a fresh review")
	flags.BoolVar(&cfg.ClearCache, "clear-cache", false, "Clear cached reviews and exit (honors --clear-cache-scope)")
	flags.StringVar(&cfg.ClearCacheScope, "clear-cache-scope", "", "Restrict --clear-cache to a single command (review, propose, audit, elaborate)")
	flags.BoolVar(&cfg.CacheStats, "cache-stats", false, "Show cache size, age distribution, and per-command breakdown, then exit")
	flags.StringVar(&cfg.CacheInspect, "cache-inspect", "", "Print the metadata and payload for the given cache key, then exit")
	flags.DurationVar(&cfg.CacheMaxAge, "cache-max-age", cache.DefaultMaxAge, "Reject cached entries older than this duration (0 disables the TTL)")
	flags.StringVar(&cfg.Format, "format", "markdown", "Output format (markdown, json)")
	flags.BoolVar(&cfg.PostReview, "post-review", false, "Post the review as a comment on the PR")
	flags.BoolVar(&cfg.InlineReview, "inline", false, "Post review with inline comments using GitHub Review API (implies --post-review)")
	flags.BoolVar(&cfg.Thorough, "thorough", false, "Run additional adversarial review pass")
	flags.BoolVar(&cfg.CoverageMap, "coverage-map", false, "Generate test coverage map for changed functions")
	flags.IntVar(&cfg.MaxPatterns, "max-patterns", patterns.DefaultMaxPatternsInPrompt, "Max review patterns injected into the prompt (<=0 disables truncation, env: "+envMaxPatterns+")")
	flags.IntVar(&cfg.MaxFindings, "max-findings", 0, "Cap on findings returned (<=0 disables cap)")
	flags.BoolVar(&showVersion, "version", false, "Show version information and exit")

	// propose subcommand
	var proposeCfg cli.ProposeConfig

	proposeCmd := &cobra.Command{
		Use:   "propose <repo-ref>",
		Short: "Analyze a codebase and generate feature proposals",
		Long: `Analyze a GitHub repository in depth and generate concrete, actionable
feature proposals as structured Markdown suitable for GitHub issues.

Repository reference can be a URL (https://github.com/owner/repo)
or short form (owner/repo).`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			proposeCfg.RepoRef = args[0]

			fileCfg.ApplyPropose(&proposeCfg, cmd.Flags().Changed)

			maxPatterns, err := resolveMaxPatterns(proposeCfg.MaxPatterns, cmd.Flags().Changed("max-patterns"), fileCfg.Propose.MaxPatterns)
			if err != nil {
				return err
			}
			proposeCfg.MaxPatterns = maxPatterns

			switch proposeCfg.Format {
			case formatMarkdown, formatJSON, formatIssues:
			default:
				return fmt.Errorf("unknown format %q, supported: markdown, json, issues", proposeCfg.Format)
			}

			opts := proposeCfg.ToProposeOptions(version)
			return propose.Run(os.Stdout, opts, claude.Propose)
		},
	}

	proposeFlags := proposeCmd.Flags()
	proposeFlags.StringSliceVar(&proposeCfg.PatternDirs, "patterns", nil, "Additional pattern sources: local dirs, github:owner/repo[/sub][@ref], or git+https://...[#ref[:sub]]")
	proposeFlags.BoolVar(&proposeCfg.NoRepoPatterns, "no-repo-patterns", false, "Ignore repo-specific patterns")
	proposeFlags.BoolVar(&proposeCfg.NoLocalPatterns, "no-local-patterns", false, "Ignore local patterns from the tool")
	proposeFlags.BoolVar(&proposeCfg.NoCache, "no-cache", false, "Ignore cache, force a fresh analysis")
	proposeFlags.DurationVar(&proposeCfg.CacheMaxAge, "cache-max-age", cache.DefaultMaxAge, "Reject cached entries older than this duration (0 disables the TTL)")
	proposeFlags.StringVar(&proposeCfg.Format, "format", "markdown", "Output format (markdown, json, issues)")
	proposeFlags.IntVar(&proposeCfg.MaxPatterns, "max-patterns", patterns.DefaultMaxPatternsInPrompt, "Max review patterns injected into the prompt (<=0 disables truncation, env: "+envMaxPatterns+")")
	proposeFlags.BoolVar(&proposeCfg.CreateIssues, "create-issues", false, "Interactively create GitHub issues from proposals")
	proposeFlags.BoolVar(&proposeCfg.NoIssueDedupe, "no-issue-dedupe", false, "Do not filter proposals whose title matches an existing GitHub issue")

	rootCmd.AddCommand(proposeCmd)

	// audit subcommand
	var auditCfg cli.AuditConfig
	var auditMinSeverity string
	var auditIssueMinSeverity string

	auditCmd := &cobra.Command{
		Use:   "audit <repo-ref>",
		Short: "Apply all known review patterns to an entire codebase",
		Long: `Clone a GitHub repository and apply every loaded review pattern to the
entire current state of the codebase. Produces concrete, prioritized
improvement findings (blocking/critical/warning/info) with file paths,
line numbers, and suggested fixes — identical finding format to the
review command, but analyzing the whole repo instead of a PR diff.

Repository reference can be a URL (https://github.com/owner/repo)
or short form (owner/repo).`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			auditCfg.RepoRef = args[0]

			fileCfg.ApplyAudit(&auditCfg, &auditMinSeverity, &auditIssueMinSeverity, cmd.Flags().Changed)

			maxPatterns, err := resolveMaxPatterns(auditCfg.MaxPatterns, cmd.Flags().Changed("max-patterns"), fileCfg.Audit.MaxPatterns)
			if err != nil {
				return err
			}
			auditCfg.MaxPatterns = maxPatterns

			if auditMinSeverity != "" {
				sev, err := report.ParseSeverity(auditMinSeverity)
				if err != nil {
					return err
				}
				auditCfg.MinSeverity = sev
			} else {
				auditCfg.MinSeverity = report.SeverityInfo
			}

			switch auditCfg.Format {
			case formatMarkdown, formatJSON:
			default:
				return fmt.Errorf("unknown format %q, supported: markdown, json", auditCfg.Format)
			}

			if auditIssueMinSeverity != "" {
				sev, err := report.ParseSeverity(auditIssueMinSeverity)
				if err != nil {
					return err
				}
				auditCfg.IssueMinSeverity = sev
			} else {
				auditCfg.IssueMinSeverity = report.SeverityWarning
			}

			if auditCfg.CreateIssues && auditCfg.Format == formatJSON {
				return fmt.Errorf("--create-issues cannot be used with --format json")
			}

			opts := auditCfg.ToAuditOptions(version)
			return audit.Run(os.Stdout, opts, claude.Audit)
		},
	}

	auditFlags := auditCmd.Flags()
	auditFlags.StringSliceVar(&auditCfg.PatternDirs, "patterns", nil, "Additional pattern sources: local dirs, github:owner/repo[/sub][@ref], or git+https://...[#ref[:sub]]")
	auditFlags.StringVar(&auditMinSeverity, "min-severity", "", "Minimum severity level (info, warning, critical, blocking)")
	auditFlags.BoolVar(&auditCfg.NoRepoPatterns, "no-repo-patterns", false, "Ignore repo-specific patterns")
	auditFlags.BoolVar(&auditCfg.NoLocalPatterns, "no-local-patterns", false, "Ignore local patterns from the tool")
	auditFlags.BoolVar(&auditCfg.NoCache, "no-cache", false, "Ignore cache, force a fresh audit")
	auditFlags.DurationVar(&auditCfg.CacheMaxAge, "cache-max-age", cache.DefaultMaxAge, "Reject cached entries older than this duration (0 disables the TTL)")
	auditFlags.StringVar(&auditCfg.Format, "format", "markdown", "Output format (markdown, json)")
	auditFlags.IntVar(&auditCfg.MaxPatterns, "max-patterns", patterns.DefaultMaxPatternsInPrompt, "Max review patterns injected into the prompt (<=0 disables truncation, env: "+envMaxPatterns+")")
	auditFlags.IntVar(&auditCfg.MaxFindings, "max-findings", 0, "Cap on findings returned (<=0 disables cap)")
	auditFlags.BoolVar(&auditCfg.CreateIssues, "create-issues", false, "Interactively create GitHub issues from audit findings")
	auditFlags.StringVar(&auditIssueMinSeverity, "issue-min-severity", "", "Minimum severity for issue creation (default WARNING)")
	auditFlags.BoolVar(&auditCfg.NoIssueDedupe, "no-issue-dedupe", false, "Do not filter findings whose title matches an existing GitHub issue")

	rootCmd.AddCommand(auditCmd)

	// elaborate subcommand: turn a high-level issue (typically the output of
	// propose or audit) into a deeply detailed engineering plan grounded in
	// the actual repository state.
	var elaborateCfg cli.ElaborateConfig

	elaborateCmd := &cobra.Command{
		Use:   "elaborate <issue-ref>",
		Short: "Expand an existing GitHub issue into a detailed engineering plan",
		Long: `Fetch a GitHub issue, clone the repository, and ask Claude to expand
the issue into a deeply detailed engineering plan with Description,
Motivation, Affected Areas, Acceptance Criteria, Non-Goals, and References
sections — grounded in concrete files and symbols from the repo.

Issue reference can be a URL (https://github.com/owner/repo/issues/123)
or short form (owner/repo#123).`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			elaborateCfg.IssueRef = args[0]

			maxPatterns, err := resolveMaxPatterns(elaborateCfg.MaxPatterns, cmd.Flags().Changed("max-patterns"), nil)
			if err != nil {
				return err
			}
			elaborateCfg.MaxPatterns = maxPatterns

			switch elaborateCfg.Format {
			case formatMarkdown, formatJSON:
			default:
				return fmt.Errorf("unknown format %q, supported: markdown, json", elaborateCfg.Format)
			}

			if elaborateCfg.UpdateIssue && elaborateCfg.PostComment {
				return fmt.Errorf("--update-issue and --post-comment are mutually exclusive")
			}

			opts := elaborateCfg.ToElaborateOptions(version)
			return elaborate.Run(os.Stdout, opts, claude.Elaborate)
		},
	}

	elaborateFlags := elaborateCmd.Flags()
	elaborateFlags.StringSliceVar(&elaborateCfg.PatternDirs, "patterns", nil, "Additional pattern sources: local dirs, github:owner/repo[/sub][@ref], or git+https://...[#ref[:sub]]")
	elaborateFlags.BoolVar(&elaborateCfg.NoRepoPatterns, "no-repo-patterns", false, "Ignore repo-specific patterns")
	elaborateFlags.BoolVar(&elaborateCfg.NoLocalPatterns, "no-local-patterns", false, "Ignore local patterns from the tool")
	elaborateFlags.BoolVar(&elaborateCfg.NoCache, "no-cache", false, "Ignore cache, force a fresh elaboration")
	elaborateFlags.DurationVar(&elaborateCfg.CacheMaxAge, "cache-max-age", cache.DefaultMaxAge, "Reject cached entries older than this duration (0 disables the TTL)")
	elaborateFlags.StringVar(&elaborateCfg.Format, "format", "markdown", "Output format (markdown, json)")
	elaborateFlags.IntVar(&elaborateCfg.MaxPatterns, "max-patterns", patterns.DefaultMaxPatternsInPrompt, "Max review patterns injected into the prompt (<=0 disables truncation, env: "+envMaxPatterns+")")
	elaborateFlags.BoolVar(&elaborateCfg.UpdateIssue, "update-issue", false, "Replace the issue body with the elaborated body via gh issue edit")
	elaborateFlags.BoolVar(&elaborateCfg.PostComment, "post-comment", false, "Post the elaborated body as a new issue comment via gh issue comment")

	rootCmd.AddCommand(elaborateCmd)

	// prompt subcommand: deterministically render a copy-paste-ready Claude
	// Code prompt from an existing GitHub issue (typically an audit finding
	// or an elaborated proposal). No Claude call — pure prompt assembly.
	var promptCfg cli.PromptConfig

	promptCmd := &cobra.Command{
		Use:   "prompt <issue-ref>",
		Short: "Generate a Claude Code prompt that fixes or implements an issue",
		Long: `Fetch a GitHub issue and emit a copy-paste-ready prompt for Claude
Code (or another AI agent) to fix or implement it.

Mode is auto-detected from the issue body: issues whose body carries the
audit severity marker ("**Severity**:") get the "fix" prompt; everything
else gets the "implement" prompt. Override with --mode.

Issue reference can be a URL (https://github.com/owner/repo/issues/123)
or short form (owner/repo#123).`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			promptCfg.IssueRef = args[0]
			switch promptCfg.Mode {
			case "auto", "fix", "implement":
			default:
				return fmt.Errorf("unknown mode %q, supported: auto, fix, implement", promptCfg.Mode)
			}
			opts := promptCfg.ToPromptOptions(version)
			return prompt.Run(os.Stdout, opts)
		},
	}
	promptCmd.Flags().StringVar(&promptCfg.Mode, "mode", "auto", "Prompt variant (auto, fix, implement)")

	rootCmd.AddCommand(promptCmd)

	// fix subcommand: drive a self-healing loop that watches a PR's checks,
	// dispatches a fresh Claude session to repair failures, then waits for
	// the new commit's checks to come back. The loop continues until every
	// check is green or --max-iterations is exhausted.
	var fixCfg cli.FixConfig

	fixCmd := &cobra.Command{
		Use:   "fix <pr-ref>",
		Short: "Loop on a PR's failing CI checks until they all pass",
		Long: `Watch a GitHub pull request's CI checks and, when one fails, dispatch a
fresh Claude Code session to apply a minimal-invasive fix and push it as a
follow-up commit. After each push the loop waits for the new commit's checks
to complete; if any still fail the loop starts over. Continues until every
check is green or --max-iterations is hit.

Status checks are queried directly via the GitHub API (gh CLI) — Claude is
only invoked when an actual fix is needed.

PR reference can be a URL (https://github.com/owner/repo/pull/123)
or short form (owner/repo#123).`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			fixCfg.PRRef = args[0]
			if fixCfg.PollInterval <= 0 {
				return fmt.Errorf("--interval must be > 0, got %s", fixCfg.PollInterval)
			}
			if fixCfg.MaxIterations <= 0 {
				return fmt.Errorf("--max-iterations must be > 0, got %d", fixCfg.MaxIterations)
			}
			opts := fixCfg.ToFixOptions(version)
			return fix.Run(cmd.OutOrStdout(), opts, claude.Fix)
		},
	}

	fixFlags := fixCmd.Flags()
	fixFlags.DurationVar(&fixCfg.PollInterval, "interval", fix.DefaultPollInterval, "Polling interval between check-status queries")
	fixFlags.IntVar(&fixCfg.MaxIterations, "max-iterations", fix.DefaultMaxIterations, "Maximum number of fix attempts before giving up")
	fixFlags.BoolVar(&fixCfg.Interactive, "interactive", false, "Ask before starting each new fix iteration (after the first)")
	fixFlags.BoolVar(&fixCfg.DryRun, "dry-run", false, "Report failing checks but do not invoke Claude or commit")

	rootCmd.AddCommand(fixCmd)

	// cache subcommand group: visibility into the on-disk cache. The existing
	// top-level --cache-stats / --cache-inspect flags remain for compatibility;
	// these subcommands are the preferred entry points.
	cacheCmd := &cobra.Command{
		Use:   "cache",
		Short: "Inspect and manage cached review/propose/audit results",
		Long: `Inspect and manage planwerk-review's on-disk cache.

Cached entries are keyed by repo + HEAD SHA + flags and are written under the
user cache directory. Use "cache stats" for an overview and "cache inspect
<key>" to dump a single entry.`,
	}

	cacheStatsCmd := &cobra.Command{
		Use:   "stats",
		Short: "Show cache size, age distribution, and per-command breakdown",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCacheStats(cmd.OutOrStdout())
		},
	}

	cacheInspectCmd := &cobra.Command{
		Use:   "inspect <key>",
		Short: "Print metadata and payload for a single cache key",
		Long: `Print the metadata (command, writtenAt, age, size) and pretty-printed
payload for a single cache entry. Keys are listed by "cache stats".`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCacheInspect(cmd.OutOrStdout(), args[0])
		},
	}

	cacheCmd.AddCommand(cacheStatsCmd, cacheInspectCmd)
	rootCmd.AddCommand(cacheCmd)

	// gen-man-pages: hidden helper used by release tooling to emit man pages.
	genManCmd := &cobra.Command{
		Use:    "gen-man-pages <dir>",
		Short:  "Generate man pages into the given directory",
		Args:   cobra.ExactArgs(1),
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := args[0]
			if err := os.MkdirAll(dir, 0o750); err != nil {
				return err
			}
			header := &doc.GenManHeader{
				Title:   "PLANWERK-REVIEW",
				Section: "1",
				Source:  "planwerk-review " + version,
			}
			return doc.GenManTree(rootCmd, header, dir)
		},
	}
	rootCmd.AddCommand(genManCmd)

	if err := rootCmd.Execute(); err != nil {
		// Route the final error through slog so it honors --log-format.
		slog.Error(err.Error())
		os.Exit(1)
	}
}
