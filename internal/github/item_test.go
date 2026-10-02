package github

import (
	"bytes"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// captureLogs routes the default logger into a buffer for the test.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var logBuf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &logBuf
}

// recordedCalls returns the argv of every call a fake gh appended to argvFile
// as one argument per line, closed by a "----" line.
func recordedCalls(t *testing.T, argvFile string) [][]string {
	t.Helper()
	raw, err := os.ReadFile(argvFile)
	if err != nil {
		t.Fatalf("reading recorded argv: %v", err)
	}
	var calls [][]string
	for _, call := range strings.Split(strings.TrimSuffix(string(raw), "----\n"), "----\n") {
		calls = append(calls, strings.Split(strings.TrimRight(call, "\n"), "\n"))
	}
	return calls
}

// recordArgv is the head of a fake gh script that records its argv.
func recordArgv(argvFile string) string {
	return `printf '%s\n' "$@" >> ` + argvFile + "\necho ---- >> " + argvFile + "\n"
}

func TestListUpdatedItems_PassesTheListingAndSince(t *testing.T) {
	for _, tc := range []struct {
		name, since, wantEndpoint string
	}{
		{"with a since time", "2026-01-01T00:00:00Z", "repos/acme/widgets/issues?state=all&sort=updated&direction=asc&per_page=100&since=2026-01-01T00:00:00Z"},
		{"without one", "", "repos/acme/widgets/issues?state=all&sort=updated&direction=asc&per_page=100"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			argvFile := filepath.Join(t.TempDir(), "argv")
			fakeGH(t, recordArgv(argvFile)+`echo '{"number":7,"pull":false,"updated_at":"2026-03-01T10:00:00Z"}'
echo '{"number":9,"pull":true,"updated_at":"2026-03-02T10:00:00Z"}'
`)
			items, err := client.ListUpdatedItems("acme", "widgets", tc.since)
			if err != nil {
				t.Fatalf("ListUpdatedItems: %v", err)
			}
			want := []UpdatedItem{
				{Number: 7, UpdatedAt: "2026-03-01T10:00:00Z"},
				{Number: 9, UpdatedAt: "2026-03-02T10:00:00Z", IsPull: true},
			}
			if !slices.Equal(items, want) {
				t.Errorf("items = %+v\nwant %+v", items, want)
			}

			calls := recordedCalls(t, argvFile)
			if len(calls) != 1 {
				t.Fatalf("gh ran %d times, want 1", len(calls))
			}
			argv := calls[0]
			if !slices.Contains(argv, "--paginate") {
				t.Errorf("argv %q lacks --paginate", argv)
			}
			if !slices.Contains(argv, tc.wantEndpoint) {
				t.Errorf("argv %q lacks the endpoint %q", argv, tc.wantEndpoint)
			}
			if i := slices.Index(argv, "--jq"); i < 0 || i+1 >= len(argv) || argv[i+1] != updatedItemsJQ {
				t.Errorf("argv %q must pass the filter with --jq", argv)
			}
		})
	}
}

func TestListUpdatedItems_NoOutputIsAnEmptyListing(t *testing.T) {
	fakeGH(t, "exit 0\n")
	items, err := client.ListUpdatedItems("acme", "widgets", "")
	if err != nil || len(items) != 0 {
		t.Errorf("ListUpdatedItems = %+v, %v, want no item and no error", items, err)
	}
}

func TestListUpdatedItems_Errors(t *testing.T) {
	t.Run("a failing gh names the listing and carries stderr", func(t *testing.T) {
		fakeGH(t, "echo 'API rate limit exceeded' >&2\nexit 1\n")
		_, err := client.ListUpdatedItems("acme", "widgets", "")
		if err == nil {
			t.Fatal("want an error from a failing gh")
		}
		for _, want := range []string{"gh api (updated items of acme/widgets): ", "API rate limit exceeded"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q lacks %q", err, want)
			}
		}
	})
	t.Run("a line that is not JSON is a parse error", func(t *testing.T) {
		fakeGH(t, `echo '{"number":7,"pull":false,"updated_at":"2026-03-01T10:00:00Z"}'
echo 'not json'
`)
		_, err := client.ListUpdatedItems("acme", "widgets", "")
		if err == nil || !strings.HasPrefix(err.Error(), "parsing updated items of acme/widgets: ") {
			t.Errorf("err = %v, want a parse error of the listing", err)
		}
	})
}

