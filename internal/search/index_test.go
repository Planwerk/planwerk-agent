package search

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/planwerk/planwerk-agent/internal/github"
	"github.com/planwerk/planwerk-agent/internal/mirror"
)

const testRepo = "acme/widgets"

// testSyncedAt is the end of the last sync of every test mirror.
const testSyncedAt = "2026-10-02T09:00:00Z"

// captureLogs routes the default logger into a buffer for the test.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var logBuf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &logBuf
}

// newMirrorAt creates dir as the mirror directory of acme/widgets with a
// finished sync and no file.
func newMirrorAt(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	st, _, err := mirror.LoadState(dir, testRepo)
	if err != nil {
		t.Fatalf("mirror.LoadState: %v", err)
	}
	st.SyncedAt = testSyncedAt
	st.Items.Cursor = "2026-10-01T08:00:00Z"
	st.Wiki = mirror.WikiState{Repo: testRepo, Commit: "1a2b3c4d5e6f70819293a4b5c6d7e8f901234567"}
	if err := st.Save(dir); err != nil {
		t.Fatalf("saving the mirror state: %v", err)
	}
}

// newMirror returns the mirror directory of acme/widgets under a fresh root.
func newMirror(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "acme", "widgets")
	newMirrorAt(t, dir)
	return dir
}

// writeFile writes content to path and creates the directories above it.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// writeItem writes the item file of it into the mirror directory dir.
func writeItem(t *testing.T, dir string, it *github.Item) {
	t.Helper()
	writeFile(t, mirror.ItemPath(dir, it.Kind, it.Number), string(mirror.Render(testRepo, it)))
}

// writeWiki writes a page of the wiki clone in the mirror directory dir.
func writeWiki(t *testing.T, dir, name, content string) {
	t.Helper()
	writeFile(t, filepath.Join(mirror.WikiDir(dir), filepath.FromSlash(name)), content)
}

// testIssue returns an open issue with the given number, title, and body.
func testIssue(number int, title, body string) *github.Item {
	return &github.Item{
		Kind: github.ItemKindIssue, Number: number, Title: title, Body: body,
		URL:   fmt.Sprintf("https://github.com/acme/widgets/issues/%d", number),
		State: "open", Author: "alice", AuthorAssociation: "MEMBER",
		CreatedAt: "2026-09-01T08:00:00Z", UpdatedAt: "2026-09-02T08:00:00Z",
	}
}

// testPull returns a merged pull request with the given number, title, and
// body.
func testPull(number int, title, body string) *github.Item {
	return &github.Item{
		Kind: github.ItemKindPull, Number: number, Title: title, Body: body,
		URL:   fmt.Sprintf("https://github.com/acme/widgets/pull/%d", number),
		State: "merged", Author: "bob", AuthorAssociation: "CONTRIBUTOR",
		CreatedAt: "2026-09-03T08:00:00Z", UpdatedAt: "2026-09-04T08:00:00Z",
	}
}

// openIndex opens the index of dir and closes it when the test ends.
func openIndex(t *testing.T, dir string) *Index {
	t.Helper()
	ix, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = ix.Close() })
	return ix
}

// reopen opens the index of dir, checks what the refresh counted, and closes
// it again.
func reopen(t *testing.T, dir string, wantIndexed, wantRemoved int) {
	t.Helper()
	ix, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = ix.Close() }()
	if ix.Indexed != wantIndexed || ix.Removed != wantRemoved {
		t.Fatalf("Open indexed %d and removed %d files, want %d and %d", ix.Indexed, ix.Removed, wantIndexed, wantRemoved)
	}
}

// indexedPaths returns the paths of the files the index holds, in order.
func indexedPaths(t *testing.T, ix *Index) []string {
	t.Helper()
	stats, err := ix.fileRows()
	if err != nil {
		t.Fatalf("reading the files of the index: %v", err)
	}
	paths := make([]string, 0, len(stats))
	for p := range stats {
		paths = append(paths, p)
	}
	slices.Sort(paths)
	return paths
}

// searchFiles runs q and returns the files of its hits, in order.
func searchFiles(t *testing.T, ix *Index, q Query) []string {
	t.Helper()
	hits, err := ix.Search(q)
	if err != nil {
		t.Fatalf("Search(%+v): %v", q, err)
	}
	files := make([]string, 0, len(hits))
	for _, h := range hits {
		files = append(files, h.File)
	}
	return files
}

// countBlocks returns the number of blocks the index holds and the number its
// files count.
func countBlocks(t *testing.T, ix *Index) (blocks, counted int) {
	t.Helper()
	if err := ix.db.QueryRow(`SELECT (SELECT count(*) FROM blocks), (SELECT coalesce(sum(block_count), 0) FROM files)`).Scan(&blocks, &counted); err != nil {
		t.Fatal(err)
	}
	return blocks, counted
}

