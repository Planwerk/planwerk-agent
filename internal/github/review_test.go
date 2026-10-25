package github

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// reviewThreadsPage is a representative GraphQL response carrying two threads
// (one unresolved with a two-comment chain from two reviews and a null
// resolver, one resolved by a named user whose comment names no review) and a
// next-page cursor, so parseReviewThreads is exercised on flattening and
// pagination.
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
              "resolvedBy": null,
              "comments": {
                "nodes": [
                  {
                    "author": { "login": "reviewer" },
                    "body": "Please rename this helper.",
                    "createdAt": "2026-06-01T10:00:00Z",
                    "path": "internal/foo/bar.go",
                    "line": 42,
                    "diffHunk": "@@ -40,3 +40,3 @@\n-old\n+new",
                    "pullRequestReview": { "id": "PRR_1" }
                  },
                  {
                    "author": { "login": "author" },
                    "body": "Good catch, will do.",
                    "createdAt": "2026-06-01T11:00:00Z",
                    "path": "internal/foo/bar.go",
                    "line": 42,
                    "diffHunk": "@@ -40,3 +40,3 @@\n-old\n+new",
                    "pullRequestReview": { "id": "PRR_2" }
                  }
                ]
              }
            },
            {
              "id": "RT_2",
              "isResolved": true,
              "isOutdated": true,
              "resolvedBy": { "login": "maintainer" },
              "comments": {
                "nodes": [
                  {
                    "author": { "login": "reviewer" },
                    "body": "Already handled.",
                    "createdAt": "2026-06-01T09:00:00Z",
                    "path": "internal/foo/baz.go",
                    "line": 7,
                    "diffHunk": "@@ -5,2 +5,2 @@",
                    "pullRequestReview": null
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

	// A null resolvedBy leaves ResolvedBy empty; a named resolver is kept.
	if first.ResolvedBy != "" || threads[1].ResolvedBy != "maintainer" {
		t.Errorf("ResolvedBy = [%q, %q], want [\"\", \"maintainer\"]", first.ResolvedBy, threads[1].ResolvedBy)
	}

	// Each comment keeps the review it was posted in; a null review leaves
	// ReviewID empty.
	if got := []string{first.Comments[0].ReviewID, first.Comments[1].ReviewID, threads[1].Comments[0].ReviewID}; got[0] != "PRR_1" || got[1] != "PRR_2" || got[2] != "" {
		t.Errorf("ReviewID = %q, want [PRR_1 PRR_2 \"\"]", got)
	}
}

// TestReviewThreadsQuery_SelectsResolverAndReview: the query selects the two
// fields a re-review's dismissals read (decision 132). parseReviewThreads
// leaves either empty when the query stops asking for it, which its fixture
// test cannot notice.
func TestReviewThreadsQuery_SelectsResolverAndReview(t *testing.T) {
	for _, sel := range []string{"resolvedBy { login }", "pullRequestReview { id }"} {
		if !strings.Contains(reviewThreadsQuery, sel) {
			t.Errorf("reviewThreadsQuery does not select %q", sel)
		}
	}
}

// TestParseReviewThreads_ReadsTheOutdatedGuardFields: a thread carries the
// fields --resolve-outdated's guards read (decision 133). A null
// originalStartLine, as GitHub returns for a single-line thread, is 0, and a
// totalCount above the comments returned survives as CommentCount, so a
// partial view is visible to the caller. The query must keep selecting them,
// which the fixture alone cannot notice.
func TestParseReviewThreads_ReadsTheOutdatedGuardFields(t *testing.T) {
	const page = `{"data":{"repository":{"pullRequest":{"reviewThreads":{
  "pageInfo":{"hasNextPage":false,"endCursor":null},
  "nodes":[{
    "id":"RT_9","isResolved":false,"isOutdated":true,"resolvedBy":null,
    "originalLine":42,"originalStartLine":null,
    "comments":{"totalCount":3,"nodes":[
      {"author":{"login":"github-actions","__typename":"Bot"},"body":"finding","createdAt":"2026-06-01T10:00:00Z","path":"a.go","line":null,"diffHunk":"@@","pullRequestReview":{"id":"PRR_1"}},
      {"author":{"login":"alice","__typename":"User"},"body":"reply","createdAt":"2026-06-01T11:00:00Z","path":"a.go","line":null,"diffHunk":"@@","pullRequestReview":{"id":"PRR_2"}}
    ]}
  }]
}}}}}`
	threads, _, _, err := parseReviewThreads([]byte(page))
	if err != nil {
		t.Fatalf("parseReviewThreads returned error: %v", err)
	}
	if len(threads) != 1 {
		t.Fatalf("got %d threads, want 1", len(threads))
	}
	got := threads[0]
	if got.OriginalLine != 42 || got.OriginalStartLine != 0 || got.CommentCount != 3 || len(got.Comments) != 2 {
		t.Errorf("thread = original %d-%d, %d of %d comments; want original 0-42, 2 of 3 comments",
			got.OriginalStartLine, got.OriginalLine, len(got.Comments), got.CommentCount)
	}
	if !got.Comments[0].AuthorIsBot || got.Comments[1].AuthorIsBot {
		t.Errorf("AuthorIsBot = [%v, %v], want [true, false] for a Bot and a User", got.Comments[0].AuthorIsBot, got.Comments[1].AuthorIsBot)
	}

	for _, sel := range []string{"originalLine", "originalStartLine", "totalCount", "author { login __typename }"} {
		if !strings.Contains(reviewThreadsQuery, sel) {
			t.Errorf("reviewThreadsQuery does not select %q", sel)
		}
	}
}

// TestViewerLogin reads the authenticated login through a fake gh, and names
// the call and gh's output when gh fails. Only GitHub's refusal of an app's
// token wraps ErrViewerRefused; a failure that says nothing about the token,
// such as a timeout, does not.
func TestViewerLogin(t *testing.T) {
	argvFile := filepath.Join(t.TempDir(), "argv")
	fakeGH(t, recordArgv(argvFile)+"echo octocat\n")
	login, err := client.ViewerLogin()
	if err != nil || login != "octocat" {
		t.Fatalf("ViewerLogin = %q, %v; want octocat", login, err)
	}
	argv := recordedCalls(t, argvFile)[0]
	want := []string{"api", "graphql", "-f", "query=query { viewer { login } }", "--jq", ".data.viewer.login"}
	if !slices.Equal(argv, want) {
		t.Errorf("argv = %q, want %q", argv, want)
	}

	for _, tc := range []struct {
		name, output string
		refused      bool
	}{
		{name: "refused to an app token", output: "GraphQL: Resource not accessible by integration (viewer)", refused: true},
		{name: "timed out", output: `Post "https://api.github.com/graphql": net/http: TLS handshake timeout`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fakeGH(t, fmt.Sprintf("echo '%s' >&2\nexit 1\n", tc.output))
			login, err := client.ViewerLogin()
			if err == nil || login != "" {
				t.Fatalf("a failing gh = %q, %v; want an error alone", login, err)
			}
			for _, want := range []string{"gh api graphql (viewer): ", tc.output} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q lacks %q", err, want)
				}
			}
			if got := errors.Is(err, ErrViewerRefused); got != tc.refused {
				t.Errorf("errors.Is(err, ErrViewerRefused) = %v, want %v", got, tc.refused)
			}
		})
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

// TestIsOwnThread: only a signature in the thread's first comment marks it as
// one of the tool's own; a reply that carries it does not.
func TestIsOwnThread(t *testing.T) {
	signed := "**C-1: SQL injection** | CRITICAL\n" + reviewSignature
	for _, tc := range []struct {
		name   string
		thread ReviewThread
		want   bool
	}{
		{name: "signature in the first comment", thread: ReviewThread{Comments: []ReviewThreadComment{{Body: signed}, {Body: "fixed"}}}, want: true},
		{name: "signature only in a reply", thread: ReviewThread{Comments: []ReviewThreadComment{{Body: "please rename"}, {Body: signed}}}},
		{name: "no comments", thread: ReviewThread{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsOwnThread(tc.thread); got != tc.want {
				t.Errorf("IsOwnThread = %v, want %v", got, tc.want)
			}
		})
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

// TestSignReviewComments_RoundTripsThroughFilter is the regression test for the
// gap that made `address` offer planwerk-agent's own findings back as human
// review threads: the signature used to go on the review body only, while
// FilterReviewThreads reads a thread's FIRST COMMENT. GitHub never hands the
// review body along with an inline comment, so the filter could never fire.
func TestSignReviewComments_RoundTripsThroughFilter(t *testing.T) {
	signed := signReviewComments([]ReviewComment{
		{Path: "main.go", Line: 42, Side: "RIGHT", Body: "**C-1: SQL injection** | CRITICAL"},
	})
	if len(signed) != 1 {
		t.Fatalf("signReviewComments returned %d comments, want 1", len(signed))
	}
	if !strings.Contains(signed[0].Body, reviewSignature) {
		t.Fatalf("signed body %q carries no inline signature", signed[0].Body)
	}
	if !strings.Contains(signed[0].Body, "SQL injection") {
		t.Errorf("signed body %q dropped the original text", signed[0].Body)
	}
	if signed[0].Path != "main.go" || signed[0].Line != 42 {
		t.Errorf("signing changed the anchor: %+v", signed[0])
	}

	// The thread GitHub builds from that comment must be filtered out again.
	threads := []ReviewThread{
		{ID: "human", Comments: []ReviewThreadComment{{Body: "please rename"}}},
		{ID: "tool", Comments: []ReviewThreadComment{{Body: signed[0].Body}}},
	}
	got := FilterReviewThreads(threads, false)
	if len(got) != 1 || got[0].ID != "human" {
		t.Errorf("filter = %+v, want only the human thread", got)
	}
}

// TestSignReviewComments_DoesNotMutateInput guards the copy semantics: the
// caller's slice must be usable unchanged after signing.
func TestSignReviewComments_DoesNotMutateInput(t *testing.T) {
	in := []ReviewComment{{Body: "original"}}
	_ = signReviewComments(in)
	if in[0].Body != "original" {
		t.Errorf("input body mutated to %q", in[0].Body)
	}
	if got := signReviewComments(nil); got != nil {
		t.Errorf("signReviewComments(nil) = %+v, want nil", got)
	}
}
