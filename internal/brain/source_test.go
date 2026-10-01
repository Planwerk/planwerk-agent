package brain

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/planwerk/planwerk-agent/internal/github"
	"github.com/planwerk/planwerk-agent/internal/github/githubtest"
)

// fakeSource is a scripted Source that records which issues and pull requests
// were read.
type fakeSource struct {
	listing Listing
	listErr error

	issues   map[int]*github.IssueThread
	prs      map[int]*github.PRThread
	issueErr error
	prErr    error

	lists      int
	issueCalls []int
	prCalls    []int
}

func (s *fakeSource) List(_, _ string) (Listing, error) {
	s.lists++
	return s.listing, s.listErr
}

func (s *fakeSource) Issue(_, _ string, number int) (*github.IssueThread, error) {
	s.issueCalls = append(s.issueCalls, number)
	if s.issueErr != nil {
		return nil, s.issueErr
	}
	if iss, ok := s.issues[number]; ok {
		return iss, nil
	}
	return &github.IssueThread{Number: number, Title: fmt.Sprintf("Issue %d", number)}, nil
}

func (s *fakeSource) PullRequest(_, _ string, number int) (*github.PRThread, error) {
	s.prCalls = append(s.prCalls, number)
	if s.prErr != nil {
		return nil, s.prErr
	}
	if pr, ok := s.prs[number]; ok {
		return pr, nil
	}
	return &github.PRThread{Number: number, Title: fmt.Sprintf("Pull request %d", number)}, nil
}

// itemKinds returns "<kind> <ref>" per item.
func itemKinds(items []Item) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.Kind+" "+it.Ref)
	}
	return out
}

func TestUnitItems_IssueUnitOrder(t *testing.T) {
	src := &fakeSource{
		issues: map[int]*github.IssueThread{3: {
			Number: 3, Title: "Need a flag", Body: "Please add one.", Author: "reporter",
			Comments: []github.ThreadComment{{Author: "maintainer", CreatedAt: "2026-01-02T10:00:00Z", Body: "Agreed."}},
		}},
		prs: map[int]*github.PRThread{
			7: {
				Number: 7, Title: "Add the flag", Body: "Closes #3", Author: "contributor",
				Comments: []github.ThreadComment{{Author: "passerby", CreatedAt: "2026-01-03T10:00:00Z", Body: "Nice."}},
				Reviews: []github.PRReview{
					{Author: "maintainer", SubmittedAt: "2026-01-03T11:00:00Z", Body: ""},
					{Author: "maintainer", SubmittedAt: "2026-01-03T12:00:00Z", Body: "Wrap the error."},
				},
				ReviewThreads: []github.ReviewThread{
					{Path: "cmd/main.go", Line: 12, DiffHunk: "@@ -1 +1 @@", Comments: []github.ReviewThreadComment{
						{Author: "maintainer", CreatedAt: "2026-01-03T12:00:00Z", Body: "Use %w here."},
						{Author: "contributor", CreatedAt: "2026-01-03T13:00:00Z", Body: "Done."},
					}},
					{Path: "empty.go", Line: 1},
				},
				Commits: []github.PRCommit{{SHA: "0123456789abcdef0123", Headline: "Add the flag", Body: "Off by default."}},
			},
			9: {Number: 9, Title: "Follow up", Author: "contributor"},
		},
	}
	gh := &githubtest.Fake{CommitMessages: map[string]string{"fedcba9876543210fedc": "Close the issue\n\nFixes #3"}}
	u := Unit{Key: "issue-3", Kind: KindIssue, Issue: 3, PRs: []int{7, 9}, Commits: []string{"fedcba9876543210fedc"}}

	items, omitted, err := unitItems(src, gh, "/clone", "acme", "widgets", u)
	if err != nil {
		t.Fatalf("unitItems: %v", err)
	}
	if omitted != 0 {
		t.Errorf("omitted = %d, want 0", omitted)
	}
	want := []string{
		"issue #3",
		"issue-comment #3",
		"pull-request #7",
		"pr-comment #7",
		"review #7", // the review with an empty body yields no item
		"review-thread #7 cmd/main.go:12",
		"commit 0123456789ab",
		"pull-request #9",
		"commit fedcba987654",
	}
	if got := itemKinds(items); !slices.Equal(got, want) {
		t.Fatalf("items = %q\nwant    %q", got, want)
	}

	if got := items[0]; got.Author != "reporter" || got.Date != "" || got.Body != "Title: Need a flag\n\nPlease add one." {
		t.Errorf("issue item = %+v", got)
	}
	if got := items[1]; got.Author != "maintainer" || got.Date != "2026-01-02T10:00:00Z" || got.Body != "Agreed." {
		t.Errorf("issue comment item = %+v", got)
	}
	if got := items[4]; got.Author != "maintainer" || got.Date != "2026-01-03T12:00:00Z" || got.Body != "Wrap the error." {
		t.Errorf("review item = %+v", got)
	}
	wantThread := "cmd/main.go:12\n\nmaintainer (2026-01-03T12:00:00Z):\nUse %w here.\n\ncontributor (2026-01-03T13:00:00Z):\nDone."
	if got := items[5]; got.Body != wantThread || got.Author != "" || strings.Contains(got.Body, "@@") {
		t.Errorf("review thread item = %+v\nwant body %q", got, wantThread)
	}
	if got := items[6].Body; got != "Add the flag\n\nOff by default." {
		t.Errorf("pull request commit body = %q", got)
	}
	if got := items[7].Body; got != "Title: Follow up" {
		t.Errorf("a pull request without a body = %q, want its title line alone", got)
	}
	if got := items[8].Body; got != "Close the issue\n\nFixes #3" {
		t.Errorf("unit commit body = %q", got)
	}
	if calls := gh.Calls("CommitMessage"); len(calls) != 1 || calls[0].Args[0] != "/clone" {
		t.Errorf("CommitMessage calls = %+v, want one in the clone", calls)
	}
}

