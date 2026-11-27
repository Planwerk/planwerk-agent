# Use diagram-design for diagrams

Load the [diagram-design](https://github.com/cathrynlavery/diagram-design)
plugin into the sessions that write code, so a diagram a change adds is drawn
with it, and let the `diagram` review specialist read its guidance and its
checkers, so a change to a diagram is reviewed against more than the generic
checklist.

diagram-design is a Claude Code plugin: one skill that draws editorial diagrams
of some fifty types (architecture, flowchart, sequence, state machine, ER, and
the like) as self-contained HTML pages with inline SVG, commands that import
Mermaid, PlantUML, draw.io and Excalidraw files and export a page to SVG or
PNG, and two checkers, `self_check.py` and `verify_geometry.py`, that hold a
page to the skill's accessibility contract and connector rules without a model.
The checkers import only Python's standard library, so a run that uses them
needs `python3` on the machine. planwerk-agent uses the copy Claude Code
installed, where Claude Code keeps it. Nothing of diagram-design is copied
into planwerk-agent, so the version you install is the version that runs.

## Install the plugin

```bash
claude plugin marketplace add cathrynlavery/diagram-design
claude plugin install diagram-design@diagram-design
claude plugin list        # shows the id and the installed version
```

As with every plugin, the install reaches your interactive Claude Code
sessions, not planwerk-agent's own: a run loads a plugin only when you name it.
[Use impeccable for UI work](/how-to/use-impeccable-for-ui-work#load-it-into-a-run)
describes how a plugin is named, resolved, and keyed into the cache; all of it
holds for diagram-design.

## Load it into a run

```bash
# implement an issue with the plugin in the sessions that write code
planwerk-agent implement owner/repo#42 --claude-plugin diagram-design@diagram-design

# review a pull request with the specialists, the diagram specialist among them
planwerk-agent owner/repo#123 --specialists --claude-plugin diagram-design@diagram-design

# both plugins, once, for every command
export PLANWERK_CLAUDE_PLUGINS=impeccable@impeccable,diagram-design@diagram-design
```

## What changes in the run

| Where | What the plugin does there |
|-------|----------------------------|
| The sessions that edit: implement and its implementer subagents, fix, address, rebase, finalize, and the simplify and review apply sessions | The session loads the plugin with `--plugin-dir`. Its system prompt lists the `diagram-design:diagram-design` skill and ends with how the session draws a diagram with it. See [Drawing a diagram](#drawing-a-diagram). |
| The `diagram` review specialist (`review --specialists`, and the first round of `implement`'s review-and-fix pass) | The specialist may read the skill's reference documents, and its prompt carries the checkers' reports on the changed HTML diagrams as leads. See [The checkers' leads](#the-checkers-leads). |
| Every other read-only session | Nothing. |
| The cache key | The plugin's name and version join it. |

The five rules the sessions that edit run a plugin under, with no person
watching, apply to diagram-design as to every plugin; [Use impeccable for UI
work](/how-to/use-impeccable-for-ui-work#what-changes-in-the-run) lists them.

## Drawing a diagram

When the task adds a diagram to the repository, the session draws it with the
skill instead of writing Mermaid, ASCII art, or SVG by hand. Three exceptions
keep a repository's own choices:

- A repository that documents a format for its diagrams, in its contributing
  guide or its documentation conventions, keeps that format.
- An existing diagram keeps its format. One the skill drew is changed with the
  skill; one in another format is corrected in that format, and redrawn with
  the skill only when the issue asks for it.
- A pull request description, an issue, or a comment gets no diagram from the
  skill, since GitHub renders none of its files there.

The skill asks a person two things, and an unattended session settles both
itself:

- **The style guide.** Before its first diagram in a project, the skill checks
  whether its style guide still carries the shipped defaults and offers to
  brand it. The session resolves the repository's `.diagram-design` marker, a
  file that names a saved profile, as the skill describes. Without a marker it
  draws with the installed style guide as it stands. It never runs the skill's
  onboarding, edits the style guide, or writes a profile or a marker, and it
  names the profile or skin it drew with in its report. To brand the diagrams
  of a repository, save a profile in an interactive session
  (`/diagram-design:profile`) and commit the marker that names it.
- **The plan.** The skill states the diagram type, the size preset, and what
  its complexity budget cut before it draws. The session puts that plan in its
  report instead of waiting for a reply.

The page goes where the repository keeps its diagrams, or, where it keeps none,
into a `diagrams/` directory beside the document that shows it, so that its
path names it a diagram, which is what the `diagram` specialist's gate looks
for. A Markdown document cannot hold the HTML page: the session exports the
figure as an SVG next to the page, embeds that, and exports it again whenever
the page changes. It makes no other export.

Before it commits a page, the session runs both checkers on it and fixes what
they report, or names in its report what it left standing and why. The skill's
render-and-look step draws a PNG preview with Playwright; the preview goes into
a temporary directory, never into a commit, and where Playwright is missing the
report says the diagram was not visually reviewed.

## The checkers' leads

The `diagram` specialist runs when the branch changes a diagram file: the
source of Mermaid, PlantUML, draw.io, Excalidraw, Graphviz or D2 (`.mmd`,
`.puml`, `.drawio`, `.excalidraw`, `.dot`, `.d2`, and the like), an image
draw.io or Excalidraw embeds its source in (`.drawio.svg`, `.excalidraw.png`),
or an `.svg`, `.html` or `.htm` file whose path contains "diagram". An icon
or a page elsewhere does not start it, and neither does a diagram inside a
Markdown fence, since the gate reads paths alone. It runs with or without the
plugin; the plugin adds guidance and leads.

Before the specialist runs, planwerk-agent runs `self_check.py` and
`verify_geometry.py` over the HTML diagrams among those files, the format the
skill draws. Each checker runs once per file, as `python3 -I`, from the
checkout's top level. The checks cover only the files the review diff keeps,
and of those only regular files that stay inside the checkout once every
directory on their path resolves, at most 100 of them in path order, and stop
after three minutes for all runs together. At most 80 reports reach the prompt,
and the prompt says how many more there were. A plugin whose diagram-design
skill lacks either checker or its reference directory is logged as a warning,
and the specialist then runs without them.

The reports reach the specialist as leads, not findings. It drops a lead the
file refutes, one the project's documented design decisions sanction, and one
that holds a diagram the skill did not draw to the skill's own rules, unless it
names a defect a reader meets anyway: a figure without a text alternative,
content cut off at the viewBox, a label a node covers. Every other lead the
diff causes it reports as a finding, with a confidence label, and those
findings pass the same anchoring, snippet, and claim checks as every other
finding. The skill's palette, typography, and accent rules count as visual
style, which the specialist judges only where the project's design decisions
rule on it.

A checker that fails or times out, or a machine without `python3`, costs the
leads, not the review: the specialist runs with the guidance alone, and the
run logs a warning. The dry run (`--dry-run`) sizes the specialist's prompt
without the guidance section.

## In CI

The id form needs `claude plugin install` on the runner. To pin the version,
check the plugin out at a commit and pass its directory:

```yaml
- uses: actions/checkout@<pinned-sha>
  with:
    repository: cathrynlavery/diagram-design
    ref: <diagram-design-commit>
    path: diagram-design
- run: planwerk-agent implement "$ISSUE" --claude-plugin "$GITHUB_WORKSPACE/diagram-design"
```

The checkers need `python3`, which GitHub's hosted runners carry. The
[GitHub Action](/how-to/use-the-github-action) runs the review without
`--specialists`, so the `diagram` specialist and its leads do not run there.

## With the skills

[`/planwerk:implement`](/how-to/implement-an-issue-interactively) runs in your
own Claude Code session, where an installed diagram-design is already enabled.
With it, the plan names each diagram the change adds: the document that shows
it, its type, and where its page goes. The skill draws each one, and its
style-guide question and its plan go to you, since you are there to answer. A
diagram the plan does not name is a deviation to ask you about. Before the
commit it runs both checkers, and the report lists each diagram and
each problem left standing.

## Remove it

Drop `--claude-plugin` and `PLANWERK_CLAUDE_PLUGINS` from your runs, then:

```bash
claude plugin uninstall diagram-design@diagram-design
claude plugin marketplace remove diagram-design
```
