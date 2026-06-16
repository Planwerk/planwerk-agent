package github

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	// maxCommentLen is the GitHub API limit for issue/PR comment bodies.
	maxCommentLen = 65536
	// commentSignature is appended to comments so we can detect duplicates.
	commentSignature = "<!-- planwerk-review -->"
)

// truncationNotice is appended when a comment body is cut to fit within
// maxCommentLen. The %d is filled in with maxCommentLen at format time.
var truncationNotice = fmt.Sprintf(
	"\n\n---\n*Review truncated due to GitHub comment size limit (%d characters).*\n",
	maxCommentLen,
)

// PostPRComment posts a comment on a GitHub pull request via the gh CLI.
// It detects and replaces any previous planwerk-review comment on the same PR.
// Bodies exceeding maxCommentLen are truncated.
func PostPRComment(owner, repo string, number int, body string) (string, error) {
	fullName := fmt.Sprintf("%s/%s", owner, repo)
	body = truncateComment(body + "\n" + commentSignature)

	// Check for an existing planwerk-review comment to update.
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
// quoting. Unlike PostPRComment it carries no planwerk-review signature and
// never replaces a prior comment: each call appends a fresh comment. The fix
// loop uses it to record one comment per pushed fix iteration, so the history
// of what each follow-up commit changed survives on the PR. (`gh issue
// comment` rejects PR numbers, so the fix path cannot reuse AddIssueComment.)
func AddPRComment(owner, repo string, number int, body string) (string, error) {
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
func AddReviewThreadReply(threadID, body string) (string, error) {
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
func ResolveReviewThread(threadID string) error {
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

type ghComment struct {
	ID   string `json:"id"`
	Body string `json:"body"`
}

// findExistingComment returns the node ID of the prior planwerk-review comment
// on the PR, or "" when none exists.
func findExistingComment(repo string, number int) (string, error) {
	c, err := fetchExistingComment(repo, number)
	if err != nil {
		return "", err
	}
	return c.ID, nil
}

// FetchReviewComment returns the body of the most recent planwerk-review
// comment on the PR. found is false when no such comment exists. It lets the
// review pipeline read the data block from the previous review.
func FetchReviewComment(owner, repo string, number int) (body string, found bool, err error) {
	c, err := fetchExistingComment(fmt.Sprintf("%s/%s", owner, repo), number)
	if err != nil {
		return "", false, err
	}
	if c.ID == "" {
		return "", false, nil
	}
	return c.Body, true, nil
}

// fetchExistingComment returns the most recent planwerk-review comment (id and
// body) on the PR, or a zero ghComment when none is found.
func fetchExistingComment(repo string, number int) (ghComment, error) {
	args := []string{"pr", "view", strconv.Itoa(number),
		"--repo", repo,
		"--json", "comments",
		"--jq", `.comments[] | select(.body | contains("` + commentSignature + `")) | {id, body}`,
	}

	ctx, cancel := context.WithTimeout(context.Background(), ghTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return ghComment{}, fmt.Errorf("gh pr view comments: %s: %w", strings.TrimSpace(string(out)), err)
	}

	output := strings.TrimSpace(string(out))
	if output == "" {
		return ghComment{}, nil
	}

	// Take the last matching comment (most recent).
	var last ghComment
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var c ghComment
		if err := json.Unmarshal([]byte(line), &c); err != nil {
			continue
		}
		last = c
	}

	return last, nil
}

// editComment updates an existing comment by its node ID.
// Uses GraphQL variables to avoid injection via commentID or body.
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

	return strings.TrimSpace(string(out)), nil
}
