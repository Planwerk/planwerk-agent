# Review Pattern: Go Slice and Map Idioms

**Review-Area**: quality
**Detection-Hint**: Append result not assigned back, comma-ok idiom not used for map lookups, nil slice vs empty slice confusion, map used without initialization
**Severity**: WARNING
**Category**: technology
**Applies-When**: go
**Sources**: Effective Go (https://go.dev/doc/effective_go#slices), Effective Go (https://go.dev/doc/effective_go#maps), Effective Go (https://go.dev/doc/effective_go#append), Google Go Style Guide — Decisions (https://google.github.io/styleguide/go/decisions#nil-slices), Uber Go Style Guide (https://github.com/uber-go/guide/blob/master/style.md)

## What to check

1. `append` result must always be assigned back: `s = append(s, elem)` — the underlying array may be reallocated, making the old slice header stale
2. When a missing map key and the zero value are indistinguishable (e.g., `int` map where 0 is valid), use the comma-ok idiom: `val, ok := m[key]`
3. Slices are reference types — passing a slice to a function allows modification of elements, but `append` inside the function does not affect the caller unless the slice is returned
4. Use `copy(dst, src)` to duplicate slice data — simple assignment (`dst = src`) shares the underlying array
5. Use maps as sets with `bool` values: `seen[item] = true` — checking `if seen[item]` returns `false` for missing keys
6. `delete(m, key)` is safe on missing keys — no need for existence checks before deletion
7. `range` over strings iterates Unicode code points (runes), not bytes — use `range []byte(s)` for byte-level iteration

## Why it matters

Forgetting to assign the `append` result is a silent data loss bug. Using map lookups without comma-ok leads to subtle logic errors when zero values are meaningful. These are among the most common Go bugs caught in code review.
