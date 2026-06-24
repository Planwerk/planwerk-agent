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
	"github.com/planwerk/planwerk-review/internal/patterns"
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
	// Version is the planwerk-review build version, threaded through for parity
	// with the other commands; the PR footer reads it from the attribution
	// package's process-wide record.
	Version string
	// Remote configures how the wiki clone resolves through the remote-cache
	// machinery (carries the --remote-patterns-ttl value).
	Remote patterns.RemoteOptions
	// Wiki configures the wiki knowledge source. extract always sets
	// Enabled=true (reading the wiki is its whole purpose); the CLI fills Repo
	// and Ref from --wiki-ref / PLANWERK_WIKI_REF / the config file.
	Wiki patterns.WikiOptions
}
