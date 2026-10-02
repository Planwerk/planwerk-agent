package brain

import (
	"cmp"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"slices"
	"time"

	"github.com/planwerk/planwerk-agent/internal/github"
	"github.com/planwerk/planwerk-agent/internal/mirror"
)

// The states of a mirrored item that the listing keeps.
const (
	mirrorStateMerged = "merged"
	mirrorStateClosed = "closed"
)

// MirrorSource is the Source that reads the history from the mirror
// `brain sync` keeps in the directory Dir. It calls no GitHub API, and it
// reads the mirror as it is: it does not sync it, so the history ends where
// the last sync ended. The mirror holds GitHub's text unchanged; unitItems
// redacts every body it reads from a Source.
type MirrorSource struct {
	Dir string
}

var _ Source = MirrorSource{}

// List returns the mirrored history: the commits of history.jsonl, every
// mirrored pull request whose state is merged, and every mirrored issue whose
// state is closed, each in the order of creation. A mirror that is missing, or
// in which no sync has finished, is an error that names the command to run.
func (s MirrorSource) List(owner, name string) (Listing, error) {
	repo := owner + "/" + name
	st, err := mirror.LoadFinishedState(s.Dir, repo)
	if err != nil {
		return Listing{}, err
	}
	slog.Info("reading the history from the local mirror", "dir", s.Dir, "synced_at", st.SyncedAt)

	history, err := mirror.ReadHistory(s.Dir)
	if err != nil {
		return Listing{}, err
	}
	l := Listing{History: history}

	pulls, err := listMirrored(s.Dir, github.ItemKindPull, mirrorStateMerged,
		func(fm mirror.Frontmatter) string { return fm.MergedAt })
	if err != nil {
		return Listing{}, err
	}
	for _, p := range pulls {
		l.PRs = append(l.PRs, github.MergedPR{
			Number: p.fm.Number, Title: p.fm.Title, MergedAt: p.at, AuthorIsBot: p.fm.AuthorIsBot, ClosesIssues: nilIfEmpty(p.fm.ClosesIssues),
		})
	}
	issues, err := listMirrored(s.Dir, github.ItemKindIssue, mirrorStateClosed,
		func(fm mirror.Frontmatter) string { return fm.ClosedAt })
	if err != nil {
		return Listing{}, err
	}
	for _, i := range issues {
		l.Issues = append(l.Issues, github.ClosedIssue{
			Number: i.fm.Number, Title: i.fm.Title, ClosedAt: i.at, CloserSHA: i.fm.CloserSHA, ClosedByPRs: nilIfEmpty(i.fm.ClosedByPRs),
		})
	}
	return l, nil
}

// mirroredItem is the frontmatter of one item file the listing keeps, with its
// creation time and the time it was merged or closed.
type mirroredItem struct {
	fm      mirror.Frontmatter
	created time.Time
	at      time.Time
}

// listMirrored reads the frontmatter of every item file of kind in the mirror
// directory dir and returns the items in the given state, sorted by creation
// time, then number. endOf names the time the state was reached. A mirror
// without a file of kind holds no item. A file whose frontmatter cannot be
// read fails the listing, and so does a kept item whose creation or end time
// is not RFC 3339.
func listMirrored(dir, kind, state string, endOf func(mirror.Frontmatter) string) ([]mirroredItem, error) {
	paths, err := mirror.ItemFiles(dir, kind)
	if err != nil {
		return nil, err
	}
	var items []mirroredItem
	for _, path := range paths {
		fm, err := mirror.ReadFrontmatter(path)
		if err != nil {
			return nil, err
		}
		if fm.State != state {
			continue
		}
		it := mirroredItem{fm: fm}
		if it.created, err = time.Parse(time.RFC3339, fm.CreatedAt); err != nil {
			return nil, fmt.Errorf("reading %s: %w", path, err)
		}
		if it.at, err = time.Parse(time.RFC3339, endOf(fm)); err != nil {
			return nil, fmt.Errorf("reading %s: %w", path, err)
		}
		items = append(items, it)
	}
	slices.SortFunc(items, func(a, b mirroredItem) int {
		return cmp.Or(a.created.Compare(b.created), cmp.Compare(a.fm.Number, b.fm.Number))
	})
	return items, nil
}

// nilIfEmpty returns nil for a list without an entry, as the API listings do.
func nilIfEmpty(numbers []int) []int {
	if len(numbers) == 0 {
		return nil
	}
	return numbers
}

// Issue returns the mirrored issue with its whole conversation.
func (s MirrorSource) Issue(owner, name string, number int) (*github.IssueThread, error) {
	it, err := s.item(owner, name, github.ItemKindIssue, number)
	if err != nil {
		return nil, err
	}
	return &github.IssueThread{
		Number:   it.Number,
		Title:    it.Title,
		Body:     it.Body,
		Author:   it.Author,
		Comments: mirroredComments(it.Comments),
	}, nil
}

// PullRequest returns the mirrored pull request with its whole conversation.
func (s MirrorSource) PullRequest(owner, name string, number int) (*github.PRThread, error) {
	it, err := s.item(owner, name, github.ItemKindPull, number)
	if err != nil {
		return nil, err
	}
	pr := &github.PRThread{
		Number:   it.Number,
		Title:    it.Title,
		Body:     it.Body,
		Author:   it.Author,
		Comments: mirroredComments(it.Comments),
	}
	for _, r := range it.Reviews {
		pr.Reviews = append(pr.Reviews, github.PRReview{Author: r.Author, SubmittedAt: r.SubmittedAt, Body: r.Body})
	}
	for _, c := range it.Commits {
		pr.Commits = append(pr.Commits, github.PRCommit{SHA: c.SHA, Headline: c.Headline, Body: c.Body})
	}
	for _, t := range it.Threads {
		thread := github.ReviewThread{ID: t.ID, IsResolved: t.IsResolved, IsOutdated: t.IsOutdated, Path: t.Path, Line: t.Line, DiffHunk: t.DiffHunk}
		for _, c := range t.Comments {
			thread.Comments = append(thread.Comments, github.ReviewThreadComment{Author: c.Author, Body: c.Body, CreatedAt: c.CreatedAt})
		}
		pr.ReviewThreads = append(pr.ReviewThreads, thread)
	}
	return pr, nil
}

// item parses the mirrored file of an issue or a pull request. A missing file
// is an error that names the command to run.
func (s MirrorSource) item(owner, name, kind string, number int) (*github.Item, error) {
	path := mirror.ItemPath(s.Dir, kind, number)
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%s/%s#%d is not in the mirror at %s; run \"planwerk-agent brain sync %s/%s\"", owner, name, number, s.Dir, owner, name)
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	it, err := mirror.Parse(data)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	return it, nil
}

// mirroredComments converts the comments of a mirrored item, in order.
func mirroredComments(in []github.ItemComment) []github.ThreadComment {
	var out []github.ThreadComment
	for _, c := range in {
		out = append(out, github.ThreadComment{Author: c.Author, AuthorAssociation: c.AuthorAssociation, CreatedAt: c.CreatedAt, Body: c.Body})
	}
	return out
}
