# Guidance analysis 2026-10-21: Best practices for Claude Code

url: https://code.claude.com/docs/en/best-practices
sha256: e3b99b377961bdd071ec6390904dcb319c9d3d3a8f627230642eeb84ad753d2e
head: 86353ab
claude_code: 2.1.306
linked: https://code.claude.com/docs/en/goal, https://code.claude.com/docs/en/hooks, https://code.claude.com/docs/en/permission-modes

The five lines after the heading are read by `scripts/fetch.sh`; keep their
names, order, and one-line form. Everything below is for the next reader.

## Scope

First analysis of the page (`state=new`). All five slices had mappers; 37
recommendations were extracted, none carried forward. Totals: 16 adopted,
17 partial, 2 declined, 1 gap, 1 unknown. The mappers ran as `Plan` agents
following `.claude/agents/guidance-mapper.md`, because the session predated
the agent file.

## Recommendations

Slices: 1 headless sessions, 2 prompt builders, 3 pipeline, 4 interactive
skills, 5 repository setup and user guidance.

| key | section | recommendation | status | slices | evidence |
|---|---|---|---|---|---|
| context-is-the-constraint | intro | Treat context as the scarce resource; track and reduce token use | adopted | 1, 2, 3, 5 | `internal/claude/claude.go:155` per-pass usage (decision 79); decision 91 empty-dir structuring; `internal/skills/loader.go:26` cap |
| verify-with-runnable-check | Verify | Give every task a pass/fail check to iterate against | adopted | 2, 3, 4 | `internal/claude/baseline.go:41`; `internal/claude/implement.go:354`; `plugins/planwerk/skills/diagnose/SKILL.md:14` |
| fix-root-cause-not-symptom | Verify | Fix and verify the root cause, never suppress | adopted (4) / partial (2) | 2, 4 | `internal/claude/components.go:1087`; gap: no rule in `rebase.go`, `address.go` (grep `suppress\|nolint\|weaken\|root cause`) |
| gate-stop-with-goal | Verify | Gate a session's stop on a `/goal` condition | adopted (equivalent) | 1, 3 | `internal/claude/completion.go:34-37,64-68` Go-side report gate (decision 78); `/goal` evaluator is model-judged and off under `disableAllHooks` |
| gate-stop-with-stop-hook | Verify | Deterministic Stop hook blocks the turn until the check passes | partial | 1, 5 | nudge runs after exit (`completion.go:54`); grep `"Stop"\|background_tasks` over Go: empty; no `.claude/settings.json` |
| verify-by-second-opinion | Verify | A fresh model tries to refute the result | adopted | 2, 3 | `internal/claude/implement.go:13-15`; `internal/claude/verifyclaims.go:55`; `internal/elaborate/elaborate.go:376` |
| show-evidence-not-assertion | Verify | Show the command and its output, not a claim | adopted (4) / partial (2) | 2, 4 | `internal/claude/implement.go:384`; gap: rebase-apply report `internal/claude/rebase.go:259-267`, address JSON `internal/report/address.go:11` |
| verify-against-running-app | Verify | Check the running app after the tests (`/verify`) | partial | 4, 5 | `plugins/planwerk/skills/diagnose/SKILL.md:151-154`; no zero-cost run guide for contributors; 2.1.306 bundles `run`, not `verify` |
| explore-plan-implement-commit | Plan | Separate exploration and planning from implementation | adopted | 3, 4 | `internal/claude/runner.go:756` read-only plan; `plugins/planwerk/skills/implement/SKILL.md:97,126` |
| skip-plan-for-one-sentence-diffs | Plan | Skip the plan when the diff fits one sentence | adopted (3) / declined (4) | 3, 4 | `--no-plan` (`cmd/planwerk-agent/implement_cmd.go:225`); decision 85 keeps the skill's plan gate |
| edit-plan-before-implementing | Plan | Let the human edit the plan first | adopted | 3, 4 | decision 70 (answers into the body); `ExitPlanMode` approval with Ctrl+G |
| specific-context-in-prompts | Context | Scope, sources, patterns, symptom; reproduce first | adopted | 2, 4 | `internal/claude/plan.go:54`; `internal/claude/fix.go:447`; reproduce-first removed from baseline by decision 117 |
| rich-content-and-self-fetch | Context | Pipe data, let Claude fetch its own context | adopted | 1, 5 | `internal/claude/runner.go:925` stdin; `internal/search/surface.go:32`; all-domain WebFetch `runner.go:144-154` |
| concise-claude-md | CLAUDE.md | A short checked-in CLAUDE.md with what Claude cannot infer | partial | 1, 2, 5 | no tracked CLAUDE.md/AGENTS.md (git ls-files); `internal/claude/prompt.go:32` flags only `.claude/` |
| compaction-preservation-instructions | Context | Tell compaction what must survive | adopted (1) / partial (2) | 1, 2 | `internal/claude/runner.go:863` implement system prompt (decision 108); grep `appendSystemPrompt`: implement only |
| permissions-allowlist-and-sandbox | Permissions | Allowlist trusted tools, sandbox the rest | partial | 1, 5 | `runner.go:104-106,891` read-only passes no mode; no `cmd.Env` for claude; no sandbox |
| prefer-cli-tools | CLI tools | Use `gh` and other CLIs for external services | adopted | 1, 4 | `plugins/planwerk/shared/github.md:3`; `internal/claude/finalize.go:91` |
| connect-mcp-servers | MCP | Connect MCP servers | declined | 1, 5 | decision 45 (`--strict-mcp-config`, no prompt needs one); decision 114 |
| hooks-for-zero-exception-actions | Hooks | Hooks for what must happen every time | adopted (1) / partial (5) | 1, 5 | decisions 46, 95, 119; no contributor hooks; user docs silent on hooks (grep `hook` docs/: empty) |
| skills-for-on-demand-knowledge | Skills | Skills for knowledge and workflows; `disable-model-invocation` for side effects | adopted (1, 5) / partial (4) | 1, 4, 5 | decisions 63, 107, 119; no plugin skill weighs `disable-model-invocation` (decision 116 vs 69) |
| custom-subagents | Subagents | Specialized agents with own tools and model | adopted | 1, 5 | `internal/claude/runner.go:327` `--agents` (decision 74); `.claude/agents/` |
| code-intelligence-plugin | Plugins | Code-intelligence plugin for typed languages | gap (conformance only) | 1, 5 | grep `lsp\|enabledPlugins\|gopls`: empty; decision 100 host has no Go |
| ask-codebase-questions | Communicate | Use Claude for codebase Q&A | adopted | 5 | 73 code comments cite decision numbers; `docs/reference/project-structure.md` |
| interview-then-self-contained-spec | Interview | Interview, write a self-contained spec, execute in a fresh session | adopted (3) / partial (4) | 3, 4 | `plugins/planwerk/skills/elaborate/SKILL.md:113`; gap: elaborate has no `--no-plan-reuse` handoff, which `plugins/planwerk/shared/github.md:97-99` requires |
| course-correct-then-restart | Course-correct | After two failed corrections, start fresh with a better prompt | adopted | 2, 3, 4 | `internal/claude/implement.go:400-404` (decision 33); `internal/claude/fix.go:81`; `diagnose/SKILL.md:340` |
| fresh-context-per-task | Context | `/clear` between unrelated tasks | adopted (3) / partial (4) | 3, 4 | fresh `claude -p` per pass; skill-to-skill handoffs never say "fresh session" (grep: empty) |
| compact-deliberately | Context | Steer compaction, keep side questions out | adopted (1, 2) / unknown (4) | 1, 2, 4 | system-prompt contract; whether a skill body survives compaction needs a probe |
| subagents-for-investigation | Subagents | Delegate research to subagents | partial | 2, 4 | `internal/claude/implement.go:425,469`; prompt names the "Task tool" (`implement.go:473`), the CLI's tool is `Agent`; no skill delegates |
| checkpoints-not-git | Checkpoints | Use checkpoints, but they do not replace git | adopted (3) / partial (4) | 3, 4 | git is the state of record (decisions 61, 94); humanize names only `git diff` as undo |
| name-and-resume-sessions | Resume | Name and resume sessions | adopted | 1, 3 | `internal/claude/runner.go:296-304` `--session-id`/`--resume` (decision 78) |
| headless-structured-output | Headless | `-p` with JSON output; `--no-session-persistence` for one-shot runs | partial | 1 | `runner.go:884-887`; grep `no-session-persistence`: empty |
| parallel-sessions-in-worktrees | Parallel | Isolated checkouts for parallel sessions | adopted | 1, 3 | per-run clone `internal/github/pr.go:244`; read-only fan-out only; decision 74 sequential edits |
| writer-reviewer-fresh-context | Parallel | Review in a context that did not write the code | adopted | 3 | `internal/claude/adversarial.go:10`; decision 72 |
| fan-out-with-scoped-permissions | Fan out | `--allowedTools` plus `dontAsk` for unattended fan-out | partial | 1, 3 | allow/deny rules `runner.go:154,170`; grep `dontAsk\|defaultMode`: empty; documented minimum v2.1.83 predates the 2.1.302 `-p` default |
| auto-mode-for-unattended-runs | Auto mode | Auto mode for unattended work; know its fallbacks | partial | 1 | `runner.go:108`; `permission_denials` never decoded (grep: empty); failures 4b11b96, 3c413ef |
| adversarial-review-against-plan | Review | Fresh reviewer checks the diff against the plan, flags only correctness gaps | partial | 2, 3 | verifier opt-in (`implement_cmd.go:234`, `ship_cmd.go:148`), no plan (`internal/implement/interfaces.go:216`), no scope check; INFO reaches apply (`internal/claude/review_apply.go:64`) |
| scope-investigations | Failure patterns | Scope investigations narrowly or delegate them | adopted | 2, 3 | `internal/claude/components.go:916` diff scope; decision 79 area-scoped specialists |

