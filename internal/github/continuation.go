package github

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// MaxIssueBodyLen is GitHub's cap on an issue body and on a comment, in
// characters. Go's len counts bytes, which is never smaller than the character
// count, so a part that fits in bytes fits in characters.
const MaxIssueBodyLen = 65536

// A document that does not fit in one issue body is written as the body plus
// continuation comments. The body ends (before its attribution footer) with a
// `continued` marker and a visible pointer line; each continuation comment
// opens with a `continuation` marker and a visible note, then carries the
// sections that did not fit. Every reader of an issue body — the implement,
// elaborate, and prompt commands, and the skills — merges the parts back into
// one document before it reads a single section, so the split changes where
// the text is stored and nothing else. The markers, not the prose, are the
// contract: plugins/planwerk/shared/issue-format.md documents them for the
// skills, which write and read the same shape by hand.
const (
	continuedMarkerFmt    = "<!-- planwerk-agent:continued %d/%d -->"
	continuationMarkerFmt = "<!-- planwerk-agent:continuation %d/%d -->"
	// continuedPointerPrefix opens the visible line under the body's marker;
	// continuationNotePrefix opens the visible line under a comment's marker.
	// The merge strips a line by its prefix, so the rest of the sentence is
	// free to name the parts and the sections.
	continuedPointerPrefix = "_This body continues in "
	continuationNotePrefix = "_Issue body, continued "
	// continuationReserve is the room every part keeps free for its marker,
	// its pointer or note line, and the blank lines around them.
	continuationReserve = 1024
	// minPartFill is the fraction of a part's limit a cut must reach before a
	// weaker boundary is preferred over a stronger one further up: a `## `
	// heading in the first lines of a document is not a place to cut.
	minPartFill = 2
)

var (
	continuedMarkerRe    = regexp.MustCompile(`^<!-- planwerk-agent:continued (\d+)/(\d+) -->\s*$`)
	continuationMarkerRe = regexp.MustCompile(`^<!-- planwerk-agent:continuation (\d+)/(\d+) -->\s*$`)
)

// IsContinued reports whether body carries the `continued` marker, meaning the
// rest of the document lives in continuation comments on the same issue.
func IsContinued(body string) bool {
	_, _, ok := stripContinuedMarker(body)
	return ok
}

// SplitIssueBody cuts a rendered issue document into the parts GitHub accepts:
// the body first, then one comment per further part. A document within
// MaxIssueBodyLen comes back as a single part, unchanged. A longer one is cut
// at the strongest Markdown boundary that fills the part — a `## ` heading,
// then `### `, then any heading, then a blank line — never inside a fenced code
// block, and only as a last resort at an arbitrary rune boundary. The
// attribution footer stays on the body, after the pointer that names the parts;
// every continuation comment opens with its marker and note.
func SplitIssueBody(doc string) []string {
	if len(doc) <= MaxIssueBodyLen {
		return []string{doc}
	}
	head, footer := splitFooter(doc)
	limit := MaxIssueBodyLen - continuationReserve - len(footer)
	chunks := cutChunks(head, limit)
	n := len(chunks)
	if n == 1 {
		// The footer alone pushed the document over the cap; nothing to
		// continue with, so hand the caller the document as it is.
		return []string{doc}
	}

	parts := make([]string, 0, n)
	var body strings.Builder
	body.WriteString(strings.TrimRight(chunks[0], "\n"))
	body.WriteString("\n\n")
	fmt.Fprintf(&body, continuedMarkerFmt, 1, n)
	body.WriteString("\n")
	body.WriteString(continuedPointer(n, chunks[1:]))
	body.WriteString("\n")
	if footer != "" {
		body.WriteString("\n" + footer + "\n")
	}
	parts = append(parts, body.String())

	for i, chunk := range chunks[1:] {
		k := i + 2
		var sb strings.Builder
		fmt.Fprintf(&sb, continuationMarkerFmt, k, n)
		sb.WriteString("\n")
		fmt.Fprintf(&sb, "%s(part %d of %d). The sections below belong to the body above, which reached GitHub's %d-character cap. Read them as part of the body; whoever rewrites the body rewrites this comment with it._\n\n",
			continuationNotePrefix, k, n, MaxIssueBodyLen)
		sb.WriteString(strings.TrimSpace(chunk))
		sb.WriteString("\n")
		parts = append(parts, sb.String())
	}
	return parts
}

