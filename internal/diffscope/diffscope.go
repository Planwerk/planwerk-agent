// Package diffscope writes the diff a diff-scoped finder session reads, without
// the diff drivers, text conversion, color, and path prefixes the git
// configuration on the machine would apply, and decides which changed files
// that diff may leave out under --diff-exclusions (decision 137).
package diffscope

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/planwerk/planwerk-agent/internal/report"
)

const (
	// DirPrefix names the temporary directory Materialize writes the diff into.
	DirPrefix = "planwerk-agent-diff-"
	// fileName is the diff file's name inside that directory.
	fileName = "branch.diff"
	// maxDiffBytes caps the diff file; a larger diff is not written, and the
	// session runs git diff itself as it did before the file existed.
	maxDiffBytes = 32 << 20
	// gitTimeout bounds each git call. A pull request clone is blobless, so the
	// diff fetches the base blobs it compares against, which takes as long as
	// a remote call (github.gitRemoteTimeout).
	gitTimeout = 2 * time.Minute
	// gitWaitDelay bounds how long a git call may still block once git has
	// exited or its timeout has killed it. A lazy fetch git starts in a blobless
	// clone inherits the output pipes and would keep Wait reading them; this
	// long after, Wait closes them instead (claude.claudeWaitDelay).
	gitWaitDelay = 2 * time.Second
	// maxHeaderBytes is how much of a changed file isGenerated reads.
	maxHeaderBytes = 8 << 10
)

// Request says which diff Materialize writes.
type Request struct {
	// RepoDir is a directory of the checkout. git runs at its top level, so a
	// RepoDir below it still diffs, and classifies, the whole change. ""
	// writes nothing and yields the zero Diff.
	RepoDir string
	// Revs are the revision arguments of the diff, e.g. {"origin/main...HEAD"}
	// for a branch or {"<sha>"} for what changed in the working tree since a
	// commit. Empty writes nothing.
	Revs []string
	// Exclude leaves the lock, vendored and generated files Classify names out
	// of the diff (--diff-exclusions).
	Exclude bool
	// Paths limits the diff to these slash-separated paths, relative to the
	// checkout's top level and each taken literally, as one partition of a
	// partitioned review reads (decision 142). Exclude then classifies only the
	// changed files among them. Empty diffs the whole tree.
	Paths []string
}

// Diff is a diff written to disk for a session to read. The zero Diff names no
// file, and a prompt built from it keeps the git command it carried before the
// file existed.
type Diff struct {
	// Dir holds File and is the directory the session is handed through
	// --add-dir; "" when nothing was written.
	Dir string
	// File is the diff file's absolute path.
	File string
	// Range is the revision arguments joined by spaces, as the prompt names
	// them after `git diff`.
	Range string
	// Lines is the number of lines in File.
	Lines int
	// Excluded lists the changed files the diff leaves out, sorted by path;
	// nil when exclusions are off or no changed file matched.
	Excluded []report.ExcludedFile
}

// lockFiles are the lock files left out by their exact base name. They pin
// what a manifest in the same change declares, which is where a reviewer reads
// the dependency change.
var lockFiles = map[string]bool{
	"go.sum":              true,
	"go.work.sum":         true,
	"package-lock.json":   true,
	"npm-shrinkwrap.json": true,
	"yarn.lock":           true,
	"pnpm-lock.yaml":      true,
	"Cargo.lock":          true,
	"poetry.lock":         true,
	"Pipfile.lock":        true,
	"uv.lock":             true,
	"Gemfile.lock":        true,
	"composer.lock":       true,
}

// vendoredDirs are the directory names whose changed files are folded into
// one vendored entry per directory.
var vendoredDirs = map[string]bool{"vendor": true, "node_modules": true}

// testDirs are the directory names that mark a path as a test. A test outside
// a vendored directory is never left out (decision 16), and a vendor directory
// below one is a fixture, not vendored code.
var testDirs = map[string]bool{
	"test": true, "tests": true, "testdata": true, "__tests__": true, "e2e": true,
	"fixtures": true, "spec": true, "chainsaw": true, ".chainsaw": true,
}

// generatedMarker is the comment generators write to say a file is generated,
// in the form Go standardized and other ecosystems copy.
var generatedMarker = regexp.MustCompile(`Code generated .* DO NOT EDIT\.`)

