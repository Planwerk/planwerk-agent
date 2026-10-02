package mirror

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/planwerk/planwerk-agent/internal/github"
	"github.com/planwerk/planwerk-agent/internal/github/githubtest"
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

// fixedNow is the clock of every test run, and fixedNowStamp that time as the
// state stores it.
var fixedNow = time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC)

const fixedNowStamp = "2026-10-02T09:00:00Z"

// openIssueUpdated is the update time of the oldest of threeItems.
const openIssueUpdated = "2026-03-01T09:00:00Z"

const testWikiHead = "1a2b3c4d5e6f70819293a4b5c6d7e8f901234567"

// wikiCall is one recorded clone of a fakeWiki.
type wikiCall struct{ repo, ref, dest string }

// fakeWiki is a scripted CloneWikiFn. A successful clone writes Home.md with
// the head into dest; a failing one leaves dest alone, as patterns.MirrorWiki
// does.
type fakeWiki struct {
	head  string
	err   error
	calls []wikiCall
}

func (w *fakeWiki) clone(repo, ref, dest string) (string, error) {
	w.calls = append(w.calls, wikiCall{repo, ref, dest})
	if w.err != nil {
		return "", w.err
	}
	if err := os.MkdirAll(dest, 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dest, "Home.md"), []byte(w.head), 0o600); err != nil {
		return "", err
	}
	return w.head, nil
}

// testItem returns an item of the given kind whose update time is updatedAt.
func testItem(kind string, number int, updatedAt string) *github.Item {
	it := fixtureIssue()
	if kind == github.ItemKindPull {
		it = fixturePull()
	}
	it.Number = number
	it.Title = fmt.Sprintf("Item %d", number)
	it.UpdatedAt = updatedAt
	return it
}

// testGitHub returns a fake that serves items and lists them in the order of
// their update time, honoring since the way GitHub does: an item updated at
// that time is listed.
func testGitHub(items ...*github.Item) *githubtest.Fake {
	gh := &githubtest.Fake{
		Items: map[int]*github.Item{},
		History: []github.HistoryCommit{
			{SHA: "c1", CommittedAt: time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)},
			{SHA: "c2", CommittedAt: time.Date(2026, 3, 2, 10, 0, 0, 0, time.UTC), PRNumber: 9},
			{SHA: "c3", CommittedAt: time.Date(2026, 3, 3, 10, 0, 0, 0, time.UTC)},
		},
	}
	for _, it := range items {
		gh.Items[it.Number] = it
		gh.UpdatedItems = append(gh.UpdatedItems, github.UpdatedItem{Number: it.Number, UpdatedAt: it.UpdatedAt, IsPull: it.Kind == github.ItemKindPull})
	}
	gh.ListUpdatedItemsFn = func(_, _, since string) ([]github.UpdatedItem, error) {
		if gh.UpdatedItemsErr != nil {
			return nil, gh.UpdatedItemsErr
		}
		var out []github.UpdatedItem
		for _, e := range gh.UpdatedItems {
			if e.UpdatedAt >= since {
				out = append(out, e)
			}
		}
		return out, nil
	}
	return gh
}

// threeItems are an open issue, a closed issue, and a merged pull request, in
// the order of their update time.
func threeItems() []*github.Item {
	open := testItem(github.ItemKindIssue, 3, openIssueUpdated)
	open.State, open.StateReason, open.ClosedAt, open.ClosedByPRs = "open", "", "", nil
	return []*github.Item{
		open,
		testItem(github.ItemKindIssue, 7, "2026-03-03T10:00:00Z"),
		testItem(github.ItemKindPull, 9, "2026-03-04T10:00:00Z"),
	}
}

// runSync runs s for acme/widgets under root and returns what it printed.
func runSync(t *testing.T, s *Syncer, opts Options) (string, error) {
	t.Helper()
	if opts.RepoRef == "" {
		opts.RepoRef = testRepo
	}
	var out bytes.Buffer
	err := s.Run(&out, opts)
	return out.String(), err
}