// continuedPointer renders the visible line under the body's marker: how many
// comments continue the body and, when the cut fell on section boundaries,
// which sections they carry.
func continuedPointer(n int, tail []string) string {
	var sb strings.Builder
	sb.WriteString(continuedPointerPrefix)
	if n == 2 {
		sb.WriteString("a comment below (part 2 of 2")
	} else {
		fmt.Fprintf(&sb, "%d comments below (parts 2 to %d of %d", n-1, n, n)
	}
	if sections := sectionHeadings(tail); len(sections) > 0 {
		sb.WriteString(": ")
		sb.WriteString(strings.Join(sections, ", "))
	}
	fmt.Fprintf(&sb, "). GitHub caps a body at %d characters; read the parts as one document._", MaxIssueBodyLen)
	return sb.String()
}

// sectionHeadings lists the `## ` headings the chunks open, in order and
// without repeats, for the pointer line. A chunk that starts mid-section
// contributes the headings it does contain.
func sectionHeadings(chunks []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, c := range chunks {
		for _, line := range strings.Split(c, "\n") {
			if !strings.HasPrefix(line, "## ") {
				continue
			}
			h := strings.TrimSpace(strings.TrimPrefix(line, "## "))
			if h == "" || seen[h] {
				continue
			}
			seen[h] = true
			out = append(out, h)
		}
	}
	return out
}

// splitFooter detaches the attribution footer — a trailing `---` rule followed
// by a single italic line — from the rest of the document. Both halves come
// back without trailing newlines; footer is "" when the document has none.
func splitFooter(doc string) (head, footer string) {
	trimmed := strings.TrimRight(doc, "\n")
	const rule = "\n---\n"
	i := strings.LastIndex(trimmed, rule)
	if i < 0 {
		return trimmed, ""
	}
	tail := strings.TrimSpace(trimmed[i+len(rule):])
	if tail == "" || strings.ContainsRune(tail, '\n') || !strings.HasPrefix(tail, "_") || !strings.HasSuffix(tail, "_") {
		return trimmed, ""
	}
	return strings.TrimRight(trimmed[:i], "\n"), "---\n\n" + tail
}

// cutChunks cuts s into consecutive pieces of at most limit bytes each, at the
// boundaries cutIndex picks. The last piece carries whatever remains.
func cutChunks(s string, limit int) []string {
	var chunks []string
	for len(s) > limit {
		at := cutIndex(s, limit)
		chunks = append(chunks, strings.TrimRight(s[:at], "\n"))
		s = strings.TrimLeft(s[at:], "\n")
	}
	if strings.TrimSpace(s) != "" || len(chunks) == 0 {
		chunks = append(chunks, s)
	}
	return chunks
}

// boundary is a position in a document where a cut keeps both halves valid
// Markdown, ranked by how natural the cut reads: the higher the rank, the
// better the boundary.
type boundary struct {
	pos  int
	rank int
}

const (
	rankLine = iota + 1
	rankBlank
	rankHeading
	rankH3
	rankH2
)

// cutIndex returns the byte offset in s at which to end the first part so it
// stays within limit. It walks the lines up to limit, tracking fenced code
// blocks so no cut lands inside one, and picks the best-ranked boundary that
// fills at least half the limit; failing that, the best boundary anywhere;
// failing that, a hard cut at a rune boundary.
func cutIndex(s string, limit int) int {
	var candidates []boundary
	inFence := false
	off := 0
	for off < len(s) && off <= limit {
		lineEnd := len(s)
		if nl := strings.IndexByte(s[off:], '\n'); nl >= 0 {
			lineEnd = off + nl
		}
		line := s[off:lineEnd]
		if off > 0 && !inFence {
			rank := rankLine
			switch {
			case strings.HasPrefix(line, "## "):
				rank = rankH2
			case strings.HasPrefix(line, "### "):
				rank = rankH3
			case strings.HasPrefix(line, "#"):
				rank = rankHeading
			case strings.TrimSpace(line) == "":
				rank = rankBlank
			}
			candidates = append(candidates, boundary{pos: off, rank: rank})
		}
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inFence = !inFence
		}
		off = lineEnd + 1
	}

	if best, ok := bestBoundary(candidates, limit/minPartFill); ok {
		return best
	}
	if best, ok := bestBoundary(candidates, 1); ok {
		return best
	}
	at := limit
	for at > 0 && !utf8.RuneStart(s[at]) {
		at--
	}
	if at == 0 {
		at = limit
	}
	return at
}

