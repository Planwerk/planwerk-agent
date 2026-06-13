# Configure the project

For repos that run `review`, `propose`, or `audit` repeatedly with the same
flags, pin the defaults in `.planwerk/config.yaml` at the repo root instead of
repeating flags in every CI invocation and local run. The file is loaded from
the current working directory if present.

Create `.planwerk/config.yaml` with only the keys you want to pin — absent keys
fall through to the environment variable and then the compiled-in default:

```yaml
# .planwerk/config.yaml
review:
  min-severity: warning        # info | warning | critical | blocking
  max-patterns: 40             # <=0 disables truncation
  format: markdown             # markdown | json

audit:
  min-severity: warning
  issue-min-severity: critical
```

A malformed file (bad YAML or unknown keys) is a hard error, so typos surface
immediately rather than silently running with the wrong settings.

For the full schema and the exact precedence rules between flags, the config
file, environment variables, and defaults, see the
[configuration reference](/reference/configuration).