// setVersionRow writes value into the version row of the index of dir.
func setVersionRow(t *testing.T, dir, value string) {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(dir, IndexFileName))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`UPDATE meta SET value = ? WHERE key = 'version'`, value); err != nil {
		t.Fatalf("writing the version row: %v", err)
	}
}

// later moves the modification time of path an hour ahead, so a rewrite is
// seen whatever the resolution of the file system's clock.
func later(t *testing.T, path string) {
	t.Helper()
	at := time.Now().Add(time.Hour)
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
}

// threeFiles writes two item files and one wiki page into dir.
func threeFiles(t *testing.T, dir string) {
	t.Helper()
	writeItem(t, dir, testIssue(7, "Pin the base image", "The tag moved twice last month."))
	writeItem(t, dir, testPull(9, "Pin the base image to a digest", "Closes #7"))
	writeWiki(t, dir, "Home.md", "# Home\n\nWelcome.\n")
}

func TestOpen_IndexesEveryFileOnce(t *testing.T) {
	dir := newMirror(t)
	threeFiles(t, dir)

	ix := openIndex(t, dir)
	if ix.Indexed != 3 || ix.Removed != 0 {
		t.Fatalf("first Open indexed %d and removed %d files, want 3 and 0", ix.Indexed, ix.Removed)
	}
	if want := []string{"issues/7.md", "pulls/9.md", "wiki/Home.md"}; !slices.Equal(indexedPaths(t, ix), want) {
		t.Errorf("indexed paths = %v, want %v", indexedPaths(t, ix), want)
	}
	if _, err := os.Stat(filepath.Join(dir, IndexFileName)); err != nil {
		t.Errorf("Open must create %s: %v", IndexFileName, err)
	}

	// Nothing changed, so a second Open writes nothing.
	reopen(t, dir, 0, 0)
}

func TestOpen_CreatesTheIndexForItsOwnerOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no file mode bits")
	}
	dir := newMirror(t)
	openIndex(t, dir)
	info, err := os.Stat(filepath.Join(dir, IndexFileName))
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("mode of %s = %o, want 600", IndexFileName, got)
	}
}

func TestOpen_AMirrorWithoutAFile(t *testing.T) {
	// No item directory and no wiki directory.
	ix := openIndex(t, newMirror(t))
	if ix.Indexed != 0 || ix.Removed != 0 {
		t.Errorf("Open indexed %d and removed %d files, want 0 and 0", ix.Indexed, ix.Removed)
	}
	hits, err := ix.Search(Query{Text: "cursor"})
	if err != nil || hits == nil || len(hits) != 0 {
		t.Errorf("Search = %#v, %v, want an empty slice and no error", hits, err)
	}
}

func TestOpen_RebuildsAnIndexBuiltByOtherRules(t *testing.T) {
	for name, version := range map[string]string{
		"another index version":   "0/" + strings.Repeat("0", 64),
		"other redaction rules":   fmt.Sprintf("%d/%s", indexVersion, strings.Repeat("0", 64)),
		"a value of another form": "x",
	} {
		t.Run(name, func(t *testing.T) {
			dir := newMirror(t)
			threeFiles(t, dir)
			reopen(t, dir, 3, 0)
			setVersionRow(t, dir, version)

			logs := captureLogs(t)
			reopen(t, dir, 3, 0)
			if !strings.Contains(logs.String(), "rebuilding the search index") || !strings.Contains(logs.String(), `reason="the index was built by other rules"`) {
				t.Errorf("log = %q, want the rebuild and its reason named", logs)
			}
			// The rebuilt index carries the version of this binary.
			reopen(t, dir, 0, 0)
		})
	}
}

// TestOpen_ConcurrentOpensOfAStaleIndex proves that eight callers that find
// an index built by other rules all succeed and leave one index: none of them
// removes what another one built.
func TestOpen_ConcurrentOpensOfAStaleIndex(t *testing.T) {
	dir := newMirror(t)
	threeFiles(t, dir)
	reopen(t, dir, 3, 0)
	setVersionRow(t, dir, "0/"+strings.Repeat("0", 64))
	before, err := os.Stat(filepath.Join(dir, IndexFileName))
	if err != nil {
		t.Fatal(err)
	}

	const callers = 8
	errs := make(chan error, callers)
	for range callers {
		go func() {
			ix, err := Open(dir)
			if err == nil {
				err = ix.Close()
			}
			errs <- err
		}()
	}
	for range callers {
		if err := <-errs; err != nil {
			t.Errorf("Open: %v", err)
		}
	}

	// The index was emptied inside its file. A caller that removed the file
	// would have taken it from under the others.
	after, err := os.Stat(filepath.Join(dir, IndexFileName))
	if err != nil || !os.SameFile(before, after) {
		t.Errorf("the stale index must be replaced inside its file: %v", err)
	}
	ix := openIndex(t, dir)
	if ix.Indexed != 0 || ix.Removed != 0 {
		t.Errorf("Open after the eight indexed %d and removed %d files, want 0 and 0", ix.Indexed, ix.Removed)
	}
	if want := []string{"issues/7.md", "pulls/9.md", "wiki/Home.md"}; !slices.Equal(indexedPaths(t, ix), want) {
		t.Errorf("indexed paths = %v, want %v", indexedPaths(t, ix), want)
	}
	if blocks, counted := countBlocks(t, ix); blocks != counted {
		t.Errorf("the index holds %d blocks, its files count %d", blocks, counted)
	}
}

