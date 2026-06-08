package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/planwerk/planwerk-review/internal/claude"
	"github.com/planwerk/planwerk-review/internal/cli"
	"github.com/planwerk/planwerk-review/internal/implement"
	"github.com/planwerk/planwerk-review/internal/patterns"
)

// newImplementCmd builds the "implement" subcommand: take an elaborated GitHub
// issue and run a fresh Claude Code session inside a clone of the target repo
// to implement the feature end-to-end (code + tests + docs) and open a draft
// pull request. --print-prompt / --print-bare-prompt mirror the fix subcommand
// for users who want to drive the session manually.
func newImplementCmd(deps *runtimeDeps) *cobra.Command {
	var implementCfg cli.ImplementConfig

	implementCmd := &cobra.Command{
		Use:   "implement <issue-ref>",
		Short: "Implement an elaborated GitHub issue end-to-end with Claude Code",
		Long: `Fetch a GitHub issue (typically already elaborated via the elaborate
subcommand), clone the repository, and run a fresh Claude Code session to
implement the feature end-to-end: code, tests, documentation, fresh feature
branch, draft pull request linked to the issue.

The session runs in Claude Code's auto mode (--permission-mode auto) so it can
edit files, run the test suite, commit, push the branch, and open the draft PR
without an interactive confirmation. A background classifier still vets each
action and blocks anything irreversible or aimed outside the repository (force
push, pushing to main, data exfiltration). Requires Claude Code v2.1.83+.

Use --print-prompt to render the implement prompt (with the issue body
embedded) to stdout without invoking Claude. Use --print-bare-prompt to
render a portable, self-contained prompt that you can paste into a manual
Claude Code session already running inside your own checkout.

Issue reference can be a URL (https://github.com/owner/repo/issues/123)
or short form (owner/repo#123).`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			implementCfg.IssueRef = args[0]
			maxPatterns, err := resolveMaxPatterns(implementCfg.MaxPatterns, cmd.Flags().Changed("max-patterns"), nil)
			if err != nil {
				return err
			}
			implementCfg.MaxPatterns = maxPatterns
			modes := 0
			if implementCfg.DryRun {
				modes++
			}
			if implementCfg.PrintPrompt {
				modes++
			}
			if implementCfg.PrintBarePrompt {
				modes++
			}
			if modes > 1 {
				return fmt.Errorf("--dry-run, --print-prompt, and --print-bare-prompt are mutually exclusive")
			}
			opts := implementCfg.ToImplementOptions(deps.version)
			if implementCfg.PrintBarePrompt {
				return implement.PrintBarePrompt(cmd.OutOrStdout(), opts, claude.BuildBareImplementPrompt)
			}
			return implement.Run(cmd.OutOrStdout(), opts, claude.Implement, claude.BuildImplementPrompt, claude.VerifyImplementation)
		},
	}

	implementFlags := implementCmd.Flags()
	implementFlags.BoolVar(&implementCfg.DryRun, "dry-run", false, "Report what would happen but do not clone, invoke Claude, or push anything")
	implementFlags.BoolVar(&implementCfg.PrintPrompt, "print-prompt", false, "Render the implement prompt (with the issue body embedded) to stdout and exit; do not clone or invoke Claude")
	implementFlags.BoolVar(&implementCfg.PrintBarePrompt, "print-bare-prompt", false, "Render a self-contained implement prompt (no issue body) to stdout and exit; meant to be pasted into a manual Claude session already running inside a checkout of the repository")
	implementFlags.BoolVar(&implementCfg.Verify, "verify", false, "After implementing, run an independent pass that checks the actual diff against the issue's Acceptance Criteria without trusting the implementer's report")
	implementFlags.StringSliceVar(&implementCfg.PatternDirs, "patterns", nil, "Additional pattern sources: local dirs, github:owner/repo[/sub][@ref], or git+https://...[#ref[:sub]]")
	implementFlags.BoolVar(&implementCfg.NoRepoPatterns, "no-repo-patterns", false, "Ignore repo-specific patterns under .planwerk/review_patterns/ in the target repo")
	implementFlags.BoolVar(&implementCfg.NoLocalPatterns, "no-local-patterns", false, "Ignore local patterns from the tool")
	implementFlags.IntVar(&implementCfg.MaxPatterns, "max-patterns", patterns.DefaultMaxPatternsInPrompt, "Max review patterns injected into the prompt (<=0 disables truncation, env: "+envMaxPatterns+")")
	implementFlags.BoolVar(&implementCfg.Local, "local", false, "Operate on the current working directory instead of cloning into a temp dir")
	implementFlags.BoolVar(&implementCfg.Force, "force", false, "With --local, skip the confirmation prompt when the working tree is dirty")

	return implementCmd
}
