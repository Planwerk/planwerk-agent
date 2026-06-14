// Package draft turns a one-line feature idea into a ready-to-file GitHub
// issue. It runs a short Claude-led clarifying Q&A loop, drafts a structured
// issue (title plus Description / Motivation / rough Scope), previews it, runs
// a duplicate-title check, and creates the issue on confirmation.
//
// draft is deliberately shallow: it does NOT clone the repo, load review
// patterns, or produce an engineering plan. That work is the separate
// elaborate step. draft only needs the target's owner/repo, which --local
// infers from the cwd's origin remote.
package draft

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/planwerk/planwerk-review/internal/github"
	"github.com/planwerk/planwerk-review/internal/workspace"
)

// maxQuestions caps how many clarifying questions the interactive loop asks, so
// a chatty model cannot turn the capture step into an interrogation.
const maxQuestions = 5

// formatJSON is the --format value that emits the machine-readable draft.
const formatJSON = "json"

// Options configures a draft run.
type Options struct {
	RepoRef         string
	Seed            string
	Local           bool
	NoInteractive   bool
	DryRun          bool
	Labels          []string
	Format          string // "markdown" or "json"
	PrintPrompt     bool
	PrintBarePrompt bool
	Version         string
}

// Runner executes the draft pipeline using injected Claude, GitHub, and
// terminal dependencies.
type Runner struct {
	Claude          ClaudeDrafter
	Create          func(owner, name, title, body string, labels []string) (string, error)
	Search          github.DuplicateSearcher
	DetectOrigin    func() (string, string, error)
	BuildPrompt     PromptBuildFn
	BuildBarePrompt BarePromptBuildFn
	In              io.Reader
	// Prompt receives the interactive seed and Q&A prompts. It is stderr in
	// production so the prompts stay visible even when stdout is redirected and
	// never pollute the --format json or --dry-run output written to w.
	Prompt io.Writer
	IsTTY  func() bool
}

// NewRunner wires a Runner with the production backends: the github issue
// creator and duplicate searcher, origin detection over the cwd, stdin, and
// the stdin-TTY check.
func NewRunner(qFn QuestionsFn, dFn DraftFn, build PromptBuildFn, bare BarePromptBuildFn) *Runner {
	return &Runner{
		Claude:          claudeFnAdapter{questions: qFn, draft: dFn},
		Create:          github.CreateIssueWithLabels,
		Search:          github.SearchIssues,
		DetectOrigin:    detectCwdOrigin,
		BuildPrompt:     build,
		BuildBarePrompt: bare,
		In:              os.Stdin,
		Prompt:          os.Stderr,
		IsTTY:           workspace.IsStdinTTY,
	}
}

// Run is a package-level convenience that delegates to NewRunner(...).Run.
func Run(w io.Writer, opts Options, qFn QuestionsFn, dFn DraftFn, build PromptBuildFn, bare BarePromptBuildFn) error {
	return NewRunner(qFn, dFn, build, bare).Run(w, opts)
}

// Run executes the draft pipeline: optionally print the prompt and exit;
// otherwise resolve the target repo, capture the idea, run the clarifying Q&A
// loop, draft the issue, then preview / dedupe / confirm / create — or render
// without filing in --dry-run / --format json mode.
func (r *Runner) Run(w io.Writer, opts Options) error {
	if opts.PrintPrompt || opts.PrintBarePrompt {
		return r.printPrompt(w, opts)
	}

	owner, name, err := r.resolveRepo(opts)
	if err != nil {
		return err
	}

	// One reader serves the seed prompt, the Q&A loop, and the create
	// confirmation so piped input is never split across two buffers.
	reader := bufio.NewReader(r.In)

	seed, err := r.resolveSeed(reader, opts)
	if err != nil {
		return err
	}

	var answers []QA
	if !opts.NoInteractive {
		answers, err = r.clarify(reader, seed)
		if err != nil {
			return err
		}
	}

	result, err := r.Claude.Draft(Context{Seed: seed, Answers: answers})
	if err != nil {
		return fmt.Errorf("drafting issue: %w", err)
	}
	result.Body = BuildIssueBody(result)

	repoFull := fmt.Sprintf("%s/%s", owner, name)

	switch {
	case opts.Format == formatJSON:
		return NewRenderer(w).RenderJSON(*result)
	case opts.DryRun:
		NewRenderer(w).RenderMarkdown(repoFull, opts.Version, result)
		return nil
	}

	var preview bytes.Buffer
	NewRenderer(&preview).RenderMarkdown(repoFull, opts.Version, result)
	candidate := github.IssueCandidate{
		Title:   result.Title,
		Preview: preview.String(),
		Body:    result.Body,
	}
	create := func(o, n, title, body string) (string, error) {
		return r.Create(o, n, title, body, opts.Labels)
	}
	return github.RunInteractiveIssueCreation(
		w, reader, []github.IssueCandidate{candidate},
		owner, name, "draft", create, r.Search,
	)
}

