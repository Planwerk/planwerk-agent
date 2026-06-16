package github

import (
	"encoding/json"
	"testing"
)

// reviewThreadsPage is a representative GraphQL response carrying two threads
// (one unresolved with a two-comment chain, one resolved) and a next-page
// cursor, so parseReviewThreads is exercised on flattening and pagination.
const reviewThreadsPage = `{
  "data": {
    "repository": {
      "pullRequest": {
        "reviewThreads": {
          "pageInfo": { "hasNextPage": true, "endCursor": "Y3Vyc29yOjI=" },
          "nodes": [
            {
              "id": "RT_1",
              "isResolved": false,
              "isOutdated": false,
              "comments": {
                "nodes": [
                  {
                    "author": { "login": "reviewer" },
                    "body": "Please rename this helper.",
                    "createdAt": "2026-06-01T10:00:00Z",
                    "path": "internal/foo/bar.go",
                    "line": 42,
                    "diffHunk": "@@ -40,3 +40,3 @@\n-old\n+new"
                  },
                  {
                    "author": { "login": "author" },
                    "body": "Good catch, will do.",
                    "createdAt": "2026-06-01T11:00:00Z",
                    "path": "internal/foo/bar.go",
                    "line": 42,
                    "diffHunk": "@@ -40,3 +40,3 @@\n-old\n+new"
                  }
                ]
              }
            },
            {
              "id": "RT_2",
              "isResolved": true,
              "isOutdated": true,
              "comments": {
                "nodes": [
                  {
                    "author": { "login": "reviewer" },
                    "body": "Already handled.",
                    "createdAt": "2026-06-01T09:00:00Z",
                    "path": "internal/foo/baz.go",
                    "line": 7,
                    "diffHunk": "@@ -5,2 +5,2 @@"
                  }
                ]
              }
            }
          ]
        }
      }
    }
  }
}`

func TestParseReviewThreads(t *testing.T) {
	threads, hasNext, endCursor, err := parseReviewThreads([]byte(reviewThreadsPage))
	if err != nil {
		t.Fatalf("parseReviewThreads returned error: %v", err)
	}
	if !hasNext || endCursor != "Y3Vyc29yOjI=" {
		t.Errorf("pagination = (%v, %q), want (true, %q)", hasNext, endCursor, "Y3Vyc29yOjI=")
	}
	if len(threads) != 2 {
		t.Fatalf("got %d threads, want 2", len(threads))
	}

	first := threads[0]
	if first.ID != "RT_1" || first.IsResolved || first.IsOutdated {
		t.Errorf("first thread = %+v, want id RT_1, unresolved, not outdated", first)
	}
	// Path/Line/DiffHunk come from the first comment in the chain.
	if first.Path != "internal/foo/bar.go" || first.Line != 42 {
		t.Errorf("first thread anchor = %s:%d, want internal/foo/bar.go:42", first.Path, first.Line)
	}
	if first.DiffHunk == "" {
		t.Error("first thread should carry the diff hunk from its first comment")
	}
	if len(first.Comments) != 2 {
		t.Fatalf("first thread has %d comments, want 2", len(first.Comments))
	}
	if first.Comments[0].Author != "reviewer" || first.Comments[1].Author != "author" {
		t.Errorf("comment chain authors = [%s, %s], want [reviewer, author]",
			first.Comments[0].Author, first.Comments[1].Author)
	}

	if !threads[1].IsResolved || !threads[1].IsOutdated {
		t.Errorf("second thread should be resolved and outdated, got %+v", threads[1])
	}
}

// TestParseReviewThreads_EmptyPage covers a PR with no review threads: an empty
// nodes array yields no threads, no next page, and no error.
func TestParseReviewThreads_EmptyPage(t *testing.T) {
	const empty = `{"data":{"repository":{"pullRequest":{"reviewThreads":{"pageInfo":{"hasNextPage":false,"endCursor":null},"nodes":[]}}}}}`
	threads, hasNext, endCursor, err := parseReviewThreads([]byte(empty))
	if err != nil {
		t.Fatalf("parseReviewThreads returned error: %v", err)
	}
	if len(threads) != 0 {
		t.Errorf("got %d threads, want 0", len(threads))
	}
	if hasNext || endCursor != "" {
		t.Errorf("pagination = (%v, %q), want (false, \"\")", hasNext, endCursor)
	}
}

