package hygiene

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/planwerk/planwerk-agent/internal/report"
)

// anchorFile is the file the TestAnchorFindings cases quote. Line numbers are
// given on the right.
var anchorFile = strings.Join([]string{
	"package main",                      // 1
	"",                                  // 2
	"\tcfg := loadConfigFromDisk(path)", // 3
	"\tapplyDefaultSettings(cfg)",       // 4
	"",                                  // 5
	"func start() error {",              // 6
	"\treturn nil",                      // 7
	"}",                                 // 8
	"\tif err := runServer(cfg); err != nil {", // 9
	"\t\tfmt.Println(err)",                     // 10
	"\t}",                                      // 11
	"",                                         // 12
	"func stop() error {",                      // 13
	"\treturn nil",                             // 14
	"}",                                        // 15
	"",                                         // 16
	"func cleanup() {",                         // 17
	"",                                         // 18
	"\tremoveTemporaryFiles(dir)",              // 19
	"\tcfg := loadConfigFromDisk(path)",        // 20
	"\tapplyDefaultSettings(cfg)",              // 21
	"}",                                        // 22
}, "\n") + "\n"

// anchorOne anchors a single finding against dir and returns it with the
// recorded stats.
func anchorOne(t *testing.T, dir string, changed []string, f report.Finding) (report.Finding, *report.AnchorGateStats) {
	t.Helper()
	result := &report.ReviewResult{Findings: []report.Finding{f}}
	stats := AnchorFindings(result, dir, changed)
	return result.Findings[0], stats
}

func TestAnchorFindings(t *testing.T) {
	dir := t.TempDir()
	writeChangedFile(t, dir, "main.go", anchorFile)
	writeChangedFile(t, dir, "weak.go", "func f() error {\n\treturn nil\n}\n")
	// The err-check block twice (lines 3-5 and 9-11), with line 6 the variant
	// a model quotes in reconstructed form.
	writeChangedFile(t, dir, "errs.go", strings.Join([]string{
		"func load() error {",               // 1
		"\terr := open()",                   // 2
		"\tif err != nil {",                 // 3
		"\t\treturn err",                    // 4
		"\t}",                               // 5
		"\tif err := parse(); err != nil {", // 6
		"\t\treturn err",                    // 7
		"\t}",                               // 8
		"\tif err != nil {",                 // 9
		"\t\treturn err",                    // 10
		"\t}",                               // 11
		"}",                                 // 12
	}, "\n")+"\n")

	const (
		serverQuote    = "if err := runServer(cfg); err != nil {\n\tfmt.Println(err)"
		duplicateQuote = "cfg := loadConfigFromDisk(path)\napplyDefaultSettings(cfg)"
		// Every line is boilerplate under minDistinctiveLineLen, though the
		// three together run past it.
		errCheckQuote = "if err != nil {\n\treturn err\n}"
	)
	cases := []struct {
		name          string
		file          string
		line, lineEnd int
		snippet       string
		wantCheck     string
		wantLine      int
		wantLineEnd   int
	}{
		{"model line inside the match is kept", "main.go", 10, 0, serverQuote, report.LineCheckAnchored, 10, 0},
		{"model range equal to the match is kept", "main.go", 9, 10, serverQuote, report.LineCheckAnchored, 9, 10},
		{"single match moves a miscounted line", "main.go", 2, 0, serverQuote, report.LineCheckReanchored, 9, 10},
		{"single match moves a line given none", "main.go", 0, 0, serverQuote, report.LineCheckReanchored, 9, 10},
		{"single match narrows a range reaching past it", "main.go", 9, 12, serverQuote, report.LineCheckReanchored, 9, 10},
		{"several matches move to the nearest after", "main.go", 18, 0, duplicateQuote, report.LineCheckReanchored, 20, 21},
		{"several matches move to the nearest before", "main.go", 6, 0, duplicateQuote, report.LineCheckReanchored, 3, 4},
		{"several matches and no model line stay unanchored", "main.go", 0, 0, duplicateQuote, report.LineCheckUnanchored, 0, 0},
		{"a weak quote in several places confirms a line inside one", "main.go", 14, 0, "return nil", report.LineCheckAnchored, 14, 0},
		{"a weak quote in several places moves nothing", "main.go", 10, 0, "return nil", report.LineCheckUnanchored, 10, 0},
		{"a weak quote unique in its file moves", "weak.go", 9, 0, "return nil", report.LineCheckReanchored, 2, 0},
		{"a boilerplate block in several places moves nothing", "errs.go", 6, 0, errCheckQuote, report.LineCheckUnanchored, 6, 0},
		{"a boilerplate block confirms a line inside one", "errs.go", 10, 0, errCheckQuote, report.LineCheckAnchored, 10, 0},
		{"a single-line match leaves LineEnd 0", "main.go", 3, 5, "removeTemporaryFiles(dir)", report.LineCheckReanchored, 19, 0},
		{"a blank file line inside the match falls in the span", "main.go", 1, 0, "func cleanup() {\n\tremoveTemporaryFiles(dir)", report.LineCheckReanchored, 17, 19},
		{"a blank quote line is skipped", "main.go", 1, 0, "func cleanup() {\n\n\n\tremoveTemporaryFiles(dir)", report.LineCheckReanchored, 17, 19},
		{"a quoted comment marker is ignored", "main.go", 0, 0, "// removeTemporaryFiles(dir)", report.LineCheckReanchored, 19, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := anchorOne(t, dir, []string{"main.go", "weak.go"}, report.Finding{
				Title: tc.name, File: tc.file, Line: tc.line, LineEnd: tc.lineEnd, CodeSnippet: tc.snippet,
			})
			if got.LineCheck != tc.wantCheck {
				t.Errorf("line check = %q, want %q", got.LineCheck, tc.wantCheck)
			}
			if got.File != tc.file || got.Line != tc.wantLine || got.LineEnd != tc.wantLineEnd {
				t.Errorf("place = %s:%d-%d, want %s:%d-%d", got.File, got.Line, got.LineEnd, tc.file, tc.wantLine, tc.wantLineEnd)
			}
		})
	}
}