func TestOpen_ReplacesAFileThatIsNoDatabase(t *testing.T) {
	dir := newMirror(t)
	threeFiles(t, dir)
	writeFile(t, filepath.Join(dir, IndexFileName), "not a database")

	logs := captureLogs(t)
	reopen(t, dir, 3, 0)
	if !strings.Contains(logs.String(), "rebuilding the search index") {
		t.Errorf("log = %q, want the rebuild named", logs)
	}
	reopen(t, dir, 0, 0)
}

func TestOpen_ReplacesADamagedIndex(t *testing.T) {
	dir := newMirror(t)
	threeFiles(t, dir)
	reopen(t, dir, 3, 0)

	// Keep the header, which names the file a database, and overwrite the
	// pages after it.
	index := filepath.Join(dir, IndexFileName)
	data, err := os.ReadFile(index)
	if err != nil {
		t.Fatal(err)
	}
	const headerBytes = 100
	for i := headerBytes; i < len(data); i++ {
		data[i] = 0xff
	}
	if err := os.WriteFile(index, data, 0o600); err != nil {
		t.Fatal(err)
	}

	reopen(t, dir, 3, 0)
	reopen(t, dir, 0, 0)
}

// TestOpen_ALockedIndexIsErrorAndStays covers an index another process holds
// the lock of for longer than a connection waits: the lock is no damage, so
// Open fails and removes nothing.
func TestOpen_ALockedIndexIsErrorAndStays(t *testing.T) {
	dir := newMirror(t)
	threeFiles(t, dir)
	reopen(t, dir, 3, 0)
	index := filepath.Join(dir, IndexFileName)
	before, err := os.Stat(index)
	if err != nil {
		t.Fatal(err)
	}

	holder, err := sql.Open("sqlite", index)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = holder.Close() })
	conn, err := holder.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if _, err := conn.ExecContext(context.Background(), "BEGIN EXCLUSIVE"); err != nil {
		t.Fatalf("taking the lock: %v", err)
	}

	logs := captureLogs(t)
	ix, err := open(dir, 50*time.Millisecond)
	if err == nil || !strings.HasPrefix(err.Error(), "opening the search index ") || !strings.Contains(err.Error(), "locked") {
		t.Fatalf("Open of a locked index = %v, %v, want an error that names the lock", ix, err)
	}
	if strings.Contains(logs.String(), "rebuilding the search index") {
		t.Errorf("log = %q, want no rebuild of a locked index", logs)
	}

	if _, err := conn.ExecContext(context.Background(), "ROLLBACK"); err != nil {
		t.Fatalf("releasing the lock: %v", err)
	}
	after, err := os.Stat(index)
	if err != nil || !os.SameFile(before, after) {
		t.Errorf("a locked index must stay in place: %v", err)
	}
	// The index is the one that was there: nothing is indexed again.
	reopen(t, dir, 0, 0)
}

func TestOpen_SkipsAFileItCannotParseUntilItParses(t *testing.T) {
	dir := newMirror(t)
	threeFiles(t, dir)
	broken := mirror.ItemPath(dir, github.ItemKindIssue, 5)
	writeFile(t, broken, "no frontmatter\n")

	logs := captureLogs(t)
	ix := openIndex(t, dir)
	if ix.Indexed != 3 {
		t.Fatalf("Open indexed %d files, want the 3 that parse", ix.Indexed)
	}
	if slices.Contains(indexedPaths(t, ix), "issues/5.md") {
		t.Error("a file that does not parse must get no row")
	}
	if want := "skipped mirror files that could not be indexed; run brain sync --full if this persists"; !strings.Contains(logs.String(), want) || !strings.Contains(logs.String(), "files=1") {
		t.Errorf("log = %q, want one warning that counts the file", logs)
	}

	// The file has no row, so the next Open tries it again.
	reopen(t, dir, 0, 0)
	writeItem(t, dir, testIssue(5, "Now it parses", "A body."))
	reopen(t, dir, 1, 0)
}

