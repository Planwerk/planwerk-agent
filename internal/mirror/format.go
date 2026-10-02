package mirror

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/planwerk/planwerk-agent/internal/github"
)

// formatVersion is the version of the item file format, written to the
// frontmatter as "format".
const formatVersion = 1

// A block marker is the line "<!-- planwerk-agent:mirror <kind> <json> -->".
const (
	markerPrefix = "<!-- planwerk-agent:mirror "
	markerSuffix = " -->"
)

// The kinds of block an item file holds.
const (
	blockBody          = "body"
	blockComment       = "comment"
	blockReview        = "review"
	blockThread        = "thread"
	blockThreadComment = "thread-comment"
	blockCommit        = "commit"
)

// errNoFrontmatter reports an item file that does not open with a frontmatter
// between two "---" lines.
var errNoFrontmatter = errors.New("no frontmatter")

// Frontmatter is the head of an item file: everything about an issue or a
// pull request but its texts. Every key is written, whatever its value, and a
// list without an entry is written as an empty list.
type Frontmatter struct {
	Format            int      `yaml:"format"`
	Kind              string   `yaml:"kind"`
	Repo              string   `yaml:"repo"`
	Number            int      `yaml:"number"`
	ID                string   `yaml:"id"`
	URL               string   `yaml:"url"`
	Title             string   `yaml:"title"`
	State             string   `yaml:"state"`
	StateReason       string   `yaml:"state_reason"`
	Author            string   `yaml:"author"`
	AuthorIsBot       bool     `yaml:"author_is_bot"`
	AuthorAssociation string   `yaml:"author_association"`
	Labels            []string `yaml:"labels"`
	CreatedAt         string   `yaml:"created_at"`
	UpdatedAt         string   `yaml:"updated_at"`
	ClosedAt          string   `yaml:"closed_at"`
	MergedAt          string   `yaml:"merged_at"`
	BaseBranch        string   `yaml:"base_branch"`
	ClosedByPRs       []int    `yaml:"closed_by_prs"`
	CloserSHA         string   `yaml:"closer_commit"`
	ClosesIssues      []int    `yaml:"closes_issues"`
}

// The JSON of a block marker, one struct per kind, so the key order is fixed.
// Lines is the number of lines of the block's text.
type (
	bodyMarker struct {
		Lines int `json:"lines"`
	}
	commentMarker struct {
		ID          string `json:"id"`
		URL         string `json:"url"`
		Author      string `json:"author"`
		Association string `json:"association"`
		Created     string `json:"created"`
		Updated     string `json:"updated"`
		Lines       int    `json:"lines"`
	}
	reviewMarker struct {
		ID          string `json:"id"`
		URL         string `json:"url"`
		Author      string `json:"author"`
		Association string `json:"association"`
		State       string `json:"state"`
		Submitted   string `json:"submitted"`
		Updated     string `json:"updated"`
		Lines       int    `json:"lines"`
	}
	threadMarker struct {
		ID       string `json:"id"`
		Path     string `json:"path"`
		Line     int    `json:"line"`
		Resolved bool   `json:"resolved"`
		Outdated bool   `json:"outdated"`
		Lines    int    `json:"lines"`
	}
	commitMarker struct {
		SHA       string `json:"sha"`
		Committed string `json:"committed"`
		Headline  string `json:"headline"`
		Lines     int    `json:"lines"`
	}
)

