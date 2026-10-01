package brain

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/planwerk/planwerk-agent/internal/github"
)

const unitsRepo = "acme/widgets"

// day returns noon of the n-th day of the test history.
func day(n int) time.Time {
	return time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC).AddDate(0, 0, n)
}

// commit is a history commit committed on day n; pr is its merged pull
// request, 0 for a commit pushed without one.
func commit(sha string, n, pr int) github.HistoryCommit {
	return github.HistoryCommit{SHA: sha, CommittedAt: day(n), PRNumber: pr}
}

func unitKeys(units []Unit) []string {
	keys := make([]string, 0, len(units))
	for _, u := range units {
		keys = append(keys, u.Key)
	}
	return keys
}

func unitByKey(t *testing.T, units []Unit, key string) Unit {
	t.Helper()
	for _, u := range units {
		if u.Key == key {
			return u
		}
	}
	t.Fatalf("no unit %q in %v", key, unitKeys(units))
	return Unit{}
}

func TestBuildUnits(t *testing.T) {
	t.Parallel()

	t.Run("a closed issue holds its closing pull request, which gets no unit of its own", func(t *testing.T) {
		t.Parallel()
		l := Listing{
			History: []github.HistoryCommit{commit("c1", 1, 7)},
			PRs:     []github.MergedPR{{Number: 7, Title: "Add a flag", MergedAt: day(1), ClosesIssues: []int{3}}},
			Issues:  []github.ClosedIssue{{Number: 3, Title: "Need a flag", ClosedAt: day(1)}},
		}
		units, bots := BuildUnits(unitsRepo, l, nil)
		if got := unitKeys(units); !slices.Equal(got, []string{"issue-3"}) {
			t.Fatalf("units = %v, want [issue-3]", got)
		}
		u := units[0]
		if u.Kind != KindIssue || u.Issue != 3 || u.Title != "Need a flag" || !slices.Equal(u.PRs, []int{7}) || len(u.Commits) != 0 {
			t.Errorf("unit = %+v", u)
		}
		if bots != 0 {
			t.Errorf("skippedBots = %d, want 0", bots)
		}
	})

	t.Run("the link from the issue side attaches a pull request too, once", func(t *testing.T) {
		t.Parallel()
		l := Listing{
			PRs: []github.MergedPR{
				{Number: 9, MergedAt: day(2), ClosesIssues: []int{3}},
				{Number: 7, MergedAt: day(1)},
			},
			Issues: []github.ClosedIssue{{Number: 3, ClosedAt: day(2), ClosedByPRs: []int{9, 7}}},
		}
		units, _ := BuildUnits(unitsRepo, l, nil)
		if got := unitKeys(units); !slices.Equal(got, []string{"issue-3"}) {
			t.Fatalf("units = %v, want [issue-3]", got)
		}
		if !slices.Equal(units[0].PRs, []int{7, 9}) {
			t.Errorf("PRs = %v, want [7 9], ascending and without a duplicate", units[0].PRs)
		}
	})

	t.Run("a pull request closing two closed issues is attached to each", func(t *testing.T) {
		t.Parallel()
		l := Listing{
			PRs:    []github.MergedPR{{Number: 7, MergedAt: day(1), ClosesIssues: []int{3, 4, 99}}},
			Issues: []github.ClosedIssue{{Number: 3, ClosedAt: day(1)}, {Number: 4, ClosedAt: day(1)}},
		}
		units, _ := BuildUnits(unitsRepo, l, nil)
		if got := unitKeys(units); !slices.Equal(got, []string{"issue-3", "issue-4"}) {
			t.Fatalf("units = %v, want [issue-3 issue-4]", got)
		}
		for _, u := range units {
			if !slices.Equal(u.PRs, []int{7}) {
				t.Errorf("%s: PRs = %v, want [7]", u.Key, u.PRs)
			}
		}
	})

	t.Run("a pull request closing no closed issue is a unit, a bot's is skipped and counted", func(t *testing.T) {
		t.Parallel()
		l := Listing{
			PRs: []github.MergedPR{
				{Number: 7, Title: "Refactor the loader", MergedAt: day(1)},
				{Number: 8, Title: "Update a dependency", MergedAt: day(2), AuthorIsBot: true},
				// Issue 50 is still open, so it has no unit to attach to.
				{Number: 9, Title: "Half of a fix", MergedAt: day(3), ClosesIssues: []int{50}},
			},
		}
		units, bots := BuildUnits(unitsRepo, l, nil)
		if got := unitKeys(units); !slices.Equal(got, []string{"pr-7", "pr-9"}) {
			t.Fatalf("units = %v, want [pr-7 pr-9]", got)
		}
		if u := units[0]; u.Kind != KindPR || u.Title != "Refactor the loader" || !slices.Equal(u.PRs, []int{7}) || u.Issue != 0 {
			t.Errorf("unit = %+v", u)
		}
		if bots != 1 {
			t.Errorf("skippedBots = %d, want 1", bots)
		}
	})

	t.Run("a bot's pull request that closes a closed issue is attached to it", func(t *testing.T) {
		t.Parallel()
		l := Listing{
			PRs:    []github.MergedPR{{Number: 8, MergedAt: day(1), AuthorIsBot: true, ClosesIssues: []int{3}}},
			Issues: []github.ClosedIssue{{Number: 3, ClosedAt: day(1)}},
		}
		units, bots := BuildUnits(unitsRepo, l, nil)
		if got := unitKeys(units); !slices.Equal(got, []string{"issue-3"}) || !slices.Equal(units[0].PRs, []int{8}) {
			t.Errorf("units = %+v, want issue-3 holding pull request 8", units)
		}
		if bots != 0 {
			t.Errorf("skippedBots = %d, want 0", bots)
		}
	})

	t.Run("a commit that closed an issue belongs to the issue and to no range", func(t *testing.T) {
		t.Parallel()
		l := Listing{
			History: []github.HistoryCommit{commit("a1", 1, 0), commit("fix", 2, 0), commit("a2", 3, 0)},
			Issues:  []github.ClosedIssue{{Number: 3, ClosedAt: day(2), CloserSHA: "fix"}},
		}
		units, _ := BuildUnits(unitsRepo, l, nil)
		if got := unitKeys(units); !slices.Equal(got, []string{"commits-a1-a1", "issue-3", "commits-a2-a2"}) {
			t.Fatalf("units = %v", got)
		}
		if got := unitByKey(t, units, "issue-3").Commits; !slices.Equal(got, []string{"fix"}) {
			t.Errorf("issue commits = %v, want [fix]", got)
		}
		for _, u := range units {
			if u.Kind == KindCommits && slices.Contains(u.Commits, "fix") {
				t.Errorf("range %s holds the closer commit", u.Key)
			}
		}
	})

	t.Run("a closer that is not in the history, or belongs to a pull request, is not a unit commit", func(t *testing.T) {
		t.Parallel()
		l := Listing{
			History: []github.HistoryCommit{commit("c1", 1, 7)},
			PRs:     []github.MergedPR{{Number: 7, MergedAt: day(1)}},
			Issues: []github.ClosedIssue{
				{Number: 3, ClosedAt: day(1), CloserSHA: "gone"},
				{Number: 4, ClosedAt: day(1), CloserSHA: "c1"},
			},
		}
		units, _ := BuildUnits(unitsRepo, l, nil)
		for _, key := range []string{"issue-3", "issue-4"} {
			if got := unitByKey(t, units, key).Commits; len(got) != 0 {
				t.Errorf("%s: Commits = %v, want none", key, got)
			}
		}
	})

	t.Run("60 adjacent commits are cut into ranges of 25, 25, and 10", func(t *testing.T) {
		t.Parallel()
		var l Listing
		for i := range 60 {
			l.History = append(l.History, commit(fmt.Sprintf("%040x", i+1), i, 0))
		}
		units, _ := BuildUnits(unitsRepo, l, nil)
		if len(units) != 3 {
			t.Fatalf("got %d units, want 3: %v", len(units), unitKeys(units))
		}
		for i, want := range []struct{ first, last, n int }{{1, 25, 25}, {26, 50, 25}, {51, 60, 10}} {
			u := units[i]
			first, last := fmt.Sprintf("%040x", want.first), fmt.Sprintf("%040x", want.last)
			if u.Kind != KindCommits || len(u.Commits) != want.n || u.Commits[0] != first || u.Commits[want.n-1] != last {
				t.Errorf("range %d = %d commits %s..%s, want %d commits %s..%s", i, len(u.Commits), u.Commits[0], u.Commits[len(u.Commits)-1], want.n, first, last)
			}
			if wantKey := "commits-" + first[:12] + "-" + last[:12]; u.Key != wantKey {
				t.Errorf("range %d key = %q, want %q", i, u.Key, wantKey)
			}
			if wantTitle := fmt.Sprintf("%d commits %s..%s", want.n, first[:7], last[:7]); u.Title != wantTitle {
				t.Errorf("range %d title = %q, want %q", i, u.Title, wantTitle)
			}
			if u.Source != unitsRepo+"@"+last {
				t.Errorf("range %d source = %q, want the full SHA of its last commit", i, u.Source)
			}
		}
	})

	t.Run("a pull request commit between two commits splits them into two ranges", func(t *testing.T) {
		t.Parallel()
		l := Listing{
			History: []github.HistoryCommit{commit("a1", 1, 0), commit("p1", 2, 7), commit("a2", 3, 0)},
			PRs:     []github.MergedPR{{Number: 7, MergedAt: day(2)}},
		}
		units, _ := BuildUnits(unitsRepo, l, nil)
		if got := unitKeys(units); !slices.Equal(got, []string{"commits-a1-a1", "pr-7", "commits-a2-a2"}) {
			t.Errorf("units = %v", got)
		}
		if got := units[0].Title; got != "1 commit a1..a1" {
			t.Errorf("title = %q, want the singular for one commit", got)
		}
	})

	t.Run("an empty listing yields no unit", func(t *testing.T) {
		t.Parallel()
		units, bots := BuildUnits(unitsRepo, Listing{}, nil)
		if len(units) != 0 || bots != 0 {
			t.Errorf("BuildUnits = %v, %d, want no unit and 0", units, bots)
		}
	})

	t.Run("each kind names its source", func(t *testing.T) {
		t.Parallel()
		l := Listing{
			History: []github.HistoryCommit{commit("a1", 1, 0)},
			PRs:     []github.MergedPR{{Number: 7, MergedAt: day(1)}},
			Issues:  []github.ClosedIssue{{Number: 3, ClosedAt: day(1)}},
		}
		docs := []DocChunk{{Path: "docs/adr/0001-x.md", Index: 1, Count: 1, Text: "x\n"}}
		units, _ := BuildUnits(unitsRepo, l, docs)
		for key, want := range map[string]string{
			"issue-3":       "acme/widgets#3",
			"pr-7":          "acme/widgets#7",
			"commits-a1-a1": "acme/widgets@a1",
			docs[0].key():   "acme/widgets:docs/adr/0001-x.md",
		} {
			if got := unitByKey(t, units, key).Source; got != want {
				t.Errorf("%s: Source = %q, want %q", key, got, want)
			}
		}
	})
}

