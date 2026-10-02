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
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/planwerk/planwerk-agent/internal/github"
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
