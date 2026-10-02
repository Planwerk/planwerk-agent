package search

import (
	"log/slog"
	"os"
	"regexp"

	"github.com/planwerk/planwerk-agent/internal/mirror"
)

// Surface is what a run hands a read-only session so it can search the
// mirror: the command line up to the query, the end of the last finished sync,
// and the mirror's revision. The zero Surface means no search.
type Surface struct {
	Command  string // "<binary> brain search <owner>/<name>"
	SyncedAt string
	Revision string
}

// Enabled reports whether the surface lets a session search.
func (s Surface) Enabled() bool {
	return s.Command != ""
}

// AllowRule returns the permission rule that pre-approves the command for a
// session, pinned to the repository: "Bash(<command>:*)". It is empty for a
// surface that is not enabled.
func (s Surface) AllowRule() string {
	if !s.Enabled() {
		return ""
	}
	return "Bash(" + s.Command + ":*)"
}

// CacheFlag returns the part of a cache key that names the mirror's revision:
// a cached result came from a session that could search the mirror at that
// revision. It is empty for a surface that is not enabled, so the key of a run
// without the search carries no mirror part.
func (s Surface) CacheFlag() string {
	if !s.Enabled() {
		return ""
	}
	return "brain=" + s.Revision
}

// SurfaceFor returns the surface of a run: the zero Surface when the run did
// not opt in, and otherwise what resolve yields. A nil resolve means
// ResolveSurface.
func SurfaceFor(enabled bool, resolve func(owner, name string) Surface, owner, name string) Surface {
	if !enabled {
		return Surface{}
	}
	if resolve == nil {
		resolve = ResolveSurface
	}
	return resolve(owner, name)
}

// ruleSafePath matches the path of a binary that can be written into a
// permission rule as it is. A path with a space or a backslash would need
// quoting the rule cannot carry, so a Windows path never matches.
var ruleSafePath = regexp.MustCompile(`^[A-Za-z0-9_./+@-]+$`)

// ResolveSurface returns the surface of the mirror of owner/name, or the zero
// Surface with a warning when the sessions cannot be given one.
func ResolveSurface(owner, name string) Surface {
	return resolveSurface(owner, name, "", os.Executable)
}

// resolveSurface is ResolveSurface over the directory the mirrors live under
// (root; empty means mirror.DefaultRoot()) and the lookup of this binary's
// path. It never fails a run: a repository without a finished mirror, a path
// that cannot be written into a permission rule, and an index that cannot be
// built each log a warning and yield the zero Surface.
//
// It opens the index once, so every session of the run finds it up to date and
// no session's first search pays for the build.
func resolveSurface(owner, name, root string, executable func() (string, error)) Surface {
	repo := owner + "/" + name
	noMirror := func(err error) Surface {
		slog.Warn("the brain is enabled, but this repository has no finished mirror; the sessions get no search", "repo", repo, "err", err)
		return Surface{}
	}
	if root == "" {
		var err error
		if root, err = mirror.DefaultRoot(); err != nil {
			return noMirror(err)
		}
	}
	dir, err := mirror.Dir(root, owner, name)
	if err != nil {
		return noMirror(err)
	}
	st, err := mirror.LoadFinishedState(dir, repo)
	if err != nil {
		return noMirror(err)
	}

	path, err := executable()
	if err != nil || !ruleSafePath.MatchString(path) {
		args := []any{"path", path}
		if err != nil {
			args = append(args, "err", err)
		}
		slog.Warn("the brain is enabled, but the path of this binary cannot be written into a permission rule; the sessions get no search", args...)
		return Surface{}
	}

	ix, err := Open(dir)
	if err == nil {
		err = ix.Close()
	}
	if err != nil {
		slog.Warn("the brain is enabled, but the search index could not be built; the sessions get no search", "err", err)
		return Surface{}
	}

	slog.Info("the sessions can search the mirror", "repo", repo, "synced_at", st.SyncedAt)
	return Surface{Command: path + " brain search " + repo, SyncedAt: st.SyncedAt, Revision: st.Revision()}
}
