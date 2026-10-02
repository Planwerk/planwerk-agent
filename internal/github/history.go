package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"strings"
	"time"
)

// HistoryCommit is one commit on a repository's default branch. PRNumber is
// the lowest-numbered merged pull request GitHub associates with the commit,
// or 0 for a commit that was pushed without one.
type HistoryCommit struct {
	SHA         string
	CommittedAt time.Time
	PRNumber    int
}

// MergedPR is one merged pull request. AuthorIsBot reports an author GitHub
// types as a Bot (Renovate, Dependabot). ClosesIssues holds the numbers of
// the issues of the same repository the pull request closes.
type MergedPR struct {
	Number       int
	Title        string
	MergedAt     time.Time
	AuthorIsBot  bool
	ClosesIssues []int
}

// ClosedIssue is one closed issue. CloserSHA is the commit that closed the
// issue, or "" when a pull request or a person closed it. ClosedByPRs holds
// the numbers of the merged pull requests of the same repository that closed
// it.
type ClosedIssue struct {
	Number      int
	Title       string
	ClosedAt    time.Time
	CloserSHA   string
	ClosedByPRs []int
}

// The names of the three listings, as their errors print them.
const (
	listingHistory   = "default-branch history"
	listingMergedPRs = "merged pull requests"
	listingIssues    = "closed issues"
)

// pageInfo is the cursor part of a GraphQL connection.
type pageInfo struct {
	HasNextPage bool   `json:"hasNextPage"`
	EndCursor   string `json:"endCursor"`
}

// historyQuery pages the commits of the default branch, newest first, with the
// pull requests GitHub associates with each commit.
const historyQuery = `query($owner: String!, $name: String!, $cursor: String) {
  repository(owner: $owner, name: $name) {
    defaultBranchRef {
      target {
        ... on Commit {
          history(first: 100, after: $cursor) {
            totalCount
            pageInfo { hasNextPage endCursor }
            nodes {
              oid
              committedDate
              associatedPullRequests(first: 5) { nodes { number merged } }
            }
          }
        }
      }
    }
  }
}`

// mergedPRsQuery pages the merged pull requests in creation order, with each
// one's author type and the issues it closes.
const mergedPRsQuery = `query($owner: String!, $name: String!, $cursor: String) {
  repository(owner: $owner, name: $name) {
    pullRequests(states: MERGED, first: 100, after: $cursor, orderBy: {field: CREATED_AT, direction: ASC}) {
      pageInfo { hasNextPage endCursor }
      nodes {
        number
        title
        mergedAt
        author { __typename }
        closingIssuesReferences(first: 10) { nodes { number repository { nameWithOwner } } }
      }
    }
  }
}`

// closedIssuesQuery pages the closed issues in creation order, with the pull
// requests that closed each one and the closer of its last close event.
const closedIssuesQuery = `query($owner: String!, $name: String!, $cursor: String) {
  repository(owner: $owner, name: $name) {
    issues(states: CLOSED, first: 100, after: $cursor, orderBy: {field: CREATED_AT, direction: ASC}) {
      pageInfo { hasNextPage endCursor }
      nodes {
        number
        title
        closedAt
        closedByPullRequestsReferences(first: 10, includeClosedPrs: true) {
          nodes { number state repository { nameWithOwner } }
        }
        timelineItems(itemTypes: [CLOSED_EVENT], last: 1) {
          nodes { ... on ClosedEvent { closer { __typename ... on Commit { oid } } } }
        }
      }
    }
  }
}`

// DefaultBranchHistory returns every commit on the repository's default
// branch, oldest first, following pagination. A repository without a default
// branch (an empty repository) yields no commit and no error.
func (c Client) DefaultBranchHistory(owner, name string) ([]HistoryCommit, error) {
	return c.DefaultBranchHistoryUntil(owner, name, func([]HistoryCommit, int) bool { return false })
}

// DefaultBranchHistoryUntil pages the history of the repository's default
// branch newest first and returns the commits of the pages it read, oldest
// first. After each page it calls done with the commits of that page, in
// GitHub's order, and the number of commits on the branch, and it stops at the
// page for which done returns true, or at the end of the history.
//
// GitHub lists the history by commit date, so a merge can place commits behind
// a commit the caller already holds: a caller has every commit of the branch
// only when the ones it holds and the ones it was handed add up to that
// number.
func (Client) DefaultBranchHistoryUntil(owner, name string, done func(page []HistoryCommit, total int) bool) ([]HistoryCommit, error) {
	total, first := 0, true
	commits, err := listAll(listingHistory, historyQuery, owner, name, func(raw []byte) ([]HistoryCommit, bool, string, error) {
		page, pageTotal, hasNext, endCursor, err := parseHistoryPage(raw)
		if err != nil {
			return nil, false, "", err
		}
		// The count belongs to the head the first page was read at.
		if first {
			total, first = pageTotal, false
		}
		if done(page, total) {
			return page, false, "", nil
		}
		return page, hasNext, endCursor, nil
	})
	if err != nil {
		return nil, err
	}
	// GitHub returns the history newest first.
	slices.Reverse(commits)
	return commits, nil
}

