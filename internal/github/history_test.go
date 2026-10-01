package github

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// historyPage is a history response of three commits, newest first: one of a
// merged pull request, one of two merged pull requests and an unmerged one
// with a lower number, and one whose only pull request was never merged.
const historyPage = `{
  "data": { "repository": { "defaultBranchRef": { "target": { "history": {
    "pageInfo": { "hasNextPage": true, "endCursor": "abc 2" },
    "nodes": [
      { "oid": "c3", "committedDate": "2026-03-03T10:00:00Z",
        "associatedPullRequests": { "nodes": [ { "number": 9, "merged": true } ] } },
      { "oid": "c2", "committedDate": "2026-03-02T10:00:00Z",
        "associatedPullRequests": { "nodes": [
          { "number": 4, "merged": false }, { "number": 8, "merged": true }, { "number": 6, "merged": true } ] } },
      { "oid": "c1", "committedDate": "2026-03-01T10:00:00Z",
        "associatedPullRequests": { "nodes": [ { "number": 5, "merged": false } ] } }
    ]
  } } } } }
}`

func TestParseHistoryPage(t *testing.T) {
	commits, hasNext, cursor, err := parseHistoryPage([]byte(historyPage))
	if err != nil {
		t.Fatalf("parseHistoryPage: %v", err)
	}
	if !hasNext || cursor != "abc 2" {
		t.Errorf("pagination = %v, %q, want true, \"abc 2\"", hasNext, cursor)
	}
	want := []HistoryCommit{
		{SHA: "c3", CommittedAt: time.Date(2026, 3, 3, 10, 0, 0, 0, time.UTC), PRNumber: 9},
		{SHA: "c2", CommittedAt: time.Date(2026, 3, 2, 10, 0, 0, 0, time.UTC), PRNumber: 6},
		{SHA: "c1", CommittedAt: time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC), PRNumber: 0},
	}
	if !slices.Equal(commits, want) {
		t.Errorf("commits = %+v\nwant %+v", commits, want)
	}
}

// TestParseHistoryPage_NoDefaultBranch covers an empty repository: GitHub
// answers with a null defaultBranchRef, which is an empty history, not an
// error.
func TestParseHistoryPage_NoDefaultBranch(t *testing.T) {
	commits, hasNext, cursor, err := parseHistoryPage([]byte(`{"data":{"repository":{"defaultBranchRef":null}}}`))
	if err != nil {
		t.Fatalf("parseHistoryPage: %v", err)
	}
	if len(commits) != 0 || hasNext || cursor != "" {
		t.Errorf("got %v, %v, %q, want an empty last page", commits, hasNext, cursor)
	}
}

func TestParseMergedPRsPage(t *testing.T) {
	const page = `{
  "data": { "repository": { "pullRequests": {
    "pageInfo": { "hasNextPage": false, "endCursor": "end" },
    "nodes": [
      { "number": 7, "title": "Add a flag", "mergedAt": "2026-03-01T10:00:00Z", "author": { "__typename": "User" },
        "closingIssuesReferences": { "nodes": [
          { "number": 3, "repository": { "nameWithOwner": "Acme/Widgets" } },
          { "number": 4, "repository": { "nameWithOwner": "acme/other" } } ] } },
      { "number": 8, "title": "Update a dependency", "mergedAt": "2026-03-02T10:00:00Z", "author": { "__typename": "Bot" },
        "closingIssuesReferences": { "nodes": [] } },
      { "number": 9, "title": "From a deleted account", "mergedAt": "2026-03-03T10:00:00Z", "author": null,
        "closingIssuesReferences": { "nodes": [] } }
    ]
  } } }
}`
	prs, hasNext, cursor, err := parseMergedPRsPage([]byte(page), "acme/widgets")
	if err != nil {
		t.Fatalf("parseMergedPRsPage: %v", err)
	}
	if hasNext || cursor != "end" {
		t.Errorf("pagination = %v, %q, want false, \"end\"", hasNext, cursor)
	}
	if len(prs) != 3 {
		t.Fatalf("got %d pull requests, want 3", len(prs))
	}
	if prs[0].Number != 7 || prs[0].Title != "Add a flag" || !prs[0].MergedAt.Equal(time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)) {
		t.Errorf("first pull request = %+v", prs[0])
	}
	if prs[0].AuthorIsBot || !prs[1].AuthorIsBot || prs[2].AuthorIsBot {
		t.Errorf("AuthorIsBot = %v, %v, %v, want false, true, false", prs[0].AuthorIsBot, prs[1].AuthorIsBot, prs[2].AuthorIsBot)
	}
	// The repository name differs in case, which GitHub treats as the same
	// repository; the issue of acme/other is dropped.
	if !slices.Equal(prs[0].ClosesIssues, []int{3}) {
		t.Errorf("ClosesIssues = %v, want [3]", prs[0].ClosesIssues)
	}
}

