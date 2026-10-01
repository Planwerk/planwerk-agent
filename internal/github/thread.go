package github

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// ThreadComment is one comment on an issue or a pull request as the history
// readers return it: who wrote it, their relation to the repository as GitHub
// reports it, when, and the text.
type ThreadComment struct {
	Author            string
	AuthorAssociation string
	CreatedAt         string
	Body              string
}

// IssueThread is an issue with its whole conversation. Unlike
// ListIssueComments, GetIssueThread keeps every comment, whoever wrote it: the
// caller analyzes the text as data and never acts on what a comment says.
type IssueThread struct {
	Number   int
	Title    string
	Body     string
	Author   string
	Comments []ThreadComment
}

// PRReview is one submitted review of a pull request: the reviewer, when it
// was submitted, and the summary text.
type PRReview struct {
	Author      string
	SubmittedAt string
	Body        string
}

// PRCommit is one commit of a pull request.
type PRCommit struct {
	SHA      string
	Headline string
	Body     string
}

// PRThread is a pull request with its whole conversation: the comments, the
// reviews, the commits, and the inline review threads. Like IssueThread it
// keeps text from every author.
type PRThread struct {
	Number        int
	Title         string
	Body          string
	Author        string
	Comments      []ThreadComment
	Reviews       []PRReview
	Commits       []PRCommit
	ReviewThreads []ReviewThread
}

// ghAuthor is the author object gh prints for an issue, a pull request, a
// comment, and a review.
type ghAuthor struct {
	Login string `json:"login"`
}

// ghThreadComment is one entry of the comments array gh prints.
type ghThreadComment struct {
	Author            ghAuthor `json:"author"`
	AuthorAssociation string   `json:"authorAssociation"`
	CreatedAt         string   `json:"createdAt"`
	Body              string   `json:"body"`
}

// threadComments flattens gh's comments into ThreadComments, in order.
func threadComments(in []ghThreadComment) []ThreadComment {
	var out []ThreadComment
	for _, c := range in {
		out = append(out, ThreadComment{Author: c.Author.Login, AuthorAssociation: c.AuthorAssociation, CreatedAt: c.CreatedAt, Body: c.Body})
	}
	return out
}

// GetIssueThread fetches an issue with every comment on it via gh.
func (Client) GetIssueThread(owner, name string, number int) (*IssueThread, error) {
	ctx, cancel := context.WithTimeout(context.Background(), ghTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", "issue", "view", strconv.Itoa(number),
		"--repo", owner+"/"+name,
		"--json", "number,title,body,author,comments")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("gh issue view: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return parseIssueThread(out)
}

// parseIssueThread decodes the JSON GetIssueThread asks gh for. It is pure so
// the mapping is unit-testable without gh.
func parseIssueThread(out []byte) (*IssueThread, error) {
	var raw struct {
		Number   int               `json:"number"`
		Title    string            `json:"title"`
		Body     string            `json:"body"`
		Author   ghAuthor          `json:"author"`
		Comments []ghThreadComment `json:"comments"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("parsing gh issue view output: %w", err)
	}
	return &IssueThread{
		Number:   raw.Number,
		Title:    raw.Title,
		Body:     raw.Body,
		Author:   raw.Author.Login,
		Comments: threadComments(raw.Comments),
	}, nil
}

// GetPRThread fetches a pull request with every comment, review, and commit
// via gh, and its inline review threads through FetchReviewThreads, whose
// error it returns unchanged.
func (c Client) GetPRThread(owner, name string, number int) (*PRThread, error) {
	ctx, cancel := context.WithTimeout(context.Background(), ghTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", "pr", "view", strconv.Itoa(number),
		"--repo", owner+"/"+name,
		"--json", "number,title,body,author,comments,reviews,commits")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("gh pr view: %s: %w", strings.TrimSpace(string(out)), err)
	}
	pr, err := parsePRThread(out)
	if err != nil {
		return nil, err
	}
	threads, err := c.FetchReviewThreads(owner, name, number)
	if err != nil {
		return nil, err
	}
	pr.ReviewThreads = threads
	return pr, nil
}

// parsePRThread decodes the JSON GetPRThread asks gh for, without the review
// threads. It is pure so the mapping is unit-testable without gh.
func parsePRThread(out []byte) (*PRThread, error) {
	var raw struct {
		Number   int               `json:"number"`
		Title    string            `json:"title"`
		Body     string            `json:"body"`
		Author   ghAuthor          `json:"author"`
		Comments []ghThreadComment `json:"comments"`
		Reviews  []struct {
			Author      ghAuthor `json:"author"`
			SubmittedAt string   `json:"submittedAt"`
			Body        string   `json:"body"`
		} `json:"reviews"`
		Commits []struct {
			OID             string `json:"oid"`
			MessageHeadline string `json:"messageHeadline"`
			MessageBody     string `json:"messageBody"`
		} `json:"commits"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("parsing gh pr view output: %w", err)
	}
	pr := &PRThread{
		Number:   raw.Number,
		Title:    raw.Title,
		Body:     raw.Body,
		Author:   raw.Author.Login,
		Comments: threadComments(raw.Comments),
	}
	for _, r := range raw.Reviews {
		pr.Reviews = append(pr.Reviews, PRReview{Author: r.Author.Login, SubmittedAt: r.SubmittedAt, Body: r.Body})
	}
	for _, c := range raw.Commits {
		pr.Commits = append(pr.Commits, PRCommit{SHA: c.OID, Headline: c.MessageHeadline, Body: c.MessageBody})
	}
	return pr, nil
}
