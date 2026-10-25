package hygiene

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/planwerk/planwerk-agent/internal/report"
)

// AnchorFindings places each finding on the code its code_snippet quotes, in
// the HEAD content of the checkout under dir. It must run before MergeResults,
// because the merge matches findings by file and line (±dedupLineTolerance): a
// pass that miscounted a line would otherwise report the same defect twice.
//
// It anchors primary's findings and those of every non-nil secondary. A finding
// is examined when it names a file and quotes non-blank code. Its quote is
// matched line by line against that file, ignoring whitespace and // markers,
// skipping blank lines, and accepting each quote line with or without one
// leading '+' diff marker. A leading '-' is kept as the line's own content (a
// YAML or Markdown list item), so a deleted line matches no copy of it that
// survives at HEAD. A quote of a replaced hunk (some lines '+', some '-') is
// also tried by its added lines alone. A quote is distinctive when one of its
// lines is at least minDistinctiveLineLen long; a block of boilerplate lines
// is not, however many it strings together. Then:
//
//   - a model range that lies inside a match is kept (LineCheckAnchored);
//   - so is a model range that touches no match but holds one of the quote's
//     distinctive lines, as the whole line or within it, which covers a quote
//     with an elided, trimmed or reconstructed line (LineCheckAnchored);
//   - otherwise the finding moves to the only match, or, when the model gave a
//     line and the quote is distinctive, to the match nearest that line
//     (LineCheckReanchored);
//   - a distinctive quote whose file holds none of its distinctive lines, even
//     within a longer line, but that matches at exactly one place across the
//     other changedFiles moves the finding's File, Line and LineEnd there
//     (LineCheckRelocated);
//   - anything else keeps the model's line (LineCheckUnanchored).
//
// Every finding is searched in its own file before any is looked for in the
// other changed files, so the read budget relocation spends never leaves a
// finding's own file unread. No finding is relocated while a file it would be
// searched in, its own or another changed file, was left unread for its size
// or the budget, since that file may hold the quote.
//
// Only the head content is searched, never a diff's old side, so every line it
// sets is a RIGHT-side line. A deleted line quoted without its '-' reads as
// head code, which the anchor cannot tell apart without the diff, so it may
// still land on a surviving copy. It clears any LineCheck a finding arrived
// with, stamps LineCheck on each examined finding, records the summed counts
// on primary.Gates.Anchor (also when nothing was examined) and returns them.
// The counts are per pass: a defect that two passes report is counted twice.
// It moves no finding between report sections. It does nothing and returns
// nil when primary is nil or dir is empty. changedFiles is the relocation set;
// nil disables relocation (decision 139).
func AnchorFindings(primary *report.ReviewResult, dir string, changedFiles []string, secondary ...*report.ReviewResult) *report.AnchorGateStats {
	if primary == nil || dir == "" {
		return nil
	}
	a := &anchorer{dir: dir, changed: changedFiles, files: make(map[string]headFile)}
	results := append([]*report.ReviewResult{primary}, secondary...)
	var homeless []*report.Finding
	for _, res := range results {
		if res == nil {
			continue
		}
		for i := range res.Findings {
			f := &res.Findings[i]
			// The pipeline owns the record: a line_check the model emitted
			// never survives.
			check, relocate := a.anchorInFile(f)
			f.LineCheck = check
			if relocate {
				homeless = append(homeless, f)
			}
		}
	}
	// Relocation reads the other changed files, so it waits until every
	// finding's own file has been read within the budget.
	for _, f := range homeless {
		if file, s, ok := a.relocate(f.File, quoteForms(f.CodeSnippet)); ok {
			f.File = file
			move(f, s)
			f.LineCheck = report.LineCheckRelocated
		}
	}
	stats := &report.AnchorGateStats{}
	for _, res := range results {
		if res == nil {
			continue
		}
		for _, f := range res.Findings {
			if f.LineCheck == "" {
				continue
			}
			stats.Examined++
			switch f.LineCheck {
			case report.LineCheckReanchored:
				stats.Reanchored++
			case report.LineCheckRelocated:
				stats.Relocated++
			case report.LineCheckUnanchored:
				stats.Unanchored++
			}
		}
	}
	ensureGates(primary).Anchor = stats
	if stats.Reanchored+stats.Relocated+stats.Unanchored > 0 {
		slog.Info("anchored findings on the code they quote",
			"examined", stats.Examined, "reanchored", stats.Reanchored,
			"relocated", stats.Relocated, "unanchored", stats.Unanchored)
	}
	return stats
}

