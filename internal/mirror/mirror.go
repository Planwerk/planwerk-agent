// Package mirror keeps a local copy of what a repository knows on GitHub: one
// markdown file per issue and per pull request with the whole conversation,
// the commit list of the default branch, and a clone of the wiki.
//
// `brain sync` writes the mirror into the per-user cache area, keyed by owner
// and repository. The mirror is a derivative: deleting it loses nothing, and a
// later sync rebuilds it from GitHub.
//
// The files hold GitHub's text unchanged. That text is written by everyone who
// can open an issue or comment, and it can hold a secret someone pasted. No
// spawned session is given the mirror directory, and a reader that hands
// mirrored text to a session redacts it and frames it as data first.
package mirror

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/planwerk/planwerk-agent/internal/github"
	"github.com/planwerk/planwerk-agent/internal/patterns"
)

// The names of the files and directories of a mirror directory.
const (
	stateFileName   = "state.json"
	historyFileName = "history.jsonl"
	issuesDirName   = "issues"
	pullsDirName    = "pulls"
	wikiDirName     = "wiki"
)

// itemFileExt is the extension of an item file.
const itemFileExt = ".md"

// tempFileExt is the extension of the temporary file a write goes through.
const tempFileExt = ".tmp"

// stateVersion is the version of state.json this package reads and writes.
const stateVersion = 1

// DefaultRoot returns the directory every mirror lives under:
// <UserCacheDir>/planwerk-agent/brain. A user cache directory that cannot be
// resolved is an error. The mirror holds GitHub's text unchanged, so it never
// falls back to the temp directory, where another local user can create the
// path in advance.
func DefaultRoot() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("resolving the user cache directory for the mirror: %w", err)
	}
	return filepath.Join(base, "planwerk-agent", "brain"), nil
}

// Dir returns the mirror directory of a repository under root:
// <root>/<owner>/<name>, both in lowercase, because GitHub compares the two
// without case. The owner and the name of a parsed repository reference can be
// "." or "..", which would leave the root, so both are refused.
func Dir(root, owner, name string) (string, error) {
	for _, part := range []string{owner, name} {
		if part == "." || part == ".." {
			return "", fmt.Errorf("invalid repository %s/%s for a mirror directory", owner, name)
		}
	}
	return filepath.Join(root, strings.ToLower(owner), strings.ToLower(name)), nil
}

// itemsDir returns the directory of the mirror directory dir that holds the
// item files of kind: pulls for github.ItemKindPull, issues otherwise.
func itemsDir(dir, kind string) string {
	if kind == github.ItemKindPull {
		return filepath.Join(dir, pullsDirName)
	}
	return filepath.Join(dir, issuesDirName)
}

// ItemPath returns the file of an item in the mirror directory dir:
// pulls/<number>.md for github.ItemKindPull, issues/<number>.md otherwise.
func ItemPath(dir, kind string, number int) string {
	return filepath.Join(itemsDir(dir, kind), strconv.Itoa(number)+itemFileExt)
}

// ItemFiles returns the paths of the item files of kind in the mirror
// directory dir, in name order. A missing directory holds none. An entry that
// is no item file is left out: a directory, or a file of another extension,
// such as the temporary file an interrupted write leaves behind.
func ItemFiles(dir, kind string) ([]string, error) {
	sub := itemsDir(dir, kind)
	entries, err := os.ReadDir(sub)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", sub, err)
	}
	var paths []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), itemFileExt) {
			paths = append(paths, filepath.Join(sub, e.Name()))
		}
	}
	return paths, nil
}

// ItemsState is what the mirror remembers about the issues and pull requests.
// Cursor is the newest update time a sync has handled, as GitHub prints it; the
// next sync lists the items updated at or after it.
type ItemsState struct {
	Cursor string `json:"cursor"`
}

// WikiState names the mirrored wiki ("owner/name") and the commit its clone
// stands at.
type WikiState struct {
	Repo   string `json:"repo"`
	Commit string `json:"commit"`
}

// State is the content of state.json. SyncedAt is the end of the last sync in
// which every adapter succeeded, in UTC and RFC 3339, or "" when none has. The
// history keeps no entry: history.jsonl is its own cursor.
type State struct {
	Version  int        `json:"version"`
	Repo     string     `json:"repo"`
	SyncedAt string     `json:"synced_at"`
	Items    ItemsState `json:"items"`
	Wiki     WikiState  `json:"wiki"`
}

