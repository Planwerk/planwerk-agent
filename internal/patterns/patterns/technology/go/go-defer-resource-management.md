# Review Pattern: Go Defer and Resource Management

**Review-Area**: quality
**Detection-Hint**: File or connection opened without defer Close, defer placed far from resource acquisition, defer inside loops, ignoring deferred function return values
**Severity**: WARNING
**Category**: technology
**Applies-When**: go
**Sources**: Effective Go (https://go.dev/doc/effective_go#defer), Google Go Style Guide — Best Practices (https://google.github.io/styleguide/go/best-practices), Dave Cheney — Practical Go (https://dave.cheney.net/practical-go/presentations/gophercon-singapore-2019.html)

## What to check

1. Every resource acquisition (`Open`, `Lock`, `Begin`) must have a corresponding `defer` for cleanup (`Close`, `Unlock`, `Rollback`) placed immediately after the error check
2. `defer` inside a loop body does not execute until the function returns — this causes resource accumulation; extract the loop body into a separate function or close explicitly
3. Arguments to deferred functions are evaluated at `defer` time, not at execution time — watch for closures that capture loop variables or mutated state
4. Deferred functions execute in LIFO order — this matters when cleanup order is significant (e.g., unlock before close)
5. Ignoring errors from deferred `Close()` can mask write errors on buffered I/O — use named return values to capture deferred errors when writing files

## Why it matters

Defer guarantees cleanup regardless of which return path is taken, preventing resource leaks. Misplaced or missing defers are a leading cause of leaked file handles, database connections, and mutex deadlocks in Go services.
