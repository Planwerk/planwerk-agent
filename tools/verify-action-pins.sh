#!/usr/bin/env bash
# Verifies that every external action the composite action (action.yml) and
# this repository's workflows reference is pinned to a full 40-hex commit SHA
# followed by a "# vX.Y.Z" comment. A tag is a pointer its owner can move: a
# consumer who SHA-pins Planwerk/planwerk-agent is only frozen if the
# references inside action.yml are frozen too, and the release workflow runs
# its actions with contents: write and an OIDC token. Two forms are allowed
# besides a pin: a local "./" path, and the SLSA generator on its vX.Y.Z tag.
# The lint workflow's action-pins job and `make lint` run it.
#
# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 alibaba/open-code-review Contributors
# Adapted from alibaba/open-code-review scripts/verify-action-pins.sh
# (alibaba/open-code-review@375f64c, Apache-2.0).
#
# Usage: bash tools/verify-action-pins.sh   (it checks the checkout it sits in)
# Exit:  0 every reference is pinned; 1 an unpinned reference (each is
#        printed as file:line) or a missing file.
set -euo pipefail

cd "$(dirname "$0")/.."

# nullglob drops a glob that matches nothing instead of leaving it as a
# literal the missing-file check below would trip on. GitHub runs both .yml
# and .yaml workflow files.
shopt -s nullglob
files=(action.yml .github/workflows/*.yml .github/workflows/*.yaml)
shopt -u nullglob

# The directive filter takes a line that holds a `uses` key in any of these
# forms: first on the line or after "- ", inside a flow mapping or sequence
# ("- {name: x, uses: ...}", "steps: [{uses: ...}]"), after an anchor or a
# tag ("- &step uses: ..."), quoted ("uses":), or as an explicit key
# ("? uses") whose ":" opens the next line. The patterns below accept only
# the plain block form, so every other one fails closed instead of slipping
# past the filter. Comment lines are skipped; any other line with " uses:"
# in it, a run: script line included, is taken for a key and fails too.
directive="(^|[[:space:]{,[])['\"]?uses['\"]?[[:space:]]*:|(^|[[:space:]])[?][[:space:]]+['\"]?uses['\"]?[[:space:]]*\$"
comment='^[[:space:]]*#'
# The whole line must be the pinned form: list or indent syntax, a 40-hex
# SHA, and a full "# vX.Y.Z" comment with nothing after it. Without the
# anchors a tag would pass whenever a pinned-looking fragment sat in a
# trailing comment, and "# v7" would satisfy a check that names vX.Y.Z.
pinned='^[[:space:]]*(-[[:space:]]+)?uses:[[:space:]]+[A-Za-z0-9_.-]+/[A-Za-z0-9_./-]+@[0-9a-f]{40}[[:space:]]+#[[:space:]]*v[0-9]+\.[0-9]+\.[0-9]+[[:space:]]*$'
local_ref='^[[:space:]]*(-[[:space:]]+)?uses:[[:space:]]*\./'
# slsa-verifier verifies the generator's ref, which works only for a vX.Y.Z
# tag; the generator's build fails when it is called by a SHA.
slsa_tag='^[[:space:]]*(-[[:space:]]+)?uses:[[:space:]]+slsa-framework/slsa-github-generator/\.github/workflows/[A-Za-z0-9_.-]+@v[0-9]+\.[0-9]+\.[0-9]+[[:space:]]*$'

bad=""
for file in "${files[@]}"; do
  if [ ! -f "$file" ]; then
    echo "ERROR: $file not found; the pin check cannot run." >&2
    exit 1
  fi
  hits="$(grep -nE "$directive" "$file" || true)"
  [ -n "$hits" ] || continue
  while IFS= read -r line; do
    # Strip the NN: prefix grep -n added; the patterns anchor on the line.
    value="${line#*:}"
    if printf '%s' "$value" | grep -qE "$comment"; then
      continue
    fi
    if printf '%s' "$value" | grep -qE "$local_ref"; then
      continue
    fi
    if printf '%s' "$value" | grep -qE "$slsa_tag"; then
      continue
    fi
    if ! printf '%s' "$value" | grep -qE "$pinned"; then
      bad="${bad}${file}:${line}"$'\n'
    fi
  done <<< "$hits"
done

if [ -n "$bad" ]; then
  echo "These action references are not pinned to a full commit SHA with a"
  echo "'# vX.Y.Z' comment:"
  printf '%s' "$bad"
  echo "Pin them like: uses: owner/repo@<40-hex-sha> # vX.Y.Z"
  exit 1
fi

echo "OK: every external action reference in ${files[*]} is pinned to a commit SHA."
