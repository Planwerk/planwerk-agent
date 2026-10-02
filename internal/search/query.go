package search

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

// DefaultLimit is the number of hits of a search without a limit of its own.
const DefaultLimit = 10

// Query is one search. A Limit below 1 means DefaultLimit.
type Query struct {
	Text   string
	Types  []string // issue, pull, wiki; none means all
	State  string
	Labels []string
	Limit  int
}

// Block describes one block of a file.
type Block struct {
	Ordinal     int    `json:"ordinal"`
	Count       int    `json:"count"`
	Kind        string `json:"kind"`
	Author      string `json:"author"`
	Association string `json:"association"`
	URL         string `json:"url"`
	CreatedAt   string `json:"created_at"`
}

// Hit is one item with the block that matched best, or, from Index.Block,
// one block with its text.
type Hit struct {
	ID        string   `json:"id"` // <file>:<ordinal>
	File      string   `json:"file"`
	Type      string   `json:"type"`
	Number    int      `json:"number"`
	Title     string   `json:"title"`
	State     string   `json:"state"`
	Labels    []string `json:"labels"`
	URL       string   `json:"url"`
	UpdatedAt string   `json:"updated_at"`
	Block     Block    `json:"block"`
	Excerpt   string   `json:"excerpt,omitempty"`
	Score     float64  `json:"score,omitempty"`
	Text      string   `json:"text,omitempty"`
}

// ErrNoTerms reports a query without a word that can be searched for.
var ErrNoTerms = errors.New("the query holds no word to search for")

// ErrNoBlock reports a block id that names no block of the index.
var ErrNoBlock = errors.New("no such block in the mirror")

// rankSelect is the statement that ranks the blocks that match, up to its
// filters. bm25 is lower for a better match. It reads no text: the excerpt of
// a block is cut once the block is known to be a hit (hitSelect).
const rankSelect = `SELECT b.rowid, b.path, b.ordinal, bm25(blocks) AS score
FROM blocks b JOIN files f ON f.path = b.path
WHERE blocks MATCH ?`

// hitSelect is the statement that reads one ranked block with its file. The
// excerpt is cut from the text column around the words that matched, so the
// statement takes the match expression again. The excerpt carries no marks
// around those words and is at most 40 tokens long.
const hitSelect = `SELECT b.kind, b.author, b.association, b.url, b.created_at,
       f.type, f.number, f.title, f.state, f.labels, f.url, f.updated_at, f.block_count,
       snippet(blocks, 0, '', '', '...', 40)
FROM blocks b JOIN files f ON f.path = b.path
WHERE blocks MATCH ? AND b.rowid = ?`

// blockSelect is the statement that reads one block with its file, by the
// path of the file and the ordinal of the block. The blocks of a file have the
// rowids from first_block on, so the block is read by its rowid.
const blockSelect = `SELECT b.kind, b.author, b.association, b.url, b.created_at, b.text,
       f.type, f.number, f.title, f.state, f.labels, f.url, f.updated_at, f.block_count
FROM files f JOIN blocks b ON b.rowid = f.first_block + ?2 - 1
WHERE f.path = ?1 AND ?2 BETWEEN 1 AND f.block_count`

