package brain

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

const (
	// maxDecisionDocBytes is the largest decision document the bootstrap reads;
	// a larger file is skipped with a warning.
	maxDecisionDocBytes = 2 << 20
	// docChunkBytes is the largest chunk a decision document is cut into. A
	// single line longer than this is a chunk of its own.
	docChunkBytes = 16 << 10
)

// DocChunk is one piece of a decision document: the repository-relative slash
// path of the document, the 1-based index of the chunk, the number of chunks
// the document was cut into, and the chunk's text.
type DocChunk struct {
	Path  string
	Index int
	Count int
	Text  string
}

// key returns the unit key of the chunk. It holds a hash of the chunk's text
// and not its index, so appending to a document changes the key of its last
// chunk only and the earlier chunks are not processed again.
func (c DocChunk) key() string {
	sum := sha256.Sum256([]byte(c.Text))
	return "doc-" + c.Path + "@" + hex.EncodeToString(sum[:])[:keyHashLen]
}

// decisionDirs are the directory names, and decisionFiles the file names, that
// mark a Markdown file as a decision document. Both are compared in lower case.
var (
	decisionDirs  = []string{"adr", "adrs", "decisions"}
	decisionFiles = []string{"design-decisions.md", "decisions.md", "decision-log.md", "adr.md"}
)

// isDecisionDoc reports whether the repository-relative slash path names a
// decision document: a Markdown file inside a decisionDirs directory, or one
// named like a decisionFiles entry.
func isDecisionDoc(rel string) bool {
	rel = strings.ToLower(rel)
	if !strings.HasSuffix(rel, ".md") {
		return false
	}
	segments := strings.Split(rel, "/")
	base := segments[len(segments)-1]
	if slices.Contains(decisionFiles, base) {
		return true
	}
	return slices.ContainsFunc(segments[:len(segments)-1], func(seg string) bool {
		return slices.Contains(decisionDirs, seg)
	})
}

// discoverDecisionDocs walks the checkout at root and returns the
// repository-relative slash paths of its decision documents (isDecisionDoc),
// in walk order. It skips .git and keeps regular files only, so a symlink is
// never followed.
func discoverDecisionDocs(root string) ([]string, error) {
	var paths []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		if rel = filepath.ToSlash(rel); isDecisionDoc(rel) {
			paths = append(paths, rel)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("finding decision documents: %w", err)
	}
	return paths, nil
}

// resolveDecisionDocs returns the repository-relative slash paths of the
// decision documents a run reads from the checkout at root: none when none is
// set, the explicit paths when there are any, and the discovered ones
// otherwise. An explicit path must be relative, canonical, without a ".."
// segment, and a regular file inside the checkout.
func resolveDecisionDocs(root string, explicit []string, none bool) ([]string, error) {
	if none {
		return nil, nil
	}
	if len(explicit) == 0 {
		return discoverDecisionDocs(root)
	}
	checkout, err := os.OpenRoot(root)
	if err != nil {
		return nil, fmt.Errorf("opening the checkout: %w", err)
	}
	defer func() { _ = checkout.Close() }()
	for _, p := range explicit {
		if path.IsAbs(p) || path.Clean(p) != p || p == ".." || strings.HasPrefix(p, "../") {
			return nil, fmt.Errorf("decision document %q not found in the repository", p)
		}
		// Lstat through the root reports a symlink as such and refuses a path
		// that leaves the checkout through a symlinked directory.
		info, err := checkout.Lstat(filepath.FromSlash(p))
		if err != nil || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("decision document %q not found in the repository", p)
		}
	}
	return explicit, nil
}

// docPathRe matches a path that is safe inside an HTML comment: the path of a
// document unit is written into the provenance marker of the pages it yields.
var docPathRe = regexp.MustCompile(`^[A-Za-z0-9._/-]+$`)

// loadDocChunks reads the decision documents at paths (repository-relative,
// slash form) from the checkout at root and returns their chunks. A document
// is skipped with a warning when its path is not safe inside an HTML comment,
// when it is larger than maxDecisionDocBytes, and when it cannot be read.
func loadDocChunks(root string, paths []string) []DocChunk {
	var chunks []DocChunk
	for _, p := range paths {
		if !docPathRe.MatchString(p) || strings.Contains(p, "--") {
			// The path failed the check because of what it holds, which can be
			// an escape sequence or a newline: quote it for the terminal.
			slog.Warn("skipping decision document whose path holds a character outside letters, digits, '.', '_', '/', and '-', or holds \"--\"", "path", strconv.Quote(p))
			continue
		}
		text, err := readDecisionDoc(root, p)
		if err != nil {
			if errors.Is(err, errDecisionDocTooLarge) {
				slog.Warn("skipping oversized decision document", "path", p, "cap", maxDecisionDocBytes)
			} else {
				slog.Warn("skipping unreadable decision document", "path", p, "err", err)
			}
			continue
		}
		chunks = append(chunks, chunkDocument(p, text)...)
	}
	return chunks
}

// errDecisionDocTooLarge marks a decision document larger than
// maxDecisionDocBytes.
var errDecisionDocTooLarge = errors.New("decision document exceeds size cap")

// readDecisionDoc reads the document at the repository-relative slash path p
// inside the checkout at root through a bounded read, so an oversized file is
// rejected without being held in memory.
func readDecisionDoc(root, p string) (string, error) {
	checkout, err := os.OpenRoot(root)
	if err != nil {
		return "", err
	}
	defer func() { _ = checkout.Close() }()
	f, err := checkout.Open(filepath.FromSlash(p))
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, maxDecisionDocBytes+1))
	if err != nil {
		return "", err
	}
	if len(data) > maxDecisionDocBytes {
		return "", errDecisionDocTooLarge
	}
	return string(data), nil
}

// chunkDocument cuts text at line boundaries into chunks of at most
// docChunkBytes, filling each chunk before it starts the next. A single line
// longer than that is a chunk of its own. The chunks concatenate to text; an
// empty document yields none.
func chunkDocument(docPath, text string) []DocChunk {
	var chunks []DocChunk
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			chunks = append(chunks, DocChunk{Path: docPath, Text: cur.String()})
			cur.Reset()
		}
	}
	for _, line := range strings.SplitAfter(text, "\n") {
		if cur.Len()+len(line) > docChunkBytes {
			flush()
		}
		cur.WriteString(line)
	}
	flush()
	for i := range chunks {
		chunks[i].Index, chunks[i].Count = i+1, len(chunks)
	}
	return chunks
}
