package hygiene

import (
	"strings"

	"github.com/planwerk/planwerk-agent/internal/report"
)

// CheckFileLedger compares the files the review prompt listed with the
// finder's account of them in result.FilesReviewed, and records the outcome in
// result.Gates.Files: how many were listed, how many the session reported
// reading, and every other listed file, in listed order, as skipped with the
// session's reason or as unreported. It returns how many listed files were not
// reviewed.
//
// It never moves, demotes or drops a finding: the account is the session's
// own claim, and a session can call a file reviewed it never opened. An empty
// list or a nil result records nothing, so a run that listed no files stays
// distinguishable from one that covered every file. An account entry matches
// the listed path it names exactly, so a path git reports with surrounding
// spaces matches as listed. An entry that names no listed path matches the
// listed path it equals once both lose their surrounding spaces and a leading
// "./". A reviewed entry wins over a duplicate that says otherwise, and an
// entry for a path the list does not hold is ignored. The review command hands
// it the merged result, whose account is the primary review's: MergeResults
// keeps the primary (decision 138). In a partitioned review that account joins
// every partition session's, each for its own share of the list (decision
// 142).
func CheckFileLedger(result *report.ReviewResult, listed []string) int {
	if result == nil || len(listed) == 0 {
		return 0
	}
	inList := make(map[string]bool, len(listed))
	for _, p := range listed {
		inList[p] = true
	}
	exact := make(map[string]report.FileReview)
	loose := make(map[string]report.FileReview)
	for _, e := range result.FilesReviewed {
		if inList[e.File] {
			keepAccount(exact, e.File, e)
		} else if key := ledgerKey(e.File); key != "" {
			keepAccount(loose, key, e)
		}
	}

	stats := &report.FileGateStats{Changed: len(listed)}
	for _, p := range listed {
		e, ok := exact[p]
		if l, found := loose[ledgerKey(p)]; found && (!ok || (!isReviewed(e) && isReviewed(l))) {
			e, ok = l, true
		}
		switch {
		case !ok:
			stats.Missing = append(stats.Missing, report.MissingFile{File: p, Status: report.FileUnreported})
		case isReviewed(e):
			stats.Reviewed++
		default:
			stats.Missing = append(stats.Missing, report.MissingFile{File: p, Status: report.FileSkipped, Reason: strings.Join(strings.Fields(e.Reason), " ")})
		}
	}
	ensureGates(result).Files = stats
	return len(stats.Missing)
}

// keepAccount records e under key in account, unless a reviewed entry is
// already there.
func keepAccount(account map[string]report.FileReview, key string, e report.FileReview) {
	if prev, seen := account[key]; seen && isReviewed(prev) {
		return
	}
	account[key] = e
}

// isReviewed reports whether an account entry says the file was reviewed. The
// CLI does not validate the result-envelope fallback, so a status may arrive in
// any letter case.
func isReviewed(e report.FileReview) bool {
	return strings.EqualFold(e.Status, report.FileReviewed)
}

// ledgerKey normalizes a path for the fallback match between the finder's
// account and the listed paths: the surrounding spaces and a leading "./" go,
// anything else must match as listed.
func ledgerKey(p string) string {
	return strings.TrimPrefix(strings.TrimSpace(p), "./")
}
