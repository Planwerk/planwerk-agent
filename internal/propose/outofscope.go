package propose

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

const (
	// maxOutOfScopeBytes caps a single rejected-idea file. The knowledge base is
	// meant for short concept notes; reading an attacker-supplied multi-gigabyte
	// (or never-ending, e.g. a FIFO) file would OOM the process or balloon the
	// analysis prompt and its API cost. Oversized entries are skipped.
	maxOutOfScopeBytes = 64 * 1024
	// maxOutOfScopeEntries caps how many rejected ideas are loaded, bounding the
	// prompt size regardless of how many files the directory holds.
	maxOutOfScopeEntries = 100
)

// OutOfScopeEntry is one rejected idea loaded from the target repo's
// .planwerk/out-of-scope/ knowledge base. Name identifies the concept (the
// file's first Markdown heading, or its filename without the extension when it
// has none) and Body is the trimmed file contents shown to the analysis so it
// stops re-proposing the idea.
type OutOfScopeEntry struct {
	Name string
	Body string
}

// LoadOutOfScope reads the rejected-idea knowledge base from
// <repoDir>/.planwerk/out-of-scope/, one Markdown file per concept. It mirrors
// patterns.RepoPatternDir: a missing directory is not an error — it returns
// (nil, nil) so a repo without the convention runs unchanged. Entries come back
// in the filename order os.ReadDir guarantees, so the resulting prompt is
// deterministic; non-.md files, subdirectories, and symlinks are ignored, as
// are entries larger than maxOutOfScopeBytes, and at most maxOutOfScopeEntries
// are loaded.
func LoadOutOfScope(repoDir string) ([]OutOfScopeEntry, error) {
	dir := filepath.Join(repoDir, ".planwerk", "out-of-scope")
	// Lstat, not Stat: a committed symlink at this path (git checks symlinks out
	// verbatim) must not be followed, since it could redirect the read outside
	// the repo. A symlink reports !IsDir() here and is treated as "no directory".
	info, err := os.Lstat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading out-of-scope directory %s: %w", dir, err)
	}
	if !info.IsDir() {
		return nil, nil
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("reading out-of-scope directory %s: %w", dir, err)
	}

	var result []OutOfScopeEntry
	for _, e := range entries {
		if !e.Type().IsRegular() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		if len(result) >= maxOutOfScopeEntries {
			slog.Warn("out-of-scope entry cap reached, ignoring the rest", "cap", maxOutOfScopeEntries)
			break
		}
		path := filepath.Join(dir, e.Name())
		// Lstat again so a symlink reports itself rather than its target: a
		// committed aws.md -> /etc/passwd (or ~/.ssh/id_rsa) must not be followed
		// and read into the prompt. This also closes the TOCTOU window between
		// ReadDir and the read, and yields the size for the cap below.
		fi, err := os.Lstat(path)
		if err != nil || !fi.Mode().IsRegular() {
			continue
		}
		if fi.Size() > maxOutOfScopeBytes {
			slog.Warn("skipping oversized out-of-scope entry", "path", path, "size", fi.Size())
			continue
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("reading out-of-scope entry %s: %w", path, err)
		}
		body := strings.TrimSpace(string(content))
		result = append(result, OutOfScopeEntry{
			Name: outOfScopeName(body, e.Name()),
			Body: body,
		})
	}
	return result, nil
}

// outOfScopeName derives an entry's display name from the first Markdown ATX
// heading (a line whose first non-space character is "#"), falling back to the
// filename with its .md extension stripped when the file carries no heading.
func outOfScopeName(body, filename string) string {
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			if name := strings.TrimSpace(strings.TrimLeft(trimmed, "#")); name != "" {
				return name
			}
		}
	}
	return strings.TrimSuffix(filename, ".md")
}
