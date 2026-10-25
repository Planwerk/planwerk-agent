package review

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/planwerk/planwerk-agent/internal/diffscope"
	"github.com/planwerk/planwerk-agent/internal/github"
	"github.com/planwerk/planwerk-agent/internal/report"
)

// maxLedgerFiles is the most changed files the review prompt lists. Each
// listed line and its files_reviewed entry cost about 15-20 tokens, so the cap
// keeps the list and the answer under about 4k tokens each; a pull request
// with more listed files gets no list and no record (decision 138).
const maxLedgerFiles = 200

// planwerkDir holds the planning artifacts the review ignores, and
// ledgerKeptPlanwerk the paths under it that change how the repository is
// reviewed or worked on, which the review prompt calls findings in their own
// right: the checklist, its appended parts, and the review patterns.
const planwerkDir = ".planwerk/"

var ledgerKeptPlanwerk = []string{".planwerk/checklist.md", ".planwerk/checklist.d/", ".planwerk/review_patterns/"}

// reviewLedger returns the changed files the review prompt lists and the
// changed-file check counts, in git's order: files minus every path the run's
// diff left out (excluded, diffscope.IsExcluded) and minus the .planwerk/
// planning artifacts other than the checklist and the review patterns. Each
// Path and OldPath goes through ledgerPath, so the prompt, the check and the
// report all use the same sanitized string. The result is a slice of its own,
// nil when no file remains (decision 138).
func reviewLedger(files []github.FileChange, excluded []report.ExcludedFile) []github.FileChange {
	var ledger []github.FileChange
	for _, f := range files {
		if diffscope.IsExcluded(f.Path, excluded) || planningArtifact(f.Path) {
			continue
		}
		f.Path = ledgerPath(f.Path)
		f.OldPath = ledgerPath(f.OldPath)
		ledger = append(ledger, f)
	}
	return ledger
}

// planningArtifact reports whether p lies under .planwerk/ and is none of the
// paths ledgerKeptPlanwerk names.
func planningArtifact(p string) bool {
	if !strings.HasPrefix(p, planwerkDir) {
		return false
	}
	for _, keep := range ledgerKeptPlanwerk {
		if p == keep || (strings.HasSuffix(keep, "/") && strings.HasPrefix(p, keep)) {
			return false
		}
	}
	return true
}

// ledgerPath replaces every control character in p with U+FFFD, so a path git
// printed raw cannot break its line of the Changed Files list or the report.
// strings.Map turns each invalid UTF-8 byte into U+FFFD too.
func ledgerPath(p string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return utf8.RuneError
		}
		return r
	}, p)
}

// ledgerPaths returns the Path of each file, in order: the list
// hygiene.CheckFileLedger checks the session's account against.
func ledgerPaths(files []github.FileChange) []string {
	if len(files) == 0 {
		return nil
	}
	paths := make([]string, len(files))
	for i, f := range files {
		paths[i] = f.Path
	}
	return paths
}
