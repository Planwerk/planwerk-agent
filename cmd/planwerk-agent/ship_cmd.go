package main

import (
	"fmt"
	"io"
	"log/slog"

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
	// pattern, wiki, and capture flags bind onto implOpts, and the pattern and
	// wiki settings are copied into fixOpts per run.
	var implOpts implement.Options
	var fixOpts fix.Options
	var wiki wikiFlags
	var brain brainFlags
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

With the wiki enabled (--wiki, the wiki section of .planwerk/config.yaml, or
PLANWERK_WIKI, in that order of precedence), every implement and fix run ship
drives reads the wiki's review patterns and project memory, and every implement
run proposes new wiki pages in a comment on its Sub Issue (--no-capture skips
that). ship never asks for confirmation, so it pushes the accepted pages only
when the write-back is enabled (--capture-wiki, capture.wiki, or
PLANWERK_CAPTURE_WIKI) and --yes confirms it for the whole run. Enabled without
--yes, ship logs one warning at start and every run stays propose-only.

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
			implOpts = shipWikiAndCapture(implOpts, wiki.resolve(cmd.Flags(), deps.fileCfg.Wiki),
				resolveCaptureWiki(implOpts.CaptureWiki, cmd.Flags().Changed("capture-wiki"), deps.fileCfg.Capture))
			// Every implement run plans with the search. The fix runs get none:
			// shipFixOptions copies no brain setting.
			implOpts.Brain = brain.resolve(cmd.Flags(), deps.fileCfg.Brain)

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
				iopts := shipImplementOptions(implOpts, deps, issueRef,
					resolveString(implementWorkerModel, cmd.Flags().Changed("implement-worker-model"), envImplementWorkerModel, ""),
					resolveString(implementWorkerEffort, cmd.Flags().Changed("implement-worker-effort"), envImplementWorkerEffort, claude.DefaultImplementWorkerEffort))
				return implement.Run(w, iopts, client.Plan, claude.BuildPlanPrompt, client.Implement, claude.BuildImplementPrompt, client.VerifyImplementation, client.AdversarialReview, client.SpecialistReviews, client.SimplifyFindings, client.ApplySimplifications, client.ApplyReview, client.DedupFindings, client.VerifyFindingClaims, client.Capture, client.FinalizePR)
			}
			fixFn := func(w io.Writer, prRef string) error {
				return fix.Run(w, shipFixOptions(fixOpts, implOpts, deps, prRef), client.Fix, claude.BuildFixPrompt)
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
	wiki.register(shipFlags)
	brain.register(shipFlags)
	shipFlags.BoolVar(&implOpts.NoCapture, "no-capture", false, "Skip the read-only capture pass in each per–Sub Issue implement run (only runs with --wiki; writes nothing)")
	shipFlags.BoolVar(&implOpts.CaptureWiki, "capture-wiki", false, "Push the accepted capture pages of each per–Sub Issue implement run to the wiki; ship never asks for confirmation, so the push also needs --yes (off by default; env: "+envCaptureWiki+")")
	shipFlags.BoolVar(&implOpts.Yes, "yes", false, "Confirm the --capture-wiki write for the whole run")

	return shipCmd
}

// shipWikiAndCapture returns base with the wiki and capture settings of a ship
// run. The wiki is set before the capture gate reads it: shipCaptureWrite
// warns only for a run whose wiki is enabled.
func shipWikiAndCapture(base implement.Options, wiki patterns.WikiOptions, captureEnabled bool) implement.Options {
	base.Wiki = wiki
	base.CaptureWiki = shipCaptureWrite(base, captureEnabled)
	return base
}

// shipCaptureWrite reports whether a ship run may push capture pages:
// only when the write-back is enabled and --yes confirmed it.
func shipCaptureWrite(opts implement.Options, enabled bool) bool {
	if enabled && !opts.Yes && opts.Wiki.Enabled && !opts.NoCapture {
		slog.Warn("the capture write-back is enabled, but ship runs unattended and cannot confirm a wiki write; every run stays propose-only (pass --yes to push)")
	}
	return enabled && opts.Yes
}

// shipImplementOptions returns the options of one per–Sub Issue implement run:
// base, which carries the flags bound on the ship command (among them the wiki
// and capture settings), with the run's own values on top.
func shipImplementOptions(base implement.Options, deps *runtimeDeps, issueRef, workerModel, workerEffort string) implement.Options {
	base.Version = deps.version
	base.IssueRef = issueRef
	// ship runs unattended over Sub Issues that meta files at draft depth, so
	// nobody is there to answer the unelaborated-issue question; the planning
	// session carries them as before.
	base.AllowUnelaborated = true
	base.Remote = deps.remoteOpts
	base.WorkerModel = workerModel
	base.WorkerEffort = workerEffort
	return base
}

// shipFixOptions returns the options of one CI self-heal loop: base with the
// pattern and wiki settings of the implement runs, so a fix session is held to
// the same catalog and reads the same project memory as the run it repairs.
func shipFixOptions(base fix.Options, impl implement.Options, deps *runtimeDeps, prRef string) fix.Options {
	base.Version = deps.version
	base.PatternDirs, base.NoRepoPatterns, base.NoLocalPatterns, base.MaxPatterns = impl.PatternDirs, impl.NoRepoPatterns, impl.NoLocalPatterns, impl.MaxPatterns
	base.Wiki = impl.Wiki
	base.PRRef = prRef
	base.Remote = deps.remoteOpts
	return base
}
