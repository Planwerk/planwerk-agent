// Package extract anchors a target repository's GitHub Wiki review patterns
// into committed, reproducible files. It is the path back from a fast-moving,
// world-editable wiki to a code-coupled knowledge store: once a wiki pattern
// proves itself, a maintainer promotes it into the target repo's
// .planwerk/review_patterns/ (PR-gated, or directly with --local) or into
// planwerk-review's own bundled catalog (--to-catalog, the contribution path).
//
// The command is mechanical — it never invokes Claude. It reads the wiki's
// review_patterns/ directory (resolved through the same machinery the review,
// audit, propose, and implement commands use for the wiki knowledge source),
// lets the operator select which entries to anchor (mirroring the address
// command's --all / --pattern / interactive selector), and writes the selected
// files verbatim (normalizing only the frontmatter category for --to-catalog).
package extract

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/planwerk/planwerk-review/internal/github"
	"github.com/planwerk/planwerk-review/internal/patterns"
	"github.com/planwerk/planwerk-review/internal/workspace"
)

// Options configures the extract subcommand. Mirrors the Options style used by
// the propose and address packages.
type Options struct {
	// RepoRef is the target repository whose wiki review patterns are read. It
	// is required for the default (PR) and --to-catalog modes; under --local it
	// may be empty and is inferred from the working tree's origin remote.
	RepoRef string
	// All extracts every wiki review pattern without prompting.
	All bool
	// Patterns extracts only the named pattern(s), matched by filename stem
	// (repeatable --pattern). Mutually exclusive with All.
	Patterns []string
	// ToCatalog anchors the selected patterns into this checkout's bundled
	// review catalog (internal/patterns/patterns/review/), normalizing their
	// frontmatter to the review category. Mutually exclusive with Local.
	ToCatalog bool
	// Local writes the selected patterns directly into the current working
	// tree's .planwerk/review_patterns/ instead of opening a PR.
	Local bool
	// Force, with Local, skips the dirty-working-tree confirmation prompt.
	Force bool
	// Overwrite, with Local or ToCatalog, allows a selected pattern to replace
	// an existing file at its destination. Without it a name collision is
	// refused, so a wiki author cannot clobber a trusted file (a bundled catalog
	// pattern, or an existing repo pattern) by naming a pattern to match it.
	Overwrite bool
	// Version is the planwerk-review build version, named in the PR footer's
	// attribution clause.
	Version string
	// Remote configures how the wiki clone resolves through the remote-cache
	// machinery (carries the --remote-patterns-ttl value).
	Remote patterns.RemoteOptions
	// Wiki configures the wiki knowledge source. extract always sets
	// Enabled=true (reading the wiki is its whole purpose); the CLI fills Repo
	// and Ref from --wiki-ref / PLANWERK_WIKI_REF / the config file.
	Wiki patterns.WikiOptions
}

// resolveWikiFn resolves a target repo's wiki to its local review-patterns
// directory and commit. It matches patterns.ResolveWiki and is a Runner seam so
// tests can stand in a temp directory without cloning a real wiki.
type resolveWikiFn func(owner, name string, wopts patterns.WikiOptions, ropts patterns.RemoteOptions) patterns.ResolvedWiki

// Runner glues together the GitHub client, the wiki resolver, and the
// interactive selector. Tests inject fakes via the exported fields.
type Runner struct {
	GitHub GitHubClient
	// ResolveWiki resolves the target repo's wiki. Defaults to
	// patterns.ResolveWiki.
	ResolveWiki resolveWikiFn
	// In is the stream the interactive selector reads from.
	In io.Reader
	// IsTTY reports whether the selector should prompt interactively. When it
	// returns false the run defaults to extracting every pattern.
	IsTTY func() bool
}

// Run is a package-level convenience that runs a default Runner. The Runner's
// Run method fills in the production seams via applyDefaults.
func Run(w io.Writer, opts Options) error {
	return (&Runner{}).Run(w, opts)
}