## Proposals

Ranked by value. The author took 15 of the 16 forward on 2026-10-21 and
deferred one.

1. **readonly-permission-mode** (R34, R16; slices 1, 3, 5). Pass an explicit
   `--permission-mode` (`dontAsk`, or `manual`) to read-only and structuring
   specs in `claudeArgs`, correct decision 45's "flags outrank project
   settings" for those sessions, and raise the documented minimum Claude Code
   version (v2.1.83 in `docs/reference/cli.md:54,636`, `runner.go:106`,
   `implement_cmd.go:79`) to at least 2.1.302. Size S; code, docs, decision.
   Prevents a reviewed checkout's `permissions.defaultMode: acceptEdits` (or
   its allow rules) from letting a read-only pass run `rm`/`sed` in the
   checkout or the `--add-dir` catalog: decision 95's threat class. Needs the
   decision-95-style probe first (checkout with `defaultMode: acceptEdits`,
   then with an allow rule, session asked to `touch probe`), then argv tests.
   Disposition: accepted, to be filed through `/planwerk:draft`.
2. **claude-md-is-agent-config** (R14; slices 1, 2, 5). (a) Extend the
   review's agent-configuration rule (`internal/claude/prompt.go:32`) to
   `CLAUDE.md`, `CLAUDE.local.md`, and `AGENTS.md` at any depth; S,
   prompt-behavior on a finder (golden diff plus `make eval` per decision
   106). (b) Decide whether read-only review sessions should load the head's
   CLAUDE.md at all (decision 97: rules come from the base); M, needs probes
   that a head CLAUDE.md reaches the session and what `--bare` /
   `--safe-mode` keep on 2.1.306. Disposition: accepted, to be filed through `/planwerk:draft`.