// span is a matched quote's place in a file: 1-based line numbers, inclusive.
type span struct{ start, end int }

// headLine is a non-blank line of a file at HEAD, in comparison form, with its
// 1-based line number.
type headLine struct {
	num  int
	text string
}

// quoteLine is one non-blank line of a finding's quote in comparison form: raw
// keeps a leading '+' that may be the line's own content, bare has it stripped
// as a diff marker. A leading '-' stays in both, as the line's own content (a
// YAML or Markdown list item): as a diff marker it marks deleted code, which
// HEAD does not hold.
type quoteLine struct{ raw, bare string }

// headFile is one file's comparison-form lines as the anchorer cached them.
// skipped marks a regular file left unread for its size or the read budget:
// its content is unknown, not empty.
type headFile struct {
	lines   []headLine
	skipped bool
}

// anchorer holds one AnchorFindings call's state: the checkout, the relocation
// set, and the files asked for so far, cached by cleaned path. read counts the
// bytes read, bounded by maxCheckoutHaystackBytes.
type anchorer struct {
	dir     string
	changed []string
	files   map[string]headFile
	read    int
}

// anchorInFile places f on the code it quotes within f's own file and returns
// its LineCheck value, or "" when f is not examined: it names no file or quotes
// no code. relocate is true for an unanchored finding whose file holds none of
// the quote's distinctive lines, which the caller may then look for in the
// other changed files; it is false when the file was left unread for its size
// or the read budget.
func (a *anchorer) anchorInFile(f *report.Finding) (check string, relocate bool) {
	if f.File == "" {
		return "", false
	}
	forms := quoteForms(f.CodeSnippet)
	if forms == nil {
		return "", false
	}
	lo, hi := f.Line, max(f.LineEnd, f.Line)
	own, skipped := a.lines(f.File)
	spans, distinct := matchQuote(own, forms)
	if lo > 0 {
		for _, s := range spans {
			if s.start <= lo && hi <= s.end {
				return report.LineCheckAnchored, false
			}
		}
		// A range that touches no match but holds a distinctive quote line
		// names a place of its own that holds the quoted code, so a whole-line
		// copy elsewhere does not claim the finding. A range that overlaps a
		// match is narrowed or shifted onto it below.
		if !overlapsAny(spans, lo, hi) && holdsDistinctiveLine(own, forms[0], lo, hi) {
			return report.LineCheckAnchored, false
		}
	}
	if len(spans) > 0 {
		switch {
		case len(spans) == 1:
			move(f, spans[0])
			return report.LineCheckReanchored, false
		case lo > 0 && distinct:
			move(f, nearestSpan(spans, lo, hi))
			return report.LineCheckReanchored, false
		}
		// Several places and no line, or no distinctive line, to choose by.
		return report.LineCheckUnanchored, false
	}
	// The file holds the quoted code, trimmed or elided, off the model's
	// range: the line is wrong, but the file is not, so it stays.
	if len(own) > 0 && holdsDistinctiveLine(own, forms[0], own[0].num, own[len(own)-1].num) {
		return report.LineCheckUnanchored, false
	}
	// A file left unread may hold the quote, so it is not looked for elsewhere.
	return report.LineCheckUnanchored, !skipped
}

