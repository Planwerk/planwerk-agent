package patterns

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
)

// BundledURLBase is the public raw-markdown URL prefix under which the
// embedded catalog's pattern files can be read without planwerk-agent. It is
// pinned to "main" so a manual session pasting a bare prompt always picks up
// the latest patterns, without the binary's version baked into URLs that then
// drift on dev builds.
const BundledURLBase = "https://raw.githubusercontent.com/planwerk/planwerk-agent/main/internal/patterns/patterns"

// ResolveOptions configures Resolve. NoRepo mirrors the --no-repo-patterns
// toggle the pattern-loading subcommands expose.
type ResolveOptions struct {
	// NoRepo suppresses the target repo's .planwerk/review_patterns directory.
	NoRepo bool
	// RepoDir is the target repository checkout root. It is only consulted
	// when NoRepo is false.
	RepoDir string
	// Wiki is a resolved local directory holding the target repo's GitHub Wiki
	// review patterns (see ResolveWiki). Empty means no wiki patterns. It sits
	// below the repo's .planwerk/review_patterns and below the explicit Extra
	// dirs, so the repo's committed (reviewed, branch-protected) patterns override
	// the world-editable wiki on a name collision, while an operator's --patterns
	// still overrides both.
	Wiki string
	// Extra are explicit --patterns directories supplied by the caller. They
	// have the highest priority and are always appended.
	Extra []string
}

// Resolve assembles the ordered list of on-disk pattern directories to load,
// applying the precedence the subcommands share: the target repo's GitHub
// Wiki review patterns (lowest priority), then the target repo's
// .planwerk/review_patterns directory, then any explicit --patterns
// directories (highest priority). The wiki sits below the committed repo
// patterns on purpose: the wiki is world-editable and unreviewed, so a repo's
// committed (and branch-protected) patterns must win over it on a name
// collision. NoRepo drops the repo group; the wiki slot is dropped by passing
// an empty Wiki.
//
// Resolve is the single source of truth for this precedence order; callers
// must not re-derive it. The binary's embedded catalog is layered in
// separately by LoadFilteredWithOptions (see LoadOptions.NoEmbedded), not
// here. The error return leaves room for future fallible sources (e.g.
// XDG_DATA_DIRS); today Resolve never returns a non-nil error.
func Resolve(opts ResolveOptions) ([]string, error) {
	var dirs []string
	if opts.Wiki != "" {
		dirs = append(dirs, opts.Wiki)
	}
	if dir := RepoPatternDir(opts.NoRepo, opts.RepoDir); dir != "" {
		dirs = append(dirs, dir)
	}
	dirs = append(dirs, opts.Extra...)
	return dirs, nil
}

// RepoLoadOptions configures LoadForRepo: which catalogs to read for one
// target checkout and how to filter them. The fields mirror the
// --patterns, --no-local-patterns and --no-repo-patterns flags plus the
// detected technology tags.
type RepoLoadOptions struct {
	// RepoDir is the target repository checkout root, consulted for its
	// .planwerk/review_patterns directory unless NoRepo is set.
	RepoDir string
	// Wiki is the resolved local directory of the repo's GitHub Wiki review
	// patterns (see ResolveWiki); empty means none.
	Wiki string
	// Extra are the explicit --patterns sources: local directories or remote
	// URIs accepted by IsRemote.
	Extra []string
	// Tags are the detected technology tags the catalog is filtered by; nil
	// keeps every pattern.
	Tags []string
	// NoEmbedded suppresses the binary's embedded catalog (--no-local-patterns).
	NoEmbedded bool
	// NoRepo suppresses the repo's .planwerk/review_patterns (--no-repo-patterns).
	NoRepo bool
	// Remote controls how remote sources are resolved into local directories.
	Remote RemoteOptions
}

