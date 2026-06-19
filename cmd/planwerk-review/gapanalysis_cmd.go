package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/planwerk/planwerk-review/internal/cache"
	"github.com/planwerk/planwerk-review/internal/claude"
	"github.com/planwerk/planwerk-review/internal/cli"
	"github.com/planwerk/planwerk-review/internal/gapanalysis"
	"github.com/planwerk/planwerk-review/internal/patterns"
)

// newGapAnalysisCmd builds the "gap-analysis" subcommand: compare completed
// Planwerk feature specs in the target repo against the actual codebase and
// report incomplete implementations as structured gaps. Reuses the audit
// cache, dedupe, and interactive issue-creation infrastructure.
func newGapAnalysisCmd(deps *runtimeDeps) *cobra.Command {
	var gapCfg cli.GapAnalysisConfig

	gapCmd := &cobra.Command{
		Use:   "gap-analysis <repo-ref>",
		Short: "Detect incomplete implementation of completed Planwerk features",
		Long: `Clone a GitHub repository and compare every Planwerk feature file under
.planwerk/completed/ against the actual codebase. Reports gaps where the spec's
acceptance criteria, scenarios, planned tests, or completed tasks are not
visibly implemented in the code.

Use --feature CC-NNNN to limit the analysis to one feature by ID, or --file
<path> to limit it to a single completed feature file. Both filters can also
be combined as a sanity check.

Repository reference can be a URL (https://github.com/owner/repo)
or short form (owner/repo).`,
		Args: cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				gapCfg.RepoRef = args[0]
			} else if !gapCfg.Local {
				return fmt.Errorf("requires a repository reference argument (or use --local)")
			}

			maxPatterns, err := resolveMaxPatterns(gapCfg.MaxPatterns, cmd.Flags().Changed("max-patterns"), nil)
			if err != nil {
				return err
			}
			gapCfg.MaxPatterns = maxPatterns

			switch gapCfg.Format {
			case formatMarkdown, formatJSON:
			default:
				return fmt.Errorf("unknown format %q, supported: markdown, json", gapCfg.Format)
			}

			if gapCfg.CreateIssues && gapCfg.Format == formatJSON {
				return fmt.Errorf("--create-issues cannot be used with --format json")
			}

			opts := gapCfg.ToGapAnalysisOptions(deps.version)
			opts.Remote = deps.remoteOpts
			return gapanalysis.Run(os.Stdout, opts, claude.GapAnalysis)
		},
	}

	gapFlags := gapCmd.Flags()
	gapFlags.StringSliceVar(&gapCfg.PatternDirs, "patterns", nil, "Additional pattern sources: local dirs, github:owner/repo[/sub][@ref], or git+https://...[#ref[:sub]]")
	gapFlags.BoolVar(&gapCfg.NoRepoPatterns, "no-repo-patterns", false, "Ignore repo-specific patterns")
	gapFlags.BoolVar(&gapCfg.NoLocalPatterns, "no-local-patterns", false, "Ignore local patterns from the tool")
	gapFlags.BoolVar(&gapCfg.NoCache, "no-cache", false, "Ignore cache, force a fresh gap analysis")
	gapFlags.DurationVar(&gapCfg.CacheMaxAge, "cache-max-age", cache.DefaultMaxAge, "Reject cached entries older than this duration (0 disables the TTL)")
	gapFlags.StringVar(&gapCfg.Format, "format", "markdown", "Output format (markdown, json)")
	gapFlags.IntVar(&gapCfg.MaxPatterns, "max-patterns", patterns.DefaultMaxPatternsInPrompt, "Max review patterns injected into the prompt (<=0 disables truncation, env: "+envMaxPatterns+")")
	gapFlags.StringVar(&gapCfg.FeatureID, "feature", "", "Limit analysis to a single feature by feature_id (e.g. CC-0042)")
	gapFlags.StringVar(&gapCfg.FilePath, "file", "", "Limit analysis to a single feature file under .planwerk/completed/ (path or basename)")
	gapFlags.BoolVar(&gapCfg.CreateIssues, "create-issues", false, "Interactively create GitHub issues from gaps")
	gapFlags.BoolVar(&gapCfg.NoIssueDedupe, "no-issue-dedupe", false, "Do not filter gaps whose suggested-issue title matches an existing GitHub issue")
	gapFlags.BoolVar(&gapCfg.Local, "local", false, "Operate on the current working directory instead of cloning into a temp dir")
	gapFlags.BoolVar(&gapCfg.Force, "force", false, "With --local, skip the confirmation prompt when the working tree is dirty")

	return gapCmd
}
