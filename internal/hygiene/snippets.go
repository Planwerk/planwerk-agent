package hygiene

import (
	"bytes"
	"io/fs"
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
// The changed files are the primary ground truth, but a finding may legitimately
// quote code in a file the change references without modifying — a cross-file
// regression such as a new caller that makes an existing routine quadratic. So a
// snippet absent from the changed files is checked once more against the whole
// checkout (loaded lazily, at most once per run): found there it is verified —
// real code, not a hallucination — rather than demoted, and stamped
// snippetPassedOutsideDiff so the outside-diff provenance stays visible. Only a
// snippet found nowhere in the checkout is demoted.
//
// Matching is whitespace-, diff-marker-, and line-comment-marker-insensitive so
// indentation, a leading +/- carried over from git diff output, or the interior
// // markers a multi-line comment carries at each line break never cause a false
// demotion. It returns the number of findings demoted.
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
	changedHaystack := matchable(loadChangedContent(dir, changedFiles))
	if changedHaystack == "" {
		return 0 // no ground truth — do not demote blindly; record nothing
	}
	// checkoutHaystack is the whole-checkout fallback, built lazily the first time
	// a snippet misses the changed files so the common path (every snippet quoted
	// from the diff) never pays to walk the tree.
	var checkoutHaystack string
	var checkoutTried bool
	examined, demoted, recovered := 0, 0, 0
	for i := range result.Findings {
		f := &result.Findings[i]
		if f.Confidence == report.ConfidenceUncertain {
			continue // already lowest confidence; not examined, not stamped
		}
		examined++
		// matchableSnippet strips the snippet's leading +/- diff markers and its
		// line-comment markers before whitespace normalization, so a snippet quoted
		// verbatim from `git diff` output — or a multi-line // comment quoted as
		// prose — still matches the on-disk source.
		needle := matchableSnippet(f.CodeSnippet)
		switch {
		case needle == "":
			// A finding with no quoted evidence cannot be confirmed.
			f.Confidence = report.ConfidenceUncertain
			f.SnippetCheck = snippetReasonNoQuote
			demoted++
		case strings.Contains(changedHaystack, needle):
			f.SnippetCheck = report.SnippetCheckPassed
		default:
			// Absent from the diff — fall back to the whole checkout so a real
			// cross-file finding (its evidence in a file the change references but
			// did not modify) is verified rather than demoted as hallucinated.
			if !checkoutTried {
				checkoutHaystack = matchable(loadCheckoutContent(dir))
				checkoutTried = true
			}
			if checkoutHaystack != "" && strings.Contains(checkoutHaystack, needle) {
				f.SnippetCheck = snippetPassedOutsideDiff
				recovered++
			} else {
				f.Confidence = report.ConfidenceUncertain
				f.SnippetCheck = snippetReasonNotFound
				demoted++
			}
		}
	}
	ensureGates(result).Snippet = &report.SnippetGateStats{Examined: examined, Demoted: demoted, RecoveredOutsideDiff: recovered}
	return demoted
}

// snippetReason* are the SnippetCheck strings the gate stamps on a demoted
// finding, kept as constants so the writer, the renderer, and the tests share
// one spelling. snippetReasonNoQuote covers a finding whose quoted evidence is
// empty or whitespace-only; snippetReasonNotFound covers a snippet found neither
// in the changed files nor anywhere else in the checkout.
const (
	snippetReasonNoQuote  = "demoted: the finding quotes no code to verify"
	snippetReasonNotFound = "demoted: quoted code not found in the checkout"
)

// snippetPassedOutsideDiff is stamped on a finding whose quoted code was not in
// the changed files but WAS found elsewhere in the checkout — the cross-file
// case where the change makes existing, unmodified code matter (e.g. a new
// caller that turns an existing loop quadratic). The finding is verified, not
// demoted; the distinct wording (vs report.SnippetCheckPassed) keeps it visible
// that the evidence sits outside the diff.
const snippetPassedOutsideDiff = "verified: quoted code found outside the changed files"

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

