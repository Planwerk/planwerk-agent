// Package gitref reads repository content as it stands at a git ref rather
// than as it stands in the working tree.
//
// A review, fix, or address run works in a checkout of a pull request's head,
// and some of what the prompts load from that checkout decides how the change
// is judged: the review checklist, the project's review patterns, its TODO
// list, the skills a session is obliged to use. Read from the head, a pull
// request could rewrite the rules it is reviewed or repaired by. Read from the
// base branch, those files are the ones the maintainers merged.
package gitref

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// gitTimeout bounds each git invocation. Reading a handful of small files at a
// ref completes in milliseconds; the bound only stops a wedged git from
// stalling the run.
const gitTimeout = 30 * time.Second

// maxArchiveBytes caps what Materialize extracts. The directories it is used
// for hold a few dozen Markdown files; anything larger is not what the caller
// expected and is skipped whole.
const maxArchiveBytes = 8 << 20

// Show returns the blob at rel as of ref in the repository at repoDir. ok is
// false when ref is empty, the path does not exist at ref, git fails, or the
// blob is larger than maxBytes — every case a caller treats as "not there".
func Show(repoDir, ref, rel string, maxBytes int) (string, bool) {
	if ref == "" {
		return "", false
	}
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	// git addresses paths with forward slashes regardless of the host OS.
	cmd := exec.CommandContext(ctx, "git", "show", ref+":"+filepath.ToSlash(rel))
	cmd.Dir = repoDir
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	if maxBytes > 0 && len(out) > maxBytes {
		slog.Warn("skipping oversized file at ref", "ref", ref, "path", rel, "size", len(out))
		return "", false
	}
	return string(out), true
}

// Materialize writes the directory rel, as it stands at ref, into a fresh
// temporary directory and returns that directory's root: the files land at
// <root>/<rel>/…, so a loader that takes a repository root reads them exactly
// as it would read a checkout. cleanup removes the temporary directory and is
// always safe to call. ok is false when ref is empty, rel does not exist at
// ref, or git fails; root is then "" and nothing is left behind.
func Materialize(repoDir, ref, rel string) (root string, cleanup func(), ok bool) {
	cleanup = func() {}
	if ref == "" {
		return "", cleanup, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "archive", "--format=tar", ref, "--", filepath.ToSlash(rel))
	cmd.Dir = repoDir
	out, err := cmd.Output()
	if err != nil {
		// The path is absent at ref (git exits 128) or git failed: not there.
		return "", cleanup, false
	}
	if len(out) > maxArchiveBytes {
		slog.Warn("skipping oversized directory at ref", "ref", ref, "path", rel, "size", len(out))
		return "", cleanup, false
	}
	tmp, err := os.MkdirTemp("", "planwerk-ref-")
	if err != nil {
		slog.Warn("creating a directory for content at ref failed", "ref", ref, "path", rel, "err", err)
		return "", cleanup, false
	}
	cleanup = func() { _ = os.RemoveAll(tmp) }
	if err := extract(tmp, rel, out); err != nil {
		slog.Warn("extracting content at ref failed", "ref", ref, "path", rel, "err", err)
		cleanup()
		return "", func() {}, false
	}
	return tmp, cleanup, true
}

// extract unpacks the regular files of a tar stream under root, refusing any
// entry whose name is not a local path inside rel.
func extract(root, rel string, archive []byte) error {
	prefix := filepath.ToSlash(filepath.Clean(rel)) + "/"
	tr := tar.NewReader(bytes.NewReader(archive))
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		name := filepath.ToSlash(filepath.Clean(hdr.Name))
		if !filepath.IsLocal(name) || !strings.HasPrefix(name, prefix) {
			continue
		}
		dst := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
			return err
		}
		data, err := io.ReadAll(io.LimitReader(tr, maxArchiveBytes))
		if err != nil {
			return err
		}
		if err := os.WriteFile(dst, data, 0o600); err != nil {
			return err
		}
	}
}
