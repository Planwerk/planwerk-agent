# Install shell completions & man pages

Completions for `bash`, `zsh`, `fish`, and `powershell` are emitted via Cobra's
built-in `completion` subcommand:

```bash
# Load completions for the current shell session (bash)
source <(planwerk-review completion bash)

# Install persistently (zsh, Homebrew example)
planwerk-review completion zsh > "$(brew --prefix)/share/zsh/site-functions/_planwerk-review"

# Fish
planwerk-review completion fish > ~/.config/fish/completions/planwerk-review.fish
```

When installed from Homebrew, deb, or rpm packages, completions and man pages
(`man planwerk-review`) are installed automatically. Packages are produced by
`goreleaser` — see `.goreleaser.yml`.

For local development, regenerate the artifacts into `completions/` and
`docs/man/`:

```bash
make completions
make man
```
