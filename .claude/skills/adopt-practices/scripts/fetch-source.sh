#!/usr/bin/env bash
# Clones another repository of Claude Code practice (a skill or agent
# collection, a best-practices repository) and says whether an analysis of it
# is due. It checks out the text files only, with symlinks as plain files and
# without LFS content, so nothing in the source can point a reader outside the
# clone; writes an inventory of every text file with its size, kind, and
# title; finds the newest ledger entry under .claude/practice-analyses/ for
# the same URL; and lists the files the source changed and counts the commits
# this repository gained since that entry. It never runs anything the source
# contains and never edits the repository.
#
# Usage: fetch-source.sh <repo-url> <outdir>     from the repository root
# Exit:  0 nothing changed since the last analysis, 3 an analysis is due,
#        1 error.
set -euo pipefail

if [ $# -ne 2 ]; then
  echo "usage: fetch-source.sh <repo-url> <outdir>" >&2
  exit 1
fi
mkdir -p "$2"
outdir=$(cd "$2" && pwd)

root=$(git rev-parse --show-toplevel)
cd "$root"

# The surfaces are the whole repository except the dependency pins, which
# Renovate moves weekly, and the analysis ledgers themselves.
surfaces=(. ':(exclude)go.mod' ':(exclude)go.sum' ':(exclude).claude/practice-analyses')

# One spelling per repository, so a ledger entry matches however the URL was
# typed: https, no .git, no trailing slash, no tree or blob view of a path.
url=${1%%#*}
url=${url%%\?*}
case "$url" in
  git@*:*)
    hostpath=${url#git@}
    url="https://${hostpath%%:*}/${hostpath#*:}"
    ;;
esac
url=${url%%/tree/*}
url=${url%%/blob/*}
url=${url%%/-/*}
url=${url%/}
url=${url%.git}

slug=$(printf '%s\n' "$url" | awk -F/ '{print $(NF-1) "-" $NF}' |
  tr '[:upper:]' '[:lower:]' | tr -c 'a-z0-9\n' '-')
clone="$outdir/$slug"

# Symlinks become files holding their target, and LFS pointers stay pointers:
# a reader of the clone reads the source's text and nothing beside it.
export GIT_LFS_SKIP_SMUDGE=1 GIT_TERMINAL_PROMPT=0
src() { git -C "$clone" -c core.symlinks=false "$@"; }

if [ -d "$clone/.git" ]; then
  timeout 300 git -C "$clone" fetch --quiet origin
  git -C "$clone" remote set-head origin --auto >/dev/null
else
  if ! timeout 300 git -c core.symlinks=false clone --quiet --filter=blob:none \
    --no-checkout "$url" "$clone"; then
    echo "error: could not clone $url" >&2
    exit 1
  fi
fi
commit=$(src rev-parse --verify 'origin/HEAD^{commit}')

# The text a reader can analyze, by extension; a name without one is kept
# (LICENSE, Makefile). Images, audio, and fonts never leave the server.
text_re='(^|/)[^./]+$|\.(md|mdx|markdown|txt|rst|json|jsonc|ya?ml|toml|sh|bash|zsh|fish|ps1|py|js|mjs|cjs|ts|mts|go|rb|lua)$'
src ls-tree -r --name-only "$commit" >"$outdir/$slug.tree"
grep -iE "$text_re" "$outdir/$slug.tree" >"$outdir/$slug.text" || true

# A sparse pattern is gitignore syntax: anchor every path with "/" (a leading
# "!" would negate it) and escape the glob characters.
sed -e 's/[][*?\\]/\\&/g' -e 's|^|/|' "$outdir/$slug.text" |
  src sparse-checkout set --no-cone --stdin
src checkout --quiet --detach "$commit"

inventory="$outdir/$slug.inventory.tsv"
printf 'path\tbytes\tlines\tkind\ttitle\n' >"$inventory"
while IFS= read -r p; do
  f="$clone/$p"
  [ -f "$f" ] || continue
  case "$p" in
    SKILL.md | */SKILL.md) kind=skill ;;
    skills/* | */skills/*) kind=skill-file ;;
    agents/* | */agents/*) kind=agent ;;
    commands/* | */commands/*) kind=command ;;
    CLAUDE.md | */CLAUDE.md | CLAUDE.local.md | */CLAUDE.local.md | \
      AGENTS.md | */AGENTS.md | .claude/rules/* | */.claude/rules/*) kind=memory ;;
    *hooks*) kind=hook ;;
    *settings*.json | .mcp.json | */.mcp.json) kind=settings ;;
    .claude-plugin/* | */.claude-plugin/*) kind=plugin ;;
    *.md | *.mdx | *.markdown | *.txt | *.rst) kind=doc ;;
    *.sh | *.bash | *.zsh | *.fish | *.ps1 | *.py | *.js | *.mjs | *.cjs | \
      *.ts | *.mts | *.go | *.rb | *.lua) kind=script ;;
    *) kind=other ;;
  esac
  title=""
  case "$p" in
    *.md | *.mdx | *.markdown)
      title=$(awk '/^# /{sub(/^# +/, ""); print; exit}' "$f" | tr '\t' ' ' | cut -c1-80)
      ;;
  esac
  printf '%s\t%s\t%s\t%s\t%s\n' "$p" "$(wc -c <"$f")" "$(wc -l <"$f")" "$kind" "$title" >>"$inventory"
done <"$outdir/$slug.text"

license=none
for l in LICENSE LICENSE.md LICENSE.txt LICENCE COPYING; do
  if [ -f "$clone/$l" ]; then
    license=$(awk 'NF{sub(/^[ \t]+/, ""); print; exit}' "$clone/$l")
    break
  fi
done

cc=$(claude --version 2>/dev/null | awk '{print $1}' || true)

echo "clone: $clone (text files only; never a working directory)"
echo "url=$url"
echo "slug=$slug"
echo "commit=$commit"
echo "commit_date=$(src log -1 --format=%cs "$commit")"
echo "license=$license"
echo "claude_code=${cc:-unknown}"
echo "inventory=$inventory"
echo "files: $(wc -l <"$outdir/$slug.tree") in the tree, $(($(wc -l <"$inventory") - 1)) text files checked out"
echo "text by top-level directory (files, bytes):"
awk -F'\t' 'NR > 1 {
    n = split($1, part, "/")
    top = (n == 1) ? "(root)" : (part[1] == ".claude" && n > 2) ? part[1] "/" part[2] : part[1]
    files[top]++; bytes[top] += $2
  }
  END { for (t in files) printf "  %-40s %5d %10d\n", t, files[t], bytes[t] }' "$inventory" | sort
echo "text by kind (files, bytes):"
awk -F'\t' 'NR > 1 { files[$4]++; bytes[$4] += $2 }
  END { for (k in files) printf "  %-40s %5d %10d\n", k, files[k], bytes[k] }' "$inventory" | sort

ledger=$(grep -lxF "url: $url" .claude/practice-analyses/*.md 2>/dev/null | sort | tail -1 || true)
if [ -z "$ledger" ]; then
  echo "ledger: none for this url under .claude/practice-analyses/"
  echo "state=new"
  exit 3
fi
echo "ledger: $ledger"

field() { sed -n "s/^$1: *//p" "$ledger" | head -1; }
last_commit=$(field commit)
echo "last_commit=$last_commit"

source_changed=no
if [ "$last_commit" != "$commit" ]; then
  source_changed=yes
  if src rev-parse --verify --quiet "$last_commit^{commit}" >/dev/null; then
    changed="$outdir/$slug.changed.tsv"
    src diff --no-renames --name-status "$last_commit" "$commit" |
      TEXT_RE=$text_re awk -F'\t' 'tolower($2) ~ ENVIRON["TEXT_RE"]' >"$changed" || true
    echo "changed=$changed ($(wc -l <"$changed") text files added, modified, or deleted)"
  else
    echo "changed=unknown (the last commit is gone from the source's history)"
  fi
fi

# The analysis's own commit lands after the commit it read, so the delta
# starts at the ledger entry's commit; an entry not yet committed falls back
# to the head it names.
base=$(git log -1 --format=%h -- "$ledger" 2>/dev/null || true)
if [ -z "$base" ]; then
  base=$(field head)
fi
commits=unknown
if git rev-parse --verify --quiet "$base^{commit}" >/dev/null; then
  commits=$(git rev-list --count "$base..HEAD" -- "${surfaces[@]}")
fi
echo "commits since the ledger entry ($base): $commits"

if [ "$source_changed" = yes ]; then
  echo "state=source-changed"
  exit 3
fi
if [ "$commits" != "0" ]; then
  echo "state=repo-changed"
  exit 3
fi
echo "state=unchanged"
exit 0
