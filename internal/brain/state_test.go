package brain

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/planwerk/planwerk-agent/internal/capture"
	"github.com/planwerk/planwerk-agent/internal/patterns"
	"github.com/planwerk/planwerk-agent/internal/report"
)

const stateRepo = "acme/widgets"

// fakeWiki is an offline capture.WikiWriter over a directory that stands for
// the wiki. Clone hands that directory out as the clone; ApplyAdditions
// writes the pushed files into it and moves the head, the way a push does.
type fakeWiki struct {
	dir      string
	head     string
	cloneErr error
	applyErr error

	clones   int
	cleanups int
	repo     string
	ref      string
	pushed   [][]patterns.WikiFile
	msgs     []string
}

// newFakeWiki returns a wiki at head holding files (wiki-relative slash paths).
func newFakeWiki(t *testing.T, head string, files map[string]string) *fakeWiki {
	t.Helper()
	w := &fakeWiki{dir: t.TempDir(), head: head}
	writeTree(t, w.dir, files)
	return w
}

func (w *fakeWiki) Clone(repo, ref string) (string, string, func(), error) {
	w.clones++
	w.repo, w.ref = repo, ref
	if w.cloneErr != nil {
		return "", "", func() {}, w.cloneErr
	}
	return w.dir, w.head, func() { w.cleanups++ }, nil
}

func (w *fakeWiki) ApplyAdditions(dir string, files []patterns.WikiFile, msg string) error {
	if w.applyErr != nil {
		return w.applyErr
	}
	for _, f := range files {
		p := filepath.Join(dir, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(p, []byte(f.Content), 0o644); err != nil {
			return err
		}
	}
	w.pushed = append(w.pushed, files)
	w.msgs = append(w.msgs, msg)
	w.head = fmt.Sprintf("pushed-%d", len(w.pushed))
	return nil
}

var _ capture.WikiWriter = (*fakeWiki)(nil)

// readFile returns the content of the file, or fails the test.
func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(data)
}

// dirSnapshot maps every file under dir (slash path) to its content.
func dirSnapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		files[filepath.ToSlash(rel)] = readFile(t, p)
		return nil
	})
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("walking %s: %v", dir, err)
	}
	return files
}

func TestLoadState_FreshWithoutAStateFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), StateDirName)
	s, err := LoadState(dir, stateRepo)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if s.Version != 1 || s.Repo != stateRepo || len(s.Units) != 0 || len(s.Pages) != 0 || s.WikiCommit != "" {
		t.Errorf("state = %+v, want a fresh version-1 state of %s", s, stateRepo)
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("LoadState must not create the directory (stat err = %v)", err)
	}
}

func TestLoadState_Errors(t *testing.T) {
	for _, tc := range []struct {
		name    string
		content string
		want    string // FILE stands for the state file, DIR for the directory
	}{
		{"malformed JSON", `{"version": 1,`, "parsing FILE: "},
		{"another version", `{"version": 2, "repo": "acme/widgets"}`, "unsupported state version 2 in FILE; delete the directory to start over"},
		{"another repository", `{"version": 1, "repo": "acme/gadgets"}`, "DIR holds the bootstrap state of acme/gadgets, not acme/widgets"},
		{"page key that leaves the state directory", `{"version": 1, "repo": "acme/widgets", "pages": {"../../go.mod": {}}}`, `FILE names the page "../../go.mod", which is not a file in memory/ or review_patterns/; delete the directory to start over`},
		{"page key that leaves its directory", `{"version": 1, "repo": "acme/widgets", "pages": {"memory/../../x.md": {}}}`, `FILE names the page "memory/../../x.md", which is not a file in memory/ or review_patterns/`},
		{"page key in another directory", `{"version": 1, "repo": "acme/widgets", "pages": {"notes/x.md": {}}}`, `FILE names the page "notes/x.md", which is not a file in memory/ or review_patterns/`},
		{"page key in a subdirectory", `{"version": 1, "repo": "acme/widgets", "pages": {"memory/sub/x.md": {}}}`, `FILE names the page "memory/sub/x.md", which is not a file in memory/ or review_patterns/`},
		{"page key that is no Markdown file", `{"version": 1, "repo": "acme/widgets", "pages": {"memory/x.txt": {}}}`, `FILE names the page "memory/x.txt", which is not a file in memory/ or review_patterns/`},
		{"absolute page key", `{"version": 1, "repo": "acme/widgets", "pages": {"/etc/x.md": {}}}`, `FILE names the page "/etc/x.md", which is not a file in memory/ or review_patterns/`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			file := filepath.Join(dir, "state.json")
			writeTree(t, dir, map[string]string{"state.json": tc.content, "pages/memory/a.md": "a\n"})
			before := dirSnapshot(t, dir)

			_, err := LoadState(dir, stateRepo)
			want := strings.NewReplacer("FILE", file, "DIR", dir).Replace(tc.want)
			if err == nil || !strings.HasPrefix(err.Error(), want) {
				t.Errorf("err = %v, want it to start with %q", err, want)
			}
			if after := dirSnapshot(t, dir); !reflect.DeepEqual(before, after) {
				t.Errorf("the directory changed: %v -> %v", before, after)
			}
		})
	}
}

