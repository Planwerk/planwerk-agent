#!/usr/bin/env bash

# run.sh starts the planwerk-agent toolbox container around the checkout in
# the current directory and runs the given command inside it. The Makefile
# is the caller; docs/how-to/build-from-source.md is the operator guide.
#
#   TOOLBOX_IMAGE=planwerk-agent-toolbox:<tag> tools/toolbox/run.sh make build
#
# Environment:
#   TOOLBOX_IMAGE     image reference to run (required)
#   TOOLBOX_PASS_ENV  space-separated names of host variables to forward
#   TOOLBOX_RUN_ARGS  extra `docker run` arguments, word-split
#   TOOLBOX_DRY_RUN   when 1, print the docker command instead of running it

set -euo pipefail

: "${TOOLBOX_IMAGE:?run.sh: TOOLBOX_IMAGE must name the toolbox image}"
if [ "$#" -eq 0 ]; then
	echo "run.sh: no command given" >&2
	exit 64
fi

repo=$(pwd -P)

# The checkout is mounted at its host path, not at a fixed /src: a linked
# git worktree's .git file names its object store by absolute host path,
# and so does the store's record of the worktree, so git inside the
# container only finds either when the paths match. It also keeps file
# paths in compiler and test output clickable on the host.
#
# The container runs as the invoking user, so build output in the bind
# mount stays owned by them.
args=(run --rm --init -i
	-v "$repo:$repo" -w "$repo"
	-v planwerk-agent-toolbox-go:/go
	-v planwerk-agent-toolbox-cache:/cache
	--user "$(id -u):$(id -g)")

if [ -t 0 ] && [ -t 1 ]; then
	args+=(-t)
fi

common=$(git rev-parse --path-format=absolute --git-common-dir 2>/dev/null || true)
case "$common" in
"" | "$repo" | "$repo"/*) ;;
*) args+=(-v "$common:$common") ;;
esac

for name in ${TOOLBOX_PASS_ENV:-}; do
	args+=(-e "$name")
done

# shellcheck disable=SC2206 # word-splitting is the documented contract
args+=(${TOOLBOX_RUN_ARGS:-})

if [ "${TOOLBOX_DRY_RUN:-0}" = "1" ]; then
	printf 'docker'
	printf ' %q' "${args[@]}" "$TOOLBOX_IMAGE" "$@"
	printf '\n'
	exit 0
fi

exec docker "${args[@]}" "$TOOLBOX_IMAGE" "$@"