// TestAnchorFindings_DiffMarkersAndListItems locks the two forms of a quote
// line: with one leading '+' stripped (a line copied out of git diff) and as
// written (a YAML or Markdown list item whose own content starts with '-'). A
// leading '-' is never stripped, so a deleted list item stays off the copy of
// it that HEAD holds.
func TestAnchorFindings_DiffMarkersAndListItems(t *testing.T) {
	dir := t.TempDir()
	writeChangedFile(t, dir, "serve.go", "func serve() {\n\trunServer(cfg)\n}\n")
	writeChangedFile(t, dir, "ci.yml", "- name: build\n  run: make\n- name: test\n  run: make test\n")

	cases := []struct {
		name      string
		file      string
		line      int
		snippet   string
		wantCheck string
		wantLine  int
	}{
		{"added line keeps the model line on it", "serve.go", 2, "+\trunServer(cfg)", report.LineCheckAnchored, 2},
		{"added line moves a line given none", "serve.go", 0, "+\trunServer(cfg)", report.LineCheckReanchored, 2},
		// The '-' is the item's own content: only the raw form matches.
		{"list item quoted as written", "ci.yml", 0, "- name: build", report.LineCheckReanchored, 1},
		// One '+' is the diff column: only the bare form matches.
		{"list item quoted from the diff", "ci.yml", 0, "+- name: build", report.LineCheckReanchored, 1},
		// The first '-' marks the item deleted: HEAD's copy does not claim it.
		{"deleted list item quoted from the diff", "ci.yml", 0, "-- name: build", report.LineCheckUnanchored, 0},
		{"list item kept on the model line", "ci.yml", 3, "- name: test", report.LineCheckAnchored, 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := anchorOne(t, dir, nil, report.Finding{Title: tc.name, File: tc.file, Line: tc.line, CodeSnippet: tc.snippet})
			if got.LineCheck != tc.wantCheck || got.Line != tc.wantLine {
				t.Errorf("got %q at line %d, want %q at line %d", got.LineCheck, got.Line, tc.wantCheck, tc.wantLine)
			}
		})
	}
}