func TestLoadState_RepoMatchIgnoresCase(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{"state.json": `{"version": 1, "repo": "Acme/Widgets", "units": null, "pages": null}`})
	s, err := LoadState(dir, stateRepo)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if s.Units == nil || s.Pages == nil {
		t.Errorf("null collections must load as empty ones: %+v", s)
	}
}

func TestStateSave_RoundTripsAndLeavesNoTempFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), StateDirName)
	s := newState(stateRepo)
	s.WikiRepo, s.WikiCommit = stateRepo, "abc123"
	s.Units = append(s.Units, UnitRecord{
		Key: "issue-42", Title: "Pin the base image", Proposed: 2, Accepted: 1,
		Rejected:   []Rejection{{Path: "memory/x.md", Reason: "a one-off"}},
		FinishedAt: "2026-10-01T12:00:00Z",
	})
	s.Pages["memory/y.md"] = PageState{Source: "acme/widgets#42", BaseSHA256: "", Diverged: false}
	s.Usage = report.Usage{Calls: 3, InputTokens: 100, OutputTokens: 20, CostUSD: 0.5}

	if err := s.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "state.json.tmp")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("state.json.tmp left behind (stat err = %v)", err)
	}
	for _, sub := range []string{"pages/memory", "pages/review_patterns"} {
		if info, err := os.Stat(filepath.Join(dir, sub)); err != nil || !info.IsDir() {
			t.Errorf("Save must create %s: %v", sub, err)
		}
	}
	loaded, err := LoadState(dir, stateRepo)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if !reflect.DeepEqual(loaded, s) {
		t.Errorf("loaded state differs:\n got %+v\nwant %+v", loaded, s)
	}
	if raw := readFile(t, filepath.Join(dir, "state.json")); strings.Contains(raw, `"passes"`) {
		t.Errorf("the saved usage must carry no per-pass breakdown:\n%s", raw)
	}
}

