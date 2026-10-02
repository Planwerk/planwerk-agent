package brain

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"strings"

	"github.com/planwerk/planwerk-agent/internal/github"
	"github.com/planwerk/planwerk-agent/internal/mirror"
	"github.com/planwerk/planwerk-agent/internal/search"
)

// SearchOptions configures one `brain search` call.
type SearchOptions struct {
	RepoRef string
	Query   string
	Show    string // a block id; set in place of Query
	Types   []string
	State   string
	Labels  []string
	Limit   int
	JSON    bool
	// Root is the directory the mirrors live under; empty means
	// mirror.DefaultRoot().
	Root string
}

// searchResult is the JSON form of a search.
type searchResult struct {
	Repo     string       `json:"repo"`
	SyncedAt string       `json:"synced_at"`
	Hits     []search.Hit `json:"hits"`
}

// blockResult is the JSON form of one block.
type blockResult struct {
	Repo     string     `json:"repo"`
	SyncedAt string     `json:"synced_at"`
	Block    search.Hit `json:"block"`
}

// Search writes to w what the mirror of opts.RepoRef holds for opts.Query:
// the best-matching issues, pull requests, and wiki pages, each with the block
// that matched best, an excerpt, and the id of that block. With opts.Show it
// writes the block of that id in full, in place of a search.
//
// The mirror is read as it is. Search does not sync it, and it stops when the
// mirror is missing or no sync of it has finished. It brings the search index
// in the mirror directory up to date first (search.Open).
//
// What it writes is redacted, names a file relative to the mirror directory,
// and never holds the query: a session steered into putting file content into
// a query gets nothing back. For the same reason no error it returns holds the
// query, the block id, or a filter. The text form passes through printable.
func Search(w io.Writer, opts SearchOptions) error {
	owner, name, err := github.ParseRepoRef(opts.RepoRef)
	if err != nil {
		return fmt.Errorf("parsing repo ref: %w", err)
	}
	root := opts.Root
	if root == "" {
		if root, err = mirror.DefaultRoot(); err != nil {
			return err
		}
	}
	dir, err := mirror.Dir(root, owner, name)
	if err != nil {
		return err
	}
	repo := owner + "/" + name
	st, err := mirror.LoadFinishedState(dir, repo)
	if err != nil {
		return err
	}

	ix, err := search.Open(dir)
	if err != nil {
		return err
	}
	var hits []search.Hit
	var block search.Hit
	if opts.Show != "" {
		block, err = ix.Block(opts.Show)
	} else {
		hits, err = ix.Search(search.Query{Text: opts.Query, Types: opts.Types, State: opts.State, Labels: opts.Labels, Limit: opts.Limit})
	}
	if cerr := ix.Close(); cerr != nil {
		slog.Debug("closing the search index failed", "err", cerr)
	}
	if err != nil {
		return err
	}

	var out string
	switch {
	case opts.JSON && opts.Show != "":
		out, err = searchJSON(blockResult{Repo: repo, SyncedAt: st.SyncedAt, Block: block})
	case opts.JSON:
		out, err = searchJSON(searchResult{Repo: repo, SyncedAt: st.SyncedAt, Hits: hits})
	case opts.Show != "":
		out = printable(blockText(block))
	default:
		out = printable(hitsText(repo, st.SyncedAt, hits))
	}
	if err != nil {
		return err
	}
	if _, err := io.WriteString(w, out); err != nil {
		return fmt.Errorf("writing the search result: %w", err)
	}
	return nil
}

// searchJSON encodes a result as one JSON object and a line feed.
// encoding/json escapes control characters, so the output needs no printable.
func searchJSON(result any) (string, error) {
	data, err := json.Marshal(result)
	if err != nil {
		return "", fmt.Errorf("encoding the search result: %w", err)
	}
	return string(data) + "\n", nil
}

// hitsText renders the text form of a search: a line that names the
// repository, the end of the last sync, and the number of hits, and then every
// hit after a blank line, numbered from 1, with its lines after the head
// indented by three spaces.
func hitsText(repo, syncedAt string, hits []search.Hit) string {
	var b strings.Builder
	noun := "hits"
	if len(hits) == 1 {
		noun = "hit"
	}
	fmt.Fprintf(&b, "Search of %s, mirror synced %s: %d %s\n", repo, syncedAt, len(hits), noun)
	const indent = "   "
	for i, h := range hits {
		fmt.Fprintf(&b, "\n%d. %s\n", i+1, hitHead(h))
		if len(h.Labels) > 0 {
			b.WriteString(indent + "labels: " + strings.Join(h.Labels, ", ") + "\n")
		}
		b.WriteString(indent + blockLine(h.Block) + "\n")
		if url := hitURL(h); url != "" {
			b.WriteString(indent + url + "\n")
		}
		b.WriteString(indent + "id: " + h.ID + "\n")
		b.WriteString(indent + h.Excerpt + "\n")
	}
	return b.String()
}

// blockTextPrefix starts every line of a block's text in the text form.
const blockTextPrefix = "| "

// blockText renders the text form of one block: its head, what the block is,
// its URL, a blank line, a line that counts the lines of its text, and the
// text with blockTextPrefix before every line. The text is whatever its
// author wrote, so without the prefix a comment could end in lines that read
// like the head of another block, with another author and association. With
// it, a line of the output that does not start with the prefix is the
// command's, and one that does is the block's. Line feeds at the end of the
// text are dropped.
func blockText(h search.Hit) string {
	var b strings.Builder
	b.WriteString(hitHead(h) + "\n")
	b.WriteString(blockLine(h.Block) + "\n")
	if url := hitURL(h); url != "" {
		b.WriteString(url + "\n")
	}
	lines := strings.Split(strings.TrimRight(h.Text, "\n"), "\n")
	noun := "lines"
	if len(lines) == 1 {
		noun = "line"
	}
	fmt.Fprintf(&b, "\ntext, %d %s, each after %q:\n", len(lines), noun, blockTextPrefix)
	for _, line := range lines {
		b.WriteString(blockTextPrefix + line + "\n")
	}
	return b.String()
}

// hitHead returns the line that names the item of a hit: the type, the
// number, the state, and the title of an issue or a pull request, or the path
// of a wiki page in the clone and its title.
func hitHead(h search.Hit) string {
	if h.Type == search.TypeWiki {
		return "wiki " + mirror.WikiPagePath(h.File) + ": " + h.Title
	}
	return fmt.Sprintf("%s #%d [%s] %s", h.Type, h.Number, h.State, h.Title)
}

// blockLine returns the line that describes a block: its kind, then its
// author, the author's association, and its time where the block has them,
// and its position among the blocks of its file.
func blockLine(b search.Block) string {
	line := b.Kind
	if b.Author != "" {
		line += " by " + b.Author
	}
	if b.Association != "" {
		line += " (" + b.Association + ")"
	}
	if b.CreatedAt != "" {
		line += " on " + b.CreatedAt
	}
	return line + ", block " + strconv.Itoa(b.Ordinal) + " of " + strconv.Itoa(b.Count)
}

// hitURL returns the URL of a hit's block, or of its file when the block has
// none. It is empty for a wiki page.
func hitURL(h search.Hit) string {
	if h.Block.URL != "" {
		return h.Block.URL
	}
	return h.URL
}
