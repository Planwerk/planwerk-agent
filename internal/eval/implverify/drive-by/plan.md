## Implementation Plan (issue #1)

PLAN_READY: `Hello` greets an empty name as a stranger, with a test for it.

### Summary
- Add an empty-name branch to `Hello` and a test for it. Nothing else changes.
### Work Breakdown Coverage
- None: the issue is a single undivided change.
### Change Set
- `greet.go`, modify: `Hello` returns `"Hello, stranger"` when `name` is empty.
- `greet_test.go`, modify: add `TestHelloEmptyName`.
### Commit Sequence
1. Greet an empty name as a stranger: `greet.go`, `greet_test.go`.
### Test Plan
- `greet_test.go` `TestHelloEmptyName`: `Hello("")` returns `"Hello, stranger"`, the empty-name edge path.
### Documentation Plan
- `greet.go` doc comment of `Hello`: say that an empty name greets a stranger.
### Verification Commands
- `go test ./...`
### Status
STATUS: PLAN_READY