// TestUnitItems_EmptyIssueStillHasOneItem covers an issue without a body and
// without comments: the unit is not empty, its title is the item.
func TestUnitItems_EmptyIssueStillHasOneItem(t *testing.T) {
	src := &fakeSource{issues: map[int]*github.IssueThread{3: {Number: 3, Title: "Only a title"}}}
	items, _, err := unitItems(src, &githubtest.Fake{}, "/clone", "acme", "widgets", Unit{Key: "issue-3", Kind: KindIssue, Issue: 3})
	if err != nil {
		t.Fatalf("unitItems: %v", err)
	}
	if len(items) != 1 || items[0].Body != "Title: Only a title" {
		t.Errorf("items = %+v, want the issue alone", items)
	}
}

func TestUnitItems_DocumentUnit(t *testing.T) {
	chunk := DocChunk{Path: "docs/adr/0001-x.md", Index: 1, Count: 2, Text: "# Decision\n"}
	u := Unit{Key: chunk.key(), Kind: KindDoc, Title: "docs/adr/0001-x.md (chunk 1 of 2)", Doc: &chunk}
	src := &fakeSource{}
	items, _, err := unitItems(src, &githubtest.Fake{}, "/clone", "acme", "widgets", u)
	if err != nil {
		t.Fatalf("unitItems: %v", err)
	}
	if len(items) != 1 || items[0].Kind != ItemDecisionDoc || items[0].Ref != u.Title || items[0].Body != "# Decision\n" {
		t.Errorf("items = %+v", items)
	}
	if len(src.issueCalls) != 0 || len(src.prCalls) != 0 {
		t.Error("a document unit must read nothing from the source")
	}
}

