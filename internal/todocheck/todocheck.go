// Package todocheck reads TODOS.md from a repository and provides its content
// for cross-referencing with PR changes.
package todocheck

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/planwerk/planwerk-agent/internal/gitref"
)

// maxFileSize is the maximum size of a TODO file (64 KB).
const maxFileSize = 64 * 1024

// Load reads TODOS.md from the repo root directory.
// Returns empty string if the file does not exist or cannot be read.
// Also checks for common variants: TODO.md, todos.md.
// Files larger than 64 KB are ignored with a warning on stderr.
func Load(repoDir string) string {
	if repoDir == "" {
		return ""
	}

	candidates := []string{"TODOS.md", "TODO.md", "todos.md"}
	for _, name := range candidates {
		path := filepath.Join(repoDir, name)
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		if info.Size() > maxFileSize {
			slog.Warn("TODO file exceeds 64 KB limit, skipping",
				"path", path, "size", info.Size())
			continue
		}
		data, err := os.ReadFile(path)
		if err == nil && len(data) > 0 {
			return strings.TrimSpace(string(data))
		}
	}

	return ""
}

// LoadFromRef is Load reading the TODO file as it stands at ref (the pull
// request's base branch) instead of from the working tree, so the open items a
// review cross-references are the ones the maintainers merged, not a list the
// pull request under review wrote. The same candidates and size limit apply.
func LoadFromRef(repoDir, ref string) string {
	for _, name := range []string{"TODOS.md", "TODO.md", "todos.md"} {
		if body, ok := gitref.Show(repoDir, ref, name, maxFileSize); ok {
			if body = strings.TrimSpace(body); body != "" {
				return body
			}
		}
	}
	return ""
}