// TestStateSave_GitIgnoresTheDirectory proves the directory never shows up in
// the repository it sits in.
func TestStateSave_GitIgnoresTheDirectory(t *testing.T) {
	repo := t.TempDir()
	if out, err := exec.Command("git", "-C", repo, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	dir := filepath.Join(repo, StateDirName)
	s := newState(stateRepo)
	if err := s.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := writePage(dir, "memory/a.md", "a"); err != nil {
		t.Fatalf("writePage: %v", err)
	}
	if got := readFile(t, filepath.Join(dir, ".gitignore")); got != "*\n" {
		t.Errorf(".gitignore = %q, want one line holding *", got)
	}
	out, err := exec.Command("git", "-C", repo, "status", "--porcelain").CombinedOutput()
	if err != nil {
		t.Fatalf("git status: %v\n%s", err, out)
	}
	if len(out) != 0 {
		t.Errorf("git status --porcelain = %q, want nothing", out)
	}

	// A second Save keeps a .gitignore the operator changed.
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("*\n!keep\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(dir); err != nil {
		t.Fatalf("second Save: %v", err)
	}
	if got := readFile(t, filepath.Join(dir, ".gitignore")); got != "*\n!keep\n" {
		t.Errorf(".gitignore was rewritten: %q", got)
	}
}

func TestStateSave_UnwritableDirectory(t *testing.T) {
	// A regular file where the directory should be: MkdirAll fails for every
	// user, root included.
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := newState(stateRepo).Save(filepath.Join(blocker, StateDirName)); err == nil {
		t.Error("Save into a path below a regular file must fail")
	}
}

func TestReconcile(t *testing.T) {
	dir := t.TempDir()
	s := newState(stateRepo)
	s.Pages["memory/kept.md"] = PageState{}
	s.Pages["memory/missing.md"] = PageState{BaseSHA256: "abc"}
	if err := s.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}
	writeTree(t, dir, map[string]string{
		"pages/memory/kept.md":            "kept\n",
		"pages/memory/orphan.md":          "orphan\n",
		"pages/review_patterns/orphan.md": "orphan\n",
	})

	// LoadState alone changes neither the entries nor the files.
	loaded, err := LoadState(dir, stateRepo)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if len(loaded.Pages) != 2 {
		t.Errorf("LoadState dropped an entry: %v", loaded.Pages)
	}
	if _, err := os.Stat(filepath.Join(dir, "pages", "memory", "orphan.md")); err != nil {
		t.Errorf("LoadState deleted a file: %v", err)
	}

	logs := captureLogs(t)
	if err := loaded.Reconcile(dir); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if _, ok := loaded.Pages["memory/missing.md"]; ok || len(loaded.Pages) != 1 {
		t.Errorf("pages = %v, want only memory/kept.md", loaded.Pages)
	}
	files := dirSnapshot(t, filepath.Join(dir, "pages"))
	if len(files) != 1 || files["memory/kept.md"] != "kept\n" {
		t.Errorf("page files = %v, want only memory/kept.md", files)
	}
	// A file without an entry is kept, outside the working set, and named in
	// a warning.
	wantOrphans := map[string]string{"memory/orphan.md": "orphan\n", "review_patterns/orphan.md": "orphan\n"}
	if got := dirSnapshot(t, filepath.Join(dir, "orphans")); !reflect.DeepEqual(got, wantOrphans) {
		t.Errorf("orphans = %v, want %v", got, wantOrphans)
	}
	if n := strings.Count(logs.String(), "level=WARN"); n != 2 || !strings.Contains(logs.String(), filepath.Join("orphans", "memory", "orphan.md")) {
		t.Errorf("want one warning per moved file that names where it went, got:\n%s", logs.String())
	}
}

// TestReconcile_DropsASymlinkedPage is the guard against a page file that is
// a symbolic link: readPage would follow it and --write-wiki push what it
// points at.
func TestReconcile_DropsASymlinkedPage(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(t.TempDir(), "outside.md")
	if err := os.WriteFile(target, []byte("outside\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := newState(stateRepo)
	s.Pages["memory/link.md"] = PageState{}
	if err := s.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}
	link := filepath.Join(dir, "pages", "memory", "link.md")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	captureLogs(t)

	if err := s.Reconcile(dir); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if _, ok := s.Pages["memory/link.md"]; ok {
		t.Error("a symlinked page kept its entry")
	}
	if _, err := os.Lstat(link); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the symlink is still in the working set (lstat err = %v)", err)
	}
	if got := readFile(t, target); got != "outside\n" {
		t.Errorf("the symlink's target changed: %q", got)
	}
}

// TestReconcile_KeepsAnEarlierOrphan proves a file moved to orphans/ never
// replaces what is already there: an earlier orphan under the same path is the
// only copy of its text too.
func TestReconcile_KeepsAnEarlierOrphan(t *testing.T) {
	for _, tc := range []struct {
		name    string
		earlier map[string]string // what orphans/ holds before the second file is moved
		page    string            // the second file, under pages/ without an entry
	}{
		{"a file at the path", map[string]string{"orphans/memory/notes.md": "first\n"}, "pages/memory/notes.md"},
		{"a directory at the path", map[string]string{"orphans/memory/notes.md/inner.md": "first\n"}, "pages/memory/notes.md"},
		{"the first free name is taken too", map[string]string{"orphans/memory/notes.md": "first\n", "orphans/memory/notes.md.1": "between\n"}, "pages/memory/notes.md"},
		{"a file at a directory of the path", map[string]string{"orphans/memory/notes.md": "first\n"}, "pages/memory/notes.md/inner.md"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeTree(t, dir, tc.earlier)
			writeTree(t, dir, map[string]string{tc.page: "second\n"})
			before := dirSnapshot(t, filepath.Join(dir, "orphans"))
			logs := captureLogs(t)

			if err := newState(stateRepo).Reconcile(dir); err != nil {
				t.Fatalf("Reconcile: %v", err)
			}
			if files := dirSnapshot(t, filepath.Join(dir, "pages")); len(files) != 0 {
				t.Errorf("page files = %v, want none", files)
			}
			after := dirSnapshot(t, filepath.Join(dir, "orphans"))
			var moved string
			for p, text := range after {
				if _, ok := before[p]; !ok && text == "second\n" {
					moved = p
				}
			}
			if moved == "" || len(after) != len(before)+1 {
				t.Fatalf("orphans = %v, want %v and the moved file under a free name", after, before)
			}
			for p, text := range before {
				if after[p] != text {
					t.Errorf("the earlier orphan %s = %q, want %q", p, after[p], text)
				}
			}
			if !strings.Contains(logs.String(), filepath.Join("orphans", filepath.FromSlash(moved))) {
				t.Errorf("want a warning that names %s, got:\n%s", moved, logs.String())
			}
		})
	}
}

