package brain

import (
	"fmt"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/planwerk/planwerk-agent/internal/capture"
	"github.com/planwerk/planwerk-agent/internal/github"
	"github.com/planwerk/planwerk-agent/internal/patterns"
	"github.com/planwerk/planwerk-agent/internal/redact"
)

const (
	// maxItemBytes is the largest body one item of a unit carries into a
	// prompt; a longer body is cut and says how much is missing.
	maxItemBytes = 32 << 10
	// maxUnitBytes is the largest sum of item bodies one unit carries into a
	// prompt; the items past it are dropped and counted.
	maxUnitBytes = 384 << 10
)

// Listing is the history of a repository as BuildUnits groups it: the commits
// of the default branch, oldest first, every merged pull request, and every
// closed issue.
type Listing struct {
	History []github.HistoryCommit
	PRs     []github.MergedPR
	Issues  []github.ClosedIssue
}

// Source reads a repository's history: the listing the units are built from,
// and the conversation of one issue or pull request when its unit is
// processed. APISource reads it from the GitHub API.
type Source interface {
	List(owner, name string) (Listing, error)
	Issue(owner, name string, number int) (*github.IssueThread, error)
	PullRequest(owner, name string, number int) (*github.PRThread, error)
}

// HistoryGitHub is the part of the GitHub client APISource reads through.
type HistoryGitHub interface {
	DefaultBranchHistory(owner, name string) ([]github.HistoryCommit, error)
	ListMergedPRs(owner, name string) ([]github.MergedPR, error)
	ListClosedIssues(owner, name string) ([]github.ClosedIssue, error)
	GetIssueThread(owner, name string, number int) (*github.IssueThread, error)
	GetPRThread(owner, name string, number int) (*github.PRThread, error)
}

// BootstrapGitHub is the part of the GitHub client the bootstrap run itself
// uses: the clone the sessions run in, and the commit messages read from it.
type BootstrapGitHub interface {
	CloneRepo(ref string) (*github.Repo, error)
	CommitMessage(dir, sha string) (string, error)
}

var (
	_ HistoryGitHub   = github.Client{}
	_ BootstrapGitHub = github.Client{}
	_ Source          = APISource{}
)

// APISource is the Source that reads the history from the GitHub API.
type APISource struct {
	GitHub HistoryGitHub
}

// List runs the three listings.
func (s APISource) List(owner, name string) (Listing, error) {
	history, err := s.GitHub.DefaultBranchHistory(owner, name)
	if err != nil {
		return Listing{}, err
	}
	prs, err := s.GitHub.ListMergedPRs(owner, name)
	if err != nil {
		return Listing{}, err
	}
	issues, err := s.GitHub.ListClosedIssues(owner, name)
	if err != nil {
		return Listing{}, err
	}
	return Listing{History: history, PRs: prs, Issues: issues}, nil
}

// Issue returns the issue with its whole conversation.
func (s APISource) Issue(owner, name string, number int) (*github.IssueThread, error) {
	return s.GitHub.GetIssueThread(owner, name, number)
}

// PullRequest returns the pull request with its whole conversation.
func (s APISource) PullRequest(owner, name string, number int) (*github.PRThread, error) {
	return s.GitHub.GetPRThread(owner, name, number)
}

// The kinds of item a unit's content is made of.
const (
	ItemIssue        = "issue"
	ItemIssueComment = "issue-comment"
	ItemPullRequest  = "pull-request"
	ItemPRComment    = "pr-comment"
	ItemReview       = "review"
	ItemReviewThread = "review-thread"
	ItemCommit       = "commit"
	ItemDecisionDoc  = "decision-document"
)

// Item is one piece of a unit's content as a prompt carries it: an issue, a
// comment, a review, a review thread, a commit message, or a document chunk.
// Ref locates it ("#42", "#42 a.go:3", a short SHA, a document title). Author
// and Date are empty where the reader returns none. Body is redacted and
// capped at maxItemBytes.
type Item struct {
	Kind   string
	Ref    string
	Author string
	Date   string
	Body   string
}

// UnitContext is what the analysis session of one unit is given.
type UnitContext struct {
	// RepoName is the repository as "owner/name".
	RepoName string
	Unit     Unit
	// Items is the unit's content, and Omitted the number of items left out
	// because the unit exceeds maxUnitBytes.
	Items   []Item
	Omitted int
	// PagesDir is the absolute path of the working set's pages directory, and
	// Index lists its pages, one line each (workingSetIndex).
	PagesDir string
	Index    string
	// Patterns is the pattern catalog a proposal is deduplicated against.
	Patterns []patterns.Pattern
}

// ReviewContext is what the review session of one unit is given: the unit's
// content and the pages its analysis proposed.
type ReviewContext struct {
	UnitContext
	Proposed []capture.ProposedPage
}

// The verdicts a review gives a proposed page.
const (
	VerdictAccept = "accept"
	VerdictRevise = "revise"
	VerdictReject = "reject"
)

// ReviewedPage is the review's verdict on one proposed page. Body is the
// corrected page of a "revise" verdict; Reason says why.
type ReviewedPage struct {
	Path    string `json:"path"`
	Verdict string `json:"verdict"`
	Body    string `json:"body"`
	Reason  string `json:"reason"`
}

// ReviewResult is the outcome of one review session. Model is the resolved
// Claude model id that produced it, excluded from the payload.
type ReviewResult struct {
	Pages []ReviewedPage `json:"pages"`
	Model string         `json:"-"`
}