func TestOpen_AFileThatStopsParsingLosesItsRow(t *testing.T) {
	dir := newMirror(t)
	threeFiles(t, dir)
	reopen(t, dir, 3, 0)

	path := mirror.ItemPath(dir, github.ItemKindIssue, 7)
	writeFile(t, path, "no frontmatter\n")
	later(t, path)

	ix := openIndex(t, dir)
	if ix.Indexed != 0 || ix.Removed != 1 {
		t.Fatalf("Open indexed %d and removed %d files, want 0 and 1", ix.Indexed, ix.Removed)
	}
	if slices.Contains(indexedPaths(t, ix), "issues/7.md") {
		t.Error("the row of a file that no longer parses must be deleted")
	}
}

func TestOpen_MissingMirrorDirectoryIsError(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "missing")
	ix, err := Open(dir)
	if err == nil || !strings.HasPrefix(err.Error(), "creating the search index ") {
		t.Fatalf("Open = %v, %v, want an error that starts with %q", ix, err, "creating the search index ")
	}
	if _, statErr := os.Stat(dir); !os.IsNotExist(statErr) {
		t.Errorf("Open must not create the mirror directory: %v", statErr)
	}
}

func TestOpen_AnIndexThatCannotBeWrittenIsError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no file mode bits")
	}
	if os.Geteuid() == 0 {
		t.Skip("the root user writes a read-only file")
	}
	dir := newMirror(t)
	threeFiles(t, dir)
	reopen(t, dir, 3, 0)
	index := filepath.Join(dir, IndexFileName)
	if err := os.Chmod(index, 0o400); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(index, 0o600) })

	// Nothing changed, so the index is opened without a write.
	reopen(t, dir, 0, 0)

	writeItem(t, dir, testIssue(8, "A new issue", "A body."))
	ix, err := Open(dir)
	if err == nil || !strings.HasPrefix(err.Error(), "updating the search index ") {
		t.Fatalf("Open = %v, %v, want an error that starts with %q", ix, err, "updating the search index ")
	}
}

func TestOpen_APathWithASpaceAndAHash(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "my cache #1", "acme", "widgets")
	newMirrorAt(t, dir)
	threeFiles(t, dir)

	reopen(t, dir, 3, 0)
	if _, err := os.Stat(filepath.Join(dir, IndexFileName)); err != nil {
		t.Errorf("the index must be in the mirror directory: %v", err)
	}
	// A data source name that is cut at the "#" or the space opens a file
	// beside the directory.
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("the root holds %d entries, want only the directory", len(entries))
	}
	reopen(t, dir, 0, 0)
}

func TestOpen_KeepsOnlyThePagesOfTheWikiClone(t *testing.T) {
	dir := newMirror(t)
	writeWiki(t, dir, "Home.md", "# Home\n\nWelcome.\n")
	writeWiki(t, dir, "memory/one-cursor.md", "# One cursor\n\nItems share one cursor.\n")
	writeWiki(t, dir, ".git/description.md", "# Inside the git directory\n")
	writeWiki(t, dir, "notes.txt", "# No page\n")
	writeWiki(t, dir, "big.md", "# Big\n\n"+strings.Repeat("word ", maxWikiFileBytes/5+1))
	outside := filepath.Join(t.TempDir(), "outside.md")
	writeFile(t, outside, "# Outside the clone\n")
	if err := os.Symlink(outside, filepath.Join(mirror.WikiDir(dir), "link.md")); err != nil {
		t.Skipf("no symbolic links here: %v", err)
	}

	ix := openIndex(t, dir)
	if want := []string{"wiki/Home.md", "wiki/memory/one-cursor.md"}; !slices.Equal(indexedPaths(t, ix), want) {
		t.Errorf("indexed paths = %v, want %v", indexedPaths(t, ix), want)
	}
	for _, word := range []string{"inside", "page", "big", "outside"} {
		if files := searchFiles(t, ix, Query{Text: word}); len(files) != 0 {
			t.Errorf("search for %q found %v, want nothing: its file is not a page of the clone", word, files)
		}
	}
}