func TestReconcile_WithoutAPagesDirectory(t *testing.T) {
	s := newState(stateRepo)
	s.Pages["memory/gone.md"] = PageState{}
	if err := s.Reconcile(filepath.Join(t.TempDir(), "absent")); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(s.Pages) != 0 {
		t.Errorf("pages = %v, want none", s.Pages)
	}
}

// refreshFixture is a state directory with one page per refresh case and the
// wiki those pages meet.
type refreshFixture struct {
	dir   string
	state *State
	wiki  *fakeWiki
}

const marker = "<!-- planwerk-agent: captured from acme/widgets#7 -->\n\n"

// lastRefreshHead is the wiki commit the fixture's state was last refreshed at.
const lastRefreshHead = "old"

func newRefreshFixture(t *testing.T) refreshFixture {
	t.Helper()
	dir := t.TempDir()
	s := newState(stateRepo)
	s.WikiRepo, s.WikiCommit = stateRepo, lastRefreshHead
	local := map[string]string{
		"memory/clean.md":           "base\n",
		"memory/dirty-unchanged.md": "local edit\n",
		"memory/dirty-same.md":      "same text\n",
		"memory/dirty-other.md":     "local edit\n",
		"memory/was-diverged.md":    "local edit\n",
		"memory/gone-clean.md":      "base\n",
		"memory/gone-dirty.md":      "local edit\n",
		"memory/local-only.md":      "new page\n",
	}
	for p, body := range local {
		if err := writePage(dir, p, body); err != nil {
			t.Fatalf("writePage: %v", err)
		}
	}
	base := hashPage("base\n")
	s.Pages = map[string]PageState{
		"memory/clean.md":           {Source: "acme/widgets#1", BaseSHA256: base, Diverged: true},
		"memory/dirty-unchanged.md": {Source: "acme/widgets#2", BaseSHA256: base},
		"memory/dirty-same.md":      {Source: "acme/widgets#3", BaseSHA256: base, Diverged: true},
		"memory/dirty-other.md":     {Source: "acme/widgets#4", BaseSHA256: base},
		"memory/was-diverged.md":    {Source: "acme/widgets#5", BaseSHA256: base, Diverged: true},
		"memory/gone-clean.md":      {BaseSHA256: base},
		"memory/gone-dirty.md":      {Source: "acme/widgets#6", BaseSHA256: base, Diverged: true},
		"memory/local-only.md":      {Source: "acme/widgets#8"},
	}
	wiki := newFakeWiki(t, "new", map[string]string{
		"Home.md":                     "navigation\n",
		"memory/added.md":             marker + "from the wiki\n\n\n",
		"review_patterns/added.md":    "# Review Pattern: Added\n",
		"memory/clean.md":             "changed on the wiki\n",
		"memory/dirty-unchanged.md":   "base\n",
		"memory/dirty-same.md":        marker + "same text\n",
		"memory/dirty-other.md":       "changed on the wiki\n",
		"memory/was-diverged.md":      "base\n",
		"memory/README.txt":           "not a page\n",
		"review_patterns/sub/deep.md": "not enumerated\n",
	})
	return refreshFixture{dir: dir, state: s, wiki: wiki}
}

