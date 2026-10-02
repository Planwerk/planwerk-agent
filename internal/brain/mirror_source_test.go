package brain

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/planwerk/planwerk-agent/internal/capture"
	"github.com/planwerk/planwerk-agent/internal/github"
	"github.com/planwerk/planwerk-agent/internal/github/githubtest"
	"github.com/planwerk/planwerk-agent/internal/mirror"
)

// The owner and the name of testRepoRef.
const (
	mirrorOwner = "acme"
	mirrorName  = "widgets"
)

// mirrorItems are the items the test mirror is built from, in the order of
// their update time: two closed issues, an open one, two merged pull requests
// (one by a bot), one closed without a merge, and an open one.
func mirrorItems() []*github.Item {
	return []*github.Item{
		{Kind: github.ItemKindIssue, Number: 5, Title: "Closed by a commit", State: "closed", StateReason: "completed",
			Author: "reporter", CreatedAt: "2025-12-31T10:00:00Z", UpdatedAt: "2026-01-01T10:00:00Z", ClosedAt: "2026-01-01T10:00:00Z",
			CloserSHA: "c1"},
		{Kind: github.ItemKindIssue, Number: 3, Title: "Need a flag", Body: "Please add one.", State: "closed", StateReason: "completed",
			Author: "reporter", AuthorAssociation: "NONE",
			CreatedAt: "2026-01-01T10:00:00Z", UpdatedAt: "2026-01-05T10:00:00Z", ClosedAt: "2026-01-05T10:00:00Z",
			ClosedByPRs: []int{7},
			Comments: []github.ItemComment{
				{ID: "IC_1", URL: "u1", Author: "maintainer", AuthorAssociation: "MEMBER", CreatedAt: "2026-01-02T10:00:00Z", UpdatedAt: "2026-01-02T10:05:00Z", Body: "Agreed.\nSend a pull request."},
				{ID: "IC_2", URL: "u2", Author: "passerby", AuthorAssociation: "NONE", CreatedAt: "2026-01-02T11:00:00Z", UpdatedAt: "2026-01-02T11:00:00Z", Body: "Same here."},
			}},
		{Kind: github.ItemKindIssue, Number: 4, Title: "Still open", State: "open",
			Author: "reporter", CreatedAt: "2026-01-01T12:00:00Z", UpdatedAt: "2026-01-06T10:00:00Z"},
		{Kind: github.ItemKindPull, Number: 7, Title: "Add the flag", Body: "Closes #3", State: "merged",
			Author: "contributor", AuthorAssociation: "CONTRIBUTOR", BaseBranch: "main",
			CreatedAt: "2026-01-02T10:00:00Z", UpdatedAt: "2026-01-07T10:00:00Z", ClosedAt: "2026-01-04T10:00:00Z", MergedAt: "2026-01-04T10:00:00Z",
			ClosesIssues: []int{3},
			Comments: []github.ItemComment{
				{ID: "IC_3", URL: "u3", Author: "passerby", AuthorAssociation: "NONE", CreatedAt: "2026-01-03T10:00:00Z", UpdatedAt: "2026-01-03T10:00:00Z", Body: "Nice."},
			},
			Reviews: []github.ItemReview{
				{ID: "PRR_1", URL: "u4", Author: "maintainer", AuthorAssociation: "MEMBER", State: "APPROVED", SubmittedAt: "2026-01-03T11:00:00Z", UpdatedAt: "2026-01-03T11:00:00Z"},
				{ID: "PRR_2", URL: "u5", Author: "maintainer", AuthorAssociation: "MEMBER", State: "CHANGES_REQUESTED", SubmittedAt: "2026-01-03T12:00:00Z", UpdatedAt: "2026-01-03T12:00:00Z", Body: "Wrap the error."},
			},
			Threads: []github.ItemThread{
				{ID: "PRRT_1", IsResolved: true, Path: "cmd/flag.go", Line: 12, DiffHunk: "@@ -1 +1 @@\n-old\n+new", Comments: []github.ItemComment{
					{ID: "PRRC_1", URL: "u6", Author: "maintainer", AuthorAssociation: "MEMBER", CreatedAt: "2026-01-03T12:00:00Z", UpdatedAt: "2026-01-03T12:00:00Z", Body: "Wrap it."},
					{ID: "PRRC_2", URL: "u7", Author: "contributor", AuthorAssociation: "CONTRIBUTOR", CreatedAt: "2026-01-03T13:00:00Z", UpdatedAt: "2026-01-03T13:00:00Z", Body: "Done."},
				}},
				{ID: "PRRT_2", IsOutdated: true},
			},
			Commits: []github.ItemCommit{
				{SHA: "c2", CommittedAt: "2026-01-02T09:00:00Z", Headline: "Add the flag", Body: "The default stays off."},
			}},
		{Kind: github.ItemKindPull, Number: 8, Title: "Closed without a merge", State: "closed",
			Author: "contributor", CreatedAt: "2026-01-02T12:00:00Z", UpdatedAt: "2026-01-08T10:00:00Z", ClosedAt: "2026-01-08T10:00:00Z"},
		{Kind: github.ItemKindPull, Number: 9, Title: "Still open", State: "open",
			Author: "contributor", CreatedAt: "2026-01-02T13:00:00Z", UpdatedAt: "2026-01-09T10:00:00Z"},
		{Kind: github.ItemKindPull, Number: 10, Title: "Update a dependency", State: "merged",
			Author: "renovate", AuthorIsBot: true, BaseBranch: "main",
			CreatedAt: "2026-01-03T10:00:00Z", UpdatedAt: "2026-01-10T10:00:00Z", ClosedAt: "2026-01-06T10:00:00Z", MergedAt: "2026-01-06T10:00:00Z"},
	}
}