// Run executes the extract pipeline:
//  1. Resolve the target repo identity (parse the ref, or open the local tree
//     under --local) and its wiki review_patterns directory.
//  2. Enumerate and select which patterns to anchor.
//  3. Dispatch to the write mode: open a PR into .planwerk/review_patterns/
//     (default), write the working tree directly (--local), or anchor into this
//     checkout's bundled catalog with the category normalized (--to-catalog).
func (r *Runner) Run(w io.Writer, opts Options) error {
	r.applyDefaults()

	// --local opens the working tree first (gating on a clean tree) so its
	// origin can supply owner/name when no ref is passed; the other modes parse
	// the required ref.
	var localRepo *github.Repo
	var owner, name string
	if opts.Local {
		repo, err := r.GitHub.CloneRepoLocal(opts.RepoRef, github.LocalOptions{Force: opts.Force, Prompter: workspace.NewStdinPrompter()})
		if err != nil {
			return fmt.Errorf("opening local repository: %w", err)
		}
		defer repo.Cleanup() // no-op for a local repo
		localRepo = repo
		owner, name = repo.Owner, repo.Name
	} else {
		o, n, err := github.ParseRepoRef(opts.RepoRef)
		if err != nil {
			return fmt.Errorf("parsing repo ref: %w", err)
		}
		owner, name = o, n
	}

	wiki := r.ResolveWiki(owner, name, opts.Wiki, opts.Remote)
	if wiki.PatternsDir == "" {
		return fmt.Errorf("no wiki review patterns to extract for %s/%s: the wiki is missing, uninitialized, offline, or has no review_patterns/ directory", owner, name)
	}

	entries, err := readEntries(wiki.PatternsDir)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		_, _ = fmt.Fprintf(w, "The %s wiki has a review_patterns/ directory but no patterns to extract.\n", wiki.Repo)
		return nil
	}

	selected, err := selectEntries(w, opts, r.In, r.IsTTY, entries)
	if err != nil {
		return err
	}
	if len(selected) == 0 {
		_, _ = fmt.Fprintln(w, "No patterns selected — nothing extracted.")
		return nil
	}

	switch {
	case opts.ToCatalog:
		return writeToCatalog(w, selected, wiki, opts.Overwrite)
	case opts.Local:
		return writeLocal(w, localRepo, selected, wiki, opts.Overwrite)
	default:
		return r.openPR(w, opts, selected, wiki)
	}
}

// writeLocal writes the selected patterns into the working tree's
// .planwerk/review_patterns/ directory, verbatim. overwrite allows replacing an
// existing pattern at the destination (see writeWorkingTree).
func writeLocal(w io.Writer, repo *github.Repo, selected []entry, wiki patterns.ResolvedWiki, overwrite bool) error {
	written, err := writeWorkingTree(repo.Dir, planwerkPatternsSubdir, selected, nil, overwrite)
	if err != nil {
		return err
	}
	reportWritten(w, written, fmt.Sprintf("%s (working tree)", repo.Dir), wiki)
	return nil
}

// writeToCatalog anchors the selected patterns into this planwerk-review
// checkout's bundled review catalog, normalizing each pattern's frontmatter
// category to review. It guards that the catalog parent exists so the command
// only writes when run from a planwerk-review checkout. overwrite allows
// replacing an existing catalog pattern at the destination (see
// writeWorkingTree).
func writeToCatalog(w io.Writer, selected []entry, wiki patterns.ResolvedWiki, overwrite bool) error {
	if info, err := os.Stat(catalogParentDir); err != nil || !info.IsDir() {
		return fmt.Errorf("--to-catalog must run from a planwerk-review checkout: %s not found in the current directory", catalogParentDir)
	}
	transform := func(raw []byte) []byte { return normalizeCategory(raw, categoryReview) }
	written, err := writeWorkingTree(".", catalogReviewSubdir, selected, transform, overwrite)
	if err != nil {
		return err
	}
	reportWritten(w, written, fmt.Sprintf("the bundled review catalog (frontmatter normalized to category %q)", categoryReview), wiki)
	return nil
}

// openPR clones the target repo, writes the selected patterns into
// .planwerk/review_patterns/, and opens a pull request via the existing
// improvement-PR path.
func (r *Runner) openPR(w io.Writer, opts Options, selected []entry, wiki patterns.ResolvedWiki) error {
	repo, err := r.GitHub.CloneRepo(opts.RepoRef)
	if err != nil {
		return fmt.Errorf("cloning repository: %w", err)
	}
	defer repo.Cleanup()

	files := make([]github.ImprovementFile, 0, len(selected))
	for _, e := range selected {
		files = append(files, github.ImprovementFile{
			RelativePath: filepath.Join(planwerkPatternsSubdir, e.Stem+mdExt),
			Content:      e.Raw,
		})
	}

	url, err := r.GitHub.OpenImprovementPR(repo, github.ImprovementPROptions{
		Branch: DefaultPRBranch,
		Title:  prTitle(selected),
		Body:   prBody(selected, wiki, opts.Version),
		Commit: prCommit(selected, wiki),
		Files:  files,
	})
	if err != nil {
		return fmt.Errorf("opening pull request: %w", err)
	}
	_, _ = fmt.Fprintf(w, "Opened pull request: %s\n", url)
	return nil
}

// applyDefaults fills in the Runner seams with their production defaults.
func (r *Runner) applyDefaults() {
	if r.GitHub == nil {
		r.GitHub = defaultGitHubClient{}
	}
	if r.ResolveWiki == nil {
		r.ResolveWiki = patterns.ResolveWiki
	}
	if r.In == nil {
		r.In = os.Stdin
	}
	if r.IsTTY == nil {
		r.IsTTY = workspace.IsStdinTTY
	}
}
