# Use impeccable for UI work

Load the [impeccable](https://github.com/pbakaus/impeccable) design plugin into
the sessions that write code, and let the `ui` review specialist read its
guidance and its design detector, so a change to an interface is built and
reviewed against more than the generic checklist.

impeccable is a Claude Code plugin: one skill with commands such as `audit`,
`harden`, and `polish`, hooks that run its detector on every edit of a UI file,
and a detector that matches dozens of rules (accessibility, layout, typography,
the common tells of generated interfaces) without a model. planwerk-agent uses
the copy Claude Code installed, where Claude Code keeps it. Nothing of
impeccable is copied into planwerk-agent, so the version you install is the
version that runs.

## Install the plugin

```bash
claude plugin marketplace add pbakaus/impeccable
claude plugin install impeccable@impeccable
claude plugin list        # shows the id and the installed version
```

The install enables the plugin in your interactive Claude Code sessions. It does
not reach planwerk-agent's own sessions: those run isolated from your user
settings (`--setting-sources`, [design decision #45](/explanation/design-decisions)),
which is where the install records the plugin as enabled. A run loads a plugin
only when you name it. The exception is `--claude-inherit-user-config`: with it,
every session, the read-only ones included, loads the plugins your user settings
enable, and a plugin you also name loads once, from the directory you named.

## Load it into a run

```bash
# implement an issue with the plugin in the sessions that write code
planwerk-agent implement owner/repo#42 --claude-plugin impeccable@impeccable

# review a pull request with the specialists, the ui specialist among them
planwerk-agent owner/repo#123 --specialists --claude-plugin impeccable@impeccable

# or once, for every command (see the note below on the cache)
export PLANWERK_CLAUDE_PLUGINS=impeccable@impeccable
```

`impeccable@impeccable` resolves through `claude plugin list --json` to the
cache directory of the installed version, and the run logs the plugin, its
version, and the directory. A plugin directory works as well, for example a
checkout of the plugin at a commit you pinned. A value that names no installed
plugin and no plugin directory stops the run before any session starts, and a
session that does not load a plugin it was given logs a warning. Commands that
start no session ignore the variable: `schema`, `cache`, `gen-man-pages`,
`prompt`, `extract`, `brain memory`, `brain sync`, `brain search`, the removed
command stubs, `help`, `completion` and the hidden commands shell completion
calls, `--version`, and the root's `--cache-stats`, `--cache-inspect`,
`--clear-cache` and `--clear-cache-scope`. Every other command resolves the
plugins and keys its cache by their names and versions, its `--print-prompt` and
`--dry-run` modes and the commands whose sessions are all read-only (`audit`,
`propose`, `elaborate`, and the like) included. Exporting the variable for every
command therefore makes those commands miss the results they cached without it
once; name the plugin on the commands that edit or review if you want to keep
them.

## What changes in the run

| Where | What the plugin does there |
|-------|----------------------------|
| The sessions that edit: implement and its implementer subagents, fix, address, rebase, finalize, and the simplify and review apply sessions | The session loads the plugin with `--plugin-dir`. Its system prompt lists the `impeccable:impeccable` skill, which the session uses when a task falls within the skill's purpose, the way it uses a skill the repository ships. The plugin's hooks run the detector after each edit of a UI file and once more when the session ends. |
| The `ui` review specialist (`review --specialists`, and the first round of `implement`'s review-and-fix pass) | The specialist may read the skill's reference documents, and its prompt carries the detector's matches on the changed UI files as leads. See [The detector's leads](#the-detector-s-leads). |
| Every other read-only session | Nothing. Read-only sessions never load a plugin you name; only `--claude-inherit-user-config` brings in the plugins your user settings enable. |
| The cache key | The plugin's name and version join it, so a run with the plugin never serves a result from a run without it. |

No person watches the sessions that edit, so the system prompt sets five rules
for the plugin, its skill, and its hooks, whatever their own text says:

- A step that needs a person (a question, a choice between variants, a page to
  look at in a browser) is skipped and named in the session's final message.
  `init`, `live`, and the commands that start a new design are such steps.
- No ignore, waiver, or exception that silences one of the plugin's checks is
  added. impeccable's hook asks the agent to record one for a false positive; an
  unattended session fixes the finding or names it in its report instead.
- The working files the plugin writes for itself, such as `.impeccable/`, stay
  out of the commits. impeccable's hook keeps its own cache files out of
  `git status` by adding them to the checkout's `.git/info/exclude`, which
  under `--local` is your repository's.
- When a hook speaks after the session's final message, the session handles it
  and ends that turn with its final message again, in full and updated for what
  it changed. impeccable's end-of-session pass does this: it starts one more turn
  after the report.
- A subagent the session delegates to runs under the same rules, and the
  session carries each step the subagent skipped and each check it left
  standing into its own final message.

What the rules ask a session to name goes inside the shape of its final
message: a section of a report, or the summary of a JSON object.

A session that loads plugins runs with `DO_NOT_TRACK=1`, which turns
impeccable's anonymous usage reporting off.

## The detector's leads

Before the `ui` specialist runs, planwerk-agent runs the detector over the UI
files the branch changed: markup, stylesheets, components, and view templates
(`.html`, `.css`, `.scss`, `.jsx`, `.tsx`, `.vue`, `.svelte`, `.astro`,
`.erb`, `.blade.php`, and the like; plain `.js` and `.ts` files are left out).
It runs `detect --json --no-config` through the launcher the plugin ships, so
the repository's own detector configuration cannot silence a rule for the
files a pull request changes. The scan covers only the files the review diff
keeps (under `--diff-exclusions` it leaves generated and vendored ones out), and
of those only regular files, not symlinks, that stay inside the checkout once
every directory on their path resolves, at most 200 of them in path order, and stops after three
minutes. At most 80 leads reach the prompt, the `quality` ones first, and the
prompt says how many more there were. A plugin whose impeccable skill lacks its
launcher or its reference directory is logged as a warning, and the specialist
then runs without them.

The matches reach the specialist as leads, not findings, each with its category:
`quality` for a defect claim, `slop` for a judgment of visual taste. The
specialist drops a `slop` lead unless the project's documented design decisions
rule on it, and it drops a lead the code refutes, one the project's documented design decisions sanction (a
`DESIGN.md`, a `PRODUCT.md`, a brand guide), and one about computed sizes,
contrast on a layered background, or layout that the source does not show, since
the detector reads files without rendering them. Every other lead the diff causes
it reports as a finding, with a confidence label that says how far the code
bears it out, and those findings pass the same anchoring, snippet, and claim
checks as every other finding. The detector's matches in a file it was not asked
to scan, such as a stylesheet a page links to, are dropped before the prompt.

A detector that fails or times out costs the leads, not the review: the
specialist runs with the guidance alone, and the run logs a warning. The dry run
(`--dry-run`) sizes the specialist's prompt without the design section, the
guidance and the leads alike, since the leads exist only once the detector ran.

## In CI

The id form needs `claude plugin install` on the runner. To pin the version, check
the plugin out at a commit and pass its directory:

```yaml
- uses: actions/checkout@<pinned-sha>
  with:
    repository: pbakaus/impeccable
    ref: <impeccable-commit>
    path: impeccable
- run: planwerk-agent implement "$ISSUE" --claude-plugin "$GITHUB_WORKSPACE/impeccable/plugin"
```

The plugin's launcher downloads its engine binary (about 21 MB) from
impeccable's GitHub releases the first time it runs, and refuses it unless it
matches the checksum file published beside it. To skip the download, put an
engine binary you verified on the runner and point `IMPECCABLE_BIN` at it;
planwerk-agent's sessions and the detector inherit the variable.

The [GitHub Action](/how-to/use-the-github-action) runs the review without
`--specialists`, so the `ui` specialist and its leads do not run there.

## With the skills

The skills run in your own Claude Code session, where an installed impeccable is
already enabled:

- [`/planwerk:implement`](/how-to/implement-an-issue-interactively) names in
  its plan the impeccable command for each UI surface the change touches:
  `harden` for the states an Acceptance Criterion names, `audit` to check the
  result. It runs those, each with its surface as the target (invoked without
  one, impeccable shows a menu instead of running), and treats a design change
  the plan does not name as a deviation to ask you about. A design finding it
  leaves standing goes into its report, never into an ignore, and a hook that
  reports after the push changes nothing pushed without your yes.
- [`/planwerk:review`](/how-to/review-a-change-interactively) leaves visual
  taste to it: when the change touches UI files, its report names
  `/impeccable critique` and `/impeccable audit` on the changed surface, or,
  without impeccable, says the review did not judge how the interface looks.

## Remove it

Drop `--claude-plugin` and `PLANWERK_CLAUDE_PLUGINS` from your runs, then:

```bash
claude plugin uninstall impeccable@impeccable
claude plugin marketplace remove impeccable
rm -rf ~/.impeccable        # the cached engine binary
```