// TestOpen_LeavesOutAWikiEntryWithALineFeedInItsName covers a name git
// permits and the output cannot carry: the path of a file is part of a hit and
// of the id of a block, so a line feed in it would let a page forge lines of
// the output.
func TestOpen_LeavesOutAWikiEntryWithALineFeedInItsName(t *testing.T) {
	dir := newMirror(t)
	writeWiki(t, dir, "Home.md", "# Home\n\nWelcome.\n")
	wiki := mirror.WikiDir(dir)
	if err := os.WriteFile(filepath.Join(wiki, "two\nlines.md"), []byte("# Forged\n"), 0o600); err != nil {
		t.Skipf("no line feed in a file name here: %v", err)
	}
	writeWiki(t, dir, "two\nlines/Nested.md", "# Nested\n")

	logs := captureLogs(t)
	ix := openIndex(t, dir)
	if want := []string{"wiki/Home.md"}; !slices.Equal(indexedPaths(t, ix), want) {
		t.Errorf("indexed paths = %q, want %q", indexedPaths(t, ix), want)
	}
	for _, word := range []string{"forged", "nested"} {
		if files := searchFiles(t, ix, Query{Text: word}); len(files) != 0 {
			t.Errorf("search for %q found %q, want nothing", word, files)
		}
	}
	// The page is left out on purpose, so no warning counts it as unreadable.
	if strings.Contains(logs.String(), "skipped mirror files") {
		t.Errorf("log = %q, want no warning about skipped files", logs)
	}
}

func TestOpen_ARewrittenFileIsIndexedAgain(t *testing.T) {
	dir := newMirror(t)
	threeFiles(t, dir)
	reopen(t, dir, 3, 0)

	writeItem(t, dir, testIssue(7, "Pin the base image", "Pin it to a digest."))
	later(t, mirror.ItemPath(dir, github.ItemKindIssue, 7))

	ix := openIndex(t, dir)
	if ix.Indexed != 1 || ix.Removed != 0 {
		t.Fatalf("Open indexed %d and removed %d files, want 1 and 0", ix.Indexed, ix.Removed)
	}
	if got, want := searchFiles(t, ix, Query{Text: "digest", Types: []string{"issue"}}), []string{"issues/7.md"}; !slices.Equal(got, want) {
		t.Errorf("search for a word of the new body found %v, want %v", got, want)
	}
	// Only the old body held "twice".
	if got := searchFiles(t, ix, Query{Text: "twice"}); len(got) != 0 {
		t.Errorf("search for a word of the old body found %v, want nothing", got)
	}
}

// TestOpen_ReplacesTheBlocksOfARewrittenFileOnly rewrites the file in the
// middle of the index with more blocks, then with fewer: every block of the
// two other files stays readable under its id, and no block of the old text
// is left.
func TestOpen_ReplacesTheBlocksOfARewrittenFileOnly(t *testing.T) {
	dir := newMirror(t)
	threeFiles(t, dir)
	reopen(t, dir, 3, 0)
	pull := mirror.ItemPath(dir, github.ItemKindPull, 9)

	check := func(t *testing.T, ix *Index, pullBlocks []string) {
		t.Helper()
		want := map[string]string{
			"issues/7.md:1":  "Pin the base image",
			"issues/7.md:2":  "The tag moved twice last month.",
			"wiki/Home.md:1": "# Home\n\nWelcome.\n",
		}
		for i, text := range pullBlocks {
			want[fmt.Sprintf("pulls/9.md:%d", i+1)] = text
		}
		for id, text := range want {
			if got, err := ix.Block(id); err != nil || got.Text != text {
				t.Errorf("Block(%s) = %q, %v, want %q", id, got.Text, err, text)
			}
		}
		if _, err := ix.Block(fmt.Sprintf("pulls/9.md:%d", len(pullBlocks)+1)); !errors.Is(err, ErrNoBlock) {
			t.Errorf("Block past the last one of the pull request = %v, want ErrNoBlock", err)
		}
		if blocks, counted := countBlocks(t, ix); blocks != len(want) || counted != len(want) {
			t.Errorf("the index holds %d blocks, its files count %d, want %d", blocks, counted, len(want))
		}
	}

	longer := testPull(9, "Pin the base image to a digest", "Closes #7")
	longer.Comments = []github.ItemComment{{Author: "alice", Body: "First."}, {Author: "bob", Body: "Second."}}
	writeItem(t, dir, longer)
	later(t, pull)
	ix := openIndex(t, dir)
	if ix.Indexed != 1 || ix.Removed != 0 {
		t.Fatalf("Open indexed %d and removed %d files, want 1 and 0", ix.Indexed, ix.Removed)
	}
	check(t, ix, []string{"Pin the base image to a digest", "Closes #7", "First.", "Second."})

	// An item without a body is one block.
	writeItem(t, dir, testPull(9, "Pin the digest", ""))
	at := time.Now().Add(2 * time.Hour)
	if err := os.Chtimes(pull, at, at); err != nil {
		t.Fatal(err)
	}
	ix = openIndex(t, dir)
	if ix.Indexed != 1 || ix.Removed != 0 {
		t.Fatalf("Open indexed %d and removed %d files, want 1 and 0", ix.Indexed, ix.Removed)
	}
	check(t, ix, []string{"Pin the digest"})
}

