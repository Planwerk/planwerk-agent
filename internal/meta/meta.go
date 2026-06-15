// Package meta expands a Meta Issue — an issue that frames a larger body of
// work as several self-contained work packages — into linked, draft-depth Sub
// Issues. It reads the Meta Issue, asks Claude to carve it into the fewest
// sensible Sub Issues, files each one, links it to the Meta Issue via GitHub's
// native sub-issue relationship, and back-fills the Meta Issue body with the
// fresh references so the prose and the sub-issue list agree.
//
// meta is deliberately shallow, like draft: it does NOT clone the repo, load
// review patterns, cache, or elaborate. Each Sub Issue stops at draft depth —
// enough to stand on its own and be picked up later, never a file-level plan.
// Turning a Sub Issue into an engineering plan stays the job of the separate
// elaborate and implement commands, run per Sub Issue. meta also stops at
// creating and linking: it does not orchestrate the Sub Issues or close the
// Meta Issue.
package meta

import (
	"fmt"
	"io"
	"log/slog"

	"github.com/planwerk/planwerk-review/internal/github"
)

// Options configures a meta run.
type Options struct {
	IssueRef string
	Format   string // "markdown" or "json"
	Labels   []string
	DryRun   bool
	Version  string
}

// Runner executes the meta pipeline using injected Claude and GitHub clients.
type Runner struct {
	Claude ClaudeMetaSplitter
	GitHub GitHubClient
}

// NewRunner wires a Runner with the production GitHub backend and the given
// Claude split function.
func NewRunner(fn MetaFn) *Runner {
	return &Runner{
		Claude: metaFnAdapter{fn: fn},
		GitHub: defaultGitHubClient{},
	}
}

// Run is a package-level convenience that delegates to NewRunner(fn).Run.
func Run(w io.Writer, opts Options, fn MetaFn) error {
	return NewRunner(fn).Run(w, opts)
}

// Run executes the meta pipeline: parse the issue ref, fetch the Meta Issue,
// ask Claude to split it, validate the split, then — unless --dry-run — file
// each Sub Issue, link it to the Meta Issue, and back-fill the Meta Issue body
// with the fresh references. A split with no Sub Issues and a dry run both
// render a preview without any GitHub writes.
func (r *Runner) Run(w io.Writer, opts Options) error {
	owner, name, number, err := github.ParseIssueRef(opts.IssueRef)
	if err != nil {
		return fmt.Errorf("parsing issue ref: %w", err)
	}

	issue, err := r.GitHub.GetIssue(owner, name, number)
	if err != nil {
		return fmt.Errorf("fetching issue: %w", err)
	}
	repoFull := fmt.Sprintf("%s/%s", owner, name)
	slog.Info("fetched meta issue", "repo", repoFull, "issue", number, "title", issue.Title)

	result, err := r.Claude.Split(Context{Issue: issue})
	if err != nil {
		return fmt.Errorf("splitting meta issue: %w", err)
	}
	if err := result.Validate(); err != nil {
		return fmt.Errorf("invalid meta split: %w", err)
	}

	switch {
	case len(result.SubIssues) == 0:
		slog.Info("no work packages found in the meta issue; nothing to file", "issue", number)
		return r.render(w, repoFull, number, opts, result)
	case opts.DryRun:
		slog.Info("dry-run: rendering planned split without filing", "issue", number, "subIssues", len(result.SubIssues))
		return r.render(w, repoFull, number, opts, result)
	}

	if err := r.createAndLink(owner, name, number, opts, result); err != nil {
		return err
	}
	r.syncMetaBody(owner, name, number, result)

	return r.render(w, repoFull, number, opts, result)
}

// createAndLink files each Sub Issue in returned order, records its number and
// URL, and links it to the Meta Issue. A link failure does not abort the run:
// the Sub Issue already exists and can be linked by hand, so the failure is
// recorded on the Sub Issue and surfaced rather than swallowed. A creation
// failure does abort — a missing Sub Issue would leave the Meta body out of
// sync with no way to recover the reference.
func (r *Runner) createAndLink(owner, name string, metaNumber int, opts Options, result *Result) error {
	for i := range result.SubIssues {
		s := &result.SubIssues[i]
		body := BuildSubIssueBody(metaNumber, *s)
		url, err := r.GitHub.CreateIssueWithLabels(owner, name, s.Title, body, opts.Labels)
		if err != nil {
			return fmt.Errorf("creating sub-issue %q: %w", s.Key, err)
		}
		s.URL = url

		_, _, childNumber, err := github.ParseIssueRef(url)
		if err != nil {
			return fmt.Errorf("parsing created sub-issue URL %q: %w", url, err)
		}
		s.Number = childNumber
		slog.Info("created sub-issue", "key", s.Key, "issue", childNumber, "url", url)

		if err := r.GitHub.AddSubIssue(owner, name, metaNumber, childNumber); err != nil {
			s.LinkError = err.Error()
			slog.Warn("could not link sub-issue to meta issue; link it by hand", "key", s.Key, "issue", childNumber, "err", err)
			continue
		}
		s.Linked = true
	}
	return nil
}

// syncMetaBody back-fills the Meta Issue body with the created Sub Issue
// references and writes it back when every placeholder resolved. A partial
// substitution is skipped — never write a body with dangling {{sub:KEY}}
// tokens — and so is a body that carries no placeholders to begin with.
func (r *Runner) syncMetaBody(owner, name string, metaNumber int, result *Result) {
	refs := make(map[string]int, len(result.SubIssues))
	for _, s := range result.SubIssues {
		refs[s.Key] = s.Number
	}

	synced, allResolved := applyMetaReferences(result.MetaBody, refs)
	if synced == result.MetaBody {
		return
	}
	if !allResolved {
		slog.Warn("meta body has unresolved sub-issue placeholders; skipping body edit", "issue", metaNumber)
		return
	}
	if err := r.GitHub.EditIssueBody(owner, name, metaNumber, synced); err != nil {
		slog.Warn("could not update meta issue body; sub-issues were still created and linked", "issue", metaNumber, "err", err)
		return
	}
	result.MetaBody = synced
	slog.Info("synced meta issue body with sub-issue references", "issue", metaNumber)
}

// render writes the result in the configured format.
func (r *Runner) render(w io.Writer, repoFull string, metaNumber int, opts Options, result *Result) error {
	if opts.Format == "json" {
		return RenderJSON(w, result)
	}
	RenderMarkdown(w, repoFull, metaNumber, opts.Version, result)
	return nil
}
