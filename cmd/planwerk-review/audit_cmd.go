package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/planwerk/planwerk-review/internal/audit"
	"github.com/planwerk/planwerk-review/internal/cache"
	"github.com/planwerk/planwerk-review/internal/cli"
	"github.com/planwerk/planwerk-review/internal/patterns"
	"github.com/planwerk/planwerk-review/internal/report"
)

// newAuditCmd builds the "audit" subcommand: clone a GitHub repository and
// apply every loaded review pattern to the entire current state of the
// codebase, producing prioritized improvement findings.
func newAuditCmd(deps *runtimeDeps) *cobra.Command {
	var auditCfg cli.AuditConfig
	var auditMinSeverity string
	var auditMinConfidence string
	var auditIssueMinSeverity string
	var wikiEnable, wikiDisable bool
	var wikiRef string

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
		Args: cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				auditCfg.RepoRef = args[0]
			} else if !auditCfg.Local {
				return fmt.Errorf("requires a repository reference argument (or use --local)")
			}

			deps.fileCfg.ApplyAudit(&auditCfg, &auditMinSeverity, &auditIssueMinSeverity, cmd.Flags().Changed)

			maxPatterns, err := resolveMaxPatterns(auditCfg.MaxPatterns, cmd.Flags().Changed("max-patterns"), deps.fileCfg.Audit.MaxPatterns)
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

			if auditMinConfidence != "" {
				conf, err := report.ParseConfidence(auditMinConfidence)
				if err != nil {
					return err
				}
				auditCfg.MinConfidence = conf
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

			auditCfg.CaptureWiki = resolveCaptureWiki(auditCfg.CaptureWiki, cmd.Flags().Changed("capture-wiki"), deps.fileCfg.Capture)

			// Under --format json the capture render — including the interactive
			// write confirmation — is discarded so stdout stays valid JSON. An
			// interactive --capture-wiki run would then block on an invisible
			// prompt. Require --yes (which skips the prompt) for the non-interactive
			// CI combination, mirroring the --create-issues guard above.
			if auditCfg.CaptureWiki && auditCfg.Format == formatJSON && !auditCfg.Yes {
				return fmt.Errorf("--capture-wiki cannot be used with --format json without --yes: the wiki write confirmation cannot be shown alongside JSON output")
			}

			opts := auditCfg.ToAuditOptions(deps.version)
			opts.Remote = deps.remoteOpts
			opts.Wiki = resolveWikiOptions(wikiEnable, wikiDisable, cmd.Flags().Changed("wiki"), cmd.Flags().Changed("no-wiki"), wikiRef, cmd.Flags().Changed("wiki-ref"), deps.fileCfg.Wiki)
			return audit.Run(os.Stdout, opts, deps.claude.Audit, deps.claude)
		},
	}

	auditFlags := auditCmd.Flags()
	auditFlags.StringSliceVar(&auditCfg.PatternDirs, "patterns", nil, "Additional pattern sources: local dirs, github:owner/repo[/sub][@ref], or git+https://...[#ref[:sub]]")
	auditFlags.StringVar(&auditMinSeverity, "min-severity", "", "Minimum severity level (info, warning, critical, blocking)")
	auditFlags.StringVar(&auditMinConfidence, "min-confidence", "", "Minimum confidence shown in the main report (verified, likely, uncertain); findings below the threshold are filtered out, and uncertain low-severity findings otherwise move to an Unverified section")
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
	auditFlags.BoolVar(&auditCfg.Local, "local", false, "Operate on the current working directory instead of cloning into a temp dir")
	auditFlags.BoolVar(&auditCfg.Force, "force", false, "With --local, skip the confirmation prompt when the working tree is dirty")
	auditFlags.BoolVar(&auditCfg.NoCapture, "no-capture", false, "Skip the read-only capture pass that proposes new wiki review patterns from the audit findings (only runs with --wiki; writes nothing)")
	auditFlags.BoolVar(&auditCfg.CaptureWiki, "capture-wiki", false, "Push the accepted capture pages to the wiki instead of only proposing them (off by default — a normal run is propose-only; confirms first, refuses a non-TTY run without --yes; env: "+envCaptureWiki+")")
	auditFlags.BoolVar(&auditCfg.Yes, "yes", false, "Skip the --capture-wiki write confirmation prompt (for a non-interactive write)")
	addWikiFlags(auditFlags, &wikiEnable, &wikiDisable, &wikiRef)

	return auditCmd
}