func TestRefresh(t *testing.T) {
	f := newRefreshFixture(t)
	if err := f.state.Refresh(f.dir, f.wiki, stateRepo, "pinned"); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if f.wiki.clones != 1 || f.wiki.repo != stateRepo || f.wiki.ref != "pinned" || f.wiki.cleanups != 1 {
		t.Errorf("clone = %d calls, repo %q, ref %q, %d cleanups", f.wiki.clones, f.wiki.repo, f.wiki.ref, f.wiki.cleanups)
	}
	if f.state.WikiCommit != "new" || f.state.WikiRepo != stateRepo {
		t.Errorf("wiki = %s @ %s, want %s @ new", f.state.WikiRepo, f.state.WikiCommit, stateRepo)
	}

	base := hashPage("base\n")
	wantPages := map[string]PageState{
		// No entry: added with the wiki's text as its base, marker stripped,
		// under the source the marker names.
		"memory/added.md":          {Source: "acme/widgets#7", BaseSHA256: hashPage("from the wiki\n")},
		"review_patterns/added.md": {BaseSHA256: hashPage("# Review Pattern: Added\n")},
		// Clean: takes the wiki's text, and its source with it. This wiki page
		// has no marker.
		"memory/clean.md": {BaseSHA256: hashPage("changed on the wiki\n")},
		// Dirty, the wiki did not change: untouched.
		"memory/dirty-unchanged.md": {Source: "acme/widgets#2", BaseSHA256: base},
		// Dirty, the wiki holds the local text: clean now, under the source of
		// the wiki page's marker.
		"memory/dirty-same.md": {Source: "acme/widgets#7", BaseSHA256: hashPage("same text\n")},
		// Dirty and the wiki changed to something else: diverged.
		"memory/dirty-other.md": {Source: "acme/widgets#4", BaseSHA256: base, Diverged: true},
		// The wiki is back at the base of the local edit: not diverged any more.
		"memory/was-diverged.md": {Source: "acme/widgets#5", BaseSHA256: base},
		// Dropped from the wiki while dirty: a new page.
		"memory/gone-dirty.md": {Source: "acme/widgets#6"},
		// Never on the wiki: untouched.
		"memory/local-only.md": {Source: "acme/widgets#8"},
	}
	if !reflect.DeepEqual(f.state.Pages, wantPages) {
		for p, want := range wantPages {
			if got, ok := f.state.Pages[p]; !ok || got != want {
				t.Errorf("%s = %+v (present %v), want %+v", p, got, ok, want)
			}
		}
		for p := range f.state.Pages {
			if _, ok := wantPages[p]; !ok {
				t.Errorf("unexpected page entry %s", p)
			}
		}
	}

	wantFiles := map[string]string{
		"memory/added.md":           "from the wiki\n",
		"review_patterns/added.md":  "# Review Pattern: Added\n",
		"memory/clean.md":           "changed on the wiki\n",
		"memory/dirty-unchanged.md": "local edit\n",
		"memory/dirty-same.md":      "same text\n",
		"memory/dirty-other.md":     "local edit\n",
		"memory/was-diverged.md":    "local edit\n",
		"memory/gone-dirty.md":      "local edit\n",
		"memory/local-only.md":      "new page\n",
	}
	if got := dirSnapshot(t, filepath.Join(f.dir, "pages")); !reflect.DeepEqual(got, wantFiles) {
		t.Errorf("page files = %v\nwant %v", got, wantFiles)
	}
}

func TestRefresh_FailingCloneWarnsAndChangesNothing(t *testing.T) {
	f := newRefreshFixture(t)
	f.wiki.cloneErr = errors.New("repository not found")
	pagesBefore := dirSnapshot(t, f.dir)
	entriesBefore := make(map[string]PageState, len(f.state.Pages))
	for p, e := range f.state.Pages {
		entriesBefore[p] = e
	}
	logs := captureLogs(t)

	if err := f.state.Refresh(f.dir, f.wiki, stateRepo, ""); err != nil {
		t.Fatalf("Refresh must continue after a failed clone, got %v", err)
	}
	if n := strings.Count(logs.String(), "level=WARN"); n != 1 || !strings.Contains(logs.String(), "repository not found") {
		t.Errorf("want one warning carrying the clone error, got:\n%s", logs.String())
	}
	if f.state.WikiCommit != lastRefreshHead || !reflect.DeepEqual(f.state.Pages, entriesBefore) {
		t.Errorf("the state changed: commit %q, pages %v", f.state.WikiCommit, f.state.Pages)
	}
	if after := dirSnapshot(t, f.dir); !reflect.DeepEqual(pagesBefore, after) {
		t.Error("the page files changed")
	}
}

