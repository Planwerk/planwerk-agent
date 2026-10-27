package main

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/planwerk/planwerk-agent/internal/attribution"
	"github.com/planwerk/planwerk-agent/internal/cache"
	"github.com/planwerk/planwerk-agent/internal/claude"
	"github.com/planwerk/planwerk-agent/internal/claudeplugin"
	"github.com/planwerk/planwerk-agent/internal/cli"
	"github.com/planwerk/planwerk-agent/internal/logging"
	"github.com/planwerk/planwerk-agent/internal/patterns"
	"github.com/planwerk/planwerk-agent/internal/report"
	"github.com/planwerk/planwerk-agent/internal/review"
)

// mirrorOnlyAnnotation marks a command that reads a local mirror and nothing
// else. The root command's PersistentPreRunE sets up logging for it and stops
// there: it loads no config file, resolves no setting, and builds no Claude
// client. `brain search` carries it, because a session runs that command in
// the checkout under review, whose .planwerk/config.yaml the author of the
// reviewed change wrote: a key this binary does not know would fail every
// search of the session.
const mirrorOnlyAnnotation = "planwerk-agent/mirror-only"

// noSessionAnnotation marks a command that starts no Claude Code session, as
// do its subcommands. The root command's pre-run resolves no --claude-plugin
// for it, so a schema dump, a cache listing, or a mirror sync never depends
// on the claude CLI that resolving a plugin id runs (decision 153).
const noSessionAnnotation = "planwerk-agent/no-session"

// cobraCommands are the commands cobra adds on its own, none of which starts
// a session: help, the completion scripts, and the hidden commands a shell
// calls on every completion request.
var cobraCommands = map[string]bool{
	"help":                          true,
	"completion":                    true,
	cobra.ShellCompRequestCmd:       true,
	cobra.ShellCompNoDescRequestCmd: true,
}

// startsNoSession reports whether cmd, or a command it is nested in, carries
// noSessionAnnotation or is one of cobraCommands.
func startsNoSession(cmd *cobra.Command) bool {
	for c := cmd; c != nil; c = c.Parent() {
		if c.Annotations[noSessionAnnotation] != "" || (c.HasParent() && cobraCommands[c.Name()]) {
			return true
		}
	}
	return false
}

