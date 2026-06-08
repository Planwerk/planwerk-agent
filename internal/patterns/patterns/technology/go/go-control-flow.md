# Review Pattern: Go Control Flow Idioms

**Review-Area**: quality
**Detection-Hint**: Unnecessary else after return/break/continue, if-else chains that could be a switch, missing if-init pattern for scoped variables, nested conditionals instead of early returns
**Severity**: INFO
**Category**: technology
**Applies-When**: go
**Sources**: Effective Go (https://go.dev/doc/effective_go#if), Effective Go (https://go.dev/doc/effective_go#switch), Go Code Review Comments (https://go.dev/wiki/CodeReviewComments#indent-error-flow), Google Go Style Guide — Decisions (https://google.github.io/styleguide/go/decisions#indent-error-flow)

## What to check

1. When an `if` body ends with `break`, `continue`, `goto`, or `return`, the `else` block is unnecessary — remove it and let the successful flow run down the page
2. Use the if-init idiom (`if err := doThing(); err != nil`) to scope variables to the conditional block
3. Long `if-else-if-else` chains should be rewritten as expression-less `switch` statements
4. Guard clauses (early returns for error/edge cases) are preferred over deeply nested conditionals
5. Switch cases should use comma-separated values (`case ' ', '?', '&':`) instead of fall-through or repeated cases
6. Labeled `break` should be used to exit a surrounding `for` loop from within a `switch` — a bare `break` only exits the `switch`

## Why it matters

Effective Go explicitly recommends eliminating unnecessary `else` blocks and using early returns so that the "happy path" flows straight down the function. Deeply nested conditionals and long if-else chains reduce readability and increase cognitive load during review.
