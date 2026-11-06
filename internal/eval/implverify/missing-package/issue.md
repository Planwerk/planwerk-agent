# Greet strangers and say goodbye

## Description

The package can greet a named person but cannot greet an unnamed one or say goodbye. This change adds both.

## Work Breakdown

1. Empty-name greeting: `Hello("")` returns `"Hello, stranger"` instead of `"Hello, "`.
2. Farewell: a new `Farewell(name string) string` in `greet.go` returns `"Goodbye, " + name`.

## Affected Areas

- `greet.go` (`Hello`, new `Farewell`)
- `greet_test.go` (tests for both)

## Acceptance Criteria

- [ ] `Hello("")` returns `"Hello, stranger"`.
- [ ] `Farewell("Ada")` returns `"Goodbye, Ada"`.
- [ ] `greet_test.go` tests both.

## Non-Goals

- Changing `Shout`.
