package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/planwerk/planwerk-review/internal/address"
	"github.com/planwerk/planwerk-review/internal/claude"
	"github.com/planwerk/planwerk-review/internal/cli"
	"github.com/planwerk/planwerk-review/internal/patterns"
)

// newAddressCmd builds the "address" subcommand: fetch a PR's human review
// threads, let the operator select which unresolved ones to act on, drive
// Claude to incorporate each as a follow-up commit, push the commits, and
// (gated) reply to and resolve each addressed thread. Mirrors fix_cmd.go.
func newAddressCmd(deps *runtimeDeps) *cobra.Command {
	var addressCfg cli.AddressConfig

	addressCmd := &cobra.Command{
		Use:   "address <pr-ref>",
		Short: "Incorporate selected PR review comments as follow-up commits",
		Long: `Read a pull request's human review threads, present the unresolved ones as
an interactive selection list, and drive a fresh Claude Code session to
incorporate the selected ones as follow-up commits on the PR head branch —
optionally replying to and resolving each addressed thread afterwards.

This closes the loop the other commands leave open: fix loops on failing CI
checks, rebase resolves merge conflicts, and implement works from an issue —
none of them consume the reviewer feedback left as inline comments on a PR.

Threads that GitHub already marks resolved, and the tool's own inline review
comments, are skipped by default (--include-resolved offers the resolved ones).
Replies are posted per thread by default (--no-reply to skip); resolving is
outward-facing and off by default (--resolve to enable). Both are best-effort
and never abort the run.

PR reference can be a URL (https://github.com/owner/repo/pull/123)
or short form (owner/repo#123).`,
		Args: cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				addressCfg.PRRef = args[0]
			} else if !addressCfg.Local {
				return fmt.Errorf("requires a PR reference argument (or use --local)")
			}
			if addressCfg.MaxIterations <= 0 {
				return fmt.Errorf("--max-iterations must be > 0, got %d", addressCfg.MaxIterations)
			}
			maxPatterns, err := resolveMaxPatterns(addressCfg.MaxPatterns, cmd.Flags().Changed("max-patterns"), nil)
			if err != nil {
				return err
			}
			addressCfg.MaxPatterns = maxPatterns
			modes := 0
			if addressCfg.DryRun {
				modes++
			}
			if addressCfg.PrintPrompt {
				modes++
			}
			if addressCfg.PrintBarePrompt {
				modes++
			}
			if modes > 1 {
				return fmt.Errorf("--dry-run, --print-prompt, and --print-bare-prompt are mutually exclusive")
			}
			opts := addressCfg.ToAddressOptions(deps.version)
			opts.Remote = deps.remoteOpts
			if addressCfg.PrintBarePrompt {
				return address.PrintBarePrompt(cmd.OutOrStdout(), opts, claude.BuildBareAddressPrompt)
			}
			return address.Run(cmd.OutOrStdout(), opts, claude.Address, claude.BuildAddressPrompt)
		},
	}

	addressFlags := addressCmd.Flags()
	addressFlags.BoolVar(&addressCfg.All, "all", false, "Address every unresolved thread without prompting")
	addressFlags.StringSliceVar(&addressCfg.ThreadIDs, "thread", nil, "Address only the named review thread(s) (repeatable)")
	addressFlags.BoolVar(&addressCfg.IncludeResolved, "include-resolved", false, "Also offer threads GitHub already marks resolved")
	addressFlags.BoolVar(&addressCfg.Reply, "reply", true, "Post a per-thread reply summarizing the change")
	addressFlags.BoolVar(&addressCfg.NoReply, "no-reply", false, "Do not post per-thread replies (overrides --reply)")
	addressFlags.BoolVar(&addressCfg.Resolve, "resolve", false, "Mark addressed threads as resolved (outward-facing; off by default)")
	addressFlags.BoolVar(&addressCfg.OneCommitPerThread, "one-commit-per-thread", true, "Commit each thread separately (default) instead of one aggregate commit")
	addressFlags.BoolVar(&addressCfg.NoAddressComment, "no-address-comment", false, "Do not post the aggregate address report as a comment on the pull request")
	addressFlags.IntVar(&addressCfg.MaxIterations, "max-iterations", address.DefaultMaxIterations, "Maximum number of per-thread address iterations")
	addressFlags.BoolVar(&addressCfg.DryRun, "dry-run", false, "List the selected threads and the planned changes without invoking Claude or committing")
	addressFlags.BoolVar(&addressCfg.PrintPrompt, "print-prompt", false, "Render the address prompt for the selected threads to stdout and exit; do not invoke Claude or commit")
	addressFlags.BoolVar(&addressCfg.PrintBarePrompt, "print-bare-prompt", false, "Render a self-contained address prompt (no thread fetch) to stdout and exit; meant to be pasted into a manual Claude session already running inside a checkout of the PR")
	addressFlags.StringSliceVar(&addressCfg.PatternDirs, "patterns", nil, "Additional pattern sources: local dirs, github:owner/repo[/sub][@ref], or git+https://...[#ref[:sub]]")
	addressFlags.BoolVar(&addressCfg.NoRepoPatterns, "no-repo-patterns", false, "Ignore repo-specific patterns under .planwerk/review_patterns/ in the target repo")
	addressFlags.BoolVar(&addressCfg.NoLocalPatterns, "no-local-patterns", false, "Ignore local patterns from the tool")
	addressFlags.IntVar(&addressCfg.MaxPatterns, "max-patterns", patterns.DefaultMaxPatternsInPrompt, "Max review patterns injected into the prompt (<=0 disables truncation, env: "+envMaxPatterns+")")
	addressFlags.BoolVar(&addressCfg.Local, "local", false, "Operate on the current working directory instead of cloning into a temp dir")
	addressFlags.BoolVar(&addressCfg.Force, "force", false, "With --local, skip the confirmation prompt when the working tree is dirty")

	return addressCmd
}
