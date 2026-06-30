package main

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/planwerk/planwerk-agent/internal/attribution"
	"github.com/planwerk/planwerk-agent/internal/cache"
	"github.com/planwerk/planwerk-agent/internal/claude"
	"github.com/planwerk/planwerk-agent/internal/cli"
	"github.com/planwerk/planwerk-agent/internal/logging"
	"github.com/planwerk/planwerk-agent/internal/patterns"
	"github.com/planwerk/planwerk-agent/internal/report"
	"github.com/planwerk/planwerk-agent/internal/review"
)

// newRootCmd builds the root command. The root command is the review command:
// it analyzes a single GitHub PR (or the local working tree with --local). It
// also owns the persistent flags, the env-var resolution done in
// PersistentPreRunE, and the top-level cache-maintenance flags shared by every
// subcommand.
func newRootCmd(deps *runtimeDeps) *cobra.Command {
	var cfg cli.Config
	var minSeverity string
	var minConfidence string
	var showVersion, verbose bool
	var showClaudeOutput bool
	var logFormat string
	var remotePatternsTTL time.Duration
	var claudeTimeout time.Duration
	var claudeModel string
	var claudeEffort string
	var structureModel string
	var structureEffort string
	var claudeInheritUserConfig bool
	var wikiEnable, wikiDisable bool
	var wikiRef string

	rootCmd := &cobra.Command{
		Use:   "planwerk-agent <pr-ref>",
		Short: "AI-powered code review for GitHub Pull Requests",
		Long: `planwerk-agent uses Claude Code to analyze GitHub PR changes and produces
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
			deps.fileCfg = loaded

			ttl, err := resolveRemotePatternsTTL(remotePatternsTTL, cmd.Flags().Changed("remote-patterns-ttl"))
			if err != nil {
				return err
			}
			deps.remoteOpts = patterns.RemoteOptions{TTL: ttl}

			timeout, err := resolveClaudeTimeout(claudeTimeout, cmd.Flags().Changed("claude-timeout"))
			if err != nil {
				return err
			}

			// Validate the structuring effort up front: it gates only the late
			// structuring call, so a typo must be rejected here (before any
			// claude call) rather than after the expensive reasoning pass.
			structEffort, err := resolveStructureEffort(structureEffort, cmd.Flags().Changed("structure-effort"))
			if err != nil {
				return err
			}

			// Build the Claude Code client once from the resolved --claude-*
			// flags and share it with every subcommand via deps. The implement
			// command appends its --plan-* options to deps.claudeOpts.
			deps.claudeOpts = []claude.Option{
				claude.WithTimeout(timeout),
				claude.WithShowOutput(resolveShowClaudeOutput(showClaudeOutput, cmd.Flags().Changed("show-claude-output"))),
				claude.WithModel(resolveClaudeModel(claudeModel, cmd.Flags().Changed("claude-model"))),
				claude.WithEffort(resolveClaudeEffort(claudeEffort, cmd.Flags().Changed("claude-effort"))),
				claude.WithStructureModel(resolveStructureModel(structureModel, cmd.Flags().Changed("structure-model"))),
				claude.WithStructureEffort(structEffort),
				claude.WithInheritUserConfig(resolveClaudeInheritUserConfig(claudeInheritUserConfig, cmd.Flags().Changed("claude-inherit-user-config"))),
			}
			deps.claude = claude.NewClient(deps.claudeOpts...)

			// Record the build version so every attribution footer names the
			// exact planwerk-agent build, matching the report headers and the
			// `--version` output. Same value threaded into every command's
			// options (deps.version).
			attribution.SetVersion(deps.version)
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if showVersion {
				writeVersion(cmd.OutOrStdout(), resolveBuildInfo(deps.version), verbose)
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

			if len(args) == 1 {
				cfg.PRRef = args[0]
			} else if !cfg.Local {
				return fmt.Errorf("requires a PR reference argument (or use --local)")
			}

			deps.fileCfg.ApplyReview(&cfg, &minSeverity, cmd.Flags().Changed)

			maxPatterns, err := resolveMaxPatterns(cfg.MaxPatterns, cmd.Flags().Changed("max-patterns"), deps.fileCfg.Review.MaxPatterns)
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

			if minConfidence != "" {
				conf, err := report.ParseConfidence(minConfidence)
				if err != nil {
					return err
				}
				cfg.MinConfidence = conf
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

			cfg.CaptureWiki = resolveCaptureWiki(cfg.CaptureWiki, cmd.Flags().Changed("capture-wiki"), deps.fileCfg.Capture)
			opts := cfg.ToReviewOptions(deps.version)
			opts.Remote = deps.remoteOpts
			opts.Wiki = resolveWikiOptions(wikiEnable, wikiDisable, cmd.Flags().Changed("wiki"), cmd.Flags().Changed("no-wiki"), wikiRef, cmd.Flags().Changed("wiki-ref"), deps.fileCfg.Wiki)
			return review.Run(os.Stdout, opts, deps.claude)
		},
	}

	persistent := rootCmd.PersistentFlags()
	persistent.BoolVarP(&verbose, "verbose", "v", false, "Enable debug-level logging (and verbose build info with --version)")
	persistent.StringVar(&logFormat, "log-format", "text", "Log output format (text, json)")
	persistent.DurationVar(&remotePatternsTTL, "remote-patterns-ttl", patterns.DefaultRemoteTTL, "Refresh interval for remote pattern sources (env: "+envRemotePatternsTTL+"; <=0 disables refresh once cached)")
	persistent.DurationVar(&claudeTimeout, "claude-timeout", claude.DefaultClaudeTimeout, "Maximum duration for a single Claude Code invocation (env: "+envClaudeTimeout+"; must be > 0)")
	persistent.BoolVar(&showClaudeOutput, "show-claude-output", false, "Stream Claude Code's live output to stderr while running (env: "+envShowClaudeOutput+")")
	persistent.StringVar(&claudeModel, "claude-model", claude.DefaultClaudeModel, "Model passed to Claude Code via --model (e.g. opus, fable, sonnet; env: "+envClaudeModel+")")
	persistent.StringVar(&claudeEffort, "claude-effort", claude.DefaultClaudeEffort, "Reasoning effort passed to Claude Code via --effort (low, medium, high, xhigh, max; env: "+envClaudeEffort+")")
	persistent.StringVar(&structureModel, "structure-model", claude.DefaultStructureModel, "Model for the mechanical JSON-structuring passes (independent of --claude-model; the cheap tier that transcribes reasoned prose into the report schema; env: "+envStructureModel+")")
	persistent.StringVar(&structureEffort, "structure-effort", claude.DefaultStructureEffort, "Reasoning effort for the JSON-structuring passes (low, medium, high, xhigh, max; env: "+envStructureEffort+")")
	persistent.BoolVar(&claudeInheritUserConfig, "claude-inherit-user-config", false, "Let orchestrated Claude sessions inherit your user-global ~/.claude settings and MCP servers (default: isolated for reproducible output; env: "+envClaudeInheritUserConfig+")")

	flags := rootCmd.Flags()
	flags.StringSliceVar(&cfg.PatternDirs, "patterns", nil, "Additional pattern sources: local dirs, github:owner/repo[/sub][@ref], or git+https://...[#ref[:sub]]")
	flags.StringVar(&minSeverity, "min-severity", "", "Minimum severity level (info, warning, critical, blocking)")
	flags.StringVar(&minConfidence, "min-confidence", "", "Minimum confidence shown in the main report (verified, likely, uncertain); findings below the threshold are filtered out, and uncertain low-severity findings otherwise move to an Unverified section")
	flags.BoolVar(&cfg.NoRepoPatterns, "no-repo-patterns", false, "Ignore repo-specific patterns")
	flags.BoolVar(&cfg.NoLocalPatterns, "no-local-patterns", false, "Ignore local patterns from the tool")
	flags.BoolVar(&cfg.NoCache, "no-cache", false, "Ignore cache, force a fresh review")
	flags.BoolVar(&cfg.ClearCache, "clear-cache", false, "Clear cached reviews and exit (honors --clear-cache-scope)")
	flags.StringVar(&cfg.ClearCacheScope, "clear-cache-scope", "", "Restrict --clear-cache to a single command (review, propose, audit, glossary, elaborate, gap-analysis, review-prepared)")
	flags.BoolVar(&cfg.CacheStats, "cache-stats", false, "Show cache size, age distribution, and per-command breakdown, then exit")
	flags.StringVar(&cfg.CacheInspect, "cache-inspect", "", "Print the metadata and payload for the given cache key, then exit")
	flags.DurationVar(&cfg.CacheMaxAge, "cache-max-age", cache.DefaultMaxAge, "Reject cached entries older than this duration (0 disables the TTL)")
	flags.StringVar(&cfg.Format, "format", "markdown", "Output format (markdown, json)")
	flags.BoolVar(&cfg.PostReview, "post-review", false, "Post the review as a comment on the PR")
	flags.BoolVar(&cfg.InlineReview, "inline", false, "Post review with inline comments using GitHub Review API (implies --post-review)")
	flags.BoolVar(&cfg.Thorough, "thorough", false, "Run additional adversarial review pass")
	flags.BoolVar(&cfg.Specialists, "specialists", false, "Run the domain-specialist review fan-out (security, data-migration, testing, performance, api-contract, maintainability) concurrently and merge their findings")
	flags.BoolVar(&cfg.CoverageMap, "coverage-map", false, "Generate test coverage map for changed functions")
	flags.IntVar(&cfg.MaxPatterns, "max-patterns", patterns.DefaultMaxPatternsInPrompt, "Max review patterns injected into the prompt (<=0 disables truncation, env: "+envMaxPatterns+")")
	flags.IntVar(&cfg.MaxFindings, "max-findings", 0, "Cap on findings returned (<=0 disables cap)")
	flags.BoolVar(&cfg.Local, "local", false, "Operate on the current working directory instead of cloning into a temp dir")
	flags.BoolVar(&cfg.Force, "force", false, "With --local, skip the confirmation prompt when the working tree is dirty")
	flags.BoolVar(&cfg.NoCapture, "no-capture", false, "Skip the read-only capture pass that proposes new wiki review patterns from the review findings (only runs with --wiki; writes nothing)")
	flags.BoolVar(&cfg.CaptureWiki, "capture-wiki", false, "Ignored by review: a review analyzes an untrusted pull request, so its capture pass is always propose-only and never pushes to the wiki. Capture pattern pages from a trusted source instead (implement or audit; env: "+envCaptureWiki+")")
	flags.BoolVar(&cfg.Yes, "yes", false, "Skip the --capture-wiki write confirmation prompt (for a non-interactive write)")
	addWikiFlags(flags, &wikiEnable, &wikiDisable, &wikiRef)
	flags.BoolVar(&showVersion, "version", false, "Show version information and exit")

	return rootCmd
}
