package main

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/planwerk/planwerk-agent/internal/claude"
	"github.com/planwerk/planwerk-agent/internal/fix"
	"github.com/planwerk/planwerk-agent/internal/implement"
	"github.com/planwerk/planwerk-agent/internal/patterns"
	"github.com/planwerk/planwerk-agent/internal/ship"
)

// newShipCmd builds the "ship" subcommand: the unattended fleet driver that
// takes a Meta Issue and autonomously drives every one of its Sub Issues to
// merged on the default branch, in dependency order. It composes the implement
// pipeline and the fix CI self-heal loop per Sub Issue, reads the dependency DAG
// from GitHub's native blocked_by relationships, and merges with the rebase
// method by default — fixing its own CI rather than handing failing checks back
// to a human.
func newShipCmd(deps *runtimeDeps) *cobra.Command {
	var shipOpts ship.Options
	// implOpts and fixOpts carry the per-Sub Issue implement and fix options; the
	// pattern flags bind onto implOpts and are copied into fixOpts per run.
	var implOpts implement.Options
	var fixOpts fix.Options
	var planModel string
	var planEffort string
	var implementModel string
	var implementWorkerModel string
	var implementWorkerEffort string

	shipCmd := &cobra.Command{
		Use:   "ship <issue-ref>",
		Short: "Autonomously implement, CI-fix, and merge every Sub Issue of a Meta Issue",
		Long: `Take a Meta Issue — the kind the "meta" command produces — and drive every
one of its Sub Issues to merged on the default branch, in dependency order,
without a human in the loop. Where "implement" is supervised and deliberately
stops at a draft pull request, "ship" makes those decisions itself: for each
Sub Issue it runs the full implement pipeline, marks the opened PR ready, waits
for CI, fixes red CI itself (reusing the "fix" loop), and merges when green,
then advances to the next ready Sub Issue.

Sub Issues are processed in the order their dependencies allow: ship reads the
native "blocked by" relationships "meta" records and works them topologically,
so a Sub Issue becomes eligible only once every Sub Issue it is blocked by has
merged. Independent Sub Issues stay independently shippable. When a Sub Issue
cannot be finished autonomously — implement reports BLOCKED/NEEDS_CONTEXT, CI
stays red past the fix budget, or the PR will not merge — ship skips it and
everything transitively blocked by it, then continues with any remaining Sub
Issue whose blockers have all merged. The failed Sub Issue's PR is left open
with its report for a human to pick up.

ship narrates its progress on the Meta Issue and posts a final summary; because
state lives in GitHub (closed Sub Issues, merged PRs), a re-run resumes
naturally, skipping past Sub Issues that have already merged. When every Sub
Issue has merged, the Meta Issue is closed.

Merges use the rebase method by default (--merge-method), preserving the
per-commit history the simplify/review passes curate. ship honors branch
protection: it refuses to merge past a required check or review and never force-
merges. Use --no-merge to run the whole pipeline but stop at green CI, leaving
the merges to a human, and --dry-run to report the planned order without cloning
or calling Claude.

Issue reference can be a URL (https://github.com/owner/repo/issues/123)
or short form (owner/repo#123).`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			shipOpts.IssueRef = args[0]
			switch shipOpts.MergeMethod {
			case ship.MergeRebase, ship.MergeSquash, ship.MergeMerge:
			default:
				return fmt.Errorf("unknown --merge-method %q, supported: rebase, squash, merge", shipOpts.MergeMethod)
			}
			if fixOpts.PollInterval <= 0 {
				return fmt.Errorf("--interval must be > 0, got %s", fixOpts.PollInterval)
			}
			if fixOpts.MaxIterations <= 0 {
				return fmt.Errorf("--max-fix-iterations must be > 0, got %d", fixOpts.MaxIterations)
			}
			if shipOpts.StartAt < 0 {
				return fmt.Errorf("--start-at must be a positive Sub Issue number, got %d", shipOpts.StartAt)
			}
			maxPatterns, err := resolveMaxPatterns(implOpts.MaxPatterns, cmd.Flags().Changed("max-patterns"), nil)
			if err != nil {
				return err
			}
			implOpts.MaxPatterns = maxPatterns

			// The per–Sub Issue implement run plans on the dedicated planning
			// model/effort — and implements on its optional model override — so
			// build a client that layers the resolved --plan-* and
			// --implement-model options on top of the shared --claude-* options,
			// exactly as the implement command does. The fix loop reuses the
			// same client.
			planOpts := append([]claude.Option{}, deps.claudeOpts...)
			planOpts = append(planOpts,
				claude.WithPlanModel(resolveString(planModel, cmd.Flags().Changed("plan-model"), envPlanModel, claude.DefaultPlanModel)),
				claude.WithPlanEffort(resolveString(planEffort, cmd.Flags().Changed("plan-effort"), envPlanEffort, claude.DefaultPlanEffort)),
				claude.WithImplementModel(resolveString(implementModel, cmd.Flags().Changed("implement-model"), envImplementModel, "")),
			)
			client := claude.NewClient(planOpts...)
			defer client.LogUsageSummary(cmd.ErrOrStderr())

			implementFn := func(w io.Writer, issueRef string) error {
				iopts := implOpts
				iopts.Version = deps.version
				iopts.IssueRef = issueRef
				iopts.Remote = deps.remoteOpts
				iopts.WorkerModel = resolveString(implementWorkerModel, cmd.Flags().Changed("implement-worker-model"), envImplementWorkerModel, "")
				iopts.WorkerEffort = resolveString(implementWorkerEffort, cmd.Flags().Changed("implement-worker-effort"), envImplementWorkerEffort, claude.DefaultImplementWorkerEffort)
				return implement.Run(w, iopts, client.Plan, claude.BuildPlanPrompt, client.Implement, claude.BuildImplementPrompt, client.VerifyImplementation, client.AdversarialReview, client.SpecialistReviews, client.SimplifyFindings, client.ApplySimplifications, client.ApplyReview, client.DedupFindings, client.VerifyFindingClaims, client.Capture, client.FinalizePR)
			}
			fixFn := func(w io.Writer, prRef string) error {
				fopts := fixOpts
				fopts.Version = deps.version
				fopts.PatternDirs, fopts.NoRepoPatterns, fopts.NoLocalPatterns, fopts.MaxPatterns = implOpts.PatternDirs, implOpts.NoRepoPatterns, implOpts.NoLocalPatterns, implOpts.MaxPatterns
				fopts.PRRef = prRef
				fopts.Remote = deps.remoteOpts
				return fix.Run(w, fopts, client.Fix, claude.BuildFixPrompt)
			}
			return ship.Run(cmd.OutOrStdout(), shipOpts, implementFn, fixFn)
		},
	}

	shipFlags := shipCmd.Flags()
	shipFlags.BoolVar(&shipOpts.DryRun, "dry-run", false, "Report the planned order of Sub Issues without cloning, calling Claude, or merging")
	shipFlags.BoolVar(&shipOpts.NoMerge, "no-merge", false, "Run the whole pipeline but stop at green CI, leaving the merges to a human")
	shipFlags.StringVar(&shipOpts.MergeMethod, "merge-method", ship.MergeRebase, "Merge method for each PR (rebase, squash, merge)")
	shipFlags.IntVar(&shipOpts.StartAt, "start-at", 0, "Begin from a specific Sub Issue number (0 = from the top of the dependency order)")
	shipFlags.IntVar(&fixOpts.MaxIterations, "max-fix-iterations", fix.DefaultMaxIterations, "CI self-heal budget per PR before the Sub Issue is skipped")
	shipFlags.DurationVar(&fixOpts.PollInterval, "interval", fix.DefaultPollInterval, "Polling interval between CI check-status queries")
	shipFlags.BoolVar(&implOpts.NoSimplify, "no-simplify", false, "Skip the automatic simplify pass in each per–Sub Issue implement run")
	shipFlags.BoolVar(&implOpts.NoReview, "no-review", false, "Skip the automatic review-and-fix pass in each per–Sub Issue implement run")
	shipFlags.BoolVar(&implOpts.Verify, "verify", false, "In each implement run, check the produced diff against the Sub Issue's Acceptance Criteria")
	shipFlags.BoolVar(&implOpts.VerifyAdversarial, "verify-adversarial", false, "In each implement run, red-team the produced diff for the bugs it introduces")
	shipFlags.BoolVar(&implOpts.NoPlan, "no-plan", false, "Skip the planning session in each per–Sub Issue implement run")
	shipFlags.BoolVar(&implOpts.NoPlanReuse, "no-plan-reuse", false, "Always run a fresh planning session; do not reuse a plan already posted on the Sub Issue")
	shipFlags.BoolVar(&implOpts.NoPlanComment, "no-plan-comment", false, "Do not post the generated implementation plan as a comment on each Sub Issue")
	shipFlags.StringVar(&planModel, "plan-model", claude.DefaultPlanModel, "Model for the planning session passed to Claude Code via --model (env: "+envPlanModel+")")
	shipFlags.StringVar(&planEffort, "plan-effort", claude.DefaultPlanEffort, "Reasoning effort for the planning session passed via --effort (low, medium, high, xhigh, max; env: "+envPlanEffort+")")
	shipFlags.StringVar(&implementModel, "implement-model", "", "Model for the implement session in each per–Sub Issue run; the other sessions stay on --claude-model (empty inherits --claude-model; env: "+envImplementModel+")")
	shipFlags.StringVar(&implementWorkerModel, "implement-worker-model", "", "Model for the implementer subagents in each per–Sub Issue implement run; setting it switches those runs into orchestrator mode (empty keeps the single-session behavior; env: "+envImplementWorkerModel+")")
	shipFlags.StringVar(&implementWorkerEffort, "implement-worker-effort", claude.DefaultImplementWorkerEffort, "Reasoning effort for the implementer subagents in orchestrator mode (low, medium, high, xhigh, max; ignored without --implement-worker-model; env: "+envImplementWorkerEffort+")")
	shipFlags.StringSliceVar(&implOpts.PatternDirs, "patterns", nil, "Additional pattern sources: local dirs, github:owner/repo[/sub][@ref], or git+https://...[#ref[:sub]]")
	shipFlags.BoolVar(&implOpts.NoRepoPatterns, "no-repo-patterns", false, "Ignore repo-specific patterns under .planwerk/review_patterns/ in the target repo")
	shipFlags.BoolVar(&implOpts.NoLocalPatterns, "no-local-patterns", false, "Ignore local patterns from the tool")
	shipFlags.IntVar(&implOpts.MaxPatterns, "max-patterns", patterns.DefaultMaxPatternsInPrompt, "Max review patterns injected into the prompt (<=0 disables truncation, env: "+envMaxPatterns+")")

	return shipCmd
}