// Render returns the item file of it in the mirror of repo ("owner/name"): the
// frontmatter, the title as a heading, and one block per text in the order
// body, comments, reviews, review threads each followed by its comments, and
// commits. It is a pure function of its arguments, so the same item renders to
// the same bytes on every run.
//
// A block is a marker line, the lines of its text, and a blank line. The
// marker carries the number of text lines, and the text is written verbatim: a
// reader takes that many lines and never scans a text for markers, so no text
// can open or close a block.
func Render(repo string, it *github.Item) []byte {
	fm := Frontmatter{
		Format:            formatVersion,
		Kind:              it.Kind,
		Repo:              repo,
		Number:            it.Number,
		ID:                it.ID,
		URL:               it.URL,
		Title:             it.Title,
		State:             it.State,
		StateReason:       it.StateReason,
		Author:            it.Author,
		AuthorIsBot:       it.AuthorIsBot,
		AuthorAssociation: it.AuthorAssociation,
		// A nil list is written as an empty one, whatever the encoder would
		// print for nil.
		Labels:       append([]string{}, it.Labels...),
		CreatedAt:    it.CreatedAt,
		UpdatedAt:    it.UpdatedAt,
		ClosedAt:     it.ClosedAt,
		MergedAt:     it.MergedAt,
		BaseBranch:   it.BaseBranch,
		ClosedByPRs:  append([]int{}, it.ClosedByPRs...),
		CloserSHA:    it.CloserSHA,
		ClosesIssues: append([]int{}, it.ClosesIssues...),
	}
	// A struct of strings, numbers, booleans, and lists of them always encodes.
	head, _ := yaml.Marshal(fm)

	var b bytes.Buffer
	b.WriteString("---\n")
	b.Write(head)
	b.WriteString("---\n\n")
	// The heading is one line: a line break in the title would let the rest of
	// it stand on a line of its own. The frontmatter keeps the title verbatim.
	b.WriteString("# " + strings.NewReplacer("\r", " ", "\n", " ").Replace(it.Title) + "\n\n")

	writeBlock(&b, blockBody, bodyMarker{Lines: countLines(it.Body)}, it.Body)
	for _, c := range it.Comments {
		writeBlock(&b, blockComment, newCommentMarker(c), c.Body)
	}
	for _, r := range it.Reviews {
		writeBlock(&b, blockReview, reviewMarker{
			ID: r.ID, URL: r.URL, Author: r.Author, Association: r.AuthorAssociation,
			State: r.State, Submitted: r.SubmittedAt, Updated: r.UpdatedAt, Lines: countLines(r.Body),
		}, r.Body)
	}
	for _, t := range it.Threads {
		writeBlock(&b, blockThread, threadMarker{
			ID: t.ID, Path: t.Path, Line: t.Line, Resolved: t.IsResolved, Outdated: t.IsOutdated, Lines: countLines(t.DiffHunk),
		}, t.DiffHunk)
		for _, c := range t.Comments {
			writeBlock(&b, blockThreadComment, newCommentMarker(c), c.Body)
		}
	}
	for _, c := range it.Commits {
		writeBlock(&b, blockCommit, commitMarker{
			SHA: c.SHA, Committed: c.CommittedAt, Headline: c.Headline, Lines: countLines(c.Body),
		}, c.Body)
	}
	return b.Bytes()
}

// newCommentMarker returns the marker of a comment block.
func newCommentMarker(c github.ItemComment) commentMarker {
	return commentMarker{
		ID: c.ID, URL: c.URL, Author: c.Author, Association: c.AuthorAssociation,
		Created: c.CreatedAt, Updated: c.UpdatedAt, Lines: countLines(c.Body),
	}
}

// countLines returns the number of lines a block's text takes: 0 for an empty
// text, else the number of line feeds plus one.
func countLines(text string) int {
	if text == "" {
		return 0
	}
	return strings.Count(text, "\n") + 1
}

// writeBlock writes one block: the marker line, the text, and a blank line.
// encoding/json escapes "<", ">", and "&", so no value of the marker can close
// the HTML comment.
func writeBlock(b *bytes.Buffer, kind string, marker any, text string) {
	// A struct of strings, numbers, and booleans always encodes.
	data, _ := json.Marshal(marker)
	b.WriteString(markerPrefix + kind + " ")
	b.Write(data)
	b.WriteString(markerSuffix + "\n")
	if text != "" {
		b.WriteString(text + "\n")
	}
	b.WriteString("\n")
}

// Parse reads an item file back into the item Render was given. A list
// without an entry is nil.
//
// After the frontmatter it scans line by line: a marker line opens a block
// whose next "lines" lines are its text, and every other line outside a block
// is skipped. A line number in an error counts from the first line of the
// file.
func Parse(data []byte) (*github.Item, error) {
	r := bufio.NewReader(bytes.NewReader(data))
	fm, offset, err := readFrontmatter(r)
	if err != nil {
		return nil, err
	}
	// Reading from a bytes.Reader cannot fail.
	rest, _ := io.ReadAll(r)

	it := &github.Item{
		Kind:              fm.Kind,
		ID:                fm.ID,
		Number:            fm.Number,
		Title:             fm.Title,
		URL:               fm.URL,
		State:             fm.State,
		StateReason:       fm.StateReason,
		Author:            fm.Author,
		AuthorIsBot:       fm.AuthorIsBot,
		AuthorAssociation: fm.AuthorAssociation,
		CreatedAt:         fm.CreatedAt,
		UpdatedAt:         fm.UpdatedAt,
		ClosedAt:          fm.ClosedAt,
		MergedAt:          fm.MergedAt,
		BaseBranch:        fm.BaseBranch,
		CloserSHA:         fm.CloserSHA,
	}
	if len(fm.Labels) > 0 {
		it.Labels = fm.Labels
	}
	if len(fm.ClosedByPRs) > 0 {
		it.ClosedByPRs = fm.ClosedByPRs
	}
	if len(fm.ClosesIssues) > 0 {
		it.ClosesIssues = fm.ClosesIssues
	}

	// Only a line feed ends a line: a carriage return belongs to the text.
	lines := strings.Split(strings.TrimSuffix(string(rest), "\n"), "\n")
	for i := 0; i < len(lines); i++ {
		if !strings.HasPrefix(lines[i], markerPrefix) {
			continue
		}
		lineNo := offset + i + 1
		kind, n, add, err := parseMarker(lines[i], lineNo)
		if err != nil {
			return nil, err
		}
		if n > len(lines)-i-1 {
			return nil, fmt.Errorf("line %d: block of %d lines runs past the end of the file", lineNo, n)
		}
		if kind == blockThreadComment && len(it.Threads) == 0 {
			return nil, fmt.Errorf("line %d: thread-comment before any thread", lineNo)
		}
		add(it, strings.Join(lines[i+1:i+1+n], "\n"))
		i += n
	}
	return it, nil
}