// newSyncer returns a Syncer over gh, wiki, and the fixed clock.
func newSyncer(gh *githubtest.Fake, wiki *fakeWiki) *Syncer {
	return &Syncer{GitHub: gh, CloneWiki: wiki.clone, Now: func() time.Time { return fixedNow }}
}

// readState decodes state.json in dir.
func readState(t *testing.T, dir string) State {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, stateFileName))
	if err != nil {
		t.Fatalf("reading the state: %v", err)
	}
	var st State
	if err := json.Unmarshal(raw, &st); err != nil {
		t.Fatalf("decoding the state: %v", err)
	}
	return st
}

// mirrorDir returns the mirror directory of acme/widgets under root.
func mirrorDir(root string) string {
	return filepath.Join(root, "acme", "widgets")
}

// tree returns the content of every file under the given paths of dir, keyed
// by the slash path relative to dir.
func tree(t *testing.T, dir string, paths ...string) map[string]string {
	t.Helper()
	files := map[string]string{}
	for _, p := range paths {
		err := filepath.WalkDir(filepath.Join(dir, p), func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(dir, path)
			files[filepath.ToSlash(rel)] = string(data)
			return err
		})
		if err != nil {
			t.Fatalf("walking %s: %v", p, err)
		}
	}
	return files
}

func TestDir(t *testing.T) {
	root := t.TempDir()
	got, err := Dir(root, "Acme", "Widgets")
	if err != nil {
		t.Fatalf("Dir: %v", err)
	}
	if want := filepath.Join(root, "acme", "widgets"); got != want {
		t.Errorf("Dir = %q, want %q (owner and name in lowercase)", got, want)
	}

	// "." and ".." pass the repository reference parser and would leave the
	// root.
	for _, tc := range []struct{ owner, name, want string }{
		{"..", "x", "invalid repository ../x for a mirror directory"},
		{"acme", ".", "invalid repository acme/. for a mirror directory"},
		{".", "x", "invalid repository ./x for a mirror directory"},
		{"acme", "..", "invalid repository acme/.. for a mirror directory"},
	} {
		if dir, err := Dir(root, tc.owner, tc.name); err == nil || err.Error() != tc.want {
			t.Errorf("Dir(%q, %q) = %q, %v, want the error %q", tc.owner, tc.name, dir, err, tc.want)
		}
	}
}

func TestDefaultRoot(t *testing.T) {
	t.Run("under the user cache directory", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("XDG_CACHE_HOME", home)
		base, err := os.UserCacheDir()
		if err != nil {
			t.Fatalf("os.UserCacheDir: %v", err)
		}
		got, err := DefaultRoot()
		if want := filepath.Join(base, "planwerk-agent", "brain"); err != nil || got != want {
			t.Errorf("DefaultRoot = %q, %v, want %q", got, err, want)
		}
	})

	// The mirror holds GitHub's text unchanged, so there is no fallback to the
	// temp directory, which every local user can write to.
	t.Run("an error without a user cache directory", func(t *testing.T) {
		t.Setenv("HOME", "")
		t.Setenv("XDG_CACHE_HOME", "")
		if _, err := os.UserCacheDir(); err == nil {
			t.Skip("the platform resolves a cache directory without HOME")
		}
		got, err := DefaultRoot()
		if err == nil || got != "" || !strings.HasPrefix(err.Error(), "resolving the user cache directory for the mirror: ") {
			t.Errorf("DefaultRoot = %q, %v, want an error and no path", got, err)
		}
	})
}

func TestItemPath(t *testing.T) {
	dir := filepath.Join("m", "acme", "widgets")
	if got, want := ItemPath(dir, github.ItemKindIssue, 7), filepath.Join(dir, "issues", "7.md"); got != want {
		t.Errorf("issue path = %q, want %q", got, want)
	}
	if got, want := ItemPath(dir, github.ItemKindPull, 9), filepath.Join(dir, "pulls", "9.md"); got != want {
		t.Errorf("pull request path = %q, want %q", got, want)
	}
}