// TestBuildUnits_Order covers the order of the units: by the newest commit a
// unit holds, by time for a unit without a commit in the history, and with the
// document chunks at the end.
func TestBuildUnits_Order(t *testing.T) {
	t.Parallel()
	l := Listing{
		History: []github.HistoryCommit{
			commit("a1", 10, 0),  // 0: a range of its own
			commit("p1", 11, 20), // 1: pull request 20, which closes issue 5
			commit("p2", 12, 21), // 2: pull request 21
			commit("p3", 13, 20), // 3: pull request 20 again, its newest commit
			commit("fix", 14, 0), // 4: closes issue 6
		},
		PRs: []github.MergedPR{
			{Number: 20, MergedAt: day(13), ClosesIssues: []int{5}},
			{Number: 21, MergedAt: day(12)},
			// No commit of pull request 22 is on the default branch any more.
			{Number: 22, MergedAt: day(12).Add(time.Hour)},
			// Merged before the first commit of the history.
			{Number: 23, MergedAt: day(1)},
		},
		Issues: []github.ClosedIssue{
			{Number: 5, ClosedAt: day(13)},
			{Number: 6, ClosedAt: day(14), CloserSHA: "fix"},
			// Closed by hand between p2 and p3.
			{Number: 7, ClosedAt: day(12).Add(2 * time.Hour)},
			// Closed by hand before the first commit of the history.
			{Number: 8, ClosedAt: day(2)},
		},
	}
	docs := []DocChunk{
		{Path: "docs/b.md", Index: 1, Count: 1, Text: "b\n"},
		{Path: "docs/a.md", Index: 2, Count: 2, Text: "a2\n"},
		{Path: "docs/a.md", Index: 1, Count: 2, Text: "a1\n"},
	}

	units, _ := BuildUnits(unitsRepo, l, docs)
	want := []string{
		"issue-8", // placed by time before every commit, issue before pull request
		"pr-23",
		"commits-a1-a1",
		"pr-21",   // newest commit at index 2
		"issue-7", // placed by time at index 2, after the unit placed there by commit
		"pr-22",
		"issue-5", // newest commit of pull request 20 at index 3
		"issue-6", // closer commit at index 4
		docs[2].key(),
		docs[1].key(),
		docs[0].key(),
	}
	if got := unitKeys(units); !slices.Equal(got, want) {
		t.Errorf("order = %v\nwant    %v", got, want)
	}
	if got := units[len(units)-3].Title; got != "docs/a.md (chunk 1 of 2)" {
		t.Errorf("document title = %q", got)
	}
	if units[len(units)-1].Doc == nil || units[len(units)-1].Doc.Text != "b\n" {
		t.Errorf("the last unit must carry its chunk: %+v", units[len(units)-1])
	}
}
