# check=skip=InvalidDefaultArgInFrom

# planwerk-agent toolbox image.
#
# Carries every tool a `make` target expects on PATH, so a checkout needs
# nothing on the host beyond a container runtime, `make`, and `git`. The
# Makefile builds this image on demand (`make toolbox-image`) and re-runs
# every target inside it unless TOOLBOX=0 is set.
#
# No ARG below carries a default. The Makefile hands every version in: Go
# from go.mod and golangci-lint from .github/workflows/lint.yml, the files
# CI reads them from, and the rest from its TOOLBOX_*_VERSION variables.
# A bare `docker build` therefore fails loudly instead of producing an
# image whose tools drifted from CI.

ARG GO_VERSION
ARG NODE_VERSION

FROM node:${NODE_VERSION}-trixie-slim AS node

# The Debian release is trixie, not bookworm: bookworm's git 2.39 rejects
# `git checkout --end-of-options <ref>`, which internal/patterns runs and
# tests, and CI's runners carry a newer git than that.
FROM golang:${GO_VERSION}-trixie AS toolbox

ARG TARGETARCH
ARG GOLANGCI_LINT_VERSION
ARG CLAUDE_CODE_VERSION

SHELL ["/bin/bash", "-euo", "pipefail", "-c"]

# golangci-lint comes from its release binary, verified against the
# checksum file upstream publishes next to it.
WORKDIR /tmp/fetch
RUN version="${GOLANGCI_LINT_VERSION#v}" \
 && asset="golangci-lint-${version}-linux-${TARGETARCH}" \
 && base="https://github.com/golangci/golangci-lint/releases/download/${GOLANGCI_LINT_VERSION}" \
 && curl -fsSLO "${base}/${asset}.tar.gz" \
 && curl -fsSL "${base}/golangci-lint-${version}-checksums.txt" | grep " ${asset}.tar.gz\$" | sha256sum -c - \
 && tar -xzf "${asset}.tar.gz" "${asset}/golangci-lint" \
 && install -m 0755 "${asset}/golangci-lint" /usr/local/bin/golangci-lint \
 && rm -rf /tmp/fetch

# The claude CLI is what `make plugin-validate` validates with and what
# `make eval` drives. Its postinstall script copies the native binary over
# a placeholder, so it is allowed by name; npm is moving to refusing
# install scripts that are not. The auto-updater is off: the version is
# the pin, and the install directory is not writable by the container user.
COPY --from=node /usr/local/bin/node /usr/local/bin/node
COPY --from=node /usr/local/lib/node_modules /usr/local/lib/node_modules
RUN ln -s ../lib/node_modules/npm/bin/npm-cli.js /usr/local/bin/npm \
 && npm install --global --no-audit --no-fund --no-update-notifier \
      --cache /tmp/npm-cache --allow-scripts=@anthropic-ai/claude-code \
      "@anthropic-ai/claude-code@${CLAUDE_CODE_VERSION}" \
 && rm -rf /tmp/npm-cache

# The Makefile runs the container as the invoking host user, a uid this
# image has never heard of. HOME and the cache root are therefore
# world-writable with the sticky bit, the way the base image already
# treats /go; a fresh named volume inherits the mode of its mount point.
# git refuses a repository whose owner is not the running uid, and a bind
# mount does not promise they match, so every directory is marked safe.
RUN mkdir -p /home/toolbox /cache/go-build /cache/golangci-lint \
 && chmod 1777 /home/toolbox /cache /cache/go-build /cache/golangci-lint \
 && git config --system --add safe.directory '*'

# PLANWERK_TOOLBOX is what the Makefile keys on to run targets natively
# instead of delegating into yet another container.
ENV PLANWERK_TOOLBOX=1 \
    HOME=/home/toolbox \
    GOCACHE=/cache/go-build \
    GOLANGCI_LINT_CACHE=/cache/golangci-lint \
    GOTOOLCHAIN=local \
    DISABLE_AUTOUPDATER=1 \
    npm_config_update_notifier=false

WORKDIR /
CMD ["/bin/bash"]
