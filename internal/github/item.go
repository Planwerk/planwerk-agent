package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"strings"
	"time"
)

// The kinds of item a repository holds, as Item.Kind names them.
const (
	ItemKindIssue = "issue"
	ItemKindPull  = "pull"
)

// updatedItemsTimeout bounds ListUpdatedItems. It is longer than ghTimeout
// because the one gh call follows every page of the listing.
const updatedItemsTimeout = 15 * time.Minute

// UpdatedItem is one entry of the listing by update time: the number of an
// issue or a pull request, when it was last updated as GitHub prints it, and
// which of the two it is.
type UpdatedItem struct {
	Number    int
	UpdatedAt string
	IsPull    bool
}

// ItemComment is one comment of an item's conversation or of a review thread.
type ItemComment struct {
	ID                string
	URL               string
	Author            string
	AuthorAssociation string
	CreatedAt         string
	UpdatedAt         string
	Body              string
}

// ItemReview is one submitted review of a pull request. State is the verdict
// as GitHub prints it (APPROVED, CHANGES_REQUESTED, COMMENTED, ...), and Body
// the review's summary.
type ItemReview struct {
	ID                string
	URL               string
	Author            string
	AuthorAssociation string
	State             string
	SubmittedAt       string
	UpdatedAt         string
	Body              string
}

// ItemThread is one review thread of a pull request. Path, Line, and DiffHunk
// are those of its first comment. Comments holds at most the first 100.
type ItemThread struct {
	ID         string
	IsResolved bool
	IsOutdated bool
	Path       string
	Line       int
	DiffHunk   string
	Comments   []ItemComment
}

// ItemCommit is one commit of a pull request.
type ItemCommit struct {
	SHA         string
	CommittedAt string
	Headline    string
	Body        string
}

// Item is an issue or a pull request with its whole conversation.
//
// Kind is ItemKindIssue or ItemKindPull. State is "open", "closed", or
// "merged", and StateReason an issue's lowercased close reason or "". Every
// timestamp is the string GitHub prints, or "" where GitHub has none.
// ClosedByPRs (issues) holds the merged pull requests of the same repository
// that closed the issue, CloserSHA the commit that closed it, and ClosesIssues
// (pull requests) the issues of the same repository the pull request closes.
// A list without an entry is nil.
type Item struct {
	Kind              string
	ID                string
	Number            int
	Title             string
	Body              string
	URL               string
	State             string
	StateReason       string
	Author            string
	AuthorIsBot       bool
	AuthorAssociation string
	Labels            []string
	CreatedAt         string
	UpdatedAt         string
	ClosedAt          string
	MergedAt          string
	BaseBranch        string
	ClosedByPRs       []int
	CloserSHA         string
	ClosesIssues      []int
	Comments          []ItemComment
	Reviews           []ItemReview
	Threads           []ItemThread
	Commits           []ItemCommit
}

// updatedItemsJQ prints one JSON object per item of a listing page.
const updatedItemsJQ = `.[] | {number, updated_at, pull: has("pull_request")}`

// ListUpdatedItems returns the issues and pull requests of the repository in
// ascending order of their update time, following pagination. With a since
// timestamp it returns only the items updated at or after it. A repository
// without a matching item yields no entry and no error.
func (Client) ListUpdatedItems(owner, name, since string) ([]UpdatedItem, error) {
	endpoint := fmt.Sprintf("repos/%s/%s/issues?state=all&sort=updated&direction=asc&per_page=100", owner, name)
	if since != "" {
		endpoint += "&since=" + since
	}
	ctx, cancel := context.WithTimeout(context.Background(), updatedItemsTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", "api", "--paginate", endpoint, "--jq", updatedItemsJQ)
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return nil, fmt.Errorf("gh api (updated items of %s/%s): %w: %s", owner, name, err, strings.TrimSpace(string(exitErr.Stderr)))
		}
		return nil, fmt.Errorf("gh api (updated items of %s/%s): %w", owner, name, err)
	}
	items, err := parseUpdatedItems(out)
	if err != nil {
		return nil, fmt.Errorf("parsing updated items of %s/%s: %w", owner, name, err)
	}
	return items, nil
}

