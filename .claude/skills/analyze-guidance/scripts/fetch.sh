#!/usr/bin/env bash
# Fetches one page of vendor guidance and says whether an analysis of it is
# due. It saves the page as Markdown (the documentation hosts serve a page's
# Markdown at its URL plus ".md"), keeps a copy keyed by its hash under the
# user cache so a later run can diff two versions, finds the newest ledger
# entry under .claude/guidance-analyses/ for the same URL, and counts the
# commits that touched the repository since that entry landed. It never runs
# anything the page says and never edits the repository.
#
# Usage: fetch.sh <url> <outdir>     from the repository root
# Exit:  0 nothing changed since the last analysis, 3 an analysis is due,
#        1 error.
set -euo pipefail

if [ $# -ne 2 ]; then
  echo "usage: fetch.sh <url> <outdir>" >&2
  exit 1
fi
outdir=$2

root=$(git rev-parse --show-toplevel)
cd "$root"

# The surfaces are the whole repository except the dependency pins, which
# Renovate moves weekly, and the analysis ledgers themselves.
surfaces=(. ':(exclude)go.mod' ':(exclude)go.sum' ':(exclude).claude/guidance-analyses')

# One spelling per page, so a ledger entry matches however the URL was typed.
url=${1%%#*}
url=${url%%\?*}
url=${url%/}
url=${url%.md}

src=$url
case "$url" in
  https://code.claude.com/docs/* | https://docs.claude.com/* | \
  https://docs.anthropic.com/* | https://platform.claude.com/docs/*)
    src="$url.md"
    ;;
esac

slug=$(basename "$url" | tr '[:upper:]' '[:lower:]' | tr -c 'a-z0-9\n' '-')
mkdir -p "$outdir"
page="$outdir/$slug.md"

ctype=$(curl -fsSL --max-time 60 -o "$page" -w '%{content_type}' "$src" 2>/dev/null || true)
if [ ! -s "$page" ] && [ "$src" != "$url" ]; then
  src=$url
  ctype=$(curl -fsSL --max-time 60 -o "$page" -w '%{content_type}' "$src" 2>/dev/null || true)
fi
if [ ! -s "$page" ]; then
  echo "error: could not fetch $src" >&2
  exit 1
fi

sha=$(sha256sum "$page" | cut -c1-64)
cache="${XDG_CACHE_HOME:-$HOME/.cache}/planwerk-agent/guidance/$slug"
mkdir -p "$cache"
cp "$page" "$cache/$sha.md"

cc=$(claude --version 2>/dev/null | awk '{print $1}' || true)

echo "page: $page ($(wc -c <"$page") bytes, $ctype, from $src)"
echo "url=$url"
echo "slug=$slug"
echo "sha256=$sha"
echo "claude_code=${cc:-unknown}"

ledger=$(grep -lxF "url: $url" .claude/guidance-analyses/*.md 2>/dev/null | sort | tail -1 || true)
if [ -z "$ledger" ]; then
  echo "ledger: none for this url under .claude/guidance-analyses/"
  echo "state=new"
  exit 3
fi
echo "ledger: $ledger"

field() { sed -n "s/^$1: *//p" "$ledger" | head -1; }
last_sha=$(field sha256)
echo "last_sha256=$last_sha"
if [ -f "$cache/$last_sha.md" ] && [ "$last_sha" != "$sha" ]; then
  echo "previous=$cache/$last_sha.md"
else
  echo "previous=none"
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

if [ "$sha" != "$last_sha" ]; then
  echo "state=page-changed"
  exit 3
fi
if [ "$commits" != "0" ]; then
  echo "state=repo-changed"
  exit 3
fi
echo "state=unchanged"
exit 0
