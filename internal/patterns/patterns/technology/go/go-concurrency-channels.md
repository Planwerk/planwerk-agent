# Review Pattern: Go Concurrency and Channel Patterns

**Review-Area**: quality
**Detection-Hint**: Shared mutable state protected by mutexes instead of channels, unbounded goroutine creation, missing select-default for non-blocking operations, channel direction not restricted in function signatures
**Severity**: WARNING
**Category**: technology
**Applies-When**: go
**Sources**: Effective Go (https://go.dev/doc/effective_go#sharing), Effective Go (https://go.dev/doc/effective_go#channels), Effective Go (https://go.dev/doc/effective_go#parallel), Google Go Style Guide — Decisions (https://google.github.io/styleguide/go/decisions#goroutine-lifetimes), Rob Pike — Go Concurrency Patterns (https://go.dev/talks/2012/concurrency.slide), The Go Memory Model (https://go.dev/ref/mem)

## What to check

1. Prefer "share memory by communicating" — if data is passed between goroutines via channels, no mutex is needed; if mutexes guard shared state, consider whether a channel-based design is clearer
2. Unbounded goroutine creation (`go handle(req)` in a loop) causes resource exhaustion under load — use a fixed worker pool reading from a channel or a buffered channel as a semaphore
3. Function signatures should restrict channel direction (`chan<-` send-only, `<-chan` receive-only) to make intent explicit and catch misuse at compile time
4. Use `select` with a `default` case for non-blocking channel operations (e.g., returning a buffer to a pool without blocking when the pool is full)
5. Completion signaling should use a channel (`done <- struct{}{}`) or `sync.WaitGroup`, not polling or sleep loops
6. Buffered channels act as semaphores — capacity limits concurrency; unbuffered channels synchronize sender and receiver

## Why it matters

Go's concurrency model is built on CSP (Communicating Sequential Processes). Using channels instead of shared memory eliminates data races by design. Unbounded goroutine creation is a common production incident cause — worker pools and semaphore channels prevent resource exhaustion under load.