3. **surface-permission-denials** (R35; slice 1). Decode the result
   envelope's `permission_denials` on both runner paths, log them per pass,
   and warn when an auto session had Edit/Write denied (auto mode silently
   fell back to Manual). S–M; code. The repository has seen silent denials
   twice (4b11b96, 3c413ef). Needs a probe of the field's shape on 2.1.306
   and envelope tests. Disposition: deferred: not selected by the author on 2026-10-21, no reason given.
4. **verify-against-plan** (R36, R6; slices 2, 3). Thread the plan into
   `VerifyImplementation`, add an independent scope check (a hunk no
   criterion, work package, or Affected Area accounts for, or one a Non-Goal
   excludes), make `--verify` default-on in `implement` and `ship` with
   `--no-verify`, and fix the docs that still call it report-only
   (`docs/reference/cli.md:747-748`, `docs/how-to/implement-an-issue.md:109`
   contradict `cli.md:685-689`). M; code, prompt-behavior (finder: eval
   evidence), decision. Prevents decision 62's failure (4 of 7 work packages
   shipped), today caught only by self-report. Disposition: accepted, to be filed through `/planwerk:draft`.
5. **elaborate-handoff** (R24; slice 4). `elaborate` ends with a report that
   says whether an implementation plan comment exists and hands off with
   `--no-plan-reuse` when one does, as `plugins/planwerk/shared/github.md:97-99`
   requires of every skill that changes an issue body (clarify, decide, and
   revisit comply). Optional guard in `internal/skills/plugin_test.go`. S;
   skill. Prevents an implement run that silently reuses a plan written
   against the pre-elaboration body. Disposition: accepted, to be filed through `/planwerk:draft`.
