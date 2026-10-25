# Review Pattern: Go Goroutine Leaks

**Review-Area**: quality
**Detection-Hint**: Goroutines started with `go func()` without cancellation mechanism, missing context propagation, unbuffered channels without readers
**Severity**: CRITICAL
**Category**: technology
**Applies-When**: go
**Sources**: Go Concurrency Patterns (https://go.dev/blog/pipelines), Effective Go (https://go.dev/doc/effective_go#goroutines), Google Go Style Guide — Decisions (https://google.github.io/styleguide/go/decisions#goroutine-lifetimes), Rob Pike — Go Concurrency Patterns (https://go.dev/talks/2012/concurrency.slide), Uber Go Style Guide (https://github.com/uber-go/guide/blob/master/style.md), Fixing For Loops in Go 1.22 (https://go.dev/blog/loopvar-preview)

## What to check

1. Every goroutine must have a clear shutdown path — either via `context.Context`, a done channel, or guaranteed completion
2. Goroutines writing to channels must have a reader; goroutines reading from channels must have a writer or the channel must be closed
3. Before reporting a loop-variable capture in a `go func()`, read the module's `go` directive: from `go 1.22` each iteration gets its own copy of the variables the `for` statement declares, so the goroutine reads its own iteration's value and the capture is correct unless the iteration assigns to that variable after the `go` statement. The capture is a bug below `go 1.22` (or with no `go` directive), where every goroutine shares one variable, and in any version when the loop assigns to a variable declared outside it
4. HTTP handlers spawning goroutines must not outlive the request unless explicitly managed
5. Check for `select` statements missing a `ctx.Done()` case

## Why it matters

Leaked goroutines consume memory indefinitely and can hold resources (connections, file handles). They are silent — no error, no panic — making them hard to detect in production until the service OOMs.
