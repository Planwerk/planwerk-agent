package hygiene

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/planwerk/planwerk-agent/internal/report"
)

// VerifySnippets enforces the quote-or-demote gate (#23): a finding whose
// code_snippet cannot be located in the changed files is downgraded to
// "uncertain" confidence — never dropped — so the renderer routes it to the
// Unverified section. This targets the largest false-positive class in LLM
// review (a hallucinated "this symbol does not exist" finding quotes code that
// is not actually there) while preserving a legitimate finding that merely
// quoted an imprecise snippet.
//
// Matching is whitespace- and diff-marker-insensitive so indentation or a
// leading +/- carried over from git diff output never causes a false demotion.
// It returns the number of findings demoted.
//
// The gate records what it did on the result: every examined finding is stamped
// with its SnippetCheck outcome (SnippetCheckPassed on a match, one of the
// snippetReason* strings on a demotion), and the run-level counts land in
// result.Gates.Snippet so a run where everything passed is distinguishable from
// one where the gate never ran.
//
// When no changed-file content can be loaded (empty diff, unreadable checkout)
// the gate is skipped entirely and nothing is demoted or recorded: without
// ground truth a "not found" result is meaningless and would spuriously bury
// every finding, and a nil Gates.Snippet keeps that skip visible.
func VerifySnippets(result *report.ReviewResult, dir string, changedFiles []string) int {
	if result == nil {
		return 0
	}
	haystack := normalizeForMatch(loadChangedContent(dir, changedFiles))
	if haystack == "" {
		return 0 // no ground truth — do not demote blindly; record nothing
	}
	examined, demoted := 0, 0
	for i := range result.Findings {
		f := &result.Findings[i]
		if f.Confidence == report.ConfidenceUncertain {
			continue // already lowest confidence; not examined, not stamped
		}
		examined++
		// The snippet may be quoted verbatim from `git diff` output, so strip its
		// leading +/- markers before normalizing (see stripDiffMarkers); the
		// haystack is on-disk source and is left untouched.
		needle := normalizeForMatch(stripDiffMarkers(f.CodeSnippet))
		switch {
		case needle == "":
			// A finding with no quoted evidence cannot be confirmed.
			f.Confidence = report.ConfidenceUncertain
			f.SnippetCheck = snippetReasonNoQuote
			demoted++
		case strings.Contains(haystack, needle):
			f.SnippetCheck = report.SnippetCheckPassed
		default:
			f.Confidence = report.ConfidenceUncertain
			f.SnippetCheck = snippetReasonNotFound
			demoted++
		}
	}
	ensureGates(result).Snippet = &report.SnippetGateStats{Examined: examined, Demoted: demoted}
	return demoted
}

// snippetReason* are the SnippetCheck strings the gate stamps on a demoted
// finding, kept as constants so the writer, the renderer, and the tests share
// one spelling. snippetReasonNoQuote covers a finding whose quoted evidence is
// empty or whitespace-only; snippetReasonNotFound covers a snippet absent from
// the changed files.
const (
	snippetReasonNoQuote  = "demoted: the finding quotes no code to verify"
	snippetReasonNotFound = "demoted: quoted code not found in the changed files"
)

// ensureGates returns result.Gates, allocating it on first use so each gate can
// record into a shared object without the caller pre-initializing it.
func ensureGates(result *report.ReviewResult) *report.GateStats {
	if result.Gates == nil {
		result.Gates = &report.GateStats{}
	}
	return result.Gates
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

// stripDiffMarkers removes the single leading diff column ('+' or '-') each line
// may carry when a snippet is quoted verbatim from `git diff` output. Only the
// needle (the diff-derived snippet) passes through it — never the haystack:
// on-disk source carries no diff prefixes, so stripping a marker there would
// corrupt a line whose own content legitimately begins with '+'/'-' (e.g. a YAML
// or Markdown list item '- foo'), leaving 'foo' in the haystack while a
// double-marked snippet ('+- foo', an added line quoted from the diff) keeps its
// genuine '-foo' — a mismatch that falsely demotes the finding. Exactly one
// marker is stripped, so that added '- foo' line quoted as '+- foo' still yields
// '- foo'.
func stripDiffMarkers(s string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if len(line) > 0 && (line[0] == '+' || line[0] == '-') {
			lines[i] = line[1:]
		}
	}
	return strings.Join(lines, "\n")
}

// normalizeForMatch strips every whitespace character so matching ignores
// indentation and line breaks. The haystack passes through it directly; the
// needle is marker-stripped first (see stripDiffMarkers), so a snippet quoted
// verbatim from git diff output still matches the on-disk source.
func normalizeForMatch(s string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case ' ', '\t', '\n', '\r', '\f', '\v':
			return -1
		}
		return r
	}, s)
}