// TestAnchorFindings_NewSideOnly proves the anchor searches only the head
// content: a quote of a deleted line anchors nothing, also where a copy of the
// deleted code survives at HEAD, in its own file or another changed file, and
// a quoted replacement anchors on its added line.
func TestAnchorFindings_NewSideOnly(t *testing.T) {
	dir := t.TempDir()
	writeChangedFile(t, dir, "auth.go", "func check(r *http.Request) bool {\n\tok := verifySessionToken(r)\n\treturn ok\n}\n")
	// The change deleted release's `defer mu.Unlock()`; acquire keeps its own.
	writeChangedFile(t, dir, "lock.go", strings.Join([]string{
		"func acquire() {",    // 1
		"\tmu.Lock()",         // 2
		"\tdefer mu.Unlock()", // 3
		"}",                   // 4
		"func release() {",    // 5
		"\tmu.Lock()",         // 6
		"}",                   // 7
	}, "\n")+"\n")
	changed := []string{"auth.go", "lock.go"}

	cases := []struct {
		name, file, quote string
		line              int
	}{
		{"a deleted line HEAD lacks", "auth.go", "-\tlegacyAuthCheck(request)", 3},
		{"a deleted line that survives in its own file", "lock.go", "-\tdefer mu.Unlock()", 7},
		{"a deleted line that survives in another changed file", "auth.go", "-\tdefer mu.Unlock()", 3},
		{"a deleted line quoted with its context", "lock.go", "\tmu.Lock()\n-\tdefer mu.Unlock()", 7},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := anchorOne(t, dir, changed, report.Finding{Title: tc.name, File: tc.file, Line: tc.line, CodeSnippet: tc.quote})
			if got.LineCheck != report.LineCheckUnanchored || got.File != tc.file || got.Line != tc.line || got.LineEnd != 0 {
				t.Errorf("got %q at %s:%d-%d, want unanchored at the model's %s:%d", got.LineCheck, got.File, got.Line, got.LineEnd, tc.file, tc.line)
			}
		})
	}

	replaced, _ := anchorOne(t, dir, changed, report.Finding{
		Title: "replaced", File: "auth.go", Line: 7,
		CodeSnippet: "-\told := legacyAuthCheck(r)\n+\tok := verifySessionToken(r)",
	})
	if replaced.LineCheck != report.LineCheckReanchored || replaced.Line != 2 || replaced.LineEnd != 0 {
		t.Errorf("replacement quote: got %q at %d-%d, want reanchored on the added line 2", replaced.LineCheck, replaced.Line, replaced.LineEnd)
	}
}

