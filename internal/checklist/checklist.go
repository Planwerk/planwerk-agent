// Package checklist provides the review checklist used in the Claude prompt.
// The default checklist is embedded at compile time. A repository can replace
// it with .planwerk/checklist.md and extend it with the Markdown files under
// .planwerk/checklist.d/, which are appended in file-name order.
package checklist

import (
	_ "embed"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/planwerk/planwerk-agent/internal/gitref"
)

// maxFileSize is the maximum size of a checklist override file (64 KB), and
// the cap on the combined size of the fragment files appended to it.
const maxFileSize = 64 * 1024

// overridePath is the repo-relative location of the checklist that replaces
// the embedded default.
const overridePath = ".planwerk/checklist.md"

// fragmentDir is the repo-relative directory whose Markdown files are appended
// to the checklist.
const fragmentDir = ".planwerk/checklist.d"

//go:embed checklist.md
var defaultChecklist string

// Load resolves the review checklist from the working tree at repoDir. The
// base is .planwerk/checklist.md when the repository has a non-empty one of
// at most 64 KB (a larger one is ignored with a warning), and the embedded
// default otherwise. The fragments under .planwerk/checklist.d/ are appended
// to it (see appendFragments). An empty repoDir returns the default.
func Load(repoDir string) string {
	if repoDir == "" {
		return defaultChecklist
	}
	base := defaultChecklist
	repoChecklist := filepath.Join(repoDir, filepath.FromSlash(overridePath))
	if info, err := os.Stat(repoChecklist); err == nil {
		if info.Size() > maxFileSize {
			slog.Warn("checklist override exceeds 64 KB limit, using default",
				"path", repoChecklist, "size", info.Size())
		} else if data, err := os.ReadFile(repoChecklist); err == nil && len(data) > 0 {
			base = string(data)
		}
	}
	dir := filepath.Join(repoDir, filepath.FromSlash(fragmentDir))
	return appendFragments(base, workingTreeFiles(dir), func(name string) (string, error) {
		data, err := os.ReadFile(filepath.Join(dir, name))
		return string(data), err
	})
}

// Default returns the embedded default checklist content.
func Default() string {
	return defaultChecklist
}

// LoadFromRef is Load reading the override and the fragments as they stand at
// ref (the pull request's base branch) instead of from the working tree. The
// review runs in a checkout of the pull request's head, and the checklist is
// spliced into the review prompt as instructions: read from the head, a pull
// request could rewrite or extend the checklist it is reviewed against
// (decision 97). An override absent at ref, empty, or larger than 64 KB falls
// back to the embedded default; a ref without .planwerk/checklist.d/ appends
// nothing. Both are read from the committed tree (gitref.ListFiles and
// gitref.Show), so ref's .gitattributes cannot hide a fragment with
// export-ignore or rewrite one with export-subst (decision 145).
func LoadFromRef(repoDir, ref string) string {
	base := defaultChecklist
	if body, ok := gitref.Show(repoDir, ref, overridePath, maxFileSize); ok && body != "" {
		base = body
	}
	return appendFragments(base, gitref.ListFiles(repoDir, ref, fragmentDir), func(name string) (string, error) {
		body, ok := gitref.Show(repoDir, ref, fragmentDir+"/"+name, maxFileSize)
		if !ok {
			return "", errors.New("git show failed")
		}
		return body, nil
	})
}

// workingTreeFiles lists the regular files directly in dir, in the file-name
// order os.ReadDir returns; subdirectories, symlinks, and FIFOs are left out.
// A missing dir lists nothing; any other read error is logged and lists
// nothing.
func workingTreeFiles(dir string) []gitref.File {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			slog.Warn("reading checklist fragments failed; using the checklist without them",
				"dir", dir, "err", err)
		}
		return nil
	}
	var files []gitref.File
	for _, e := range entries {
		if !e.Type().IsRegular() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, gitref.File{Name: e.Name(), Size: info.Size()})
	}
	return files
}

// appendFragments appends the *.md files among files to checklist, in the
// order given, each trimmed of trailing newlines and placed after a blank
// line. files must be the regular files directly in the fragment directory,
// sorted by name (workingTreeFiles, gitref.ListFiles), and read returns one
// file's content by name. Other extensions and empty or whitespace-only files
// are skipped. The files' combined size is capped at 64 KB: the file whose
// size would cross the cap is skipped with every file after it, so what loads
// is always a prefix of the sorted list. With no fragment appended, checklist
// comes back byte for byte, so a repository without the directory reviews as
// before (decision 145).
func appendFragments(checklist string, files []gitref.File, read func(name string) (string, error)) string {
	var fragments, names []string
	var total int64
	for _, f := range files {
		if filepath.Ext(f.Name) != ".md" {
			continue
		}
		if total+f.Size > maxFileSize {
			slog.Warn("checklist fragments exceed the 64 KB limit; skipping this fragment and the ones after it",
				"file", f.Name, "size", f.Size)
			break
		}
		data, err := read(f.Name)
		if err != nil {
			slog.Warn("reading a checklist fragment failed; skipping it", "file", f.Name, "err", err)
			continue
		}
		if strings.TrimSpace(data) == "" {
			continue
		}
		total += int64(len(data))
		fragments = append(fragments, strings.TrimRight(data, "\n"))
		names = append(names, f.Name)
	}
	if len(fragments) == 0 {
		return checklist
	}
	slog.Info("appended checklist fragments", "dir", fragmentDir, "files", names)
	var sb strings.Builder
	sb.WriteString(strings.TrimRight(checklist, "\n"))
	for _, f := range fragments {
		sb.WriteString("\n\n")
		sb.WriteString(f)
	}
	sb.WriteString("\n")
	return sb.String()
}
