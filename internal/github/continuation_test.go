package github

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

const testFooter = "---\n\n_Elaborated by [planwerk-agent](https://github.com/planwerk/planwerk-agent) with Claude:test_"

// longDocument renders a house-format document whose Description alone is
// well over the cap, with a fenced block near the middle so a cut that landed
// inside it would be visible.
func longDocument(descriptionParagraphs int) string {
	var sb strings.Builder
	sb.WriteString("**Category**: feature | **Scope**: Large\n\n## Description\n\n")
	for i := 0; i < descriptionParagraphs; i++ {
		fmt.Fprintf(&sb, "Paragraph %d of the description, padded so a handful of them exceed the cap. %s\n\n", i, strings.Repeat("x", 900))
		if i == descriptionParagraphs/2 {
			sb.WriteString("```go\nfunc example() {\n\treturn // ## not a heading\n}\n```\n\n")
		}
	}
	sb.WriteString("## Motivation\n\nWhy.\n\n## Affected Areas\n\n- a.go (changes)\n\n## Acceptance Criteria\n\n- [ ] Do the thing\n\n## Non-Goals\n\n- Not that\n\n## References\n\n- README\n\n")
	sb.WriteString(testFooter)
	sb.WriteString("\n")
	return sb.String()
}

func TestSplitIssueBody_FitsIsUnchanged(t *testing.T) {
	doc := "## Description\n\nShort.\n\n" + testFooter + "\n"
	parts := SplitIssueBody(doc)
	if len(parts) != 1 || parts[0] != doc {
		t.Fatalf("a document within the cap must come back as itself; got %d part(s)", len(parts))
	}
	if IsContinued(doc) {
		t.Error("an unsplit document must not read as continued")
	}
}

func TestSplitIssueBody_RoundTripsThroughMerge(t *testing.T) {
	doc := longDocument(150) // ~147 KB: body plus two continuations
	parts := SplitIssueBody(doc)
	if len(parts) < 3 {
		t.Fatalf("expected at least three parts for a %d-byte document, got %d", len(doc), len(parts))
	}
	for i, p := range parts {
		if len(p) > MaxIssueBodyLen {
			t.Errorf("part %d is %d bytes, over the %d cap", i+1, len(p), MaxIssueBodyLen)
		}
	}

	body := parts[0]
	if !IsContinued(body) {
		t.Fatal("the body of a split document must carry the continued marker")
	}
	if !strings.HasSuffix(strings.TrimSpace(body), testFooter) {
		t.Errorf("the footer must stay the last line of the body; body ends with:\n%s", body[len(body)-200:])
	}
	if !strings.Contains(body, fmt.Sprintf(continuedMarkerFmt, 1, len(parts))) {
		t.Errorf("body lacks the continued 1/%d marker", len(parts))
	}
	if !strings.Contains(body, continuedPointerPrefix) {
		t.Error("body lacks the visible pointer line")
	}
	for i, p := range parts[1:] {
		want := fmt.Sprintf(continuationMarkerFmt, i+2, len(parts))
		if !strings.HasPrefix(p, want) {
			t.Errorf("continuation %d must open with %q; got %q", i+2, want, firstLine(p))
		}
		if !strings.Contains(p, continuationNotePrefix) {
			t.Errorf("continuation %d lacks the visible note line", i+2)
		}
	}

	var comments []IssueComment
	for _, p := range parts[1:] {
		comments = append(comments, IssueComment{ID: "id", Body: p})
	}
	merged, err := MergeContinuations(body, comments)
	if err != nil {
		t.Fatalf("MergeContinuations: %v", err)
	}
	if merged != doc {
		t.Errorf("merge did not reproduce the document\n--- want tail ---\n%s\n--- got tail ---\n%s", doc[len(doc)-300:], merged[len(merged)-300:])
	}
}

func TestSplitIssueBody_CutsOutsideFencesAndOnBoundaries(t *testing.T) {
	doc := longDocument(150)
	for i, p := range SplitIssueBody(doc) {
		if strings.Count(p, "```")%2 != 0 {
			t.Errorf("part %d has an odd number of fence markers: a cut landed inside a code block", i+1)
		}
		if i > 0 {
			payload := p[strings.Index(p, "\n\n")+2:]
			if strings.HasPrefix(payload, "x") {
				t.Errorf("part %d starts mid-paragraph: %q", i+1, firstLine(payload))
			}
		}
	}
}