// newRootCmd builds the root command. The root command is the review command:
// it analyzes a single GitHub PR (or the local working tree with --local). It
// also owns the persistent flags, the env-var resolution done in
// PersistentPreRunE, and the top-level cache-maintenance flags shared by every
// subcommand.
func newRootCmd(deps *runtimeDeps) *cobra.Command {
	var cfg review.Options
	var clearCache, cacheStats bool
	var clearCacheScope, cacheInspect string
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
	var finderModel string
	var finderEffort string
	var finderTools string
	var claudeInheritUserConfig bool
	var keepTranscripts bool
	var claudePlugins []string
	var diffExclusions bool
	var wikiEnable, wikiDisable bool
	var wikiRef string
	var brain brainFlags

	rootCmd := &cobra.Command{
		Use:   "planwerk-agent <pr-ref>",
		Short: "AI-powered code review for GitHub Pull Requests",
		Long: `planwerk-agent uses Claude Code to analyze GitHub PR changes and produces
structured, categorized review results as Markdown, JSON, or SARIF output.

Use --dry-run to see what a review would run, and what each pass would be
sent, without a Claude call; --print-prompt renders the review prompt to
stdout.

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
				if cmd.Annotations[mirrorOnlyAnnotation] != "" {
					// A session reads this error: it holds no argument.
					return errors.New("--log-format must be one of text, json")
				}
				return err
			}
			if err := logging.Init(logging.Options{
				Writer:  os.Stderr,
				Format:  format,
				Verbose: verbose,
			}); err != nil {
				return err
			}
			if cmd.Annotations[mirrorOnlyAnnotation] != "" {
				return nil
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
			structEffort, err := resolveEffort(structureEffort, cmd.Flags().Changed("structure-effort"), envStructureEffort, claude.DefaultStructureEffort, false, "--structure-effort")
			if err != nil {
				return err
			}

			// Same reason as the structuring effort: the finder passes run before
			// the passes that act on their findings, so a typo must be caught here
			// rather than after a seven-specialist fan-out has spent its tokens.
			findEffort, err := resolveEffort(finderEffort, cmd.Flags().Changed("finder-effort"), envFinderEffort, claude.DefaultFinderEffort, true, "--finder-effort")
			if err != nil {
				return err
			}
			findTools, err := resolveFinderTools(finderTools, cmd.Flags().Changed("finder-tools"))
			if err != nil {
				return err
			}
			// A command that starts no session, and the root's own version and
			// cache actions, load no plugin and so resolve none.
			var plugins []claudeplugin.Plugin
			if !startsNoSession(cmd) && !showVersion && !cacheStats && cacheInspect == "" && !clearCache && clearCacheScope == "" {
				if plugins, err = resolveClaudePlugins(claudePlugins, cmd.Flags().Changed("claude-plugin"), deps.listPlugins); err != nil {
					return err
				}
			}
			for _, p := range plugins {
				slog.Info("loading a Claude Code plugin into the sessions that edit", "plugin", p.Label(), "dir", p.Dir)
			}

			// Build the Claude Code client once from the resolved --claude-*
			// flags and share it with every subcommand via deps. The implement
			// command appends its --plan-* options to deps.claudeOpts.
			mainModel := resolveString(claudeModel, cmd.Flags().Changed("claude-model"), envClaudeModel, claude.DefaultClaudeModel)
			mainEffort := resolveString(claudeEffort, cmd.Flags().Changed("claude-effort"), envClaudeEffort, claude.DefaultClaudeEffort)
			structModel := resolveString(structureModel, cmd.Flags().Changed("structure-model"), envStructureModel, claude.DefaultStructureModel)
			findModel := resolveString(finderModel, cmd.Flags().Changed("finder-model"), envFinderModel, claude.DefaultFinderModel)
			exclude := resolveBool(diffExclusions, cmd.Flags().Changed("diff-exclusions"), envDiffExclusions)
			deps.claudeOpts = []claude.Option{
				claude.WithTimeout(timeout),
				claude.WithShowOutput(resolveBool(showClaudeOutput, cmd.Flags().Changed("show-claude-output"), envShowClaudeOutput)),
				claude.WithKeepTranscripts(resolveBool(keepTranscripts, cmd.Flags().Changed("keep-transcripts"), envKeepTranscripts)),
				claude.WithModel(mainModel),
				claude.WithEffort(mainEffort),
				claude.WithStructureModel(structModel),
				claude.WithStructureEffort(structEffort),
				claude.WithFinderModel(findModel),
				claude.WithFinderEffort(findEffort),
				claude.WithFinderTools(findTools),
				claude.WithInheritUserConfig(resolveBool(claudeInheritUserConfig, cmd.Flags().Changed("claude-inherit-user-config"), envClaudeInheritUserConfig)),
				claude.WithDiffExclusions(exclude),
				claude.WithPlugins(plugins),
			}
			deps.claude = claude.NewClient(deps.claudeOpts...)

			// Scope every cache key to the tiers that produced the entry. The
			// key answered "same repo, same commit, same command flags?" and
			// served a hit, although a review by sonnet and one by fable are
			// different analyses of the same diff — so re-running with a
			// stronger model returned the weaker one's findings for the whole
			// TTL. The per-command pattern sources are folded in by each
			// command, next to its own flags. The diff exclusions decide which
			// changed files the sessions read, the finder tool list what the
			// finding passes can call, and the plugins, by name and version,
			// what the sessions that edit and the ui specialist draw on. Each
			// is appended only when on or set, so a key without them is the
			// one it was before they existed.
			fingerprint := []string{mainModel, mainEffort, structModel, structEffort, findModel, findEffort}
			if exclude {
				fingerprint = append(fingerprint, "diff-exclusions")
			}
			if len(findTools) > 0 {
				fingerprint = append(fingerprint, "finder-tools="+strings.Join(findTools, ","))
			}
			if len(plugins) > 0 {
				labels := make([]string, len(plugins))
				for i, p := range plugins {
					labels[i] = p.Label()
				}
				fingerprint = append(fingerprint, "plugins="+strings.Join(labels, ","))
			}
			cache.SetRunFingerprint(fingerprint...)

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
			if cacheStats {
				return runCacheStats(cmd.OutOrStdout())
			}
			if cacheInspect != "" {
				return runCacheInspect(cmd.OutOrStdout(), cacheInspect)
			}
			if clearCache || clearCacheScope != "" {
				scope := clearCacheScope
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
			cfg.ScopePatterns = resolveBool(cfg.ScopePatterns, cmd.Flags().Changed("scope-patterns"), envScopePatterns)

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

			if err := validateReviewFormat(cfg); err != nil {
				return err
			}

			// --inline implies --post-review
			if cfg.InlineReview {
				cfg.PostReview = true
			}
			if cfg.ResolveOutdated && !cfg.InlineReview {
				return fmt.Errorf("--resolve-outdated requires --inline")
			}
			// The checks above stay: they describe the run a dry run previews.
			if cfg.DryRun && cfg.PrintPrompt {
				return fmt.Errorf("--dry-run and --print-prompt are mutually exclusive")
			}

			opts := cfg
			opts.Version = deps.version
			opts.Remote = deps.remoteOpts
			opts.Wiki = resolveWikiOptions(wikiEnable, wikiDisable, cmd.Flags().Changed("wiki"), cmd.Flags().Changed("no-wiki"), wikiRef, cmd.Flags().Changed("wiki-ref"), deps.fileCfg.Wiki)
			opts.Brain = brain.resolve(cmd.Flags(), deps.fileCfg.Brain)
			opts.DiffExclusions = deps.claude.DiffExclusions()
			run := review.Run
			if deps.runReview != nil {
				run = deps.runReview
			}
			return run(os.Stdout, opts, deps.claude)
		},
	}

	persistent := rootCmd.PersistentFlags()
	persistent.BoolVarP(&verbose, "verbose", "v", false, "Enable debug-level logging (and verbose build info with --version)")
	persistent.StringVar(&logFormat, "log-format", "text", "Log output format (text, json)")
	persistent.DurationVar(&remotePatternsTTL, "remote-patterns-ttl", patterns.DefaultRemoteTTL, "Refresh interval for remote pattern sources (env: "+envRemotePatternsTTL+"; <=0 disables refresh once cached)")
	persistent.DurationVar(&claudeTimeout, "claude-timeout", claude.DefaultClaudeTimeout, "Maximum duration for a single Claude Code invocation (env: "+envClaudeTimeout+"; must be > 0)")
	persistent.BoolVar(&showClaudeOutput, "show-claude-output", false, "Stream Claude Code's live output to stderr while running (env: "+envShowClaudeOutput+")")
	persistent.BoolVar(&keepTranscripts, "keep-transcripts", false, "Let Claude Code save the transcript of every session under ~/.claude/projects, the read-only ones included (default: only the sessions that edit keep one; env: "+envKeepTranscripts+")")
	persistent.StringVar(&claudeModel, "claude-model", claude.DefaultClaudeModel, "Model passed to Claude Code via --model (e.g. opus, fable, sonnet; env: "+envClaudeModel+")")
	persistent.StringVar(&claudeEffort, "claude-effort", claude.DefaultClaudeEffort, "Reasoning effort passed to Claude Code via --effort (low, medium, high, xhigh, max; env: "+envClaudeEffort+")")
	persistent.StringVar(&structureModel, "structure-model", claude.DefaultStructureModel, "Model for the mechanical JSON-structuring passes (independent of --claude-model; the cheap tier that casts an analysis session's prose into its JSON schema for propose, elaborate, gap-analysis, sync, capture and review-prepared, and runs the JSON repair and dedup calls; brain bootstrap defaults it to "+claude.DefaultBrainStructureModel+"; env: "+envStructureModel+")")
	persistent.StringVar(&structureEffort, "structure-effort", claude.DefaultStructureEffort, "Reasoning effort for the JSON-structuring passes (low, medium, high, xhigh, max; env: "+envStructureEffort+")")
	persistent.StringVar(&finderModel, "finder-model", claude.DefaultFinderModel, "Model for the read-only finder passes: the adversarial pass, the domain specialists, coverage, compliance, the simplify finder, and claim verification (empty inherits --claude-model; env: "+envFinderModel+")")
	persistent.StringVar(&finderEffort, "finder-effort", claude.DefaultFinderEffort, "Reasoning effort for the finder passes (low, medium, high, xhigh, max; empty inherits --claude-effort; env: "+envFinderEffort+")")
	persistent.StringVar(&finderTools, "finder-tools", claude.DefaultFinderTools, "Comma-separated Claude Code built-in tools the seven passes that emit findings load (review, audit, adversarial, specialists, compliance, simplify finder, implementation verifier), e.g. Bash,Glob,Grep,Read,WebFetch,WebSearch; empty keeps Claude Code's default set; Edit, Write and NotebookEdit stay denied either way (env: "+envFinderTools+")")
	persistent.BoolVar(&claudeInheritUserConfig, "claude-inherit-user-config", false, "Let orchestrated Claude sessions inherit your user-global ~/.claude settings and MCP servers (default: isolated for reproducible output; env: "+envClaudeInheritUserConfig+")")
	persistent.StringArrayVar(&claudePlugins, "claude-plugin", nil, "Claude Code plugin the sessions that edit (implement, fix, address, rebase) load, named by its installed id (name@marketplace, as claude plugin list prints it) or its directory; repeat for more than one. Read-only sessions never load one; the impeccable plugin also feeds the ui review specialist (env: "+envClaudePlugins+", comma-separated)")
	persistent.BoolVar(&diffExclusions, "diff-exclusions", false, "Leave lock files, vendored directories, and generated files out of the diff the review and finder passes read, and list them in the report; tests outside vendored directories are never left out (env: "+envDiffExclusions+")")

	flags := rootCmd.Flags()
	flags.StringSliceVar(&cfg.PatternDirs, "patterns", nil, "Additional pattern sources: local dirs, github:owner/repo[/sub][@ref], or git+https://...[#ref[:sub]]")
	flags.StringVar(&minSeverity, "min-severity", "", "Minimum severity level (info, warning, critical, blocking)")
	flags.StringVar(&minConfidence, "min-confidence", "", "Minimum confidence shown in the main report (verified, likely, uncertain); findings below the threshold are filtered out, and uncertain low-severity findings otherwise move to an Unverified section")
	flags.BoolVar(&cfg.NoRepoPatterns, "no-repo-patterns", false, "Ignore repo-specific patterns")
	flags.BoolVar(&cfg.NoLocalPatterns, "no-local-patterns", false, "Ignore local patterns from the tool")
	flags.BoolVar(&cfg.NoCache, "no-cache", false, "Ignore cache, force a fresh review")
	flags.BoolVar(&clearCache, "clear-cache", false, "Clear cached reviews and exit (honors --clear-cache-scope)")
	flags.StringVar(&clearCacheScope, "clear-cache-scope", "", "Restrict --clear-cache to a single command (review, propose, audit, glossary, elaborate, gap-analysis, review-prepared)")
	flags.BoolVar(&cacheStats, "cache-stats", false, "Show cache size, age distribution, and per-command breakdown, then exit")
	flags.StringVar(&cacheInspect, "cache-inspect", "", "Print the metadata and payload for the given cache key, then exit")
	flags.DurationVar(&cfg.CacheMaxAge, "cache-max-age", cache.DefaultMaxAge, "Reject cached entries older than this duration (0 disables the TTL)")
	flags.StringVar(&cfg.Format, "format", "markdown", "Output format (markdown, json, sarif)")
	flags.BoolVar(&cfg.PostReview, "post-review", false, "Post the review as a comment on the PR")
	flags.BoolVar(&cfg.InlineReview, "inline", false, "Post findings as inline comments using the GitHub Review API; the summary goes into the PR comment --post-review keeps up to date (implies --post-review)")
	flags.BoolVar(&cfg.ResolveOutdated, "resolve-outdated", false, "With --inline, resolve the review threads earlier runs opened once GitHub marks them outdated, unless someone replied or a current finding still covers their lines (needs write access to the repository)")
	flags.BoolVar(&cfg.Thorough, "thorough", false, "Run additional adversarial review pass")
	flags.BoolVar(&cfg.Specialists, "specialists", false, "Run the domain-specialist review fan-out (security, data-migration, testing, performance, api-contract, maintainability, ui) concurrently and merge their findings")
	flags.BoolVar(&cfg.CoverageMap, "coverage-map", false, "Generate test coverage map for changed functions")
	flags.IntVar(&cfg.MaxPatterns, "max-patterns", patterns.DefaultMaxPatternsInPrompt, "Max review patterns injected into the prompt (<=0 disables truncation, env: "+envMaxPatterns+")")
	flags.BoolVar(&cfg.ScopePatterns, "scope-patterns", false, "Carry only the review-pattern bodies that apply to every file or whose technology or path matches a changed file and list the rest as an index the review session reads from disk (experimental, off until make eval holds; env: "+envScopePatterns+")")
	flags.IntVar(&cfg.MaxFindings, "max-findings", 0, "Cap on findings returned (<=0 disables cap)")
	flags.IntVar(&cfg.PartitionFiles, "partition-files", 0, "Split a pull request that changes more than N files into partitions of at most N files, grouped by directory and balanced by changed lines, each reviewed by its own concurrent review session; the adversarial pass and the specialists still review the whole pull request (<=0 reviews it in one session)")
	flags.BoolVar(&cfg.Local, "local", false, "Operate on the current working directory instead of cloning into a temp dir")
	flags.BoolVar(&cfg.Force, "force", false, "With --local, skip the confirmation prompt when the working tree is dirty")
	flags.BoolVar(&cfg.NoCapture, "no-capture", false, "Skip the read-only capture pass that proposes new wiki review patterns from the review findings (only runs with --wiki; writes nothing)")
	flags.BoolVar(&cfg.DryRun, "dry-run", false, "Report what the review would run (the PR, head SHA, cache key and whether it hits, technologies, pattern counts, changed files, each pass with the specialist gate's choice and its prompt size) without invoking Claude, writing the cache, or posting")
	flags.BoolVar(&cfg.PrintPrompt, "print-prompt", false, "Render the review prompt (with the pattern bodies) to stdout and exit; do not invoke Claude, write the cache, or post")
	addWikiFlags(flags, &wikiEnable, &wikiDisable, &wikiRef)
	brain.register(flags)
	flags.BoolVar(&showVersion, "version", false, "Show version information and exit")

	return rootCmd
}

// validateReviewFormat rejects a --format the review cannot render and the
// flags that cannot go with it: a posted comment is the Markdown report, so
// json and sarif refuse --post-review and --inline, and a SARIF log has no
// place for the coverage map. A dry run's report and a printed prompt are no
// SARIF log either, so sarif refuses --dry-run and --print-prompt
// (decision 143). It returns nil for every combination it accepts. The caller
// runs it before --inline implies --post-review, so each refusal names the
// flag the user typed (decision 134).
func validateReviewFormat(cfg review.Options) error {
	switch cfg.Format {
	case formatMarkdown:
		return nil
	case formatJSON, formatSARIF:
	default:
		return fmt.Errorf("unknown format %q, supported: markdown, json, sarif", cfg.Format)
	}
	if cfg.PostReview {
		return fmt.Errorf("--post-review cannot be used with --format %s", cfg.Format)
	}
	if cfg.InlineReview {
		return fmt.Errorf("--inline cannot be used with --format %s", cfg.Format)
	}
	if cfg.CoverageMap && cfg.Format == formatSARIF {
		return errors.New("--coverage-map cannot be used with --format sarif: SARIF has no place for the coverage map")
	}
	if cfg.DryRun && cfg.Format == formatSARIF {
		return errors.New("--dry-run cannot be used with --format sarif")
	}
	if cfg.PrintPrompt && cfg.Format == formatSARIF {
		return errors.New("--print-prompt cannot be used with --format sarif")
	}
	return nil
}
