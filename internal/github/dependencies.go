package github

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// AddIssueDependency records that blockedNumber is "blocked by" blockerNumber
// using GitHub's native issue-dependency relationship, so the dependency renders
// in GitHub's issue UI and gates the blocked issue there too. Like AddSubIssue it
// is a two-step call: the REST endpoint keys the blocked issue by its number but
// identifies the blocker by its integer database id, so the blocker's id is
// resolved first.
//
// See https://docs.github.com/en/rest/issues/dependencies —
// POST /repos/{owner}/{repo}/issues/{issue_number}/dependencies/blocked_by with
// an issue_id body field carrying the blocker's database id.
func AddIssueDependency(owner, name string, blockedNumber, blockerNumber int) error {
	blockerID, err := issueDatabaseID(owner, name, blockerNumber)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), ghTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", addIssueDependencyArgs(owner, name, blockedNumber, blockerID)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("gh api dependencies/blocked_by: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

// addIssueDependencyArgs builds the gh argv that POSTs a blocker issue's database
// id to the blocked issue's dependencies/blocked_by endpoint. The id is passed
// with -F (not -f) so gh serializes it as a JSON number, which the endpoint
// requires. Kept separate so the argument assembly is unit-testable without
// invoking gh.
func addIssueDependencyArgs(owner, name string, blockedNumber, blockerID int) []string {
	return []string{"api",
		"--method", "POST",
		fmt.Sprintf("repos/%s/%s/issues/%d/dependencies/blocked_by", owner, name, blockedNumber),
		"-F", fmt.Sprintf("issue_id=%d", blockerID),
	}
}

// BlockedByIssues lists the issues that block the given issue via GitHub's native
// issue-dependency relationship. Callers treat a returned error as best-effort: a
// repo whose GitHub does not expose issue dependencies (an older GHES, or the
// feature disabled) surfaces here and should degrade to "no dependencies" rather
// than abort the run, consistent with how GetIssueRelations already degrades.
//
// See https://docs.github.com/en/rest/issues/dependencies —
// GET /repos/{owner}/{repo}/issues/{issue_number}/dependencies/blocked_by.
func BlockedByIssues(owner, name string, number int) ([]Issue, error) {
	ctx, cancel := context.WithTimeout(context.Background(), ghTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", blockedByIssuesArgs(owner, name, number)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("gh api dependencies/blocked_by: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return parseBlockedByIssues(out, owner, name)
}

// blockedByIssuesArgs builds the gh argv that GETs the issues blocking the given
// issue. Kept separate so the argument assembly is unit-testable without invoking
// gh.
func blockedByIssuesArgs(owner, name string, number int) []string {
	return []string{"api",
		fmt.Sprintf("repos/%s/%s/issues/%d/dependencies/blocked_by", owner, name, number),
	}
}

// parseBlockedByIssues decodes the REST array the blocked_by endpoint returns
// into []Issue, stamping the repo coordinates and lowercasing the state enum to
// match GetIssue's convention. Kept separate from BlockedByIssues so the decode
// is unit-testable without invoking gh.
func parseBlockedByIssues(out []byte, owner, name string) ([]Issue, error) {
	var raw []struct {
		Number int    `json:"number"`
		Title  string `json:"title"`
		URL    string `json:"html_url"`
		State  string `json:"state"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("parsing gh api dependencies/blocked_by output: %w", err)
	}
	var issues []Issue
	for _, r := range raw {
		issues = append(issues, Issue{
			Owner:  owner,
			Name:   name,
			Number: r.Number,
			Title:  r.Title,
			URL:    r.URL,
			State:  strings.ToLower(r.State),
		})
	}
	return issues, nil
}