// ListMergedPRs returns every merged pull request of the repository in
// creation order, following pagination.
func (Client) ListMergedPRs(owner, name string) ([]MergedPR, error) {
	repo := owner + "/" + name
	return listAll(listingMergedPRs, mergedPRsQuery, owner, name, func(raw []byte) ([]MergedPR, bool, string, error) {
		return parseMergedPRsPage(raw, repo)
	})
}

// ListClosedIssues returns every closed issue of the repository in creation
// order, following pagination.
func (Client) ListClosedIssues(owner, name string) ([]ClosedIssue, error) {
	repo := owner + "/" + name
	return listAll(listingIssues, closedIssuesQuery, owner, name, func(raw []byte) ([]ClosedIssue, bool, string, error) {
		return parseClosedIssuesPage(raw, repo)
	})
}

// listAll pages the named listing to its end with one gh call per page, in the
// form of FetchReviewThreads, and returns the items parse decodes from each
// page in the order GitHub returns them.
func listAll[T any](listing, query, owner, name string, parse func(raw []byte) (items []T, hasNextPage bool, endCursor string, err error)) ([]T, error) {
	var all []T
	cursor := ""
	for {
		out, err := fetchListingPage(listing, query, owner, name, cursor)
		if err != nil {
			return nil, err
		}
		items, hasNext, endCursor, err := parse(out)
		if err != nil {
			return nil, fmt.Errorf("parsing %s: %w", listing, err)
		}
		all = append(all, items...)
		if !hasNext || endCursor == "" {
			return all, nil
		}
		cursor = endCursor
	}
}

// CommitMessage returns the full message of commit sha in the checkout at dir.
func (Client) CommitMessage(dir, sha string) (string, error) {
	msg, err := gitOutput(dir, "log", "-1", "--format=%B", sha)
	if err != nil {
		return "", fmt.Errorf("reading commit message of %s: %w", sha, err)
	}
	return msg, nil
}

// fetchListingPage runs query for one page of the named listing. cursor is
// empty for the first page (the nullable $cursor variable defaults to null, so
// GraphQL returns the first page).
func fetchListingPage(listing, query, owner, name, cursor string) ([]byte, error) {
	// owner/name go through -f (verbatim string): -F type-coerces its value, so
	// a repository literally named "2048", "404" or "null" would reach GraphQL
	// as a number or null and be rejected against String!.
	args := []string{"api", "graphql",
		"-f", "owner=" + owner,
		"-f", "name=" + name,
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
			return nil, fmt.Errorf("gh api graphql (%s): %w: %s", listing, err, strings.TrimSpace(string(exitErr.Stderr)))
		}
		return nil, fmt.Errorf("gh api graphql (%s): %w", listing, err)
	}
	return out, nil
}