// parseUpdatedItems decodes the stream of JSON objects ListUpdatedItems asks
// gh for. Empty output is an empty listing. It is pure so the mapping is
// unit-testable without gh.
func parseUpdatedItems(out []byte) ([]UpdatedItem, error) {
	var items []UpdatedItem
	dec := json.NewDecoder(bytes.NewReader(out))
	for {
		var raw struct {
			Number    int    `json:"number"`
			UpdatedAt string `json:"updated_at"`
			Pull      bool   `json:"pull"`
		}
		if err := dec.Decode(&raw); err != nil {
			if errors.Is(err, io.EOF) {
				return items, nil
			}
			return nil, err
		}
		items = append(items, UpdatedItem{Number: raw.Number, UpdatedAt: raw.UpdatedAt, IsPull: raw.Pull})
	}
}

// The node selections of an item's connections. The first query and the query
// that pages one connection share them, so the two cannot drift.
const (
	itemCommentFields   = `id url author { login } authorAssociation createdAt updatedAt body`
	itemCommentsNodes   = `pageInfo { hasNextPage endCursor } nodes { ` + itemCommentFields + ` }`
	itemReviewsNodes    = `pageInfo { hasNextPage endCursor } nodes { id url author { login } authorAssociation state submittedAt updatedAt body }`
	itemCommitsNodes    = `pageInfo { hasNextPage endCursor } nodes { commit { oid committedDate messageHeadline messageBody } }`
	itemThreadsNodes    = `pageInfo { hasNextPage endCursor } nodes { id isResolved isOutdated comments(first: 100) { pageInfo { hasNextPage } nodes { ` + itemCommentFields + ` path line diffHunk } } }`
	itemAuthorAndLabels = `author { login __typename } authorAssociation labels(first: 100) { nodes { name } }`
)

// The connections of an item that GetItem pages, by their GraphQL field name.
const (
	connComments      = "comments"
	connReviews       = "reviews"
	connCommits       = "commits"
	connReviewThreads = "reviewThreads"
)

// itemConnections lists the connections of an item that GetItem pages, in the
// order it follows them, each with its node selection.
var itemConnections = []struct{ name, nodes string }{
	{connComments, itemCommentsNodes},
	{connReviews, itemReviewsNodes},
	{connCommits, itemCommitsNodes},
	{connReviewThreads, itemThreadsNodes},
}

// itemQuery reads an issue or a pull request with the first page of each of
// its connections.
const itemQuery = `query($owner: String!, $name: String!, $number: Int!) {
  repository(owner: $owner, name: $name) {
    issueOrPullRequest(number: $number) {
      __typename
      ... on Issue {
        id number title body url state stateReason createdAt updatedAt closedAt
        ` + itemAuthorAndLabels + `
        closedByPullRequestsReferences(first: 10, includeClosedPrs: true) { nodes { number state repository { nameWithOwner } } }
        timelineItems(itemTypes: [CLOSED_EVENT], last: 1) { nodes { ... on ClosedEvent { closer { __typename ... on Commit { oid } } } } }
        comments(first: 100) { ` + itemCommentsNodes + ` }
      }
      ... on PullRequest {
        id number title body url state createdAt updatedAt closedAt mergedAt baseRefName
        ` + itemAuthorAndLabels + `
        closingIssuesReferences(first: 10) { nodes { number repository { nameWithOwner } } }
        comments(first: 100) { ` + itemCommentsNodes + ` }
        reviews(first: 100) { ` + itemReviewsNodes + ` }
        commits(first: 100) { ` + itemCommitsNodes + ` }
        reviewThreads(first: 100) { ` + itemThreadsNodes + ` }
      }
    }
  }
}`

