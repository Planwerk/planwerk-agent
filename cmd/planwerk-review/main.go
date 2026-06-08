package main

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/cobra/doc"

	"github.com/planwerk/planwerk-review/internal/cache"
	"github.com/planwerk/planwerk-review/internal/claude"
	"github.com/planwerk/planwerk-review/internal/cli"
	"github.com/planwerk/planwerk-review/internal/elaborate"
	"github.com/planwerk/planwerk-review/internal/fix"
	"github.com/planwerk/planwerk-review/internal/implement"
	"github.com/planwerk/planwerk-review/internal/patterns"
	"github.com/planwerk/planwerk-review/internal/prompt"
)

// runtimeDeps carries the process-wide values every subcommand constructor
// needs: the resolved build version and the parsed .planwerk/config.yaml.
// fileCfg is populated by the root command's PersistentPreRunE before any
// subcommand RunE runs, so the review, propose, and audit commands read it
// back through this shared pointer.
type runtimeDeps struct {
	version string
	fileCfg cli.FileConfig
}

func main() {
	deps := &runtimeDeps{version: version}

	rootCmd := newRootCmd(deps)

	rootCmd.AddCommand(newProposeCmd(deps))

	rootCmd.AddCommand(newAuditCmd(deps))

	rootCmd.AddCommand(newGapAnalysisCmd(deps))

	rootCmd.AddCommand(newReviewPreparedCmd(deps))

	// elaborate subcommand: turn a high-level issue (typically the output of
	// propose or audit) into a deeply detailed engineering plan grounded in
	// the actual repository state.
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
			return elaborate.Run(os.Stdout, opts, claude.Elaborate, claude.ReviewElaboration)
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

	rootCmd.AddCommand(elaborateCmd)

	// prompt subcommand: deterministically render a copy-paste-ready Claude
	// Code prompt from an existing GitHub issue (typically an audit finding
	// or an elaborated proposal). No Claude call — pure prompt assembly.
	var promptCfg cli.PromptConfig

	promptCmd := &cobra.Command{
		Use:   "prompt <issue-ref>",
		Short: "Generate a Claude Code prompt that fixes or implements an issue",
		Long: `Fetch a GitHub issue and emit a copy-paste-ready prompt for Claude
Code (or another AI agent) to fix or implement it.

Mode is auto-detected from the issue body: issues whose body carries the
audit severity marker ("**Severity**:") get the "fix" prompt; everything
else gets the "implement" prompt. Override with --mode.

Issue reference can be a URL (https://github.com/owner/repo/issues/123)
or short form (owner/repo#123).`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			promptCfg.IssueRef = args[0]
			switch promptCfg.Mode {
			case "auto", "fix", "implement":
			default:
				return fmt.Errorf("unknown mode %q, supported: auto, fix, implement", promptCfg.Mode)
			}
			opts := promptCfg.ToPromptOptions(deps.version)
			return prompt.Run(os.Stdout, opts)
		},
	}
	promptCmd.Flags().StringVar(&promptCfg.Mode, "mode", "auto", "Prompt variant (auto, fix, implement)")

	rootCmd.AddCommand(promptCmd)

	// fix subcommand: drive a self-healing loop that watches a PR's checks,
	// dispatches a fresh Claude session to repair failures, then waits for
	// the new commit's checks to come back. The loop continues until every
	// check is green or --max-iterations is exhausted.
	var fixCfg cli.FixConfig

	fixCmd := &cobra.Command{
		Use:   "fix <pr-ref>",
		Short: "Loop on a PR's failing CI checks until they all pass",
		Long: `Watch a GitHub pull request's CI checks and, when one fails, dispatch a
fresh Claude Code session to apply a minimal-invasive fix and push it as a
follow-up commit. After each push the loop waits for the new commit's checks
to complete; if any still fail the loop starts over. Continues until every
check is green or --max-iterations is hit.

Status checks are queried directly via the GitHub API (gh CLI) — Claude is
only invoked when an actual fix is needed.

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
	fixFlags.StringSliceVar(&fixCfg.PatternDirs, "patterns", nil, "Additional pattern sources: local dirs, github:owner/repo[/sub][@ref], or git+https://...[#ref[:sub]]")
	fixFlags.BoolVar(&fixCfg.NoRepoPatterns, "no-repo-patterns", false, "Ignore repo-specific patterns under .planwerk/review_patterns/ in the target repo")
	fixFlags.BoolVar(&fixCfg.NoLocalPatterns, "no-local-patterns", false, "Ignore local patterns from the tool")
	fixFlags.IntVar(&fixCfg.MaxPatterns, "max-patterns", patterns.DefaultMaxPatternsInPrompt, "Max review patterns injected into the prompt (<=0 disables truncation, env: "+envMaxPatterns+")")
	fixFlags.BoolVar(&fixCfg.Local, "local", false, "Operate on the current working directory instead of cloning into a temp dir")
	fixFlags.BoolVar(&fixCfg.Force, "force", false, "With --local, skip the confirmation prompt when the working tree is dirty")

	rootCmd.AddCommand(fixCmd)

	// implement subcommand: take an elaborated GitHub issue and run a fresh
	// Claude Code session inside a clone of the target repo to implement the
	// feature end-to-end (code + tests + docs) and open a draft pull request.
	// --print-prompt / --print-bare-prompt mirror the fix subcommand for
	// users who want to drive the session manually.
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

	rootCmd.AddCommand(implementCmd)

	rootCmd.AddCommand(newCacheCmd(deps))

	// gen-man-pages: hidden helper used by release tooling to emit man pages.
	genManCmd := &cobra.Command{
		Use:    "gen-man-pages <dir>",
		Short:  "Generate man pages into the given directory",
		Args:   cobra.ExactArgs(1),
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := args[0]
			if err := os.MkdirAll(dir, 0o750); err != nil {
				return err
			}
			header := &doc.GenManHeader{
				Title:   "PLANWERK-REVIEW",
				Section: "1",
				Source:  "planwerk-review " + deps.version,
			}
			return doc.GenManTree(rootCmd, header, dir)
		},
	}
	rootCmd.AddCommand(genManCmd)

	if err := rootCmd.Execute(); err != nil {
		// Route the final error through slog so it honors --log-format.
		slog.Error(err.Error())
		os.Exit(1)
	}
}
