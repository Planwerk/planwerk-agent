#!/usr/bin/env bash
# Prints the Claude Code version and the model id each alias planwerk-agent
# passes resolves to today, one `key=value` line each, so an iteration can
# compare them with the newest ledger entry. Runs one one-word session per
# alias with no tools, no hooks, no settings, and no MCP servers; the three
# sessions cost about $0.11 in total (2026-10-07).
#
# Usage: probe-models.sh [alias ...]   (default: opus sonnet fable)
set -euo pipefail

aliases=("$@")
if [ ${#aliases[@]} -eq 0 ]; then
  aliases=(opus sonnet fable)
fi

printf 'claude_code=%s\n' "$(claude --version | awk '{print $1}')"

for alias in "${aliases[@]}"; do
  out=$(claude -p --model "$alias" --output-format json --tools "" \
    --settings '{"disableAllHooks":true}' --setting-sources "" \
    --strict-mcp-config "Reply with exactly the word ok" 2>/dev/null) || {
    printf '%s=error\n' "$alias"
    continue
  }
  model=$(printf '%s' "$out" | python3 -I -c '
import json, sys
d = json.load(sys.stdin)
keys = list((d.get("modelUsage") or {}).keys())
print(keys[0] if keys else "unknown")
')
  printf '%s=%s\n' "$alias" "$model"
done
