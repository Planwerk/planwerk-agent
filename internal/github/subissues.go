package github

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// AddSubIssue links a child issue to a parent issue using GitHub's native
// sub-issue relationship so the child appears under the parent's sub-issue
// list. It is a two-step call: the REST sub-issues endpoint keys the parent by
// its issue number but identifies the child by its integer database id, not its
// number, so the child's id is resolved first.
//
// See https://docs.github.com/en/rest/issues/sub-issues —
// POST /repos/{owner}/{repo}/issues/{issue_number}/sub_issues with a
// sub_issue_id body field carrying the child's database id.
func AddSubIssue(owner, name string, parentNumber, childNumber int) error {
	childID, err := childIssueDatabaseID(owner, name, childNumber)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), ghTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", addSubIssueArgs(owner, name, parentNumber, childID)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("gh api sub_issues: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

// childIssueDatabaseID resolves the integer database id of an issue from its
// number via the REST API. The sub-issues endpoint needs the database id (the
// issue's `.id`), which differs from the human-facing issue number.
func childIssueDatabaseID(owner, name string, childNumber int) (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), ghTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", childIssueDatabaseIDArgs(owner, name, childNumber)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return 0, fmt.Errorf("gh api issues/%d: %s: %w", childNumber, strings.TrimSpace(string(out)), err)
	}
	id, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return 0, fmt.Errorf("parsing issue %d database id: %w", childNumber, err)
	}
	return id, nil
}

// childIssueDatabaseIDArgs builds the gh argv that reads an issue's database id
// (`.id`) from the REST API. Kept separate so the argument assembly is
// unit-testable without invoking gh.
func childIssueDatabaseIDArgs(owner, name string, childNumber int) []string {
	return []string{"api",
		fmt.Sprintf("repos/%s/%s/issues/%d", owner, name, childNumber),
		"--jq", ".id",
	}
}

// addSubIssueArgs builds the gh argv that POSTs a child issue's database id to
// the parent's sub_issues endpoint. The id is passed with -F (not -f) so gh
// serializes it as a JSON number, which the endpoint requires. Kept separate so
// the argument assembly is unit-testable without invoking gh.
func addSubIssueArgs(owner, name string, parentNumber, childID int) []string {
	return []string{"api",
		"--method", "POST",
		fmt.Sprintf("repos/%s/%s/issues/%d/sub_issues", owner, name, parentNumber),
		"-F", fmt.Sprintf("sub_issue_id=%d", childID),
	}
}
