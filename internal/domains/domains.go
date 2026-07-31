// Package domains provides the domain list the planning prompts sweep before
// they commit to a change set. The default list is embedded at compile time and
// can be overridden per-repo via .planwerk/domains.md.
//
// It exists because a diff review cannot catch what a plan never considered. The
// specialist fan-out reads the code that was written; these domains are the ones
// a change silently fails by omission — an absent rollback path, a metric nobody
// added, a config key an existing deployment never learns about. Nothing in the
// diff points at them, so the sweep has to happen while the plan is still being
// written.
//
// Testing and documentation are deliberately absent from the list. The plan
// already carries a Test Plan and a Documentation Plan section, and the
// elaborated issue already forces per-criterion edge-case enumeration (design
// decision 31); repeating either here would split one instruction across two
// places, which is the duplication failure mode the prompt-design doctrine names.
package domains

import (
	_ "embed"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// maxFileSize caps the override file at 64 KB, mirroring checklist.Load and
// glossary.Load. The list is meant to be a handful of lines; reading an
// attacker-supplied multi-gigabyte (or never-ending, e.g. a FIFO) file would OOM
// the process or balloon every planning prompt and its API cost.
const maxFileSize = 64 * 1024

//go:embed domains.md
var defaultDomains string

// overridePath is the repo-relative location of the per-repo domain list.
const overridePath = ".planwerk/domains.md"

// Load resolves the domain list with the following priority:
//  1. .planwerk/domains.md in the target repo (repoDir) — per-repo override
//  2. Embedded default list shipped with the binary
//
// A repo without the override is not an error: Load returns the default so the
// run is unchanged, mirroring checklist.Load. An empty or whitespace-only
// override falls back to the default, since a list of nothing would silently
// delete the sweep rather than replace it. An oversized file is ignored with a
// warning, and a committed symlink is not followed — os.Lstat reports the link
// itself, so a redirect outside the repo (e.g. .planwerk/domains.md -> /etc/passwd)
// is treated as "no override" rather than read into the prompt (design decision 42).
func Load(repoDir string) string {
	if repoDir == "" {
		return defaultDomains
	}
	path := filepath.Join(repoDir, filepath.FromSlash(overridePath))
	// Lstat, not Stat: git checks symlinks out verbatim, so a committed symlink
	// at this path must report itself (a non-regular file) instead of redirecting
	// the read. This also yields the size for the cap below.
	fi, err := os.Lstat(path)
	if err != nil || !fi.Mode().IsRegular() {
		return defaultDomains
	}
	if fi.Size() > maxFileSize {
		slog.Warn("domain-list override exceeds 64 KB limit, using default",
			"path", path, "size", fi.Size())
		return defaultDomains
	}
	data, err := os.ReadFile(path)
	if err != nil {
		slog.Warn("could not read domain-list override, using default", "path", path, "err", err)
		return defaultDomains
	}
	if strings.TrimSpace(string(data)) == "" {
		return defaultDomains
	}
	slog.Info("loaded domain-list override", "path", overridePath)
	return string(data)
}

// Default returns the embedded default domain list. It is the fallback the
// prompt builders use when no checkout was resolved — the --print-plan-prompt
// mode renders before any clone exists, and the sweep is part of the plan
// contract rather than an optional enrichment, so it must render there too.
func Default() string {
	return defaultDomains
}
