// Package search keeps a full-text index of a mirror directory and answers
// ranked keyword queries over it.
//
// The index is the file index.sqlite in the mirror directory: a SQLite
// database with an FTS5 table of blocks. A block is one text of an issue or a
// pull request (its title, its body, a comment, a review, the diff hunk of a
// review thread, a thread comment, a commit message) or one section of a wiki
// page. The index is a derivative of the mirror: Open rebuilds it when it is
// missing, damaged, or was built by other rules, and deleting it loses
// nothing.
//
// The mirror files hold GitHub's text unchanged. The index holds redacted
// text, because an excerpt cut from unredacted text can cut through a secret,
// and no redaction rule matches the remainder. What a query returns is
// redacted, and it is still text that everyone who can comment on the
// repository wrote.
package search

import (
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode"

	"modernc.org/sqlite" // registers the "sqlite" driver of database/sql
	sqlite3 "modernc.org/sqlite/lib"

	"github.com/planwerk/planwerk-agent/internal/github"
	"github.com/planwerk/planwerk-agent/internal/mirror"
	"github.com/planwerk/planwerk-agent/internal/redact"
)

// IndexFileName is the name of the index file in a mirror directory.
const IndexFileName = "index.sqlite"

// indexVersion is the version of the index. A change to the schema or to how
// blocks are cut raises it, and Open then rebuilds every index.
const indexVersion = 2

// maxWikiFileBytes is the size above which a wiki page is not indexed.
const maxWikiFileBytes = 1 << 20

// refreshBatchFiles is the largest number of files one transaction of a
// refresh writes.
const refreshBatchFiles = 500

// lockWait is how long a connection waits for a lock another process holds.
const lockWait = 10 * time.Second

// dsn returns the data source name of the index at path: the plain path and
// the parameters. With a "file:" prefix, a path that holds "#" or a space
// opens another file. A connection waits up to wait for a lock, and a
// transaction takes the write lock when it begins, so two writers queue and
// neither fails on an upgrade.
func dsn(path string, wait time.Duration) string {
	return fmt.Sprintf("%s?_pragma=busy_timeout(%d)&_txlock=immediate", path, wait.Milliseconds())
}

// TypeWiki is the value of the type column of a wiki page, and of its
// Hit.Type. An item's type is its github.ItemKindIssue or github.ItemKindPull.
const TypeWiki = "wiki"

// The kinds of block. The kinds of an item's blocks are those of its file,
// with the title as a block of its own.
const (
	kindTitle         = "title"
	kindBody          = "body"
	kindComment       = "comment"
	kindReview        = "review"
	kindThread        = "thread"
	kindThreadComment = "thread-comment"
	kindCommit        = "commit"
	kindSection       = "section"
)

// schema replaces the tables of an index: it drops what another version left
// and creates the tables of this one. files holds one row per indexed mirror
// file and blocks the texts that are searched.
//
// path is a column of blocks that FTS5 keeps no index for, so a statement
// finds the blocks of a file by their rowids: they are first_block and the
// block_count - 1 rowids after it.
var schema = []string{
	`DROP TABLE IF EXISTS blocks`,
	`DROP TABLE IF EXISTS files`,
	`DROP TABLE IF EXISTS meta`,
	`CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL) WITHOUT ROWID`,
	`CREATE TABLE files (
  path TEXT PRIMARY KEY,        -- relative to the mirror directory, with "/"
  size INTEGER NOT NULL,
  mtime_ns INTEGER NOT NULL,
  type TEXT NOT NULL,           -- issue, pull, wiki
  number INTEGER NOT NULL,      -- 0 for a wiki page
  title TEXT NOT NULL,
  state TEXT NOT NULL,          -- "" for a wiki page
  labels TEXT NOT NULL,         -- "\n", then every label in lowercase followed by "\n"
  url TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  block_count INTEGER NOT NULL,
  first_block INTEGER NOT NULL  -- the rowid of the first block, 0 for a file without one
) WITHOUT ROWID`,
	`CREATE VIRTUAL TABLE blocks USING fts5(
  text,
  path UNINDEXED, ordinal UNINDEXED, kind UNINDEXED, author UNINDEXED,
  association UNINDEXED, url UNINDEXED, created_at UNINDEXED,
  tokenize = "unicode61 remove_diacritics 2"
)`,
}

// errOtherVersion reports an index that another version of the index or
// another set of redaction rules built.
var errOtherVersion = errors.New("the index was built by other rules")

// Index is the search index of one mirror directory. Indexed and Removed are
// the numbers of files the refresh in Open indexed and removed.
type Index struct {
	Indexed, Removed int

	db  *sql.DB
	dir string
}

