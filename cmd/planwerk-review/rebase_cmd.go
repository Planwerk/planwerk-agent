package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/planwerk/planwerk-review/internal/claude"
	"github.com/planwerk/planwerk-review/internal/cli"
	"github.com/planwerk/planwerk-review/internal/patterns"
	"github.com/planwerk/planwerk-review/internal/rebase"
)

// newRebaseCmd builds the "rebase" subcommand: rebase a PR's branch onto a base
// branch, resolving conflicts with Claude rather than a naive ours/theirs pick,
// then analyze the rebased commits against the upstream range that entered the
// base since the PR forked. History is force-pushed only when --push is given.
func newRebaseCmd(deps *runtimeDeps) *cobra.Command {
	var rebaseCfg cli.RebaseConfig

	rebaseCmd := &cobra.Command{
		Use:   "rebase <pr-ref>",
		Short: "Rebase a PR onto a base branch, resolve conflicts, and analyze the result",
		Long: `Rebase a pull request's branch onto a base branch (--onto, default main),
resolving any rebase conflicts semantically with Claude rather than with a
naive ours/theirs pick. Individual commits are preserved (no squash).

After a clean rebase, a second pass walks the rebased commits one by one and
reports, for each, whether the upstream commits that entered the base since the
PR's original merge-base require an adjustment — even where git produced no
textual conflict (a renamed symbol, a changed signature, a removed helper, a
new lint/format rule, a semantic behavior change). The analysis is report-only
by default (use --apply-adjustments to apply it as fixup commits).

Rewriting history requires a force-push, which is gated behind --push
(git push --force-with-lease) and never happens implicitly.

PR reference can be a URL (https://github.com/owner/repo/pull/123)
or short form (owner/repo#123).`,
		Args: cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				rebaseCfg.PRRef = args[0]
			} else if !rebaseCfg.Local {
				return fmt.Errorf("requires a PR reference argument (or use --local)")
			}
			if rebaseCfg.MaxIterations <= 0 {
				return fmt.Errorf("--max-iterations must be > 0, got %d", rebaseCfg.MaxIterations)
			}
			maxPatterns, err := resolveMaxPatterns(rebaseCfg.MaxPatterns, cmd.Flags().Changed("max-patterns"), nil)
			if err != nil {
				return err
			}
			rebaseCfg.MaxPatterns = maxPatterns
			modes := 0
			if rebaseCfg.DryRun {
				modes++
			}
			if rebaseCfg.PrintPrompt {
				modes++
			}
			if rebaseCfg.PrintBarePrompt {
				modes++
			}
			if modes > 1 {
				return fmt.Errorf("--dry-run, --print-prompt, and --print-bare-prompt are mutually exclusive")
			}
			opts := rebaseCfg.ToRebaseOptions(deps.version)
			opts.Remote = deps.remoteOpts
			if rebaseCfg.PrintBarePrompt {
				return rebase.PrintBarePrompt(cmd.OutOrStdout(), opts, claude.BuildBareRebasePrompt)
			}
			return rebase.Run(cmd.OutOrStdout(), opts,
				claude.ResolveRebaseConflict,
				claude.AnalyzeRebasedCommits,
				claude.BuildRebaseAnalysisPrompt,
				claude.ApplyRebaseAdjustments)
		},
	}

	rebaseFlags := rebaseCmd.Flags()
	rebaseFlags.StringVar(&rebaseCfg.Onto, "onto", rebase.DefaultOnto, "Base branch to rebase onto")
	rebaseFlags.BoolVar(&rebaseCfg.Push, "push", false, "Force-push the rebased branch with --force-with-lease (never done implicitly)")
	rebaseFlags.BoolVar(&rebaseCfg.ApplyAdjustments, "apply-adjustments", false, "Apply the post-rebase analysis as fixup commits instead of only reporting")
	rebaseFlags.IntVar(&rebaseCfg.MaxIterations, "max-iterations", rebase.DefaultMaxIterations, "Maximum number of conflict-resolution iterations before aborting")
	rebaseFlags.BoolVar(&rebaseCfg.NoAnalysis, "no-analysis", false, "Skip the post-rebase commit analysis")
	rebaseFlags.BoolVar(&rebaseCfg.NoAnalysisComment, "no-analysis-comment", false, "Do not post the post-rebase analysis as a comment on the pull request")
	rebaseFlags.BoolVar(&rebaseCfg.DryRun, "dry-run", false, "Show the rebase plan and conflicting commit without resolving, committing, or pushing")
	rebaseFlags.BoolVar(&rebaseCfg.PrintPrompt, "print-prompt", false, "Render the post-rebase analysis prompt to stdout and exit; do not rebase or invoke Claude")
	rebaseFlags.BoolVar(&rebaseCfg.PrintBarePrompt, "print-bare-prompt", false, "Render a self-contained rebase prompt (rebase + conflict resolution + analysis) to stdout and exit; meant to be pasted into a manual Claude session already running inside a checkout of the PR")
	rebaseFlags.StringSliceVar(&rebaseCfg.PatternDirs, "patterns", nil, "Additional pattern sources: local dirs, github:owner/repo[/sub][@ref], or git+https://...[#ref[:sub]]")
	rebaseFlags.BoolVar(&rebaseCfg.NoRepoPatterns, "no-repo-patterns", false, "Ignore repo-specific patterns under .planwerk/review_patterns/ in the target repo")
	rebaseFlags.BoolVar(&rebaseCfg.NoLocalPatterns, "no-local-patterns", false, "Ignore local patterns from the tool")
	rebaseFlags.IntVar(&rebaseCfg.MaxPatterns, "max-patterns", patterns.DefaultMaxPatternsInPrompt, "Max review patterns injected into the prompt (<=0 disables truncation, env: "+envMaxPatterns+")")
	rebaseFlags.BoolVar(&rebaseCfg.Local, "local", false, "Operate on the current working directory instead of cloning into a temp dir")
	rebaseFlags.BoolVar(&rebaseCfg.Force, "force", false, "With --local, skip the confirmation prompt when the working tree is dirty")

	return rebaseCmd
}
