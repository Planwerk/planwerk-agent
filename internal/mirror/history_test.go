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

// readHistoryFile returns history.jsonl of dir and its file info.
func readHistoryFile(t *testing.T, dir string) (string, os.FileInfo) {
	t.Helper()
	path := filepath.Join(dir, historyFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the history: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat of the history: %v", err)
	}
	return string(data), info
}

// pageCounter counts the pages of the history a run asks GitHub for.
type pageCounter struct {
	*githubtest.Fake
	pages int
}

func (g *pageCounter) DefaultBranchHistoryUntil(owner, name string, done func(page []github.HistoryCommit, total int) bool) ([]github.HistoryCommit, error) {
	return g.Fake.DefaultBranchHistoryUntil(owner, name, func(page []github.HistoryCommit, total int) bool {
		g.pages++
		return done(page, total)
	})
}

const firstHistory = `{"sha":"c1","committed_at":"2026-03-01T10:00:00Z","pr":0}
{"sha":"c2","committed_at":"2026-03-02T10:00:00Z","pr":9}
{"sha":"c3","committed_at":"2026-03-03T10:00:00Z","pr":0}
`

func TestHistory_WritesAppendsAndLeavesAlone(t *testing.T) {
	root := t.TempDir()
	gh := testGitHub()
	s := newSyncer(gh, &fakeWiki{head: testWikiHead})
	dir := mirrorDir(root)

	out, err := runSync(t, s, Options{Root: root})
	if err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if got, _ := readHistoryFile(t, dir); got != firstHistory {
		t.Errorf("history.jsonl =\n%s\nwant, oldest first,\n%s", got, firstHistory)
	}
	if !strings.Contains(out, "history: 3 new commits, 3 in the mirror\n") {
		t.Errorf("output = %q", out)
	}

	// No new commit: the file is not written again.
	_, before := readHistoryFile(t, dir)
	out, err = runSync(t, s, Options{Root: root})
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	got, after := readHistoryFile(t, dir)
	if got != firstHistory || !os.SameFile(before, after) || !after.ModTime().Equal(before.ModTime()) {
		t.Errorf("a run without a new commit must not write history.jsonl")
	}
	if !strings.Contains(out, "history: 0 new commits, 3 in the mirror\n") {
		t.Errorf("output = %q", out)
	}

	// Two new commits are appended; the offset of a commit time is written as
	// UTC.
	gh.History = append(gh.History,
		github.HistoryCommit{SHA: "c4", CommittedAt: time.Date(2026, 3, 4, 12, 0, 0, 0, time.FixedZone("CEST", 2*60*60)), PRNumber: 12},
		github.HistoryCommit{SHA: "c5", CommittedAt: time.Date(2026, 3, 5, 10, 0, 0, 0, time.UTC)})
	out, err = runSync(t, s, Options{Root: root})
	if err != nil {
		t.Fatalf("third Run: %v", err)
	}
	want := firstHistory + `{"sha":"c4","committed_at":"2026-03-04T10:00:00Z","pr":12}` + "\n" + `{"sha":"c5","committed_at":"2026-03-05T10:00:00Z","pr":0}` + "\n"
	if got, _ := readHistoryFile(t, dir); got != want {
		t.Errorf("history.jsonl =\n%s\nwant the old lines and the two new commits:\n%s", got, want)
	}
	if !strings.Contains(out, "history: 2 new commits, 5 in the mirror\n") {
		t.Errorf("output = %q", out)
	}
}

// TestHistory_RewrittenBranchReplacesTheFile covers a default branch that no
// longer holds the mirrored head, as after a force push.
func TestHistory_RewrittenBranchReplacesTheFile(t *testing.T) {
	commit := func(sha string, day int) github.HistoryCommit {
		return github.HistoryCommit{SHA: sha, CommittedAt: time.Date(2026, 3, day, 10, 0, 0, 0, time.UTC)}
	}
	for _, tc := range []struct {
		name     string
		pageSize int
		history  []github.HistoryCommit
		want     string
	}{
		{"a branch read in one page", 0,
			[]github.HistoryCommit{commit("c1", 1), commit("d2", 6)},
			`{"sha":"c1","committed_at":"2026-03-01T10:00:00Z","pr":0}
{"sha":"d2","committed_at":"2026-03-06T10:00:00Z","pr":0}
`},
		// The two commits of the first page and the three mirrored ones are as
		// many as the branch holds. The mirrored head is gone, so that proves
		// nothing, and the read goes on to the end.
		{"commit counts that add up on the first page", 2,
			[]github.HistoryCommit{commit("c1", 1), commit("d2", 6), commit("d3", 7), commit("d4", 8), commit("d5", 9)},
			`{"sha":"c1","committed_at":"2026-03-01T10:00:00Z","pr":0}
{"sha":"d2","committed_at":"2026-03-06T10:00:00Z","pr":0}
{"sha":"d3","committed_at":"2026-03-07T10:00:00Z","pr":0}
{"sha":"d4","committed_at":"2026-03-08T10:00:00Z","pr":0}
{"sha":"d5","committed_at":"2026-03-09T10:00:00Z","pr":0}
`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			gh := testGitHub()
			s := newSyncer(gh, &fakeWiki{head: testWikiHead})
			if _, err := runSync(t, s, Options{Root: root}); err != nil {
				t.Fatalf("first Run: %v", err)
			}

			gh.History, gh.HistoryPageSize = tc.history, tc.pageSize
			logs := captureLogs(t)
			out, err := runSync(t, s, Options{Root: root})
			if err != nil {
				t.Fatalf("second Run: %v", err)
			}
			if got, _ := readHistoryFile(t, mirrorDir(root)); got != tc.want {
				t.Errorf("history.jsonl =\n%s\nwant the rewritten branch alone:\n%s", got, tc.want)
			}
			if want := fmt.Sprintf("history: replaced, %d commits in the mirror\n", len(tc.history)); !strings.Contains(out, want) {
				t.Errorf("output = %q, want the line %q", out, want)
			}
			if !strings.Contains(logs.String(), "the default branch no longer holds the mirrored head; replacing the mirrored history") {
				t.Errorf("log %q lacks the warning", logs)
			}
		})
	}
}