// TestStatements_FindBlocksByRowid pins how the statements that touch the
// blocks of one file find them. FTS5 has an index for a rowid and for a
// match, and for no other column, so a statement that names neither reads
// every block of the index: once per changed file in a refresh, and once per
// block a session reads.
func TestStatements_FindBlocksByRowid(t *testing.T) {
	dir := newMirror(t)
	threeFiles(t, dir)
	ix := openIndex(t, dir)

	for name, stmt := range map[string]struct {
		query string
		args  []any
	}{
		"the delete of a file's blocks": {blockRangeDelete, []any{1, 2}},
		"the read of one block":         {blockSelect, []any{"issues/7.md", 2}},
		"the read of one hit":           {hitSelect, []any{`"pin"`, 1}},
	} {
		t.Run(name, func(t *testing.T) {
			rows, err := ix.db.Query("EXPLAIN QUERY PLAN "+stmt.query, stmt.args...)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = rows.Close() }()
			var steps []string
			for rows.Next() {
				var id, parent, unused int
				var detail string
				if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
					t.Fatal(err)
				}
				steps = append(steps, detail)
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			// The plan of a virtual table ends in the constraints its index
			// takes: "=" for a rowid, "><" for a rowid range, "M" for a match,
			// and nothing for a read of every row.
			for _, step := range steps {
				if strings.Contains(step, "VIRTUAL TABLE") && strings.HasSuffix(step, "INDEX 0:") {
					t.Errorf("the plan reads every block: %q", steps)
				}
			}
			if !slices.ContainsFunc(steps, func(step string) bool { return strings.Contains(step, "VIRTUAL TABLE") }) {
				t.Errorf("the plan names no step on the blocks: %q", steps)
			}
		})
	}
}

func TestOpen_ADeletedFileLeavesTheIndex(t *testing.T) {
	dir := newMirror(t)
	threeFiles(t, dir)
	reopen(t, dir, 3, 0)

	if err := os.Remove(mirror.ItemPath(dir, github.ItemKindIssue, 7)); err != nil {
		t.Fatal(err)
	}
	ix := openIndex(t, dir)
	if ix.Indexed != 0 || ix.Removed != 1 {
		t.Fatalf("Open indexed %d and removed %d files, want 0 and 1", ix.Indexed, ix.Removed)
	}
	if got := searchFiles(t, ix, Query{Text: "twice"}); len(got) != 0 {
		t.Errorf("search for the text of the deleted file found %v, want nothing", got)
	}
	if got, want := searchFiles(t, ix, Query{Text: "digest"}), []string{"pulls/9.md"}; !slices.Equal(got, want) {
		t.Errorf("search for the text of a kept file found %v, want %v", got, want)
	}
}

func TestOpen_IndexesMoreFilesThanOneBatch(t *testing.T) {
	const n = refreshBatchFiles + 1
	dir := newMirror(t)
	for i := 1; i <= n; i++ {
		writeItem(t, dir, testIssue(i, fmt.Sprintf("Topic %d", i), "A body."))
	}

	ix := openIndex(t, dir)
	if ix.Indexed != n || ix.Removed != 0 {
		t.Fatalf("Open indexed %d and removed %d files, want %d and 0", ix.Indexed, ix.Removed, n)
	}
	if got := len(indexedPaths(t, ix)); got != n {
		t.Errorf("the index holds %d files, want %d", got, n)
	}
	// A title and a body per file.
	if blocks, counted := countBlocks(t, ix); blocks != 2*n || counted != 2*n {
		t.Errorf("the index holds %d blocks, its files count %d, want %d", blocks, counted, 2*n)
	}
	// The file after the last full batch is in the index too.
	for _, id := range []string{"issues/1.md:2", fmt.Sprintf("issues/%d.md:1", n)} {
		if _, err := ix.Block(id); err != nil {
			t.Errorf("Block(%s): %v", id, err)
		}
	}
	reopen(t, dir, 0, 0)
}

// TestOpen_ConcurrentOpensOfANewIndex proves that eight callers that create
// and fill one index at once all succeed and leave every block once.
func TestOpen_ConcurrentOpensOfANewIndex(t *testing.T) {
	dir := newMirror(t)
	writeItem(t, dir, testIssue(7, "Pin the base image", "A shared word."))
	writeItem(t, dir, testPull(9, "Pin the digest", "The shared word again."))
	writeWiki(t, dir, "Home.md", "# Home\n\nShared once more.\n")

	const callers = 8
	errs := make(chan error, callers)
	for range callers {
		go func() {
			ix, err := Open(dir)
			if err == nil {
				err = ix.Close()
			}
			errs <- err
		}()
	}
	for range callers {
		if err := <-errs; err != nil {
			t.Errorf("Open: %v", err)
		}
	}

	ix := openIndex(t, dir)
	if ix.Indexed != 0 || ix.Removed != 0 {
		t.Errorf("Open after the eight indexed %d and removed %d files, want 0 and 0", ix.Indexed, ix.Removed)
	}
	got := searchFiles(t, ix, Query{Text: "shared"})
	slices.Sort(got)
	if want := []string{"issues/7.md", "pulls/9.md", "wiki/Home.md"}; !slices.Equal(got, want) {
		t.Errorf("search found %v, want each item once: %v", got, want)
	}
	// Two refreshes that wrote the same file must not have left its blocks
	// twice.
	if blocks, counted := countBlocks(t, ix); blocks != counted {
		t.Errorf("the index holds %d blocks, its files count %d", blocks, counted)
	}
}

