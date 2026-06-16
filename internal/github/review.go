package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// ReviewComment represents a single inline comment in a PR review.
type ReviewComment struct {
	Path      string `json:"path"`
	Line      int    `json:"line"`                 // absolute line number on RIGHT side
	Side      string `json:"side"`                 // "RIGHT" for new-file lines
	StartLine int    `json:"start_line,omitempty"`  // for multi-line comments
	StartSide string `json:"start_side,omitempty"`  // "RIGHT" when StartLine is set
	Body      string `json:"body"`
}

// ReviewRequest represents the payload for creating a PR review.
type ReviewRequest struct {
	Body     string          `json:"body"`
	Event    string          `json:"event"`     // "COMMENT"
	CommitID string          `json:"commit_id"`
	Comments []ReviewComment `json:"comments"`
}

const reviewSignature = "<!-- planwerk-review-inline -->"

// ReviewThreadComment is a single comment within a PR review thread, flattened
// for the address command: the author login, the comment body, and when it was
// created. The full chain is what Claude needs to understand the reviewer's ask.
type ReviewThreadComment struct {
	Author    string
	Body      string
	CreatedAt string
}

// ReviewThread is a PR review thread: a chain of inline comments anchored to a
// file and line, plus its resolved/outdated status and the diff hunk the
// comment was anchored to. The address command fetches these so it can offer
// the unresolved ones for the operator to act on. Path, Line, and DiffHunk are
// taken from the thread's first comment.
type ReviewThread struct {
	ID         string
	IsResolved bool
	IsOutdated bool
	Path       string
	Line       int
	DiffHunk   string
	Comments   []ReviewThreadComment
}

// reviewThreadsResponse is the GraphQL envelope returned by FetchReviewThreads
// for a single page of a PR's review threads.
type reviewThreadsResponse struct {
	Data struct {
		Repository struct {
			PullRequest struct {
				ReviewThreads struct {
					PageInfo struct {
						HasNextPage bool   `json:"hasNextPage"`
						EndCursor   string `json:"endCursor"`
					} `json:"pageInfo"`
					Nodes []struct {
						ID         string `json:"id"`
						IsResolved bool   `json:"isResolved"`
						IsOutdated bool   `json:"isOutdated"`
						Comments   struct {
							Nodes []struct {
								Author struct {
									Login string `json:"login"`
								} `json:"author"`
								Body      string `json:"body"`
								CreatedAt string `json:"createdAt"`
								Path      string `json:"path"`
								Line      int    `json:"line"`
								DiffHunk  string `json:"diffHunk"`
							} `json:"nodes"`
						} `json:"comments"`
					} `json:"nodes"`
				} `json:"reviewThreads"`
			} `json:"pullRequest"`
		} `json:"repository"`
	} `json:"data"`
}

// reviewThreadsQuery pages a PR's review threads, carrying each thread's
// resolved/outdated status and its full comment chain (with the anchored file,
// line, and diff hunk on each comment).
const reviewThreadsQuery = `query($owner: String!, $name: String!, $number: Int!, $cursor: String) {
  repository(owner: $owner, name: $name) {
    pullRequest(number: $number) {
      reviewThreads(first: 100, after: $cursor) {
        pageInfo { hasNextPage endCursor }
        nodes {
          id
          isResolved
          isOutdated
          comments(first: 100) {
            nodes { author { login } body createdAt path line diffHunk }
          }
        }
      }
    }
  }
}`

// FetchReviewThreads returns every review thread on the PR via the GitHub
// GraphQL API, following pagination so a PR with more than 100 threads is fully
// covered. Each thread carries its resolved/outdated status, the full comment
// chain, and the file/line/diff-hunk the thread is anchored to. Use
// FilterReviewThreads to drop resolved threads and the tool's own findings.
func FetchReviewThreads(owner, repo string, number int) ([]ReviewThread, error) {
	var all []ReviewThread
	cursor := ""
	for {
		out, err := fetchReviewThreadsPage(owner, repo, number, cursor)
		if err != nil {
			return nil, err
		}
		threads, hasNext, endCursor, err := parseReviewThreads(out)
		if err != nil {
			return nil, fmt.Errorf("parsing review threads for %s/%s#%d: %w", owner, repo, number, err)
		}
		all = append(all, threads...)
		if !hasNext || endCursor == "" {
			return all, nil
		}
		cursor = endCursor
	}
}