// issueItemResponse is an item response of a closed issue: a Bot author, a
// commit closer, and three closing pull requests of which one counts.
const issueItemResponse = `{
  "data": { "repository": { "issueOrPullRequest": {
    "__typename": "Issue",
    "id": "I_7", "number": 7, "title": "Pin the base image", "body": "The tag moved.",
    "url": "https://github.com/acme/widgets/issues/7",
    "state": "CLOSED", "stateReason": "NOT_PLANNED",
    "createdAt": "2026-03-01T10:00:00Z", "updatedAt": "2026-03-04T10:00:00Z", "closedAt": "2026-03-03T10:00:00Z",
    "author": { "login": "renovate", "__typename": "Bot" }, "authorAssociation": "NONE",
    "labels": { "nodes": [ { "name": "bug" }, { "name": "docker" } ] },
    "closedByPullRequestsReferences": { "nodes": [
      { "number": 10, "state": "MERGED", "repository": { "nameWithOwner": "Acme/Widgets" } },
      { "number": 11, "state": "CLOSED", "repository": { "nameWithOwner": "acme/widgets" } },
      { "number": 12, "state": "MERGED", "repository": { "nameWithOwner": "acme/other" } } ] },
    "timelineItems": { "nodes": [ { "closer": { "__typename": "Commit", "oid": "abc123" } } ] },
    "comments": { "pageInfo": { "hasNextPage": false, "endCursor": "c-end" }, "nodes": [
      { "id": "IC_1", "url": "https://github.com/acme/widgets/issues/7#issuecomment-1",
        "author": { "login": "maintainer" }, "authorAssociation": "MEMBER",
        "createdAt": "2026-03-02T10:00:00Z", "updatedAt": "2026-03-02T10:05:00Z", "body": "Agreed." },
      { "id": "IC_2", "url": "https://github.com/acme/widgets/issues/7#issuecomment-2",
        "author": null, "authorAssociation": "NONE",
        "createdAt": "2026-03-02T11:00:00Z", "updatedAt": "2026-03-02T11:00:00Z", "body": "From a deleted account." } ] }
  } } }
}`

