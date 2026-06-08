# Review Pattern: BDD - Behavior-Driven Development

**Review-Area**: testing
**Detection-Hint**: Tests describing implementation details rather than behavior, missing acceptance criteria in PRs, test names using technical jargon instead of domain language
**Severity**: INFO
**Category**: design-principle
**Sources**: BDD in Action (John Ferguson Smart), The Cucumber Book (Matt Wynne, Aslak Hellesoy)

## What to check

1. Test names should read as behavior specifications using domain language: "should reject orders exceeding credit limit"
2. Integration and E2E tests should map to acceptance criteria from the PR description or ticket
3. Test scenarios should be structured as Given-When-Then (setup, action, verification)
4. Tests should focus on observable behavior (outputs, state changes, side effects), not internal implementation
5. Complex business rules should have examples that a domain expert could verify

## Why it matters

Tests that describe behavior serve as living documentation. When tests focus on implementation details, they break on every refactor but miss actual behavioral regressions.
