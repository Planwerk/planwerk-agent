package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/planwerk/planwerk-review/internal/cache"
	"github.com/planwerk/planwerk-review/internal/claude"
	"github.com/planwerk/planwerk-review/internal/cli"
	"github.com/planwerk/planwerk-review/internal/patterns"
	"github.com/planwerk/planwerk-review/internal/propose"
)

// newProposeCmd builds the "propose" subcommand: analyze a GitHub repository in
// depth and generate concrete, actionable feature proposals as structured
// Markdown suitable for GitHub issues.
func newProposeCmd(deps *runtimeDeps) *cobra.Command {
	var proposeCfg cli.ProposeConfig

	proposeCmd := &cobra.Command{
		Use:   "propose <repo-ref>",
		Short: "Analyze a codebase and generate feature proposals",
		Long: `Analyze a GitHub repository in depth and generate concrete, actionable
feature proposals as structured Markdown suitable for GitHub issues.

Repository reference can be a URL (https://github.com/owner/repo)
or short form (owner/repo).`,
		Args: cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				proposeCfg.RepoRef = args[0]
			} else if !proposeCfg.Local {
				return fmt.Errorf("requires a repository reference argument (or use --local)")
			}

			deps.fileCfg.ApplyPropose(&proposeCfg, cmd.Flags().Changed)

			maxPatterns, err := resolveMaxPatterns(proposeCfg.MaxPatterns, cmd.Flags().Changed("max-patterns"), deps.fileCfg.Propose.MaxPatterns)
			if err != nil {
				return err
			}
			proposeCfg.MaxPatterns = maxPatterns

			switch proposeCfg.Format {
			case formatMarkdown, formatJSON, formatIssues:
			default:
				return fmt.Errorf("unknown format %q, supported: markdown, json, issues", proposeCfg.Format)
			}

			opts := proposeCfg.ToProposeOptions(deps.version)
			opts.Remote = deps.remoteOpts
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
	proposeFlags.BoolVar(&proposeCfg.Local, "local", false, "Operate on the current working directory instead of cloning into a temp dir")
	proposeFlags.BoolVar(&proposeCfg.Force, "force", false, "With --local, skip the confirmation prompt when the working tree is dirty")

	return proposeCmd
}
