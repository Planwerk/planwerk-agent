# Review Pattern: Go Goroutine Leaks

**Review-Area**: quality
**Detection-Hint**: Goroutines started with `go func()` without cancellation mechanism, missing context propagation, unbuffered channels without readers
**Severity**: CRITICAL
**Category**: technology
**Applies-When**: go
**Sources**: Go Concurrency Patterns (https://go.dev/blog/pipelines), Effective Go (https://go.dev/doc/effective_go#goroutines), Google Go Style Guide — Decisions (https://google.github.io/styleguide/go/decisions#goroutine-lifetimes), Rob Pike — Go Concurrency Patterns (https://go.dev/talks/2012/concurrency.slide), Uber Go Style Guide (https://github.com/uber-go/guide/blob/master/style.md)

## What to check

1. Every goroutine must have a clear shutdown path — either via `context.Context`, a done channel, or guaranteed completion
2. Goroutines writing to channels must have a reader; goroutines reading from channels must have a writer or the channel must be closed
3. `go func()` in loops must capture loop variables correctly
4. HTTP handlers spawning goroutines must not outlive the request unless explicitly managed
5. Check for `select` statements missing a `ctx.Done()` case

## Why it matters

Leaked goroutines consume memory indefinitely and can hold resources (connections, file handles). They are silent — no error, no panic — making them hard to detect in production until the service OOMs.