// commentPrefixes are the line-comment and block-comment openers isGenerated
// treats as part of a file's leading comment block.
var commentPrefixes = []string{"//", "#", "/*", "*", "--", ";", "<!--"}

// isTestPath reports whether p, a slash-separated repository path, is a test:
// a directory segment in testDirs, or a base name that reads as a test file in
// one of the conventions the review prompt names. It is a deliberate superset
// of implement.isTestFile and doccheck.isTestFile: a false positive only keeps
// a file in the diff.
func isTestPath(p string) bool {
	segs := strings.Split(p, "/")
	if slices.ContainsFunc(segs[:len(segs)-1], func(s string) bool { return testDirs[s] }) {
		return true
	}
	base := strings.ToLower(segs[len(segs)-1])
	for _, marker := range []string{"_test.", ".test.", ".spec.", "_spec."} {
		if strings.Contains(base, marker) {
			return true
		}
	}
	return strings.HasPrefix(base, "test_") || strings.HasPrefix(base, "chainsaw-test")
}

// vendoredRoot returns the vendored directory p lies under, up to and
// including the first directory segment in vendoredDirs, with a trailing "/".
// It returns "" for a path under no such directory, and for one whose testDirs
// segment comes first, so testdata/vendor/x.go stays in the diff.
func vendoredRoot(p string) string {
	segs := strings.Split(p, "/")
	for i, s := range segs[:len(segs)-1] {
		if testDirs[s] {
			return ""
		}
		if vendoredDirs[s] {
			return strings.Join(segs[:i+1], "/") + "/"
		}
	}
	return ""
}

// isGenerated reports whether the regular file at abs opens with a comment
// block that carries generatedMarker. It reads at most maxHeaderBytes and skips
// blank lines; the first line that is not a comment ends the scan, so a
// generator's template that holds the marker in a string does not match. A
// symlink, a FIFO, or a missing file is not generated, and none is opened.
func isGenerated(abs string) bool {
	info, err := os.Lstat(abs)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	f, err := os.Open(abs)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(io.LimitReader(f, maxHeaderBytes))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if !slices.ContainsFunc(commentPrefixes, func(prefix string) bool { return strings.HasPrefix(line, prefix) }) {
			return false
		}
		if generatedMarker.MatchString(line) {
			return true
		}
	}
	return false
}

// Classify returns the changed files of changed, slash-separated paths
// relative to repoDir, the checkout's top level, that a diff may leave out:
// each vendored directory as one entry counting every changed file under it,
// tests included, then lock files by base name and files whose leading comment
// says they are generated, which isGenerated reads from repoDir. A test outside
// a vendored directory is never left out (decision 16). Nor is a path whose
// entry would carry a control character, since the prompt and the report print
// each entry on a line of its own: a lock or generated file with one in its
// path, or a file under a vendored directory with one in the directory's. When
// every changed file would be left out it returns nil, so a change made only
// of such files is diffed in full. The entries are sorted by path.
func Classify(repoDir string, changed []string) []report.ExcludedFile {
	var out []report.ExcludedFile
	vendored := map[string]int{} // vendored root -> its index in out
	count := 0
	for _, p := range changed {
		if root := vendoredRoot(p); root != "" {
			// The directory's pathspec leaves out every file under it, so each
			// one counts, whatever its own name holds.
			if strings.ContainsFunc(root, unicode.IsControl) {
				continue
			}
			if i, ok := vendored[root]; ok {
				out[i].Files++
			} else {
				vendored[root] = len(out)
				out = append(out, report.ExcludedFile{Path: root, Reason: report.ExcludedVendored, Files: 1})
			}
			count++
			continue
		}
		if strings.ContainsFunc(p, unicode.IsControl) || isTestPath(p) {
			continue
		}
		switch {
		case lockFiles[path.Base(p)]:
			out = append(out, report.ExcludedFile{Path: p, Reason: report.ExcludedLockfile})
		case isGenerated(filepath.Join(repoDir, filepath.FromSlash(p))):
			out = append(out, report.ExcludedFile{Path: p, Reason: report.ExcludedGenerated})
		default:
			continue
		}
		count++
	}
	if count == len(changed) {
		return nil
	}
	slices.SortFunc(out, func(a, b report.ExcludedFile) int { return strings.Compare(a.Path, b.Path) })
	return out
}