func TestParseClosedIssuesPage(t *testing.T) {
	const page = `{
  "data": { "repository": { "issues": {
    "pageInfo": { "hasNextPage": true, "endCursor": "next" },
    "nodes": [
      { "number": 1, "title": "Closed by a commit", "closedAt": "2026-03-01T10:00:00Z",
        "closedByPullRequestsReferences": { "nodes": [] },
        "timelineItems": { "nodes": [ { "closer": { "__typename": "Commit", "oid": "abc123" } } ] } },
      { "number": 2, "title": "Closed by a pull request", "closedAt": "2026-03-02T10:00:00Z",
        "closedByPullRequestsReferences": { "nodes": [
          { "number": 10, "state": "MERGED", "repository": { "nameWithOwner": "acme/widgets" } },
          { "number": 11, "state": "CLOSED", "repository": { "nameWithOwner": "acme/widgets" } },
          { "number": 12, "state": "MERGED", "repository": { "nameWithOwner": "acme/other" } } ] },
        "timelineItems": { "nodes": [ { "closer": { "__typename": "PullRequest" } } ] } },
      { "number": 3, "title": "Closed by hand", "closedAt": "2026-03-03T10:00:00Z",
        "closedByPullRequestsReferences": { "nodes": [] },
        "timelineItems": { "nodes": [ { "closer": null } ] } }
    ]
  } } }
}`
	issues, hasNext, cursor, err := parseClosedIssuesPage([]byte(page), "acme/widgets")
	if err != nil {
		t.Fatalf("parseClosedIssuesPage: %v", err)
	}
	if !hasNext || cursor != "next" {
		t.Errorf("pagination = %v, %q, want true, \"next\"", hasNext, cursor)
	}
	if len(issues) != 3 {
		t.Fatalf("got %d issues, want 3", len(issues))
	}
	if issues[0].CloserSHA != "abc123" || issues[1].CloserSHA != "" || issues[2].CloserSHA != "" {
		t.Errorf("CloserSHA = %q, %q, %q, want abc123 for the commit closer only", issues[0].CloserSHA, issues[1].CloserSHA, issues[2].CloserSHA)
	}
	if !slices.Equal(issues[1].ClosedByPRs, []int{10}) {
		t.Errorf("ClosedByPRs = %v, want [10] (merged and of the same repository)", issues[1].ClosedByPRs)
	}
	if issues[2].Title != "Closed by hand" || !issues[2].ClosedAt.Equal(time.Date(2026, 3, 3, 10, 0, 0, 0, time.UTC)) {
		t.Errorf("third issue = %+v", issues[2])
	}
}

func TestParseListingPages_MalformedJSON(t *testing.T) {
	if _, _, _, err := parseHistoryPage([]byte("not json")); err == nil {
		t.Error("parseHistoryPage accepted malformed JSON")
	}
	if _, _, _, err := parseMergedPRsPage([]byte("{"), "o/r"); err == nil {
		t.Error("parseMergedPRsPage accepted malformed JSON")
	}
	if _, _, _, err := parseClosedIssuesPage([]byte(`{"data": []}`), "o/r"); err == nil {
		t.Error("parseClosedIssuesPage accepted a response of the wrong shape")
	}
}

