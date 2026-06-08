# Review Pattern: Go Embedding Patterns

**Review-Area**: architecture
**Detection-Hint**: Manual forwarding methods that delegate to a field, embedded types exposing unintended methods, name conflicts between embedded types, embedding a type only to access one method
**Severity**: INFO
**Category**: technology
**Applies-When**: go
**Sources**: Effective Go (https://go.dev/doc/effective_go#embedding), Google Go Style Guide — Decisions (https://google.github.io/styleguide/go/decisions#struct-embedding), The Go Programming Language (https://www.gopl.io/)

## What to check

1. Use embedding to compose behaviors — if a struct manually forwards all methods of a field (`func (w *Wrapper) Read(p []byte) (int, error) { return w.inner.Read(p) }`), it should likely embed instead
2. Embedding promotes ALL methods of the inner type — verify that promoted methods make sense on the outer type; embedding `*log.Logger` into `Job` exposes `Fatal`, `Panic`, etc. on `Job`
3. When embedded types have name conflicts at the same nesting level, it is an error if the name is referenced — outer type fields/methods shadow deeper ones
4. The receiver of an embedded method is the inner type, not the outer — `ReadWriter.Read()` calls `Reader.Read()` with `Reader` as receiver, not `ReadWriter`
5. Embed interfaces in interfaces to compose contracts (`io.ReadWriter` embeds `io.Reader` + `io.Writer`) — this is preferred over listing all methods explicitly
6. When a type exists only to implement an interface, consider exporting only the interface and hiding the type — return `hash.Hash32` not `*crc32.digest`

## Why it matters

Embedding is Go's composition mechanism, replacing inheritance. Proper use eliminates boilerplate forwarding methods and creates clean interfaces. Improper use leaks implementation details — a struct embedding `*sql.DB` exposes 30+ database methods that may not be appropriate.
