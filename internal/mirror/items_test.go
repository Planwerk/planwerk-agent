package mirror

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/planwerk/planwerk-agent/internal/github"
	"github.com/planwerk/planwerk-agent/internal/github/githubtest"
)

// fetchedNumbers returns the numbers GetItem was called with from call index
// from on.
func fetchedNumbers(gh *githubtest.Fake, from int) []int {
	var out []int
	for _, c := range gh.Calls("GetItem")[from:] {
		out = append(out, c.Args[2].(int))
	}
	return out
}

// TestItems_SecondRunFetchesNothing covers the run right after a sync: the
// listing starts at the cursor, returns the newest item again, and the item's
// file is up to date.
func TestItems_SecondRunFetchesNothing(t *testing.T) {
	root := t.TempDir()
	gh := testGitHub(threeItems()...)
	s := newSyncer(gh, &fakeWiki{head: testWikiHead})
	if _, err := runSync(t, s, Options{Root: root}); err != nil {
		t.Fatalf("first Run: %v", err)
	}
	fetched := gh.Count("GetItem")

	out, err := runSync(t, s, Options{Root: root})
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if got := gh.Count("GetItem"); got != fetched {
		t.Errorf("the second run fetched %d items, want none", got-fetched)
	}
	if !strings.Contains(out, "items: 1 listed, 0 fetched, 3 in the mirror (2 issues, 1 pull requests)\n") {
		t.Errorf("output = %q, want one listed item and none fetched", out)
	}
	if !strings.Contains(out, "history: 0 new commits, 3 in the mirror\n") || !strings.Contains(out, "wiki: acme/widgets.wiki at 1a2b3c4, unchanged\n") {
		t.Errorf("output = %q, want an unchanged history and wiki", out)
	}
	// The second listing starts at the cursor the first run stored.
	if since := gh.Calls("ListUpdatedItems")[1].Args[2]; since != "2026-03-04T10:00:00Z" {
		t.Errorf("the second listing started at %q, want the cursor", since)
	}
}

func TestItems_EmptyListingKeepsTheCursor(t *testing.T) {
	root := t.TempDir()
	gh := testGitHub(threeItems()...)
	s := newSyncer(gh, &fakeWiki{head: testWikiHead})
	if _, err := runSync(t, s, Options{Root: root}); err != nil {
		t.Fatalf("first Run: %v", err)
	}

	gh.ListUpdatedItemsFn = nil
	gh.UpdatedItems = nil
	out, err := runSync(t, s, Options{Root: root})
	if err != nil {
		t.Fatalf("Run over an empty listing: %v", err)
	}
	if !strings.Contains(out, "items: 0 listed, 0 fetched, 3 in the mirror (2 issues, 1 pull requests)\n") {
		t.Errorf("output = %q, want no listed item", out)
	}
	if st := readState(t, mirrorDir(root)); st.Items.Cursor != "2026-03-04T10:00:00Z" {
		t.Errorf("cursor = %q, want it unchanged", st.Items.Cursor)
	}
}

// TestItems_EmptyRepository covers a repository without an issue or a pull
// request: nothing is listed, no item directory is created, and the cursor
// stays empty.
func TestItems_EmptyRepository(t *testing.T) {
	root := t.TempDir()
	gh := testGitHub()
	out, err := runSync(t, newSyncer(gh, &fakeWiki{head: testWikiHead}), Options{Root: root})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out, "items: 0 listed, 0 fetched, 0 in the mirror (0 issues, 0 pull requests)\n") {
		t.Errorf("output = %q, want an empty mirror", out)
	}
	st := readState(t, mirrorDir(root))
	if st.Items.Cursor != "" || st.SyncedAt == "" {
		t.Errorf("state = %+v, want an empty cursor and a finished sync", st)
	}
}