// itemPageQuery returns the query that reads one further page of a single
// connection. parent is "issue" or "pullRequest".
func itemPageQuery(parent, connection, nodes string) string {
	return `query($owner: String!, $name: String!, $number: Int!, $cursor: String) {
  repository(owner: $owner, name: $name) {
    ` + parent + `(number: $number) {
      ` + connection + `(first: 100, after: $cursor) { ` + nodes + ` }
    }
  }
}`
}

// GetItem fetches issue or pull request number with its whole conversation:
// the comments, and for a pull request the reviews, the commits, and the
// review threads. Every connection is followed to its end. A review thread
// keeps its first 100 comments, and a longer one is logged.
func (Client) GetItem(owner, name string, number int) (*Item, error) {
	repo := owner + "/" + name
	ref := fmt.Sprintf("%s#%d", repo, number)
	out, err := fetchItemPage(ref, itemQuery, owner, name, number, "")
	if err != nil {
		return nil, err
	}
	it, more, err := parseItem(out, repo)
	if err != nil {
		return nil, fmt.Errorf("parsing item %s: %w", ref, err)
	}
	if it == nil {
		return nil, fmt.Errorf("item %s not found", ref)
	}

	parent := "issue"
	if it.Kind == ItemKindPull {
		parent = "pullRequest"
	}
	for _, conn := range itemConnections {
		query := itemPageQuery(parent, conn.name, conn.nodes)
		for cursor := more[conn.name]; cursor != ""; {
			out, err := fetchItemPage(ref, query, owner, name, number, cursor)
			if err != nil {
				return nil, err
			}
			next, err := parseItemPage(out, it, repo)
			if err != nil {
				return nil, fmt.Errorf("parsing item %s: %w", ref, err)
			}
			cursor = next[conn.name]
		}
	}
	return it, nil
}

// fetchItemPage runs one query of GetItem. cursor is empty for the first
// query, which declares no $cursor variable.
func fetchItemPage(ref, query, owner, name string, number int, cursor string) ([]byte, error) {
	// owner/name go through -f (verbatim string) and number through -F, as in
	// fetchReviewThreadsPage.
	args := []string{"api", "graphql",
		"-f", "owner=" + owner,
		"-f", "name=" + name,
		"-F", fmt.Sprintf("number=%d", number),
		"-f", "query=" + query,
	}
	if cursor != "" {
		args = append(args, "-f", "cursor="+cursor)
	}
	ctx, cancel := context.WithTimeout(context.Background(), ghTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", args...)
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return nil, fmt.Errorf("gh api graphql (item %s): %w: %s", ref, err, strings.TrimSpace(string(exitErr.Stderr)))
		}
		return nil, fmt.Errorf("gh api graphql (item %s): %w", ref, err)
	}
	return out, nil
}

// ghConnection is one page of a GraphQL connection.
type ghConnection[T any] struct {
	PageInfo pageInfo `json:"pageInfo"`
	Nodes    []T      `json:"nodes"`
}

// ghItemComment is a comment node. Path, Line, and DiffHunk are set on the
// comment of a review thread only.
type ghItemComment struct {
	ID                string   `json:"id"`
	URL               string   `json:"url"`
	Author            ghAuthor `json:"author"`
	AuthorAssociation string   `json:"authorAssociation"`
	CreatedAt         string   `json:"createdAt"`
	UpdatedAt         string   `json:"updatedAt"`
	Body              string   `json:"body"`
	Path              string   `json:"path"`
	Line              int      `json:"line"`
	DiffHunk          string   `json:"diffHunk"`
}