func TestSplitIssueBody_PrefersSectionBoundaries(t *testing.T) {
	// A document whose sections are each well under the cap must be cut
	// between sections, never inside one.
	var sb strings.Builder
	sb.WriteString("**Category**: feature | **Scope**: Large\n\n")
	sections := []string{"Description", "Motivation", "Affected Areas", "Acceptance Criteria", "Non-Goals", "References"}
	for _, s := range sections {
		fmt.Fprintf(&sb, "## %s\n\n%s\n\n", s, strings.Repeat("y", 20000))
	}
	sb.WriteString(testFooter + "\n")
	parts := SplitIssueBody(sb.String())
	if len(parts) < 2 {
		t.Fatal("expected a split")
	}
	for i, p := range parts[1:] {
		payload := strings.TrimSpace(p[strings.Index(p, "\n\n")+2:])
		if !strings.HasPrefix(payload, "## ") {
			t.Errorf("continuation %d should start on a section heading; starts with %q", i+2, firstLine(payload))
		}
	}
	if !strings.Contains(parts[0], "part 2 of") || !strings.Contains(parts[0], "References") {
		t.Errorf("the pointer should name the parts and the sections they carry; got %q", pointerLine(parts[0]))
	}
}

func TestMergeContinuations_NoMarkerIsIdentity(t *testing.T) {
	body := "## Description\n\nPlain.\n"
	got, err := MergeContinuations(body, []IssueComment{{Body: fmt.Sprintf(continuationMarkerFmt, 2, 2) + "\n\nstale"}})
	if err != nil || got != body {
		t.Fatalf("a body without the marker must come back unchanged (err=%v)", err)
	}
}

func TestMergeContinuations_MissingPartIsAnError(t *testing.T) {
	parts := SplitIssueBody(longDocument(150))
	comments := []IssueComment{{Body: parts[1]}} // part 3 absent
	_, err := MergeContinuations(parts[0], comments)
	if err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("expected a missing-part error, got %v", err)
	}
}

func TestMergeContinuations_IgnoresStaleTotalsAndTakesTheLatestDuplicate(t *testing.T) {
	body := "## Description\n\nIntro.\n\n" + fmt.Sprintf(continuedMarkerFmt, 1, 2) + "\n" + continuedPointerPrefix + "a comment below (part 2 of 2)._\n\n" + testFooter + "\n"
	comments := []IssueComment{
		{ID: "stale", Body: fmt.Sprintf(continuationMarkerFmt, 2, 3) + "\n\n## Stale\n"},
		{ID: "old", Body: fmt.Sprintf(continuationMarkerFmt, 2, 2) + "\n" + continuationNotePrefix + "(part 2 of 2)._\n\n## Old\n"},
		{ID: "new", Body: fmt.Sprintf(continuationMarkerFmt, 2, 2) + "\n" + continuationNotePrefix + "(part 2 of 2)._\n\n## New\n"},
	}
	got, err := MergeContinuations(body, comments)
	if err != nil {
		t.Fatalf("MergeContinuations: %v", err)
	}
	want := "## Description\n\nIntro.\n\n## New\n\n" + testFooter + "\n"
	if got != want {
		t.Errorf("merged =\n%s\nwant\n%s", got, want)
	}
}

// bodyWriter is the smallest IssueBodyWriter a PublishIssueBody test needs.
// githubtest.Fake cannot be used here: it imports this package.
type bodyWriter struct {
	comments []IssueComment
	body     string
	added    []string
	edited   map[string]string
	deleted  []string
	listErr  error
}

func (w *bodyWriter) ListIssueComments(string, string, int) ([]IssueComment, error) {
	return w.comments, w.listErr
}
func (w *bodyWriter) EditIssueBody(_, _ string, _ int, body string) error { w.body = body; return nil }
func (w *bodyWriter) AddIssueComment(_, _ string, _ int, body string) (string, error) {
	w.added = append(w.added, body)
	return "url", nil
}
func (w *bodyWriter) EditIssueComment(id, body string) error {
	if w.edited == nil {
		w.edited = map[string]string{}
	}
	w.edited[id] = body
	return nil
}
func (w *bodyWriter) DeleteIssueComment(id string) error {
	w.deleted = append(w.deleted, id)
	return nil
}

