# Review Pattern: Go Defer and Resource Management

**Review-Area**: quality
**Detection-Hint**: File or connection opened without defer Close, defer placed far from resource acquisition, defer inside loops, ignoring deferred function return values
**Severity**: WARNING
**Category**: technology
**Applies-When**: go
**Sources**: Effective Go (https://go.dev/doc/effective_go#defer), Google Go Style Guide — Best Practices (https://google.github.io/styleguide/go/best-practices), Dave Cheney — Practical Go (https://dave.cheney.net/practical-go/presentations/gophercon-singapore-2019.html), Fixing For Loops in Go 1.22 (https://go.dev/blog/loopvar-preview)

## What to check

1. Every resource acquisition (`Open`, `Lock`, `Begin`) must have a corresponding `defer` for cleanup (`Close`, `Unlock`, `Rollback`) placed immediately after the error check
2. `defer` inside a loop body does not execute until the function returns — this causes resource accumulation; extract the loop body into a separate function or close explicitly
3. Arguments to deferred functions are evaluated at `defer` time, but a deferred closure reads the variables it captures when it runs, so it sees every later assignment to them. For a loop variable, read the module's `go` directive: from `go 1.22` each iteration gets its own copy of the variables the `for` statement declares, so each deferred closure sees its own iteration's value; below `go 1.22` (or with no `go` directive) the iterations share one variable and every closure sees the last value
4. Deferred functions execute in LIFO order — this matters when cleanup order is significant (e.g., unlock before close)
5. Ignoring errors from deferred `Close()` can mask write errors on buffered I/O — use named return values to capture deferred errors when writing files

## Not a finding

- A resource the function returns to its caller: the caller owns its `Close`, so look for the missing one there. A resource the function drops on an error path before it returns is still its own to close.
- `r.Body` in an `http.Handler`: the server closes the request body, and the handler need not (`net/http`, `Request.Body`). The body of a response an `http.Client` returns is the caller's to close, and a missing `Close` there is still a finding.
- A `defer` inside a function literal the loop calls once per iteration (`for ... { func() { f := open(); defer f.Close() }() }`): it runs when that call returns, which is the extraction check 2 asks for.

## Why it matters

Defer guarantees cleanup regardless of which return path is taken, preventing resource leaks. Misplaced or missing defers are a leading cause of leaked file handles, database connections, and mutex deadlocks in Go services.
