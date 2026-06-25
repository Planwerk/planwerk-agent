# Configuration file

For repos that run `review`, `propose`, or `audit` repeatedly with the same
flags, defaults can be pinned in `.planwerk/config.yaml`. The file is loaded
from the current working directory if present — so dropping it at the repo root
lets teams standardize conventions once instead of repeating flags in every CI
invocation and local run.

For the task of creating one, see
[Configure the project](/how-to/configure-the-project).

## Precedence

Values are resolved in this order (highest wins):

1. **Command-line flag** — `--min-severity`, `--max-patterns`, etc.
2. **Config file** — `.planwerk/config.yaml` entries.
3. **Environment variable** — e.g. `PLANWERK_MAX_PATTERNS`.
4. **Compiled-in default** — what you get with no config at all.

Only fields explicitly set in the file override the lower tiers; absent keys
fall through. A malformed file (bad YAML or unknown keys) is a hard error so
that typos surface immediately rather than silently running with the wrong
settings.

## Schema

```yaml
# .planwerk/config.yaml
review:
  min-severity: warning        # info | warning | critical | blocking
  max-patterns: 40             # <=0 disables truncation
  max-findings: 25             # <=0 disables cap
  format: markdown             # markdown | json
  patterns:
    - ./custom-review-patterns

propose:
  max-patterns: 60
  format: issues               # markdown | json | issues
  patterns: []

audit:
  min-severity: warning        # info | warning | critical | blocking
  issue-min-severity: critical # default: warning
  max-patterns: 40
  max-findings: 50
  format: markdown             # markdown | json
  patterns: []

wiki:                          # GitHub Wiki knowledge source (review + audit + propose + implement)
  enabled: true                # opt the wiki in (default: off); false is the same as --no-wiki
  repo: owner/repo             # override the wiki source; default: the target repo's own wiki
  ref: main                    # pin to a branch/tag/commit; default: the wiki's default branch

capture:                       # implement capture write-back gate
  wiki: true                   # push accepted capture pages to the wiki (default: off — propose-only)
```

The `wiki:` section is top-level (not per-command) because the same wiki backs
`review`, `audit`, `propose`, and the `implement` plan step. See
[GitHub Wiki](/reference/review-patterns#github-wiki) for the page convention.
`enabled` and `ref` are overridden by the `--wiki`/`--no-wiki`/`--wiki-ref`
flags and the `PLANWERK_WIKI`/`PLANWERK_WIKI_REF` environment variables; `repo`
is config-only.

The separate `capture:` section gates the *write*: `capture.wiki` controls
whether `implement`'s capture pass pushes the accepted pages to the wiki (the
`--capture-wiki` opt-in) instead of only proposing them. It is kept apart from
the read-only `wiki:` knobs so read and write config stay distinct, and is
overridden by the `--capture-wiki` flag and `PLANWERK_CAPTURE_WIKI` (flag → env →
config → off). Off by default keeps a run propose-only.

All keys are optional. Flags beyond `--min-severity`, `--max-patterns`,
`--max-findings`, `--format`, and `--patterns` (the high-churn ones) remain
CLI-only to keep the config surface small; boolean toggles like
`--post-review`, `--inline`, `--thorough`, and `--no-cache` stay on the command
line where they belong.