// TestAnchorFindings_ElidedQuote covers a quote that is not one contiguous
// block of whole lines: the model's line is kept when it holds one of the
// quote's distinctive lines, whole or within a longer line, also beside a
// whole-line copy elsewhere in the file, and stamped unanchored when it does
// not.
func TestAnchorFindings_ElidedQuote(t *testing.T) {
	dir := t.TempDir()
	writeChangedFile(t, dir, "handler.go", strings.Join([]string{
		"func handle(w http.ResponseWriter, r *http.Request) {", // 1
		"\ttoken := r.Header.Get(\"Authorization\")",            // 2
		"\tif token == \"\" {",                                  // 3
		"\t\treturn",                                            // 4
		"\t}",                                                   // 5
		"\tok := verifySessionToken(token)",                     // 6
		"\tdb.Exec(\"SELECT * FROM t WHERE id=\" + token) // TODO: bind", // 7
		"}",                                      // 8
		"func closeNow(resp *http.Response) {",   // 9
		"\tdefer resp.Body.Close()",              // 10
		"}",                                      // 11
		"func closeLater(resp *http.Response) {", // 12
		"\tdefer resp.Body.Close() // TODO: check", // 13
		"}", // 14
	}, "\n")+"\n")
	const (
		elided   = "token := r.Header.Get(\"Authorization\")\n...\nok := verifySessionToken(token)"
		trimmed  = "db.Exec(\"SELECT * FROM t WHERE id=\" + token)"
		fragment = "\"SELECT * FROM t WHERE id=\" + token"
		closer   = "defer resp.Body.Close()"
	)

	cases := []struct {
		name          string
		quote         string
		line, lineEnd int
		wantCheck     string
	}{
		{"model line on a distinctive quote line", elided, 6, 0, report.LineCheckAnchored},
		{"model range holding a distinctive quote line", elided, 3, 7, report.LineCheckAnchored},
		{"model line on no quote line", elided, 4, 0, report.LineCheckUnanchored},
		{"no model line", elided, 0, 0, report.LineCheckUnanchored},
		{"a quote that left out the trailing comment", trimmed, 7, 0, report.LineCheckAnchored},
		{"a quote of one expression of the line", fragment, 7, 0, report.LineCheckAnchored},
		{"a trimmed quote off the model line", trimmed, 6, 0, report.LineCheckUnanchored},
		// Line 10 holds the quote as a whole line, line 13 within a longer one:
		// the model's line holds the evidence, so the copy does not claim it.
		{"a model line holding the quote beside a whole-line copy", closer, 13, 0, report.LineCheckAnchored},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := anchorOne(t, dir, []string{"handler.go"}, report.Finding{
				Title: tc.name, File: "handler.go", Line: tc.line, LineEnd: tc.lineEnd, CodeSnippet: tc.quote,
			})
			if got.LineCheck != tc.wantCheck {
				t.Errorf("line check = %q, want %q", got.LineCheck, tc.wantCheck)
			}
			if got.Line != tc.line || got.LineEnd != tc.lineEnd {
				t.Errorf("place = %d-%d, want the model's %d-%d kept", got.Line, got.LineEnd, tc.line, tc.lineEnd)
			}
		})
	}
}