// TestHistory_CommitsBehindTheMirroredHeadAreReadWithoutTheWholeHistory covers
// a branch that is merged with a merge commit m. GitHub lists by commit date,
// so the branch's commits f1 and f2, dated before the mirrored head c6, come
// after c6 in the listing. The run reads past the head until the commits add
// up to the commit count of the branch, and no further: the page of the three
// oldest commits is not read.
func TestHistory_CommitsBehindTheMirroredHeadAreReadWithoutTheWholeHistory(t *testing.T) {
	root := t.TempDir()
	at := func(day, hour int) time.Time { return time.Date(2026, 3, day, hour, 0, 0, 0, time.UTC) }
	gh := &pageCounter{Fake: testGitHub()}
	gh.HistoryPageSize = 3
	gh.History = []github.HistoryCommit{
		{SHA: "c1", CommittedAt: at(1, 10)},
		{SHA: "c2", CommittedAt: at(2, 10), PRNumber: 9},
		{SHA: "c3", CommittedAt: at(3, 10)},
		{SHA: "c4", CommittedAt: at(4, 10)},
		{SHA: "c5", CommittedAt: at(5, 10)},
		{SHA: "c6", CommittedAt: at(6, 10)},
	}
	s := newSyncer(gh.Fake, &fakeWiki{head: testWikiHead})
	s.GitHub = gh
	if _, err := runSync(t, s, Options{Root: root}); err != nil {
		t.Fatalf("first Run: %v", err)
	}
	dir := mirrorDir(root)

	gh.History = []github.HistoryCommit{
		{SHA: "c1", CommittedAt: at(1, 10)},
		{SHA: "c2", CommittedAt: at(2, 10), PRNumber: 9},
		{SHA: "c3", CommittedAt: at(3, 10)},
		{SHA: "c4", CommittedAt: at(4, 10)},
		{SHA: "f1", CommittedAt: at(4, 12)},
		{SHA: "c5", CommittedAt: at(5, 10)},
		{SHA: "f2", CommittedAt: at(5, 12)},
		{SHA: "c6", CommittedAt: at(6, 10)},
		{SHA: "m", CommittedAt: at(7, 10)},
	}
	gh.pages = 0
	out, err := runSync(t, s, Options{Root: root})
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	want := `{"sha":"c1","committed_at":"2026-03-01T10:00:00Z","pr":0}
{"sha":"c2","committed_at":"2026-03-02T10:00:00Z","pr":9}
{"sha":"c3","committed_at":"2026-03-03T10:00:00Z","pr":0}
{"sha":"c4","committed_at":"2026-03-04T10:00:00Z","pr":0}
{"sha":"f1","committed_at":"2026-03-04T12:00:00Z","pr":0}
{"sha":"c5","committed_at":"2026-03-05T10:00:00Z","pr":0}
{"sha":"f2","committed_at":"2026-03-05T12:00:00Z","pr":0}
{"sha":"c6","committed_at":"2026-03-06T10:00:00Z","pr":0}
{"sha":"m","committed_at":"2026-03-07T10:00:00Z","pr":0}
`
	if got, _ := readHistoryFile(t, dir); got != want {
		t.Errorf("history.jsonl =\n%s\nwant every commit of the branch in GitHub's order:\n%s", got, want)
	}
	if !strings.Contains(out, "history: 3 new commits, 9 in the mirror\n") {
		t.Errorf("output = %q, want the merge commit and the two commits behind the head counted", out)
	}
	if gh.pages != 2 {
		t.Errorf("the run read %d pages of the history, want the 2 that hold the new commits", gh.pages)
	}

	// The next run finds every commit of its first page in the mirror and
	// leaves the file alone.
	_, before := readHistoryFile(t, dir)
	gh.pages = 0
	out, err = runSync(t, s, Options{Root: root})
	if err != nil {
		t.Fatalf("third Run: %v", err)
	}
	if _, after := readHistoryFile(t, dir); !after.ModTime().Equal(before.ModTime()) || !strings.Contains(out, "history: 0 new commits, 9 in the mirror\n") {
		t.Errorf("a run without a new commit must not write history.jsonl: output %q", out)
	}
	if gh.pages != 1 {
		t.Errorf("the run read %d pages of the history, want 1", gh.pages)
	}
}

