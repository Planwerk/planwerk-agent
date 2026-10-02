package search

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/planwerk/planwerk-agent/internal/github"
)

// writeFiller writes n issues, numbered from 100, that hold none of the words
// a test searches for. SQLite ranks a word that more than half of the blocks
// hold as if it told nothing, so a corpus needs blocks without the query
// words for its ranking to be the ranking of a real mirror.
func writeFiller(t *testing.T, dir string, n int) {
	t.Helper()
	for i := range n {
		writeItem(t, dir, testIssue(100+i, fmt.Sprintf("Unrelated topic %d", i), "Nothing of interest stands in this text."))
	}
}

func TestMatchExpr(t *testing.T) {
	for _, tc := range []struct{ name, text, want string }{
		{"two words", "one cursor", `"one" OR "cursor"`},
		{"a quoted phrase", `"items cursor"`, `"items cursor"`},
		{"a prefix", "curs*", `"curs"*`},
		{"an unclosed quote", `"items cursor`, `"items cursor"`},
		{"a quote inside a word", `say"hi`, `"say""hi"`},
		{"punctuation inside words", "c++ a?b", `"c++" OR "a?b"`},
		{"words, a phrase, a prefix, and punctuation", `why one "items cursor" curs* ?`, `"why" OR "one" OR "items cursor" OR "curs"*`},
		{"an operator of the engine", "cursor AND NOT (wiki)", `"cursor" OR "AND" OR "NOT" OR "(wiki)"`},
		{"whitespace of every kind", "\tone\n cursor  ", `"one" OR "cursor"`},
		{"a word right after a phrase", `"one cursor"shared`, `"one cursor" OR "shared"`},
		{"a star after a star", "curs**", `"curs*"*`},
		{"a NUL", "one\x00cursor \"items\x00cursor\"", `"one" OR "cursor" OR "items cursor"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := matchExpr(tc.text)
			if err != nil || got != tc.want {
				t.Errorf("matchExpr(%q) = %q, %v, want %q", tc.text, got, err, tc.want)
			}
		})
	}

	for _, text := range []string{"", "   ", "?!", "*", `""`, `"?!"`, "-- *"} {
		if got, err := matchExpr(text); !errors.Is(err, ErrNoTerms) || got != "" {
			t.Errorf("matchExpr(%q) = %q, %v, want ErrNoTerms", text, got, err)
		}
	}
}

func TestSearch_ATextWithoutAWordIsErrNoTerms(t *testing.T) {
	dir := newMirror(t)
	threeFiles(t, dir)
	ix := openIndex(t, dir)
	for _, text := range []string{"", "   ", "?!"} {
		if hits, err := ix.Search(Query{Text: text}); !errors.Is(err, ErrNoTerms) || hits != nil {
			t.Errorf("Search(%q) = %v, %v, want ErrNoTerms", text, hits, err)
		}
	}
}

func TestSearch_NoMatchIsAnEmptySlice(t *testing.T) {
	dir := newMirror(t)
	threeFiles(t, dir)
	ix := openIndex(t, dir)
	hits, err := ix.Search(Query{Text: "xylophone"})
	if err != nil || hits == nil || len(hits) != 0 {
		t.Errorf("Search = %#v, %v, want an empty slice and no error", hits, err)
	}
}

func TestSearch_RanksTheBlockWithMoreOfTheWordsFirst(t *testing.T) {
	dir := newMirror(t)
	writeFiller(t, dir, 8)
	writeItem(t, dir, testIssue(1, "First", "The sync moves the cursor."))
	writeItem(t, dir, testIssue(2, "Second", "The items cursor is shared."))
	ix := openIndex(t, dir)

	hits, err := ix.Search(Query{Text: "items cursor"})
	if err != nil || len(hits) != 2 {
		t.Fatalf("Search = %v, %v, want two hits", hits, err)
	}
	if hits[0].File != "issues/2.md" || hits[1].File != "issues/1.md" {
		t.Errorf("order = %s, %s, want the block with both words first", hits[0].File, hits[1].File)
	}
	if hits[0].Score <= hits[1].Score || hits[1].Score <= 0 {
		t.Errorf("scores = %v, %v, want the better hit higher and both above 0", hits[0].Score, hits[1].Score)
	}
}

func TestSearch_OneHitPerItemWithItsBestBlock(t *testing.T) {
	it := testIssue(1, "A question", "A note about the cursor.")
	it.Comments = []github.ItemComment{
		{Author: "alice", AuthorAssociation: "MEMBER", URL: "https://github.com/acme/widgets/issues/1#issuecomment-1", CreatedAt: "2026-09-05T08:00:00Z",
			Body: "The items cursor is one cursor for all items."},
		{Author: "bob", Body: "The cursor came up again, in passing, among many other words that make this block a long one."},
	}
	dir := newMirror(t)
	writeFiller(t, dir, 8)
	writeItem(t, dir, it)
	ix := openIndex(t, dir)

	hits, err := ix.Search(Query{Text: "items cursor"})
	if err != nil || len(hits) != 1 {
		t.Fatalf("Search = %v, %v, want the item once", hits, err)
	}
	want := Block{Ordinal: 3, Count: 4, Kind: "comment", Author: "alice", Association: "MEMBER", URL: "https://github.com/acme/widgets/issues/1#issuecomment-1", CreatedAt: "2026-09-05T08:00:00Z"}
	if hits[0].Block != want || hits[0].ID != "issues/1.md:3" {
		t.Errorf("hit = %s with block %+v, want issues/1.md:3 with %+v", hits[0].ID, hits[0].Block, want)
	}
}

func TestSearch_AWordOfTheTitleOnly(t *testing.T) {
	dir := newMirror(t)
	writeFiller(t, dir, 4)
	writeItem(t, dir, testIssue(1, "Zebra migration", "The body names no animal."))
	ix := openIndex(t, dir)

	hits, err := ix.Search(Query{Text: "zebra"})
	if err != nil || len(hits) != 1 {
		t.Fatalf("Search = %v, %v, want one hit", hits, err)
	}
	if hits[0].Block.Kind != "title" || hits[0].Block.Ordinal != 1 || hits[0].Title != "Zebra migration" {
		t.Errorf("hit = %+v, want the title block of the item", hits[0])
	}
}

func TestSearch_Limit(t *testing.T) {
	dir := newMirror(t)
	writeFiller(t, dir, 30)
	// A body that names the word more often ranks higher.
	for i := 1; i <= 12; i++ {
		writeItem(t, dir, testIssue(i, fmt.Sprintf("Topic %d", i), strings.Repeat("cursor ", i)+"and the rest of the text."))
	}
	ix := openIndex(t, dir)

	all := searchFiles(t, ix, Query{Text: "cursor", Limit: 100})
	if len(all) != 12 {
		t.Fatalf("a search with a wide limit found %d items, want 12", len(all))
	}
	if got := searchFiles(t, ix, Query{Text: "cursor", Limit: 2}); !slices.Equal(got, all[:2]) {
		t.Errorf("Limit 2 = %v, want the two best %v", got, all[:2])
	}
	for _, limit := range []int{0, -1} {
		if got := searchFiles(t, ix, Query{Text: "cursor", Limit: limit}); !slices.Equal(got, all[:DefaultLimit]) {
			t.Errorf("Limit %d = %v, want the %d best %v", limit, got, DefaultLimit, all[:DefaultLimit])
		}
	}
}

// TestSearch_LimitCountsItemsNotBlocks covers an item with several matching
// blocks ahead of another item: a limit of two yields both items, not two
// blocks of the first.
func TestSearch_LimitCountsItemsNotBlocks(t *testing.T) {
	chatty := testIssue(1, "The cursor", "The cursor moves.")
	chatty.Comments = []github.ItemComment{{Author: "alice", Body: "The cursor again."}, {Author: "bob", Body: "And the cursor once more."}}
	dir := newMirror(t)
	writeFiller(t, dir, 8)
	writeItem(t, dir, chatty)
	writeItem(t, dir, testIssue(2, "Second", "One cursor among the many other words of a long body of text."))
	ix := openIndex(t, dir)

	// The short blocks of the first item all rank above the long one of the
	// second.
	if got, want := searchFiles(t, ix, Query{Text: "cursor", Limit: 2}), []string{"issues/1.md", "issues/2.md"}; !slices.Equal(got, want) {
		t.Errorf("Limit 2 found %v, want both items: %v", got, want)
	}
}

// TestSearch_ReadsOneSnapshotBesideARewrite searches while a second index of
// the same mirror, as the search of another session opens it, replaces the
// blocks of every file again and again. A replaced block has a new rowid, and
// a search reads each hit by the rowid its ranking named: every search must
// still return every item, each hit with the number of its own file.
func TestSearch_ReadsOneSnapshotBesideARewrite(t *testing.T) {
	const items, rounds = 20, 25
	dir := newMirror(t)
	// The body grows with the round, so every rewrite changes the size.
	write := func(round int) {
		for i := 1; i <= items; i++ {
			writeItem(t, dir, testIssue(i, fmt.Sprintf("Topic %d", i), "The cursor moves"+strings.Repeat("!", round)))
		}
	}
	write(0)
	ix := openIndex(t, dir)

	stop := make(chan struct{})
	searched := make(chan error, 1)
	go func() {
		for {
			select {
			case <-stop:
				searched <- nil
				return
			default:
			}
			hits, err := ix.Search(Query{Text: "cursor", Limit: items})
			if err != nil {
				searched <- err
				return
			}
			if len(hits) != items {
				searched <- fmt.Errorf("%d hits, want %d", len(hits), items)
				return
			}
			for _, h := range hits {
				if want := fmt.Sprintf("issues/%d.md", h.Number); h.File != want {
					searched <- fmt.Errorf("the hit of %s carries the row of %s", h.File, want)
					return
				}
			}
		}
	}()

	for round := 1; round <= rounds; round++ {
		write(round)
		reopen(t, dir, items, 0)
	}
	close(stop)
	if err := <-searched; err != nil {
		t.Errorf("Search beside the rewrites: %v", err)
	}
}

func TestSearch_AClosedIndexIsError(t *testing.T) {
	dir := newMirror(t)
	threeFiles(t, dir)
	ix, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := ix.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if hits, err := ix.Search(Query{Text: "pin"}); err == nil || !strings.HasPrefix(err.Error(), "searching the index: ") {
		t.Errorf("Search = %v, %v, want an error that starts with %q", hits, err, "searching the index: ")
	}
	if hit, err := ix.Block("issues/7.md:1"); err == nil || !strings.HasPrefix(err.Error(), "reading the index: ") || errors.Is(err, ErrNoBlock) {
		t.Errorf("Block = %v, %v, want an error that starts with %q", hit, err, "reading the index: ")
	}
}

func TestSearch_Filters(t *testing.T) {
	open := testIssue(1, "An open issue", "The cursor.")
	open.Labels = []string{"Bug", "docker"}
	closed := testIssue(2, "A closed issue", "The cursor.")
	closed.State, closed.Labels = "closed", []string{"bug"}
	merged := testPull(3, "A merged pull request", "The cursor.")
	dir := newMirror(t)
	writeFiller(t, dir, 8)
	writeItem(t, dir, open)
	writeItem(t, dir, closed)
	writeItem(t, dir, merged)
	writeWiki(t, dir, "Cursor.md", "# A page\n\nThe cursor.\n")
	ix := openIndex(t, dir)

	const issue1, issue2, pull3, page = "issues/1.md", "issues/2.md", "pulls/3.md", "wiki/Cursor.md"
	for _, tc := range []struct {
		name string
		q    Query
		want []string
	}{
		{"no filter", Query{}, []string{issue1, issue2, pull3, page}},
		{"one type", Query{Types: []string{"issue"}}, []string{issue1, issue2}},
		{"two types", Query{Types: []string{"pull", "wiki"}}, []string{pull3, page}},
		{"an unknown type", Query{Types: []string{"discussion"}}, []string{}},
		{"a state", Query{State: "closed"}, []string{issue2}},
		{"the state of a pull request", Query{State: "merged"}, []string{pull3}},
		{"one label", Query{Labels: []string{"bug"}}, []string{issue1, issue2}},
		{"two labels an item carries", Query{Labels: []string{"bug", "docker"}}, []string{issue1}},
		{"two labels of which every item lacks one", Query{Labels: []string{"bug", "wontfix"}}, []string{}},
		{"a label in another case", Query{Labels: []string{"BUG"}}, []string{issue1, issue2}},
		{"a part of a label", Query{Labels: []string{"dock"}}, []string{}},
		{"a state and a type", Query{State: "open", Types: []string{"issue"}}, []string{issue1}},
		// A wiki page has no state and no label.
		{"a wiki page under a state", Query{State: "open", Types: []string{"wiki"}}, []string{}},
		{"a wiki page under a label", Query{Labels: []string{"bug"}, Types: []string{"wiki"}}, []string{}},
		{"every state leaves the wiki out", Query{State: "open"}, []string{issue1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.q.Text = "cursor"
			got := searchFiles(t, ix, tc.q)
			slices.Sort(got)
			if !slices.Equal(got, tc.want) {
				t.Errorf("Search(%+v) found %v, want %v", tc.q, got, tc.want)
			}
		})
	}
}

func TestSearch_FoldsCaseAndDiacriticsAndMatchesPrefixesAndPhrases(t *testing.T) {
	dir := newMirror(t)
	writeFiller(t, dir, 8)
	writeItem(t, dir, testIssue(1, "Deutsch", "Die Änderungen am Abgleich."))
	writeItem(t, dir, testIssue(2, "English", "There is one shared cursor."))
	ix := openIndex(t, dir)

	for _, tc := range []struct {
		text string
		want []string
	}{
		{"anderungen", []string{"issues/1.md"}},
		{"ÄNDERUNGEN", []string{"issues/1.md"}},
		{"curs*", []string{"issues/2.md"}},
		// Without the star a word is matched whole.
		{"curs", []string{}},
		{`"one cursor"`, []string{}},
		{`"shared cursor"`, []string{"issues/2.md"}},
		{"one cursor", []string{"issues/2.md"}},
	} {
		if got := searchFiles(t, ix, Query{Text: tc.text}); !slices.Equal(got, tc.want) {
			t.Errorf("search for %s found %v, want %v", tc.text, got, tc.want)
		}
	}
}

func TestSearch_AHitCarriesItsFileAndAnExcerptOnOneLine(t *testing.T) {
	it := testIssue(7, "Pin the base image", "The first line.\nThe cursor is on\tthe second line.\n\nThe last line.")
	it.Labels = []string{"Bug", "docker"}
	dir := newMirror(t)
	writeFiller(t, dir, 4)
	writeItem(t, dir, it)
	ix := openIndex(t, dir)

	hits, err := ix.Search(Query{Text: "cursor"})
	if err != nil || len(hits) != 1 {
		t.Fatalf("Search = %v, %v, want one hit", hits, err)
	}
	got := hits[0]
	if got.Score <= 0 {
		t.Errorf("score = %v, want it above 0", got.Score)
	}
	if want := "The first line. The cursor is on the second line. The last line."; got.Excerpt != want {
		t.Errorf("excerpt = %q, want %q", got.Excerpt, want)
	}
	got.Score, got.Excerpt = 0, ""
	want := Hit{
		ID: "issues/7.md:2", File: "issues/7.md", Type: "issue", Number: 7, Title: "Pin the base image", State: "open",
		Labels: []string{"bug", "docker"}, URL: it.URL, UpdatedAt: it.UpdatedAt,
		Block: Block{Ordinal: 2, Count: 2, Kind: "body", Author: "alice", Association: "MEMBER", URL: it.URL, CreatedAt: it.CreatedAt},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("hit =\n%+v\nwant\n%+v", got, want)
	}
}

func TestBlock(t *testing.T) {
	it := testIssue(7, "Pin the base image", "The tag moved twice.\n\nPin it to a digest.")
	it.Labels = []string{"Bug"}
	dir := newMirror(t)
	writeItem(t, dir, it)
	ix := openIndex(t, dir)

	got, err := ix.Block("issues/7.md:2")
	if err != nil {
		t.Fatalf("Block: %v", err)
	}
	want := Hit{
		ID: "issues/7.md:2", File: "issues/7.md", Type: "issue", Number: 7, Title: "Pin the base image", State: "open",
		Labels: []string{"bug"}, URL: it.URL, UpdatedAt: it.UpdatedAt,
		Block: Block{Ordinal: 2, Count: 2, Kind: "body", Author: "alice", Association: "MEMBER", URL: it.URL, CreatedAt: it.CreatedAt},
		Text:  "The tag moved twice.\n\nPin it to a digest.",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Block =\n%+v\nwant\n%+v", got, want)
	}

	// Neither error holds the id: it is an argument of the command.
	for _, id := range []string{"issues/7.md", "issues/7.md:0", "issues/7.md:x", "issues/7.md:-1", "issues/7.md:", ""} {
		const wantErr = "invalid block id: want <file>:<number>, as a hit prints it"
		if hit, err := ix.Block(id); err == nil || err.Error() != wantErr {
			t.Errorf("Block(%q) = %+v, %v, want the error %q", id, hit, err, wantErr)
		}
	}
	for _, id := range []string{"issues/7.md:99", "issues/7.md:3", "issues/404.md:1"} {
		hit, err := ix.Block(id)
		if !errors.Is(err, ErrNoBlock) || err.Error() != "no such block in the mirror" {
			t.Errorf("Block(%q) = %+v, %v, want ErrNoBlock as it is", id, hit, err)
		}
	}
}

// FuzzMatchExpr hands the expression matchExpr builds from any text to the
// engine: no text may be a syntax error there.
func FuzzMatchExpr(f *testing.F) {
	for _, seed := range []string{
		"one cursor", `"items cursor"`, "curs*", `"items cursor`, `say"hi`, "c++ a?b", "cursor AND NOT (wiki)",
		"NEAR(a b, 2)", "text:x ^y", "curs**", `""*`, "a\x00b", "{a b}: c", "-a +b", "",
	} {
		f.Add(seed)
	}
	// A mirror directory without a file: the engine parses the expression, and
	// no row matches.
	ix, err := Open(f.TempDir())
	if err != nil {
		f.Fatal(err)
	}
	f.Cleanup(func() { _ = ix.Close() })

	f.Fuzz(func(t *testing.T, text string) {
		expr, err := matchExpr(text)
		if errors.Is(err, ErrNoTerms) {
			if expr != "" {
				t.Fatalf("matchExpr(%q) = %q with ErrNoTerms", text, expr)
			}
			return
		}
		if err != nil {
			t.Fatalf("matchExpr(%q): %v", text, err)
		}
		if _, err := ix.Search(Query{Text: text}); err != nil {
			t.Fatalf("Search(%q) with the expression %q: %v", text, expr, err)
		}
	})
}