6. **agent-tool-name** (R28; slice 2). `internal/claude/implement.go:473`
   tells the orchestrator to delegate "via the Task tool"; the 2.1.306 tool
   is `Agent`. Rename it, or name no tool, and regenerate
   `implement_orchestrated.golden`. S; prompt-wording. Disposition: accepted, to be filed through `/planwerk:draft`.
7. **readonly-env-tokens** (R16; slice 1). Probe whether a read-only session
   can print `GH_TOKEN` (`printenv`, `echo $GH_TOKEN`); if it can, set
   `cmd.Env` for read-only specs without the GitHub tokens. S after the
   probe; code. Decision 95's threat, in the Action with a write token.
   Disposition: accepted, to be filed through `/planwerk:draft`.
8. **stop-hook-background-gate** (R5; slices 1, 5). A Stop hook (the binary
   itself as the command) that blocks the turn while `background_tasks` holds
   a running shell task and while the report is missing; the completion nudge
   stays as the backstop. M; code, decision. Prevents decision 78's failure
   at its source instead of re-running the killed tests. Must answer decision
   108's objections to inline hooks; probe first. Disposition: accepted, to be filed through `/planwerk:draft`.
9. **count-compactions-and-delegations** (R15, R27, R28; slice 2). Count
   compaction events and `Agent` calls per pass in `stream.go` and the usage
   summary, so a lost contract outside implement becomes visible before it
   is fixed by prompt. S; code. Disposition: accepted, to be filed through `/planwerk:draft`.
10. **info-findings-optional** (R36; slices 2, 3). Decide whether verified
    INFO findings reach review-apply; filter them in the partition
    (`internal/implement/implement.go:1616-1625`) or say "optional" in the
    apply prompt. S; decision. Count applied INFO findings in real runs
    first. Disposition: accepted, to be filed through `/planwerk:draft`.
11. **rebase-apply-evidence** (R7, R2; slice 2). Add the Verification line
    simplify-apply and review-apply carry to the rebase-apply report and its
    DONE, as one shared block. S; prompt-behavior. The runner force-pushes on
    that DONE. Disposition: accepted, to be filed through `/planwerk:draft`.
12. **target-repo-reads-page** (R5, R14, R16, R18, R19; slice 5). A
    reference page "What planwerk-agent reads from your repository": each
    file, which sessions read it, from which ref (`.planwerk/*`,
    `STYLE_GUIDE.md`, `CONTEXT.md`, `.claude/skills/`, `.claude/settings.json`
    hooks, `CLAUDE.md`, `.mcp.json` ignored). M; docs. Disposition: accepted, to be filed through `/planwerk:draft`.
