package brain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/planwerk/planwerk-agent/internal/capture"
	"github.com/planwerk/planwerk-agent/internal/report"
	"github.com/planwerk/planwerk-agent/internal/sync"
)

// StateDirName is the directory `brain bootstrap` keeps its state in, inside
// the working directory the command runs in:
//
//	.planwerk-brain-sync/
//	├── .gitignore        one line, "*", so git never tracks the directory
//	├── state.json
//	├── pages/
//	│   ├── memory/<name>.md
//	│   └── review_patterns/<name>.md
//	└── orphans/          files found under pages/ without an entry in state.json
//
// A page file holds the page body without a provenance marker, with exactly
// one trailing newline.
const StateDirName = ".planwerk-brain-sync"

const (
	stateFileName  = "state.json"
	pagesDirName   = "pages"
	orphansDirName = "orphans"
	// memoryDirName and patternsDirName are the two directories of pages, in
	// the working set as on the wiki.
	memoryDirName   = "memory"
	patternsDirName = "review_patterns"
	// stateVersion is the version of the state.json format this build reads
	// and writes.
	stateVersion = 1
)

// State is the content of state.json: which units a bootstrap has processed,
// what it knows about every page of the working set, and what it spent.
type State struct {
	Version int `json:"version"`
	// Repo is the repository whose history is bootstrapped, as "owner/name".
	Repo string `json:"repo"`
	// WikiRepo and WikiCommit are the wiki the working set was last refreshed
	// from and that wiki's HEAD at the time; both are empty before the first
	// refresh that reached the wiki.
	WikiRepo   string `json:"wiki_repo"`
	WikiCommit string `json:"wiki_commit"`
	// Units lists the processed units in processing order.
	Units []UnitRecord `json:"units"`
	// Pages maps the wiki-relative slash path of every page of the working set
	// ("memory/x.md") to what the bootstrap knows about it.
	Pages map[string]PageState `json:"pages"`
	// Usage sums the Claude usage of every run, without the per-pass breakdown.
	Usage report.Usage `json:"usage"`
}

// UnitRecord is one processed unit: how many pages its analysis proposed, how
// many of them the review accepted, the rejected ones with the reason, and
// when the unit finished (UTC, RFC 3339).
type UnitRecord struct {
	Key   string `json:"key"`
	Title string `json:"title"`
	// PRs and Commits are the pull requests and the closer commit an issue
	// unit held when it was processed. An issue keeps its key when a later
	// pull request or commit closes it again, so the key alone does not say
	// whether the record covers the unit (covers). Both are empty for the
	// other kinds, whose key names everything they hold.
	PRs        []int       `json:"prs,omitempty"`
	Commits    []string    `json:"commits,omitempty"`
	Proposed   int         `json:"proposed"`
	Accepted   int         `json:"accepted"`
	Rejected   []Rejection `json:"rejected"`
	FinishedAt string      `json:"finished_at"`
}

// covers reports whether the record was written for everything u holds. An
// issue unit that gained a pull request or a closer commit since is not
// covered, and is processed again.
func (rec UnitRecord) covers(u Unit) bool {
	if u.Kind != KindIssue {
		return true
	}
	for _, n := range u.PRs {
		if !slices.Contains(rec.PRs, n) {
			return false
		}
	}
	for _, sha := range u.Commits {
		if !slices.Contains(rec.Commits, sha) {
			return false
		}
	}
	return true
}

// Rejection is one proposed page that was not accepted, with the reason.
type Rejection struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// PageState is what the bootstrap knows about one page of the working set.
//
// A page is clean when the hash of its file equals BaseSHA256 and dirty
// otherwise; a dirty page is what `--write-wiki` pushes. Every hash is the
// SHA-256 of the page text with exactly one trailing newline.
type PageState struct {
	// Source is the reference its provenance marker names ("owner/repo#42"):
	// the unit that last wrote the page, or, for a page that came from the wiki
	// and that no unit changed, the source the wiki page's marker names. It is
	// empty for a wiki page without a marker.
	Source string `json:"source"`
	// BaseSHA256 is the hash of the wiki's version of the page that the local
	// file is based on. It is empty for a page the wiki does not hold.
	BaseSHA256 string `json:"base_sha256"`
	// Diverged is set when the wiki page and the local file both changed since
	// BaseSHA256. A diverged page is never written to the wiki.
	Diverged bool `json:"diverged"`
}

// newState returns the state of a bootstrap of repo that has processed nothing.
func newState(repo string) *State {
	return &State{Version: stateVersion, Repo: repo, Units: []UnitRecord{}, Pages: map[string]PageState{}}
}