// relocate looks for the quote in the changed files other than file, and
// returns the changed-file path, as listed, and the place when the quote occurs
// exactly once across them. Only distinctive forms are searched, so a quote of
// boilerplate never moves a finding to another file. A changed file left unread
// for its size or the read budget may hold a second copy, so it ends the search
// with no place.
func (a *anchorer) relocate(file string, forms [][]quoteLine) (string, span, bool) {
	var strong [][]quoteLine
	for _, q := range forms {
		if distinctive(q) {
			strong = append(strong, q)
		}
	}
	if len(strong) == 0 {
		return "", span{}, false
	}
	own := filepath.Clean(file)
	var (
		found string
		at    span
		total int
	)
	for _, rel := range a.changed {
		if filepath.Clean(rel) == own {
			continue
		}
		lines, skipped := a.lines(rel)
		if skipped {
			return "", span{}, false // its content is unknown, so no place is known to be the only one
		}
		spans, _ := matchQuote(lines, strong)
		if len(spans) == 0 {
			continue
		}
		total += len(spans)
		if total > 1 {
			return "", span{}, false // ambiguous: no verdict can come from looking further
		}
		found, at = rel, spans[0]
	}
	return found, at, total == 1
}

// lines returns the comparison-form lines of rel at HEAD, reading the file the
// first time it is asked for. A file that cannot be read returns nil: a path
// that leaves the checkout (repoPath), a missing file, anything but a regular
// file, or binary data. A regular file over maxHaystackFileBytes, or any file
// once the call has read maxCheckoutHaystackBytes, also returns nil, with
// skipped true: its content is unknown rather than absent.
func (a *anchorer) lines(rel string) (lines []headLine, skipped bool) {
	key := filepath.Clean(rel)
	if hf, ok := a.files[key]; ok {
		return hf.lines, hf.skipped
	}
	data, skipped := a.load(rel)
	hf := headFile{skipped: skipped}
	if data != nil {
		hf.lines = headLines(string(data))
	}
	a.files[key] = hf
	return hf.lines, hf.skipped
}

// load reads rel under the checkout within the limits lines documents. It
// returns nil data for a file it cannot or may not read, and skipped for a
// regular file it left unread for its size or the read budget.
func (a *anchorer) load(rel string) (data []byte, skipped bool) {
	path, ok := repoPath(a.dir, rel)
	if !ok {
		return nil, false
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return nil, false
	}
	if info.Size() > maxHaystackFileBytes || a.read >= maxCheckoutHaystackBytes {
		return nil, true
	}
	data, err = os.ReadFile(path)
	if err != nil || looksBinary(data) {
		return nil, false
	}
	a.read += len(data)
	return data, false
}

// headLines splits a file into its non-blank lines in comparison form (line
// comment markers and all whitespace removed), each with its 1-based number.
func headLines(content string) []headLine {
	var out []headLine
	for i, line := range strings.Split(content, "\n") {
		if text := normalizeForMatch(stripLineComments(line)); text != "" {
			out = append(out, headLine{num: i + 1, text: text})
		}
	}
	return out
}

// quoteForms returns the forms a quote is matched in, or nil when it has no
// non-blank line. Form 0 is the quote itself, one quoteLine per non-blank line;
// a '-' line in it matches only as written, so a deleted line never lands on a
// copy of the deleted code that survives at HEAD. A quote with both '+' and '-'
// lines, a replacement copied out of git diff, also gets form 1, its new side:
// the '-' lines dropped and one '+' stripped from the rest, since only the new
// side exists at HEAD.
func quoteForms(snippet string) [][]quoteLine {
	lines := strings.Split(snippet, "\n")
	var quote []quoteLine
	added, deleted := false, false
	for _, line := range lines {
		added = added || strings.HasPrefix(line, "+")
		deleted = deleted || strings.HasPrefix(line, "-")
		bare := normalizeForMatch(stripLineComments(stripDiffMarkers(line)))
		if bare == "" {
			continue
		}
		raw := normalizeForMatch(stripLineComments(line))
		if strings.HasPrefix(line, "-") {
			bare = raw // deleted code is not at HEAD; a '-' that is the line's own content matches as written
		}
		quote = append(quote, quoteLine{raw: raw, bare: bare})
	}
	if len(quote) == 0 {
		return nil
	}
	forms := [][]quoteLine{quote}
	if !added || !deleted {
		return forms
	}
	var newSide []quoteLine
	for _, line := range lines {
		if strings.HasPrefix(line, "-") {
			continue
		}
		if text := normalizeForMatch(stripLineComments(strings.TrimPrefix(line, "+"))); text != "" {
			newSide = append(newSide, quoteLine{raw: text, bare: text})
		}
	}
	if len(newSide) > 0 {
		forms = append(forms, newSide)
	}
	return forms
}

