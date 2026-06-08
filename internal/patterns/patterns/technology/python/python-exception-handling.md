# Review Pattern: Python Exception Handling

**Review-Area**: quality
**Detection-Hint**: Bare `except:` or `except Exception:` catching everything, swallowed exceptions, missing `from` in exception chaining
**Severity**: WARNING
**Category**: technology
**Applies-When**: python
**Sources**: PEP 3134 - Exception Chaining (https://peps.python.org/pep-3134/), Python Documentation: Errors and Exceptions (https://docs.python.org/3/tutorial/errors.html)

## What to check

1. Never use bare `except:` — always catch specific exception types
2. Use `raise ... from err` to preserve the exception chain when re-raising
3. Don't silently swallow exceptions with empty `except` blocks — at minimum log the error
4. `except Exception` is almost always too broad — catch the specific exceptions you can handle
5. Use context managers (`with`) for resource cleanup instead of try/finally when possible

## Why it matters

Bare exception handlers mask bugs by catching `KeyboardInterrupt`, `SystemExit`, and programming errors like `AttributeError`. Silent swallowing makes debugging nearly impossible.
