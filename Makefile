BINARY  := planwerk-agent
MAIN    := ./cmd/planwerk-agent
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.DEFAULT_GOAL := all

.PHONY: all build test vet lint fmt clean completions man eval plugin-validate \
	toolbox-image toolbox-shell toolbox-clean

# ---------------------------------------------------------------------------
# Toolbox
# ---------------------------------------------------------------------------

# The toolbox image (tools/toolbox/Dockerfile) carries every tool the targets
# below expect on PATH: Go, golangci-lint, and the claude CLI. With TOOLBOX=1,
# the default outside CI, `make <target>` builds that image if this checkout's
# tag is missing and then re-runs the same goals inside it, so the host needs
# docker, make, and git, and nothing else. docs/how-to/build-from-source.md is
# the operator guide.
#
# The mode is resolved once, here, and every native target sits in the `else`
# branch of the single conditional at the end of this section, so a new target
# cannot forget to opt in.
#
# TOOLBOX resolves to 0 (native, host tools) when
#   * the caller says so: `make build TOOLBOX=0` or `export TOOLBOX=0`,
#   * make already runs inside the toolbox (PLANWERK_TOOLBOX is baked into the
#     image) — this one overrides even an explicit TOOLBOX=1,
#   * CI is set — the workflows install their pinned tools through setup
#     actions and keep exercising the native recipes.
ifeq ($(origin TOOLBOX),undefined)
ifeq ($(strip $(CI)),)
TOOLBOX := 1
else
TOOLBOX := 0
endif
endif
ifneq ($(strip $(PLANWERK_TOOLBOX)),)
override TOOLBOX := 0
endif

# Go and golangci-lint are read from the files CI reads them from, so the
# toolbox cannot drift from CI. Node matches the plugin job's major.
TOOLBOX_GO_VERSION := $(shell sed -n 's/^go //p' go.mod)
TOOLBOX_GOLANGCI_LINT_VERSION := $(shell sed -n 's/^ *version: *//p' .github/workflows/lint.yml)
TOOLBOX_NODE_VERSION ?= 24
TOOLBOX_CLAUDE_CODE_VERSION ?= 2.1.280

TOOLBOX_BUILD_ARGS := \
	--build-arg GO_VERSION=$(TOOLBOX_GO_VERSION) \
	--build-arg GOLANGCI_LINT_VERSION=$(TOOLBOX_GOLANGCI_LINT_VERSION) \
	--build-arg NODE_VERSION=$(TOOLBOX_NODE_VERSION) \
	--build-arg CLAUDE_CODE_VERSION=$(TOOLBOX_CLAUDE_CODE_VERSION)

# The image tag is a digest over the Dockerfile and every build argument, so
# editing either one yields a new tag and the next `make` rebuilds, while an
# unchanged checkout finds its image by name and skips the build.
TOOLBOX_TAG = $(shell { cat tools/toolbox/Dockerfile; echo '$(TOOLBOX_BUILD_ARGS)'; } 2>/dev/null | { sha256sum 2>/dev/null || shasum -a 256; } | cut -c1-12)
TOOLBOX_IMAGE ?= planwerk-agent-toolbox:$(TOOLBOX_TAG)

# Host variables forwarded into the container: anything the caller set in the
# environment that matches one of these patterns. Variables given on the
# command line travel separately, through MAKEOVERRIDES. Extend the list per
# invocation with TOOLBOX_ENV="FOO BAR".
TOOLBOX_ENV_PATTERNS := PLANWERK_% ANTHROPIC_% CLAUDE_CODE_OAUTH_TOKEN NO_COLOR TERM
toolbox_pass_env = $(strip $(foreach v,$(filter-out PLANWERK_TOOLBOX,$(filter $(TOOLBOX_ENV_PATTERNS) $(TOOLBOX_ENV),$(.VARIABLES))),$(if $(filter environment%,$(origin $(v))),$(v))))

TOOLBOX_RUN = TOOLBOX_IMAGE='$(TOOLBOX_IMAGE)' TOOLBOX_PASS_ENV='$(toolbox_pass_env)' bash tools/toolbox/run.sh