// parseMarker decodes the marker line at lineNo. It returns the block's kind,
// the number of lines of its text, and a function that adds the block with
// that text to an item.
func parseMarker(line string, lineNo int) (kind string, lines int, add func(it *github.Item, text string), err error) {
	malformed := fmt.Errorf("line %d: malformed block marker", lineNo)
	inner, ok := strings.CutSuffix(strings.TrimPrefix(line, markerPrefix), markerSuffix)
	if !ok {
		return "", 0, nil, malformed
	}
	kind, raw, ok := strings.Cut(inner, " ")
	if !ok || kind == "" {
		return "", 0, nil, malformed
	}

	switch kind {
	case blockBody:
		var m bodyMarker
		err = json.Unmarshal([]byte(raw), &m)
		lines = m.Lines
		add = func(it *github.Item, text string) { it.Body = text }
	case blockComment, blockThreadComment:
		var m commentMarker
		err = json.Unmarshal([]byte(raw), &m)
		lines = m.Lines
		add = func(it *github.Item, text string) {
			c := github.ItemComment{
				ID: m.ID, URL: m.URL, Author: m.Author, AuthorAssociation: m.Association,
				CreatedAt: m.Created, UpdatedAt: m.Updated, Body: text,
			}
			if kind == blockComment {
				it.Comments = append(it.Comments, c)
				return
			}
			last := &it.Threads[len(it.Threads)-1]
			last.Comments = append(last.Comments, c)
		}
	case blockReview:
		var m reviewMarker
		err = json.Unmarshal([]byte(raw), &m)
		lines = m.Lines
		add = func(it *github.Item, text string) {
			it.Reviews = append(it.Reviews, github.ItemReview{
				ID: m.ID, URL: m.URL, Author: m.Author, AuthorAssociation: m.Association,
				State: m.State, SubmittedAt: m.Submitted, UpdatedAt: m.Updated, Body: text,
			})
		}
	case blockThread:
		var m threadMarker
		err = json.Unmarshal([]byte(raw), &m)
		lines = m.Lines
		add = func(it *github.Item, text string) {
			it.Threads = append(it.Threads, github.ItemThread{
				ID: m.ID, IsResolved: m.Resolved, IsOutdated: m.Outdated, Path: m.Path, Line: m.Line, DiffHunk: text,
			})
		}
	case blockCommit:
		var m commitMarker
		err = json.Unmarshal([]byte(raw), &m)
		lines = m.Lines
		add = func(it *github.Item, text string) {
			it.Commits = append(it.Commits, github.ItemCommit{SHA: m.SHA, CommittedAt: m.Committed, Headline: m.Headline, Body: text})
		}
	default:
		return "", 0, nil, fmt.Errorf("line %d: unknown block kind %q", lineNo, kind)
	}
	if err != nil || lines < 0 {
		return "", 0, nil, malformed
	}
	return kind, lines, add, nil
}

// ReadFrontmatter reads the frontmatter of the item file at path. It reads the
// file up to the closing "---" line and no further, so a listing of many items
// never loads their conversations.
func ReadFrontmatter(path string) (Frontmatter, error) {
	f, err := os.Open(path)
	if err != nil {
		return Frontmatter{}, fmt.Errorf("reading %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	fm, _, err := readFrontmatter(bufio.NewReader(f))
	if err != nil {
		return Frontmatter{}, fmt.Errorf("reading %s: %w", path, err)
	}
	return fm, nil
}

// readFrontmatter reads a line "---", the YAML up to the next line that is
// exactly "---", and decodes it. lines is the number of lines it consumed. A
// bufio.Reader, not a Scanner, reads the lines: a frontmatter value has no
// length limit.
func readFrontmatter(r *bufio.Reader) (fm Frontmatter, lines int, err error) {
	var head bytes.Buffer
	for {
		line, err := r.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return Frontmatter{}, 0, err
		}
		atEOF := err != nil
		if atEOF && line == "" {
			return Frontmatter{}, 0, errNoFrontmatter
		}
		lines++
		isFence := strings.TrimSuffix(line, "\n") == "---"
		if lines == 1 {
			if !isFence {
				return Frontmatter{}, 0, errNoFrontmatter
			}
			continue
		}
		if isFence {
			break
		}
		if atEOF {
			return Frontmatter{}, 0, errNoFrontmatter
		}
		head.WriteString(line)
	}
	if err := yaml.Unmarshal(head.Bytes(), &fm); err != nil {
		return Frontmatter{}, 0, fmt.Errorf("frontmatter: %w", err)
	}
	if fm.Format != formatVersion {
		return Frontmatter{}, 0, fmt.Errorf("unsupported mirror format %d; run brain sync --full to rebuild", fm.Format)
	}
	return fm, lines, nil
}