// LoadForRepo resolves the pattern sources for one checkout in precedence
// order (see Resolve) and loads the tag-filtered catalog on top of the
// embedded one. It is the one loader every subcommand goes through; the
// error names the step that failed.
func LoadForRepo(opts RepoLoadOptions) ([]Pattern, error) {
	dirs, err := Resolve(ResolveOptions{
		NoRepo:  opts.NoRepo,
		RepoDir: opts.RepoDir,
		Wiki:    opts.Wiki,
		Extra:   opts.Extra,
	})
	if err != nil {
		return nil, fmt.Errorf("resolving pattern sources: %w", err)
	}
	pats, err := LoadFilteredWithOptions(LoadOptions{Remote: opts.Remote, NoEmbedded: opts.NoEmbedded}, opts.Tags, dirs...)
	if err != nil {
		return nil, fmt.Errorf("loading patterns: %w", err)
	}
	return pats, nil
}

// LoadForRepoOrWarn is LoadForRepo for the commands that run without a
// catalog rather than fail on a corrupt pattern source (the fix, rebase,
// address and implement loops, and every bare-prompt build): a load error is
// logged and yields nil.
func LoadForRepoOrWarn(opts RepoLoadOptions) []Pattern {
	pats, err := LoadForRepo(opts)
	if err != nil {
		slog.Warn("loading review patterns failed; continuing without them", "err", err)
		return nil
	}
	return pats
}

// BareCatalog builds the catalog a bare prompt embeds from a loaded pattern
// set: each entry carries either the public URL of an embedded pattern or the
// in-checkout path of a repo-specific one under .planwerk/review_patterns.
// hasRepoLocal reports whether any entry is the latter, so the prompt can tell
// the pasted-into session to read those from its own checkout.
func BareCatalog(pats []Pattern, repoDir string, noRepo bool) (refs []CatalogReference, hasRepoLocal bool) {
	refs = BuildCatalogReferences(pats, CatalogRefOptions{
		BundledURLBase: BundledURLBase,
		RepoRoot:       RepoPatternDir(noRepo, repoDir),
		RepoRelBase:    ".planwerk/review_patterns",
	})
	for _, c := range refs {
		if c.LocalPath != "" {
			return refs, true
		}
	}
	return refs, false
}

// RepoPatternDir returns the target repo's .planwerk/review_patterns
// directory, or "" when noRepo is set or the directory does not exist. The
// bare-prompt catalog builder uses this root to emit "read this from your
// checkout" entries instead of remote URLs.
func RepoPatternDir(noRepo bool, repoDir string) string {
	if noRepo {
		return ""
	}
	candidate := filepath.Join(repoDir, ".planwerk", "review_patterns")
	if info, err := os.Stat(candidate); err == nil && info.IsDir() {
		return candidate
	}
	return ""
}

// resolveSources turns each entry into a local directory path: remote URIs
// are materialized into the cache via ResolveRemote, local paths pass
// through unchanged.
func resolveSources(opts RemoteOptions, sources []string) ([]string, error) {
	dirs := make([]string, 0, len(sources))
	for _, src := range sources {
		if IsRemote(src) {
			d, err := ResolveRemote(src, opts)
			if err != nil {
				return nil, fmt.Errorf("resolving remote pattern source %q: %w", src, err)
			}
			dirs = append(dirs, d)
			continue
		}
		dirs = append(dirs, src)
	}
	return dirs, nil
}

// loadOrderedSources resolves opts and sources into parsed-pattern groups in
// ascending priority order (lowest first): the embedded catalog (unless
// opts.NoEmbedded) is the lowest-priority group, followed by one group per
// explicit on-disk/remote source in slice order. The caller dedups across the
// groups by Pattern.Name — later groups win — and applies tag filtering.
func loadOrderedSources(opts LoadOptions, sources []string) ([][]Pattern, error) {
	var groups [][]Pattern

	if !opts.NoEmbedded {
		embedded, err := loadEmbedded()
		if err != nil {
			return nil, fmt.Errorf("loading embedded patterns: %w", err)
		}
		groups = append(groups, embedded)
	}

	dirs, err := resolveSources(opts.Remote, sources)
	if err != nil {
		return nil, err
	}
	for _, dir := range dirs {
		pats, err := loadDir(dir)
		if err != nil {
			return nil, err
		}
		groups = append(groups, pats)
	}

	return groups, nil
}
