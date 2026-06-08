# Review Pattern: Go Zero Value and Allocation Idioms

**Review-Area**: quality
**Detection-Hint**: Using `new` for slices/maps/channels, unnecessary constructors for types with usable zero values, `make` with wrong type, uninitialized map writes causing panic
**Severity**: WARNING
**Category**: technology
**Applies-When**: go
**Sources**: Effective Go (https://go.dev/doc/effective_go#allocation_new), Effective Go (https://go.dev/doc/effective_go#allocation_make), Effective Go (https://go.dev/doc/effective_go#composite_literals), Go Proverbs (https://go-proverbs.github.io/), Google Go Style Guide — Best Practices (https://google.github.io/styleguide/go/best-practices), Uber Go Style Guide (https://github.com/uber-go/guide/blob/master/style.md)

## What to check

1. `make` is only valid for slices, maps, and channels — using `new` for these types creates a nil pointer, not an initialized value
2. Writing to a nil map panics at runtime — maps must be initialized with `make` or a composite literal before use
3. Types should be designed so their zero value is useful (e.g., `bytes.Buffer`, `sync.Mutex`) — unnecessary constructors for zero-ready types add complexity
4. Prefer composite literals with named fields (`&File{fd: fd, name: name}`) over sequential field assignment — unnamed fields (`&File{fd, name, nil, 0}`) are fragile when struct fields change
5. Returning `&T{...}` from a constructor is safe — the Go compiler heap-allocates when the address escapes, unlike C

## Why it matters

Confusing `new` and `make` or forgetting to initialize maps causes runtime panics. Designing types with useful zero values reduces constructor boilerplate and makes the API harder to misuse. Composite literals with named fields survive struct refactoring without silent breakage.