func TestItemFiles(t *testing.T) {
	dir := t.TempDir()
	if files, err := ItemFiles(dir, github.ItemKindIssue); files != nil || err != nil {
		t.Errorf("ItemFiles of a missing directory = %v, %v, want nil, nil", files, err)
	}

	issues := filepath.Join(dir, "issues")
	pulls := filepath.Join(dir, "pulls")
	// Beside the items, issues/ holds what an interrupted write leaves behind,
	// a file of another extension, and a directory with the items' extension.
	for _, d := range []string{issues, pulls, filepath.Join(issues, "old.md")} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{
		filepath.Join(issues, "7.md"), filepath.Join(issues, "10.md"),
		filepath.Join(issues, "3.md.1234.tmp"), filepath.Join(issues, "notes.txt"),
		filepath.Join(pulls, "9.md"),
	} {
		if err := os.WriteFile(f, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		kind string
		want []string
	}{
		{github.ItemKindIssue, []string{filepath.Join(issues, "10.md"), filepath.Join(issues, "7.md")}},
		{github.ItemKindPull, []string{filepath.Join(pulls, "9.md")}},
	} {
		got, err := ItemFiles(dir, tc.kind)
		if err != nil || strings.Join(got, "\n") != strings.Join(tc.want, "\n") {
			t.Errorf("ItemFiles(%s) = %v, %v, want %v", tc.kind, got, err, tc.want)
		}
	}

	t.Run("a directory that cannot be read", func(t *testing.T) {
		dir := t.TempDir()
		blocked := filepath.Join(dir, "pulls")
		if err := os.WriteFile(blocked, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		files, err := ItemFiles(dir, github.ItemKindPull)
		if err == nil || files != nil || !strings.HasPrefix(err.Error(), "reading "+blocked+": ") {
			t.Errorf("ItemFiles = %v, %v, want an error that names the directory", files, err)
		}
	})
}

func TestLoadState(t *testing.T) {
	writeState := func(t *testing.T, content string) string {
		t.Helper()
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, stateFileName), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return dir
	}

	t.Run("a missing file is a fresh state", func(t *testing.T) {
		dir := t.TempDir()
		st, exists, err := LoadState(dir, testRepo)
		if err != nil || exists {
			t.Fatalf("LoadState = %+v, %v, %v, want a fresh state", st, exists, err)
		}
		if want := (State{Version: 1, Repo: testRepo}); *st != want {
			t.Errorf("state = %+v, want %+v", *st, want)
		}
		if entries, _ := os.ReadDir(dir); len(entries) != 0 {
			t.Errorf("LoadState must only read; the directory now holds %d entries", len(entries))
		}
	})

	t.Run("a saved state loads back", func(t *testing.T) {
		dir := t.TempDir()
		want := State{Version: 1, Repo: testRepo, SyncedAt: "2026-10-02T09:00:00Z",
			Items: ItemsState{Cursor: "2026-10-02T08:17:02Z"}, Wiki: WikiState{Repo: testRepo, Commit: "1a2b3c4"}}
		if err := want.Save(dir); err != nil {
			t.Fatalf("Save: %v", err)
		}
		// The repository is compared without case, as GitHub does.
		st, exists, err := LoadState(dir, "Acme/Widgets")
		if err != nil || !exists || *st != want {
			t.Errorf("LoadState = %+v, %v, %v, want %+v", st, exists, err, want)
		}
		info, err := os.Stat(filepath.Join(dir, stateFileName))
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Errorf("state.json mode = %v, %v, want 0600", info.Mode().Perm(), err)
		}
		if entries, err := os.ReadDir(dir); err != nil || len(entries) != 1 {
			t.Errorf("the directory holds %d entries after a save (%v), want state.json alone: the temporary file must be gone", len(entries), err)
		}
		raw, _ := os.ReadFile(filepath.Join(dir, stateFileName))
		for _, key := range []string{`"version": 1`, `"repo": "acme/widgets"`, `"synced_at"`, `"items"`, `"cursor"`, `"wiki"`, `"commit"`} {
			if !strings.Contains(string(raw), key) {
				t.Errorf("state.json lacks %s:\n%s", key, raw)
			}
		}
	})

	for _, tc := range []struct{ name, content, repo, want string }{
		{"malformed JSON", "{", testRepo, "parsing "},
		{"another version", `{"version": 2, "repo": "acme/widgets"}`, testRepo, "unsupported mirror state version 2 in "},
		{"another repository", `{"version": 1, "repo": "acme/widgets"}`, "acme/gadgets", " holds the mirror of acme/widgets, not acme/gadgets"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := writeState(t, tc.content)
			st, exists, err := LoadState(dir, tc.repo)
			if err == nil || st != nil || exists {
				t.Fatalf("LoadState = %+v, %v, %v, want an error alone", st, exists, err)
			}
			if !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), dir) {
				t.Errorf("err = %q, want it to contain %q and the directory", err, tc.want)
			}
		})
	}

	t.Run("another version names the rebuild", func(t *testing.T) {
		_, _, err := LoadState(writeState(t, `{"version": 2}`), testRepo)
		if err == nil || !strings.HasSuffix(err.Error(), "; run brain sync --full to rebuild") {
			t.Errorf("err = %v, want it to name brain sync --full", err)
		}
	})
}