// LoadState reads the state of the mirror of repo ("owner/name") in dir. It
// writes nothing. Without a state file it returns a fresh state and exists ==
// false. A state file of another version or of another repository is an
// error.
func LoadState(dir, repo string) (st *State, exists bool, err error) {
	path := filepath.Join(dir, stateFileName)
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return &State{Version: stateVersion, Repo: repo}, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("reading %s: %w", path, err)
	}
	st = &State{}
	if err := json.Unmarshal(data, st); err != nil {
		return nil, false, fmt.Errorf("parsing %s: %w", path, err)
	}
	if st.Version != stateVersion {
		return nil, false, fmt.Errorf("unsupported mirror state version %d in %s; run brain sync --full to rebuild", st.Version, path)
	}
	if !strings.EqualFold(st.Repo, repo) {
		return nil, false, fmt.Errorf("%s holds the mirror of %s, not %s", dir, st.Repo, repo)
	}
	return st, true, nil
}

// Save writes the state to state.json in dir, which must exist.
func (s *State) Save(dir string) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding the mirror state: %w", err)
	}
	return writeFileAtomic(filepath.Join(dir, stateFileName), append(data, '\n'))
}

// writeFileAtomic writes data to path with mode 0600 through a temporary file
// beside it and a rename, so the file holds either its old content or all of
// data. The temporary file is created under a name of its own, so two writers
// of one path never share it and no file planted under a known name is
// written through, and it is synced before the rename, so a crash does not
// leave an empty file under the final name.
func writeFileAtomic(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*"+tempFileExt)
	if err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	tmp := f.Name()
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}

// Options configures one `brain sync` run.
type Options struct {
	RepoRef string
	// Root is the directory the mirrors live under; empty means DefaultRoot().
	Root string
	// Full deletes the repository's mirror before the sync, once GitHub has
	// answered a listing.
	Full bool
	// Wiki names the wiki to mirror (Repo; empty means the repository's own)
	// and pins it (Ref). Enabled is not read: running the command is the
	// opt-in.
	Wiki patterns.WikiOptions
}

// Mirror is the mirror of one repository as an adapter sees it: the
// repository, its mirror directory, and the state the run saves.
type Mirror struct {
	Owner string
	Name  string
	Dir   string
	State *State
}

// Adapter syncs one source of a repository's knowledge into the mirror. It
// owns a path under the mirror directory and, when it needs a cursor, a field
// of State. Sync returns the line the run prints for it. After an error the
// mirror must still be consistent, because the run saves the state and goes on
// with the next adapter.
type Adapter interface {
	Name() string
	Sync(m *Mirror) (summary string, err error)
}

// GitHub is the part of the GitHub client the adapters read through.
type GitHub interface {
	ListUpdatedItems(owner, name, since string) ([]github.UpdatedItem, error)
	GetItem(owner, name string, number int) (*github.Item, error)
	DefaultBranchHistoryUntil(owner, name string, done func(page []github.HistoryCommit, total int) bool) ([]github.HistoryCommit, error)
}

var _ GitHub = github.Client{}

// CloneWikiFn makes a full clone of the wiki of repo ("owner/name") at dest,
// pinned to ref, and returns its HEAD commit. It matches patterns.MirrorWiki:
// a failed call leaves an existing dest as it was.
type CloneWikiFn func(repo, ref, dest string) (headSHA string, err error)

// Syncer runs `brain sync` over injected seams, so the run can be exercised
// without GitHub and without a wiki. A nil seam takes its production default.
type Syncer struct {
	GitHub    GitHub           // nil means github.Client{}
	CloneWiki CloneWikiFn      // nil means patterns.MirrorWiki
	Now       func() time.Time // nil means time.Now
	// Adapters are run in order; nil means ItemsAdapter, HistoryAdapter, and
	// WikiAdapter over the seams above.
	Adapters []Adapter
}