func TestHistory_FailedReadLeavesTheFile(t *testing.T) {
	root := t.TempDir()
	gh := testGitHub()
	s := newSyncer(gh, &fakeWiki{head: testWikiHead})
	if _, err := runSync(t, s, Options{Root: root}); err != nil {
		t.Fatalf("first Run: %v", err)
	}
	dir := mirrorDir(root)
	_, before := readHistoryFile(t, dir)

	gh.HistoryErr = errors.New("API rate limit exceeded")
	gh.History = append(gh.History, github.HistoryCommit{SHA: "c4", CommittedAt: time.Date(2026, 3, 4, 10, 0, 0, 0, time.UTC)})
	out, err := runSync(t, s, Options{Root: root})
	if err == nil || !strings.HasPrefix(err.Error(), "history: ") || !errors.Is(err, gh.HistoryErr) {
		t.Fatalf("err = %v, want the read's error under the adapter's name", err)
	}
	got, after := readHistoryFile(t, dir)
	if got != firstHistory || !os.SameFile(before, after) {
		t.Errorf("a failed read must leave history.jsonl as it was:\n%s", got)
	}
	if strings.Contains(out, "history:") {
		t.Errorf("output = %q, want no history line for a failed adapter", out)
	}
}

// TestHistory_EmptyRepository covers a repository without a default branch:
// the history is an empty file, and the run finishes.
func TestHistory_EmptyRepository(t *testing.T) {
	root := t.TempDir()
	gh := testGitHub()
	gh.History = nil
	out, err := runSync(t, newSyncer(gh, &fakeWiki{head: testWikiHead}), Options{Root: root})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out, "history: 0 new commits, 0 in the mirror\n") {
		t.Errorf("output = %q", out)
	}
	commits, err := ReadHistory(mirrorDir(root))
	if err != nil || len(commits) != 0 {
		t.Errorf("ReadHistory = %v, %v, want no commit", commits, err)
	}
}

func TestReadHistory(t *testing.T) {
	t.Run("a missing file holds no commit", func(t *testing.T) {
		commits, err := ReadHistory(t.TempDir())
		if commits != nil || err != nil {
			t.Errorf("ReadHistory = %v, %v, want nil, nil", commits, err)
		}
	})

	t.Run("the lines come back as commits", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, historyFileName), []byte(firstHistory), 0o600); err != nil {
			t.Fatal(err)
		}
		commits, err := ReadHistory(dir)
		if err != nil {
			t.Fatalf("ReadHistory: %v", err)
		}
		want := []github.HistoryCommit{
			{SHA: "c1", CommittedAt: time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)},
			{SHA: "c2", CommittedAt: time.Date(2026, 3, 2, 10, 0, 0, 0, time.UTC), PRNumber: 9},
			{SHA: "c3", CommittedAt: time.Date(2026, 3, 3, 10, 0, 0, 0, time.UTC)},
		}
		if !slices.Equal(commits, want) {
			t.Errorf("commits = %+v\nwant %+v", commits, want)
		}
	})

	for _, tc := range []struct{ name, second string }{
		{"a line that is not JSON", "not json"},
		{"a commit time that is not RFC 3339", `{"sha":"c2","committed_at":"yesterday","pr":0}`},
		{"an empty line", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, historyFileName)
			content := `{"sha":"c1","committed_at":"2026-03-01T10:00:00Z","pr":0}` + "\n" + tc.second + "\n" + `{"sha":"c3","committed_at":"2026-03-03T10:00:00Z","pr":0}` + "\n"
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			commits, err := ReadHistory(dir)
			if want := "parsing " + path + " line 2: "; err == nil || !strings.HasPrefix(err.Error(), want) || commits != nil {
				t.Errorf("ReadHistory = %v, %v, want an error that starts with %q", commits, err, want)
			}
		})
	}
}

// TestHistory_BrokenFileFailsTheAdapter covers a history file that does not
// parse: the adapter fails with the line, and leaves the file for --full.
func TestHistory_BrokenFileFailsTheAdapter(t *testing.T) {
	root := t.TempDir()
	dir := mirrorDir(root)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, historyFileName), []byte("not json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gh := testGitHub()
	_, err := runSync(t, newSyncer(gh, &fakeWiki{head: testWikiHead}), Options{Root: root})
	if err == nil || !strings.Contains(err.Error(), "history: parsing ") || !strings.Contains(err.Error(), " line 1: ") {
		t.Errorf("err = %v, want the parse error under the adapter's name", err)
	}
	if gh.Count("DefaultBranchHistoryUntil") != 0 {
		t.Error("a history that does not parse must not be read past")
	}
	if got, _ := readHistoryFile(t, dir); got != "not json\n" {
		t.Errorf("the broken file must stay as it was: %q", got)
	}
}
