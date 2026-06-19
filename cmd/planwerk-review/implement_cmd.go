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
// issue, run a read-only Claude Code planning session inside a clone of the
// target repo (on the dedicated planning model), then run a fresh implement
// session that executes the plan end-to-end (code + tests + docs) and commits on
// a feature branch. The simplify and review passes then run over the committed
// diff, and a finalize session opens the draft pull request last.
// --print-prompt / --print-plan-prompt / --print-bare-prompt mirror the fix
// subcommand for users who want to drive the sessions manually.
func newImplementCmd(deps *runtimeDeps) *cobra.Command {
	var implementCfg cli.ImplementConfig
	var planModel string
	var planEffort string

	implementCmd := &cobra.Command{
		Use:   "implement <issue-ref>",
		Short: "Plan and implement an elaborated GitHub issue end-to-end with Claude Code",
		Long: `Fetch a GitHub issue (typically already elaborated via the elaborate
subcommand), clone the repository, and run two fresh Claude Code sessions:
first a read-only planning session that grounds the issue in the actual code
and produces a detailed implementation plan, then an implement session that
executes the plan end-to-end: code, tests, documentation, committed on a fresh
feature branch. The implement session does NOT open a pull request — the
simplify and review passes run over the committed diff first, then a dedicated
finalize session opens the draft PR so it lands already simplified and
self-reviewed.

The planning session runs on the dedicated planning model (--plan-model,
default "` + claude.DefaultPlanModel + `") at the dedicated planning effort (--plan-effort, default
"` + claude.DefaultPlanEffort + `") on the default read-only permission mode; its only artifact is the
plan, which is embedded into the implement prompt. The finished plan is also
posted back onto the source issue as a comment (use --no-plan-comment to skip
that). A plan that reports STATUS: BLOCKED or NEEDS_CONTEXT aborts before any
code is written. Use --no-plan to skip the planning session and implement
directly in a single session.

If an implementation plan planwerk-review posted on an earlier run is already
on the issue (for example a run that planned but was aborted before
implementing), it is reused verbatim by default: the planning session is
skipped, no duplicate plan comment is posted, and the reused plan is still held
to the same STATUS check. Use --no-plan-reuse to force a fresh planning session
when the issue has changed and the posted plan has gone stale.

The implement session runs in Claude Code's auto mode (--permission-mode
auto) so it can edit files, run the test suite, and commit without an
interactive confirmation. A background classifier still vets each action and
blocks anything irreversible or aimed outside the repository (force push,
pushing to main, data exfiltration). Requires Claude Code v2.1.83+.

After the implement session the implementation report is posted back onto the
source issue as a comment on every run — including runs where nothing was
implemented or the attempt failed — so the course of the implementation is
recorded on the issue (use --no-report-comment to skip that).

Once the branch is committed, a simplify pass runs by default: a read-only
ponytail-style finder reviews the produced diff through a YAGNI decision ladder
for over-engineering, then a fresh session folds each removal into the commit it
belongs to (git commit --fixup + git rebase --autosquash) on the local branch —
no push. It never removes validation, error handling, security, accessibility,
tests, or assertions, posts its report as a comment on the source issue, and is
non-fatal. Nothing to simplify is a clean no-op. Disable it with --no-simplify.

After the simplify pass, a review-and-fix pass runs by default: the
adversarial review machinery flags bugs in the produced diff, then a fresh
session folds each fix into the commit it belongs to (same fixup/autosquash
mechanism as the simplify pass, still no push) so the eventual PR lands already
self-reviewed. It posts its report as a comment on the source issue, a STATUS:
BLOCKED / NEEDS_CONTEXT report stops the pass without retrying, and nothing to
fix is a clean no-op. The pass is non-fatal — a failed or escalated review never
changes the run's exit code. Disable it with --no-review. (The read-only
--verify / --verify-adversarial flags remain available for a report-only run.)

Finally, once the simplify and review passes are done, a finalize session opens
the draft pull request: it pushes the feature branch and runs gh pr create with
a description that walks the reviewer through the commits and links the issue
with "Closes #N". This is the run's deliverable, so unlike the passes above a
failure to open the PR is fatal. A branch that carries no commits opens no PR
(and is not an error).

Use --print-prompt to render the implement prompt (with the issue body
embedded, without a plan) to stdout without invoking Claude;
--print-plan-prompt does the same for the planning prompt. Use
--print-bare-prompt to render a portable, self-contained prompt that you can
paste into a manual Claude Code session already running inside your own
checkout.

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
			if implementCfg.PrintPlanPrompt {
				modes++
			}
			if modes > 1 {
				return fmt.Errorf("--dry-run, --print-prompt, --print-bare-prompt, and --print-plan-prompt are mutually exclusive")
			}
			// The planning session runs on its own model/effort, so build a
			// client that layers the resolved --plan-* options on top of the
			// shared --claude-* options.
			planOpts := append([]claude.Option{}, deps.claudeOpts...)
			planOpts = append(planOpts,
				claude.WithPlanModel(resolvePlanModel(planModel, cmd.Flags().Changed("plan-model"))),
				claude.WithPlanEffort(resolvePlanEffort(planEffort, cmd.Flags().Changed("plan-effort"))),
			)
			client := claude.NewClient(planOpts...)
			opts := implementCfg.ToImplementOptions(deps.version)
			opts.Remote = deps.remoteOpts
			if implementCfg.PrintBarePrompt {
				return implement.PrintBarePrompt(cmd.OutOrStdout(), opts, claude.BuildBareImplementPrompt)
			}
			return implement.Run(cmd.OutOrStdout(), opts, client.Plan, claude.BuildPlanPrompt, client.Implement, claude.BuildImplementPrompt, client.VerifyImplementation, client.AdversarialReview, client.SimplifyFindings, client.ApplySimplifications, client.ApplyReview, client.FinalizePR)
		},
	}

	implementFlags := implementCmd.Flags()
	implementFlags.BoolVar(&implementCfg.DryRun, "dry-run", false, "Report what would happen but do not clone, invoke Claude, or push anything")
	implementFlags.BoolVar(&implementCfg.PrintPrompt, "print-prompt", false, "Render the implement prompt (with the issue body embedded, without a plan) to stdout and exit; do not clone or invoke Claude")
	implementFlags.BoolVar(&implementCfg.PrintBarePrompt, "print-bare-prompt", false, "Render a self-contained implement prompt (no issue body) to stdout and exit; meant to be pasted into a manual Claude session already running inside a checkout of the repository")
	implementFlags.BoolVar(&implementCfg.PrintPlanPrompt, "print-plan-prompt", false, "Render the planning prompt (with the issue body embedded) to stdout and exit; do not clone or invoke Claude")
	implementFlags.BoolVar(&implementCfg.NoPlan, "no-plan", false, "Skip the planning session and implement directly in a single session")
	implementFlags.BoolVar(&implementCfg.NoPlanReuse, "no-plan-reuse", false, "Always run a fresh planning session; do not reuse an implementation plan already posted on the issue")
	implementFlags.BoolVar(&implementCfg.NoPlanComment, "no-plan-comment", false, "Do not post the generated implementation plan as a comment on the source issue")
	implementFlags.BoolVar(&implementCfg.NoReportComment, "no-report-comment", false, "Do not post the implementation report as a comment on the source issue")
	implementFlags.StringVar(&planModel, "plan-model", claude.DefaultPlanModel, "Model for the planning session passed to Claude Code via --model (e.g. fable, opus; env: "+envPlanModel+")")
	implementFlags.StringVar(&planEffort, "plan-effort", claude.DefaultPlanEffort, "Reasoning effort for the planning session passed to Claude Code via --effort (low, medium, high, xhigh, max; env: "+envPlanEffort+")")
	implementFlags.BoolVar(&implementCfg.Verify, "verify", false, "After implementing, run an independent pass that checks the actual diff against the issue's Acceptance Criteria without trusting the implementer's report")
	implementFlags.BoolVar(&implementCfg.VerifyAdversarial, "verify-adversarial", false, "After implementing, red-team the produced diff for the bugs it introduces using the adversarial-review pass (independent of --verify)")
	implementFlags.BoolVar(&implementCfg.NoSimplify, "no-simplify", false, "Skip the automatic simplify pass that folds over-engineering removals into the branch before the review phase")
	implementFlags.BoolVar(&implementCfg.NoReview, "no-review", false, "Skip the automatic review-and-fix pass that folds review findings into the branch after the simplify pass")
	implementFlags.StringSliceVar(&implementCfg.PatternDirs, "patterns", nil, "Additional pattern sources: local dirs, github:owner/repo[/sub][@ref], or git+https://...[#ref[:sub]]")
	implementFlags.BoolVar(&implementCfg.NoRepoPatterns, "no-repo-patterns", false, "Ignore repo-specific patterns under .planwerk/review_patterns/ in the target repo")
	implementFlags.BoolVar(&implementCfg.NoLocalPatterns, "no-local-patterns", false, "Ignore local patterns from the tool")
	implementFlags.IntVar(&implementCfg.MaxPatterns, "max-patterns", patterns.DefaultMaxPatternsInPrompt, "Max review patterns injected into the prompt (<=0 disables truncation, env: "+envMaxPatterns+")")
	implementFlags.BoolVar(&implementCfg.Local, "local", false, "Operate on the current working directory instead of cloning into a temp dir")
	implementFlags.BoolVar(&implementCfg.Force, "force", false, "With --local, skip the confirmation prompt when the working tree is dirty")

	return implementCmd
}
