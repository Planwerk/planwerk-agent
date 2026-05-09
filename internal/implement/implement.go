// Package implement turns an elaborated GitHub issue into a single Claude
// Code session that implements the feature end-to-end (code + tests + docs)
// inside a fresh clone of the target repository.
//
// The shape mirrors the fix package: an injectable Runner with GitHub /
// Claude / prompt-build dependencies, and two prompt-only escape hatches
// (--print-prompt embeds the issue body; --print-bare-prompt is a portable
// snippet that the user pastes into a manual session running inside their
// own checkout).
package implement

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/planwerk/planwerk-review/internal/github"
)

// Options configures the implement subcommand. Mirrors the Options style
// used by the review/audit/elaborate/fix packages.
type Options struct {
	IssueRef        string
	DryRun          bool // skip the Claude invocation; report what would happen
	PrintPrompt     bool // render the implement prompt to stdout and exit; never invoke Claude
	PrintBarePrompt bool // render a self-contained prompt to stdout and exit; never fetch issue or clone
	Version         string
}

// Runner glues together the GitHub issue/clone calls, the Claude
// implementer, and the prompt builder. Tests inject fakes via the
// exported fields.
type Runner struct {
	Claude ClaudeImplementer
	GitHub GitHubClient
	// BuildPrompt renders the implement prompt without invoking Claude.
	// Required when Options.PrintPrompt is set; nil otherwise is fine.
	BuildPrompt PromptBuildFn
}

// NewRunner builds a Runner with the production GitHub backend, the given
// Claude implement function, and the prompt builder wired in. The CLI is
// expected to call this with claude.Implement and claude.BuildImplementPrompt
// so the import direction stays claude -> implement.
func NewRunner(fn ImplementFn, build PromptBuildFn) *Runner {
	return &Runner{
		Claude:      implementFnAdapter{fn: fn},
		GitHub:      defaultGitHubClient{},
		BuildPrompt: build,
	}
}

// Run is a package-level convenience that delegates to NewRunner(fn, build).Run.
func Run(w io.Writer, opts Options, fn ImplementFn, build PromptBuildFn) error {
	return NewRunner(fn, build).Run(w, opts)
}

// PrintBarePrompt parses issueRef and writes a self-contained implement
// prompt to w — no issue fetch, no clone. The rendered prompt instructs a
// manual Claude Code session (already running inside a checkout of the
// target repo) to fetch the issue itself and implement it end-to-end.
//
// Lives in this package so the github.ParseIssueRef call stays out of the
// CLI entry point; the actual prompt body is supplied by the claude package
// via the build callback to preserve the claude -> implement import
// direction.
func PrintBarePrompt(w io.Writer, issueRef string, build BarePromptBuildFn) error {
	if build == nil {
		return errors.New("--print-bare-prompt requires a prompt builder; wire claude.BuildBareImplementPrompt")
	}
	owner, name, number, err := github.ParseIssueRef(issueRef)
	if err != nil {
		return fmt.Errorf("parsing issue ref: %w", err)
	}
	prompt := build(fmt.Sprintf("%s/%s", owner, name), number)
	if _, err := io.WriteString(w, prompt); err != nil {
		return fmt.Errorf("writing prompt: %w", err)
	}
	if !strings.HasSuffix(prompt, "\n") {
		_, _ = fmt.Fprintln(w)
	}
	return nil
}

// Run executes the implement workflow:
//  1. Resolve the issue (gh CLI).
//  2. In --print-prompt mode: render the prompt with the issue body
//     embedded and exit.
//  3. Otherwise clone the repo into a fresh temp directory.
//  4. In --dry-run mode: report what would happen and exit.
//  5. Run a Claude session inside the clone to implement the issue
//     end-to-end (code + tests + docs) and open a draft PR.
func (r *Runner) Run(w io.Writer, opts Options) error {
	if opts.PrintPrompt && r.BuildPrompt == nil {
		return errors.New("--print-prompt requires a prompt builder; wire claude.BuildImplementPrompt via NewRunner")
	}

	owner, name, number, err := github.ParseIssueRef(opts.IssueRef)
	if err != nil {
		return fmt.Errorf("parsing issue ref: %w", err)
	}
	fullName := fmt.Sprintf("%s/%s", owner, name)
	slog.Info("implement starting",
		"issue", fmt.Sprintf("%s#%d", fullName, number),
		"dry_run", opts.DryRun,
		"print_prompt", opts.PrintPrompt,
	)

	issue, err := r.GitHub.GetIssue(owner, name, number)
	if err != nil {
		return fmt.Errorf("fetching issue: %w", err)
	}
	slog.Info("fetched issue", "repo", fullName, "issue", number, "title", issue.Title)

	ctx := Context{
		RepoFullName: fullName,
		IssueNumber:  number,
		IssueTitle:   issue.Title,
		IssueBody:    issue.Body,
		IssueURL:     issue.URL,
		IssueState:   issue.State,
	}

	// In --print-prompt mode the only stdout payload is the prompt itself;
	// status chatter is silenced via slog (the prompt goes to w).
	if opts.PrintPrompt {
		prompt := r.BuildPrompt(ctx)
		if _, err := io.WriteString(w, prompt); err != nil {
			return fmt.Errorf("writing prompt: %w", err)
		}
		if !strings.HasSuffix(prompt, "\n") {
			_, _ = fmt.Fprintln(w)
		}
		return nil
	}

	if opts.DryRun {
		_, _ = fmt.Fprintf(w, "[dry-run] would clone %s and run Claude to implement #%d (%s)\n",
			fullName, number, issue.Title)
		return nil
	}

	slog.Info("cloning repository for implementation", "repo", fullName)
	repo, err := r.GitHub.CloneRepo(fullName)
	if err != nil {
		return fmt.Errorf("cloning repo: %w", err)
	}
	defer repo.Cleanup()
	slog.Info("cloned repository", "dir", repo.Dir)

	report, err := r.Claude.Implement(repo.Dir, ctx)
	if err != nil {
		return fmt.Errorf("claude implement: %w", err)
	}
	if report != "" {
		_, _ = fmt.Fprintf(w, "\nClaude implementation report:\n%s\n", report)
	}
	slog.Info("implementation complete", "issue", number)
	return nil
}