// unitItems reads the content of u and returns it as items, in the order a
// reader follows the unit: the issue and its comments, then each pull request
// with its comments, reviews, review threads, and commits, then the unit's own
// commits, or the chunk of a document unit. Every body is redacted and capped
// at maxItemBytes. Items are kept until the next one would pass maxUnitBytes;
// omitted counts the rest.
func unitItems(src Source, gh BootstrapGitHub, repoDir, owner, name string, u Unit) (items []Item, omitted int, err error) {
	var all []Item
	if u.Kind == KindIssue {
		iss, err := src.Issue(owner, name, u.Issue)
		if err != nil {
			return nil, 0, err
		}
		ref := fmt.Sprintf("#%d", u.Issue)
		all = append(all, Item{Kind: ItemIssue, Ref: ref, Author: iss.Author, Body: titledBody(iss.Title, iss.Body)})
		for _, c := range iss.Comments {
			all = append(all, Item{Kind: ItemIssueComment, Ref: ref, Author: c.Author, Date: c.CreatedAt, Body: c.Body})
		}
	}
	for _, n := range u.PRs {
		pr, err := src.PullRequest(owner, name, n)
		if err != nil {
			return nil, 0, err
		}
		all = append(all, pullRequestItems(n, pr)...)
	}
	for _, sha := range u.Commits {
		msg, err := gh.CommitMessage(repoDir, sha)
		if err != nil {
			return nil, 0, err
		}
		all = append(all, Item{Kind: ItemCommit, Ref: shortSHA(sha, keyHashLen), Body: msg})
	}
	if u.Doc != nil {
		all = append(all, Item{Kind: ItemDecisionDoc, Ref: u.Title, Body: u.Doc.Text})
	}

	var redacted []string
	size := 0
	for i := range all {
		res := redact.Redact(all[i].Body)
		redacted = append(redacted, res.Names()...)
		all[i].Body = truncateBody(res.Text)
		if omitted > 0 || size+len(all[i].Body) > maxUnitBytes {
			omitted++
			continue
		}
		size += len(all[i].Body)
		items = append(items, all[i])
	}
	if len(redacted) > 0 {
		slices.Sort(redacted)
		slog.Warn("redacted secrets in the content of a unit", "unit", u.Key, "patterns", strings.Join(slices.Compact(redacted), ","))
	}
	if omitted > 0 {
		slog.Warn("unit exceeds its size budget; omitting its remaining items", "unit", u.Key, "omitted", omitted, "cap", maxUnitBytes)
	}
	return items, omitted, nil
}

// pullRequestItems returns the items of pull request n: the pull request, its
// comments, its reviews that say something, one item per review thread, and
// its commits.
func pullRequestItems(n int, pr *github.PRThread) []Item {
	ref := fmt.Sprintf("#%d", n)
	items := []Item{{Kind: ItemPullRequest, Ref: ref, Author: pr.Author, Body: titledBody(pr.Title, pr.Body)}}
	for _, c := range pr.Comments {
		items = append(items, Item{Kind: ItemPRComment, Ref: ref, Author: c.Author, Date: c.CreatedAt, Body: c.Body})
	}
	for _, r := range pr.Reviews {
		if strings.TrimSpace(r.Body) == "" {
			continue
		}
		items = append(items, Item{Kind: ItemReview, Ref: ref, Author: r.Author, Date: r.SubmittedAt, Body: r.Body})
	}
	for _, t := range pr.ReviewThreads {
		if len(t.Comments) == 0 {
			continue
		}
		anchor := fmt.Sprintf("%s:%d", t.Path, t.Line)
		var sb strings.Builder
		sb.WriteString(anchor)
		for _, c := range t.Comments {
			fmt.Fprintf(&sb, "\n\n%s (%s):\n%s", c.Author, c.CreatedAt, c.Body)
		}
		items = append(items, Item{Kind: ItemReviewThread, Ref: ref + " " + anchor, Body: sb.String()})
	}
	for _, c := range pr.Commits {
		items = append(items, Item{Kind: ItemCommit, Ref: shortSHA(c.SHA, keyHashLen), Body: strings.TrimRight(c.Headline+"\n\n"+c.Body, "\n")})
	}
	return items
}

// titledBody renders an issue or a pull request: "Title: <title>", a blank
// line, and the body.
func titledBody(title, body string) string {
	return strings.TrimRight("Title: "+title+"\n\n"+body, "\n")
}

// truncateBody cuts a body longer than maxItemBytes on a rune boundary and
// appends a line that says how many bytes are missing.
func truncateBody(body string) string {
	if len(body) <= maxItemBytes {
		return body
	}
	cut := maxItemBytes
	for cut > 0 && !utf8.RuneStart(body[cut]) {
		cut--
	}
	return fmt.Sprintf("%s\n[truncated: %d bytes omitted]", body[:cut], len(body)-cut)
}

// workingSetIndex lists the pages under pagesDir, one line per page in path
// order: "- <path>: <title>", plus " | <summary>" for a memory page that
// states one. Whitespace in a title or a summary is folded to single spaces,
// so a page cannot break out of its line.
func workingSetIndex(pagesDir string) string {
	var sb strings.Builder
	for _, p := range patterns.LoadMemoryPages(filepath.Join(pagesDir, memoryDirName)) {
		fmt.Fprintf(&sb, "- %s/%s: %s", memoryDirName, p.Name, foldSpace(p.Title))
		if summary := foldSpace(p.Summary); summary != "" {
			sb.WriteString(" | " + summary)
		}
		sb.WriteString("\n")
	}
	for _, p := range patterns.LoadMemoryPages(filepath.Join(pagesDir, patternsDirName)) {
		fmt.Fprintf(&sb, "- %s/%s: %s\n", patternsDirName, p.Name, foldSpace(p.Title))
	}
	return sb.String()
}

// foldSpace folds every run of whitespace in s to one space and trims it.
func foldSpace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