func TestOpen_StoresRedactedText(t *testing.T) {
	token := "ghp_" + strings.Repeat("a", 36)
	it := testIssue(7, "A leaked credential", "See the comment.")
	it.Comments = []github.ItemComment{{Author: "mallory", Body: "The credential " + token + " was pasted here."}}
	dir := newMirror(t)
	writeItem(t, dir, it)
	ix := openIndex(t, dir)

	if got := searchFiles(t, ix, Query{Text: token}); len(got) != 0 {
		t.Errorf("search for the token found %v, want no hit", got)
	}
	hits, err := ix.Search(Query{Text: "pasted"})
	if err != nil || len(hits) != 1 {
		t.Fatalf("Search = %v, %v, want the comment", hits, err)
	}
	if strings.Contains(hits[0].Excerpt, token) {
		t.Errorf("the excerpt holds the token: %q", hits[0].Excerpt)
	}
	block, err := ix.Block(hits[0].ID)
	if err != nil {
		t.Fatalf("Block: %v", err)
	}
	if !strings.Contains(block.Text, "[REDACTED:github-token]") || strings.Contains(block.Text, token) {
		t.Errorf("block text = %q, want the marker and not the token", block.Text)
	}
}

func TestOpen_StoresARedactedTitle(t *testing.T) {
	token := "ghp_" + strings.Repeat("a", 36)
	dir := newMirror(t)
	writeItem(t, dir, testIssue(7, "The token "+token+" stopped working", "See the gizmo."))
	writeWiki(t, dir, "Leak.md", "# Deploy key "+token+"\n\nThe gizmo page.\n")
	ix := openIndex(t, dir)

	hits, err := ix.Search(Query{Text: "gizmo"})
	if err != nil || len(hits) != 2 {
		t.Fatalf("Search = %v, %v, want the issue and the page", hits, err)
	}
	for _, h := range hits {
		block, err := ix.Block(h.ID)
		if err != nil {
			t.Fatalf("Block(%s): %v", h.ID, err)
		}
		for _, title := range []string{h.Title, block.Title} {
			if strings.Contains(title, token) || !strings.Contains(title, "[REDACTED:github-token]") {
				t.Errorf("title of %s = %q, want the marker and not the token", h.File, title)
			}
		}
	}
}

