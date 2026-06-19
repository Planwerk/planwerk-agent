package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/planwerk/planwerk-review/internal/claude"
	"github.com/planwerk/planwerk-review/internal/cli"
	"github.com/planwerk/planwerk-review/internal/fix"
	"github.com/planwerk/planwerk-review/internal/patterns"
)

// newFixCmd builds the "fix" subcommand: drive a self-healing loop that watches
// a PR's checks, dispatches a fresh Claude session to repair failures, then
// waits for the new commit's checks to come back. The loop continues until
// every check is green or --max-iterations is exhausted.
func newFixCmd(deps *runtimeDeps) *cobra.Command {
	var fixCfg cli.FixConfig

	fixCmd := &cobra.Command{
		Use:   "fix <pr-ref>",
		Short: "Loop on a PR's failing CI checks until they all pass",
		Long: `Watch a GitHub pull request's CI checks and, when one fails, dispatch a
fresh Claude Code session to apply a minimal-invasive fix and publish it. After
each push the loop waits for the new commit's checks to complete; if any still
fail the loop starts over. Continues until every check is green or
--max-iterations is hit.

By default each fix is folded into the branch commit it belongs to
(git commit --fixup + git rebase --autosquash) and published with
git push --force-with-lease, so the branch history stays clean instead of
accumulating "Fix failing CI checks" commits. Pass --no-fixup to append the fix
as a fresh on-top follow-up commit and push without rewriting history.

Status checks are queried directly via the GitHub API (gh CLI) — Claude is
only invoked when an actual fix is needed.

After each iteration the report of what was fixed is also posted back onto the
pull request as a comment (use --no-fix-comment to skip that), so the record of
what each iteration changed lives on the PR itself.

PR reference can be a URL (https://github.com/owner/repo/pull/123)
or short form (owner/repo#123).`,
		Args: cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				fixCfg.PRRef = args[0]
			} else if !fixCfg.Local {
				return fmt.Errorf("requires a PR reference argument (or use --local)")
			}
			if fixCfg.PollInterval <= 0 {
				return fmt.Errorf("--interval must be > 0, got %s", fixCfg.PollInterval)
			}
			if fixCfg.MaxIterations <= 0 {
				return fmt.Errorf("--max-iterations must be > 0, got %d", fixCfg.MaxIterations)
			}
			maxPatterns, err := resolveMaxPatterns(fixCfg.MaxPatterns, cmd.Flags().Changed("max-patterns"), nil)
			if err != nil {
				return err
			}
			fixCfg.MaxPatterns = maxPatterns
			modes := 0
			if fixCfg.DryRun {
				modes++
			}
			if fixCfg.PrintPrompt {
				modes++
			}
			if fixCfg.PrintBarePrompt {
				modes++
			}
			if modes > 1 {
				return fmt.Errorf("--dry-run, --print-prompt, and --print-bare-prompt are mutually exclusive")
			}
			opts := fixCfg.ToFixOptions(deps.version)
			opts.Remote = deps.remoteOpts
			if fixCfg.PrintBarePrompt {
				return fix.PrintBarePrompt(cmd.OutOrStdout(), opts, claude.BuildBareFixPrompt)
			}
			return fix.Run(cmd.OutOrStdout(), opts, claude.Fix, claude.BuildFixPrompt)
		},
	}

	fixFlags := fixCmd.Flags()
	fixFlags.DurationVar(&fixCfg.PollInterval, "interval", fix.DefaultPollInterval, "Polling interval between check-status queries")
	fixFlags.IntVar(&fixCfg.MaxIterations, "max-iterations", fix.DefaultMaxIterations, "Maximum number of fix attempts before giving up")
	fixFlags.BoolVar(&fixCfg.Interactive, "interactive", false, "Ask before starting each new fix iteration (after the first)")
	fixFlags.BoolVar(&fixCfg.DryRun, "dry-run", false, "Report failing checks but do not invoke Claude or commit")
	fixFlags.BoolVar(&fixCfg.PrintPrompt, "print-prompt", false, "Render the fix prompt for the current failing checks to stdout and exit; do not invoke Claude or commit")
	fixFlags.BoolVar(&fixCfg.PrintBarePrompt, "print-bare-prompt", false, "Render a self-contained fix prompt (no check analysis) to stdout and exit; meant to be pasted into a manual Claude session already running inside a checkout of the PR")
	fixFlags.BoolVar(&fixCfg.NoFixComment, "no-fix-comment", false, "Do not post each iteration's fix report as a comment on the pull request")
	fixFlags.StringSliceVar(&fixCfg.PatternDirs, "patterns", nil, "Additional pattern sources: local dirs, github:owner/repo[/sub][@ref], or git+https://...[#ref[:sub]]")
	fixFlags.BoolVar(&fixCfg.NoRepoPatterns, "no-repo-patterns", false, "Ignore repo-specific patterns under .planwerk/review_patterns/ in the target repo")
	fixFlags.BoolVar(&fixCfg.NoLocalPatterns, "no-local-patterns", false, "Ignore local patterns from the tool")
	fixFlags.IntVar(&fixCfg.MaxPatterns, "max-patterns", patterns.DefaultMaxPatternsInPrompt, "Max review patterns injected into the prompt (<=0 disables truncation, env: "+envMaxPatterns+")")
	fixFlags.BoolVar(&fixCfg.Local, "local", false, "Operate on the current working directory instead of cloning into a temp dir")
	fixFlags.BoolVar(&fixCfg.Force, "force", false, "With --local, skip the confirmation prompt when the working tree is dirty")
	fixFlags.BoolVar(&fixCfg.NoFixup, "no-fixup", false, "Append the fix as a fresh on-top follow-up commit instead of folding it into the commits it belongs to (git commit --fixup + git rebase --autosquash, then push --force-with-lease)")

	return fixCmd
}
