# Review Pattern: DRY - Don't Repeat Yourself

**Review-Area**: architecture
**Detection-Hint**: Duplicated logic across functions or files, copy-pasted code blocks with minor variations, repeated magic values
**Severity**: INFO
**Category**: design-principle
**Sources**: The Pragmatic Programmer (https://pragprog.com/titles/tpp20/the-pragmatic-programmer-20th-anniversary-edition/)

## What to check

1. Look for duplicated business logic — identical or near-identical code blocks across functions
2. Repeated magic numbers or strings should be extracted into named constants
3. Similar data transformations applied in multiple places should be consolidated
4. Three or more instances of similar code justify extraction; two instances usually do not

## Not a finding

- Two identical code blocks that serve different business purposes: DRY is about duplicated knowledge, not duplicated code. Blocks that must change together are still a finding.
- Duplication inside generated files (a `Code generated ... DO NOT EDIT.` or `@generated` header): the generator input holds that knowledge once, so review the input.

## Why it matters

Duplicated knowledge means changes must be made in multiple places. Missing one creates inconsistencies and bugs. However, premature abstraction to satisfy DRY can be worse than the duplication itself — apply judiciously.