// mirrorHistory is the default branch of the test mirror, oldest first.
func mirrorHistory() []github.HistoryCommit {
	return []github.HistoryCommit{
		{SHA: "c1", CommittedAt: time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)},
		{SHA: "c2", CommittedAt: time.Date(2026, 1, 4, 10, 0, 0, 0, time.UTC), PRNumber: 7},
		{SHA: "c3", CommittedAt: time.Date(2026, 1, 6, 10, 0, 0, 0, time.UTC), PRNumber: 10},
		{SHA: "c4", CommittedAt: time.Date(2026, 1, 7, 10, 0, 0, 0, time.UTC)},
	}
}

// buildMirror syncs items and history into a fresh mirror through the run of
// `brain sync` and returns a MirrorSource over it.
func buildMirror(t *testing.T, items []*github.Item, history []github.HistoryCommit) MirrorSource {
	t.Helper()
	gh := &githubtest.Fake{Items: map[int]*github.Item{}, History: history}
	for _, it := range items {
		gh.Items[it.Number] = it
		gh.UpdatedItems = append(gh.UpdatedItems, github.UpdatedItem{Number: it.Number, UpdatedAt: it.UpdatedAt, IsPull: it.Kind == github.ItemKindPull})
	}
	root := t.TempDir()
	s := &mirror.Syncer{
		GitHub:    gh,
		CloneWiki: func(_, _, _ string) (string, error) { return "1a2b3c4d", nil },
		Now:       func() time.Time { return time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC) },
	}
	var out bytes.Buffer
	if err := s.Run(&out, mirror.Options{RepoRef: testRepoRef, Root: root}); err != nil {
		t.Fatalf("building the mirror: %v", err)
	}
	dir, err := mirror.Dir(root, mirrorOwner, mirrorName)
	if err != nil {
		t.Fatal(err)
	}
	return MirrorSource{Dir: dir}
}

// wantListing is the listing of mirrorItems and mirrorHistory as the GitHub
// API returns it: merged pull requests and closed issues in creation order.
func wantListing() Listing {
	return Listing{
		History: mirrorHistory(),
		PRs: []github.MergedPR{
			{Number: 7, Title: "Add the flag", MergedAt: time.Date(2026, 1, 4, 10, 0, 0, 0, time.UTC), ClosesIssues: []int{3}},
			{Number: 10, Title: "Update a dependency", MergedAt: time.Date(2026, 1, 6, 10, 0, 0, 0, time.UTC), AuthorIsBot: true},
		},
		Issues: []github.ClosedIssue{
			{Number: 5, Title: "Closed by a commit", ClosedAt: time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC), CloserSHA: "c1"},
			{Number: 3, Title: "Need a flag", ClosedAt: time.Date(2026, 1, 5, 10, 0, 0, 0, time.UTC), ClosedByPRs: []int{7}},
		},
	}
}

func TestMirrorSource_List(t *testing.T) {
	src := buildMirror(t, mirrorItems(), mirrorHistory())
	logs := captureLogs(t)

	got, err := src.List(mirrorOwner, mirrorName)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	// The open issue, the open pull request, and the pull request closed
	// without a merge are left out; issue 5 was created before issue 3.
	if want := wantListing(); !reflect.DeepEqual(got, want) {
		t.Errorf("listing = %+v\nwant %+v", got, want)
	}
	for _, want := range []string{"reading the history from the local mirror", src.Dir, "synced_at=2026-10-02T09:00:00Z"} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("log %q lacks %q", logs, want)
		}
	}
}