func TestParseItem_Issue(t *testing.T) {
	got, more, err := parseItem([]byte(issueItemResponse), "acme/widgets")
	if err != nil {
		t.Fatalf("parseItem: %v", err)
	}
	want := &Item{
		Kind: ItemKindIssue, ID: "I_7", Number: 7, Title: "Pin the base image", Body: "The tag moved.",
		URL:   "https://github.com/acme/widgets/issues/7",
		State: "closed", StateReason: "not_planned",
		Author: "renovate", AuthorIsBot: true, AuthorAssociation: "NONE",
		Labels:    []string{"bug", "docker"},
		CreatedAt: "2026-03-01T10:00:00Z", UpdatedAt: "2026-03-04T10:00:00Z", ClosedAt: "2026-03-03T10:00:00Z",
		// Only the merged pull request of the same repository counts; the
		// repository name differs in case, which GitHub treats as the same.
		ClosedByPRs: []int{10},
		CloserSHA:   "abc123",
		Comments: []ItemComment{
			{ID: "IC_1", URL: "https://github.com/acme/widgets/issues/7#issuecomment-1", Author: "maintainer", AuthorAssociation: "MEMBER",
				CreatedAt: "2026-03-02T10:00:00Z", UpdatedAt: "2026-03-02T10:05:00Z", Body: "Agreed."},
			{ID: "IC_2", URL: "https://github.com/acme/widgets/issues/7#issuecomment-2", AuthorAssociation: "NONE",
				CreatedAt: "2026-03-02T11:00:00Z", UpdatedAt: "2026-03-02T11:00:00Z", Body: "From a deleted account."},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("item = %+v\nwant %+v", got, want)
	}
	if len(more) != 0 {
		t.Errorf("more = %v, want no connection to page", more)
	}
}

// TestParseItem_NullAuthorAndTimestamps covers an open issue of a deleted
// account: the null author, close time, and close reason decode to "".
func TestParseItem_NullAuthorAndTimestamps(t *testing.T) {
	const resp = `{"data":{"repository":{"issueOrPullRequest":{
  "__typename":"Issue","id":"I_8","number":8,"title":"t","body":"","url":"u","state":"OPEN","stateReason":null,
  "createdAt":"2026-03-01T10:00:00Z","updatedAt":"2026-03-01T10:00:00Z","closedAt":null,
  "author":null,"authorAssociation":"NONE","labels":{"nodes":[]},
  "closedByPullRequestsReferences":{"nodes":[]},"timelineItems":{"nodes":[{"closer":null}]},
  "comments":{"pageInfo":{"hasNextPage":false,"endCursor":null},"nodes":[]}}}}}`
	got, _, err := parseItem([]byte(resp), "acme/widgets")
	if err != nil {
		t.Fatalf("parseItem: %v", err)
	}
	if got.State != "open" || got.StateReason != "" || got.Author != "" || got.AuthorIsBot || got.ClosedAt != "" || got.MergedAt != "" || got.CloserSHA != "" {
		t.Errorf("item = %+v, want an open issue with empty author, reason, close time, and closer", got)
	}
	if got.Labels != nil || got.ClosedByPRs != nil || got.Comments != nil {
		t.Errorf("lists = %v, %v, %v, want nil for every list without an entry", got.Labels, got.ClosedByPRs, got.Comments)
	}
}

// pullItemResponse is an item response of a merged pull request whose comments
// connection has a second page and whose one review thread has two comments.
const pullItemResponse = `{
  "data": { "repository": { "issueOrPullRequest": {
    "__typename": "PullRequest",
    "id": "PR_9", "number": 9, "title": "Add a flag", "body": "Closes #3",
    "url": "https://github.com/acme/widgets/pull/9", "state": "MERGED",
    "createdAt": "2026-03-01T10:00:00Z", "updatedAt": "2026-03-05T10:00:00Z",
    "closedAt": "2026-03-04T10:00:00Z", "mergedAt": "2026-03-04T10:00:00Z", "baseRefName": "main",
    "author": { "login": "contributor", "__typename": "User" }, "authorAssociation": "CONTRIBUTOR",
    "labels": { "nodes": [] },
    "closingIssuesReferences": { "nodes": [
      { "number": 3, "repository": { "nameWithOwner": "acme/widgets" } },
      { "number": 4, "repository": { "nameWithOwner": "acme/other" } } ] },
    "comments": { "pageInfo": { "hasNextPage": true, "endCursor": "comments-2" }, "nodes": [
      { "id": "IC_1", "url": "u1", "author": { "login": "passerby" }, "authorAssociation": "NONE",
        "createdAt": "2026-03-02T10:00:00Z", "updatedAt": "2026-03-02T10:00:00Z", "body": "Nice." } ] },
    "reviews": { "pageInfo": { "hasNextPage": false, "endCursor": "r" }, "nodes": [
      { "id": "PRR_1", "url": "u2", "author": { "login": "maintainer" }, "authorAssociation": "MEMBER",
        "state": "CHANGES_REQUESTED", "submittedAt": "2026-03-02T12:00:00Z", "updatedAt": "2026-03-02T12:30:00Z", "body": "Wrap the error." } ] },
    "commits": { "pageInfo": { "hasNextPage": false, "endCursor": "k" }, "nodes": [
      { "commit": { "oid": "abc123", "committedDate": "2026-03-01T09:00:00Z", "messageHeadline": "Add a flag", "messageBody": "The default stays off." } } ] },
    "reviewThreads": { "pageInfo": { "hasNextPage": false, "endCursor": "t" }, "nodes": [
      { "id": "PRRT_1", "isResolved": true, "isOutdated": false,
        "comments": { "pageInfo": { "hasNextPage": false }, "nodes": [
          { "id": "PRRC_1", "url": "u3", "author": { "login": "maintainer" }, "authorAssociation": "MEMBER",
            "createdAt": "2026-03-02T12:00:00Z", "updatedAt": "2026-03-02T12:00:00Z", "body": "Wrap it.",
            "path": "cmd/flag.go", "line": 12, "diffHunk": "@@ -1 +1 @@\n-old\n+new" },
          { "id": "PRRC_2", "url": "u4", "author": { "login": "contributor" }, "authorAssociation": "CONTRIBUTOR",
            "createdAt": "2026-03-02T13:00:00Z", "updatedAt": "2026-03-02T13:00:00Z", "body": "Done.",
            "path": "cmd/other.go", "line": null, "diffHunk": "@@ other" } ] } },
      { "id": "PRRT_2", "isResolved": false, "isOutdated": true, "comments": { "pageInfo": { "hasNextPage": false }, "nodes": [] } } ] }
  } } }
}`

func TestParseItem_PullRequest(t *testing.T) {
	got, more, err := parseItem([]byte(pullItemResponse), "acme/widgets")
	if err != nil {
		t.Fatalf("parseItem: %v", err)
	}
	if got.Kind != ItemKindPull || got.State != "merged" || got.StateReason != "" || got.MergedAt != "2026-03-04T10:00:00Z" || got.BaseBranch != "main" {
		t.Errorf("item = %+v, want a merged pull request against main", got)
	}
	if got.AuthorIsBot || got.Author != "contributor" || got.Labels != nil {
		t.Errorf("author = %q (bot %v), labels = %v", got.Author, got.AuthorIsBot, got.Labels)
	}
	if !slices.Equal(got.ClosesIssues, []int{3}) {
		t.Errorf("ClosesIssues = %v, want [3] (the issue of acme/other is dropped)", got.ClosesIssues)
	}
	wantReviews := []ItemReview{{ID: "PRR_1", URL: "u2", Author: "maintainer", AuthorAssociation: "MEMBER",
		State: "CHANGES_REQUESTED", SubmittedAt: "2026-03-02T12:00:00Z", UpdatedAt: "2026-03-02T12:30:00Z", Body: "Wrap the error."}}
	if !slices.Equal(got.Reviews, wantReviews) {
		t.Errorf("reviews = %+v\nwant %+v", got.Reviews, wantReviews)
	}
	wantCommits := []ItemCommit{{SHA: "abc123", CommittedAt: "2026-03-01T09:00:00Z", Headline: "Add a flag", Body: "The default stays off."}}
	if !slices.Equal(got.Commits, wantCommits) {
		t.Errorf("commits = %+v\nwant %+v", got.Commits, wantCommits)
	}
	// The thread takes its anchor from the first comment; a thread without a
	// comment keeps the zero anchor.
	wantThreads := []ItemThread{
		{ID: "PRRT_1", IsResolved: true, Path: "cmd/flag.go", Line: 12, DiffHunk: "@@ -1 +1 @@\n-old\n+new", Comments: []ItemComment{
			{ID: "PRRC_1", URL: "u3", Author: "maintainer", AuthorAssociation: "MEMBER", CreatedAt: "2026-03-02T12:00:00Z", UpdatedAt: "2026-03-02T12:00:00Z", Body: "Wrap it."},
			{ID: "PRRC_2", URL: "u4", Author: "contributor", AuthorAssociation: "CONTRIBUTOR", CreatedAt: "2026-03-02T13:00:00Z", UpdatedAt: "2026-03-02T13:00:00Z", Body: "Done."},
		}},
		{ID: "PRRT_2", IsOutdated: true},
	}
	if !reflect.DeepEqual(got.Threads, wantThreads) {
		t.Errorf("threads = %+v\nwant %+v", got.Threads, wantThreads)
	}
	if len(more) != 1 || more["comments"] != "comments-2" {
		t.Errorf("more = %v, want the comments cursor alone", more)
	}
}

func TestParseItem_NullItemAndMalformedJSON(t *testing.T) {
	it, more, err := parseItem([]byte(`{"data":{"repository":{"issueOrPullRequest":null}}}`), "o/r")
	if it != nil || more != nil || err != nil {
		t.Errorf("a null item = %+v, %v, %v, want nil, nil, nil", it, more, err)
	}
	if _, _, err := parseItem([]byte("not json"), "o/r"); err == nil {
		t.Error("parseItem accepted malformed JSON")
	}
	if _, err := parseItemPage([]byte(`{"data":{"repository":{"pullRequest":null}}}`), &Item{}, "o/r"); err == nil {
		t.Error("parseItemPage accepted a page without an item")
	}
}

// TestGetItem_FollowsAConnectionToItsEnd drives the paging through a fake gh
// that answers the second page of the comments when it is handed a cursor.
func TestGetItem_FollowsAConnectionToItsEnd(t *testing.T) {
	argvFile := filepath.Join(t.TempDir(), "argv")
	const secondPage = `{"data":{"repository":{"pullRequest":{"comments":{"pageInfo":{"hasNextPage":false,"endCursor":"end"},"nodes":[` +
		`{"id":"IC_2","url":"u5","author":{"login":"maintainer"},"authorAssociation":"MEMBER","createdAt":"2026-03-03T10:00:00Z","updatedAt":"2026-03-03T10:00:00Z","body":"Merging."}]}}}}}`
	fakeGH(t, recordArgv(argvFile)+`for a in "$@"; do
  case "$a" in cursor=*) cat <<'JSON'
`+secondPage+`
JSON
  exit 0;; esac
done
cat <<'JSON'
`+pullItemResponse+`
JSON
`)

	got, err := client.GetItem("acme", "widgets", 9)
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	var ids []string
	for _, c := range got.Comments {
		ids = append(ids, c.ID)
	}
	if !slices.Equal(ids, []string{"IC_1", "IC_2"}) {
		t.Errorf("comments = %v, want those of both pages in order", ids)
	}
	if len(got.Reviews) != 1 || len(got.Commits) != 1 || len(got.Threads) != 2 {
		t.Errorf("the second page must add to the comments only: %d reviews, %d commits, %d threads", len(got.Reviews), len(got.Commits), len(got.Threads))
	}

	calls := recordedCalls(t, argvFile)
	if len(calls) != 2 {
		t.Fatalf("gh ran %d times, want 2 (the item and one further page)", len(calls))
	}
	first, second := calls[0], calls[1]
	for _, want := range []string{"owner=acme", "name=widgets"} {
		if i := slices.Index(first, want); i <= 0 || first[i-1] != "-f" {
			t.Errorf("first call %q must pass %q with -f", first, want)
		}
	}
	if i := slices.Index(first, "number=9"); i <= 0 || first[i-1] != "-F" {
		t.Errorf("first call %q must pass number=9 with -F", first)
	}
	if slices.ContainsFunc(first, func(a string) bool { return strings.HasPrefix(a, "cursor=") }) {
		t.Errorf("the first call must pass no cursor: %q", first)
	}
	if i := slices.Index(second, "cursor=comments-2"); i <= 0 || second[i-1] != "-f" {
		t.Errorf("the second call %q must pass cursor=comments-2 with -f", second)
	}
	// The further page selects the comments alone, under pullRequest.
	query := strings.Join(second, "\n")
	if !strings.Contains(query, "pullRequest(number: $number)") || !strings.Contains(query, "comments(first: 100, after: $cursor)") || strings.Contains(query, "reviewThreads") {
		t.Errorf("the second call must page the comments of the pull request alone:\n%s", query)
	}
}

// laterPageScript is a fake gh script that answers first to the item query and
// runs onCursor, a line of shell, when it is handed a cursor.
func laterPageScript(first, onCursor string) string {
	return `for a in "$@"; do
  case "$a" in cursor=*) ` + onCursor + `;; esac
done
cat <<'JSON'
` + first + `
JSON
`
}

// TestGetItem_PagesTheCommentsOfAnIssue drives the paging of an issue: the
// further page is asked for under issue, and its comments follow the first
// page's.
func TestGetItem_PagesTheCommentsOfAnIssue(t *testing.T) {
	argvFile := filepath.Join(t.TempDir(), "argv")
	first := strings.Replace(issueItemResponse, `"hasNextPage": false, "endCursor": "c-end"`, `"hasNextPage": true, "endCursor": "comments-2"`, 1)
	if first == issueItemResponse {
		t.Fatal("issueItemResponse no longer holds the page info this test rewrites")
	}
	const secondPage = `{"data":{"repository":{"issue":{"comments":{"pageInfo":{"hasNextPage":false,"endCursor":"end"},"nodes":[` +
		`{"id":"IC_3","url":"u","author":{"login":"maintainer"},"authorAssociation":"MEMBER","createdAt":"2026-03-03T10:00:00Z","updatedAt":"2026-03-03T10:00:00Z","body":"Closing."}]}}}}}`
	fakeGH(t, recordArgv(argvFile)+laterPageScript(first, "echo '"+secondPage+"'; exit 0"))

	got, err := client.GetItem("acme", "widgets", 7)
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	var ids []string
	for _, c := range got.Comments {
		ids = append(ids, c.ID)
	}
	if !slices.Equal(ids, []string{"IC_1", "IC_2", "IC_3"}) {
		t.Errorf("comments = %v, want those of both pages in order", ids)
	}
	calls := recordedCalls(t, argvFile)
	if len(calls) != 2 {
		t.Fatalf("gh ran %d times, want 2 (the item and one further page)", len(calls))
	}
	second := strings.Join(calls[1], "\n")
	if !strings.Contains(second, "issue(number: $number)") || strings.Contains(second, "pullRequest(") {
		t.Errorf("the second call must page the comments under issue:\n%s", second)
	}
}

// TestGetItem_LongReviewThreadKeepsItsFirstPage covers a review thread GitHub
// cut at 100 comments: the comments it returned stay, and the cut is logged.
func TestGetItem_LongReviewThreadKeepsItsFirstPage(t *testing.T) {
	var nodes []string
	for i := range 100 {
		nodes = append(nodes, fmt.Sprintf(`{"id":"PRRC_%d","url":"u","author":{"login":"a"},"authorAssociation":"NONE","createdAt":"2026-03-02T12:00:00Z","updatedAt":"2026-03-02T12:00:00Z","body":"b","path":"a.go","line":3,"diffHunk":"@@"}`, i))
	}
	resp := `{"data":{"repository":{"issueOrPullRequest":{"__typename":"PullRequest","id":"PR_9","number":9,"state":"OPEN",` +
		`"reviewThreads":{"pageInfo":{"hasNextPage":false,"endCursor":"t"},"nodes":[{"id":"PRRT_long","isResolved":false,"isOutdated":false,` +
		`"comments":{"pageInfo":{"hasNextPage":true},"nodes":[` + strings.Join(nodes, ",") + `]}}]}}}}}`
	fakeGH(t, "cat <<'JSON'\n"+resp+"\nJSON\n")
	logs := captureLogs(t)

	got, err := client.GetItem("acme", "widgets", 9)
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	if len(got.Threads) != 1 || len(got.Threads[0].Comments) != 100 {
		t.Fatalf("threads = %d, want one thread with its 100 comments", len(got.Threads))
	}
	for _, want := range []string{"review thread holds more than 100 comments; mirroring the first 100", "PRRT_long", "acme/widgets#9"} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("log %q lacks %q", logs, want)
		}
	}
}