// IsExcluded reports whether the changed file p is one excluded leaves out:
// an entry with that exact path, or a vendored directory entry p lies under.
func IsExcluded(p string, excluded []report.ExcludedFile) bool {
	return slices.ContainsFunc(excluded, func(e report.ExcludedFile) bool {
		return e.Path == p || (strings.HasSuffix(e.Path, "/") && strings.HasPrefix(p, e.Path))
	})
}

// Reviewable returns the files of files that excluded does not leave out, in
// order, in a slice of its own.
func Reviewable(files []string, excluded []report.ExcludedFile) []string {
	var out []string
	for _, f := range files {
		if !IsExcluded(f, excluded) {
			out = append(out, f)
		}
	}
	return out
}

// Materialize writes the diff req names into a fresh directory under the
// system temp dir and returns it with a cleanup that removes the directory and
// is safe to call more than once. The diff is `git -c core.quotePath=false
// --no-pager diff --no-ext-diff --no-textconv --no-color --src-prefix=a/
// --dst-prefix=b/ <revs> --`, so neither a diff driver, a text conversion, a
// color setting nor a prefix setting on the machine changes it. It carries one
// `:(literal)` pathspec per entry of req.Paths, and with req.Exclude one
// `:(exclude,literal)` pathspec per entry Classify returns. Every git call runs
// at the checkout's top level, where the paths git prints start and where the
// pathspecs must be anchored to match them. A request without a RepoDir or
// Revs writes nothing and returns the zero Diff.
// On a git failure, or a diff larger than 32 MiB, nothing is left behind and
// the error names the command and git's stderr or the size cap.
func Materialize(req Request) (Diff, func(), error) {
	noop := func() {}
	if req.RepoDir == "" || len(req.Revs) == 0 {
		return Diff{}, noop, nil
	}
	top, err := topLevel(req.RepoDir)
	if err != nil {
		return Diff{}, noop, err
	}
	var excluded []report.ExcludedFile
	if req.Exclude {
		changed, err := changedPaths(top, req.Revs, req.Paths)
		if err != nil {
			return Diff{}, noop, err
		}
		excluded = Classify(top, changed)
	}

	dir, err := os.MkdirTemp("", DirPrefix)
	if err != nil {
		return Diff{}, noop, fmt.Errorf("creating the diff directory: %w", err)
	}
	// A relative TMPDIR yields a relative dir, and a session running in
	// another working directory could not resolve it.
	abs, err := filepath.Abs(dir)
	if err != nil {
		_ = os.RemoveAll(dir)
		return Diff{}, noop, fmt.Errorf("resolving the diff directory: %w", err)
	}
	dir = abs
	cleanup := func() {
		if err := os.RemoveAll(dir); err != nil {
			slog.Warn("removing the diff directory failed", "dir", dir, "err", err)
		}
	}

	file := filepath.Join(dir, fileName)
	lines, err := writeDiff(top, file, diffArgs(req.Revs, req.Paths, excluded))
	if err != nil {
		cleanup()
		return Diff{}, noop, err
	}
	return Diff{Dir: dir, File: file, Range: strings.Join(req.Revs, " "), Lines: lines, Excluded: excluded}, cleanup, nil
}

// MaterializeOrWarn is Materialize for callers that run without a diff file
// rather than fail. A failure is logged and yields the zero Diff, whose prompt
// keeps the git command, and a no-op cleanup. A non-empty exclusion list is
// logged with the first report.ExcludedFilesListLimit paths.
func MaterializeOrWarn(req Request) (Diff, func()) {
	d, cleanup, err := Materialize(req)
	if err != nil {
		slog.Warn("writing the diff for the session failed; its prompt keeps the git command", "err", err, "range", strings.Join(req.Revs, " "))
		return Diff{}, func() {}
	}
	if len(d.Excluded) > 0 {
		shown := d.Excluded[:min(len(d.Excluded), report.ExcludedFilesListLimit)]
		paths := make([]string, len(shown))
		for i, e := range shown {
			paths[i] = e.Path
		}
		slog.Info("left changed files out of the diff the session reads (--diff-exclusions)", "files", report.ExcludedFileCount(d.Excluded), "paths", strings.Join(paths, ", "))
	}
	return d, cleanup
}

