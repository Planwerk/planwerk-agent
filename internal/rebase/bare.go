package rebase

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/planwerk/planwerk-agent/internal/detect"
	"github.com/planwerk/planwerk-agent/internal/github"
	"github.com/planwerk/planwerk-agent/internal/patterns"
)

// PrintBarePrompt is a package-level convenience that delegates to
// NewRunner(...).PrintBarePrompt. The prompt is built without invoking Claude,
// so the resolve/analyze/apply functions passed to NewRunner are unused here.
func PrintBarePrompt(w io.Writer, opts Options, build BarePromptFn) error {
	return NewRunner(nil, nil, nil, nil).PrintBarePrompt(w, opts, build)
}

// PrintBarePrompt builds a self-contained ("bare") rebase prompt from the PR
// reference. No Claude call is made, but we still resolve the target repo so
// the prompt can carry concrete context — detected technologies and the
// filtered review-pattern catalog (local + .planwerk/review_patterns/ +
// --patterns sources), inlined so the manual Claude session that pastes this
// prompt needs no access to planwerk-agent or its pattern dirs.
//
// The pasted-into session is expected to operate on its own checkout of the PR
// head; the rendered prompt instructs it to perform the rebase, resolve
// conflicts semantically, and analyze the rebased commits itself.
func (r *Runner) PrintBarePrompt(w io.Writer, opts Options, build BarePromptFn) error {
	r.applyDefaults(&opts)
	if build == nil {
		return errors.New("--print-bare-prompt requires a prompt builder; wire claude.BuildBareRebasePrompt")
	}
	// In non-local mode validate the ref up front so a bad ref fails fast
	// before any clone. In local mode the ref may be empty (inferred from the
	// current branch), so identity is read from the resolved PR instead.
	if !opts.Local {
		if _, _, _, err := github.ParseRef(opts.PRRef); err != nil {
			return fmt.Errorf("parsing PR ref: %w", err)
		}
	}

	pr, err := github.OpenPR(r.GitHub, opts.PRRef, opts.Local, opts.Force)
	if err != nil {
		return fmt.Errorf("fetching PR for bare prompt build: %w", err)
	}
	defer pr.Cleanup()
	owner, repo, number := pr.Owner, pr.Repo, pr.Number

	tags := detect.Technologies(pr.Dir)
	if len(tags) > 0 {
		slog.Info("detected technologies for bare prompt", "technologies", strings.Join(tags, ", "))
	}
	pats := patterns.LoadForRepoOrWarn(patterns.RepoLoadOptions{
		RepoDir:    pr.Dir,
		Extra:      opts.PatternDirs,
		Tags:       tags,
		NoEmbedded: opts.NoLocalPatterns,
		NoRepo:     opts.NoRepoPatterns,
		Remote:     opts.Remote,
	})
	if len(pats) > 0 {
		slog.Info("loaded review patterns for bare prompt", "count", len(pats))
	}

	catalog, hasRepoLocal := patterns.BareCatalog(pats, pr.Dir, opts.NoRepoPatterns)

	prompt := build(BareContext{
		RepoFullName:     fmt.Sprintf("%s/%s", owner, repo),
		PRNumber:         number,
		Onto:             opts.Onto,
		TechTags:         tags,
		PatternCatalog:   catalog,
		BundledURLBase:   patterns.BundledURLBase,
		HasRepoLocalRefs: hasRepoLocal,
	})
	if _, err := io.WriteString(w, prompt); err != nil {
		return fmt.Errorf("writing prompt: %w", err)
	}
	if !strings.HasSuffix(prompt, "\n") {
		_, _ = fmt.Fprintln(w)
	}
	return nil
}
