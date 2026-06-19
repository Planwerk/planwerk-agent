package address

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/planwerk/planwerk-review/internal/detect"
	"github.com/planwerk/planwerk-review/internal/github"
	"github.com/planwerk/planwerk-review/internal/patterns"
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
// planwerk-review or its pattern dirs. The pasted-into session is expected to
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
	dirs, err := patterns.Resolve(patterns.ResolveOptions{
		NoLocal: opts.NoLocalPatterns,
		NoRepo:  opts.NoRepoPatterns,
		RepoDir: pr.Dir,
		Extra:   opts.PatternDirs,
	})
	if err != nil {
		slog.Warn("resolving pattern sources failed; bare prompt will omit them", "err", err)
	}
	pats, err := patterns.LoadFilteredWithOptions(patterns.LoadOptions{Remote: opts.Remote, NoEmbedded: opts.NoLocalPatterns}, tags, dirs...)
	if err != nil {
		slog.Warn("loading review patterns failed; bare prompt will omit them", "err", err)
		pats = nil
	}
	if len(pats) > 0 {
		slog.Info("loaded review patterns for bare prompt", "count", len(pats))
	}

	catalog := patterns.BuildCatalogReferences(pats, patterns.CatalogRefOptions{
		BundledRoot:    patterns.LocalPatternDir(opts.NoLocalPatterns),
		BundledURLBase: BundledPatternsURLBase,
		RepoRoot:       patterns.RepoPatternDir(opts.NoRepoPatterns, pr.Dir),
		RepoRelBase:    ".planwerk/review_patterns",
	})

	hasRepoLocal := false
	for _, c := range catalog {
		if c.LocalPath != "" {
			hasRepoLocal = true
			break
		}
	}

	prompt := build(BareContext{
		RepoFullName:     fmt.Sprintf("%s/%s", owner, repo),
		PRNumber:         number,
		TechTags:         tags,
		PatternCatalog:   catalog,
		BundledURLBase:   BundledPatternsURLBase,
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