func TestPublishIssueBody_SplitsAndReusesContinuations(t *testing.T) {
	doc := longDocument(150)
	want := SplitIssueBody(doc)
	if len(want) != 3 {
		t.Fatalf("test document should split into 3 parts, got %d", len(want))
	}
	w := &bodyWriter{comments: []IssueComment{
		{ID: "plan", Body: "## Implementation Plan (issue #1)\n\nSTATUS: PLAN_READY"},
		{ID: "c3", Body: fmt.Sprintf(continuationMarkerFmt, 3, 3) + "\n\nold three"},
		{ID: "c2", Body: fmt.Sprintf(continuationMarkerFmt, 2, 3) + "\n\nold two"},
		{ID: "c4", Body: fmt.Sprintf(continuationMarkerFmt, 4, 4) + "\n\nold four"},
	}}
	n, err := PublishIssueBody(w, "o", "r", 1, doc)
	if err != nil {
		t.Fatalf("PublishIssueBody: %v", err)
	}
	if n != 3 {
		t.Errorf("parts = %d, want 3", n)
	}
	if w.body != want[0] {
		t.Error("the body must be the first part")
	}
	if w.edited["c2"] != want[1] || w.edited["c3"] != want[2] {
		t.Errorf("existing continuation comments must be rewritten in part order; edited %v", keys(w.edited))
	}
	if len(w.added) != 0 {
		t.Errorf("no comment should be added while slots remain; added %d", len(w.added))
	}
	if len(w.deleted) != 1 || w.deleted[0] != "c4" {
		t.Errorf("the surplus continuation must be deleted; deleted %v", w.deleted)
	}
}

func TestPublishIssueBody_ShortBodyDeletesStaleContinuations(t *testing.T) {
	w := &bodyWriter{comments: []IssueComment{{ID: "c2", Body: fmt.Sprintf(continuationMarkerFmt, 2, 2) + "\n\nold"}}}
	n, err := PublishIssueBody(w, "o", "r", 1, "## Description\n\nShort.\n")
	if err != nil || n != 1 {
		t.Fatalf("PublishIssueBody: n=%d err=%v", n, err)
	}
	if w.body != "## Description\n\nShort.\n" || len(w.added) != 0 || len(w.edited) != 0 {
		t.Error("a body within the cap is written as is, with no continuation comments")
	}
	if len(w.deleted) != 1 || w.deleted[0] != "c2" {
		t.Errorf("a stale continuation must be deleted so no reader merges it; deleted %v", w.deleted)
	}
}

func TestPublishIssueBody_AddsCommentsBeyondExistingSlots(t *testing.T) {
	doc := longDocument(150)
	w := &bodyWriter{}
	if _, err := PublishIssueBody(w, "o", "r", 1, doc); err != nil {
		t.Fatalf("PublishIssueBody: %v", err)
	}
	if len(w.added) != 2 || len(w.edited) != 0 || len(w.deleted) != 0 {
		t.Errorf("with no existing continuations every further part is a new comment; added=%d edited=%d deleted=%d", len(w.added), len(w.edited), len(w.deleted))
	}
}

func TestPublishIssueBody_ListFailureWritesNothing(t *testing.T) {
	w := &bodyWriter{listErr: errors.New("offline")}
	if _, err := PublishIssueBody(w, "o", "r", 1, "short"); err == nil {
		t.Fatal("expected the listing error to be returned")
	}
	if w.body != "" {
		t.Error("the body must not be written when stale continuations cannot be checked")
	}
}

func TestCompleteIssueBody_MergesOnlyWhenContinued(t *testing.T) {
	parts := SplitIssueBody(longDocument(150))
	lister := &bodyWriter{comments: []IssueComment{{Body: parts[1]}, {Body: parts[2]}}}

	plain := &Issue{Number: 1, Body: "plain"}
	if err := CompleteIssueBody(lister, plain); err != nil || plain.Body != "plain" {
		t.Fatalf("a plain body must be left alone (err=%v)", err)
	}

	continued := &Issue{Number: 1, Body: parts[0]}
	if err := CompleteIssueBody(lister, continued); err != nil {
		t.Fatalf("CompleteIssueBody: %v", err)
	}
	if IsContinued(continued.Body) || !strings.Contains(continued.Body, "## Acceptance Criteria") {
		t.Error("the merged body must carry every section and no marker")
	}

	lister.listErr = errors.New("offline")
	failing := &Issue{Number: 1, Body: parts[0]}
	if err := CompleteIssueBody(lister, failing); err == nil || !strings.Contains(err.Error(), "continues in comments") {
		t.Fatalf("a continued body whose comments cannot be read must fail loudly, got %v", err)
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func pointerLine(body string) string {
	for _, l := range strings.Split(body, "\n") {
		if strings.HasPrefix(l, continuedPointerPrefix) {
			return l
		}
	}
	return ""
}

func keys(m map[string]string) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
