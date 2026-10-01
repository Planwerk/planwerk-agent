package brain

import (
	"cmp"
	"fmt"
	"slices"
	"time"

	"github.com/planwerk/planwerk-agent/internal/github"
	"github.com/planwerk/planwerk-agent/internal/report"
)

// The kinds of unit the bootstrap processes.
const (
	KindIssue   = "issue"
	KindPR      = "pr"
	KindCommits = "commits"
	KindDoc     = "doc"
)

// commitChunkSize is the largest number of commits one commit range unit holds.
const commitChunkSize = 25

// keyHashLen is the length of the SHA and hash prefixes in a unit key
// ("commits-<first12>-<last12>", "doc-<path>@<hash12>") and in the ref of a
// commit item. The keys are stored in state.json: changing it makes every
// processed commit range and document chunk run again.
const keyHashLen = 12

// Unit is one piece of a repository's history that the bootstrap analyzes in
// one session: a closed issue with the pull requests and the commit that
// closed it, a merged pull request that closes no issue, a range of commits
// pushed without a pull request, or one chunk of a decision document.
type Unit struct {
	// Key identifies the unit in the state, so a processed unit is not
	// processed again: "issue-<n>", "pr-<n>", "commits-<first12>-<last12>", or
	// "doc-<path>@<hash12>".
	Key string
	// Kind is one of KindIssue, KindPR, KindCommits, and KindDoc.
	Kind string
	// Title names the unit in the run's output.
	Title string
	// Source is the reference a page's provenance marker names:
	// "owner/repo#<n>", "owner/repo@<sha>", or "owner/repo:<path>".
	Source string
	// Issue is the issue number of an issue unit, 0 otherwise.
	Issue int
	// PRs holds the pull requests of the unit in ascending order: the ones that
	// closed an issue unit's issue, or the one pull request of a pull request
	// unit.
	PRs []int
	// Commits holds full SHAs whose messages belong to the unit: the commit
	// that closed an issue without a pull request, or the commits of a range,
	// oldest first.
	Commits []string
	// Doc is the chunk of a document unit, nil otherwise.
	Doc *DocChunk
}

// placedUnit is a unit with its place in the history. pos is the index of the
// newest history commit the unit holds (the oldest commit has index 0). A
// unit without a commit in the history is placed by its time instead: pos is
// then the largest index whose commit is not newer than that time, or -1.
type placedUnit struct {
	unit   Unit
	pos    int
	byTime bool
}