// TestItems_StaleAndBrokenFilesAreFetchedAgain covers the two ways a listed
// item's file is not up to date: its frontmatter holds an older update time,
// or its frontmatter does not parse.
func TestItems_StaleAndBrokenFilesAreFetchedAgain(t *testing.T) {
	root := t.TempDir()
	items := threeItems()
	gh := testGitHub(items...)
	s := newSyncer(gh, &fakeWiki{head: testWikiHead})
	if _, err := runSync(t, s, Options{Root: root}); err != nil {
		t.Fatalf("first Run: %v", err)
	}
	dir := mirrorDir(root)
	fetched := gh.Count("GetItem")

	// GitHub now holds a newer version of issue 7, and the file of pull
	// request 9 was damaged on disk.
	newer := testItem(github.ItemKindIssue, 7, "2026-03-05T10:00:00Z")
	newer.Body = "Edited."
	gh.Items[7] = newer
	gh.UpdatedItems = []github.UpdatedItem{
		{Number: 9, UpdatedAt: "2026-03-04T10:00:00Z", IsPull: true},
		{Number: 7, UpdatedAt: "2026-03-05T10:00:00Z"},
	}
	if err := os.WriteFile(ItemPath(dir, github.ItemKindPull, 9), []byte("not an item file\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	out, err := runSync(t, s, Options{Root: root})
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if got := fetchedNumbers(gh, fetched); !slices.Equal(got, []int{9, 7}) {
		t.Errorf("fetched = %v, want the damaged and the updated item", got)
	}
	if !strings.Contains(out, "items: 2 listed, 2 fetched, 3 in the mirror") {
		t.Errorf("output = %q", out)
	}
	for number, want := range map[int]*github.Item{7: newer, 9: items[2]} {
		data, err := os.ReadFile(ItemPath(dir, want.Kind, number))
		if err != nil || string(data) != string(Render(testRepo, want)) {
			t.Errorf("the file of #%d was not rewritten (%v):\n%s", number, err, data)
		}
	}
	if st := readState(t, dir); st.Items.Cursor != "2026-03-05T10:00:00Z" {
		t.Errorf("cursor = %q, want the newest update time", st.Items.Cursor)
	}
}

// TestItems_FailedFetchKeepsTheCursorAtTheLastHandledEntry covers a fetch that
// fails in the middle of a listing: the run reports it, the other adapters
// still run, and the next run continues with the entries that are left.
func TestItems_FailedFetchKeepsTheCursorAtTheLastHandledEntry(t *testing.T) {
	root := t.TempDir()
	var items []*github.Item
	for i := 1; i <= 5; i++ {
		items = append(items, testItem(github.ItemKindIssue, 10+i, fmt.Sprintf("2026-03-0%dT10:00:00Z", i)))
	}
	gh := testGitHub(items...)
	rateLimit := errors.New("API rate limit exceeded")
	failing := true
	gh.GetItemFn = func(_, _ string, number int) (*github.Item, error) {
		if failing && number == 13 {
			return nil, rateLimit
		}
		it := *gh.Items[number]
		return &it, nil
	}
	s := newSyncer(gh, &fakeWiki{head: testWikiHead})

	out, err := runSync(t, s, Options{Root: root})
	if err == nil || !strings.Contains(err.Error(), "items: fetching acme/widgets#13: ") || !errors.Is(err, rateLimit) {
		t.Fatalf("err = %v, want the failed fetch under the adapter's name", err)
	}
	dir := mirrorDir(root)
	st := readState(t, dir)
	if st.Items.Cursor != "2026-03-02T10:00:00Z" || st.SyncedAt != "" {
		t.Errorf("state = %+v, want the cursor at the second entry and no sync time", st)
	}
	// The history and the wiki were still mirrored.
	if _, err := os.Stat(filepath.Join(dir, historyFileName)); err != nil {
		t.Errorf("the history adapter must still run: %v", err)
	}
	if st.Wiki.Commit != testWikiHead {
		t.Errorf("the wiki adapter must still run: state.Wiki = %+v", st.Wiki)
	}
	if strings.Contains(out, "items:") || !strings.Contains(out, "history: 3 new commits") || !strings.Contains(out, "wiki: acme/widgets.wiki at 1a2b3c4") {
		t.Errorf("output = %q, want the lines of the history and the wiki alone", out)
	}
	if _, err := os.Stat(ItemPath(dir, github.ItemKindIssue, 13)); !os.IsNotExist(err) {
		t.Errorf("the failed item must not have a file: %v", err)
	}

	// The next run lists from the cursor: the second entry is up to date, and
	// the three that are left are fetched.
	failing = false
	fetched := gh.Count("GetItem")
	out, err = runSync(t, s, Options{Root: root})
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if got := fetchedNumbers(gh, fetched); !slices.Equal(got, []int{13, 14, 15}) {
		t.Errorf("the second run fetched %v, want the remaining entries 13, 14, 15", got)
	}
	if !strings.Contains(out, "items: 4 listed, 3 fetched, 5 in the mirror (5 issues, 0 pull requests)\n") {
		t.Errorf("output = %q", out)
	}
	if st := readState(t, dir); st.Items.Cursor != "2026-03-05T10:00:00Z" || st.SyncedAt == "" {
		t.Errorf("state = %+v, want the newest cursor and a sync time", st)
	}
}

// TestItems_FailedWriteKeepsTheCursorAtTheLastHandledEntry covers an item
// file that cannot be written: the run reports it, the other adapters still
// run, and the next run continues with the entry that was not written.
func TestItems_FailedWriteKeepsTheCursorAtTheLastHandledEntry(t *testing.T) {
	root := t.TempDir()
	items := threeItems()
	gh := testGitHub(items[0])
	s := newSyncer(gh, &fakeWiki{head: testWikiHead})
	if _, err := runSync(t, s, Options{Root: root}); err != nil {
		t.Fatalf("first Run: %v", err)
	}
	dir := mirrorDir(root)

	// GitHub holds two more items, and a file stands where pulls/ belongs.
	gh.Items[7], gh.Items[9] = items[1], items[2]
	gh.UpdatedItems = append(gh.UpdatedItems,
		github.UpdatedItem{Number: 7, UpdatedAt: items[1].UpdatedAt},
		github.UpdatedItem{Number: 9, UpdatedAt: items[2].UpdatedAt, IsPull: true})
	blocked := filepath.Join(dir, pullsDirName)
	if err := os.WriteFile(blocked, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	out, err := runSync(t, s, Options{Root: root})
	if want := "items: creating " + blocked + ": "; err == nil || !strings.HasPrefix(err.Error(), want) {
		t.Fatalf("err = %v, want it to start with %q", err, want)
	}
	if st := readState(t, dir); st.Items.Cursor != items[1].UpdatedAt {
		t.Errorf("cursor = %q, want the update time of issue 7, the entry before the failed one", st.Items.Cursor)
	}
	if _, err := os.Stat(ItemPath(dir, github.ItemKindIssue, 7)); err != nil {
		t.Errorf("the entry before the failed one must be written: %v", err)
	}
	if strings.Contains(out, "items:") || !strings.Contains(out, "history: 0 new commits") || !strings.Contains(out, "wiki: acme/widgets.wiki at 1a2b3c4") {
		t.Errorf("output = %q, want the lines of the history and the wiki alone", out)
	}

	if err := os.Remove(blocked); err != nil {
		t.Fatal(err)
	}
	fetched := gh.Count("GetItem")
	if _, err := runSync(t, s, Options{Root: root}); err != nil {
		t.Fatalf("third Run: %v", err)
	}
	if got := fetchedNumbers(gh, fetched); !slices.Equal(got, []int{9}) {
		t.Errorf("the third run fetched %v, want the pull request alone", got)
	}
}

// TestItems_CountsItemFilesOnly covers what an interrupted sync and a person
// leave in issues/: the summary counts the item files alone.
func TestItems_CountsItemFilesOnly(t *testing.T) {
	root := t.TempDir()
	items := threeItems()
	s := newSyncer(testGitHub(items...), &fakeWiki{head: testWikiHead})
	if _, err := runSync(t, s, Options{Root: root}); err != nil {
		t.Fatalf("first Run: %v", err)
	}
	issues := itemsDir(mirrorDir(root), github.ItemKindIssue)
	for name, data := range map[string][]byte{
		"3.md.tmp":  Render(testRepo, items[0]),
		"notes.txt": []byte("not an item\n"),
	} {
		if err := os.WriteFile(filepath.Join(issues, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(issues, "old.md"), 0o700); err != nil {
		t.Fatal(err)
	}

	out, err := runSync(t, s, Options{Root: root})
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if !strings.Contains(out, "3 in the mirror (2 issues, 1 pull requests)\n") {
		t.Errorf("output = %q, want the three item files counted and nothing else", out)
	}
}

func TestItems_FailedListingWritesNothing(t *testing.T) {
	root := t.TempDir()
	items := threeItems()
	gh := testGitHub(items[0])
	s := newSyncer(gh, &fakeWiki{head: testWikiHead})
	if _, err := runSync(t, s, Options{Root: root}); err != nil {
		t.Fatalf("first Run: %v", err)
	}
	dir := mirrorDir(root)
	before := tree(t, dir, "issues")

	// GitHub holds two more items, and the listing fails.
	gh.Items[7], gh.Items[9] = items[1], items[2]
	gh.UpdatedItems = append(gh.UpdatedItems,
		github.UpdatedItem{Number: 7, UpdatedAt: items[1].UpdatedAt},
		github.UpdatedItem{Number: 9, UpdatedAt: items[2].UpdatedAt, IsPull: true})
	gh.UpdatedItemsErr = errors.New("API rate limit exceeded")
	s.Now = func() time.Time { return fixedNow.Add(time.Hour) }

	_, err := runSync(t, s, Options{Root: root})
	if err == nil || !strings.HasPrefix(err.Error(), "items: ") || !errors.Is(err, gh.UpdatedItemsErr) {
		t.Fatalf("err = %v, want the listing's error under the adapter's name", err)
	}
	if after := tree(t, dir, "issues"); len(after) != len(before) {
		t.Errorf("a failed listing wrote item files: %d before, %d after", len(before), len(after))
	}
	if _, err := os.Stat(filepath.Join(dir, pullsDirName)); !os.IsNotExist(err) {
		t.Errorf("a failed listing must not create pulls/: %v", err)
	}
	if st := readState(t, dir); st.Items.Cursor != openIssueUpdated || st.SyncedAt != fixedNowStamp {
		t.Errorf("state = %+v, want the cursor and the sync time of the first run", st)
	}
}

// TestItems_LogsProgressEveryFiftyFetches covers a long first sync: a progress
// line follows every 50 fetched items.
func TestItems_LogsProgressEveryFiftyFetches(t *testing.T) {
	var items []*github.Item
	start := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	for i := 1; i <= 51; i++ {
		items = append(items, testItem(github.ItemKindIssue, i, start.Add(time.Duration(i)*time.Minute).Format(time.RFC3339)))
	}
	logs := captureLogs(t)
	if _, err := runSync(t, newSyncer(testGitHub(items...), &fakeWiki{head: testWikiHead}), Options{Root: t.TempDir()}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := strings.Count(logs.String(), "mirroring items"); got != 1 {
		t.Errorf("logged %d progress lines for 51 fetches, want 1:\n%s", got, logs)
	}
	for _, want := range []string{"done=50", "listed=51"} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("the progress line lacks %s:\n%s", want, logs)
		}
	}
}

func TestLaterCursor(t *testing.T) {
	for _, tc := range []struct{ name, cursor, updatedAt, want string }{
		{"a later time moves the cursor", "2026-03-01T09:00:00Z", "2026-03-02T09:00:00Z", "2026-03-02T09:00:00Z"},
		{"an earlier time keeps it", "2026-03-02T09:00:00Z", "2026-03-01T09:00:00Z", "2026-03-02T09:00:00Z"},
		{"the same time keeps it", "2026-03-02T09:00:00Z", "2026-03-02T09:00:00Z", "2026-03-02T09:00:00Z"},
		{"an empty cursor takes the time", "", "2026-03-01T09:00:00Z", "2026-03-01T09:00:00Z"},
		{"a time that is not RFC 3339 keeps it", "2026-03-01T09:00:00Z", "yesterday", "2026-03-01T09:00:00Z"},
		{"the offset counts, not the text", "2026-03-02T09:00:00Z", "2026-03-02T10:30:00+02:00", "2026-03-02T09:00:00Z"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := laterCursor(tc.cursor, tc.updatedAt); got != tc.want {
				t.Errorf("laterCursor(%q, %q) = %q, want %q", tc.cursor, tc.updatedAt, got, tc.want)
			}
		})
	}
}
