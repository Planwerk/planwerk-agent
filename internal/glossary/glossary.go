// Package glossary reads a target repo's own domain vocabulary so the
// review, elaborate, and propose commands phrase their output in the repo's
// terms, and generates a starter glossary artifact for a repo that has none.
//
// The READ side (Load) mirrors the .planwerk/ override convention used by
// checklist.Load and propose.LoadOutOfScope: a missing file is not an error,
// so a repo without the convention runs unchanged.
package glossary

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// maxGlossaryBytes caps the glossary file read into a prompt. The CONTEXT.md
// schema is meant for a tight list of domain terms; reading an
// attacker-supplied multi-gigabyte (or never-ending, e.g. a FIFO) file would
// OOM the process or balloon the prompt and its API cost. An oversized file is
// treated as absent.
const maxGlossaryBytes = 64 * 1024

// glossaryLocations lists the repo-relative paths probed for a domain
// glossary, in precedence order. The root CONTEXT.md wins over
// .planwerk/context.md: the root file is the canonical, discoverable location
// the upstream CONTEXT-FORMAT convention documents, while .planwerk/context.md
// stays supported for repos that prefer to keep planwerk config out of the
// root. This is deliberately the opposite of the .planwerk/-first order the
// other overrides use, a nod to the external schema's convention.
var glossaryLocations = []string{
	"CONTEXT.md",
	filepath.Join(".planwerk", "context.md"),
}

// Glossary is a target repo's domain vocabulary loaded from its CONTEXT.md or
// .planwerk/context.md. Body is the trimmed file contents fed to the prompt;
// Source is the repo-relative path it was loaded from, for logging.
type Glossary struct {
	Body   string
	Source string
}

// Load resolves the repo's domain glossary from the first of CONTEXT.md or
// .planwerk/context.md that exists, root file first (see glossaryLocations).
// A repo carrying neither is not an error: Load returns (nil, nil) so the
// caller runs unchanged, mirroring patterns.RepoPatternDir and
// propose.LoadOutOfScope. An empty or whitespace-only file is treated as
// absent. A file larger than maxGlossaryBytes is skipped with a warning. A
// committed symlink at either path is not followed — os.Lstat reports the link
// itself, so a redirect outside the repo (e.g. CONTEXT.md -> /etc/passwd) is
// treated as "no file" rather than read into the prompt.
func Load(repoDir string) (*Glossary, error) {
	for _, rel := range glossaryLocations {
		path := filepath.Join(repoDir, rel)
		// Lstat, not Stat: git checks symlinks out verbatim, so a committed
		// symlink at this path must report itself (a non-regular file) instead
		// of redirecting the read. This also yields the size for the cap below.
		fi, err := os.Lstat(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("reading glossary file %s: %w", path, err)
		}
		if !fi.Mode().IsRegular() {
			continue
		}
		if fi.Size() > maxGlossaryBytes {
			slog.Warn("skipping oversized glossary file", "path", path, "size", fi.Size())
			continue
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("reading glossary file %s: %w", path, err)
		}
		body := strings.TrimSpace(string(content))
		if body == "" {
			continue
		}
		return &Glossary{Body: body, Source: rel}, nil
	}
	return nil, nil
}

// LoadBody returns the repo's domain-glossary body for prompt injection, or ""
// when the repo carries no glossary. It wraps Load with the best-effort posture
// the review, elaborate, and propose commands share: an unreadable glossary
// warns and proceeds rather than failing the run.
func LoadBody(repoDir string) string {
	g, err := Load(repoDir)
	if err != nil {
		slog.Warn("could not load domain glossary, proceeding without it", "err", err)
		return ""
	}
	if g == nil {
		return ""
	}
	slog.Info("loaded domain glossary", "source", g.Source)
	return g.Body
}
