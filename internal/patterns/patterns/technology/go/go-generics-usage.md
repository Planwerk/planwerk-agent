# Review Pattern: Go Generics Usage

**Review-Area**: architecture
**Detection-Hint**: Generic functions or types where an interface would be simpler, type parameters with only one instantiation site, over-parameterized containers, generic APIs that expose type parameters to callers unnecessarily
**Severity**: WARNING
**Category**: technology
**Applies-When**: go
**Sources**: When To Use Generics (https://go.dev/blog/when-generics), Google Go Style Guide — Decisions (https://google.github.io/styleguide/go/decisions#generics), Learning Go 2nd Edition (https://www.oreilly.com/library/view/learning-go-2nd/9781098139285/), 100 Go Mistakes (https://www.manning.com/books/100-go-mistakes-and-how-to-avoid-them)

## What to check

1. Generics should be used when a function operates on slices, maps, or channels of arbitrary element types (`[]T`, `map[K]V`, `chan T`) — before generics these needed `interface{}` and type assertions
2. If a type parameter appears only once in a function signature, an interface is usually clearer — "if you're about to write the same code twice" is the original motivation, not "if you can abstract this"
3. Avoid generic wrappers around types that already have interface methods — if `io.Reader` already describes the contract, a generic function does not add value
4. Don't expose type parameters through public APIs when the internal implementation is the only place that needs them; keep generics as an implementation detail where possible
5. Type constraints should be as narrow as the code needs — use `~int` only if the function genuinely works with derived types; prefer `constraints.Ordered` or custom constraints over `any` when operations require it
6. Watch for generic types with mixed concrete and parametric methods that could be simplified by splitting into two types

## Why it matters

Generics solve a real problem (type-safe containers and algorithms) but are easy to over-apply. The Go team's own guidance is "write the code first, then introduce type parameters when duplication becomes painful." Premature generics make call sites harder to read, error messages harder to parse, and compilation slower. Interface-based polymorphism remains the right tool when behavior is the abstraction; generics are the right tool when the type is the abstraction.