// fakeGH puts a gh shell script first on PATH for the test.
func fakeGH(t *testing.T, script string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatalf("writing fake gh: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// TestDefaultBranchHistory_TwoPagesOldestFirst drives the cursor loop through a
// fake gh that answers the second page when it is handed a cursor. The result
// is reversed across both pages, and the string variables travel with -f.
func TestDefaultBranchHistory_TwoPagesOldestFirst(t *testing.T) {
	dir := t.TempDir()
	argvFile := filepath.Join(dir, "argv")
	page := func(oids [2]string, hasNext, cursor string) string {
		return `{"data":{"repository":{"defaultBranchRef":{"target":{"history":{"pageInfo":{"hasNextPage":` + hasNext + `,"endCursor":"` + cursor + `"},"nodes":[` +
			`{"oid":"` + oids[0] + `","committedDate":"2026-03-04T10:00:00Z","associatedPullRequests":{"nodes":[]}},` +
			`{"oid":"` + oids[1] + `","committedDate":"2026-03-03T10:00:00Z","associatedPullRequests":{"nodes":[]}}]}}}}}}`
	}
	fakeGH(t, `printf '%s\n' "$@" >> `+argvFile+`
echo ---- >> `+argvFile+`
for a in "$@"; do
  case "$a" in cursor=*) echo '`+page([2]string{"c2", "c1"}, "false", "")+`'; exit 0;; esac
done
echo '`+page([2]string{"c4", "c3"}, "true", "page-2")+`'
`)

	commits, err := client.DefaultBranchHistory("2048", "widgets")
	if err != nil {
		t.Fatalf("DefaultBranchHistory: %v", err)
	}
	var shas []string
	for _, c := range commits {
		shas = append(shas, c.SHA)
	}
	if !slices.Equal(shas, []string{"c1", "c2", "c3", "c4"}) {
		t.Errorf("commits = %v, want c1..c4, oldest first", shas)
	}

	raw, err := os.ReadFile(argvFile)
	if err != nil {
		t.Fatalf("reading recorded argv: %v", err)
	}
	calls := strings.Split(strings.TrimSuffix(string(raw), "----\n"), "----\n")
	if len(calls) != 2 {
		t.Fatalf("gh ran %d times, want 2 (one per page)", len(calls))
	}
	first := strings.Split(strings.TrimRight(calls[0], "\n"), "\n")
	second := strings.Split(strings.TrimRight(calls[1], "\n"), "\n")
	for _, want := range []string{"owner=2048", "name=widgets"} {
		i := slices.Index(first, want)
		if i <= 0 || first[i-1] != "-f" {
			t.Errorf("first call %q must pass %q with -f", first, want)
		}
	}
	if slices.ContainsFunc(first, func(a string) bool { return strings.HasPrefix(a, "cursor=") }) {
		t.Errorf("the first page must pass no cursor: %q", first)
	}
	if i := slices.Index(second, "cursor=page-2"); i <= 0 || second[i-1] != "-f" {
		t.Errorf("the second call %q must pass cursor=page-2 with -f", second)
	}
}

// TestListings_GhFailureNamesTheListing proves a failing gh call names the
// listing it belonged to and carries what gh wrote to stderr (a rate limit, a
// missing scope).
func TestListings_GhFailureNamesTheListing(t *testing.T) {
	fakeGH(t, "echo 'API rate limit exceeded' >&2\nexit 1\n")

	_, historyErr := client.DefaultBranchHistory("o", "r")
	_, prsErr := client.ListMergedPRs("o", "r")
	_, issuesErr := client.ListClosedIssues("o", "r")
	for _, tc := range []struct {
		listing string
		err     error
	}{
		{"default-branch history", historyErr},
		{"merged pull requests", prsErr},
		{"closed issues", issuesErr},
	} {
		if tc.err == nil {
			t.Errorf("%s: want an error from a failing gh", tc.listing)
			continue
		}
		for _, want := range []string{"gh api graphql (" + tc.listing + "): ", "API rate limit exceeded"} {
			if !strings.Contains(tc.err.Error(), want) {
				t.Errorf("%s: error %q lacks %q", tc.listing, tc.err, want)
			}
		}
	}
}

// TestListings_UndecodablePageNamesTheListing proves a page gh returned but
// that does not decode is reported as a parse error of that listing.
func TestListings_UndecodablePageNamesTheListing(t *testing.T) {
	fakeGH(t, "echo 'not json'\n")

	if _, err := client.DefaultBranchHistory("o", "r"); err == nil || !strings.HasPrefix(err.Error(), "parsing default-branch history: ") {
		t.Errorf("DefaultBranchHistory err = %v", err)
	}
	if _, err := client.ListMergedPRs("o", "r"); err == nil || !strings.HasPrefix(err.Error(), "parsing merged pull requests: ") {
		t.Errorf("ListMergedPRs err = %v", err)
	}
	if _, err := client.ListClosedIssues("o", "r"); err == nil || !strings.HasPrefix(err.Error(), "parsing closed issues: ") {
		t.Errorf("ListClosedIssues err = %v", err)
	}
}

// FuzzParseListingPages checks that no response makes a page parser panic.
func FuzzParseListingPages(f *testing.F) {
	f.Add(historyPage)
	f.Add(`{"data":{"repository":{"defaultBranchRef":null}}}`)
	f.Add(`{"data":{"repository":{"issues":{"nodes":[{"timelineItems":{"nodes":[{"closer":null}]}}]}}}}`)
	f.Add(`{"data":{"repository":{"pullRequests":{"nodes":[{"author":null}]}}}}`)
	f.Add("")
	f.Fuzz(func(_ *testing.T, raw string) {
		_, _, _, _ = parseHistoryPage([]byte(raw))
		_, _, _, _ = parseMergedPRsPage([]byte(raw), "o/r")
		_, _, _, _ = parseClosedIssuesPage([]byte(raw), "o/r")
	})
}

func TestCommitMessage(t *testing.T) {
	dir := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com",
			"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q")
	git("commit", "-q", "--allow-empty", "-m", "Pin the base image", "-m", "The tag moved twice last month.")
	sha := git("rev-parse", "HEAD")

	msg, err := client.CommitMessage(dir, sha)
	if err != nil {
		t.Fatalf("CommitMessage: %v", err)
	}
	if want := "Pin the base image\n\nThe tag moved twice last month."; msg != want {
		t.Errorf("message = %q, want %q", msg, want)
	}

	const unknown = "0123456789012345678901234567890123456789"
	if _, err := client.CommitMessage(dir, unknown); err == nil || !strings.Contains(err.Error(), "reading commit message of "+unknown) {
		t.Errorf("CommitMessage of an unknown SHA: err = %v, want it to name the SHA", err)
	}
}
