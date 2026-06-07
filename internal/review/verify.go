package review

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/planwerk/planwerk-review/internal/report"
)

// verifyFindingSnippets enforces the quote-or-demote gate (#23): a finding
// whose code_snippet cannot be located in the changed files is downgraded to
// "uncertain" confidence — never dropped — so the renderer routes it to the
// Unverified section. This targets the largest false-positive class in LLM
// review (a hallucinated "this symbol does not exist" finding quotes code that
// is not actually there) while preserving a legitimate finding that merely
// quoted an imprecise snippet.
//
// Matching is whitespace-insensitive so indentation or diff-prefix differences
// never cause a false demotion. It returns the number of findings demoted.
//
// When no changed-file content can be loaded (empty diff, unreadable checkout)
// the gate is skipped entirely and nothing is demoted: without ground truth a
// "not found" result is meaningless and would spuriously bury every finding.
func verifyFindingSnippets(result *report.ReviewResult, dir string, changedFiles []string) int {
	if result == nil {
		return 0
	}
	haystack := normalizeForMatch(loadChangedContent(dir, changedFiles))
	if haystack == "" {
		return 0 // no ground truth — do not demote blindly
	}
	demoted := 0
	for i := range result.Findings {
		f := &result.Findings[i]
		if f.Confidence == report.ConfidenceUncertain {
			continue // already lowest confidence; nothing to demote
		}
		if snippetPresent(f.CodeSnippet, haystack) {
			continue
		}
		f.Confidence = report.ConfidenceUncertain
		demoted++
	}
	return demoted
}

// loadChangedContent reads and concatenates the current (HEAD) content of every
// changed file. Unreadable files are skipped. Files are joined with newlines so
// a snippet cannot accidentally match across a file boundary after whitespace
// normalization.
func loadChangedContent(dir string, changedFiles []string) string {
	var sb strings.Builder
	for _, rel := range changedFiles {
		// Defend against path escapes from an untrusted changed-file list.
		clean := filepath.Clean(rel)
		if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || filepath.IsAbs(clean) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, clean))
		if err != nil {
			continue
		}
		sb.Write(data)
		sb.WriteByte('\n')
	}
	return sb.String()
}

// snippetPresent reports whether snippet appears in the already-normalized
// haystack. An empty or whitespace-only snippet is treated as unverifiable
// (false) so the finding is demoted: a finding with no quoted evidence cannot
// be confirmed.
func snippetPresent(snippet, normalizedHaystack string) bool {
	needle := normalizeForMatch(snippet)
	if needle == "" {
		return false
	}
	return strings.Contains(normalizedHaystack, needle)
}

// normalizeForMatch strips every whitespace character so matching ignores
// indentation, line breaks, and any leading diff markers a snippet may carry.
func normalizeForMatch(s string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case ' ', '\t', '\n', '\r', '\f', '\v':
			return -1
		}
		return r
	}, s)
}