// TestParseReviewThreads_Malformed covers the upstream-error path: invalid JSON
// must surface a wrapped decode error, not a panic or a silent empty result.
func TestParseReviewThreads_Malformed(t *testing.T) {
	if _, _, _, err := parseReviewThreads([]byte("{not json")); err == nil {
		t.Fatal("parseReviewThreads should error on malformed JSON, got nil")
	}
}

func TestFilterReviewThreads_IncludeResolved(t *testing.T) {
	threads := []ReviewThread{
		{ID: "open", Comments: []ReviewThreadComment{{Body: "fix this"}}},
		{ID: "resolved", IsResolved: true, Comments: []ReviewThreadComment{{Body: "done"}}},
	}

	// By default resolved threads are dropped.
	got := FilterReviewThreads(threads, false)
	if len(got) != 1 || got[0].ID != "open" {
		t.Errorf("default filter = %+v, want only the open thread", got)
	}

	// With includeResolved, both survive.
	got = FilterReviewThreads(threads, true)
	if len(got) != 2 {
		t.Errorf("includeResolved filter kept %d threads, want 2", len(got))
	}
}

func TestFilterReviewThreads_SkipsInlineSignature(t *testing.T) {
	threads := []ReviewThread{
		{ID: "human", Comments: []ReviewThreadComment{{Body: "please rename"}}},
		{ID: "tool", Comments: []ReviewThreadComment{{Body: "**C-1: SQL injection** | CRITICAL\n" + reviewSignature}}},
	}
	got := FilterReviewThreads(threads, false)
	if len(got) != 1 || got[0].ID != "human" {
		t.Errorf("filter = %+v, want only the human thread (the tool's own inline comment is skipped)", got)
	}
}

// TestFilterReviewThreads_SkipsEmpty covers the edge case of a thread with no
// comments: there is nothing to address, so it is dropped.
func TestFilterReviewThreads_SkipsEmpty(t *testing.T) {
	threads := []ReviewThread{
		{ID: "empty"},
		{ID: "real", Comments: []ReviewThreadComment{{Body: "do x"}}},
	}
	got := FilterReviewThreads(threads, false)
	if len(got) != 1 || got[0].ID != "real" {
		t.Errorf("filter = %+v, want only the thread with comments", got)
	}
}

func TestReviewComment_JSON(t *testing.T) {
	c := ReviewComment{
		Path: "main.go",
		Line: 42,
		Side: "RIGHT",
		Body: "This is a problem",
	}

	data, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var decoded ReviewComment
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if decoded.Path != "main.go" {
		t.Errorf("Path = %q, want %q", decoded.Path, "main.go")
	}
	if decoded.Line != 42 {
		t.Errorf("Line = %d, want %d", decoded.Line, 42)
	}
	if decoded.Side != "RIGHT" {
		t.Errorf("Side = %q, want %q", decoded.Side, "RIGHT")
	}

	// StartLine/StartSide should be omitted
	if decoded.StartLine != 0 {
		t.Errorf("StartLine should be 0 when omitted, got %d", decoded.StartLine)
	}
}

func TestReviewComment_MultiLine_JSON(t *testing.T) {
	c := ReviewComment{
		Path:      "handler.go",
		Line:      50,
		Side:      "RIGHT",
		StartLine: 45,
		StartSide: "RIGHT",
		Body:      "Multi-line issue",
	}

	data, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var decoded map[string]interface{}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, ok := decoded["start_line"]; !ok {
		t.Error("multi-line comment should include start_line")
	}
	if _, ok := decoded["start_side"]; !ok {
		t.Error("multi-line comment should include start_side")
	}
}

func TestReviewRequest_JSON(t *testing.T) {
	req := ReviewRequest{
		Body:     "Review summary",
		Event:    "COMMENT",
		CommitID: "abc123",
		Comments: []ReviewComment{
			{Path: "a.go", Line: 10, Side: "RIGHT", Body: "Fix this"},
		},
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var decoded ReviewRequest
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if decoded.Event != "COMMENT" {
		t.Errorf("Event = %q, want %q", decoded.Event, "COMMENT")
	}
	if decoded.CommitID != "abc123" {
		t.Errorf("CommitID = %q, want %q", decoded.CommitID, "abc123")
	}
	if len(decoded.Comments) != 1 {
		t.Fatalf("expected 1 comment, got %d", len(decoded.Comments))
	}
	if decoded.Comments[0].Path != "a.go" {
		t.Errorf("Comment.Path = %q, want %q", decoded.Comments[0].Path, "a.go")
	}
}
