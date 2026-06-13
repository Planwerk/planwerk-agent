# Write your own review patterns

Review patterns codify knowledge from past reviews and make it reusable. To add
a repo-specific pattern, drop a Markdown file into `.planwerk/review_patterns/`
in the target repository — planwerk-review picks it up automatically on the next
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
Claude /review   ──▶  Claude /review       ──▶  Claude /review
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
3. **Repo-specific patterns**: The development team creates these themselves in `.planwerk/review_patterns/` based on their domain knowledge. planwerk-review picks them up automatically.
