package github

import (
	"slices"
	"strings"
	"testing"
)

func TestParseIssueThread(t *testing.T) {
	const out = `{
  "number": 42, "title": "Pin the base image", "body": "The tag moved.",
  "author": { "login": "maintainer", "is_bot": false },
  "comments": [
    { "author": { "login": "maintainer" }, "authorAssociation": "MEMBER", "createdAt": "2026-02-27T09:00:00Z", "body": "Agreed." },
    { "author": { "login": "passerby" }, "authorAssociation": "NONE", "createdAt": "2026-02-28T09:00:00Z", "body": "Same here." }
  ]
}`
	got, err := parseIssueThread([]byte(out))
	if err != nil {
		t.Fatalf("parseIssueThread: %v", err)
	}
	if got.Number != 42 || got.Title != "Pin the base image" || got.Body != "The tag moved." || got.Author != "maintainer" {
		t.Errorf("issue = %+v", got)
	}
	// The comment of an author outside the repository stays: the history is
	// analyzed, never acted on.
	wantComments := []ThreadComment{
		{Author: "maintainer", AuthorAssociation: "MEMBER", CreatedAt: "2026-02-27T09:00:00Z", Body: "Agreed."},
		{Author: "passerby", AuthorAssociation: "NONE", CreatedAt: "2026-02-28T09:00:00Z", Body: "Same here."},
	}
	if !slices.Equal(got.Comments, wantComments) {
		t.Errorf("comments = %+v\nwant %+v", got.Comments, wantComments)
	}
}

func TestParseIssueThread_NoComments(t *testing.T) {
	got, err := parseIssueThread([]byte(`{"number": 7, "title": "t", "author": null, "comments": []}`))
	if err != nil {
		t.Fatalf("parseIssueThread: %v", err)
	}
	if got.Number != 7 || len(got.Comments) != 0 || got.Author != "" {
		t.Errorf("issue = %+v, want number 7 and no comment or author", got)
	}
}

func TestParsePRThread(t *testing.T) {
	const out = `{
  "number": 9, "title": "Add a flag", "body": "Closes #3",
  "author": { "login": "contributor" },
  "comments": [ { "author": { "login": "passerby" }, "authorAssociation": "NONE", "createdAt": "2026-03-01T09:00:00Z", "body": "Nice." } ],
  "reviews": [ { "author": { "login": "maintainer" }, "submittedAt": "2026-03-01T12:00:00Z", "body": "Wrap the error." } ],
  "commits": [ { "oid": "abc123", "messageHeadline": "Add a flag", "messageBody": "The default stays off." } ]
}`
	got, err := parsePRThread([]byte(out))
	if err != nil {
		t.Fatalf("parsePRThread: %v", err)
	}
	if got.Number != 9 || got.Title != "Add a flag" || got.Body != "Closes #3" || got.Author != "contributor" {
		t.Errorf("pull request = %+v", got)
	}
	if want := []ThreadComment{{Author: "passerby", AuthorAssociation: "NONE", CreatedAt: "2026-03-01T09:00:00Z", Body: "Nice."}}; !slices.Equal(got.Comments, want) {
		t.Errorf("comments = %+v, want %+v", got.Comments, want)
	}
	if want := []PRReview{{Author: "maintainer", SubmittedAt: "2026-03-01T12:00:00Z", Body: "Wrap the error."}}; !slices.Equal(got.Reviews, want) {
		t.Errorf("reviews = %+v, want %+v", got.Reviews, want)
	}
	if want := []PRCommit{{SHA: "abc123", Headline: "Add a flag", Body: "The default stays off."}}; !slices.Equal(got.Commits, want) {
		t.Errorf("commits = %+v, want %+v", got.Commits, want)
	}
}

