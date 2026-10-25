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
	StartLine int    `json:"start_line,omitempty"` // for multi-line comments
	StartSide string `json:"start_side,omitempty"` // "RIGHT" when StartLine is set
	Body      string `json:"body"`
}

// ReviewRequest represents the payload for creating a PR review.
type ReviewRequest struct {
	Body     string          `json:"body"`
	Event    string          `json:"event"` // "COMMENT"
	CommitID string          `json:"commit_id"`
	Comments []ReviewComment `json:"comments"`
}

const reviewSignature = "<!-- planwerk-agent-inline -->"

// botTypename is the GraphQL __typename of an actor that is an app's bot
// account, such as the github-actions account an Actions token posts as.
const botTypename = "Bot"

// ReviewThreadComment is a single comment within a PR review thread, flattened
// for the address command: the author login, the comment body, and when it was
// created. The full chain is what Claude needs to understand the reviewer's ask.
type ReviewThreadComment struct {
	Author string
	// AuthorIsBot is true when GitHub types the author as a Bot, as it does the
	// github-actions account an Actions token posts under. Only an app can post
	// as one.
	AuthorIsBot bool
	Body        string
	CreatedAt   string
	// ReviewID is the node ID of the pull request review the comment was
	// posted in. It is empty when GitHub names no review.
	ReviewID string
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
	// ResolvedBy is the login of the user who resolved the thread. It is empty
	// when the thread is unresolved or GitHub names no user, as for a bot or a
	// deleted account.
	ResolvedBy string
	Path       string
	Line       int
	// OriginalLine and OriginalStartLine are the thread's last and first line
	// on the commit its first comment was posted at, which stay put when a push
	// makes the thread outdated. Either is 0 when GitHub returns null, as it
	// does for the start line of a single-line thread.
	OriginalLine      int
	OriginalStartLine int
	DiffHunk          string
	// CommentCount is the number of comments the thread holds. Comments carries
	// at most the first 100 of them, so a caller that must see the whole chain
	// compares the two.
	CommentCount int
	Comments     []ReviewThreadComment
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
						// ResolvedBy stays zero for a JSON null.
						ResolvedBy struct {
							Login string `json:"login"`
						} `json:"resolvedBy"`
						// OriginalLine and OriginalStartLine stay 0 for a JSON
						// null.
						OriginalLine      int `json:"originalLine"`
						OriginalStartLine int `json:"originalStartLine"`
						Comments          struct {
							TotalCount int `json:"totalCount"`
							Nodes      []struct {
								Author struct {
									Login    string `json:"login"`
									Typename string `json:"__typename"`
								} `json:"author"`
								Body      string `json:"body"`
								CreatedAt string `json:"createdAt"`
								Path      string `json:"path"`
								Line      int    `json:"line"`
								DiffHunk  string `json:"diffHunk"`
								// PullRequestReview stays zero for a JSON null.
								PullRequestReview struct {
									ID string `json:"id"`
								} `json:"pullRequestReview"`
							} `json:"nodes"`
						} `json:"comments"`
					} `json:"nodes"`
				} `json:"reviewThreads"`
			} `json:"pullRequest"`
		} `json:"repository"`
	} `json:"data"`
}

// reviewThreadsQuery pages a PR's review threads, carrying each thread's
// resolved/outdated status, the login of the user who resolved it, its lines on
// the commit it was posted at, its comment count, and its full comment chain
// (with the author's type, the anchored file, line, and diff hunk, and the
// review it was posted in, on each comment).
const reviewThreadsQuery = `query($owner: String!, $name: String!, $number: Int!, $cursor: String) {
  repository(owner: $owner, name: $name) {
    pullRequest(number: $number) {
      reviewThreads(first: 100, after: $cursor) {
        pageInfo { hasNextPage endCursor }
        nodes {
          id
          isResolved
          isOutdated
          resolvedBy { login }
          originalLine
          originalStartLine
          comments(first: 100) {
            totalCount
            nodes { author { login __typename } body createdAt path line diffHunk pullRequestReview { id } }
          }
        }
      }
    }
  }
}`

