package address

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
// NewRunner(nil, nil).PrintBarePrompt. The prompt is built without invoking
// Claude, so the AddressFn / PromptBuildFn passed to NewRunner are not used.
func PrintBarePrompt(w io.Writer, opts Options, build BarePromptBuildFn) error {
	return NewRunner(nil, nil).PrintBarePrompt(w, opts, build)
}

// PrintBarePrompt builds a self-contained ("bare") address prompt from the PR
// reference. Even though no Claude call is made, the target repo is still
// cloned (or used in --local mode) so the prompt can carry concrete context:
// the detected technologies and the filtered review-pattern catalog, inlined so
// the manual Claude session that pastes this prompt needs no access to
// planwerk-agent or its pattern dirs. The pasted-into session is expected to
// operate on its own checkout and fetch the review threads itself. Mirrors
// fix.PrintBarePrompt.
func (r *Runner) PrintBarePrompt(w io.Writer, opts Options, build BarePromptBuildFn) error {
	if build == nil {
		return errors.New("--print-bare-prompt requires a prompt builder; wire claude.BuildBareAddressPrompt")
	}
	if !opts.Local {
		if _, _, _, err := github.ParseRef(opts.PRRef); err != nil {
			return fmt.Errorf("parsing PR ref: %w", err)
		}
	}

	pr, err := r.fetchPR(opts)
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
