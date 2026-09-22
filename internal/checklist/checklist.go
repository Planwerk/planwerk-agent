// Package checklist provides the review checklist used in the Claude prompt.
// The default checklist is embedded at compile time and can be overridden
// per-repo via .planwerk/checklist.md.
package checklist

import (
	_ "embed"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/planwerk/planwerk-agent/internal/gitref"
)

// maxFileSize is the maximum size of a checklist override file (64 KB).
const maxFileSize = 64 * 1024

//go:embed checklist.md
var defaultChecklist string

// Load resolves the review checklist with the following priority:
//  1. .planwerk/checklist.md in the reviewed repo (repoDir) — per-repo override
//  2. Embedded default checklist shipped with the binary
//
// Override files larger than 64 KB are ignored with a warning on stderr.
func Load(repoDir string) string {
	if repoDir != "" {
		repoChecklist := filepath.Join(repoDir, ".planwerk", "checklist.md")
		if info, err := os.Stat(repoChecklist); err == nil {
			if info.Size() > maxFileSize {
				slog.Warn("checklist override exceeds 64 KB limit, using default",
					"path", repoChecklist, "size", info.Size())
			} else if data, err := os.ReadFile(repoChecklist); err == nil && len(data) > 0 {
				return string(data)
			}
		}
	}
	return defaultChecklist
}

// Default returns the embedded default checklist content.
func Default() string {
	return defaultChecklist
}

// LoadFromRef is Load reading the override as it stands at ref (the pull
// request's base branch) instead of from the working tree. The review runs in a
// checkout of the pull request's head, and the checklist is spliced into the
// review prompt as instructions: read from the head, a pull request could
// rewrite the checklist it is reviewed against. An override absent at ref, or
// larger than 64 KB, falls back to the embedded default.
func LoadFromRef(repoDir, ref string) string {
	if body, ok := gitref.Show(repoDir, ref, ".planwerk/checklist.md", maxFileSize); ok && body != "" {
		return body
	}
	return defaultChecklist
}
