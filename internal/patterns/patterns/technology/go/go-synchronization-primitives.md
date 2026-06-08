# Review Pattern: Go Synchronization Primitives

**Review-Area**: quality
**Detection-Hint**: `sync.Mutex` embedded or passed by value, manual double-checked locking, `sync/atomic` operations mixed with plain reads/writes of the same variable, `sync.Once` not used for single-initialization, `sync.RWMutex` used where `sync.Mutex` would suffice (or vice versa), lock held across blocking operations
**Severity**: CRITICAL
**Category**: technology
**Applies-When**: go
**Sources**: The Go Memory Model (https://go.dev/ref/mem), sync package (https://pkg.go.dev/sync), sync/atomic package (https://pkg.go.dev/sync/atomic), Uber Go Style Guide (https://github.com/uber-go/guide/blob/master/style.md), 100 Go Mistakes (https://www.manning.com/books/100-go-mistakes-and-how-to-avoid-them)

## What to check

1. `sync.Mutex`, `sync.RWMutex`, `sync.WaitGroup`, `sync.Once`, and `sync.Cond` must not be copied after first use — methods that need them require pointer receivers, and fields holding them belong in types passed by pointer
2. When a struct embeds a lock, document or make explicit which fields the lock protects — an unlabeled `sync.Mutex` invites inconsistent locking discipline
3. Atomic operations and plain memory operations on the same variable are a data race even if reads are "harmless" — use `atomic.Int64`/`atomic.Pointer[T]` (Go 1.19+) consistently or guard with a mutex
4. `sync.Once` is the idiomatic primitive for lazy initialization; hand-rolled double-checked-locking variants are almost always wrong under the Go memory model
5. `sync.RWMutex` is worth its overhead only when reads massively dominate writes and are non-trivial — for short critical sections `sync.Mutex` is faster due to less bookkeeping
6. Never hold a lock across a channel send/receive, I/O, or an unbounded-time operation — this causes priority inversion and deadlocks under load
7. Prefer `sync.Map` only for caches with many disjoint keys written once and read many times; for the general "map protected by a mutex" case, a plain `map` with a `sync.Mutex` is clearer and often faster
8. Use `errgroup.Group` or explicit `sync.WaitGroup` for goroutine coordination — don't poll or sleep waiting for completion

## Why it matters

The Go memory model defines data races as undefined behavior, and Go's tooling (`-race`) detects them at runtime — but only if the code paths are exercised. Synchronization mistakes are often invisible in testing and manifest as rare production corruptions. Copying a lock silently disables it; mixing atomic and non-atomic accesses produces torn reads; holding locks across blocking operations creates deadlocks when load increases. These bugs are expensive to diagnose, so they are worth catching in review.
