#!/usr/bin/env bash
# Says whether a prompt iteration is due. It compares today's Claude Code
# version and alias resolution (probe-models.sh) with the newest ledger entry
# under .claude/prompt-audits/, and counts the commits that touched a prompt
# surface since that entry landed (the entry's own commit; its head: line
# names the older commit the audit read). It calls the model only through the
# probe (about $0.11) and never edits anything.
#
# Usage: check.sh            from the repository root
# Exit:  0 nothing changed, 3 an iteration is due, 1 error.
set -euo pipefail

root=$(git rev-parse --show-toplevel)
cd "$root"
here=$(cd "$(dirname "$0")" && pwd)

surfaces=(internal/claude plugins .claude docs/explanation/prompt-design.md internal/skills)

ledger=$(ls .claude/prompt-audits/*.md 2>/dev/null | sort | tail -1 || true)
if [ -z "$ledger" ]; then
  echo "ledger: none (no file under .claude/prompt-audits/)"
  echo "due=yes reason=no-ledger"
  exit 3
fi
echo "ledger: $ledger"

field() { sed -n "s/^$1: *//p" "$ledger" | head -1; }
last_head=$(field head)
last_cc=$(field claude_code)
last_models=$(field models)

probe=$("$here/probe-models.sh")
now_cc=$(printf '%s\n' "$probe" | sed -n 's/^claude_code=//p')
now_models=$(printf '%s\n' "$probe" | grep -v '^claude_code=' | tr '\n' ' ' | sed 's/ $//')

reasons=()
if [ "$now_cc" != "$last_cc" ]; then
  reasons+=("claude-code:$last_cc->$now_cc")
fi
if [ "$now_models" != "$last_models" ]; then
  reasons+=("models:[$last_models]->[$now_models]")
fi

# The iteration's own commits land after the commit the audit read, so the
# delta starts at the ledger entry's commit; an entry not yet committed falls
# back to the head it names.
base=$(git log -1 --format=%h -- "$ledger" 2>/dev/null || true)
if [ -z "$base" ]; then
  base=$last_head
fi
commits=0
if git rev-parse --verify --quiet "$base^{commit}" >/dev/null; then
  commits=$(git rev-list --count "$base..HEAD" -- "${surfaces[@]}")
  if [ "$commits" -gt 0 ]; then
    reasons+=("commits-on-surfaces:$commits")
  fi
else
  reasons+=("ledger-commit-unknown:$base")
fi

echo "claude_code: last=$last_cc now=$now_cc"
echo "models: last=[$last_models] now=[$now_models]"
echo "commits on the prompt surfaces since the ledger entry ($base): $commits"

if [ ${#reasons[@]} -eq 0 ]; then
  echo "due=no"
  exit 0
fi
echo "due=yes reason=$(IFS=,; echo "${reasons[*]}")"
exit 3
