package extract

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/planwerk/planwerk-review/internal/attribution"
	"github.com/planwerk/planwerk-review/internal/patterns"
)

// DefaultPRBranch is the head branch extract pushes the anchored patterns to in
// the default (PR) write mode.
const DefaultPRBranch = "planwerk-review/extract-review-patterns"

// categoryReview is the frontmatter category --to-catalog normalizes every
// extracted pattern to, so a wiki pattern lands in the bundled catalog as a
// first-class review pattern regardless of how it was categorized on the wiki.
const categoryReview = "review"

// categoryLinePrefix is the frontmatter key normalizeCategory rewrites.
const categoryLinePrefix = "**Category**:"

var (
	// planwerkPatternsSubdir is the committed location extract writes selected
	// wiki patterns into within a target repo (default PR mode and --local).
	planwerkPatternsSubdir = filepath.Join(".planwerk", "review_patterns")

	// catalogReviewSubdir is the bundled review catalog directory --to-catalog
	// writes into, relative to the planwerk-review checkout's working directory.
	catalogReviewSubdir = filepath.Join("internal", "patterns", "patterns", "review")

	// catalogParentDir is the parent of catalogReviewSubdir. --to-catalog guards
	// that it exists so the command only writes when run from a planwerk-review
	// checkout, rather than creating a stray tree under an unrelated cwd.
	catalogParentDir = filepath.Join("internal", "patterns", "patterns")
)

// normalizeCategory returns raw with its frontmatter category set to category.
// It rewrites an existing `**Category**:` line in place, or inserts one when the
// pattern declares none — after the `**Severity**:` line when present, otherwise
// right after the `# Review Pattern:` header. Every other byte is preserved, so
// the result is the original file with only its category changed.
//
// The scan is bounded to the frontmatter region exactly as patterns.Parse sees
// it — everything up to the first `## ` line is frontmatter, the rest is body —
// so a `**Category**:` example documented inside a meta-pattern's body (e.g. in a
// fenced code block) is left untouched rather than silently rewritten. The
// original line endings are preserved: a CRLF wiki file stays CRLF instead of
// gaining a lone LF on the changed or inserted line.
func normalizeCategory(raw []byte, category string) []byte {
	nl := "\n"
	text := string(raw)
	if strings.Contains(text, "\r\n") {
		nl = "\r\n"
		text = strings.ReplaceAll(text, "\r\n", "\n")
	}
	line := categoryLinePrefix + " " + category
	lines := strings.Split(text, "\n")

	severityIdx, headerIdx := -1, -1
	for i, l := range lines {
		// Stop at the first body marker: patterns.Parse treats everything from
		// the first `## ` line as body, so a `**Category**:` line past it is not
		// real frontmatter and must not be rewritten.
		if strings.HasPrefix(l, "## ") {
			break
		}
		switch {
		case strings.HasPrefix(l, categoryLinePrefix):
			lines[i] = line
			return []byte(strings.Join(lines, nl))
		case strings.HasPrefix(l, "**Severity**:"):
			severityIdx = i
		case headerIdx == -1 && strings.HasPrefix(l, "# Review Pattern:"):
			headerIdx = i
		}
	}

	insertAt := 0
	switch {
	case severityIdx >= 0:
		insertAt = severityIdx + 1
	case headerIdx >= 0:
		insertAt = headerIdx + 1
	}
	out := make([]string, 0, len(lines)+1)
	out = append(out, lines[:insertAt]...)
	out = append(out, line)
	out = append(out, lines[insertAt:]...)
	return []byte(strings.Join(out, nl))
}

// writeResult is one pattern file written by writeWorkingTree: its repo-relative
// path (subdir/stem.md, in slash form for reporting) and whether it replaced an
// existing file (true) or created a new one (false).
type writeResult struct {
	Path     string
	Replaced bool
}