// FetchReviewThreads returns every review thread on the PR via the GitHub
// GraphQL API, following pagination so a PR with more than 100 threads is fully
// covered. Each thread carries its resolved/outdated status, who resolved it,
// its original lines and comment count, the first 100 comments of its chain
// with the review each was posted in and whether a bot wrote it, and the
// file/line/diff-hunk the thread is anchored to. Use FilterReviewThreads to
// drop resolved threads and the tool's own findings, and IsOwnThread to keep
// only the latter.
func (Client) FetchReviewThreads(owner, repo string, number int) ([]ReviewThread, error) {
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
	// owner/name go through -f (verbatim string): -F type-coerces its value, so
	// a repository literally named "2048", "404" or "null" would reach GraphQL
	// as a number or null and be rejected against String!.
	args := []string{"api", "graphql",
		"-f", "owner=" + owner,
		"-f", "name=" + repo,
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
			ID:                n.ID,
			IsResolved:        n.IsResolved,
			IsOutdated:        n.IsOutdated,
			ResolvedBy:        n.ResolvedBy.Login,
			OriginalLine:      n.OriginalLine,
			OriginalStartLine: n.OriginalStartLine,
			CommentCount:      n.Comments.TotalCount,
		}
		for _, c := range n.Comments.Nodes {
			t.Comments = append(t.Comments, ReviewThreadComment{
				Author:      c.Author.Login,
				AuthorIsBot: c.Author.Typename == botTypename,
				Body:        c.Body,
				CreatedAt:   c.CreatedAt,
				ReviewID:    c.PullRequestReview.ID,
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
// carries planwerk-agent's inline signature, so address never tries to address
// the tool's own findings. SubmitPRReview stamps that signature on every inline
// comment it posts, which is what makes this filter fire. Threads with no
// comments are dropped as well — there is nothing to address. The input order
// is preserved.
func FilterReviewThreads(threads []ReviewThread, includeResolved bool) []ReviewThread {
	var out []ReviewThread
	for _, t := range threads {
		if len(t.Comments) == 0 {
			continue
		}
		if t.IsResolved && !includeResolved {
			continue
		}
		if IsOwnThread(t) {
			continue
		}
		out = append(out, t)
	}
	return out
}

// IsOwnThread reports whether t is one of planwerk-agent's inline findings: its
// first comment carries the inline signature signReviewComments stamps on every
// inline comment. A signature in a reply does not count, and a thread with no
// comments is not one.
func IsOwnThread(t ReviewThread) bool {
	return len(t.Comments) > 0 && strings.Contains(t.Comments[0].Body, reviewSignature)
}

// integrationRefusal is the message GitHub refuses a request with when it
// comes from a GitHub App's token, an Actions token among them.
const integrationRefusal = "Resource not accessible by integration"

// ErrViewerRefused is wrapped by ViewerLogin's error when GitHub refused the
// lookup with integrationRefusal, which tells the caller the token is an app's
// even though its login is unread. Any other failure says nothing about the
// token. Exported so the review runner can match against it with errors.Is,
// like ErrHeadMismatch (decision 133).
var ErrViewerRefused = errors.New("login lookup refused to an app token")

// ViewerLogin returns the login gh is authenticated as, read from the GraphQL
// viewer. An app's installation token, such as an Actions token, may be
// refused the lookup; the error then wraps ErrViewerRefused. Callers must
// treat any other error as an unknown identity, which may be a personal
// token's, and trust nothing on its account.
func (Client) ViewerLogin() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), ghTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", "api", "graphql",
		"-f", "query=query { viewer { login } }",
		"--jq", ".data.viewer.login",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if strings.Contains(msg, integrationRefusal) {
			return "", fmt.Errorf("gh api graphql (viewer): %s: %w: %w", msg, ErrViewerRefused, err)
		}
		return "", fmt.Errorf("gh api graphql (viewer): %s: %w", msg, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// signReviewComments returns a copy of comments with planwerk-agent's inline
// signature appended to every body, leaving the input untouched. Splitting it
// out of SubmitPRReview keeps the marker that FilterReviewThreads depends on
// verifiable without a network call.
func signReviewComments(comments []ReviewComment) []ReviewComment {
	if len(comments) == 0 {
		return comments
	}
	signed := make([]ReviewComment, len(comments))
	for i, c := range comments {
		c.Body = truncateComment(c.Body + "\n" + reviewSignature)
		signed[i] = c
	}
	return signed
}

// SubmitPRReview creates a Pull Request Review with inline comments
// using the GitHub REST API via gh.
//
// The review pipeline posts the summary through PostPRComment and calls this
// only for a review with inline comments, or with the summary itself when that
// comment failed (decision 133).
//
// The signature goes on the review body AND on every inline comment. Only the
// latter is required: GitHub exposes an inline comment as a review thread
// but never hands the review body along with it, so FilterReviewThreads — which
// reads a thread's first comment — can recognize the tool's own findings only
// if the marker travels with the comment. Without it, `address` offers every
// finding this review just posted back as if a human had written it. The marker
// is an HTML comment, so it stays invisible in the GitHub UI.
func (Client) SubmitPRReview(owner, repo string, number int, commitSHA string, body string, comments []ReviewComment) (string, error) {
	fullBody := body + "\n" + reviewSignature

	req := ReviewRequest{
		Body:     truncateComment(fullBody),
		Event:    "COMMENT",
		CommitID: commitSHA,
		Comments: signReviewComments(comments),
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