// Open opens the index of the mirror directory dir, creates or rebuilds it
// when it is missing or was built by other rules, and brings it up to date
// with the files.
//
// An index whose version row names another index version or other redaction
// rules is emptied and built again, inside the file. A file that is no
// database, or a damaged one, is removed and built again, once. Every other
// failure to open the index is returned and leaves the file alone: a lock
// another process holds past lockWait is no damage. An index that is up to
// date is opened without a write.
func Open(dir string) (*Index, error) {
	return open(dir, lockWait)
}

// open is Open over the time a connection waits for a lock.
func open(dir string, wait time.Duration) (*Index, error) {
	path := filepath.Join(dir, IndexFileName)
	if err := createIndexFile(path); err != nil {
		return nil, err
	}
	db, err := openDB(path, wait)
	if err != nil {
		if !damaged(err) {
			return nil, fmt.Errorf("opening the search index %s: %w", path, err)
		}
		slog.Info("rebuilding the search index", "path", path, "reason", err)
		if err := removeIndexFile(path); err != nil {
			return nil, fmt.Errorf("opening the search index %s: %w", path, err)
		}
		if err := createIndexFile(path); err != nil {
			return nil, err
		}
		if db, err = openDB(path, wait); err != nil {
			return nil, fmt.Errorf("opening the search index %s: %w", path, err)
		}
	}

	ix := &Index{db: db, dir: dir}
	if err := ix.refresh(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("updating the search index %s: %w", path, err)
	}
	return ix, nil
}

// damaged reports whether err says that the file is no SQLite database, or a
// damaged one. No other error is a reason to remove the file: one that says a
// lock timed out, or that the file cannot be opened or read, leaves an index
// another process may be writing, and that process's journal, in place.
func damaged(err error) bool {
	var se *sqlite.Error
	if !errors.As(err, &se) {
		return false
	}
	// The low byte of an extended result code is the primary code.
	switch se.Code() & 0xff {
	case sqlite3.SQLITE_NOTADB, sqlite3.SQLITE_CORRUPT:
		return true
	}
	return false
}

// Close closes the index.
func (ix *Index) Close() error {
	return ix.db.Close()
}

