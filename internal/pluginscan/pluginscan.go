// Package pluginscan picks the changed files a plugin's checker may read in a
// checkout, and bounds what the checker prints back. The impeccable detector
// (decision 155) and the diagram-design checkers (decision 179) run on the
// files a change touches, and a change decides what those files are: a
// directory committed as a symlink to a path outside the checkout, a name that
// starts with a dash. Targets keeps both from reaching the checker.
package pluginscan

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Targets returns the checker arguments for files, paths relative to dir:
// each one that is a regular file inside dir once every symlink on its path is
// resolved, prefixed with "./" so a name that starts with a dash cannot read
// as a flag, sorted and without duplicates. A change can commit a directory as
// a symlink to a path outside the checkout and list a file under it as
// deleted; resolving the whole path keeps the checker from reading, and the
// prompt from quoting, a file the machine holds outside the checkout.
func Targets(dir string, files []string) []string {
	root, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, f := range files {
		rel := filepath.Clean(filepath.FromSlash(f))
		if rel == "." || filepath.IsAbs(rel) || !Inside(root, filepath.Join(root, rel)) {
			continue
		}
		path := filepath.Join(dir, rel)
		fi, err := os.Lstat(path)
		if err != nil || !fi.Mode().IsRegular() {
			continue
		}
		if resolved, err := filepath.EvalSymlinks(path); err != nil || !Inside(root, resolved) {
			continue
		}
		out = append(out, "."+string(filepath.Separator)+rel)
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// Inside reports whether the clean path lies within the clean root.
func Inside(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// Path returns a target as the path relative to the checkout it names, with
// forward slashes and without the "./" Targets puts in front.
func Path(target string) string {
	return filepath.ToSlash(strings.TrimPrefix(target, "."+string(filepath.Separator)))
}

// LimitedBuffer keeps the first Max bytes written to it and drops the rest,
// reporting every write as complete so the child is never blocked on a full
// pipe.
type LimitedBuffer struct {
	Buf *bytes.Buffer
	Max int
}

func (l *LimitedBuffer) Write(p []byte) (int, error) {
	if room := l.Max - l.Buf.Len(); room > 0 {
		l.Buf.Write(p[:min(room, len(p))])
	}
	return len(p), nil
}

// FirstLine returns s up to its first newline.
func FirstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}
