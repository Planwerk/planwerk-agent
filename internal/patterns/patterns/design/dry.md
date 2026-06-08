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
4. Note: DRY is about knowledge duplication, not code duplication — two identical code blocks serving different business purposes are NOT a DRY violation
5. Three or more instances of similar code justify extraction; two instances usually do not

## Why it matters

Duplicated knowledge means changes must be made in multiple places. Missing one creates inconsistencies and bugs. However, premature abstraction to satisfy DRY can be worse than the duplication itself — apply judiciously.