// TestMirrorSource_ListSortsByCreationThenNumber covers two items created in
// the same second: the lower number comes first, whatever the file order.
func TestMirrorSource_ListSortsByCreationThenNumber(t *testing.T) {
	items := []*github.Item{
		{Kind: github.ItemKindIssue, Number: 20, State: "closed", CreatedAt: "2026-01-01T10:00:00Z", UpdatedAt: "2026-01-02T10:00:00Z", ClosedAt: "2026-01-02T10:00:00Z"},
		{Kind: github.ItemKindIssue, Number: 100, State: "closed", CreatedAt: "2025-12-01T10:00:00Z", UpdatedAt: "2026-01-03T10:00:00Z", ClosedAt: "2026-01-03T10:00:00Z"},
		{Kind: github.ItemKindIssue, Number: 3, State: "closed", CreatedAt: "2026-01-01T10:00:00Z", UpdatedAt: "2026-01-04T10:00:00Z", ClosedAt: "2026-01-04T10:00:00Z"},
	}
	got, err := buildMirror(t, items, nil).List(mirrorOwner, mirrorName)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	var numbers []int
	for _, iss := range got.Issues {
		numbers = append(numbers, iss.Number)
	}
	if !slices.Equal(numbers, []int{100, 3, 20}) {
		t.Errorf("issues = %v, want them by creation time, then number: [100 3 20]", numbers)
	}
}