13. **contributor-claude-md** (R14; slice 5). A root CLAUDE.md of 15–25 lines
    of facts (Docker toolbox, golden regeneration, `make eval` spends tokens,
    `make plugin-validate`, trailers, search the decisions by mechanism), plus
    a make target for golden regeneration. S; docs, code. Every planwerk
    session on this repository would load it, so facts only. Disposition:
    accepted, to be filed through `/planwerk:draft`.
14. **skill-validation-in-ci** (R20; slice 5). CI validates only `.` and
    `plugins/planwerk` (`.github/workflows/ci.yml:98-99`), so the skill
    directories' schema checks decision 116 relies on run only by hand. S;
    config. Disposition: accepted, to be filed through `/planwerk:draft`.
15. **no-session-persistence** (R31; slice 1). `--no-session-persistence` for
    one-shot read-only and structuring sessions. S; code. Disposition: accepted, to be filed through `/planwerk:draft`.
16. **skills-fresh-session-handoffs** (R26; slice 4). Say "in a fresh session"
    in the skill-to-skill handoffs and in `docs/how-to/use-the-skills.md`. S;
    docs, prompt-wording. Disposition: accepted, to be filed through `/planwerk:draft`.

Conformance only, listed for completeness: subagent fan-out in the audit and
cleanup skills (R28, M); humanize's undo note (R29); a decision on
`disable-model-invocation` for the plugin skills (R20); a zero-cost run skill
for contributors (R8); the no-suppression rule in rebase and address (R3); a
verification field in the address result (R7); contributor hooks for gofmt
and hand-edited goldens (R19); a project allowlist with an `ask` rule for
`make eval` (R16); enforcing read-only Bash in the agent definitions (R21); a
gopls note (R22).

## Conflicts

- **Decision 45 vs the code and the permission-modes page.** Decision 45:
  "The CLI flags the runner already passes … outrank project settings, so a
  reviewed repo cannot override the model or permission mode." Read-only
  sessions pass no `--permission-mode` (`runner.go:104-106`), and the page
  says project `defaultMode` values other than `auto` and
  `bypassPermissions` apply. The page is newer. Proposal 1.
- **Decision 45 vs `claude --help` 2.1.306 on `--bare`.** The decision
  rejects `--bare` because it "also strips `Read`/`Grep`/`Glob`"; the current
  help lists what `--bare` skips and names no tools, but limits auth to
  `ANTHROPIC_API_KEY`/`apiKeyHelper`. Unknown until probed. Proposal 2b.
- **Page vs decision 85.** "If you could describe the diff in one sentence,
  skip the plan" against the implement skill's mandatory plan gate. The
  decision stands; the skill is opt-in.
- **Page vs decision 117.** "Write a failing test that reproduces the issue"
  against the baseline's removed reproduce-first line (commit 5d96b3a). fix
  and review-apply keep it; the decision stands for the baseline.
- **Page vs the doctrine on reviewer bars.** "Flag only gaps that affect
  correctness" against "a finder reports; the pipeline filters"
  (`docs/explanation/prompt-design.md:270-276`). The doctrine wins for finder
  prompts; the page's goal is met on the filter side (proposal 10).
- **Page vs decisions 95 and 108 on in-session gates.** The page's `/goal`
  and Stop hook against the orchestrator-side gate and the rejected inline
  hook. Open; proposal 8's probe.
- **Inside the repository.** Decision 117 keeps capitals on a few rules
  only; `implement.golden` carries 19 `NEVER` and the doctrine's
  rationalizations section says "the rule keeps its `NEVER`". For the next
  `/iterate-prompts`.
- **Unrecorded choice.** The all-domain WebFetch grant is reasoned only in a
  code comment (`runner.go:144-147`, commit 3c413ef), not a decision row.

## Dropped

- Slice 4's unknown "does `EnterPlanMode` exist in 2.1.306": it does; the
  analyzing session had the tool available.
- Slice 4's and slice 5's unknown "is `/verify` bundled": the analyzing
  session's bundled skill is `run` ("launch and drive this project's app");
  the page's `/verify` name is recorded as a version note on
  verify-against-running-app, not as an open probe.