func TestRefresh_SameHeadRewritesNothing(t *testing.T) {
	f := newRefreshFixture(t)
	f.wiki.head = lastRefreshHead
	clean := filepath.Join(f.dir, "pages", "memory", "clean.md")
	infoBefore, err := os.Stat(clean)
	if err != nil {
		t.Fatal(err)
	}
	filesBefore := dirSnapshot(t, f.dir)

	if err := f.state.Refresh(f.dir, f.wiki, stateRepo, ""); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	infoAfter, err := os.Stat(clean)
	if err != nil {
		t.Fatal(err)
	}
	if !infoAfter.ModTime().Equal(infoBefore.ModTime()) {
		t.Error("a page file was rewritten although the wiki did not move")
	}
	if after := dirSnapshot(t, f.dir); !reflect.DeepEqual(filesBefore, after) {
		t.Error("the page files changed although the wiki did not move")
	}
	if _, ok := f.state.Pages["memory/added.md"]; ok {
		t.Error("a page was added although the wiki did not move")
	}
}

func TestRefresh_WikiWithoutKnowledgeDirectoriesSeedsNoPage(t *testing.T) {
	dir := t.TempDir()
	s := newState(stateRepo)
	wiki := newFakeWiki(t, "head", map[string]string{"Home.md": "welcome\n", "notes/x.md": "elsewhere\n"})

	if err := s.Refresh(dir, wiki, stateRepo, ""); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if len(s.Pages) != 0 || s.WikiCommit != "head" {
		t.Errorf("pages = %v, commit = %q, want no page and the wiki's head", s.Pages, s.WikiCommit)
	}
	if files := dirSnapshot(t, filepath.Join(dir, "pages")); len(files) != 0 {
		t.Errorf("page files = %v, want none", files)
	}
}

// TestRefresh_SkipsAWikiPageTheStateCannotHold is the guard against a refresh
// that saves a state LoadState refuses: the wiki can hold a file named ".md".
func TestRefresh_SkipsAWikiPageTheStateCannotHold(t *testing.T) {
	dir := t.TempDir()
	s := newState(stateRepo)
	wiki := newFakeWiki(t, "head", map[string]string{
		"memory/.md":          "no name\n",
		"memory/a.md":         "a\n",
		"review_patterns/.md": "no name\n",
	})
	logs := captureLogs(t)

	if err := s.Refresh(dir, wiki, stateRepo, ""); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if err := s.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := LoadState(dir, stateRepo)
	if err != nil {
		t.Fatalf("LoadState after the refresh: %v", err)
	}
	if _, ok := loaded.Pages["memory/a.md"]; !ok || len(loaded.Pages) != 1 {
		t.Errorf("pages = %v, want only memory/a.md", loaded.Pages)
	}
	wantFiles := map[string]string{"memory/a.md": "a\n"}
	if got := dirSnapshot(t, filepath.Join(dir, "pages")); !reflect.DeepEqual(got, wantFiles) {
		t.Errorf("page files = %v, want %v", got, wantFiles)
	}
	if n := strings.Count(logs.String(), "level=WARN"); n != 2 || !strings.Contains(logs.String(), "memory/.md") || !strings.Contains(logs.String(), "review_patterns/.md") {
		t.Errorf("want one warning per skipped page that names it, got:\n%s", logs.String())
	}
}

func TestRefresh_UnwritablePagesDirectory(t *testing.T) {
	dir := t.TempDir()
	// A regular file where pages/ should be fails the page write for every user.
	if err := os.WriteFile(filepath.Join(dir, "pages"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	s := newState(stateRepo)
	wiki := newFakeWiki(t, "head", map[string]string{"memory/a.md": "a\n"})

	err := s.Refresh(dir, wiki, stateRepo, "")
	if err == nil || !strings.HasPrefix(err.Error(), "refreshing the working set from the wiki: ") {
		t.Errorf("err = %v, want a refresh error", err)
	}
	if s.WikiCommit != "" {
		t.Errorf("wiki_commit = %q, want it unchanged after a failed refresh", s.WikiCommit)
	}
}