// TestAnchorFindings_CrossFile covers relocation: a quote missing from its own
// file moves the finding to the one other changed file that holds it, and to
// no file when the place is ambiguous, the quote is short, the own file holds
// the quote trimmed or elided, or the path would leave the checkout.
func TestAnchorFindings_CrossFile(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "repo")
	const quote = "total := computeExpensiveTotal(items)"
	writeChangedFile(t, dir, "a.go", "package a\n\nfunc A() {}\n")
	writeChangedFile(t, dir, "b.go", "package b\n\nfunc B(items []int) int {\n\treturn nil\n\ttotal := computeExpensiveTotal(items)\n}\n")
	writeChangedFile(t, dir, "c.go", "package c\n\ttotal := computeExpensiveTotal(items)\n")
	// handler.go line 3 is the defect, quoted as the boilerplate block that
	// store.go holds exactly once.
	writeChangedFile(t, dir, "handler.go", "package h\nfunc h() error {\n\tif err := load(); err != nil {\n\t\treturn err\n\t}\n\treturn nil\n}\n")
	writeChangedFile(t, dir, "store.go", "package s\nfunc s() error {\n\terr := save()\n\tif err != nil {\n\t\treturn err\n\t}\n\treturn nil\n}\n")
	// trimmed.go and elided.go hold the quote, within a longer line or with a
	// comment between its lines; closer.go and timer.go hold it whole.
	writeChangedFile(t, dir, "trimmed.go", "package t\n\nfunc f() {\n\tdefer resp.Body.Close() // nolint:errcheck\n}\n")
	writeChangedFile(t, dir, "closer.go", "package c\n\nfunc g() {\n\tdefer resp.Body.Close()\n}\n")
	writeChangedFile(t, dir, "elided.go", "package e\n\nfunc f() {\n\tctx, cancel := context.WithTimeout(ctx, timeout)\n\t// release the timer\n\tdefer cancel()\n}\n")
	writeChangedFile(t, dir, "timer.go", "package t\n\nfunc g() {\n\tctx, cancel := context.WithTimeout(ctx, timeout)\n\tdefer cancel()\n}\n")
	// A file outside the checkout that holds the quote: it must never be read.
	writeChangedFile(t, base, "outside.go", quote+"\n")

	cases := []struct {
		name      string
		file      string
		snippet   string
		changed   []string
		wantCheck string
		wantFile  string
		wantLine  int
	}{
		{"one place in another changed file", "a.go", quote, []string{"a.go", "b.go"}, report.LineCheckRelocated, "b.go", 5},
		{"places in two other changed files", "a.go", quote, []string{"a.go", "b.go", "c.go"}, report.LineCheckUnanchored, "a.go", 3},
		{"a file outside the changed files is not searched", "a.go", quote, []string{"a.go"}, report.LineCheckUnanchored, "a.go", 3},
		{"relocation off without changed files", "a.go", quote, nil, report.LineCheckUnanchored, "a.go", 3},
		{"a weak quote never relocates", "a.go", "return nil", []string{"a.go", "b.go"}, report.LineCheckUnanchored, "a.go", 3},
		{"a boilerplate block never relocates", "handler.go", "if err != nil {\n\treturn err\n}", []string{"handler.go", "store.go"}, report.LineCheckUnanchored, "handler.go", 3},
		{"a path leaving the checkout is not read", "../outside.go", quote, nil, report.LineCheckUnanchored, "../outside.go", 3},
		{"a path leaving the checkout falls through to relocation", "../outside.go", quote, []string{"b.go"}, report.LineCheckRelocated, "b.go", 5},
		{"the own file listed as ./a.go does not count", "a.go", quote, []string{"./a.go", "b.go"}, report.LineCheckRelocated, "b.go", 5},
		{"the own file named ./a.go keeps its spelling", "./a.go", quote, []string{"a.go"}, report.LineCheckUnanchored, "./a.go", 3},
		{"an own file holding the quote within a longer line keeps it", "trimmed.go", "defer resp.Body.Close()", []string{"trimmed.go", "closer.go"}, report.LineCheckUnanchored, "trimmed.go", 3},
		{"an own file holding the quote apart keeps it", "elided.go", "ctx, cancel := context.WithTimeout(ctx, timeout)\ndefer cancel()", []string{"elided.go", "timer.go"}, report.LineCheckUnanchored, "elided.go", 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := anchorOne(t, dir, tc.changed, report.Finding{Title: tc.name, File: tc.file, Line: 3, CodeSnippet: tc.snippet})
			if got.LineCheck != tc.wantCheck {
				t.Errorf("line check = %q, want %q", got.LineCheck, tc.wantCheck)
			}
			if got.File != tc.wantFile || got.Line != tc.wantLine {
				t.Errorf("place = %s:%d, want %s:%d", got.File, got.Line, tc.wantFile, tc.wantLine)
			}
		})
	}
}