const (
	// maxCheckoutHaystackBytes bounds the total fallback haystack so a large
	// repository cannot make the gate read an unbounded amount into memory. The
	// fallback is built lazily (only when a snippet misses the changed files) and
	// at most once per run, so this is a backstop the common path never nears.
	maxCheckoutHaystackBytes = 32 << 20 // 32 MiB
	// maxHaystackFileBytes skips any single outsized file (a generated blob, a
	// checked-in fixture) rather than read it whole into memory; reviewed source
	// snippets never live in a file this large.
	maxHaystackFileBytes = 4 << 20 // 4 MiB
)

// loadCheckoutContent reads and concatenates the current content of the readable
// source files under dir, so a finding whose quoted code lives in a file the
// change did not modify can still be verified as real rather than demoted as
// hallucinated — the cross-file class the changed-files haystack cannot see. It
// walks the checkout once, skipping the version-control and dependency trees
// that never hold reviewed source and any file that looks binary or is outsized,
// and stops once maxCheckoutHaystackBytes is reached. Unreadable entries are
// skipped; the walk never aborts on an I/O error.
func loadCheckoutContent(dir string) string {
	var sb strings.Builder
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable entry — skip it, never abort the whole walk
		}
		if d.IsDir() {
			if skipHaystackDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if sb.Len() >= maxCheckoutHaystackBytes {
			return filepath.SkipAll // budget reached — stop the walk, best-effort
		}
		if info, err := d.Info(); err == nil && info.Size() > maxHaystackFileBytes {
			return nil // skip an outsized file rather than read it whole into memory
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil || looksBinary(data) {
			return nil
		}
		sb.Write(data)
		sb.WriteByte('\n')
		return nil
	})
	return sb.String()
}

// skipHaystackDir reports whether a directory never holds reviewed source and so
// is skipped whole when building the checkout-wide fallback haystack: the git
// metadata and the vendored dependency trees. Skipping them keeps the fallback
// bounded and stops a snippet from matching vendored third-party code.
func skipHaystackDir(name string) bool {
	switch name {
	case ".git", "vendor", "node_modules":
		return true
	}
	return false
}

// looksBinary reports whether data appears to be a binary file — a NUL byte in
// the first few KiB — so the fallback haystack stays text and never pulls a
// compiled artifact or image into the match.
func looksBinary(data []byte) bool {
	const sniff = 8192
	if len(data) > sniff {
		data = data[:sniff]
	}
	return bytes.IndexByte(data, 0) >= 0
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

// stripLineComments removes a leading // line-comment marker (after any
// indentation) from each line, so a multi-line // comment collapses to its prose
// and a finding that quotes the comment without reproducing the interior // at
// each line break still matches. Unlike stripDiffMarkers it is applied to BOTH
// the haystack and the needle, so the two converge. Only // is stripped — not #,
// *, or -- — because those collide with legitimate leading content (a Markdown
// bullet, a diff marker) and would loosen the match past the point the
// quote-or-demote gate can still prove a snippet is real.
func stripLineComments(s string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if trimmed := strings.TrimLeft(line, " \t"); strings.HasPrefix(trimmed, "//") {
			lines[i] = trimmed[2:]
		}
	}
	return strings.Join(lines, "\n")
}

// matchable renders on-disk source (a haystack) into the comparison form: line
// comments and all whitespace removed. Both the changed-files and checkout
// haystacks pass through it so they compare identically to a needle.
func matchable(s string) string {
	return normalizeForMatch(stripLineComments(s))
}

// matchableSnippet renders a finding's quoted snippet into the same comparison
// form as matchable, first stripping the leading +/- diff markers a snippet
// quoted verbatim from `git diff` output carries (see stripDiffMarkers). Diff
// markers are stripped from the needle only; comment markers and whitespace from
// both sides.
func matchableSnippet(s string) string {
	return normalizeForMatch(stripLineComments(stripDiffMarkers(s)))
}

// normalizeForMatch strips every whitespace character so matching ignores
// indentation and line breaks. It is the final step of matchable (haystack) and
// matchableSnippet (needle), which first remove comment and diff markers so both
// sides compare identically.
func normalizeForMatch(s string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case ' ', '\t', '\n', '\r', '\f', '\v':
			return -1
		}
		return r
	}, s)
}
