// Package brain reads and builds a repository's project memory.
//
// `brain memory`, which the plugin skills call, reads it for a caller outside
// a run. It prints the memory index or one page, starts no Claude session, and
// writes no file. The wiki opt-in, the clone, and the page guards stay in the
// patterns package, so a skill reads the same pages through the same checks as
// a headless command. Its reader is a shell and a terminal, so on top of those
// checks it keeps only the pages whose file name is safe on a command line and
// drops control characters from what it prints.
//
// `brain bootstrap` builds the memory from a repository's history. It groups
// the closed issues, merged pull requests, commits, and decision documents
// into units, runs an analysis session and a review session per unit, and
// keeps the resulting pages in a state directory until the operator pushes
// them to the wiki.
package brain

import (
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"

	"github.com/planwerk/planwerk-agent/internal/github"
	"github.com/planwerk/planwerk-agent/internal/patterns"
)

// MemoryOptions configures one `brain memory` call.
type MemoryOptions struct {
	RepoRef string
	Page    string // file name of the page to print; empty prints the index
	Wiki    patterns.WikiOptions
	Remote  patterns.RemoteOptions
}

// resolveWikiFn resolves the target repo's wiki. It matches patterns.ResolveWiki.
type resolveWikiFn func(owner, name string, wopts patterns.WikiOptions, ropts patterns.RemoteOptions) patterns.ResolvedWiki

// Runner prints the project memory using an injected wiki resolver, so the
// output can be exercised without cloning a real wiki.
type Runner struct {
	ResolveWiki resolveWikiFn // nil means patterns.ResolveWiki
}

// Memory is a package-level convenience that delegates to (&Runner{}).Memory.
func Memory(w io.Writer, opts MemoryOptions) error {
	return (&Runner{}).Memory(w, opts)
}

// Memory writes the project memory of opts.RepoRef to w. Without opts.Page it
// writes the index: a header naming the wiki, its commit, and the number of
// pages, a blank line, and one line per page (patterns.FormatMemoryIndexLines).
// A wiki that is disabled, unresolved, or without memory pages writes nothing
// and returns nil. With opts.Page it writes the body of the page of that file
// name and a newline. The name is matched against the loaded pages and never
// joined into a path, so it cannot reach a file the page guards rejected; a
// name without a match is an error and writes nothing. Both forms see only the
// pages commandLinePages keeps, and what they write passes through printable.
func (r *Runner) Memory(w io.Writer, opts MemoryOptions) error {
	owner, name, err := github.ParseRepoRef(opts.RepoRef)
	if err != nil {
		return fmt.Errorf("parsing repo ref: %w", err)
	}

	resolveWiki := r.ResolveWiki
	if resolveWiki == nil {
		resolveWiki = patterns.ResolveWiki
	}
	wiki := resolveWiki(owner, name, opts.Wiki, opts.Remote)
	wiki.MemoryPages = commandLinePages(wiki.MemoryPages)

	var out string
	if opts.Page == "" {
		out = memoryIndex(wiki)
	} else {
		body, ok := memoryPageBody(wiki.MemoryPages, opts.Page)
		if !ok {
			return fmt.Errorf("no project memory page named %q for %s/%s", opts.Page, owner, name)
		}
		out = body + "\n"
	}
	if _, err := io.WriteString(w, printable(out)); err != nil {
		return fmt.Errorf("writing the project memory: %w", err)
	}
	return nil
}

// memoryIndex renders the index form of wiki's memory, "" when it has no pages.
func memoryIndex(wiki patterns.ResolvedWiki) string {
	if len(wiki.MemoryPages) == 0 {
		return ""
	}
	header := "Project memory from " + wiki.Repo + ".wiki"
	if wiki.CommitSHA != "" {
		header += " @ " + wiki.CommitSHA
	}
	return fmt.Sprintf("%s, pages: %d\n\n%s", header, len(wiki.MemoryPages), patterns.FormatMemoryIndexLines(wiki.MemoryPages))
}

// memoryPageBody returns the body of the page whose file name is name. A
// caller can send a listed name in the other Unicode normalization form: a
// session re-emits a decomposed name precomposed, and so does a person who
// types it. A name without a page of the same bytes is therefore compared in
// NFC, and found when exactly one page has it.
func memoryPageBody(pages []patterns.MemoryPage, name string) (string, bool) {
	for _, p := range pages {
		if p.Name == name {
			return p.Body, true
		}
	}
	want := norm.NFC.String(name)
	var body string
	matches := 0
	for _, p := range pages {
		if norm.NFC.String(p.Name) == want {
			body = p.Body
			matches++
		}
	}
	if matches != 1 {
		return "", false
	}
	return body, true
}

// commandLinePages returns the pages whose file name is safe on a command
// line. A skill passes the name an index line printed back as the argument of
// the page form, and git accepts "$", backticks, ";", quotes, spaces, and a
// leading "-" in a file name, so such a name would run in the caller's shell
// or be parsed as a flag. Every other page is skipped. One warning counts the
// skipped pages and names none of them: a skill session reads the log beside
// the index, and a file name is wiki content. The names are logged at debug
// level.
func commandLinePages(pages []patterns.MemoryPage) []patterns.MemoryPage {
	var kept []patterns.MemoryPage
	for _, p := range pages {
		if !commandLineSafe(p.Name) {
			slog.Debug("project memory page name is not safe on a command line; skipping", "page", strconv.Quote(p.Name))
			continue
		}
		kept = append(kept, p)
	}
	if skipped := len(pages) - len(kept); skipped > 0 {
		slog.Warn("skipped project memory pages whose file name is not safe on a command line (letters, digits, '.', '_', and '-' only, starting with a letter or a digit); rerun with --verbose for the names", "pages", skipped)
	}
	return kept
}

// commandLineSafe reports whether name starts with a letter or a digit and
// holds nothing but letters, their combining marks, digits, ".", "_", and
// "-". A script such as Devanagari writes its vowels as marks, and so does a
// name stored in decomposed form.
func commandLineSafe(name string) bool {
	for i, r := range name {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r):
		case i > 0 && (r == '.' || r == '_' || r == '-' || unicode.IsMark(r)):
		default:
			return false
		}
	}
	return name != ""
}

// printable drops every control character except newline and tab, so wiki
// content cannot drive the terminal it is printed to.
func printable(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' || !unicode.IsControl(r) {
			return r
		}
		return -1
	}, s)
}
