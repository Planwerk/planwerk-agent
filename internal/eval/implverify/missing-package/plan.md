## Implementation Plan (issue #1)

PLAN_READY: `Hello` greets an empty name as a stranger, and a new `Farewell` says goodbye, each with its test.

### Summary
- Add an empty-name branch to `Hello` and a new `Farewell` in `greet.go`, with a test for each.
### Work Breakdown Coverage
- 1. Empty-name greeting: covered by the `greet.go` `Hello` change, commit 1, and `TestHelloEmptyName`.
- 2. Farewell: covered by the `greet.go` `Farewell` addition, commit 2, and `TestFarewell`.
### Change Set
- `greet.go`, modify: `Hello` returns `"Hello, stranger"` when `name` is empty.
- `greet.go`, modify: add `Farewell(name string) string`, which returns `"Goodbye, " + name`.
- `greet_test.go`, modify: add `TestHelloEmptyName` and `TestFarewell`.
### Commit Sequence
1. Greet an empty name as a stranger: `greet.go`, `greet_test.go`.
2. Add Farewell: `greet.go`, `greet_test.go`.
### Test Plan
- `greet_test.go` `TestHelloEmptyName`: `Hello("")` returns `"Hello, stranger"`, the empty-name edge path.
- `greet_test.go` `TestFarewell`: `Farewell("Ada")` returns `"Goodbye, Ada"`.
### Documentation Plan
- `greet.go` doc comments of `Hello` and `Farewell`.
### Verification Commands
- `go test ./...`
### Status
STATUS: PLAN_READY
