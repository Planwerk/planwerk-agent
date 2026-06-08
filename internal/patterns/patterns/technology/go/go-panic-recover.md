# Review Pattern: Go Panic and Recover Usage

**Review-Area**: quality
**Detection-Hint**: `panic()` used for recoverable errors, `recover()` outside deferred function, missing recover in server goroutines, panic in library code
**Severity**: CRITICAL
**Category**: technology
**Applies-When**: go
**Sources**: Effective Go (https://go.dev/doc/effective_go#panic), Effective Go (https://go.dev/doc/effective_go#recover), Google Go Style Guide — Decisions (https://google.github.io/styleguide/go/decisions#panic), Go Proverbs (https://go-proverbs.github.io/), Go Code Review Comments (https://go.dev/wiki/CodeReviewComments#dont-panic)

## What to check

1. `panic` must not be used for ordinary error handling — return an `error` value instead; panic is reserved for truly unrecoverable situations (impossible states, failed invariants during init)
2. Library code must never panic for recoverable conditions — it terminates the caller's program; always return errors and let the caller decide
3. Server goroutines (HTTP handlers, queue consumers) should wrap their work in a deferred `recover` to isolate failures — one panicking request must not crash the entire server
4. `recover()` only works inside a deferred function — calling it outside a defer is a no-op that returns nil
5. After `recover`, log the panic value and stack trace for diagnosis — silently swallowing panics hides bugs
6. `init()` functions may use panic for fatal configuration errors (e.g., missing required environment variables) since the program cannot proceed

## Why it matters

Unrecovered panics terminate the entire process. In servers, a single panicking goroutine takes down all active connections. Library panics are especially dangerous because they violate the caller's error-handling contract. Proper recover boundaries keep failures isolated.
