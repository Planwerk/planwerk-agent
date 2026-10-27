# The slices

A page of Claude Code guidance, or a practice another repository follows
(`/adopt-practices` routes its practices to the same slices), can touch
planwerk-agent in five places, and each has its own readers: the sessions
the orchestrator launches, the prompts they read, the pipeline that chains
them, the people who run the plugin's skills, and the people who work on
this repository or set up a target repository for it. One `guidance-mapper`
agent maps the recommendations routed to one slice; a slice no
recommendation touches gets no mapper.

The slices are fixed so that two analyses of the same page are comparable. A
package that is added later goes into the slice whose readers it serves; a
slice that outgrows one mapper is split in two here, and the ledger says so.

## 1. Headless sessions

How the orchestrator launches Claude Code and what the CLI does for it.

Paths: `internal/claude/runner.go`, `internal/claude/claude.go`,
`internal/claude/stream.go`, `internal/claude/completion.go`,
`internal/claude/progress.go`, `internal/workspace/`,
`internal/search/surface.go`, `action.yml`, `.github/workflows/`, and the
rows of `docs/explanation/design-decisions.md` that name a flag.

Lens: list the flags each session kind gets (permission mode, allowed and
disallowed tools, `--tools ""`, `--settings` and the hooks it carries,
`--setting-sources`, `--strict-mcp-config`, `--agents`,
`--append-system-prompt`, `--json-schema`, `--output-format`, `--resume` and
`--session-id`, `--model` and `--effort`, `--add-dir`) and the guards around
them (timeouts, the completion nudge, the report guard, the usage summary).
For each recommendation, say whether a vendor mechanism the page names (a
hook event, a permission mode, a flag, `/goal`, session persistence, a
worktree, a budget cap) would replace or harden something built here by
hand, and whether a flag in use behaves on the installed version the way the
page describes it.

## 2. Prompt builders

What the sessions are told.

Paths: the builders in `internal/claude/*.go`, the shared blocks in
`internal/claude/components.go` and `internal/claude/baseline.go`, the
goldens under `internal/claude/testdata/prompts/`, and the doctrine in
`docs/explanation/prompt-design.md`.

Lens: what the prompts say about showing evidence instead of asserting
success, fixing a root cause instead of a symptom, staying in scope,
planning before editing, delegating to subagents, and keeping what matters
alive across a compaction. Compare that with the page. The doctrine was
written for the model these prompts run on and is the tiebreaker: where it
already decided a question the page answers differently, the verdict is
`declined` with the doctrine's section quoted.

## 3. Pipeline design

How the sessions are chained.

Paths: `internal/implement/`, `internal/review/`, `internal/fix/`,
`internal/address/`, `internal/rebase/`, `internal/ship/`,
`internal/elaborate/`, `internal/eval/`, `docs/explanation/concept.md`,
`docs/explanation/review-methodology.md`, and
`docs/explanation/design-decisions.md`.

Lens: the multi-session patterns a page recommends (explore, plan, then
implement in separate contexts; a reviewer in a fresh context that sees the
diff and not the reasoning; writer and reviewer; fan-out over many files; a
verification gate before "done"; an unattended run that must finish without
a human) and which decision, if any, put each into the pipeline. Name the
pass that embodies a pattern and the decision that introduced it.

## 4. Interactive skills

What a person running `/planwerk:*` experiences.

Paths: `plugins/planwerk/skills/*/SKILL.md`, `plugins/planwerk/shared/*.md`
(`interaction.md` above all), `internal/skills/plugin_test.go`, and
`docs/how-to/use-the-skills.md`.

Lens: interviewing the author with AskUserQuestion, handing a written spec
to a fresh session, plan mode before an edit, verification evidence in an
interactive session, course correction, context hygiene across a long skill,
and the frontmatter a page recommends (`allowed-tools`,
`disable-model-invocation`, arguments, the description as a router).

## 5. Repository setup and user guidance

How contributors use Claude Code on this repository, and what planwerk-agent
tells its users to put in theirs.

Paths: `.claude/` (agents, commands, skills, ledgers, any settings file), a
root `CLAUDE.md` or `AGENTS.md` if one exists, `Makefile` and
`.golangci.yml` (the checks a session can run), `README.md`,
`docs/tutorials/`, `docs/how-to/`, and the loaders that read a target
repository's conventions: `internal/skills/`, `internal/styleguide/`,
`internal/glossary/`, `internal/patterns/`.

Lens: for contributors, a CLAUDE.md, permission rules, hooks, subagents, and
skills that would make a session on this repository verify its own work and
follow its conventions. For users, whether what planwerk-agent reads from a
target repository (`.claude/skills`, `STYLE_GUIDE.md`, `CONTEXT.md`, review
patterns) and what its hermetic sessions load under `--setting-sources
project` agree with what the page tells people to put in a repository, such
as a CLAUDE.md or project hooks.
