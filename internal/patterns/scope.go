package patterns

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// FileScope is a loaded catalog split by the changed files of one review
// (ScopeToFiles). Inline holds the patterns whose bodies the review prompt
// carries and Rest the ones it only lists as an index, each in catalog order.
// Files maps the name of each Inline pattern a changed file matched to the
// sorted files it matched; a pattern that applies to every file has no entry
// (decision 141).
type FileScope struct {
	Inline []Pattern
	Files  map[string][]string
	Rest   []Pattern
}

// ScopeToFiles splits pats by the changed files: Inline gets every pattern
// that applies to every file (AppliesToEveryFile) and every pattern one of
// files matches (MatchesFile, with that file's entry of tags), Rest gets the
// others, and Files records what each matched pattern matched. tags maps a
// file to its technology tags (detect.FileTechnologies). With no files the
// split is unknown, and every pattern goes to Inline with no Files and no Rest
// (decision 141).
func ScopeToFiles(pats []Pattern, files []string, tags map[string][]string) FileScope {
	if len(files) == 0 {
		return FileScope{Inline: pats}
	}
	var s FileScope
	for _, p := range pats {
		if p.AppliesToEveryFile() {
			s.Inline = append(s.Inline, p)
			continue
		}
		var matched []string
		for _, f := range files {
			if p.MatchesFile(f, tags[f]) {
				matched = append(matched, f)
			}
		}
		if len(matched) == 0 {
			s.Rest = append(s.Rest, p)
			continue
		}
		slices.Sort(matched)
		if s.Files == nil {
			s.Files = make(map[string][]string)
		}
		s.Files[p.Name] = slices.Compact(matched)
		s.Inline = append(s.Inline, p)
	}
	return s
}

// FormatScopedForPrompt renders s.Inline as FormatGroupedForPrompt renders a
// catalog, truncated to maxPatterns the same way, with one change inside each
// category block: the patterns without a Files entry come first, then one
// `<patterns for="…">` block per distinct set of matched files, in order of
// first appearance, naming those files (scopeForAttr). A scope without Files
// renders exactly what FormatGroupedForPrompt renders for s.Inline. fileBlocks
// reports whether the text holds a `<patterns for="…">` block, which it does
// not when the truncation keeps no pattern with a Files entry, so a caller
// introduces the blocks only when they are there. The framing follows
// alibaba/open-code-review@375f64c's `<rules for="…">` (Apache-2.0), which
// tags each rule set with the files it governs (decision 141).
func FormatScopedForPrompt(s FileScope, maxPatterns int) (text string, fileBlocks bool) {
	kept := truncatePatterns(s.Inline, maxPatterns)
	fileBlocks = slices.ContainsFunc(kept, func(p Pattern) bool { return len(s.Files[p.Name]) > 0 })
	return formatGrouped(kept, 0, s.Files), fileBlocks
}

// maxScopeFilesListed is the most changed files one `<patterns for="…">`
// attribute names; the rest are counted.
const maxScopeFilesListed = 20

// scopeForAttr renders the quoted value of a `<patterns for="…">` attribute:
// the first maxScopeFilesListed of files joined by ", ", then ", and N more
// changed files" for the rest, with "<" and ">" replaced by "&lt;" and "&gt;"
// and the whole quoted by strconv.Quote. The pull request's author chose the
// paths, so the quoting keeps one from closing the tag or starting a line of
// its own.
func scopeForAttr(files []string) string {
	listed := files
	if len(listed) > maxScopeFilesListed {
		listed = listed[:maxScopeFilesListed]
	}
	value := strings.Join(listed, ", ")
	switch more := len(files) - len(listed); {
	case more == 1:
		value += ", and 1 more changed file"
	case more > 1:
		value += fmt.Sprintf(", and %d more changed files", more)
	}
	return strconv.Quote(strings.NewReplacer("<", "&lt;", ">", "&gt;").Replace(value))
}
