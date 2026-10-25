package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// PostBestEffort posts a comment through post and reports the outcome on w
// without failing the caller: the artifact it carries is already on stdout or
// pushed, so a GitHub error is logged and shown, never returned. what names
// the artifact ("fix report"); target names where it lands ("PR #7",
// "issue #42"), or is empty when the caller has no number to show.
func PostBestEffort(w io.Writer, what, target string, post func() (string, error)) {
	on := ""
	if target != "" {
		on = " on " + target
	}
	url, err := post()
	if err != nil {
		slog.Warn("posting comment failed", "what", what, "target", target, "err", err)
		_, _ = fmt.Fprintf(w, "\nCould not post the %s as a comment%s: %v\n", what, on, err)
		return
	}
	slog.Info("posted comment", "what", what, "target", target, "url", url)
	_, _ = fmt.Fprintf(w, "\nPosted the %s as a comment%s", what, on)
	if url != "" {
		_, _ = fmt.Fprintf(w, " (%s)", url)
	}
	_, _ = fmt.Fprintln(w)
}

const (
	// maxCommentLen is the GitHub API limit for issue/PR comment bodies, the
	// same cap MaxIssueBodyLen names for issue bodies.
	maxCommentLen = MaxIssueBodyLen
	// commentSignature is appended to comments so we can detect duplicates.
	commentSignature = "<!-- planwerk-agent -->"
)

// truncationNotice is appended when a comment body is cut to fit within
// maxCommentLen. The %d is filled in with maxCommentLen at format time.
var truncationNotice = fmt.Sprintf(
	"\n\n---\n*Review truncated due to GitHub comment size limit (%d characters).*\n",
	maxCommentLen,
)

// PostPRComment posts a comment on a GitHub pull request via the gh CLI and
// returns its URL. It replaces the most recent comment on the PR that opens
// with reportHeading, carries commentSignature, and that the authenticated gh
// user wrote, and creates one when there is none, so a PR holds one such
// comment however often it is reviewed (decision 12). Bodies exceeding
// maxCommentLen are truncated.
//
// That comment is the review summary of both --post-review and --inline, with
// the data block a re-review reads (decision 133), so body must be the
// Markdown report: one that opens with anything else is never found again,
// and each call would post a new comment. PostPRComment is the only
// poster that signs with commentSignature: a second one would overwrite the
// summary and its data block, which is why the capture pass posts through
// AddPRComment.
func (Client) PostPRComment(owner, repo string, number int, body string) (string, error) {
	fullName := fmt.Sprintf("%s/%s", owner, repo)
	body = truncateComment(body + "\n" + commentSignature)

	// Check for an existing planwerk-agent comment to update.
	existingID, err := findExistingComment(fullName, number)
	if err != nil {
		return "", fmt.Errorf("checking existing comments: %w", err)
	}

	if existingID != "" {
		return editComment(fullName, existingID, body)
	}

	args := []string{"pr", "comment", strconv.Itoa(number),
		"--repo", fullName,
		"--body", body,
	}

	ctx, cancel := context.WithTimeout(context.Background(), ghTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("gh pr comment: %s: %w", strings.TrimSpace(string(out)), err)
	}

	return strings.TrimSpace(string(out)), nil
}