# Builds the toolbox image when this checkout's tag is not present yet. Every
# delegated goal depends on it, so calling it by hand only pre-warms a machine.
toolbox-image:
	@if ! command -v docker >/dev/null 2>&1; then \
		echo "make: docker not found on PATH — the toolbox needs a container runtime." >&2; \
		echo "    install docker, or run natively against host tools with TOOLBOX=0." >&2; \
		exit 127; \
	fi
	@if ! docker image inspect '$(TOOLBOX_IMAGE)' >/dev/null 2>&1; then \
		echo "==> building toolbox image $(TOOLBOX_IMAGE)"; \
		docker build $(TOOLBOX_BUILD_ARGS) --tag '$(TOOLBOX_IMAGE)' \
			--file tools/toolbox/Dockerfile tools/toolbox; \
	fi

toolbox-shell: toolbox-image
	@$(TOOLBOX_RUN) bash

toolbox-clean:
	@docker images --quiet --filter reference='planwerk-agent-toolbox' | sort -u | xargs -r docker rmi --force
	@docker volume rm --force planwerk-agent-toolbox-go planwerk-agent-toolbox-cache >/dev/null

ifeq ($(TOOLBOX),1)

# Delegation mode. Every goal except the toolbox-* targets above collapses
# onto one container run, so `make lint test` pays for one container start and
# keeps its left-to-right order. A bare `make` delegates the default goal.
toolbox_goals := $(filter-out toolbox-image toolbox-shell toolbox-clean,$(or $(MAKECMDGOALS),$(.DEFAULT_GOAL)))

# `build` compiles for the host that asked, not for the Linux container it
# runs in, so the binary it leaves in the checkout runs where `make build` was
# typed — a macOS checkout gets a macOS binary.
BUILD_GOOS ?= $(shell uname -s | tr '[:upper:]' '[:lower:]')
BUILD_GOARCH ?= $(patsubst x86_64,amd64,$(patsubst aarch64,arm64,$(shell uname -m)))

ifneq ($(toolbox_goals),)
.PHONY: toolbox-delegate
$(toolbox_goals): toolbox-delegate
	@:

toolbox-delegate: toolbox-image
	@$(TOOLBOX_RUN) make $(toolbox_goals) BUILD_GOOS=$(BUILD_GOOS) BUILD_GOARCH=$(BUILD_GOARCH) $(MAKEOVERRIDES)
endif

else

# ---------------------------------------------------------------------------
# Native targets
# ---------------------------------------------------------------------------

# The platform `build` compiles for; the toolbox delegation sets it to the
# host's.
BUILD_GOOS ?= $(shell go env GOOS)
BUILD_GOARCH ?= $(shell go env GOARCH)

all: lint test build

build:
	CGO_ENABLED=0 GOOS=$(BUILD_GOOS) GOARCH=$(BUILD_GOARCH) go build -ldflags "$(LDFLAGS)" -o $(BINARY) $(MAIN)

completions:
	mkdir -p completions
	go run $(MAIN) completion bash > completions/$(BINARY).bash
	go run $(MAIN) completion zsh > completions/_$(BINARY)
	go run $(MAIN) completion fish > completions/$(BINARY).fish

man:
	mkdir -p docs/man
	go run $(MAIN) gen-man-pages docs/man

test:
	go test ./...

vet:
	go vet ./...

lint:
	golangci-lint run

# Validate the Claude Code plugin marketplace this repo ships (the clarify /
# draft / elaborate / fix / meta / revisit skills). `go test ./internal/skills` already
# checks that the skills parse and their shared references resolve; this adds the
# manifest schema check that only the claude CLI can do. Skipped when claude is
# absent.
plugin-validate:
	@command -v claude >/dev/null 2>&1 || { echo "claude CLI not found; skipping plugin validation"; exit 0; }
	claude plugin validate --strict .
	claude plugin validate --strict plugins/planwerk

fmt:
	gofmt -s -w .

clean:
	rm -f $(BINARY)
	rm -rf completions docs/man

# Output-quality eval: scores the review pipeline against the seeded-bug corpus.
# Invokes the real claude CLI and spends tokens, so it is deliberately NOT wired
# into `test` or CI. Pass flags via EVAL_ARGS, e.g. `make eval EVAL_ARGS=-json`.
eval:
	go run ./cmd/planwerk-eval $(EVAL_ARGS)

endif # TOOLBOX
