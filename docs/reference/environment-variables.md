# Environment variables & exit codes

## Environment variables

Each `PLANWERK_*` variable overrides a compiled-in default and is itself
overridden by the matching command-line flag. See
[Configuration file → Precedence](/reference/configuration#precedence) for the
full resolution order.

| Variable | Overrides | Notes |
|----------|-----------|-------|
| `PLANWERK_MAX_PATTERNS` | `--max-patterns` | Integer; `<=0` disables truncation. Config-file value takes precedence over this variable. |
| `PLANWERK_REMOTE_PATTERNS_TTL` | `--remote-patterns-ttl` | Duration (e.g. `24h`); `<=0` disables refresh once cached. |
| `PLANWERK_SHOW_CLAUDE_OUTPUT` | `--show-claude-output` | Truthy values enable streaming: `1`, `true`, `yes`, `on` (case-insensitive). |
| `PLANWERK_CLAUDE_TIMEOUT` | `--claude-timeout` | Duration (e.g. `20m`, `1h30m`); must be `> 0`. |
| `PLANWERK_CLAUDE_MODEL` | `--claude-model` | Model alias or full ID passed to Claude Code via `--model`. |
| `PLANWERK_CLAUDE_EFFORT` | `--claude-effort` | One of `low`, `medium`, `high`, `xhigh`, `max`. |
| `PLANWERK_CLAUDE_INHERIT_USER_CONFIG` | `--claude-inherit-user-config` | Truthy values let sessions inherit user-global `~/.claude` config: `1`, `true`, `yes`, `on` (case-insensitive). Off by default (hermetic). |
| `PLANWERK_PLAN_MODEL` | `--plan-model` (`implement`) | Model for the planning session. |
| `PLANWERK_PLAN_EFFORT` | `--plan-effort` (`implement`) | Reasoning effort for the planning session. |
| `PLANWERK_WIKI` | `--wiki` / `--no-wiki` | Truthy values (`1`, `true`, `yes`, `on`) enable the GitHub Wiki knowledge source; falsy values (`0`, `false`, `no`, `off`) disable it. Config-file `wiki.enabled` takes precedence over this variable; the flags take precedence over both. |
| `PLANWERK_WIKI_REF` | `--wiki-ref` | Pins the wiki to a branch, tag, or commit. Empty uses the wiki's default branch. |
| `PLANWERK_CAPTURE_WIKI` | `--capture-wiki` (`implement`) | Truthy values (`1`, `true`, `yes`, `on`) push the accepted capture pages to the wiki; falsy values (`0`, `false`, `no`, `off`) keep the run propose-only. Config-file `capture.wiki` takes precedence over this variable; the flag takes precedence over both. Off by default. |

### Credentials

| Variable | Used by | Notes |
|----------|---------|-------|
| `GH_TOKEN` | `gh` CLI | Authenticates repo clones (including private), PR/issue metadata, checkout, and the GitHub API. Used in place of `gh auth login` in CI. |
| `ANTHROPIC_API_KEY` | Claude Code | Required when Claude Code runs in non-interactive mode (e.g. in the GitHub Action). |

### Editor

| Variable | Used by | Notes |
|----------|---------|-------|
| `VISUAL` | `draft` | Editor opened by the composer's `Ctrl-E` escape. Takes precedence over `$EDITOR`. May include flags (e.g. `code --wait`). |
| `EDITOR` | `draft` | Fallback editor when `$VISUAL` is unset. If neither is set, `draft` uses `vi`. |

## Exit codes

| Code | Meaning |
|------|---------|
| `0` | Success |
| `1` | Error — the failure is logged to stderr (honoring `--log-format`) before exit |