// bestBoundary picks, among the candidates at or beyond minPos, the one with
// the highest rank, and among equals the latest — the part should be as full
// as its boundary allows.
func bestBoundary(candidates []boundary, minPos int) (int, bool) {
	best := boundary{pos: -1}
	for _, c := range candidates {
		if c.pos < minPos {
			continue
		}
		if c.rank > best.rank || (c.rank == best.rank && c.pos > best.pos) {
			best = c
		}
	}
	return best.pos, best.pos >= 0
}

// MergeContinuations reassembles a document from a body and the comments on
// its issue. A body without the `continued` marker comes back unchanged; one
// with it is joined to the continuation comments in part order, with the
// marker, pointer, and note lines removed and the footer moved back to the end.
// A stale comment — one whose part count differs from the body's — is ignored;
// when two comments claim the same part, the later one wins. A part the body
// announces and no comment carries is an error, because a plan read without
// its Acceptance Criteria is a wrong plan, not a shorter one.
func MergeContinuations(body string, comments []IssueComment) (string, error) {
	head, n, ok := stripContinuedMarker(body)
	if !ok {
		return body, nil
	}
	head, footer := splitFooter(head)

	parts := map[int]string{}
	for _, c := range comments {
		k, total, payload, ok := parseContinuation(c.Body)
		if !ok || total != n || k < 2 || k > n {
			continue
		}
		parts[k] = payload
	}

	var sb strings.Builder
	sb.WriteString(strings.TrimRight(head, "\n"))
	var missing []int
	for k := 2; k <= n; k++ {
		payload, ok := parts[k]
		if !ok {
			missing = append(missing, k)
			continue
		}
		sb.WriteString("\n\n")
		sb.WriteString(strings.TrimSpace(payload))
	}
	if len(missing) > 0 {
		return "", fmt.Errorf("the issue body continues in %d comment(s), but part(s) %v of %d are missing from the issue's comments", n-1, missing, n)
	}
	if footer != "" {
		sb.WriteString("\n\n" + footer)
	}
	sb.WriteString("\n")
	return sb.String(), nil
}

// stripContinuedMarker removes the body's `continued` marker and the pointer
// line under it, returning the rest and the total number of parts. ok is false
// when the body carries no marker.
func stripContinuedMarker(body string) (head string, total int, ok bool) {
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		m := continuedMarkerRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		total, _ = strconv.Atoi(m[2])
		if total < 2 {
			return body, 0, false
		}
		rest := lines[i+1:]
		if j := firstNonEmpty(rest); j >= 0 && strings.HasPrefix(rest[j], continuedPointerPrefix) {
			rest = append(append([]string{}, rest[:j]...), rest[j+1:]...)
		}
		head = strings.Join(append(append([]string{}, lines[:i]...), rest...), "\n")
		return head, total, true
	}
	return body, 0, false
}

// parseContinuation reads a continuation comment: its part number, the total
// it was split against, and the payload after the marker and note lines. ok is
// false for any other comment.
func parseContinuation(comment string) (k, total int, payload string, ok bool) {
	lines := strings.Split(comment, "\n")
	i := firstNonEmpty(lines)
	if i < 0 {
		return 0, 0, "", false
	}
	m := continuationMarkerRe.FindStringSubmatch(lines[i])
	if m == nil {
		return 0, 0, "", false
	}
	k, _ = strconv.Atoi(m[1])
	total, _ = strconv.Atoi(m[2])
	rest := lines[i+1:]
	if j := firstNonEmpty(rest); j >= 0 && strings.HasPrefix(rest[j], continuationNotePrefix) {
		rest = rest[j+1:]
	}
	return k, total, strings.Join(rest, "\n"), true
}