// TestAnchorFindings_UnreadableFiles proves the anchor reads only regular text
// files: a binary file, a directory and a symlink are treated as unreadable, so
// the quote they hold anchors nothing.
func TestAnchorFindings_UnreadableFiles(t *testing.T) {
	dir := t.TempDir()
	const quote = "total := computeExpensiveTotal(items)"
	writeChangedFile(t, dir, "blob.bin", "\x00\x01"+"\n"+quote+"\n")
	writeChangedFile(t, dir, "real.go", "package real\n"+quote+"\n")
	writeChangedFile(t, dir, "pkg/doc.go", "package pkg\n")
	if err := os.Symlink(filepath.Join(dir, "real.go"), filepath.Join(dir, "link.go")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	for _, file := range []string{"blob.bin", "pkg", "link.go"} {
		t.Run(file, func(t *testing.T) {
			got, _ := anchorOne(t, dir, nil, report.Finding{Title: file, File: file, CodeSnippet: quote})
			if got.LineCheck != report.LineCheckUnanchored || got.Line != 0 {
				t.Errorf("got %q at line %d, want unanchored with no line", got.LineCheck, got.Line)
			}
		})
	}
}

// TestAnchorFindings_ReadLimits proves the read limits never cost a finding
// its own file and never make a place look unique: relocating one finding
// spends the read budget only after every finding was searched in its own
// file, and no finding is relocated while a file it would be searched in, its
// own or another changed file, was left unread.
func TestAnchorFindings_ReadLimits(t *testing.T) {
	dir := t.TempDir()
	const quote = "total := computeExpensiveTotal(items)"
	// Changed files that together fill the read budget exactly, so any file
	// read after them is skipped.
	filler := strings.Repeat(strings.Repeat("x", 63)+"\n", maxHaystackFileBytes/64)
	var budget []string
	for i := range maxCheckoutHaystackBytes / maxHaystackFileBytes {
		name := fmt.Sprintf("gen/%d.txt", i)
		writeChangedFile(t, dir, name, filler)
		budget = append(budget, name)
	}
	writeChangedFile(t, dir, "own.go", "package own\n\nfunc f() {\n\t"+quote+"\n\tpublishTheTotalNow(total)\n}\n")
	writeChangedFile(t, dir, "huge.go", quote+"\n"+filler)
	writeChangedFile(t, dir, "copy.go", "package copy\n"+quote+"\n")
	writeChangedFile(t, dir, "copy2.go", "package copy\n"+quote+"\n")
	writeChangedFile(t, dir, "none.go", "package none\n")

	t.Run("relocation spends the budget after the own files", func(t *testing.T) {
		result := &report.ReviewResult{Findings: []report.Finding{
			{Title: "deleted code", File: "gone.go", Line: 3, CodeSnippet: "legacyHandlerThatWasDeleted(request)"},
			{Title: "own file", File: "own.go", Line: 1, CodeSnippet: quote + "\npublishTheTotalNow(total)"},
		}}
		AnchorFindings(result, dir, append(budget, "own.go"))
		if got := result.Findings[1]; got.LineCheck != report.LineCheckReanchored || got.Line != 4 || got.LineEnd != 5 {
			t.Errorf("own-file finding: got %q at %d-%d, want reanchored at 4-5", got.LineCheck, got.Line, got.LineEnd)
		}
		if got := result.Findings[0]; got.LineCheck != report.LineCheckUnanchored || got.File != "gone.go" {
			t.Errorf("deleted-code finding: got %q in %s, want unanchored in gone.go", got.LineCheck, got.File)
		}
	})
	t.Run("an own file left unread is not relocated from", func(t *testing.T) {
		got, _ := anchorOne(t, dir, []string{"huge.go", "copy.go"}, report.Finding{Title: "huge", File: "huge.go", Line: 1, CodeSnippet: quote})
		if got.LineCheck != report.LineCheckUnanchored || got.File != "huge.go" || got.Line != 1 {
			t.Errorf("got %q at %s:%d, want unanchored at the model's huge.go:1", got.LineCheck, got.File, got.Line)
		}
	})
	// A second copy may sit in a changed file the anchor left unread, so one
	// copy among the files it read is not one place in the change.
	t.Run("a changed file left unread for its size blocks relocation", func(t *testing.T) {
		got, _ := anchorOne(t, dir, []string{"none.go", "huge.go", "copy.go"}, report.Finding{Title: "size", File: "none.go", Line: 1, CodeSnippet: quote})
		if got.LineCheck != report.LineCheckUnanchored || got.File != "none.go" || got.Line != 1 {
			t.Errorf("got %q at %s:%d, want unanchored at the model's none.go:1", got.LineCheck, got.File, got.Line)
		}
	})
	t.Run("a changed file past the read budget blocks relocation", func(t *testing.T) {
		changed := append(append([]string{"none.go", "copy.go"}, budget...), "copy2.go")
		got, _ := anchorOne(t, dir, changed, report.Finding{Title: "budget", File: "none.go", Line: 1, CodeSnippet: quote})
		if got.LineCheck != report.LineCheckUnanchored || got.File != "none.go" || got.Line != 1 {
			t.Errorf("got %q at %s:%d, want unanchored at the model's none.go:1", got.LineCheck, got.File, got.Line)
		}
	})
}

// TestAnchorFindings_NotExamined proves a finding with no file or no quote is
// neither stamped nor counted, and that a line_check the model emitted is
// cleared.
func TestAnchorFindings_NotExamined(t *testing.T) {
	dir := t.TempDir()
	writeChangedFile(t, dir, "main.go", anchorFile)
	result := &report.ReviewResult{Findings: []report.Finding{
		{Title: "file-less", CodeSnippet: "removeTemporaryFiles(dir)", LineCheck: report.LineCheckRelocated},
		{Title: "no quote", File: "main.go", Line: 19, LineCheck: report.LineCheckAnchored},
		{Title: "blank quote", File: "main.go", Line: 19, CodeSnippet: "  \n\t\n"},
		{Title: "markers only", File: "main.go", Line: 19, CodeSnippet: "+\n-\n//"},
	}}

	stats := AnchorFindings(result, dir, []string{"main.go"})
	for _, f := range result.Findings {
		if f.LineCheck != "" {
			t.Errorf("%s: line check = %q, want none", f.Title, f.LineCheck)
		}
	}
	if stats == nil || *stats != (report.AnchorGateStats{}) {
		t.Errorf("stats = %+v, want all zero", stats)
	}
}

// TestAnchorFindings_RecordsStatsAcrossPasses proves the counts sum every
// pass's findings and land on the primary result only, and that the anchor
// records nothing when it cannot run.
func TestAnchorFindings_RecordsStatsAcrossPasses(t *testing.T) {
	dir := t.TempDir()
	writeChangedFile(t, dir, "main.go", anchorFile)
	const quote = "removeTemporaryFiles(dir)"
	primary := &report.ReviewResult{Findings: []report.Finding{
		{Title: "moved", File: "main.go", Line: 2, CodeSnippet: quote},
	}}
	adversarial := &report.ReviewResult{Findings: []report.Finding{
		{Title: "kept", File: "main.go", Line: 19, CodeSnippet: quote},
		{Title: "nowhere", File: "main.go", Line: 4, CodeSnippet: "deleteEverything(now)"},
		{Title: "unexamined", File: "main.go", Line: 4},
	}}

	stats := AnchorFindings(primary, dir, []string{"main.go"}, adversarial, nil)
	want := report.AnchorGateStats{Examined: 3, Reanchored: 1, Unanchored: 1}
	if stats == nil || *stats != want {
		t.Fatalf("stats = %+v, want %+v", stats, want)
	}
	if primary.Gates == nil || primary.Gates.Anchor != stats {
		t.Errorf("primary.Gates.Anchor = %+v, want the returned stats", primary.Gates)
	}
	if adversarial.Gates != nil {
		t.Errorf("a secondary must record nothing, got %+v", adversarial.Gates)
	}
	if got := adversarial.Findings[0].LineCheck; got != report.LineCheckAnchored {
		t.Errorf("secondary finding line check = %q, want anchored", got)
	}

	t.Run("nothing examined still records the stage", func(t *testing.T) {
		empty := &report.ReviewResult{}
		if got := AnchorFindings(empty, dir, nil); got == nil || empty.Gates == nil || empty.Gates.Anchor != got || *got != (report.AnchorGateStats{}) {
			t.Errorf("stats = %+v, gates = %+v, want zero counts recorded", got, empty.Gates)
		}
	})
	t.Run("no checkout", func(t *testing.T) {
		res := &report.ReviewResult{Findings: []report.Finding{{File: "main.go", Line: 2, CodeSnippet: quote}}}
		if got := AnchorFindings(res, "", nil); got != nil {
			t.Errorf("stats = %+v, want nil without a checkout", got)
		}
		if res.Gates != nil || res.Findings[0].LineCheck != "" || res.Findings[0].Line != 2 {
			t.Errorf("result changed without a checkout: %+v", res)
		}
	})
	t.Run("nil primary", func(t *testing.T) {
		sec := &report.ReviewResult{Findings: []report.Finding{{File: "main.go", Line: 2, CodeSnippet: quote}}}
		if got := AnchorFindings(nil, dir, nil, sec); got != nil {
			t.Errorf("stats = %+v, want nil for a nil primary", got)
		}
		if sec.Findings[0].LineCheck != "" || sec.Findings[0].Line != 2 {
			t.Errorf("secondary changed with a nil primary: %+v", sec.Findings[0])
		}
	})
}