// matchQuote returns the places of the first form that matches at least once,
// and whether that form is distinctive. When no form matches it returns nil
// and false.
func matchQuote(lines []headLine, forms [][]quoteLine) ([]span, bool) {
	for _, q := range forms {
		if spans := findSpans(lines, q); len(spans) > 0 {
			return spans, distinctive(q)
		}
	}
	return nil, false
}

// findSpans returns every place, in file order, where the quote's lines match
// consecutive non-blank file lines, each line equal to the quote line's bare or
// raw form. A blank line between two quoted lines falls inside the span.
func findSpans(lines []headLine, q []quoteLine) []span {
	var spans []span
	for i := 0; i+len(q) <= len(lines); i++ {
		match := true
		for j := range q {
			if t := lines[i+j].text; t != q[j].bare && t != q[j].raw {
				match = false
				break
			}
		}
		if match {
			spans = append(spans, span{start: lines[i].num, end: lines[i+len(q)-1].num})
		}
	}
	return spans
}

// distinctive reports whether one of the quote's lines is at least
// minDistinctiveLineLen long in comparison form, the evidence the anchor needs
// before it chooses between places. The test is per line, as the snippet
// gate's distinctiveSnippetLines is: `if err != nil {`, `return err` and `}`
// stay boilerplate when quoted together.
func distinctive(q []quoteLine) bool {
	for _, l := range q {
		if len(l.bare) >= minDistinctiveLineLen {
			return true
		}
	}
	return false
}

// holdsDistinctiveLine reports whether a file line within [lo, hi] holds one
// of the quote's lines that is at least minDistinctiveLineLen long, as the
// whole line or within it: a quote that left out a trailing comment, or quoted
// one expression of the line, still confirms the line. It only confirms a
// line or keeps a finding in its file, never moves one, so the looser match is
// safe.
func holdsDistinctiveLine(lines []headLine, q []quoteLine, lo, hi int) bool {
	for _, l := range lines {
		if l.num < lo || l.num > hi {
			continue
		}
		for _, ql := range q {
			if len(ql.bare) >= minDistinctiveLineLen && (strings.Contains(l.text, ql.bare) || strings.Contains(l.text, ql.raw)) {
				return true
			}
		}
	}
	return false
}

// overlapsAny reports whether some span shares a line with the range [lo, hi].
func overlapsAny(spans []span, lo, hi int) bool {
	for _, s := range spans {
		if spanDistance(s, lo, hi) == 0 {
			return true
		}
	}
	return false
}

// nearestSpan returns the span closest to the range [lo, hi]: distance 0 when
// they overlap, the gap between them otherwise. A tie goes to the earlier span.
func nearestSpan(spans []span, lo, hi int) span {
	best, bestDist := spans[0], spanDistance(spans[0], lo, hi)
	for _, s := range spans[1:] {
		if d := spanDistance(s, lo, hi); d < bestDist {
			best, bestDist = s, d
		}
	}
	return best
}

// spanDistance is the number of lines between s and [lo, hi], 0 when they
// overlap.
func spanDistance(s span, lo, hi int) int {
	switch {
	case s.end < lo:
		return lo - s.end
	case s.start > hi:
		return s.start - hi
	}
	return 0
}

// move puts f on s; a single-line span leaves LineEnd 0.
func move(f *report.Finding, s span) {
	f.Line = s.start
	f.LineEnd = 0
	if s.end > s.start {
		f.LineEnd = s.end
	}
}
