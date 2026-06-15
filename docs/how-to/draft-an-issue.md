# Draft an issue

Turn a rough, one-line feature idea into a clean, ready-to-file GitHub issue.
`draft` runs a short clarifying Q&A in the terminal, drafts a structured issue
(a descriptive title plus Description, Motivation, and a rough Scope), previews
it, checks for duplicate titles, and creates it only when you confirm.

`draft` is the front of the pipeline — `draft → elaborate → implement`. It
captures the idea; it does **not** plan the work. Turning the description into a
file-level engineering plan is the separate [`elaborate`](/how-to/elaborate-an-issue)
step.

```bash
# Draft an issue for a repository — prompts for the idea, then asks a few
# clarifying questions
planwerk-review draft owner/repo

# Seed the idea up front
planwerk-review draft owner/repo "add a dark mode toggle to the settings page"

# File against the current checkout's origin (no repo-ref needed)
planwerk-review draft --local "add a dark mode toggle"

# Draft straight from the seed, skipping the clarifying questions
planwerk-review draft --no-interactive owner/repo "add a dark mode toggle"

# Preview the drafted issue without filing it
planwerk-review draft --dry-run owner/repo "add a dark mode toggle"

# Attach labels to the created issue (repeatable)
planwerk-review draft --label enhancement --label needs-triage owner/repo "add a dark mode toggle"
```

## The interactive flow

1. **Seed the idea.** Pass it as the final argument, or let the command prompt
   you for it in a multi-line composer (see [Compose your input](#compose-your-input)
   below). In a non-interactive context (stdin is not a TTY) with no idea and no
   `--no-interactive`, the command aborts with an actionable error instead of
   hanging.
2. **Answer a few questions.** Claude asks a handful of targeted questions — the
   problem, who benefits, rough scope, and any hard constraints — to sharpen the
   description. The questions are asked in the same language as your seed idea, so
   you can answer comfortably; the drafted issue itself is always written in
   English. Each answer uses the same multi-line composer. `--no-interactive`
   / `-y` skips this and drafts from the seed alone.
3. **Review the draft.** The rendered issue is shown, and its title is checked
   against existing issues. If a possible duplicate is found, you are warned and
   asked whether to proceed.
4. **Confirm.** The issue is created only when you answer `y`; `q` quits without
   filing anything. The created issue URL is printed.

The create step always asks for confirmation, even with `--no-interactive`
(which skips only the clarifying questions). To script a non-interactive run,
use `--dry-run` (or `--no-create`) to render without filing, or `--format json`
to capture the drafted issue for your own tooling.

## Compose your input

On an interactive terminal, the idea prompt and each clarifying answer open a
multi-line composer in the terminal, so you can write a real paragraph instead
of cramming everything onto one line:

| Key | Action |
|-----|--------|
| `Enter` | Insert a new line |
| `Ctrl-D` | Submit the current text |
| `Ctrl-E` | Open the text in your editor |
| `Ctrl-C` | Cancel |

An empty submission at the idea prompt aborts with `no idea provided`, just as
before.

`Ctrl-E` hands off to your editor on a temporary file seeded with what you have
typed so far; when you save and exit, the file's contents replace the buffer.
The editor is resolved with the same precedence `git` uses — `$VISUAL`, then
`$EDITOR`, then `vi`:

```bash
# Use VS Code (it must block until the file is closed) for the composer escape
export VISUAL="code --wait"
planwerk-review draft owner/repo
```

The composer engages only when **both** stdin and stderr are a terminal. When
stdin is piped, stderr is redirected, or `--no-interactive` is set, `draft`
falls back to single-line reads, so piped input, `--format json`, and
`--dry-run` stay byte-for-byte stable for scripting.

## Local mode

`--local` files the issue against the repository of the current checkout's
`origin` remote, so you do not pass a repo-ref:

```bash
cd ~/code/my-project
planwerk-review draft --local "add a dark mode toggle"
```

Unlike the other repo-facing commands, `draft` needs only the `origin`
owner/repo — it never takes a local checkout, clones nothing, and runs no
codebase analysis. If you pass an explicit ref under `--local`, it must match
`origin`, otherwise the run aborts. See [Use local mode](/how-to/use-local-mode)
for the shared semantics.

## Hand off to elaborate and implement

Once the issue exists, take it through the rest of the pipeline:

```bash
# Expand the captured idea into a detailed engineering plan
planwerk-review elaborate owner/repo#NN --update-issue

# Implement the elaborated issue end to end and open a draft PR
planwerk-review implement owner/repo#NN
```

See the [Draft to implement](/tutorials/draft-to-implement) tutorial for the
full walkthrough.

## How it works

1. **Seed**: The idea comes from the positional argument or, on an interactive
   terminal, the multi-line composer (with a `Ctrl-E` escape to `$EDITOR`).
2. **Resolve the repo**: With `--local`, owner/repo is read from the `origin`
   remote of the current working directory; otherwise from the explicit
   repo-ref. No clone happens either way.
3. **Clarify**: Claude generates a short, capped list of clarifying questions;
   your answers are collected in the terminal, each in the same multi-line
   composer. `--no-interactive` skips this.
4. **Draft**: A single Claude call turns the seed plus answers into a structured
   issue (title, Description, Motivation, rough Scope) written in English,
   validated against the [`draft` JSON schema](/reference/output-format#json-schema).
   The prompt enforces the house issue format and the non-goals — describe the
   idea, do not plan it.
5. **Preview, dedupe, confirm**: The rendered draft is shown, its title is
   searched against existing issues, and the issue is created via `gh` only on
   confirmation.