// writeWorkingTree writes each entry to <baseDir>/<subdir>/<stem>.md, applying
// transform to the bytes first when it is non-nil. It returns one writeResult
// per file written.
//
// The destination filename is the wiki-controlled stem, so a wiki author can
// name a pattern to collide with a trusted file (a shipped catalog pattern, or
// an existing repo pattern) and overwrite it with wiki bytes. To stop an
// untrusted wiki from clobbering trusted content, every destination is checked
// for an existing file before anything is written: a collision is refused unless
// overwrite is set. The check runs as a pre-pass so a collision aborts the whole
// operation with nothing on disk, rather than leaving earlier files half-applied.
func writeWorkingTree(baseDir, subdir string, entries []entry, transform func([]byte) []byte, overwrite bool) ([]writeResult, error) {
	dir := filepath.Join(baseDir, subdir)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("creating %s: %w", dir, err)
	}

	results := make([]writeResult, len(entries))
	for i, e := range entries {
		rel := filepath.Join(subdir, e.Stem+mdExt)
		results[i].Path = filepath.ToSlash(rel)
		if _, err := os.Stat(filepath.Join(baseDir, rel)); err == nil {
			if !overwrite {
				return nil, fmt.Errorf("%s already exists; rename the wiki pattern or pass --overwrite to replace it", results[i].Path)
			}
			results[i].Replaced = true
		}
	}

	for i, e := range entries {
		content := e.Raw
		if transform != nil {
			content = transform(e.Raw)
		}
		rel := filepath.Join(subdir, e.Stem+mdExt)
		if err := os.WriteFile(filepath.Join(baseDir, rel), content, 0o600); err != nil {
			if i > 0 {
				done := make([]string, i)
				for k := range results[:i] {
					done[k] = results[k].Path
				}
				return nil, fmt.Errorf("writing %s (already wrote %s): %w", results[i].Path, strings.Join(done, ", "), err)
			}
			return nil, fmt.Errorf("writing %s: %w", results[i].Path, err)
		}
	}
	return results, nil
}

// wikiProvenance renders the wiki source the patterns were extracted from, in
// the "owner/repo.wiki @ <short-sha>" form the report headers use, so every
// artifact records exactly which wiki state it was anchored from.
func wikiProvenance(wiki patterns.ResolvedWiki) string {
	sha := wiki.CommitSHA
	if len(sha) > 7 {
		sha = sha[:7]
	}
	switch {
	case wiki.Repo != "" && sha != "":
		return fmt.Sprintf("%s.wiki @ %s", wiki.Repo, sha)
	case wiki.Repo != "":
		return wiki.Repo + ".wiki"
	default:
		return "the target repository wiki"
	}
}

// prTitle renders the PR title for the anchored patterns.
func prTitle(selected []entry) string {
	if len(selected) == 1 {
		return "Anchor wiki review pattern: " + selected[0].Stem
	}
	return fmt.Sprintf("Anchor %d wiki review patterns", len(selected))
}

// prCommit renders the commit subject and body for the anchored patterns.
func prCommit(selected []entry, wiki patterns.ResolvedWiki) string {
	return prTitle(selected) + "\n\nExtracted from " + wikiProvenance(wiki) + " by planwerk-review extract."
}

// prBody renders the PR description: what was anchored, from which wiki state,
// and the planwerk-review attribution footer every generated artifact carries.
func prBody(selected []entry, wiki patterns.ResolvedWiki) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "This PR anchors review pattern(s) from %s into `%s/`.\n\n",
		wikiProvenance(wiki), filepath.ToSlash(planwerkPatternsSubdir))
	sb.WriteString("Patterns:\n\n")
	for _, e := range selected {
		if e.Name != "" {
			fmt.Fprintf(&sb, "- `%s.md` — %s\n", e.Stem, e.Name)
		} else {
			fmt.Fprintf(&sb, "- `%s.md`\n", e.Stem)
		}
	}
	sb.WriteString("\n")
	fmt.Fprintf(&sb, "_Generated by %s extract_\n", attribution.Tool())
	return sb.String()
}

// reportWritten prints the extracted files and their destination to w, marking
// any file that replaced an existing one so an overwrite (only possible under
// --overwrite) is never reported as an indistinguishable create.
func reportWritten(w io.Writer, written []writeResult, dest string, wiki patterns.ResolvedWiki) {
	_, _ = fmt.Fprintf(w, "Extracted %d pattern(s) from %s into %s:\n", len(written), wikiProvenance(wiki), dest)
	for _, r := range written {
		if r.Replaced {
			_, _ = fmt.Fprintf(w, "  %s (overwrote existing)\n", r.Path)
		} else {
			_, _ = fmt.Fprintf(w, "  %s\n", r.Path)
		}
	}
}