// fetchReviewThreadsPage runs the GraphQL query for a single page. cursor is
// empty for the first page (the nullable $cursor variable defaults to null, so
// GraphQL returns the first page).
func fetchReviewThreadsPage(owner, repo string, number int, cursor string) ([]byte, error) {
	args := []string{"api", "graphql",
		"-F", "owner=" + owner,
		"-F", "name=" + repo,
		"-F", fmt.Sprintf("number=%d", number),
		"-f", "query=" + reviewThreadsQuery,
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
			return nil, fmt.Errorf("gh api graphql (review threads): %w: %s", err, strings.TrimSpace(string(exitErr.Stderr)))
		}
		return nil, fmt.Errorf("gh api graphql (review threads): %w", err)
	}
	return out, nil
}

// parseReviewThreads decodes one page of the review-threads GraphQL response
// into flattened threads plus the pagination cursor. Path/Line/DiffHunk are
// taken from each thread's first comment; threads with no comments keep those
// fields zero. It is pure so the flattening is unit-testable without gh.
func parseReviewThreads(raw []byte) (threads []ReviewThread, hasNextPage bool, endCursor string, err error) {
	var resp reviewThreadsResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, false, "", fmt.Errorf("decoding review-threads response: %w", err)
	}
	rt := resp.Data.Repository.PullRequest.ReviewThreads
	for _, n := range rt.Nodes {
		t := ReviewThread{
			ID:         n.ID,
			IsResolved: n.IsResolved,
			IsOutdated: n.IsOutdated,
		}
		for _, c := range n.Comments.Nodes {
			t.Comments = append(t.Comments, ReviewThreadComment{
				Author:    c.Author.Login,
				Body:      c.Body,
				CreatedAt: c.CreatedAt,
			})
		}
		if len(n.Comments.Nodes) > 0 {
			first := n.Comments.Nodes[0]
			t.Path = first.Path
			t.Line = first.Line
			t.DiffHunk = first.DiffHunk
		}
		threads = append(threads, t)
	}
	return threads, rt.PageInfo.HasNextPage, rt.PageInfo.EndCursor, nil
}

// FilterReviewThreads drops the threads the address command should not offer:
// resolved threads (unless includeResolved) and any thread whose first comment
// carries planwerk-review's inline signature, so address never tries to address
// the tool's own findings. Threads with no comments are dropped as well — there
// is nothing to address. The input order is preserved.
func FilterReviewThreads(threads []ReviewThread, includeResolved bool) []ReviewThread {
	var out []ReviewThread
	for _, t := range threads {
		if len(t.Comments) == 0 {
			continue
		}
		if t.IsResolved && !includeResolved {
			continue
		}
		if strings.Contains(t.Comments[0].Body, reviewSignature) {
			continue
		}
		out = append(out, t)
	}
	return out
}

// SubmitPRReview creates a Pull Request Review with inline comments
// using the GitHub REST API via gh.
func SubmitPRReview(owner, repo string, number int, commitSHA string, body string, comments []ReviewComment) (string, error) {
	fullBody := body + "\n" + reviewSignature

	req := ReviewRequest{
		Body:     truncateComment(fullBody),
		Event:    "COMMENT",
		CommitID: commitSHA,
		Comments: comments,
	}

	payload, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("marshaling review request: %w", err)
	}

	endpoint := fmt.Sprintf("repos/%s/%s/pulls/%d/reviews", owner, repo, number)
	ctx, cancel := context.WithTimeout(context.Background(), ghTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", "api", endpoint,
		"--method", "POST",
		"--input", "-",
		"-H", "Accept: application/vnd.github.v3+json",
	)
	cmd.Stdin = bytes.NewReader(payload)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("gh api create review: %s: %w", strings.TrimSpace(string(out)), err)
	}

	var resp struct {
		HTMLURL string `json:"html_url"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		return strings.TrimSpace(string(out)), nil
	}
	return resp.HTMLURL, nil
}
