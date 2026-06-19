package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/planwerk/planwerk-review/internal/cache"
	"github.com/planwerk/planwerk-review/internal/cli"
	"github.com/planwerk/planwerk-review/internal/elaborate"
	"github.com/planwerk/planwerk-review/internal/patterns"
)

// newElaborateCmd builds the "elaborate" subcommand: turn a high-level issue
// (typically the output of propose or audit) into a deeply detailed
// engineering plan grounded in the actual repository state.
func newElaborateCmd(deps *runtimeDeps) *cobra.Command {
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

			opts := elaborateCfg.ToElaborateOptions(deps.version)
			opts.Remote = deps.remoteOpts
			return elaborate.Run(os.Stdout, opts, deps.claude.Elaborate, deps.claude.ReviewElaboration)
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
	elaborateFlags.BoolVar(&elaborateCfg.Review, "review", false, "Run a reviewer pass that checks the draft for executability and refines it to close gaps before output")
	elaborateFlags.IntVar(&elaborateCfg.MaxReviewIterations, "max-review-iterations", 0, "Cap on reviewer refine iterations when --review is set (<=0 uses the default of 3)")
	elaborateFlags.BoolVar(&elaborateCfg.Local, "local", false, "Operate on the current working directory instead of cloning into a temp dir")
	elaborateFlags.BoolVar(&elaborateCfg.Force, "force", false, "With --local, skip the confirmation prompt when the working tree is dirty")

	return elaborateCmd
}
