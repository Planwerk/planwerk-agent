// Package styleguide discovers the documentation style guide a target
// repository commits as STYLE_GUIDE.md so the mutating prompts (implement,
// fix, address) can bind the documentation prose the session writes — README
// and docs pages, CHANGELOG entries, doc comments / docstrings, CLI help
// text — to the repo's own rules. Only the repo-relative path is returned;
// the session reads the body itself from its checkout, so the prompt stays
// lean and the rules it follows are always the committed ones.
//
// Discovery is scoped to the checkout — never the invoking user's machine —
// so the result is reproducible from the checkout alone, in keeping with the
// hermetic-session design (design decision #45).
package styleguide

import (
	"log/slog"
	"os"
	"path/filepath"
)

// locations are the places a STYLE_GUIDE.md can live, in precedence order;
// the first hit wins. The root file wins over .planwerk/ (the canonical,
// discoverable location beats the tool-specific one — the glossary's
// CONTEXT.md precedence), followed by the generic docs/ and .github/
// fallbacks. Entries are slash-separated repo-relative paths — the exact
// form the prompt cites — and joined onto repoDir per-OS in Find.
var locations = []string{
	"STYLE_GUIDE.md",
	".planwerk/STYLE_GUIDE.md",
	"docs/STYLE_GUIDE.md",
	".github/STYLE_GUIDE.md",
}

// Find returns the slash-separated repo-relative path of the documentation
// style guide committed in the checkout: STYLE_GUIDE.md at the repository
// root, or the .planwerk/, docs/, or .github/ fallback location, first hit
// wins. It returns "" when repoDir is empty, when no location holds a
// regular file, or when a candidate is unreadable — best-effort, never
// fatal, mirroring the skills loader's posture.
func Find(repoDir string) string {
	if repoDir == "" {
		return ""
	}
	for _, rel := range locations {
		info, err := os.Stat(filepath.Join(repoDir, filepath.FromSlash(rel)))
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		slog.Info("found documentation style guide", "path", rel)
		return rel
	}
	return ""
}
