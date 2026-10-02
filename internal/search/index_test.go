package search

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
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
}