// Search returns the items that match q, best first, each with the block that
// matched best. A block matches when it holds at least one of the words of
// q.Text (matchExpr), and a block that holds more of them, and rarer ones,
// ranks higher. The filters keep the items of the given types, in the given
// state, that carry every given label; a wiki page has no state and no label.
// Without a match it returns an empty slice. A text without a word returns
// ErrNoTerms.
func (ix *Index) Search(q Query) ([]Hit, error) {
	expr, err := matchExpr(q.Text)
	if err != nil {
		return nil, err
	}
	// One read transaction: the ranked rowids and the rows read by them come
	// from the same snapshot, whatever another process writes meanwhile. A
	// read-only transaction begins without the write lock of dsn.
	tx, err := ix.db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("searching the index: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	best, err := rank(tx, q, expr)
	if err != nil {
		return nil, fmt.Errorf("searching the index: %w", err)
	}

	hits := make([]Hit, 0, len(best))
	for _, r := range best {
		h := Hit{ID: blockID(r.file, r.ordinal), File: r.file, Score: -r.score, Block: Block{Ordinal: r.ordinal}}
		var labels, excerpt string
		if err := tx.QueryRow(hitSelect, expr, r.rowid).Scan(
			&h.Block.Kind, &h.Block.Author, &h.Block.Association, &h.Block.URL, &h.Block.CreatedAt,
			&h.Type, &h.Number, &h.Title, &h.State, &labels, &h.URL, &h.UpdatedAt, &h.Block.Count,
			&excerpt); err != nil {
			return nil, fmt.Errorf("searching the index: %w", err)
		}
		h.Labels = splitLabels(labels)
		h.Excerpt = strings.Join(strings.Fields(excerpt), " ")
		hits = append(hits, h)
	}
	return hits, nil
}

// ranked is a block that matched a search: its rowid, its file, its ordinal,
// and its bm25 score.
type ranked struct {
	rowid   int64
	file    string
	ordinal int
	score   float64
}

// rank returns the best block of each of the best files that match expr under
// the filters of q, best first. The limit of q counts files, not blocks: the
// rows are read until that many files have a block. It reads through tx, the
// transaction the hits are then read in.
func rank(tx *sql.Tx, q Query, expr string) ([]ranked, error) {
	limit := q.Limit
	if limit < 1 {
		limit = DefaultLimit
	}

	// The statement is joined from constant fragments and placeholders: no
	// text of the caller is part of it.
	var stmt strings.Builder
	stmt.WriteString(rankSelect)
	args := []any{expr}
	if len(q.Types) > 0 {
		stmt.WriteString(" AND f.type IN (?" + strings.Repeat(", ?", len(q.Types)-1) + ")")
		for _, t := range q.Types {
			args = append(args, t)
		}
	}
	if q.State != "" {
		stmt.WriteString(" AND f.state = ?")
		args = append(args, q.State)
	}
	for _, l := range q.Labels {
		stmt.WriteString(" AND instr(f.labels, ?) > 0")
		args = append(args, "\n"+strings.ToLower(l)+"\n")
	}
	stmt.WriteString(" ORDER BY score, b.path, b.ordinal")

	rows, err := tx.Query(stmt.String(), args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	// The rows come best first, so the first row of a file is its best block.
	var best []ranked
	seen := map[string]bool{}
	for len(best) < limit && rows.Next() {
		var r ranked
		if err := rows.Scan(&r.rowid, &r.file, &r.ordinal, &r.score); err != nil {
			return nil, err
		}
		if seen[r.file] {
			continue
		}
		seen[r.file] = true
		best = append(best, r)
	}
	return best, rows.Err()
}

// Block returns the block with the given id, as a hit prints it, with its
// text and without an excerpt and a score. An id that names no block returns
// ErrNoBlock. Neither error holds the id: the id is an argument of the
// command, and what a shell expanded into it must not come back in an error.
func (ix *Index) Block(id string) (Hit, error) {
	sep := strings.LastIndex(id, ":")
	ordinal := 0
	if sep >= 0 {
		// A number that does not parse leaves the ordinal at 0.
		ordinal, _ = strconv.Atoi(id[sep+1:])
	}
	if ordinal < 1 {
		return Hit{}, errors.New("invalid block id: want <file>:<number>, as a hit prints it")
	}

	h := Hit{File: id[:sep], Block: Block{Ordinal: ordinal}}
	var labels string
	err := ix.db.QueryRow(blockSelect, h.File, ordinal).Scan(
		&h.Block.Kind, &h.Block.Author, &h.Block.Association, &h.Block.URL, &h.Block.CreatedAt, &h.Text,
		&h.Type, &h.Number, &h.Title, &h.State, &labels, &h.URL, &h.UpdatedAt, &h.Block.Count)
	if errors.Is(err, sql.ErrNoRows) {
		return Hit{}, ErrNoBlock
	}
	if err != nil {
		return Hit{}, fmt.Errorf("reading the index: %w", err)
	}
	h.ID = blockID(h.File, ordinal)
	h.Labels = splitLabels(labels)
	return h, nil
}

// blockID returns the id of the block with the given ordinal of file.
func blockID(file string, ordinal int) string {
	return file + ":" + strconv.Itoa(ordinal)
}

// splitLabels returns the labels of a labels column: an empty slice for none,
// never nil, so the JSON form prints a list.
func splitLabels(column string) []string {
	column = strings.Trim(column, "\n")
	if column == "" {
		return []string{}
	}
	return strings.Split(column, "\n")
}

// matchExpr turns the text of a query into an FTS5 expression. It reads the
// text from the left. A run between two double quotes is a phrase, and an
// opening quote without a closing one runs to the end. Every other run without
// whitespace is a word, and a word that ends in "*" and has a character before
// it is a prefix. A quote opens a phrase only where a term starts: inside a
// word it belongs to the word. A term without a letter or a digit is dropped.
//
// Each term is written in double quotes, with every quote in it doubled, and
// "*" after it for a prefix. The terms are joined with OR, so no operator of
// the caller reaches the engine and a text with punctuation is never a syntax
// error. A NUL separates words like a space: the engine reads one as the end
// of the expression. A text without a term returns ErrNoTerms.
func matchExpr(text string) (string, error) {
	text = strings.ReplaceAll(text, "\x00", " ")
	var terms []string
	add := func(term string, prefix bool) {
		if !strings.ContainsFunc(term, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }) {
			return
		}
		term = `"` + strings.ReplaceAll(term, `"`, `""`) + `"`
		if prefix {
			term += "*"
		}
		terms = append(terms, term)
	}

	rest := strings.TrimLeftFunc(text, unicode.IsSpace)
	for rest != "" {
		var term string
		if phrase, ok := strings.CutPrefix(rest, `"`); ok {
			term, rest, _ = strings.Cut(phrase, `"`)
			add(term, false)
		} else {
			end := strings.IndexFunc(rest, unicode.IsSpace)
			if end < 0 {
				end = len(rest)
			}
			term, rest = rest[:end], rest[end:]
			word, prefix := strings.CutSuffix(term, "*")
			if prefix && word != "" {
				add(word, true)
			} else {
				add(term, false)
			}
		}
		rest = strings.TrimLeftFunc(rest, unicode.IsSpace)
	}
	if len(terms) == 0 {
		return "", ErrNoTerms
	}
	return strings.Join(terms, " OR "), nil
}