// TestMirrorSource_ListIgnoresWhatIsNotAnItemFile covers what an interrupted
// sync and a person leave in issues/: the temporary file of an atomic write, a
// file of another kind, and a directory are no items.
func TestMirrorSource_ListIgnoresWhatIsNotAnItemFile(t *testing.T) {
	items := mirrorItems()
	src := buildMirror(t, items, mirrorHistory())
	issues := filepath.Dir(mirror.ItemPath(src.Dir, github.ItemKindIssue, 3))
	for name, data := range map[string][]byte{
		"3.md.tmp":  mirror.Render(testRepoRef, items[1]),
		"notes.txt": []byte("not an item\n"),
	} {
		if err := os.WriteFile(filepath.Join(issues, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(issues, "old.md"), 0o700); err != nil {
		t.Fatal(err)
	}

	got, err := src.List(mirrorOwner, mirrorName)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if want := wantListing(); !reflect.DeepEqual(got, want) {
		t.Errorf("listing = %+v\nwant %+v", got, want)
	}
}

// saveMirrorState writes a state.json for acme/widgets into a fresh mirror
// directory.
func saveMirrorState(t *testing.T, syncedAt string) MirrorSource {
	t.Helper()
	dir := t.TempDir()
	st := mirror.State{Version: 1, Repo: testRepoRef, SyncedAt: syncedAt}
	if err := st.Save(dir); err != nil {
		t.Fatalf("saving the state: %v", err)
	}
	return MirrorSource{Dir: dir}
}

func TestMirrorSource_ListOfAnEmptyMirror(t *testing.T) {
	// A synced mirror of a repository without an item and without a commit:
	// no issues/, no pulls/, no history.jsonl.
	src := saveMirrorState(t, "2026-10-02T09:00:00Z")
	got, err := src.List(mirrorOwner, mirrorName)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if !reflect.DeepEqual(got, Listing{}) {
		t.Errorf("listing = %+v, want an empty one", got)
	}
}

func TestMirrorSource_ListNeedsAFinishedSync(t *testing.T) {
	t.Run("no mirror", func(t *testing.T) {
		src := MirrorSource{Dir: filepath.Join(t.TempDir(), "acme", "widgets")}
		_, err := src.List(mirrorOwner, mirrorName)
		want := "no mirror of acme/widgets at " + src.Dir + `; run "planwerk-agent brain sync acme/widgets" first`
		if err == nil || err.Error() != want {
			t.Errorf("err = %v\nwant %s", err, want)
		}
	})

	t.Run("a mirror no sync has finished", func(t *testing.T) {
		src := saveMirrorState(t, "")
		_, err := src.List(mirrorOwner, mirrorName)
		want := "the mirror of acme/widgets at " + src.Dir + ` has never finished a sync; run "planwerk-agent brain sync acme/widgets" again`
		if err == nil || err.Error() != want {
			t.Errorf("err = %v\nwant %s", err, want)
		}
	})

	t.Run("the mirror of another repository", func(t *testing.T) {
		src := saveMirrorState(t, "2026-10-02T09:00:00Z")
		_, err := src.List(mirrorOwner, "gadgets")
		if err == nil || !strings.Contains(err.Error(), "holds the mirror of acme/widgets, not acme/gadgets") {
			t.Errorf("err = %v, want the state's error", err)
		}
	})
}

// TestMirrorSource_ListFailsOnABrokenItemFile proves a damaged mirror stops
// the listing with the file's path, where skipping the file would shorten the
// history without notice.
func TestMirrorSource_ListFailsOnABrokenItemFile(t *testing.T) {
	for _, tc := range []struct {
		name    string
		file    string
		content func(items []*github.Item) []byte
		want    string
	}{
		{"broken frontmatter", "issues/3.md", func([]*github.Item) []byte { return []byte("not an item file\n") }, "no frontmatter"},
		{"a merge time that is not RFC 3339", "pulls/7.md", func(items []*github.Item) []byte {
			pr := *items[3]
			pr.MergedAt = "yesterday"
			return mirror.Render(testRepoRef, &pr)
		}, "parsing time"},
		{"a creation time that is not RFC 3339", "issues/5.md", func(items []*github.Item) []byte {
			iss := *items[0]
			iss.CreatedAt = ""
			return mirror.Render(testRepoRef, &iss)
		}, "parsing time"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			items := mirrorItems()
			src := buildMirror(t, items, mirrorHistory())
			path := filepath.Join(src.Dir, filepath.FromSlash(tc.file))
			if err := os.WriteFile(path, tc.content(items), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := src.List(mirrorOwner, mirrorName)
			if err == nil || !strings.HasPrefix(err.Error(), "reading "+path+": ") || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want it to start with the file's path and contain %q", err, tc.want)
			}
		})
	}

	// The times of an item the listing does not keep are not read.
	t.Run("an open item with a broken time is left out", func(t *testing.T) {
		items := mirrorItems()
		src := buildMirror(t, items, mirrorHistory())
		open := *items[2]
		open.CreatedAt = "yesterday"
		if err := os.WriteFile(mirror.ItemPath(src.Dir, github.ItemKindIssue, 4), mirror.Render(testRepoRef, &open), 0o600); err != nil {
			t.Fatal(err)
		}
		if got, err := src.List(mirrorOwner, mirrorName); err != nil || len(got.Issues) != 2 {
			t.Errorf("listing = %+v, %v, want the two closed issues", got.Issues, err)
		}
	})

	t.Run("a history that does not parse", func(t *testing.T) {
		src := buildMirror(t, mirrorItems(), mirrorHistory())
		if err := os.WriteFile(filepath.Join(src.Dir, "history.jsonl"), []byte("not json\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := src.List(mirrorOwner, mirrorName); err == nil || !strings.Contains(err.Error(), "history.jsonl line 1: ") {
			t.Errorf("err = %v, want the history's parse error", err)
		}
	})
}

func TestMirrorSource_IssueAndPullRequest(t *testing.T) {
	src := buildMirror(t, mirrorItems(), mirrorHistory())

	iss, err := src.Issue(mirrorOwner, mirrorName, 3)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	wantIssue := &github.IssueThread{
		Number: 3, Title: "Need a flag", Body: "Please add one.", Author: "reporter",
		Comments: []github.ThreadComment{
			{Author: "maintainer", AuthorAssociation: "MEMBER", CreatedAt: "2026-01-02T10:00:00Z", Body: "Agreed.\nSend a pull request."},
			{Author: "passerby", AuthorAssociation: "NONE", CreatedAt: "2026-01-02T11:00:00Z", Body: "Same here."},
		},
	}
	if !reflect.DeepEqual(iss, wantIssue) {
		t.Errorf("issue = %+v\nwant %+v", iss, wantIssue)
	}

	pr, err := src.PullRequest(mirrorOwner, mirrorName, 7)
	if err != nil {
		t.Fatalf("PullRequest: %v", err)
	}
	wantPR := &github.PRThread{
		Number: 7, Title: "Add the flag", Body: "Closes #3", Author: "contributor",
		Comments: []github.ThreadComment{{Author: "passerby", AuthorAssociation: "NONE", CreatedAt: "2026-01-03T10:00:00Z", Body: "Nice."}},
		Reviews: []github.PRReview{
			{Author: "maintainer", SubmittedAt: "2026-01-03T11:00:00Z"},
			{Author: "maintainer", SubmittedAt: "2026-01-03T12:00:00Z", Body: "Wrap the error."},
		},
		Commits: []github.PRCommit{{SHA: "c2", Headline: "Add the flag", Body: "The default stays off."}},
		ReviewThreads: []github.ReviewThread{
			{ID: "PRRT_1", IsResolved: true, Path: "cmd/flag.go", Line: 12, DiffHunk: "@@ -1 +1 @@\n-old\n+new", Comments: []github.ReviewThreadComment{
				{Author: "maintainer", Body: "Wrap it.", CreatedAt: "2026-01-03T12:00:00Z"},
				{Author: "contributor", Body: "Done.", CreatedAt: "2026-01-03T13:00:00Z"},
			}},
			{ID: "PRRT_2", IsOutdated: true},
		},
	}
	if !reflect.DeepEqual(pr, wantPR) {
		t.Errorf("pull request = %+v\nwant %+v", pr, wantPR)
	}

	// An item without a conversation has nil lists, as the API readers return.
	bare, err := src.PullRequest(mirrorOwner, mirrorName, 10)
	if err != nil {
		t.Fatalf("PullRequest: %v", err)
	}
	if want := (&github.PRThread{Number: 10, Title: "Update a dependency", Author: "renovate"}); !reflect.DeepEqual(bare, want) {
		t.Errorf("pull request = %+v\nwant %+v", bare, want)
	}
}

func TestMirrorSource_ItemErrors(t *testing.T) {
	src := buildMirror(t, mirrorItems(), mirrorHistory())

	t.Run("an item the mirror does not hold", func(t *testing.T) {
		want := "acme/widgets#9 is not in the mirror at " + src.Dir + `; run "planwerk-agent brain sync acme/widgets"`
		// Number 9 is a pull request, so the mirror holds no issue 9.
		if _, err := src.Issue(mirrorOwner, mirrorName, 9); err == nil || err.Error() != want {
			t.Errorf("Issue err = %v\nwant %s", err, want)
		}
		wantPR := "acme/widgets#3 is not in the mirror at " + src.Dir + `; run "planwerk-agent brain sync acme/widgets"`
		if _, err := src.PullRequest(mirrorOwner, mirrorName, 3); err == nil || err.Error() != wantPR {
			t.Errorf("PullRequest err = %v\nwant %s", err, wantPR)
		}
	})

	t.Run("a file that does not parse", func(t *testing.T) {
		path := mirror.ItemPath(src.Dir, github.ItemKindIssue, 3)
		if err := os.WriteFile(path, []byte("---\nformat: 1\n---\n"+`<!-- planwerk-agent:mirror body {"lines":4} -->`+"\ncut short\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := src.Issue(mirrorOwner, mirrorName, 3)
		if want := "reading " + path + ": line 4: block of 4 lines runs past the end of the file"; err == nil || err.Error() != want {
			t.Errorf("err = %v\nwant %s", err, want)
		}
	})
}

// TestMirrorSource_BuildsTheSameUnitsAsTheAPI proves the mirror is a drop-in
// for the API source: the same history yields the same units in the same
// order.
func TestMirrorSource_BuildsTheSameUnitsAsTheAPI(t *testing.T) {
	fromMirror, err := buildMirror(t, mirrorItems(), mirrorHistory()).List(mirrorOwner, mirrorName)
	if err != nil {
		t.Fatalf("MirrorSource.List: %v", err)
	}
	fromAPI, err := (&fakeSource{listing: wantListing()}).List(mirrorOwner, mirrorName)
	if err != nil {
		t.Fatalf("fakeSource.List: %v", err)
	}

	mirrorUnits, mirrorBots := BuildUnits(testRepoRef, fromMirror, nil)
	apiUnits, apiBots := BuildUnits(testRepoRef, fromAPI, nil)
	want := []string{"issue-5", "issue-3", "commits-c4-c4"}
	if got := unitKeys(mirrorUnits); !slices.Equal(got, want) || !slices.Equal(got, unitKeys(apiUnits)) {
		t.Errorf("units from the mirror = %v, from the API = %v, want %v from both", got, unitKeys(apiUnits), want)
	}
	if mirrorBots != 1 || apiBots != 1 {
		t.Errorf("skipped bot pull requests = %d and %d, want 1 from both", mirrorBots, apiBots)
	}
}

// TestMirrorSource_DryRunOfTheBootstrap runs `brain bootstrap --dry-run` over
// the mirror: the units come from the files, and GitHub is asked for the clone
// alone.
func TestMirrorSource_DryRunOfTheBootstrap(t *testing.T) {
	gh := &githubtest.Fake{}
	clone := t.TempDir()
	gh.CloneRepoFn = func(string) (*github.Repo, error) {
		return &github.Repo{Owner: mirrorOwner, Name: mirrorName, Dir: clone, Local: true}, nil
	}
	run := func(src Source) (string, error) {
		var out bytes.Buffer
		b := &Bootstrapper{Source: src, GitHub: gh}
		err := b.Run(&out, BootstrapOptions{RepoRef: testRepoRef, StateDir: filepath.Join(t.TempDir(), StateDirName), DryRun: true, NoDecisionDocs: true})
		return out.String(), err
	}

	out, err := run(buildMirror(t, mirrorItems(), mirrorHistory()))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	assertContains(t, out, "Units: 3 total, 0 processed, 3 remaining\n", "1 bot-authored pull requests skipped\n",
		"issue-5  Closed by a commit\nissue-3  Need a flag\ncommits-c4-c4  ")
	for _, method := range []string{"DefaultBranchHistory", "ListMergedPRs", "ListClosedIssues"} {
		if gh.Count(method) != 0 {
			t.Errorf("the run called %s; the history must come from the mirror", method)
		}
	}

	// A mirror that is missing stops the run with the command that builds it.
	missing := MirrorSource{Dir: filepath.Join(t.TempDir(), "acme", "widgets")}
	_, err = run(missing)
	if err == nil || !strings.Contains(err.Error(), "no mirror of acme/widgets at "+missing.Dir+`; run "planwerk-agent brain sync acme/widgets" first`) {
		t.Errorf("err = %v, want the missing mirror and the command to run", err)
	}
}

// TestMirrorSource_RunOfTheBootstrap runs `brain bootstrap` over the mirror to
// its end: the analysis is handed the conversations of the mirror files,
// redacted, and GitHub is asked for no thread.
func TestMirrorSource_RunOfTheBootstrap(t *testing.T) {
	items := mirrorItems()
	items[1].Body = "credentials: AKIAIOSFODNN7EXAMPLE used for S3"
	src := buildMirror(t, items, mirrorHistory())

	gh := &githubtest.Fake{}
	clone := t.TempDir()
	gh.CloneRepoFn = func(string) (*github.Repo, error) {
		return &github.Repo{Owner: mirrorOwner, Name: mirrorName, Dir: clone, Local: true}, nil
	}
	analyzed := map[string]string{}
	b := &Bootstrapper{
		Source: src,
		GitHub: gh,
		// A nil result proposes nothing, so no review follows.
		Analyze: func(_ string, ctx UnitContext) (*capture.CaptureResult, error) {
			var bodies []string
			for _, it := range ctx.Items {
				bodies = append(bodies, it.Body)
			}
			analyzed[ctx.Unit.Key] = strings.Join(bodies, "\n")
			return nil, nil
		},
		Writer: newFakeWiki(t, "wiki-head", nil),
	}
	var out bytes.Buffer
	err := b.Run(&out, BootstrapOptions{RepoRef: testRepoRef, StateDir: filepath.Join(t.TempDir(), StateDirName), NoDecisionDocs: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Issue 3 was closed by pull request 7, so its unit holds both.
	content, ok := analyzed["issue-3"]
	if !ok || len(analyzed) != 3 {
		t.Fatalf("analyzed %d units, want three with issue-3 among them", len(analyzed))
	}
	if strings.Contains(content, "AKIAIOSFODNN7EXAMPLE") || !strings.Contains(content, "[REDACTED:aws-access-key-id]") {
		t.Errorf("the analysis saw an unredacted body:\n%s", content)
	}
	for _, want := range []string{"Agreed.\nSend a pull request.", "Closes #3", "Wrap the error.", "Wrap it.", "The default stays off."} {
		if !strings.Contains(content, want) {
			t.Errorf("the unit lacks %q of the mirrored conversation:\n%s", want, content)
		}
	}
	for _, method := range []string{"DefaultBranchHistory", "ListMergedPRs", "ListClosedIssues", "GetIssueThread", "GetPRThread"} {
		if gh.Count(method) != 0 {
			t.Errorf("the run called %s; the history and every thread must come from the mirror", method)
		}
	}
}
