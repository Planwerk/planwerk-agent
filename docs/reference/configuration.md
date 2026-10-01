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

wiki:                          # GitHub Wiki knowledge source (review + audit + propose + elaborate + implement + fix + address + ship + brain memory + brain bootstrap)
  enabled: true                # opt the wiki in (default: off); false is the same as --no-wiki
  repo: owner/repo             # override the wiki source; default: the target repo's own wiki
  ref: main                    # pin to a branch/tag/commit; default: the wiki's default branch

capture:                       # capture write-back gate (implement + ship + review + audit)
  wiki: true                   # push accepted capture pages to the wiki (default: off — propose-only)
```

The `wiki:` section is top-level (not per-command) because the same wiki backs
`review`, `audit`, `propose`, `elaborate`, the `implement` plan step, `fix`,
`address`, and every `implement` and `fix` run `ship` drives. `brain memory`
reads it too, from the directory it is run in, which is how the `elaborate`,
`implement`, `fix`, `revisit`, `clarify`, `decide`, `diagnose`, and `meta`
skills get the project memory. See
[GitHub Wiki](/reference/review-patterns#github-wiki) for the page convention.
`enabled` and `ref` are overridden by the `--wiki`/`--no-wiki`/`--wiki-ref`
flags and override the `PLANWERK_WIKI`/`PLANWERK_WIKI_REF` environment
variables ([Precedence](#precedence)); `repo` is config-only.

`brain bootstrap` reads `wiki.repo` and `wiki.ref` and, like `sync`, ignores
`wiki.enabled`: running either command is the opt-in. The review tier of
`brain bootstrap` has no config key; it is set with `--review-model` and
`--review-effort` or their environment variables.

The separate `capture:` section gates the *write*: `capture.wiki` controls
whether the capture pass pushes the accepted pages to the wiki (the
`--capture-wiki` opt-in) instead of only proposing them. The write-back runs only
from a trusted source — `implement` and `audit`; `review` has no such flag and is always
propose-only, because it analyzes an untrusted pull request. It is kept apart from
the read-only `wiki:` knobs so read and write config stay distinct, and is
overridden by the `--capture-wiki` flag and overrides `PLANWERK_CAPTURE_WIKI`
(flag → config → env → off). Off by default keeps a run propose-only.

`ship` reads `capture.wiki` for the `implement` runs it drives. It never shows
the confirmation prompt, so it pushes only when the write-back is enabled and
`--yes` is given. Enabled without `--yes`, it logs one warning at start and
every run stays propose-only.

All keys are optional. Flags beyond `--min-severity`, `--max-patterns`,
`--max-findings`, `--format`, and `--patterns` (the high-churn ones) remain
CLI-only to keep the config surface small; boolean toggles like
`--post-review`, `--inline`, `--thorough`, and `--no-cache` stay on the command
line where they belong.