// parseHistoryPage decodes one page of the history response. total is the
// number of commits on the branch. A null defaultBranchRef decodes to an empty
// page. It is pure so the mapping is unit-testable without gh.
func parseHistoryPage(raw []byte) (commits []HistoryCommit, total int, hasNextPage bool, endCursor string, err error) {
	var resp struct {
		Data struct {
			Repository struct {
				DefaultBranchRef struct {
					Target struct {
						History struct {
							TotalCount int      `json:"totalCount"`
							PageInfo   pageInfo `json:"pageInfo"`
							Nodes      []struct {
								OID                    string    `json:"oid"`
								CommittedDate          time.Time `json:"committedDate"`
								AssociatedPullRequests struct {
									Nodes []struct {
										Number int  `json:"number"`
										Merged bool `json:"merged"`
									} `json:"nodes"`
								} `json:"associatedPullRequests"`
							} `json:"nodes"`
						} `json:"history"`
					} `json:"target"`
				} `json:"defaultBranchRef"`
			} `json:"repository"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, 0, false, "", fmt.Errorf("decoding history response: %w", err)
	}
	history := resp.Data.Repository.DefaultBranchRef.Target.History
	for _, n := range history.Nodes {
		c := HistoryCommit{SHA: n.OID, CommittedAt: n.CommittedDate}
		for _, pr := range n.AssociatedPullRequests.Nodes {
			if pr.Merged && (c.PRNumber == 0 || pr.Number < c.PRNumber) {
				c.PRNumber = pr.Number
			}
		}
		commits = append(commits, c)
	}
	return commits, history.TotalCount, history.PageInfo.HasNextPage, history.PageInfo.EndCursor, nil
}

// parseMergedPRsPage decodes one page of the merged-pull-requests response.
// repo is the queried repository as "owner/name": a closing issue of another
// repository is dropped. It is pure so the mapping is unit-testable without gh.
func parseMergedPRsPage(raw []byte, repo string) (prs []MergedPR, hasNextPage bool, endCursor string, err error) {
	var resp struct {
		Data struct {
			Repository struct {
				PullRequests struct {
					PageInfo pageInfo `json:"pageInfo"`
					Nodes    []struct {
						Number   int       `json:"number"`
						Title    string    `json:"title"`
						MergedAt time.Time `json:"mergedAt"`
						Author   struct {
							Typename string `json:"__typename"`
						} `json:"author"`
						ClosingIssuesReferences struct {
							Nodes []repoRef `json:"nodes"`
						} `json:"closingIssuesReferences"`
					} `json:"nodes"`
				} `json:"pullRequests"`
			} `json:"repository"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, false, "", fmt.Errorf("decoding merged-pull-requests response: %w", err)
	}
	page := resp.Data.Repository.PullRequests
	for _, n := range page.Nodes {
		pr := MergedPR{Number: n.Number, Title: n.Title, MergedAt: n.MergedAt, AuthorIsBot: n.Author.Typename == "Bot"}
		pr.ClosesIssues = issuesIn(n.ClosingIssuesReferences.Nodes, repo)
		prs = append(prs, pr)
	}
	return prs, page.PageInfo.HasNextPage, page.PageInfo.EndCursor, nil
}

// parseClosedIssuesPage decodes one page of the closed-issues response. repo
// is the queried repository as "owner/name": a closing pull request of another
// repository, or one that was not merged, is dropped. It is pure so the
// mapping is unit-testable without gh.
func parseClosedIssuesPage(raw []byte, repo string) (issues []ClosedIssue, hasNextPage bool, endCursor string, err error) {
	var resp struct {
		Data struct {
			Repository struct {
				Issues struct {
					PageInfo pageInfo `json:"pageInfo"`
					Nodes    []struct {
						Number                         int       `json:"number"`
						Title                          string    `json:"title"`
						ClosedAt                       time.Time `json:"closedAt"`
						ClosedByPullRequestsReferences struct {
							Nodes []repoRef `json:"nodes"`
						} `json:"closedByPullRequestsReferences"`
						TimelineItems struct {
							Nodes []closedEvent `json:"nodes"`
						} `json:"timelineItems"`
					} `json:"nodes"`
				} `json:"issues"`
			} `json:"repository"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, false, "", fmt.Errorf("decoding closed-issues response: %w", err)
	}
	page := resp.Data.Repository.Issues
	for _, n := range page.Nodes {
		iss := ClosedIssue{Number: n.Number, Title: n.Title, ClosedAt: n.ClosedAt}
		iss.ClosedByPRs = mergedPRsIn(n.ClosedByPullRequestsReferences.Nodes, repo)
		iss.CloserSHA = closerSHA(n.TimelineItems.Nodes)
		issues = append(issues, iss)
	}
	return issues, page.PageInfo.HasNextPage, page.PageInfo.EndCursor, nil
}

// repoRef is an issue or a pull request another one refers to, with the
// repository it lives in. State is set for a pull request only.
type repoRef struct {
	Number     int    `json:"number"`
	State      string `json:"state"`
	Repository struct {
		NameWithOwner string `json:"nameWithOwner"`
	} `json:"repository"`
}

// in reports whether the reference lives in repo ("owner/name"). GitHub
// compares owner and repository names without case.
func (r repoRef) in(repo string) bool {
	return strings.EqualFold(r.Repository.NameWithOwner, repo)
}

// mergedPRsIn returns the numbers of the merged pull requests among refs that
// live in repo ("owner/name"), or nil when there is none.
func mergedPRsIn(refs []repoRef, repo string) []int {
	var out []int
	for _, ref := range refs {
		if ref.State == "MERGED" && ref.in(repo) {
			out = append(out, ref.Number)
		}
	}
	return out
}

// issuesIn returns the numbers of the issues among refs that live in repo
// ("owner/name"), or nil when there is none.
func issuesIn(refs []repoRef, repo string) []int {
	var out []int
	for _, ref := range refs {
		if ref.in(repo) {
			out = append(out, ref.Number)
		}
	}
	return out
}

// closedEvent is one CLOSED_EVENT node of an issue's timeline.
type closedEvent struct {
	Closer struct {
		Typename string `json:"__typename"`
		OID      string `json:"oid"`
	} `json:"closer"`
}

// closerSHA returns the commit that closed the issue, or "" when a pull
// request or a person closed it.
func closerSHA(events []closedEvent) string {
	sha := ""
	for _, ev := range events {
		if ev.Closer.Typename == "Commit" {
			sha = ev.Closer.OID
		}
	}
	return sha
}
