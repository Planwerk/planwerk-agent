# Write your own review patterns

Review patterns codify knowledge from past reviews and make it reusable. To add
a repo-specific pattern, drop a Markdown file into `.planwerk/review_patterns/`
in the target repository — planwerk-agent picks it up automatically on the next
`review`, `audit`, or `propose` run.

For the exact file layout (frontmatter fields, sections) see the
[review pattern format](/reference/review-patterns#pattern-format); for how
patterns from different locations are prioritized, see
[pattern sources](/reference/review-patterns#pattern-sources).

## Knowledge building

The tool systematically builds knowledge over time:

```text
First Review           Subsequent Reviews       Mature System
────────────          ────────────────────      ─────────────
Claude review    ──▶  Claude review        ──▶  Claude review
(no patterns)         + general patterns        + general patterns
                      + repo-specific           + repo-specific
      │               patterns                  patterns (many)
      ▼                     │                         │
Suggest new                 ▼                         ▼
patterns             Refine patterns            High-precision
                     + suggest new ones         reviews
```

**Knowledge building process:**

1. **After the first review**: The tool analyzes review results and suggests new general patterns that should be added to `internal/patterns/patterns/`.
2. **For recurring findings**: When the same issue occurs across multiple repos, the `Occurrences` field is incremented and the pattern is refined.
3. **Repo-specific patterns**: The development team creates these themselves in `.planwerk/review_patterns/` based on their domain knowledge. planwerk-agent picks them up automatically.

## Say what is not a finding

When a check keeps matching code that is fine, name that case in a
`## Not a finding` section directly after `## What to check`, one bullet per
case:

```markdown
## What to check

3. Getter methods must not use a `Get` prefix — a field `owner` gets a getter named `Owner()`, not `GetOwner()`

## Not a finding

- `GetX()` getters on protobuf messages and the calls to them: `protoc-gen-go` generates one per field, so the `Get` prefix is the generator's. A hand-written `GetX` getter is still a finding.

## Why it matters
```

Write each entry this way:

- **Name a concrete case**: a kind of file (by its header, path, or
  annotation), a construct, a language version, or a named rule of a linter
  the repository runs in CI. Pair each rule with the cases it reports: two
  rules behind one check can each report only part of it.
- **Say where to look instead**, or which file to read first, and when the
  case is a finding after all.
- **Name a case, never a level of confidence.** "Unless you are sure" names no
  case, and a finder that skips what it is unsure of drops real findings; the
  review reports an uncertain finding with its confidence instead.

A qualification that fits in a clause of one check can stay in that check's
line. A case several patterns share, such as generated files, goes into each
pattern whose check it qualifies, worded for that check: a domain specialist
renders only its own areas' patterns, so a case written once reaches only the
specialists that read that pattern.

Patterns in `.planwerk/review_patterns/` are read from the pull request's base,
so an entry a pull request adds applies from the first review after it merges.
The review of that pull request reports the change itself as
`Review Configuration Changed`.
