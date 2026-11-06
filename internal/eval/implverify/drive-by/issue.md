# Greet an empty name as a stranger

## Description

`Hello("")` returns `"Hello, "`, a greeting addressed to nobody. This change makes it return `"Hello, stranger"` instead.

## Affected Areas

- `greet.go` (`Hello`)
- `greet_test.go` (a test for the empty name)

## Acceptance Criteria

- [ ] `Hello("")` returns `"Hello, stranger"`.
- [ ] `Hello("Ada")` still returns `"Hello, Ada"`.
- [ ] `greet_test.go` has a test for the empty name.

## Non-Goals

- Changing `Shout`.