// ghItem is the issue or pull request node of an item response. A response
// that pages one connection fills that connection alone.
type ghItem struct {
	Typename    string `json:"__typename"`
	ID          string `json:"id"`
	Number      int    `json:"number"`
	Title       string `json:"title"`
	Body        string `json:"body"`
	URL         string `json:"url"`
	State       string `json:"state"`
	StateReason string `json:"stateReason"`
	CreatedAt   string `json:"createdAt"`
	UpdatedAt   string `json:"updatedAt"`
	ClosedAt    string `json:"closedAt"`
	MergedAt    string `json:"mergedAt"`
	BaseRefName string `json:"baseRefName"`
	Author      struct {
		Login    string `json:"login"`
		Typename string `json:"__typename"`
	} `json:"author"`
	AuthorAssociation string `json:"authorAssociation"`
	Labels            struct {
		Nodes []struct {
			Name string `json:"name"`
		} `json:"nodes"`
	} `json:"labels"`
	ClosedByPullRequestsReferences struct {
		Nodes []repoRef `json:"nodes"`
	} `json:"closedByPullRequestsReferences"`
	ClosingIssuesReferences struct {
		Nodes []repoRef `json:"nodes"`
	} `json:"closingIssuesReferences"`
	TimelineItems struct {
		Nodes []closedEvent `json:"nodes"`
	} `json:"timelineItems"`
	Comments ghConnection[ghItemComment] `json:"comments"`
	Reviews  ghConnection[struct {
		ID                string   `json:"id"`
		URL               string   `json:"url"`
		Author            ghAuthor `json:"author"`
		AuthorAssociation string   `json:"authorAssociation"`
		State             string   `json:"state"`
		SubmittedAt       string   `json:"submittedAt"`
		UpdatedAt         string   `json:"updatedAt"`
		Body              string   `json:"body"`
	}] `json:"reviews"`
	Commits ghConnection[struct {
		Commit struct {
			OID             string `json:"oid"`
			CommittedDate   string `json:"committedDate"`
			MessageHeadline string `json:"messageHeadline"`
			MessageBody     string `json:"messageBody"`
		} `json:"commit"`
	}] `json:"commits"`
	ReviewThreads ghConnection[struct {
		ID         string                      `json:"id"`
		IsResolved bool                        `json:"isResolved"`
		IsOutdated bool                        `json:"isOutdated"`
		Comments   ghConnection[ghItemComment] `json:"comments"`
	}] `json:"reviewThreads"`
}

