# Generate a fix/implement prompt

Generate a copy-paste-ready Claude Code prompt for an existing GitHub issue —
either to fix an audit finding or to implement a proposal/elaborated issue. No
Claude call is involved; the prompt is a deterministic assembly so the output is
stable and safe to pipe into other tools.

```bash
# Auto-detected mode (audit titles get the fix prompt, others the implement prompt)
planwerk-review prompt https://github.com/owner/repo/issues/42

# Force the fix variant
planwerk-review prompt --mode fix owner/repo#42

# Force the implement variant
planwerk-review prompt --mode implement owner/repo#42

# Pipe straight into the clipboard (macOS)
planwerk-review prompt owner/repo#42 | pbcopy
```

Mode auto-detection looks at the issue body: audit-generated issues carry a
`**Severity**:` marker and get the "fix" prompt, everything else gets the
"implement" prompt. See the [CLI reference](/reference/cli#prompt) for the
`--mode` flag.

## How it works

1. **Issue Input**: A GitHub issue reference (URL or `owner/repo#number`).
2. **Fetch Issue**: Title, body, URL, and state are fetched via `gh issue view`.
3. **Mode Selection**: `auto` (default) inspects the issue body — audit findings carry a `**Severity**:` marker and get the "fix" prompt; everything else gets the "implement" prompt. Override with `--mode fix` or `--mode implement`.
4. **Prompt Assembly**: The runner deterministically assembles a prompt containing the agent workflow, rules (no scope creep, no `--no-verify`, run tests, update docs), and the issue metadata + body. No Claude call is made — the output is reproducible so it can be piped into other tools or diffed over time.
5. **Output**: The prompt is written to stdout, ready to paste into Claude Code or any other AI coding agent.