// printPrompt renders the requested prompt and returns. Both print modes need
// a seed because the prompt is built from the idea.
func (r *Runner) printPrompt(w io.Writer, opts Options) error {
	seed := strings.TrimSpace(opts.Seed)
	if seed == "" {
		return fmt.Errorf("rendering the draft prompt requires an idea; pass it as an argument")
	}
	if opts.PrintBarePrompt {
		_, err := io.WriteString(w, r.BuildBarePrompt(seed))
		return err
	}
	_, err := io.WriteString(w, r.BuildPrompt(Context{Seed: seed}))
	return err
}

// resolveRepo determines the target owner/repo. With --local it reads the cwd's
// origin and, when an explicit ref is also given, requires it to match.
// Without --local it parses the explicit ref.
func (r *Runner) resolveRepo(opts Options) (string, string, error) {
	if opts.Local {
		owner, name, err := r.DetectOrigin()
		if err != nil {
			return "", "", fmt.Errorf("resolving origin: %w", err)
		}
		if ref := strings.TrimSpace(opts.RepoRef); ref != "" {
			o, n, err := github.ParseRepoRef(ref)
			if err != nil {
				return "", "", err
			}
			if o != owner || n != name {
				return "", "", fmt.Errorf("%w: ref %s/%s does not match origin %s/%s",
					github.ErrOriginMismatch, o, n, owner, name)
			}
		}
		return owner, name, nil
	}
	owner, name, err := github.ParseRepoRef(opts.RepoRef)
	if err != nil {
		return "", "", err
	}
	return owner, name, nil
}

// resolveSeed returns the seed idea: the supplied one when present, or one
// prompted interactively. It aborts with an actionable error when no idea is
// available and the loop cannot ask for one (--no-interactive, or stdin is not
// a TTY). The prompt goes to r.Prompt (stderr) so it never pollutes w.
func (r *Runner) resolveSeed(reader *bufio.Reader, opts Options) (string, error) {
	if seed := strings.TrimSpace(opts.Seed); seed != "" {
		return seed, nil
	}
	if opts.NoInteractive {
		return "", fmt.Errorf("no idea given and --no-interactive is set; pass the idea as an argument")
	}
	if r.IsTTY == nil || !r.IsTTY() {
		return "", fmt.Errorf("no idea given and stdin is not a TTY; pass the idea as an argument")
	}

	_, _ = fmt.Fprint(r.Prompt, "Describe your feature idea in one line: ")
	line, err := reader.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", fmt.Errorf("reading idea: %w", err)
	}
	seed := strings.TrimSpace(line)
	if seed == "" {
		return "", fmt.Errorf("no idea provided")
	}
	return seed, nil
}

// clarify runs the short Claude-led Q&A loop, capping the number of questions
// and reading one answer per question from reader. Questions are written to
// r.Prompt (stderr) so the machine-readable output on w stays clean. It returns
// early on EOF so exhausted piped input does not error.
func (r *Runner) clarify(reader *bufio.Reader, seed string) ([]QA, error) {
	questions, err := r.Claude.GenerateQuestions(seed)
	if err != nil {
		return nil, fmt.Errorf("generating clarifying questions: %w", err)
	}
	if len(questions) > maxQuestions {
		questions = questions[:maxQuestions]
	}

	answers := make([]QA, 0, len(questions))
	for i, q := range questions {
		_, _ = fmt.Fprintf(r.Prompt, "\nQ%d/%d: %s\n> ", i+1, len(questions), q)
		line, err := reader.ReadString('\n')
		if err != nil && err != io.EOF {
			return nil, fmt.Errorf("reading answer: %w", err)
		}
		answers = append(answers, QA{Question: q, Answer: strings.TrimSpace(line)})
		if err == io.EOF {
			break
		}
	}
	return answers, nil
}

// detectCwdOrigin resolves the owner/repo of the cwd's origin remote. It is the
// production DetectOrigin: draft needs only the owner/repo, never a checkout,
// so it bypasses the dirty-tree gate that github.UseLocalRepo enforces.
func detectCwdOrigin() (string, string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", "", fmt.Errorf("resolving working directory: %w", err)
	}
	return workspace.DetectOrigin(dir)
}