// decodeItemResponse returns the item node of a response of GetItem: the
// issueOrPullRequest of the first query, or the issue or pullRequest of a
// query that pages one connection. A null node decodes to nil.
func decodeItemResponse(raw []byte) (*ghItem, error) {
	var resp struct {
		Data struct {
			Repository struct {
				IssueOrPullRequest *ghItem `json:"issueOrPullRequest"`
				Issue              *ghItem `json:"issue"`
				PullRequest        *ghItem `json:"pullRequest"`
			} `json:"repository"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("decoding item response: %w", err)
	}
	r := resp.Data.Repository
	switch {
	case r.IssueOrPullRequest != nil:
		return r.IssueOrPullRequest, nil
	case r.Issue != nil:
		return r.Issue, nil
	default:
		return r.PullRequest, nil
	}
}

// parseItem decodes the response of itemQuery for repo ("owner/name"). more
// maps the name of a connection whose first page has a next one to that
// page's end cursor. A null item decodes to a nil Item and no error. A closing
// link goes through the helpers parseClosedIssuesPage and parseMergedPRsPage
// use, so an item and a listing follow the same rules. It runs no subprocess,
// so the mapping is unit-testable without gh.
func parseItem(raw []byte, repo string) (it *Item, more map[string]string, err error) {
	g, err := decodeItemResponse(raw)
	if err != nil {
		return nil, nil, err
	}
	if g == nil {
		return nil, nil, nil
	}
	it = &Item{
		Kind:              ItemKindIssue,
		ID:                g.ID,
		Number:            g.Number,
		Title:             g.Title,
		Body:              g.Body,
		URL:               g.URL,
		State:             strings.ToLower(g.State),
		StateReason:       strings.ToLower(g.StateReason),
		Author:            g.Author.Login,
		AuthorIsBot:       g.Author.Typename == "Bot",
		AuthorAssociation: g.AuthorAssociation,
		CreatedAt:         g.CreatedAt,
		UpdatedAt:         g.UpdatedAt,
		ClosedAt:          g.ClosedAt,
		MergedAt:          g.MergedAt,
		BaseBranch:        g.BaseRefName,
		ClosedByPRs:       mergedPRsIn(g.ClosedByPullRequestsReferences.Nodes, repo),
		CloserSHA:         closerSHA(g.TimelineItems.Nodes),
		ClosesIssues:      issuesIn(g.ClosingIssuesReferences.Nodes, repo),
	}
	if g.Typename == "PullRequest" {
		it.Kind = ItemKindPull
	}
	for _, l := range g.Labels.Nodes {
		it.Labels = append(it.Labels, l.Name)
	}
	return it, appendItemPage(it, g, repo), nil
}

// parseItemPage decodes the response of an itemPageQuery, appends the nodes of
// its connection to it, and returns the end cursor of that connection under
// its name when a further page follows.
func parseItemPage(raw []byte, it *Item, repo string) (more map[string]string, err error) {
	g, err := decodeItemResponse(raw)
	if err != nil {
		return nil, err
	}
	if g == nil {
		return nil, errors.New("the response holds no item")
	}
	return appendItemPage(it, g, repo), nil
}

// appendItemPage appends the nodes of every connection g carries to it, in
// GitHub's order, and returns the end cursor of each connection that has a
// next page. A review thread GitHub cut at 100 comments is logged.
func appendItemPage(it *Item, g *ghItem, repo string) map[string]string {
	for _, c := range g.Comments.Nodes {
		it.Comments = append(it.Comments, itemComment(c))
	}
	for _, r := range g.Reviews.Nodes {
		it.Reviews = append(it.Reviews, ItemReview{
			ID:                r.ID,
			URL:               r.URL,
			Author:            r.Author.Login,
			AuthorAssociation: r.AuthorAssociation,
			State:             r.State,
			SubmittedAt:       r.SubmittedAt,
			UpdatedAt:         r.UpdatedAt,
			Body:              r.Body,
		})
	}
	for _, c := range g.Commits.Nodes {
		it.Commits = append(it.Commits, ItemCommit{
			SHA:         c.Commit.OID,
			CommittedAt: c.Commit.CommittedDate,
			Headline:    c.Commit.MessageHeadline,
			Body:        c.Commit.MessageBody,
		})
	}
	for _, n := range g.ReviewThreads.Nodes {
		t := ItemThread{ID: n.ID, IsResolved: n.IsResolved, IsOutdated: n.IsOutdated}
		for _, c := range n.Comments.Nodes {
			t.Comments = append(t.Comments, itemComment(c))
		}
		if len(n.Comments.Nodes) > 0 {
			first := n.Comments.Nodes[0]
			t.Path, t.Line, t.DiffHunk = first.Path, first.Line, first.DiffHunk
		}
		if n.Comments.PageInfo.HasNextPage {
			slog.Warn("review thread holds more than 100 comments; mirroring the first 100",
				"pull_request", fmt.Sprintf("%s#%d", repo, it.Number), "thread", n.ID)
		}
		it.Threads = append(it.Threads, t)
	}

	more := make(map[string]string)
	for name, page := range map[string]pageInfo{
		connComments:      g.Comments.PageInfo,
		connReviews:       g.Reviews.PageInfo,
		connCommits:       g.Commits.PageInfo,
		connReviewThreads: g.ReviewThreads.PageInfo,
	} {
		if page.HasNextPage && page.EndCursor != "" {
			more[name] = page.EndCursor
		}
	}
	return more
}

// itemComment maps a comment node to an ItemComment.
func itemComment(c ghItemComment) ItemComment {
	return ItemComment{
		ID:                c.ID,
		URL:               c.URL,
		Author:            c.Author.Login,
		AuthorAssociation: c.AuthorAssociation,
		CreatedAt:         c.CreatedAt,
		UpdatedAt:         c.UpdatedAt,
		Body:              c.Body,
	}
}