// LoadState reads the state in dir for a bootstrap of repo ("owner/name"). It
// only reads: a directory without state.json yields a fresh state and creates
// nothing. A state file that does not parse, that has another version, that
// belongs to another repository, or that names a page outside the two page
// directories is an error.
func LoadState(dir, repo string) (*State, error) {
	file := filepath.Join(dir, stateFileName)
	data, err := os.ReadFile(file)
	if errors.Is(err, fs.ErrNotExist) {
		return newState(repo), nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", file, err)
	}
	s := &State{}
	if err := json.Unmarshal(data, s); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", file, err)
	}
	if s.Version != stateVersion {
		return nil, fmt.Errorf("unsupported state version %d in %s; delete the directory to start over", s.Version, file)
	}
	if !strings.EqualFold(s.Repo, repo) {
		return nil, fmt.Errorf("%s holds the bootstrap state of %s, not %s", dir, s.Repo, repo)
	}
	// A hand-edited file can hold null for either collection.
	if s.Units == nil {
		s.Units = []UnitRecord{}
	}
	if s.Pages == nil {
		s.Pages = map[string]PageState{}
	}
	// A page key becomes a file path under pages/ that the run writes, reads,
	// and deletes, and the file can come from anywhere (a checkout that commits
	// the directory): a key that leaves the two page directories is refused
	// before any of that.
	for p := range s.Pages {
		if !validPageKey(p) {
			return nil, fmt.Errorf("%s names the page %q, which is not a file in memory/ or review_patterns/; delete the directory to start over", file, p)
		}
	}
	return s, nil
}

// validPageKey reports whether p names one .md file directly in memory/ or
// review_patterns/. The rule is about where the file lies and not about its
// name, because a page that came from the wiki keeps the name it has there.
func validPageKey(p string) bool {
	dir, base := path.Split(p)
	return (dir == memoryDirName+"/" || dir == patternsDirName+"/") &&
		strings.HasSuffix(base, ".md") && base != ".md" &&
		// On a host whose separator is not the slash, base can still hold one.
		filepath.IsLocal(filepath.FromSlash(p))
}

// Reconcile makes the state and the page files agree: it drops every page
// entry whose file is missing or is not a regular file, and moves every file
// under pages/ that has no entry to orphans/, with a warning. Such a file is
// one the operator put there or a leftover of a stopped run, and the page
// files are the only copy of either, so it is kept and not deleted.
func (s *State) Reconcile(dir string) error {
	for p := range s.Pages {
		info, err := os.Lstat(pageFile(dir, p))
		if err == nil && info.Mode().IsRegular() {
			continue
		}
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("reconciling %s: %w", dir, err)
		}
		delete(s.Pages, p)
	}

	pages := filepath.Join(dir, pagesDirName)
	err := filepath.WalkDir(pages, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(pages, p)
		if err != nil {
			return err
		}
		if _, ok := s.Pages[filepath.ToSlash(rel)]; ok {
			return nil
		}
		orphan, err := orphanPath(filepath.Join(dir, orphansDirName), rel)
		if err != nil {
			return err
		}
		// The names are the file system's, so they are quoted like the page
		// names patterns.LoadMemoryPages logs.
		slog.Warn("moving a file under pages/ that state.json has no entry for out of the working set", "file", strconv.Quote(p), "to", strconv.Quote(orphan))
		return os.Rename(p, orphan)
	})
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("reconciling %s: %w", dir, err)
	}
	return nil
}

// orphanPath returns where under root the file at rel, a path relative to
// pages/, is moved to, and creates the directory of that path. An earlier
// orphan under the same path is the only copy of its text too, so the new one
// takes the first free name beside it, "<name>.1", "<name>.2", and so on. A
// directory of rel does the same when an earlier orphan has its name, and is
// shared with the orphans already in it otherwise.
func orphanPath(root, rel string) (string, error) {
	parts := strings.Split(rel, string(filepath.Separator))
	orphan := root
	for i, part := range parts {
		base := filepath.Join(orphan, part)
		orphan = base
		for n := 1; ; n++ {
			info, err := os.Lstat(orphan)
			if errors.Is(err, fs.ErrNotExist) || (err == nil && i < len(parts)-1 && info.IsDir()) {
				break
			}
			if err != nil {
				return "", err
			}
			orphan = base + "." + strconv.Itoa(n)
		}
	}
	if err := os.MkdirAll(filepath.Dir(orphan), 0o750); err != nil {
		return "", err
	}
	return orphan, nil
}

// Save writes the state to dir, creating the directory, its .gitignore, and
// the two page directories when they are missing. state.json is written to a
// temporary file and renamed, so a reader never sees half a state.
func (s *State) Save(dir string) error {
	for _, sub := range []string{memoryDirName, patternsDirName} {
		if err := os.MkdirAll(filepath.Join(dir, pagesDirName, sub), 0o750); err != nil {
			return fmt.Errorf("creating the state directory: %w", err)
		}
	}
	gitignore := filepath.Join(dir, ".gitignore")
	if _, err := os.Lstat(gitignore); errors.Is(err, fs.ErrNotExist) {
		if err := os.WriteFile(gitignore, []byte("*\n"), 0o600); err != nil {
			return fmt.Errorf("writing %s: %w", gitignore, err)
		}
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding the bootstrap state: %w", err)
	}
	return writeFileAtomic(filepath.Join(dir, stateFileName), append(data, '\n'))
}

