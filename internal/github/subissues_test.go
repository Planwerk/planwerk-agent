package github

import (
	"slices"
	"testing"
)

func TestIssueDatabaseIDArgs(t *testing.T) {
	got := issueDatabaseIDArgs("acme", "widgets", 7)
	want := []string{"api", "repos/acme/widgets/issues/7", "--jq", ".id"}
	if !slices.Equal(got, want) {
		t.Fatalf("issueDatabaseIDArgs() = %v, want %v", got, want)
	}
}

func TestAddSubIssueArgs(t *testing.T) {
	// The path keys the parent by its issue number; the body field carries the
	// child's database id, passed with -F so gh serializes it as a JSON number.
	got := addSubIssueArgs("acme", "widgets", 12, 998877)
	want := []string{
		"api",
		"--method", "POST",
		"repos/acme/widgets/issues/12/sub_issues",
		"-F", "sub_issue_id=998877",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("addSubIssueArgs() = %v, want %v", got, want)
	}
}