func TestOpen_CutsAPullRequestIntoBlocks(t *testing.T) {
	it := testPull(9, "Pin the base image to a digest", "Closes #7")
	it.Labels = []string{"Docker"}
	it.Comments = []github.ItemComment{{
		URL: "https://github.com/acme/widgets/pull/9#issuecomment-3", Author: "alice", AuthorAssociation: "MEMBER",
		CreatedAt: "2026-09-03T09:00:00Z", Body: "Thanks.",
	}}
	// A review without a summary is no block.
	it.Reviews = []github.ItemReview{
		{Author: "alice", AuthorAssociation: "MEMBER", State: "APPROVED", SubmittedAt: "2026-09-03T10:00:00Z"},
		{
			URL: "https://github.com/acme/widgets/pull/9#pullrequestreview-4", Author: "carol", AuthorAssociation: "OWNER",
			State: "CHANGES_REQUESTED", SubmittedAt: "2026-09-03T10:30:00Z", Body: "Pin the digest, not the tag.",
		},
	}
	// A thread without a diff hunk has no block of its own, only its comments.
	it.Threads = []github.ItemThread{
		{
			Path: "Dockerfile", Line: 1, DiffHunk: "@@ -1 +1 @@\n-FROM alpine:3\n+FROM alpine:3.21",
			Comments: []github.ItemComment{{
				URL: "https://github.com/acme/widgets/pull/9#discussion_r1", Author: "alice", AuthorAssociation: "MEMBER",
				CreatedAt: "2026-09-03T11:00:00Z", Body: "A tag can move.",
			}},
		},
		{
			Path: "README.md",
			Comments: []github.ItemComment{{
				URL: "https://github.com/acme/widgets/pull/9#discussion_r2", Author: "carol", AuthorAssociation: "OWNER",
				CreatedAt: "2026-09-03T12:00:00Z", Body: "Name the digest here.",
			}},
		},
	}
	it.Commits = []github.ItemCommit{{SHA: "1111111", CommittedAt: "2026-09-03T08:55:00Z", Headline: "Pin the base image", Body: "The tag moved twice."}}
	dir := newMirror(t)
	writeItem(t, dir, it)
	ix := openIndex(t, dir)

	want := []Hit{
		{Text: "Pin the base image to a digest", Block: Block{Kind: "title", Author: "bob", Association: "CONTRIBUTOR", URL: it.URL, CreatedAt: it.CreatedAt}},
		{Text: "Closes #7", Block: Block{Kind: "body", Author: "bob", Association: "CONTRIBUTOR", URL: it.URL, CreatedAt: it.CreatedAt}},
		{Text: "Thanks.", Block: Block{Kind: "comment", Author: "alice", Association: "MEMBER", URL: "https://github.com/acme/widgets/pull/9#issuecomment-3", CreatedAt: "2026-09-03T09:00:00Z"}},
		{Text: "Pin the digest, not the tag.", Block: Block{Kind: "review", Author: "carol", Association: "OWNER", URL: "https://github.com/acme/widgets/pull/9#pullrequestreview-4", CreatedAt: "2026-09-03T10:30:00Z"}},
		{Text: "Dockerfile\n@@ -1 +1 @@\n-FROM alpine:3\n+FROM alpine:3.21", Block: Block{Kind: "thread"}},
		{Text: "A tag can move.", Block: Block{Kind: "thread-comment", Author: "alice", Association: "MEMBER", URL: "https://github.com/acme/widgets/pull/9#discussion_r1", CreatedAt: "2026-09-03T11:00:00Z"}},
		{Text: "Name the digest here.", Block: Block{Kind: "thread-comment", Author: "carol", Association: "OWNER", URL: "https://github.com/acme/widgets/pull/9#discussion_r2", CreatedAt: "2026-09-03T12:00:00Z"}},
		{Text: "Pin the base image\n\nThe tag moved twice.", Block: Block{Kind: "commit", CreatedAt: "2026-09-03T08:55:00Z"}},
	}
	for i, w := range want {
		id := fmt.Sprintf("pulls/9.md:%d", i+1)
		w.ID, w.File, w.Type, w.Number = id, "pulls/9.md", "pull", 9
		w.Title, w.State, w.Labels, w.URL, w.UpdatedAt = it.Title, "merged", []string{"docker"}, it.URL, it.UpdatedAt
		w.Block.Ordinal, w.Block.Count = i+1, len(want)
		got, err := ix.Block(id)
		if err != nil {
			t.Fatalf("Block(%s): %v", id, err)
		}
		if !reflect.DeepEqual(got, w) {
			t.Errorf("Block(%s) =\n%+v\nwant\n%+v", id, got, w)
		}
	}
	if _, err := ix.Block("pulls/9.md:9"); !errors.Is(err, ErrNoBlock) {
		t.Errorf("Block past the last one = %v, want ErrNoBlock", err)
	}
}

func TestOpen_CutsAWikiPageIntoSections(t *testing.T) {
	dir := newMirror(t)
	writeWiki(t, dir, "memory/one-cursor.md", "# One cursor\n\nItems share one cursor.\n\n## Why\n\nThe update time rises.\n\n## How\n\nOne listing.\n")
	writeWiki(t, dir, "notes/plain.md", "No heading here.\n")
	ix := openIndex(t, dir)

	for i, text := range []string{
		"# One cursor\n\nItems share one cursor.\n\n",
		"## Why\n\nThe update time rises.\n\n",
		"## How\n\nOne listing.\n",
	} {
		id := fmt.Sprintf("wiki/memory/one-cursor.md:%d", i+1)
		want := Hit{
			ID: id, File: "wiki/memory/one-cursor.md", Type: "wiki", Title: "One cursor", Labels: []string{},
			Block: Block{Ordinal: i + 1, Count: 3, Kind: "section"}, Text: text,
		}
		got, err := ix.Block(id)
		if err != nil {
			t.Fatalf("Block(%s): %v", id, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("Block(%s) =\n%+v\nwant\n%+v", id, got, want)
		}
	}

	// A page without a heading is one block, titled by its path in the clone.
	plain, err := ix.Block("wiki/notes/plain.md:1")
	if err != nil {
		t.Fatalf("Block: %v", err)
	}
	if plain.Title != "notes/plain.md" || plain.Block.Count != 1 || plain.Text != "No heading here.\n" {
		t.Errorf("block of a page without a heading = %+v", plain)
	}
}