// BuildUnits groups the listing of repo ("owner/name") and the decision
// document chunks into units, in the order the bootstrap processes them, and
// counts the bot-authored pull requests it leaves out.
//
// Every closed issue is a unit, with the merged pull requests that closed it
// and, when a commit pushed without a pull request closed it, that commit.
// Every merged pull request that closed no closed issue is a unit unless a bot
// opened it. The history commits that belong to no pull request and closed no
// issue are cut, run by run, into ranges of at most commitChunkSize.
//
// The units follow the default branch: a unit sorts by its newest commit in
// the history, and a unit without one by the time it was closed or merged,
// after the units of the commits up to that time. Document units come last.
func BuildUnits(repo string, l Listing, docs []DocChunk) (units []Unit, skippedBots int) {
	index := make(map[string]int, len(l.History))
	newestOfPR := make(map[int]int)
	for i, c := range l.History {
		index[c.SHA] = i
		if c.PRNumber != 0 {
			newestOfPR[c.PRNumber] = i
		}
	}

	// The pull requests of each closed issue, from both directions GitHub
	// records the link in.
	closed := make(map[int]bool, len(l.Issues))
	for _, iss := range l.Issues {
		closed[iss.Number] = true
	}
	issuePRs := make(map[int][]int)
	attached := make(map[int]bool)
	for _, pr := range l.PRs {
		for _, n := range pr.ClosesIssues {
			if closed[n] {
				issuePRs[n] = append(issuePRs[n], pr.Number)
				attached[pr.Number] = true
			}
		}
	}

	var placed []placedUnit
	closers := make(map[string]bool)
	for _, iss := range l.Issues {
		prs := slices.Concat(issuePRs[iss.Number], iss.ClosedByPRs)
		slices.Sort(prs)
		prs = slices.Compact(prs)
		for _, n := range prs {
			attached[n] = true
		}
		u := Unit{
			Key:    fmt.Sprintf("issue-%d", iss.Number),
			Kind:   KindIssue,
			Title:  iss.Title,
			Source: fmt.Sprintf("%s#%d", repo, iss.Number),
			Issue:  iss.Number,
			PRs:    prs,
		}
		pos := -1
		for _, n := range prs {
			if i, ok := newestOfPR[n]; ok {
				pos = max(pos, i)
			}
		}
		if i, ok := index[iss.CloserSHA]; ok && l.History[i].PRNumber == 0 {
			u.Commits = []string{iss.CloserSHA}
			closers[iss.CloserSHA] = true
			pos = max(pos, i)
		}
		placed = append(placed, place(u, pos, iss.ClosedAt, l.History))
	}

	for _, pr := range l.PRs {
		if attached[pr.Number] {
			continue
		}
		if pr.AuthorIsBot {
			skippedBots++
			continue
		}
		u := Unit{
			Key:    fmt.Sprintf("pr-%d", pr.Number),
			Kind:   KindPR,
			Title:  pr.Title,
			Source: fmt.Sprintf("%s#%d", repo, pr.Number),
			PRs:    []int{pr.Number},
		}
		pos, ok := newestOfPR[pr.Number]
		if !ok {
			pos = -1
		}
		placed = append(placed, place(u, pos, pr.MergedAt, l.History))
	}

	// A run is a stretch of adjacent commits without a pull request; a commit
	// of a pull request and a commit that closed an issue end it.
	var run []string
	flush := func() {
		for chunk := range slices.Chunk(run, commitChunkSize) {
			first, last := chunk[0], chunk[len(chunk)-1]
			placed = append(placed, placedUnit{pos: index[last], unit: Unit{
				Key:     fmt.Sprintf("commits-%s-%s", shortSHA(first, keyHashLen), shortSHA(last, keyHashLen)),
				Kind:    KindCommits,
				Title:   fmt.Sprintf("%s %s..%s", countedCommits(len(chunk)), report.ShortSHA(first), report.ShortSHA(last)),
				Source:  repo + "@" + last,
				Commits: slices.Clone(chunk),
			}})
		}
		run = nil
	}
	for _, c := range l.History {
		if c.PRNumber != 0 || closers[c.SHA] {
			flush()
			continue
		}
		run = append(run, c.SHA)
	}
	flush()

	slices.SortStableFunc(placed, func(a, b placedUnit) int {
		return cmp.Or(
			cmp.Compare(a.pos, b.pos),
			cmp.Compare(boolRank(a.byTime), boolRank(b.byTime)),
			cmp.Compare(kindRank(a.unit.Kind), kindRank(b.unit.Kind)),
			cmp.Compare(unitNumber(a.unit), unitNumber(b.unit)),
			cmp.Compare(a.unit.Key, b.unit.Key),
		)
	})
	for _, p := range placed {
		units = append(units, p.unit)
	}

	docs = slices.Clone(docs)
	slices.SortStableFunc(docs, func(a, b DocChunk) int {
		return cmp.Or(cmp.Compare(a.Path, b.Path), cmp.Compare(a.Index, b.Index))
	})
	for i := range docs {
		d := docs[i]
		units = append(units, Unit{
			Key:    d.key(),
			Kind:   KindDoc,
			Title:  fmt.Sprintf("%s (chunk %d of %d)", d.Path, d.Index, d.Count),
			Source: repo + ":" + d.Path,
			Doc:    &d,
		})
	}
	return units, skippedBots
}

// place returns u at pos when the unit holds a commit of the history (pos >=
// 0), and otherwise at the newest history commit that is not after when.
func place(u Unit, pos int, when time.Time, history []github.HistoryCommit) placedUnit {
	if pos >= 0 {
		return placedUnit{unit: u, pos: pos}
	}
	for i := len(history) - 1; i >= 0; i-- {
		if !history[i].CommittedAt.After(when) {
			return placedUnit{unit: u, pos: i, byTime: true}
		}
	}
	return placedUnit{unit: u, pos: -1, byTime: true}
}

// boolRank orders false before true.
func boolRank(b bool) int {
	if b {
		return 1
	}
	return 0
}

// kindRank orders issue units before pull request units before commit ranges
// at the same place in the history.
func kindRank(kind string) int {
	switch kind {
	case KindIssue:
		return 0
	case KindPR:
		return 1
	default:
		return 2
	}
}

// unitNumber is the issue number of an issue unit and the pull request number
// of a pull request unit, 0 for the other kinds.
func unitNumber(u Unit) int {
	switch u.Kind {
	case KindIssue:
		return u.Issue
	case KindPR:
		return u.PRs[0]
	default:
		return 0
	}
}

// shortSHA returns the first n characters of sha, or all of it when it is
// shorter.
func shortSHA(sha string, n int) string {
	if len(sha) <= n {
		return sha
	}
	return sha[:n]
}

// countedCommits returns "1 commit" / "N commits".
func countedCommits(n int) string {
	if n == 1 {
		return "1 commit"
	}
	return fmt.Sprintf("%d commits", n)
}
