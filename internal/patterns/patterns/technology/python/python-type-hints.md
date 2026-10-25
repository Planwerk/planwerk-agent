# Review Pattern: Python Type Hints

**Review-Area**: quality
**Detection-Hint**: Functions without type annotations, use of `Any` where concrete types are possible, missing return type annotations
**Severity**: INFO
**Category**: technology
**Applies-When**: python
**Sources**: PEP 484 - Type Hints (https://peps.python.org/pep-0484/), mypy Documentation (https://mypy.readthedocs.io/)

## What to check

1. Public functions and methods should have type annotations for parameters and return types
2. Avoid `Any` when a more specific type is possible — it disables type checking
3. Use `Optional[X]` (or `X | None` in 3.10+) instead of implicit `None` returns
4. Collection types should specify their element types: `list[str]` not `list`
5. Use `typing.Protocol` for structural typing instead of ABC when only a few methods are needed

## Not a finding

- A function in `module.py` that a sibling `module.pyi` stub annotates: type checkers read the stub in place of the module (PEP 484, PEP 561).
- `typing.List[str]` or `typing.Dict[str, int]` where check 4 writes `list[str]`, in a project whose `requires-python` floor (in `pyproject.toml`, or `python_requires` in `setup.cfg` or `setup.py`) is below 3.9: the builtin generics arrived in Python 3.9 (PEP 585), and below it they fail wherever annotations are evaluated. A bare `List` or `Dict` with no element type is still a finding.

## Why it matters

Type hints enable static analysis (mypy, pyright), improve IDE autocompletion, and serve as machine-checkable documentation. Missing types in public APIs make the code harder to use correctly.
