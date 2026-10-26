package claude

import (
	"cmp"

	"github.com/planwerk/planwerk-agent/internal/diffscope"
	"github.com/planwerk/planwerk-agent/internal/report"
)

// diffRevs returns the revision arguments of the diff a finder pass reads:
// sinceRef alone for a re-review, which compares the working tree with the
// commit before the fixes, and otherwise the three-dot range from the fork
// point of origin/<baseBranch>, DefaultBaseBranch when baseBranch is empty.
func diffRevs(baseBranch, sinceRef string) []string {
	if sinceRef != "" {
		return []string{sinceRef}
	}
	return []string{"origin/" + cmp.Or(baseBranch, DefaultBaseBranch) + "...HEAD"}
}

// writeDiff writes the diff a finder pass in dir reads, with the Client's
// exclusion setting, and returns it with its cleanup. A failure is logged and
// yields the zero Diff, whose prompt keeps the git command, and a dir of ""
// writes nothing (decision 137).
func (c *Client) writeDiff(dir, baseBranch, sinceRef string) (diffscope.Diff, func()) {
	return diffscope.MaterializeOrWarn(diffscope.Request{
		RepoDir: dir,
		Revs:    diffRevs(baseBranch, sinceRef),
		Exclude: c.diffExclusions,
	})
}

// runDiffFinder runs a finder-tier pass that emits findings over a diff it
// writes first: build renders the pass's prompt from that diff, the session is
// handed the diff's directory, and the directory is removed once the session
// returns. excluded is what the diff left out, which the caller records on its
// result. The review does not run through here: the review runner writes its
// diff and passes it in ReviewContext.Diff (decision 137).
func (c *Client) runDiffFinder(dir, baseBranch, sinceRef, label string, build func(diffscope.Diff) string) (text, model string, excluded []report.ExcludedFile, err error) {
	return c.runDiffFinderReading(dir, baseBranch, sinceRef, label, nil, build)
}

// runDiffFinderReading is runDiffFinder for a pass that may also read the
// directories in readDirs, beside the diff's: the ui specialist reads the
// design skill's reference documents (decision 155).
func (c *Client) runDiffFinderReading(dir, baseBranch, sinceRef, label string, readDirs []string, build func(diffscope.Diff) string) (text, model string, excluded []report.ExcludedFile, err error) {
	d, cleanup := c.writeDiff(dir, baseBranch, sinceRef)
	defer cleanup()
	text, model, err = c.runClaudeFinderFindings(dir, build(d), label, d.Dir, readDirs...)
	return text, model, d.Excluded, err
}