// writeFileAtomic writes data to path through a temporary file beside it and
// a rename, so the file holds either its old content or all of data.
func writeFileAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("writing %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}

// pageFile returns the file of the page at the wiki-relative slash path p.
func pageFile(dir, p string) string {
	return filepath.Join(dir, pagesDirName, filepath.FromSlash(p))
}

// normalizePage trims the trailing newlines of a page text and appends one.
func normalizePage(text string) string {
	return strings.TrimRight(text, "\n") + "\n"
}

// hashPage returns the hex SHA-256 of a normalized page text.
func hashPage(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

// writePage writes body, normalized, as the page at the wiki-relative slash
// path p, through a temporary file and a rename.
func writePage(dir, p, body string) error {
	file := pageFile(dir, p)
	if err := os.MkdirAll(filepath.Dir(file), 0o750); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(file), err)
	}
	return writeFileAtomic(file, []byte(normalizePage(body)))
}

// readPage returns the normalized text of the page at the wiki-relative slash
// path p. The operator may edit a page file, so the text is normalized on
// every read and a trailing blank line an editor adds does not make a page
// dirty.
func readPage(dir, p string) (string, error) {
	data, err := os.ReadFile(pageFile(dir, p))
	if err != nil {
		return "", fmt.Errorf("reading page %s: %w", p, err)
	}
	return normalizePage(string(data)), nil
}

// Refresh brings the working set in dir up to date with the wiki wikiRepo
// ("owner/name") at ref. It clones through writer, which gives a fresh clone,
// and never through the TTL cache, which can return an old commit.
//
// A clone that fails (an uninitialized wiki fails here too) logs one warning
// and changes nothing. A clone at the commit of the last refresh changes
// nothing either. Otherwise every wiki page is compared with the local one:
//
//   - a page the working set does not hold is added;
//   - a clean page takes the wiki's text;
//   - a dirty page the wiki did not change stays as it is;
//   - a dirty page whose text the wiki now holds becomes clean;
//   - any other dirty page is marked diverged.
//
// A page that holds the wiki's text afterwards also takes the source the wiki
// page's provenance marker names, so a later hand edit is pushed under the
// marker the wiki page had.
//
// A page the wiki dropped is deleted when it is clean, and becomes a new page
// when it is dirty. The caller saves the state afterwards.
func (s *State) Refresh(dir string, writer capture.WikiWriter, wikiRepo, ref string) error {
	cloneDir, head, cleanup, err := writer.Clone(wikiRepo, ref)
	if err != nil {
		slog.Warn("could not clone the wiki; continuing with the working set as it is", "wiki", wikiRepo, "err", err)
		return nil
	}
	defer cleanup()
	if head != "" && head == s.WikiCommit {
		return nil
	}
	if err := s.applyWiki(dir, cloneDir); err != nil {
		return fmt.Errorf("refreshing the working set from the wiki: %w", err)
	}
	s.WikiCommit, s.WikiRepo = head, wikiRepo
	return nil
}

// applyWiki is the page-by-page part of Refresh over the wiki clone at
// cloneDir.
func (s *State) applyWiki(dir, cloneDir string) error {
	entries, err := sync.ReadWikiEntries(cloneDir)
	if err != nil {
		return err
	}
	inWiki := make(map[string]bool, len(entries))
	for _, e := range entries {
		// LoadState refuses a key validPageKey rejects, so such a page never
		// enters the working set.
		if !validPageKey(e.Path) {
			slog.Warn("skipping a wiki page whose path the working set cannot hold", "path", strconv.Quote(e.Path))
			continue
		}
		inWiki[e.Path] = true
		source, raw := capture.SplitMarker(e.Raw)
		body := normalizePage(raw)
		wikiHash := hashPage(body)

		page, known := s.Pages[e.Path]
		if !known {
			if err := writePage(dir, e.Path, body); err != nil {
				return err
			}
			s.Pages[e.Path] = PageState{Source: source, BaseSHA256: wikiHash}
			continue
		}
		local, err := readPage(dir, e.Path)
		if err != nil {
			return err
		}
		localHash := hashPage(local)
		switch {
		case localHash == page.BaseSHA256:
			if err := writePage(dir, e.Path, body); err != nil {
				return err
			}
			page.Source, page.BaseSHA256, page.Diverged = source, wikiHash, false
		case wikiHash == page.BaseSHA256:
			// The local edit is based on what the wiki holds. A page that was
			// diverged is not any more once the wiki is back at that text.
			page.Diverged = false
		case wikiHash == localHash:
			page.Source, page.BaseSHA256, page.Diverged = source, wikiHash, false
		default:
			page.Diverged = true
		}
		s.Pages[e.Path] = page
	}

	for p, page := range s.Pages {
		if page.BaseSHA256 == "" || inWiki[p] {
			continue
		}
		local, err := readPage(dir, p)
		if err != nil {
			return err
		}
		if hashPage(local) == page.BaseSHA256 {
			if err := os.Remove(pageFile(dir, p)); err != nil {
				return err
			}
			delete(s.Pages, p)
			continue
		}
		page.BaseSHA256, page.Diverged = "", false
		s.Pages[p] = page
	}
	return nil
}