func TestGetItem_Errors(t *testing.T) {
	for _, tc := range []struct {
		name, script string
		want         []string
	}{
		{"a failing gh names the item and carries stderr", "echo 'Could not resolve to an issue or pull request' >&2\nexit 1\n",
			[]string{"gh api graphql (item acme/widgets#7): ", "Could not resolve to an issue or pull request"}},
		{"output that is not JSON is a parse error", "echo 'not json'\n", []string{"parsing item acme/widgets#7: "}},
		{"a null item is not found", `echo '{"data":{"repository":{"issueOrPullRequest":null}}}'` + "\n", []string{"item acme/widgets#7 not found"}},
		// pullItemResponse has a second page of comments.
		{"a failing gh on a later page names the item and carries stderr", laterPageScript(pullItemResponse, "echo boom >&2; exit 1"),
			[]string{"gh api graphql (item acme/widgets#7): ", "boom"}},
		{"a later page without an item is a parse error", laterPageScript(pullItemResponse, `echo '{"data":{"repository":{"pullRequest":null}}}'; exit 0`),
			[]string{"parsing item acme/widgets#7: the response holds no item"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fakeGH(t, tc.script)
			it, err := client.GetItem("acme", "widgets", 7)
			if err == nil {
				t.Fatalf("GetItem = %+v, want an error", it)
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q lacks %q", err, want)
				}
			}
		})
	}
}

// FuzzParseItem checks that no response makes the item parsers panic.
func FuzzParseItem(f *testing.F) {
	f.Add(issueItemResponse)
	f.Add(pullItemResponse)
	f.Add(`{"data":{"repository":{"issueOrPullRequest":null}}}`)
	f.Add(`{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[{"comments":{"nodes":[{"author":null,"line":null}]}}]}}}}}`)
	f.Add(`{"number":7,"pull":true,"updated_at":"2026-03-01T10:00:00Z"}`)
	f.Add("")
	f.Fuzz(func(_ *testing.T, raw string) {
		_, _, _ = parseItem([]byte(raw), "o/r")
		_, _ = parseItemPage([]byte(raw), &Item{}, "o/r")
		_, _ = parseUpdatedItems([]byte(raw))
	})
}