// TestSave_WritesThroughNoPlantedFile covers a link someone placed where a
// temporary file with a fixed name would be: the save leaves the link's
// target alone and writes a regular file of its own.
func TestSave_WritesThroughNoPlantedFile(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(t.TempDir(), "target")
	if err := os.WriteFile(target, []byte("kept"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, stateFileName+".tmp")); err != nil {
		t.Skipf("no symbolic links here: %v", err)
	}

	st := State{Version: 1, Repo: testRepo}
	if err := st.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if data, err := os.ReadFile(target); err != nil || string(data) != "kept" {
		t.Errorf("the link's target = %q, %v, want it untouched", data, err)
	}
	info, err := os.Lstat(filepath.Join(dir, stateFileName))
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		t.Errorf("state.json: mode %v, %v, want a regular file of mode 0600", info.Mode(), err)
	}
}

// TestWriteFileAtomic_ConcurrentWritersOfOnePath covers two runs that write
// one file at the same time: every write succeeds, the file holds all of one
// writer's data, and no temporary file stays behind.
func TestWriteFileAtomic_ConcurrentWritersOfOnePath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "7.md")
	contents := []string{strings.Repeat("a", 1<<16), strings.Repeat("b", 1<<16)}
	errs := make([]error, len(contents))
	var wg sync.WaitGroup
	for i, content := range contents {
		wg.Go(func() {
			for range 100 {
				if errs[i] = writeFileAtomic(path, []byte(content)); errs[i] != nil {
					return
				}
			}
		})
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("writer %d: %v", i, err)
		}
	}
	got, err := os.ReadFile(path)
	if err != nil || (string(got) != contents[0] && string(got) != contents[1]) {
		t.Errorf("the file holds %d bytes (%v), want all of one writer's data", len(got), err)
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 1 {
		t.Errorf("the directory holds %d entries (%v), want the file alone", len(entries), err)
	}
}

func TestSave_MissingDirectoryIsError(t *testing.T) {
	st := State{Version: 1, Repo: testRepo}
	if err := st.Save(filepath.Join(t.TempDir(), "absent")); err == nil || !strings.HasPrefix(err.Error(), "writing ") {
		t.Errorf("err = %v, want a write error", err)
	}
}

