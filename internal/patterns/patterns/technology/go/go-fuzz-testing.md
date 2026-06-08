# Review Pattern: Go Fuzz Testing

**Review-Area**: quality
**Detection-Hint**: Parser, decoder, or input-handling functions without accompanying `FuzzXxx` tests, public functions accepting `[]byte`/`string`/`io.Reader` with only hand-written table tests, missing `testdata/fuzz/` corpus, fuzz targets without `f.Add` seeds
**Severity**: INFO
**Category**: technology
**Applies-When**: go
**Sources**: Go Fuzzing (https://go.dev/doc/security/fuzz/), Go Fuzzing Tutorial (https://go.dev/doc/tutorial/fuzz), testing package (https://pkg.go.dev/testing#hdr-Fuzzing)

## What to check

1. Functions that parse untrusted input (JSON/YAML/XML/protobuf decoders, URL/path parsers, regex compilers, template renderers) should have a `FuzzXxx(f *testing.F)` companion
2. Each fuzz target should call `f.Add(...)` with representative seed corpus — fuzzing starts from seeds; without them it explores an empty input space
3. Fuzz targets must assert an invariant in `f.Fuzz(func(t *testing.T, ...) { ... })`: the function should not panic, its output should round-trip, or two equivalent inputs should produce equal outputs
4. Fuzz-discovered failures (files under `testdata/fuzz/FuzzXxx/`) should be committed to the repo so they become regression tests on subsequent runs
5. Functions with trust boundaries (deserializing network input, handling user-supplied files) without fuzz coverage are a security gap worth flagging
6. Don't fuzz internal, already-validated inputs — target the trust boundary where untrusted data first enters the system

## Why it matters

Fuzzing (available since Go 1.18) finds edge cases that table-driven tests don't reach: integer overflows, panics on empty/malformed input, infinite loops on adversarial strings, round-trip failures in serialization. Built into the standard `testing` package, it requires no extra tooling and runs under `go test -fuzz=FuzzXxx`. Because fuzzing persists failing inputs as regression cases, it compounds: each found bug becomes a permanent test. For code that handles any untrusted input, fuzz tests provide coverage that hand-written tests cannot.