func TestUnitItems_RedactsSecretsAndWarnsOnce(t *testing.T) {
	src := &fakeSource{issues: map[int]*github.IssueThread{3: {
		Number: 3, Title: "Deploy fails", Body: "credentials: AKIAIOSFODNN7EXAMPLE used for S3",
		Comments: []github.ThreadComment{{Author: "a", Body: "also AKIAIOSFODNN7EXAMPLE here"}},
	}}}
	logs := captureLogs(t)

	items, _, err := unitItems(src, &githubtest.Fake{}, "/clone", "acme", "widgets", Unit{Key: "issue-3", Kind: KindIssue, Issue: 3})
	if err != nil {
		t.Fatalf("unitItems: %v", err)
	}
	for _, it := range items {
		if strings.Contains(it.Body, "AKIAIOSFODNN7EXAMPLE") || !strings.Contains(it.Body, "[REDACTED:aws-access-key-id]") {
			t.Errorf("%s body not redacted: %q", it.Kind, it.Body)
		}
	}
	if n := strings.Count(logs.String(), "level=WARN"); n != 1 {
		t.Errorf("got %d warnings, want 1:\n%s", n, logs.String())
	}
	for _, want := range []string{"unit=issue-3", "patterns=aws-access-key-id"} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("warning lacks %q:\n%s", want, logs.String())
		}
	}
}

func TestUnitItems_TruncatesALongBody(t *testing.T) {
	// A multi-byte rune straddles the cap, so a cut at the byte would split it.
	body := strings.Repeat("x", maxItemBytes-1) + "é" + strings.Repeat("y", 40<<10-maxItemBytes-1)
	src := &fakeSource{issues: map[int]*github.IssueThread{3: {Number: 3, Comments: []github.ThreadComment{{Body: body}}}}}

	items, _, err := unitItems(src, &githubtest.Fake{}, "/clone", "acme", "widgets", Unit{Key: "issue-3", Kind: KindIssue, Issue: 3})
	if err != nil {
		t.Fatalf("unitItems: %v", err)
	}
	got := items[1].Body
	kept, note, found := strings.Cut(got, "\n[truncated: ")
	if !found {
		t.Fatalf("body of %d bytes carries no truncation line", len(got))
	}
	if kept != strings.Repeat("x", maxItemBytes-1) {
		t.Errorf("kept %d bytes, want the %d bytes before the rune that straddles the cap", len(kept), maxItemBytes-1)
	}
	if want := fmt.Sprintf("%d bytes omitted]", len(body)-len(kept)); note != want {
		t.Errorf("truncation line ends %q, want %q", note, want)
	}
}

func TestUnitItems_OmitsItemsPastTheUnitCap(t *testing.T) {
	comment := strings.Repeat("x", maxItemBytes)
	iss := &github.IssueThread{Number: 3, Title: "Long thread"}
	for range 20 {
		iss.Comments = append(iss.Comments, github.ThreadComment{Author: "a", Body: comment})
	}
	// A short last comment would fit the budget, and is dropped all the same:
	// the content ends where the first item no longer fits.
	iss.Comments = append(iss.Comments, github.ThreadComment{Author: "a", Body: "short"})
	src := &fakeSource{issues: map[int]*github.IssueThread{3: iss}}
	logs := captureLogs(t)

	items, omitted, err := unitItems(src, &githubtest.Fake{}, "/clone", "acme", "widgets", Unit{Key: "issue-3", Kind: KindIssue, Issue: 3})
	if err != nil {
		t.Fatalf("unitItems: %v", err)
	}
	const total = 22 // the issue and 21 comments
	if omitted == 0 || len(items)+omitted != total {
		t.Fatalf("%d items and %d omitted, want them to add up to %d with some omitted", len(items), omitted, total)
	}
	size := 0
	for _, it := range items {
		size += len(it.Body)
	}
	if size > maxUnitBytes {
		t.Errorf("kept %d bytes, above the cap of %d", size, maxUnitBytes)
	}
	if items[len(items)-1].Body == "short" {
		t.Error("an item after the first omitted one was kept")
	}
	if !strings.Contains(logs.String(), "unit=issue-3") || !strings.Contains(logs.String(), fmt.Sprintf("omitted=%d", omitted)) {
		t.Errorf("the omission was not logged with the unit key:\n%s", logs.String())
	}
}

