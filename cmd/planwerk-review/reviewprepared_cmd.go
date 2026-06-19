package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/planwerk/planwerk-review/internal/cache"
	"github.com/planwerk/planwerk-review/internal/claude"
	"github.com/planwerk/planwerk-review/internal/cli"
	"github.com/planwerk/planwerk-review/internal/patterns"
	"github.com/planwerk/planwerk-review/internal/report"
	"github.com/planwerk/planwerk-review/internal/reviewprepared"
)

// newReviewPreparedCmd builds the "review-prepared" subcommand: review every
// Planwerk feature spec under .planwerk/features/ whose status is "prepared" —
// surface weaknesses in the spec itself and (with --create-pr) open a pull
// request that rewrites the JSON to address every WARNING-or-higher finding.
func newReviewPreparedCmd(deps *runtimeDeps) *cobra.Command {
	var preparedCfg cli.ReviewPreparedConfig
	var preparedMinSeverity string

	preparedCmd := &cobra.Command{
		Use:   "review-prepared <repo-ref>",
		Short: "Improve prepared Planwerk feature specs (spec-only, no codebase comparison)",
		Long: `Clone a GitHub repository and review every Planwerk feature file under
.planwerk/features/ whose status is "prepared". The command produces a structured
report of weaknesses in the SPEC TEXT (vague acceptance criteria, missing test
specifications, hand-wavy implementation notes, broken internal cross-references)
and — when run with --create-pr — opens a pull request that rewrites each
affected feature JSON to address every WARNING-or-higher finding.

Scope: this command reviews the SPEC ONLY. It does NOT compare the spec to the
codebase, does NOT check whether the described behaviour is implemented, and
does NOT report code-quality issues. For spec-vs-code comparisons on completed
features, use the gap-analysis subcommand instead.

Use --feature PX-NNNN to limit the review to one feature by ID, or --file <path>
to limit it to a single file. The report and PR scope is identical otherwise.

Repository reference can be a URL (https://github.com/owner/repo)
or short form (owner/repo).`,
		Args: cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				preparedCfg.RepoRef = args[0]
			} else if !preparedCfg.Local {
				return fmt.Errorf("requires a repository reference argument (or use --local)")
			}

			maxPatterns, err := resolveMaxPatterns(preparedCfg.MaxPatterns, cmd.Flags().Changed("max-patterns"), nil)
			if err != nil {
				return err
			}
			preparedCfg.MaxPatterns = maxPatterns

			switch preparedCfg.Format {
			case formatMarkdown, formatJSON:
			default:
				return fmt.Errorf("unknown format %q, supported: markdown, json", preparedCfg.Format)
			}

			if preparedMinSeverity != "" {
				sev, err := report.ParseSeverity(preparedMinSeverity)
				if err != nil {
					return err
				}
				preparedCfg.MinSeverity = sev
			} else {
				preparedCfg.MinSeverity = report.SeverityInfo
			}

			if preparedCfg.CreatePR && preparedCfg.Format == formatJSON {
				return fmt.Errorf("--create-pr cannot be used with --format json")
			}

			opts := preparedCfg.ToReviewPreparedOptions(deps.version)
			opts.Remote = deps.remoteOpts
			return reviewprepared.Run(os.Stdout, opts, claude.ReviewPrepared)
		},
	}

	preparedFlags := preparedCmd.Flags()
	preparedFlags.StringSliceVar(&preparedCfg.PatternDirs, "patterns", nil, "Additional pattern sources: local dirs, github:owner/repo[/sub][@ref], or git+https://...[#ref[:sub]]")
	preparedFlags.BoolVar(&preparedCfg.NoRepoPatterns, "no-repo-patterns", false, "Ignore repo-specific patterns")
	preparedFlags.BoolVar(&preparedCfg.NoLocalPatterns, "no-local-patterns", false, "Ignore local patterns from the tool")
	preparedFlags.BoolVar(&preparedCfg.NoCache, "no-cache", false, "Ignore cache, force a fresh review")
	preparedFlags.DurationVar(&preparedCfg.CacheMaxAge, "cache-max-age", cache.DefaultMaxAge, "Reject cached entries older than this duration (0 disables the TTL)")
	preparedFlags.StringVar(&preparedCfg.Format, "format", "markdown", "Output format (markdown, json)")
	preparedFlags.IntVar(&preparedCfg.MaxPatterns, "max-patterns", patterns.DefaultMaxPatternsInPrompt, "Max review patterns injected into the prompt (<=0 disables truncation, env: "+envMaxPatterns+")")
	preparedFlags.StringVar(&preparedMinSeverity, "min-severity", "", "Minimum severity to render (info, warning, critical)")
	preparedFlags.StringVar(&preparedCfg.FeatureID, "feature", "", "Limit review to a single feature by feature_id (e.g. PX-0028)")
	preparedFlags.StringVar(&preparedCfg.FilePath, "file", "", "Limit review to a single feature file under .planwerk/features/ (path or basename)")
	preparedFlags.BoolVar(&preparedCfg.CreatePR, "create-pr", false, "After the review, commit improved feature JSON files on a fresh branch and open a pull request")
	preparedFlags.StringVar(&preparedCfg.PRBranch, "pr-branch", "", "Branch name for --create-pr (default: planwerk-review/improve-prepared-features)")
	preparedFlags.StringVar(&preparedCfg.PRBase, "pr-base", "", "Base branch for --create-pr (default: repo default branch)")
	preparedFlags.BoolVar(&preparedCfg.Local, "local", false, "Operate on the current working directory instead of cloning into a temp dir")
	preparedFlags.BoolVar(&preparedCfg.Force, "force", false, "With --local, skip the confirmation prompt when the working tree is dirty")

	return preparedCmd
}