// firstNonEmpty returns the index of the first line that is not blank, or -1.
func firstNonEmpty(lines []string) int {
	for i, l := range lines {
		if strings.TrimSpace(l) != "" {
			return i
		}
	}
	return -1
}

// continuationComments returns the continuation comments among comments, in
// part order (and, within a part, in the order they were posted), so a rewrite
// can reuse them slot by slot.
func continuationComments(comments []IssueComment) []IssueComment {
	type indexed struct {
		k, i int
		c    IssueComment
	}
	var found []indexed
	for i, c := range comments {
		k, _, _, ok := parseContinuation(c.Body)
		if !ok {
			continue
		}
		found = append(found, indexed{k: k, i: i, c: c})
	}
	sort.SliceStable(found, func(a, b int) bool {
		if found[a].k != found[b].k {
			return found[a].k < found[b].k
		}
		return found[a].i < found[b].i
	})
	out := make([]IssueComment, 0, len(found))
	for _, f := range found {
		out = append(out, f.c)
	}
	return out
}

// IssueCommentLister is the one read CompleteIssueBody needs.
type IssueCommentLister interface {
	ListIssueComments(owner, name string, number int) ([]IssueComment, error)
}

// CompleteIssueBody replaces issue.Body with the whole document when the body
// continues in comments, and leaves it alone otherwise. The comment read is
// load-bearing once the body says it is continued: a failure is returned, not
// logged, because every caller is about to plan or implement against the body
// and a silently truncated plan is the failure this exists to prevent.
func CompleteIssueBody(gh IssueCommentLister, issue *Issue) error {
	if issue == nil || !IsContinued(issue.Body) {
		return nil
	}
	comments, err := gh.ListIssueComments(issue.Owner, issue.Name, issue.Number)
	if err != nil {
		return fmt.Errorf("the body of issue #%d continues in comments; reading them: %w", issue.Number, err)
	}
	merged, err := MergeContinuations(issue.Body, comments)
	if err != nil {
		return fmt.Errorf("issue #%d: %w", issue.Number, err)
	}
	issue.Body = merged
	return nil
}

// IssueBodyWriter is the set of writes PublishIssueBody needs.
type IssueBodyWriter interface {
	IssueCommentLister
	EditIssueBody(owner, name string, number int, body string) error
	AddIssueComment(owner, name string, number int, body string) (string, error)
	EditIssueComment(commentID, body string) error
	DeleteIssueComment(commentID string) error
}

// PublishIssueBody writes doc to the issue: the first part as the body, every
// further part as a continuation comment. Continuation comments an earlier
// write left are reused in place, so their position in the thread is stable;
// surplus ones are deleted, so a document that shrank back into one body
// leaves no stale continuation for a reader to merge. It returns the number of
// parts written.
func PublishIssueBody(gh IssueBodyWriter, owner, name string, number int, doc string) (int, error) {
	parts := SplitIssueBody(doc)
	comments, err := gh.ListIssueComments(owner, name, number)
	if err != nil {
		return 0, fmt.Errorf("listing the comments of issue #%d before writing its body: %w", number, err)
	}
	existing := continuationComments(comments)

	if err := gh.EditIssueBody(owner, name, number, parts[0]); err != nil {
		return 0, err
	}
	for i, part := range parts[1:] {
		if i < len(existing) {
			if err := gh.EditIssueComment(existing[i].ID, part); err != nil {
				return 0, fmt.Errorf("rewriting continuation comment %d of issue #%d: %w", i+2, number, err)
			}
			continue
		}
		if _, err := gh.AddIssueComment(owner, name, number, part); err != nil {
			return 0, fmt.Errorf("posting continuation comment %d of issue #%d: %w", i+2, number, err)
		}
	}
	for i := len(parts) - 1; i < len(existing); i++ {
		if err := gh.DeleteIssueComment(existing[i].ID); err != nil {
			return 0, fmt.Errorf("deleting stale continuation comment of issue #%d: %w", number, err)
		}
	}
	return len(parts), nil
}