// AddPRComment posts a NEW comment on a pull request via the gh CLI, passing
// the body on stdin so it is not subject to argv length limits or shell
// quoting. Unlike PostPRComment it carries no planwerk-agent signature and
// never replaces a prior comment: each call appends a fresh comment. The fix
// loop uses it to record one comment per pushed fix iteration, so the history
// of what each follow-up commit changed survives on the PR, and the review's
// capture pass posts its proposals with it, beside the summary comment. (`gh
// issue comment` rejects PR numbers, so the fix path cannot reuse
// AddIssueComment.)
func (Client) AddPRComment(owner, repo string, number int, body string) (string, error) {
	fullName := fmt.Sprintf("%s/%s", owner, repo)
	ctx, cancel := context.WithTimeout(context.Background(), ghTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", "pr", "comment", strconv.Itoa(number),
		"--repo", fullName,
		"--body-file", "-")
	cmd.Stdin = strings.NewReader(body)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("gh pr comment: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return strings.TrimSpace(string(out)), nil
}

// AddReviewThreadReply posts a reply onto an existing PR review thread via the
// GraphQL addPullRequestReviewThreadReply mutation and returns the new comment's
// URL. The address command uses it to record, per thread, what the follow-up
// commit changed. Uses GraphQL variables so the thread ID and body cannot inject.
func (Client) AddReviewThreadReply(threadID, body string) (string, error) {
	query := `mutation($threadId: ID!, $body: String!) { addPullRequestReviewThreadReply(input: {pullRequestReviewThreadId: $threadId, body: $body}) { comment { url } } }`

	ctx, cancel := context.WithTimeout(context.Background(), ghTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", "api", "graphql",
		"-f", "query="+query,
		"-f", "threadId="+threadID,
		"-f", "body="+body,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("gh api graphql (reply to review thread): %s: %w", strings.TrimSpace(string(out)), err)
	}

	var resp struct {
		Data struct {
			AddPullRequestReviewThreadReply struct {
				Comment struct {
					URL string `json:"url"`
				} `json:"comment"`
			} `json:"addPullRequestReviewThreadReply"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		return strings.TrimSpace(string(out)), nil
	}
	return resp.Data.AddPullRequestReviewThreadReply.Comment.URL, nil
}

// ResolveReviewThread marks a PR review thread resolved via the GraphQL
// resolveReviewThread mutation. The address command calls it only under
// --resolve, since resolving is outward-facing. Uses a GraphQL variable for the
// thread ID so it cannot inject.
func (Client) ResolveReviewThread(threadID string) error {
	query := `mutation($threadId: ID!) { resolveReviewThread(input: {threadId: $threadId}) { thread { isResolved } } }`

	ctx, cancel := context.WithTimeout(context.Background(), ghTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", "api", "graphql",
		"-f", "query="+query,
		"-f", "threadId="+threadID,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("gh api graphql (resolve review thread): %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

// truncateComment ensures body does not exceed GitHub's comment size limit.
// It avoids splitting multi-byte UTF-8 characters at the cut point.
func truncateComment(body string) string {
	if len(body) <= maxCommentLen {
		return body
	}
	// Reserve space for truncation notice + signature
	suffix := truncationNotice + commentSignature
	cutPoint := maxCommentLen - len(suffix)
	// Avoid splitting a multi-byte UTF-8 character: walk back to a valid boundary.
	for cutPoint > 0 && !utf8.RuneStart(body[cutPoint]) {
		cutPoint--
	}
	return body[:cutPoint] + suffix
}

// ghComment is a PR comment as `gh pr view --json comments` returns it, with
// the fields fetchExistingComment selects on.
type ghComment struct {
	ID              string `json:"id"`
	Body            string `json:"body"`
	ViewerDidAuthor bool   `json:"viewerDidAuthor"`
}

// findExistingComment returns the node ID of the prior planwerk-agent comment
// on the PR that the authenticated gh user wrote, or "" when none exists.
func findExistingComment(repo string, number int) (string, error) {
	c, err := fetchExistingComment(repo, number)
	if err != nil {
		return "", err
	}
	return c.ID, nil
}

// dataBlockMarker opens the machine-readable block report.RenderDataBlock
// appends to every posted review and report.ParseDataBlock reads back. A post
// without it carries no findings a re-review could compare against.
const dataBlockMarker = "<!-- planwerk-agent-data"

// reportHeading opens every body this tool posts with a data block: the body
// is the Markdown report, whose first line report.RenderMarkdown writes as
// "# Review: <owner>/<repo>#<number>" (--post-review and --inline refuse
// --format json). A post that opens with anything else is not one of them,
// whoever wrote it (decision 128).
const reportHeading = "# Review: "

// priorDataBlockQuery reads the last 100 comments and the last 100 reviews of a
// PR with the fields latestDataBlockBody selects on: the body, when it last
// changed, and who wrote it.
const priorDataBlockQuery = `query($owner: String!, $name: String!, $number: Int!) {
  repository(owner: $owner, name: $name) {
    pullRequest(number: $number) {
      comments(last: 100) {
        nodes { id body updatedAt viewerDidAuthor authorAssociation }
      }
      reviews(last: 100) {
        nodes { id body updatedAt viewerDidAuthor authorAssociation }
      }
    }
  }
}`

// postedBody is a PR comment or review as priorDataBlockQuery returns it. A
// review carries the same author fields as a comment, so IssueComment.Trusted
// applies to both; the embedded fields decode from the node's top level.
type postedBody struct {
	IssueComment
	UpdatedAt time.Time `json:"updatedAt"`
}

// carriesDataBlock reports whether body is a signed planwerk-agent post that
// carries a data block: it opens with reportHeading and holds a signature and
// the marker. The capture pass's proposals comment carries none; it was signed
// while the pass posted it through PostPRComment.
func carriesDataBlock(body string) bool {
	return strings.HasPrefix(body, reportHeading) &&
		strings.Contains(body, dataBlockMarker) &&
		(strings.Contains(body, commentSignature) || strings.Contains(body, reviewSignature))
}

// FetchReviewComment returns the body of the most recently updated
// planwerk-agent post on the PR that carries a data block: an inline review's
// body (SubmitPRReview) or the summary comment (PostPRComment), whichever
// changed last. The review pipeline reads the previous review's findings from
// it, and those decide what a re-review leaves out, so only a post the
// authenticated user or a maintainer wrote counts (IssueComment.Trusted,
// decisions 97 and 128). A signed post without a data block, such as the
// capture pass's proposals comment, is passed over, and so is one that does not
// open with reportHeading. found is false when no post qualifies. It reads the
// last 100 comments and the last 100 reviews, so a summary comment with more
// than 100 newer comments after it is missed.
//
// The author check vouches for the account, not for this tool: viewerDidAuthor
// holds for every post the authenticated account made, and under an Actions
// GITHUB_TOKEN that account is github-actions[bot], which every workflow in the
// repository posts as. The heading keeps out such a post that puts text of its
// own ahead of the PR text it copies, not a verbatim copy (decision 128).
//
// Nor does the check vouch for all of the post's text: the body quotes the PR
// title and the findings, which the PR's author controls. Read the block with
// report.ParseDataBlock, which takes only the one the post appended.
func (Client) FetchReviewComment(owner, repo string, number int) (body string, found bool, err error) {
	raw, err := fetchPriorDataBlockPosts(owner, repo, number)
	if err != nil {
		return "", false, err
	}
	body, found, err = latestDataBlockBody(raw)
	if err != nil {
		return "", false, fmt.Errorf("parsing comments and reviews of %s/%s#%d: %w", owner, repo, number, err)
	}
	return body, found, nil
}

// fetchPriorDataBlockPosts runs priorDataBlockQuery for the PR and returns the
// raw response.
func fetchPriorDataBlockPosts(owner, repo string, number int) ([]byte, error) {
	// owner/name go through -f (verbatim string) for the reason
	// fetchReviewThreadsPage gives.
	args := []string{"api", "graphql",
		"-f", "owner=" + owner,
		"-f", "name=" + repo,
		"-F", fmt.Sprintf("number=%d", number),
		"-f", "query=" + priorDataBlockQuery,
	}
	ctx, cancel := context.WithTimeout(context.Background(), ghTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", args...)
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return nil, fmt.Errorf("gh api graphql (comments and reviews): %w: %s", err, strings.TrimSpace(string(exitErr.Stderr)))
		}
		return nil, fmt.Errorf("gh api graphql (comments and reviews): %w", err)
	}
	return out, nil
}

// latestDataBlockBody decodes a priorDataBlockQuery response and returns the
// body of the most recently updated post that carriesDataBlock and
// IssueComment.Trusted accept; found is false when none does. On equal update
// times a review beats a comment, and a later node an earlier one. It is pure
// so the selection is unit-testable without gh.
func latestDataBlockBody(raw []byte) (body string, found bool, err error) {
	var resp struct {
		Data struct {
			Repository struct {
				PullRequest struct {
					Comments struct {
						Nodes []postedBody `json:"nodes"`
					} `json:"comments"`
					Reviews struct {
						Nodes []postedBody `json:"nodes"`
					} `json:"reviews"`
				} `json:"pullRequest"`
			} `json:"repository"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return "", false, fmt.Errorf("decoding comments-and-reviews response: %w", err)
	}
	pr := resp.Data.Repository.PullRequest
	var latest postedBody
	untrusted := 0
	for _, p := range slices.Concat(pr.Comments.Nodes, pr.Reviews.Nodes) {
		if !carriesDataBlock(p.Body) {
			continue
		}
		if !p.Trusted() {
			untrusted++
			continue
		}
		if !found || !p.UpdatedAt.Before(latest.UpdatedAt) {
			latest = p
			found = true
		}
	}
	if untrusted > 0 {
		slog.Info("ignoring planwerk-agent data blocks from outside the repository's maintainers", "count", untrusted)
	}
	return latest.Body, found, nil
}

// fetchExistingComment is PostPRComment's lookup, through findExistingComment,
// of the comment it replaces: the most recent comment on the PR that opens with
// reportHeading, carries commentSignature, and that the authenticated gh user
// wrote, or a zero ghComment when none is found. A signed comment from any
// other account is passed over, so the summary is never written into a comment
// someone else controls, and so is one of the account's own that opens with
// other text, as another workflow's comment under the same Actions token can;
// FetchReviewComment applies the same checks to the data block it reads back
// (decisions 128 and 133).
func fetchExistingComment(repo string, number int) (ghComment, error) {
	args := []string{"pr", "view", strconv.Itoa(number),
		"--repo", repo,
		"--json", "comments",
	}

	ctx, cancel := context.WithTimeout(context.Background(), ghTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", args...)
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return ghComment{}, fmt.Errorf("gh pr view comments: %s: %w", strings.TrimSpace(string(exitErr.Stderr)), err)
		}
		return ghComment{}, fmt.Errorf("gh pr view comments: %w", err)
	}
	c, err := latestOwnSignedComment(out)
	if err != nil {
		return ghComment{}, fmt.Errorf("parsing comments of %s#%d: %w", repo, number, err)
	}
	return c, nil
}

// latestOwnSignedComment decodes a `gh pr view --json comments` response and
// returns the last comment, the most recent one, that opens with reportHeading,
// carries commentSignature, and that the authenticated gh user wrote, or a zero
// ghComment when none does. It is pure so the selection is unit-testable
// without gh.
func latestOwnSignedComment(raw []byte) (ghComment, error) {
	var resp struct {
		Comments []ghComment `json:"comments"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return ghComment{}, fmt.Errorf("decoding comments: %w", err)
	}
	var last ghComment
	for _, c := range resp.Comments {
		if c.ViewerDidAuthor && strings.HasPrefix(c.Body, reportHeading) && strings.Contains(c.Body, commentSignature) {
			last = c
		}
	}
	return last, nil
}

// editComment updates an existing comment by its node ID and returns the
// comment's URL (parseUpdatedCommentURL). Uses GraphQL variables to avoid
// injection via commentID or body.
func editComment(repo, commentID, body string) (string, error) {
	query := `mutation($id: ID!, $body: String!) { updateIssueComment(input: {id: $id, body: $body}) { issueComment { url } } }`

	ctx, cancel := context.WithTimeout(context.Background(), ghTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", "api", "graphql",
		"-f", "query="+query,
		"-f", "id="+commentID,
		"-f", "body="+body,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("gh api graphql (update comment): %s: %w", strings.TrimSpace(string(out)), err)
	}

	return parseUpdatedCommentURL(out), nil
}

// parseUpdatedCommentURL returns the comment URL of an updateIssueComment
// response, so an edited summary comment reports a link as a created one does.
// It falls back to the trimmed raw output when that does not decode or names
// no URL.
func parseUpdatedCommentURL(out []byte) string {
	var resp struct {
		Data struct {
			UpdateIssueComment struct {
				IssueComment struct {
					URL string `json:"url"`
				} `json:"issueComment"`
			} `json:"updateIssueComment"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out, &resp); err != nil || resp.Data.UpdateIssueComment.IssueComment.URL == "" {
		return strings.TrimSpace(string(out))
	}
	return resp.Data.UpdateIssueComment.IssueComment.URL
}