func TestRun_FirstSyncWritesTheMirror(t *testing.T) {
	root := t.TempDir()
	gh := testGitHub(threeItems()...)
	out, err := runSync(t, newSyncer(gh, &fakeWiki{head: testWikiHead}), Options{Root: root})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	dir := mirrorDir(root)
	want := "Mirror of acme/widgets at " + dir + "\n" +
		"items: 3 listed, 3 fetched, 3 in the mirror (2 issues, 1 pull requests)\n" +
		"history: 3 new commits, 3 in the mirror\n" +
		"wiki: acme/widgets.wiki at 1a2b3c4\n"
	if out != want {
		t.Errorf("output =\n%s\nwant\n%s", out, want)
	}

	for _, d := range []string{dir, filepath.Join(dir, "issues"), filepath.Join(dir, "pulls")} {
		if info, err := os.Stat(d); err != nil || info.Mode().Perm() != 0o700 {
			t.Errorf("%s: mode %v, %v, want a directory of mode 0700", d, info.Mode().Perm(), err)
		}
	}
	for _, f := range []string{"issues/3.md", "issues/7.md", "pulls/9.md", "history.jsonl", "state.json"} {
		if info, err := os.Stat(filepath.Join(dir, filepath.FromSlash(f))); err != nil || info.Mode().Perm() != 0o600 {
			t.Errorf("%s: mode %v, %v, want a file of mode 0600", f, info.Mode().Perm(), err)
		}
	}
	// The file is the rendered item.
	got, err := os.ReadFile(ItemPath(dir, github.ItemKindPull, 9))
	if err != nil || !bytes.Equal(got, Render(testRepo, gh.Items[9])) {
		t.Errorf("pulls/9.md is not the rendered pull request (%v):\n%s", err, got)
	}

	wantState := State{Version: 1, Repo: testRepo, SyncedAt: fixedNowStamp,
		Items: ItemsState{Cursor: "2026-03-04T10:00:00Z"}, Wiki: WikiState{Repo: testRepo, Commit: testWikiHead}}
	if st := readState(t, dir); st != wantState {
		t.Errorf("state = %+v\nwant %+v", st, wantState)
	}
}

// TestRun_RemovesTheTemporaryFilesOfAnInterruptedRun covers a run that died
// between creating a temporary file and renaming it. The file is a copy of
// GitHub's text under a name no later write uses, so the next run removes it
// once it is more than an hour old. A younger one can belong to a run that is
// still writing, and a file of the wiki is the wiki's own.
func TestRun_RemovesTheTemporaryFilesOfAnInterruptedRun(t *testing.T) {
	root := t.TempDir()
	s := newSyncer(testGitHub(threeItems()...), &fakeWiki{head: testWikiHead})
	if _, err := runSync(t, s, Options{Root: root}); err != nil {
		t.Fatalf("first Run: %v", err)
	}
	dir := mirrorDir(root)

	files := []struct {
		path     string
		age      time.Duration
		wantKept bool
	}{
		{"state.json.1234.tmp", 2 * time.Hour, false},
		{"history.jsonl.1234.tmp", 2 * time.Hour, false},
		{"issues/7.md.1234.tmp", 2 * time.Hour, false},
		{"pulls/9.md.1234.tmp", 2 * time.Hour, false},
		{"issues/7.md.5678.tmp", 30 * time.Minute, true},
		{"wiki/Draft.tmp", 2 * time.Hour, true},
	}
	for _, f := range files {
		path := filepath.Join(dir, filepath.FromSlash(f.path))
		if err := os.WriteFile(path, []byte("a copy of an item"), 0o600); err != nil {
			t.Fatal(err)
		}
		written := fixedNow.Add(-f.age)
		if err := os.Chtimes(path, written, written); err != nil {
			t.Fatal(err)
		}
	}
	// An item file of that age stays: the extension marks a leftover.
	item := ItemPath(dir, github.ItemKindIssue, 3)
	if err := os.Chtimes(item, fixedNow.Add(-2*time.Hour), fixedNow.Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}

	if _, err := runSync(t, s, Options{Root: root}); err != nil {
		t.Fatalf("second Run: %v", err)
	}
	for _, f := range files {
		_, err := os.Stat(filepath.Join(dir, filepath.FromSlash(f.path)))
		if kept := err == nil; kept != f.wantKept {
			t.Errorf("%s, written %v before the run: kept = %v (%v), want %v", f.path, f.age, kept, err, f.wantKept)
		}
	}
	if _, err := os.Stat(item); err != nil {
		t.Errorf("the run must leave an item file alone: %v", err)
	}
}