// createIndexFile creates the index file at path, empty and with mode 0600,
// when there is none: the driver would create it with mode 0644. An empty file
// opens as an empty database. A file that exists is left as it is, whatever
// its mode.
func createIndexFile(path string) error {
	if _, err := os.Lstat(path); !errors.Is(err, fs.ErrNotExist) {
		// A file that cannot be inspected fails when the database is opened.
		return nil
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("creating the search index %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("creating the search index %s: %w", path, err)
	}
	return nil
}

// removeIndexFile removes the index file at path and its rollback journal.
func removeIndexFile(path string) error {
	for _, p := range []string{path, path + "-journal"} {
		if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return nil
}

// indexVersionValue is the value of the version row of an index this binary
// writes: the index version and the fingerprint of the redaction rules.
func indexVersionValue() string {
	return fmt.Sprintf("%d/%s", indexVersion, redact.Fingerprint())
}

// openDB opens the database at path and returns it once it holds the tables
// and the version row this binary writes (prepareSchema).
func openDB(path string, wait time.Duration) (*sql.DB, error) {
	db, err := sql.Open("sqlite", dsn(path, wait))
	if err != nil {
		return nil, err
	}
	emptied, err := prepareSchema(db)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	if emptied {
		slog.Info("rebuilding the search index", "path", path, "reason", errOtherVersion)
	}
	return db, nil
}

// queryer is what *sql.DB and *sql.Tx share that versionRow reads through.
type queryer interface {
	QueryRow(query string, args ...any) *sql.Row
}

// versionRow returns the value of the version row, "" for a database without
// the row, and whether the database has the table that holds it.
func versionRow(q queryer) (value string, tables bool, err error) {
	var count int
	if err := q.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'meta'`).Scan(&count); err != nil {
		return "", false, err
	}
	if count == 0 {
		return "", false, nil
	}
	if err := q.QueryRow(`SELECT value FROM meta WHERE key = 'version'`).Scan(&value); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", false, err
	}
	return value, true, nil
}

// prepareSchema leaves the database with the tables and the version row of
// this binary. A database that has both is only read. Every other one is
// written (writeSchema), and emptied reports that the write dropped an index
// another version or other redaction rules built. It fails for a file that is
// no database and for a statement that fails.
func prepareSchema(db *sql.DB) (emptied bool, err error) {
	want := indexVersionValue()
	got, _, err := versionRow(db)
	if err != nil {
		return false, err
	}
	if got == want {
		return false, nil
	}
	return writeSchema(db, want)
}

// writeSchema writes the tables and the version row in one transaction, and
// drops the tables and the rows that were there. The transaction takes the
// write lock when it begins and reads the version row again under it, so of
// two processes that find an index to create or to replace, the second waits
// for the first and then changes nothing: neither removes what the other
// built. emptied reports that there were tables to drop.
func writeSchema(db *sql.DB, version string) (emptied bool, err error) {
	tx, err := db.Begin()
	if err != nil {
		return false, err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	got, tables, err := versionRow(tx)
	if err != nil {
		return false, err
	}
	if got == version {
		return false, tx.Rollback()
	}
	for _, stmt := range schema {
		if _, err := tx.Exec(stmt); err != nil {
			return false, err
		}
	}
	if _, err := tx.Exec(`INSERT INTO meta (key, value) VALUES ('version', ?)`, version); err != nil {
		return false, err
	}
	return tables, tx.Commit()
}

// fileStat is what tells a changed file from an unchanged one: its size and
// its modification time in nanoseconds. The mirror replaces a file through a
// rename, so a rewritten file has a new time.
type fileStat struct {
	size, mtime int64
}

// mirrorFile is one file of the mirror directory that belongs in the index.
type mirrorFile struct {
	path string // relative to the mirror directory, with "/"
	abs  string
	stat fileStat
	wiki bool
}

// block is one text of a document with what a hit prints about it.
type block struct {
	kind, text, author, association, url, createdAt string
}

// document is a mirror file as the index stores it: the row of files and the
// blocks, in order.
type document struct {
	file      mirrorFile
	typ       string
	number    int
	title     string
	state     string
	labels    string
	url       string
	updatedAt string
	blocks    []block
}

// refresh brings the index up to date with the files of the mirror directory.
// A file whose size and modification time equal its row is skipped, a new or
// changed one is read and its rows are replaced, and a row whose file is gone
// is deleted with its blocks. A file that cannot be read or parsed gets no
// row, so the next refresh tries it again; a row it had is deleted.
func (ix *Index) refresh() error {
	files, skipped, err := listMirrorFiles(ix.dir)
	if err != nil {
		return err
	}
	rows, err := ix.fileRows()
	if err != nil {
		return err
	}

	// After the loop, rows holds the rows no file of the mirror stands for.
	var batch []document
	for _, f := range files {
		if stat, ok := rows[f.path]; ok && stat == f.stat {
			delete(rows, f.path)
			continue
		}
		doc, err := readDocument(f)
		if err != nil {
			slog.Debug("skipping a mirror file that could not be indexed", "path", f.path, "err", err)
			skipped++
			continue
		}
		delete(rows, f.path)
		batch = append(batch, doc)
		if len(batch) == refreshBatchFiles {
			if err := ix.write(batch, nil); err != nil {
				return err
			}
			batch = batch[:0]
		}
	}
	gone := slices.Sorted(maps.Keys(rows))
	if len(batch) > 0 || len(gone) > 0 {
		if err := ix.write(batch, gone); err != nil {
			return err
		}
	}

	if skipped > 0 {
		slog.Warn("skipped mirror files that could not be indexed; run brain sync --full if this persists", "files", skipped)
	}
	if ix.Indexed > 0 || ix.Removed > 0 {
		slog.Info("updated the search index", "indexed", ix.Indexed, "removed", ix.Removed)
	}
	return nil
}

// fileRows returns the size and the modification time the index holds for
// every file, by path.
func (ix *Index) fileRows() (map[string]fileStat, error) {
	rows, err := ix.db.Query(`SELECT path, size, mtime_ns FROM files`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	stats := map[string]fileStat{}
	for rows.Next() {
		var path string
		var stat fileStat
		if err := rows.Scan(&path, &stat.size, &stat.mtime); err != nil {
			return nil, err
		}
		stats[path] = stat
	}
	return stats, rows.Err()
}

// write replaces the rows of docs and deletes the rows of the paths in gone,
// in one transaction, and counts both. Two refreshes that write the same file
// at once leave its blocks once: the second finds the row of the first and
// deletes the blocks it names.
func (ix *Index) write(docs []document, gone []string) (err error) {
	tx, err := ix.db.Begin()
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	w, err := newWriter(tx)
	if err != nil {
		return err
	}

	for _, doc := range docs {
		if err := w.replace(doc); err != nil {
			return err
		}
	}
	for _, path := range gone {
		if err := w.deleteBlocks(path); err != nil {
			return err
		}
		if _, err := w.deleteFile.Exec(path); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	ix.Indexed += len(docs)
	ix.Removed += len(gone)
	return nil
}

// blockRangeDelete is the statement that deletes the blocks of one file, by
// their first and their last rowid. A rowid range is what FTS5 has an index
// for; a condition on the path column reads every block of the index.
const blockRangeDelete = `DELETE FROM blocks WHERE rowid BETWEEN ? AND ?`

// writer holds the statements of one write transaction, so each is compiled
// once however many files and blocks the transaction writes. A statement
// prepared on a transaction is closed with it.
type writer struct {
	fileBlocks, deleteRange, insertBlock, insertFile, deleteFile *sql.Stmt
}

// newWriter prepares the statements of a writer on tx.
func newWriter(tx *sql.Tx) (*writer, error) {
	w := &writer{}
	for _, s := range []struct {
		stmt  **sql.Stmt
		query string
	}{
		{&w.fileBlocks, `SELECT first_block, block_count FROM files WHERE path = ?`},
		{&w.deleteRange, blockRangeDelete},
		{&w.insertBlock, `INSERT INTO blocks
  (rowid, text, path, ordinal, kind, author, association, url, created_at)
  VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`},
		{&w.insertFile, `INSERT OR REPLACE INTO files
  (path, size, mtime_ns, type, number, title, state, labels, url, updated_at, block_count, first_block)
  VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`},
		{&w.deleteFile, `DELETE FROM files WHERE path = ?`},
	} {
		stmt, err := tx.Prepare(s.query)
		if err != nil {
			return nil, err
		}
		*s.stmt = stmt
	}
	return w, nil
}

// deleteBlocks deletes the blocks of the file at path: the rowids its row
// names. A path without a row has no block, because both are written together,
// so a first build deletes nothing.
func (w *writer) deleteBlocks(path string) error {
	var first, count int64
	err := w.fileBlocks.QueryRow(path).Scan(&first, &count)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && count == 0) {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = w.deleteRange.Exec(first, first+count-1)
	return err
}

// replace writes the blocks and the row of doc in place of the ones its path
// had. The engine picks the rowid of the first block, above every rowid in
// use, and each block after it takes the next one, so the row names all of
// them by the first rowid and the count.
func (w *writer) replace(doc document) error {
	if err := w.deleteBlocks(doc.file.path); err != nil {
		return err
	}
	var first int64
	for i, b := range doc.blocks {
		var rowid any // NULL lets the engine pick
		if i > 0 {
			rowid = first + int64(i)
		}
		res, err := w.insertBlock.Exec(rowid, b.text, doc.file.path, i+1, b.kind, b.author, b.association, b.url, b.createdAt)
		if err != nil {
			return err
		}
		if i == 0 {
			if first, err = res.LastInsertId(); err != nil {
				return err
			}
		}
	}
	_, err := w.insertFile.Exec(doc.file.path, doc.file.stat.size, doc.file.stat.mtime, doc.typ, doc.number, doc.title,
		doc.state, doc.labels, doc.url, doc.updatedAt, len(doc.blocks), first)
	return err
}

// listMirrorFiles returns the files of the mirror directory dir that belong in
// the index: every item file and every page of the wiki clone. skipped counts
// the entries that could not be inspected.
//
// A wiki page is a regular file whose name ends in ".md", outside every
// directory named ".git", of at most maxWikiFileBytes. The walk does not
// follow a symbolic link, so a link in the wiki cannot lead out of the clone.
// An entry whose name holds a control character is left out, with what is
// below it: git permits a line feed in a name, and a path that holds one would
// let a hit, and the id of a block, span lines of the output. A missing wiki
// directory holds no page.
func listMirrorFiles(dir string) (files []mirrorFile, skipped int, err error) {
	add := func(path string, info fs.FileInfo, wiki bool) error {
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		files = append(files, mirrorFile{
			path: filepath.ToSlash(rel),
			abs:  path,
			stat: fileStat{size: info.Size(), mtime: info.ModTime().UnixNano()},
			wiki: wiki,
		})
		return nil
	}

	for _, kind := range []string{github.ItemKindIssue, github.ItemKindPull} {
		paths, err := mirror.ItemFiles(dir, kind)
		if err != nil {
			return nil, 0, err
		}
		for _, path := range paths {
			info, err := os.Stat(path)
			if err != nil {
				slog.Debug("skipping a mirror file that could not be inspected", "err", err)
				skipped++
				continue
			}
			if err := add(path, info, false); err != nil {
				return nil, 0, err
			}
		}
	}

	root := mirror.WikiDir(dir)
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path == root && errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			slog.Debug("skipping a wiki entry that could not be inspected", "err", err)
			skipped++
			return nil
		}
		if strings.ContainsFunc(d.Name(), unicode.IsControl) {
			slog.Debug("leaving a wiki entry out of the search index: its name holds a control character")
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() || !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			slog.Debug("skipping a wiki entry that could not be inspected", "err", err)
			skipped++
			return nil
		}
		if info.Size() > maxWikiFileBytes {
			slog.Debug("leaving a wiki page out of the search index: it is too large", "bytes", info.Size(), "max", maxWikiFileBytes)
			return nil
		}
		return add(path, info, true)
	})
	if err != nil {
		return nil, 0, err
	}
	return files, skipped, nil
}

// readDocument reads the mirror file f and cuts it into blocks.
func readDocument(f mirrorFile) (document, error) {
	data, err := os.ReadFile(f.abs)
	if err != nil {
		return document{}, err
	}
	if f.wiki {
		return wikiDocument(f, string(data)), nil
	}
	it, err := mirror.Parse(data)
	if err != nil {
		return document{}, err
	}
	return itemDocument(f, it), nil
}

// itemDocument returns the document of an issue or a pull request. Its blocks
// are the title, the body, and then, in the order of the file, the comments,
// the reviews, the review threads each followed by its comments, and the
// commits. A thread's block is its path and its diff hunk, and is left out
// when the hunk is empty. A commit's block is its headline, a blank line, and
// its body. Labels are stored in lowercase.
func itemDocument(f mirrorFile, it *github.Item) document {
	doc := document{
		file:      f,
		typ:       it.Kind,
		number:    it.Number,
		title:     redact.Redact(it.Title).Text,
		state:     it.State,
		url:       it.URL,
		updatedAt: it.UpdatedAt,
	}
	var labels strings.Builder
	labels.WriteString("\n")
	for _, l := range it.Labels {
		labels.WriteString(strings.ToLower(l) + "\n")
	}
	doc.labels = labels.String()

	comment := func(kind string, c github.ItemComment) {
		doc.add(block{kind: kind, text: c.Body, author: c.Author, association: c.AuthorAssociation, url: c.URL, createdAt: c.CreatedAt})
	}
	doc.add(block{kind: kindTitle, text: it.Title, author: it.Author, association: it.AuthorAssociation, url: it.URL, createdAt: it.CreatedAt})
	doc.add(block{kind: kindBody, text: it.Body, author: it.Author, association: it.AuthorAssociation, url: it.URL, createdAt: it.CreatedAt})
	for _, c := range it.Comments {
		comment(kindComment, c)
	}
	for _, r := range it.Reviews {
		doc.add(block{kind: kindReview, text: r.Body, author: r.Author, association: r.AuthorAssociation, url: r.URL, createdAt: r.SubmittedAt})
	}
	for _, t := range it.Threads {
		if t.DiffHunk != "" {
			doc.add(block{kind: kindThread, text: t.Path + "\n" + t.DiffHunk})
		}
		for _, c := range t.Comments {
			comment(kindThreadComment, c)
		}
	}
	for _, c := range it.Commits {
		doc.add(block{kind: kindCommit, text: c.Headline + "\n\n" + c.Body, createdAt: c.CommittedAt})
	}
	return doc
}

// wikiDocument returns the document of a wiki page. Its title is the text of
// its first line that starts with "# ", or its path in the clone when no line
// does. Its content is cut before every line that starts with "# " or "## ",
// and each piece is a block. A line inside a code fence that starts with "# "
// also cuts, which costs a block boundary and nothing else.
func wikiDocument(f mirrorFile, content string) document {
	doc := document{file: f, typ: TypeWiki, labels: "\n"}
	var title string
	var titled bool
	var section strings.Builder
	for _, line := range strings.SplitAfter(content, "\n") {
		isTitle := strings.HasPrefix(line, "# ")
		if isTitle || strings.HasPrefix(line, "## ") {
			doc.add(block{kind: kindSection, text: section.String()})
			section.Reset()
		}
		if isTitle && !titled {
			title, titled = strings.TrimSpace(strings.TrimPrefix(line, "# ")), true
		}
		section.WriteString(line)
	}
	doc.add(block{kind: kindSection, text: section.String()})

	if title == "" {
		title = mirror.WikiPagePath(f.path)
	}
	doc.title = redact.Redact(title).Text
	return doc
}

// add appends b to the document with its text redacted. A block whose text is
// empty is left out, so the blocks that are kept number from 1 without a gap.
func (d *document) add(b block) {
	if strings.TrimSpace(b.text) == "" {
		return
	}
	b.text = redact.Redact(b.text).Text
	d.blocks = append(d.blocks, b)
}