// Run brings the mirror of opts.RepoRef up to date and prints one line per
// adapter to w. The state is saved after every adapter. An adapter that fails
// does not stop the next one: Run returns the errors of all that failed, each
// under its adapter's name, and only a run without one sets State.SyncedAt.
//
// Under opts.Full the mirror is deleted first, but only after GitHub answered
// a listing: a run that cannot reach GitHub or authenticate leaves the mirror
// as it was.
func (s *Syncer) Run(w io.Writer, opts Options) error {
	owner, name, err := github.ParseRepoRef(opts.RepoRef)
	if err != nil {
		return fmt.Errorf("parsing repo ref: %w", err)
	}
	root := opts.Root
	if root == "" {
		if root, err = DefaultRoot(); err != nil {
			return err
		}
	}
	dir, err := Dir(root, owner, name)
	if err != nil {
		return err
	}
	now := s.Now
	if now == nil {
		now = time.Now
	}
	if opts.Full {
		// Nothing was updated since now, so the listing is one short page.
		if _, err := s.github().ListUpdatedItems(owner, name, now().UTC().Format(time.RFC3339)); err != nil {
			return fmt.Errorf("reaching GitHub before deleting %s: %w", dir, err)
		}
		if err := os.RemoveAll(dir); err != nil {
			return fmt.Errorf("removing %s: %w", dir, err)
		}
	}
	repo := owner + "/" + name
	st, _, err := LoadState(dir, repo)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}
	removeStaleTemps(dir, now())
	_, _ = fmt.Fprintf(w, "Mirror of %s at %s\n", repo, dir)

	save := func() error {
		if err := st.Save(dir); err != nil {
			return fmt.Errorf("saving %s: %w", filepath.Join(dir, stateFileName), err)
		}
		return nil
	}
	m := &Mirror{Owner: owner, Name: name, Dir: dir, State: st}
	var failed []error
	for _, a := range s.adapters(opts) {
		summary, err := a.Sync(m)
		if err != nil {
			failed = append(failed, fmt.Errorf("%s: %w", a.Name(), err))
		} else {
			_, _ = fmt.Fprintln(w, summary)
		}
		if err := save(); err != nil {
			return errors.Join(append(failed, err)...)
		}
	}
	if len(failed) > 0 {
		return errors.Join(failed...)
	}

	st.SyncedAt = now().UTC().Format(time.RFC3339)
	return save()
}

// staleTempAge is the age at which a run removes a temporary file. No write of
// a run that is still going takes that long.
const staleTempAge = time.Hour

// removeStaleTemps removes the temporary files an interrupted write left in
// the mirror directory dir and in its two item directories: every regular file
// with the extension tempFileExt that was last written more than staleTempAge
// before now. Such a file is a copy of GitHub's text under a name no later
// write uses again, so nothing else would remove it. A younger one can belong
// to another run that is still writing. The wiki clone is not searched: its
// files are the wiki's own. A file that cannot be removed is a warning.
func removeStaleTemps(dir string, now time.Time) {
	for _, sub := range []string{dir, itemsDir(dir, github.ItemKindIssue), itemsDir(dir, github.ItemKindPull)} {
		entries, err := os.ReadDir(sub)
		if err != nil {
			// A mirror that holds no item of a kind has no directory for it.
			if !errors.Is(err, fs.ErrNotExist) {
				slog.Warn("could not look for leftover temporary files", "dir", sub, "err", err)
			}
			continue
		}
		for _, e := range entries {
			if !e.Type().IsRegular() || !strings.HasSuffix(e.Name(), tempFileExt) {
				continue
			}
			path := filepath.Join(sub, e.Name())
			info, err := e.Info()
			if err == nil && now.Sub(info.ModTime()) > staleTempAge {
				err = os.Remove(path)
			}
			// A file that is gone was renamed or removed by another run.
			if err != nil && !errors.Is(err, fs.ErrNotExist) {
				slog.Warn("could not remove a leftover temporary file", "path", path, "err", err)
			}
		}
	}
}

// github returns s.GitHub, or the production client when it is nil.
func (s *Syncer) github() GitHub {
	if s.GitHub != nil {
		return s.GitHub
	}
	return github.Client{}
}

// adapters returns s.Adapters, or the three production adapters over the
// seams of s when it is nil.
func (s *Syncer) adapters(opts Options) []Adapter {
	if s.Adapters != nil {
		return s.Adapters
	}
	gh := s.github()
	clone := s.CloneWiki
	if clone == nil {
		clone = patterns.MirrorWiki
	}
	return []Adapter{
		ItemsAdapter{GitHub: gh},
		HistoryAdapter{GitHub: gh},
		WikiAdapter{Clone: clone, Options: opts.Wiki},
	}
}