func TestRun_RejectsABadReference(t *testing.T) {
	root := t.TempDir()
	s := newSyncer(testGitHub(), &fakeWiki{head: testWikiHead})
	for _, tc := range []struct{ ref, want string }{
		{"x", "parsing repo ref: "},
		{"../x", "invalid repository ../x for a mirror directory"},
	} {
		out, err := runSync(t, s, Options{Root: root, RepoRef: tc.ref})
		if err == nil || !strings.HasPrefix(err.Error(), tc.want) {
			t.Errorf("Run(%q) err = %v, want it to start with %q", tc.ref, err, tc.want)
		}
		if out != "" {
			t.Errorf("Run(%q) printed %q before the error", tc.ref, out)
		}
	}
	if entries, _ := os.ReadDir(root); len(entries) != 0 {
		t.Errorf("a rejected reference must create nothing under the root, found %d entries", len(entries))
	}
}

// TestRun_FullRemovesOnlyThisMirror proves --full deletes the repository's
// mirror, and nothing else under the root, before the sync.
func TestRun_FullRemovesOnlyThisMirror(t *testing.T) {
	root := t.TempDir()
	gh := testGitHub(threeItems()...)
	s := newSyncer(gh, &fakeWiki{head: testWikiHead})
	if _, err := runSync(t, s, Options{Root: root}); err != nil {
		t.Fatalf("first Run: %v", err)
	}
	dir := mirrorDir(root)
	stale := filepath.Join(dir, "issues", "999.md")
	sibling := filepath.Join(root, "acme", "gadgets", "state.json")
	otherOwner := filepath.Join(root, "other", "widgets", "state.json")
	for _, f := range []string{stale, sibling, otherOwner} {
		if err := os.MkdirAll(filepath.Dir(f), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f, []byte("kept"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	before := gh.Count("GetItem")
	out, err := runSync(t, s, Options{Root: root, Full: true})
	if err != nil {
		t.Fatalf("full Run: %v", err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("a full sync must remove the file of an item GitHub no longer lists: %v", err)
	}
	for _, f := range []string{sibling, otherOwner} {
		if data, err := os.ReadFile(f); err != nil || string(data) != "kept" {
			t.Errorf("a full sync must leave %s alone: %q, %v", f, data, err)
		}
	}
	// The cursor went with the directory, so every item is fetched again.
	if got := gh.Count("GetItem") - before; got != 3 {
		t.Errorf("a full sync fetched %d items, want 3", got)
	}
	if !strings.Contains(out, "items: 3 listed, 3 fetched, 3 in the mirror") || !strings.Contains(out, "history: 3 new commits, 3 in the mirror") {
		t.Errorf("output = %q, want the lines of a first sync", out)
	}
}

// TestRun_FullKeepsTheMirrorWhenGitHubCannotBeReached proves --full deletes
// nothing before GitHub answered: a run without a network, or with an expired
// token, leaves the mirror it was asked to rebuild.
func TestRun_FullKeepsTheMirrorWhenGitHubCannotBeReached(t *testing.T) {
	root := t.TempDir()
	gh := testGitHub(threeItems()...)
	s := newSyncer(gh, &fakeWiki{head: testWikiHead})
	if _, err := runSync(t, s, Options{Root: root}); err != nil {
		t.Fatalf("first Run: %v", err)
	}
	dir := mirrorDir(root)
	paths := []string{"issues", "pulls", "wiki", "history.jsonl", "state.json"}
	before := tree(t, dir, paths...)

	gh.UpdatedItemsErr = errors.New("HTTP 401: Bad credentials")
	out, err := runSync(t, s, Options{Root: root, Full: true})
	if want := "reaching GitHub before deleting " + dir + ": "; err == nil || !strings.HasPrefix(err.Error(), want) || !errors.Is(err, gh.UpdatedItemsErr) {
		t.Fatalf("err = %v, want it to start with %q and wrap the listing's error", err, want)
	}
	if out != "" {
		t.Errorf("output = %q, want nothing beside the error", out)
	}
	if after := tree(t, dir, paths...); len(before) != 6 || !maps.Equal(before, after) {
		t.Errorf("the mirror changed: %d files before, %d after", len(before), len(after))
	}
}

// TestRun_FullRebuildsAMirrorOfAnotherStateVersion pins the remedy the state's
// error names: a plain run stops at such a state before any adapter, and a
// full run deletes the directory before it loads the state.
func TestRun_FullRebuildsAMirrorOfAnotherStateVersion(t *testing.T) {
	root := t.TempDir()
	dir := mirrorDir(root)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, stateFileName), []byte(`{"version": 2, "repo": "acme/widgets"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	gh := testGitHub(threeItems()...)
	s := newSyncer(gh, &fakeWiki{head: testWikiHead})

	out, err := runSync(t, s, Options{Root: root})
	if err == nil || !strings.HasSuffix(err.Error(), "; run brain sync --full to rebuild") {
		t.Fatalf("err = %v, want the hint to run --full", err)
	}
	if out != "" || gh.Count("ListUpdatedItems") != 0 {
		t.Errorf("a rejected state must stop the run before any adapter: out %q, %d listings", out, gh.Count("ListUpdatedItems"))
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 1 {
		t.Errorf("a rejected state must write nothing: %d entries, %v", len(entries), err)
	}

	if _, err := runSync(t, s, Options{Root: root, Full: true}); err != nil {
		t.Fatalf("full Run: %v", err)
	}
	if st := readState(t, dir); st.Version != 1 || st.SyncedAt != fixedNowStamp {
		t.Errorf("state = %+v, want a rebuilt version 1 state", st)
	}
}

// TestRun_NeedsAUserCacheDirectoryWithoutARoot covers a run without HOME and
// XDG_CACHE_HOME, as under a system unit: it stops before it reaches GitHub
// and does not fall back to the temp directory.
func TestRun_NeedsAUserCacheDirectoryWithoutARoot(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("XDG_CACHE_HOME", "")
	if _, err := os.UserCacheDir(); err == nil {
		t.Skip("the platform resolves a cache directory without HOME")
	}
	gh := testGitHub(threeItems()...)
	out, err := runSync(t, newSyncer(gh, &fakeWiki{head: testWikiHead}), Options{})
	if err == nil || !strings.HasPrefix(err.Error(), "resolving the user cache directory for the mirror: ") {
		t.Errorf("err = %v, want the cache directory's error", err)
	}
	if out != "" || gh.Count("ListUpdatedItems") != 0 {
		t.Errorf("the run must stop before any work: out %q, %d listings", out, gh.Count("ListUpdatedItems"))
	}
}

// TestRun_RebuildIsByteIdentical proves the mirror is a pure function of what
// GitHub holds: two full syncs of the same data, at different times, write
// the same item files and the same history.
func TestRun_RebuildIsByteIdentical(t *testing.T) {
	var trees []map[string]string
	for _, now := range []time.Time{fixedNow, fixedNow.Add(72 * time.Hour)} {
		root := t.TempDir()
		s := newSyncer(testGitHub(threeItems()...), &fakeWiki{head: testWikiHead})
		s.Now = func() time.Time { return now }
		if _, err := runSync(t, s, Options{Root: root, Full: true}); err != nil {
			t.Fatalf("Run: %v", err)
		}
		trees = append(trees, tree(t, mirrorDir(root), "issues", "pulls", "history.jsonl"))
	}
	if len(trees[0]) != 4 {
		t.Fatalf("the mirror holds %d files, want three items and the history", len(trees[0]))
	}
	for path, content := range trees[0] {
		if other, ok := trees[1][path]; !ok || other != content {
			t.Errorf("%s differs between the two rebuilds", path)
		}
	}
	if len(trees[1]) != len(trees[0]) {
		t.Errorf("the rebuilds hold %d and %d files", len(trees[0]), len(trees[1]))
	}
}

// stubAdapter is a scripted Adapter that records that it ran.
type stubAdapter struct {
	name string
	err  error
	sync func(m *Mirror)
	ran  *[]string
}

func (a stubAdapter) Name() string { return a.name }

func (a stubAdapter) Sync(m *Mirror) (string, error) {
	*a.ran = append(*a.ran, a.name)
	if a.sync != nil {
		a.sync(m)
	}
	if a.err != nil {
		return "", a.err
	}
	return a.name + ": done", nil
}

// TestRun_AFailedAdapterDoesNotStopTheNext covers the seam a later source
// plugs into: the adapters run in order, one that fails is reported under its
// name, the state the others changed is saved, and the sync does not count as
// finished.
func TestRun_AFailedAdapterDoesNotStopTheNext(t *testing.T) {
	root := t.TempDir()
	var ran []string
	boom := errors.New("boom")
	s := &Syncer{Now: func() time.Time { return fixedNow }, Adapters: []Adapter{
		stubAdapter{name: "first", ran: &ran, sync: func(m *Mirror) { m.State.Items.Cursor = openIssueUpdated }},
		stubAdapter{name: "second", ran: &ran, err: boom},
		stubAdapter{name: "third", ran: &ran, sync: func(m *Mirror) { m.State.Wiki.Commit = "abc" }},
		stubAdapter{name: "fourth", ran: &ran, err: errors.New("bang")},
	}}
	out, err := runSync(t, s, Options{Root: root})
	if err == nil || !errors.Is(err, boom) || err.Error() != "second: boom\nfourth: bang" {
		t.Errorf("err = %q, want both failures under their adapter's name", err)
	}
	if strings.Join(ran, ",") != "first,second,third,fourth" {
		t.Errorf("adapters ran = %v, want all four in order", ran)
	}
	if want := "Mirror of acme/widgets at " + mirrorDir(root) + "\nfirst: done\nthird: done\n"; out != want {
		t.Errorf("output = %q, want %q", out, want)
	}
	st := readState(t, mirrorDir(root))
	if st.Items.Cursor != openIssueUpdated || st.Wiki.Commit != "abc" || st.SyncedAt != "" {
		t.Errorf("state = %+v, want what the adapters set and no sync time", st)
	}
}

// TestRun_AFailedRunKeepsTheLastSyncTime proves a failure does not erase the
// time of the last sync that finished.
func TestRun_AFailedRunKeepsTheLastSyncTime(t *testing.T) {
	root := t.TempDir()
	var ran []string
	s := &Syncer{Now: func() time.Time { return fixedNow }, Adapters: []Adapter{stubAdapter{name: "only", ran: &ran}}}
	if _, err := runSync(t, s, Options{Root: root}); err != nil {
		t.Fatalf("first Run: %v", err)
	}
	s.Adapters = []Adapter{stubAdapter{name: "only", ran: &ran, err: errors.New("boom")}}
	s.Now = func() time.Time { return fixedNow.Add(time.Hour) }
	if _, err := runSync(t, s, Options{Root: root}); err == nil {
		t.Fatal("second Run: want the adapter's error")
	}
	if st := readState(t, mirrorDir(root)); st.SyncedAt != fixedNowStamp {
		t.Errorf("synced_at = %q, want the time of the first sync", st.SyncedAt)
	}
}

// TestRun_AFailedSaveEndsTheRun covers a state that cannot be written: the
// run stops there and names the state file.
func TestRun_AFailedSaveEndsTheRun(t *testing.T) {
	root := t.TempDir()
	var ran []string
	s := &Syncer{Adapters: []Adapter{
		// A directory in the place of the state file makes the save fail.
		stubAdapter{name: "first", ran: &ran, sync: func(m *Mirror) {
			if err := os.Mkdir(filepath.Join(m.Dir, stateFileName), 0o700); err != nil {
				t.Fatal(err)
			}
		}},
		stubAdapter{name: "second", ran: &ran},
	}}
	_, err := runSync(t, s, Options{Root: root})
	if want := "saving " + filepath.Join(mirrorDir(root), stateFileName) + ": "; err == nil || !strings.HasPrefix(err.Error(), want) {
		t.Errorf("err = %v, want it to start with %q", err, want)
	}
	if strings.Join(ran, ",") != "first" {
		t.Errorf("adapters ran = %v, want the run to end at the failed save", ran)
	}
}