// diffArgs returns the argv of the hardened diff over revs, limited to paths
// when it holds any and leaving out every entry of excluded. It must run at the
// top level: there `.` keeps the whole tree in scope, and a literal pathspec
// takes the path as written, so a name with a glob character selects or
// excludes only itself. A directory pathspec excludes its subtree.
func diffArgs(revs, paths []string, excluded []report.ExcludedFile) []string {
	args := []string{"-c", "core.quotePath=false", "--no-pager", "diff", "--no-ext-diff", "--no-textconv", "--no-color", "--src-prefix=a/", "--dst-prefix=b/"}
	args = append(args, revs...)
	args = append(args, "--")
	args = append(args, literalPathspecs(paths)...)
	if len(excluded) == 0 {
		return args
	}
	if len(paths) == 0 {
		args = append(args, ".")
	}
	for _, e := range excluded {
		args = append(args, ":(exclude,literal)"+strings.TrimSuffix(e.Path, "/"))
	}
	return args
}

// topLevel returns the top-level directory of the checkout dir lies in.
func topLevel(dir string) (string, error) {
	var out bytes.Buffer
	if err := runGit(dir, &out, []string{"rev-parse", "--show-toplevel"}); err != nil {
		return "", err
	}
	return strings.TrimSuffix(out.String(), "\n"), nil
}

// literalPathspecs returns one `:(literal)` pathspec per entry of paths.
func literalPathspecs(paths []string) []string {
	specs := make([]string, len(paths))
	for i, p := range paths {
		specs[i] = ":(literal)" + p
	}
	return specs
}

// changedPaths lists the paths the diff over revs changes, limited to paths
// when it holds any, as git prints them with -z: unquoted and NUL-separated,
// so a name with a newline or a quote is one entry.
func changedPaths(repoDir string, revs, paths []string) ([]string, error) {
	args := append([]string{"-c", "core.quotePath=false", "diff", "--name-only", "-z"}, revs...)
	args = append(args, "--")
	args = append(args, literalPathspecs(paths)...)
	var out bytes.Buffer
	if err := runGit(repoDir, &out, args); err != nil {
		return nil, err
	}
	var changed []string
	for p := range strings.SplitSeq(out.String(), "\x00") {
		if p != "" {
			changed = append(changed, p)
		}
	}
	return changed, nil
}

// writeDiff runs git with args in repoDir, writes its output to a new file at
// file (mode 0600), and returns the number of lines written. A diff that
// exceeds maxDiffBytes stops the copy and fails.
func writeDiff(repoDir, file string, args []string) (int, error) {
	f, err := os.OpenFile(file, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return 0, fmt.Errorf("creating the diff file: %w", err)
	}
	w := &cappedWriter{w: f, max: maxDiffBytes}
	runErr := runGit(repoDir, w, args)
	closeErr := f.Close()
	switch {
	case w.exceeded:
		return 0, fmt.Errorf("git %s: the diff exceeds %d MiB", strings.Join(args, " "), maxDiffBytes>>20)
	case runErr != nil:
		return 0, runErr
	case closeErr != nil:
		return 0, fmt.Errorf("writing the diff file: %w", closeErr)
	}
	return w.lines, nil
}

// runGit runs git with args in dir under gitTimeout, and gitWaitDelay past it,
// writing its stdout to out, and wraps a failure with the command and git's
// stderr.
func runGit(dir string, out io.Writer, args []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.WaitDelay = gitWaitDelay
	cmd.Stdout = out
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// errDiffTooLarge stops the copy into a cappedWriter that reached its cap.
var errDiffTooLarge = errors.New("diff exceeds the size cap")

// cappedWriter passes writes through to w, counting the newlines, until max
// bytes have been written; the write that would exceed max fails and sets
// exceeded.
type cappedWriter struct {
	w        io.Writer
	max      int
	written  int
	lines    int
	exceeded bool
}

func (c *cappedWriter) Write(p []byte) (int, error) {
	if c.written+len(p) > c.max {
		c.exceeded = true
		return 0, errDiffTooLarge
	}
	n, err := c.w.Write(p)
	c.written += n
	c.lines += bytes.Count(p[:n], []byte("\n"))
	return n, err
}
