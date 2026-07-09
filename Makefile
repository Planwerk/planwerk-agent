BINARY  := planwerk-agent
MAIN    := ./cmd/planwerk-agent
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: all build test vet lint fmt clean completions man eval plugin-validate

all: lint test build

build:
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o $(BINARY) $(MAIN)

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

# Validate the Claude Code plugin marketplace this repo ships (the draft /
# elaborate / meta skills). `go test ./internal/skills` already checks that the
# skills parse and their shared references resolve; this adds the manifest
# schema check that only the claude CLI can do. Skipped when claude is absent.
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