func TestParsePRThread_NoReviewsOrComments(t *testing.T) {
	got, err := parsePRThread([]byte(`{"number": 9, "comments": [], "reviews": [], "commits": []}`))
	if err != nil {
		t.Fatalf("parsePRThread: %v", err)
	}
	if len(got.Comments) != 0 || len(got.Reviews) != 0 || len(got.Commits) != 0 {
		t.Errorf("pull request = %+v, want empty comments, reviews, and commits", got)
	}
}

func TestParseThreads_MalformedJSON(t *testing.T) {
	if _, err := parseIssueThread([]byte("not json")); err == nil || !strings.HasPrefix(err.Error(), "parsing gh issue view output: ") {
		t.Errorf("parseIssueThread err = %v", err)
	}
	if _, err := parsePRThread([]byte(`{"number": "nine"}`)); err == nil || !strings.HasPrefix(err.Error(), "parsing gh pr view output: ") {
		t.Errorf("parsePRThread err = %v", err)
	}
}

// TestGetThreads_GhFailureCarriesItsOutput proves a failing gh view call is
// reported with what gh printed.
func TestGetThreads_GhFailureCarriesItsOutput(t *testing.T) {
	fakeGH(t, "echo 'could not resolve to an Issue' >&2\nexit 1\n")

	if _, err := client.GetIssueThread("o", "r", 1); err == nil || !strings.HasPrefix(err.Error(), "gh issue view: could not resolve to an Issue: ") {
		t.Errorf("GetIssueThread err = %v", err)
	}
	if _, err := client.GetPRThread("o", "r", 1); err == nil || !strings.HasPrefix(err.Error(), "gh pr view: could not resolve to an Issue: ") {
		t.Errorf("GetPRThread err = %v", err)
	}
}

// TestGetPRThread_ReturnsThreadErrorUnchanged lets gh answer the view call and
// fail the review-threads query: the error GetPRThread returns is the one
// FetchReviewThreads produces, without another wrap.
func TestGetPRThread_ReturnsThreadErrorUnchanged(t *testing.T) {
	fakeGH(t, `if [ "$1" = pr ]; then echo '{"number": 9, "title": "t"}'; exit 0; fi
echo 'secondary rate limit' >&2
exit 1
`)

	_, wantErr := client.FetchReviewThreads("o", "r", 9)
	if wantErr == nil {
		t.Fatal("the fake gh must fail the review-threads query")
	}
	pr, err := client.GetPRThread("o", "r", 9)
	if err == nil || err.Error() != wantErr.Error() {
		t.Errorf("GetPRThread err = %v, want %v", err, wantErr)
	}
	if pr != nil {
		t.Errorf("GetPRThread returned %+v beside the error", pr)
	}
}

// TestGetPRThread_AttachesReviewThreads runs both calls against a fake gh and
// checks the review threads land on the pull request.
func TestGetPRThread_AttachesReviewThreads(t *testing.T) {
	fakeGH(t, `if [ "$1" = pr ]; then echo '{"number": 9, "title": "t"}'; exit 0; fi
cat <<'JSON'
`+reviewThreadsPageLast+`
JSON
`)

	pr, err := client.GetPRThread("o", "r", 9)
	if err != nil {
		t.Fatalf("GetPRThread: %v", err)
	}
	if pr.Number != 9 || len(pr.ReviewThreads) != 1 || pr.ReviewThreads[0].Path != "a.go" {
		t.Errorf("pull request = %+v, want one review thread on a.go", pr)
	}
}

// reviewThreadsPageLast is a last page of review threads holding one thread.
const reviewThreadsPageLast = `{"data":{"repository":{"pullRequest":{"reviewThreads":{"pageInfo":{"hasNextPage":false,"endCursor":""},"nodes":[{"id":"RT_1","isResolved":true,"isOutdated":false,"comments":{"nodes":[{"author":{"login":"reviewer"},"body":"Rename this.","createdAt":"2026-03-01T10:00:00Z","path":"a.go","line":3,"diffHunk":"@@"}]}}]}}}}}`