func TestUnitItems_ReaderErrors(t *testing.T) {
	boom := errors.New("boom")
	for _, tc := range []struct {
		name string
		src  *fakeSource
		gh   *githubtest.Fake
	}{
		{"issue", &fakeSource{issueErr: boom}, &githubtest.Fake{}},
		{"pull request", &fakeSource{prErr: boom}, &githubtest.Fake{}},
		{"commit message", &fakeSource{}, &githubtest.Fake{CommitMessageErr: boom}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u := Unit{Key: "issue-3", Kind: KindIssue, Issue: 3, PRs: []int{7}, Commits: []string{"abc"}}
			items, _, err := unitItems(tc.src, tc.gh, "/clone", "acme", "widgets", u)
			if !errors.Is(err, boom) || items != nil {
				t.Errorf("unitItems = %v, %v, want the reader's error and no item", items, err)
			}
		})
	}
}

func TestWorkingSetIndex(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"memory/b.md":                    "# Conventions\n\nNo summary here.\n",
		"memory/a.md":                    "# Pin   every\tdependency\n\n**Summary**: Dependencies are\u00a0 pinned.\n\nBecause.\n",
		"review_patterns/wrap-errors.md": "# Review Pattern: Wrap errors\n\n**Summary**: not shown for a pattern\n",
		"memory/notes.txt":               "not a page",
	})
	want := "- memory/a.md: Pin every dependency | Dependencies are pinned.\n" +
		"- memory/b.md: Conventions\n" +
		"- review_patterns/wrap-errors.md: Review Pattern: Wrap errors\n"
	if got := workingSetIndex(dir); got != want {
		t.Errorf("index =\n%s\nwant\n%s", got, want)
	}
}

func TestWorkingSetIndex_Empty(t *testing.T) {
	if got := workingSetIndex(filepath.Join(t.TempDir(), "absent")); got != "" {
		t.Errorf("index = %q, want nothing for a working set without pages", got)
	}
}

func TestAPISource(t *testing.T) {
	gh := &githubtest.Fake{
		History:      []github.HistoryCommit{{SHA: "c1"}},
		MergedPRs:    []github.MergedPR{{Number: 7}},
		ClosedIssues: []github.ClosedIssue{{Number: 3}},
		IssueThread:  &github.IssueThread{Title: "issue"},
		PRThread:     &github.PRThread{Title: "pull request"},
	}
	src := APISource{GitHub: gh}

	l, err := src.List("acme", "widgets")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(l.History) != 1 || len(l.PRs) != 1 || len(l.Issues) != 1 {
		t.Errorf("listing = %+v", l)
	}
	if iss, err := src.Issue("acme", "widgets", 3); err != nil || iss.Number != 3 || iss.Title != "issue" {
		t.Errorf("Issue = %+v, %v", iss, err)
	}
	if pr, err := src.PullRequest("acme", "widgets", 7); err != nil || pr.Number != 7 || pr.Title != "pull request" {
		t.Errorf("PullRequest = %+v, %v", pr, err)
	}
}

// TestAPISource_ListingErrorPassesThrough proves a failing listing is
// returned as it is and ends the listing: the caller names the repository.
func TestAPISource_ListingErrorPassesThrough(t *testing.T) {
	boom := errors.New("gh api graphql (merged pull requests): exit status 1: rate limit")
	gh := &githubtest.Fake{MergedPRsErr: boom}

	_, err := APISource{GitHub: gh}.List("acme", "widgets")
	if !errors.Is(err, boom) || err.Error() != boom.Error() {
		t.Errorf("List err = %v, want the listing's error without another wrap", err)
	}
	if gh.Count("DefaultBranchHistory") != 1 || gh.Count("ListClosedIssues") != 0 {
		t.Errorf("history ran %d times and the issue listing %d, want 1 and 0", gh.Count("DefaultBranchHistory"), gh.Count("ListClosedIssues"))
	}
}
