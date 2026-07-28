package github

import (
	"reflect"
	"slices"
	"testing"
)

func TestAddIssueDependencyArgs(t *testing.T) {
	// The path keys the blocked issue by its number; the body field carries the
	// blocker's database id, passed with -F so gh serializes it as a JSON number.
	got := addIssueDependencyArgs("acme", "widgets", 12, 998877)
	want := []string{
		"api",
		"--method", "POST",
		"repos/acme/widgets/issues/12/dependencies/blocked_by",
		"-F", "issue_id=998877",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("addIssueDependencyArgs() = %v, want %v", got, want)
	}
}

func TestBlockedByIssuesArgs(t *testing.T) {
	got := blockedByIssuesArgs("acme", "widgets", 7)
	want := []string{"api", "repos/acme/widgets/issues/7/dependencies/blocked_by"}
	if !slices.Equal(got, want) {
		t.Fatalf("blockedByIssuesArgs() = %v, want %v", got, want)
	}
}

func TestParseBlockedByIssues(t *testing.T) {
	out := []byte(`[
	  {"number": 3, "title": "Foundation", "html_url": "https://github.com/acme/widgets/issues/3", "state": "CLOSED"},
	  {"number": 5, "title": "Tier 1", "html_url": "https://github.com/acme/widgets/issues/5", "state": "OPEN"}
	]`)
	got, err := parseBlockedByIssues(out, "acme", "widgets")
	if err != nil {
		t.Fatalf("parseBlockedByIssues() error: %v", err)
	}
	want := []Issue{
		{Owner: "acme", Name: "widgets", Number: 3, Title: "Foundation", URL: "https://github.com/acme/widgets/issues/3", State: "closed"},
		{Owner: "acme", Name: "widgets", Number: 5, Title: "Tier 1", URL: "https://github.com/acme/widgets/issues/5", State: "open"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseBlockedByIssues() = %+v, want %+v", got, want)
	}
}

// A blocker may live in another repository — GitHub accepts a blocked_by edge
// across repos, and the endpoint reports each blocker's own repository. Reading
// the queried pair instead would let ship resolve the blocker against the wrong
// repo, where the same number is a different issue.
func TestParseBlockedByIssues_ForeignBlocker(t *testing.T) {
	out := []byte(`[
	  {"number": 607, "title": "Server feature", "html_url": "https://github.com/acme/widgets/issues/607", "state": "OPEN",
	   "repository": {"full_name": "acme/widgets"}},
	  {"number": 59, "title": "Local blocker", "html_url": "https://github.com/acme/gadgets/issues/59", "state": "OPEN",
	   "repository": {"full_name": "acme/gadgets"}}
	]`)
	got, err := parseBlockedByIssues(out, relOwner, relOtherRepo)
	if err != nil {
		t.Fatalf("parseBlockedByIssues() error: %v", err)
	}
	want := []Issue{
		{Owner: relOwner, Name: relRepo, Number: 607, Title: "Server feature", URL: "https://github.com/acme/widgets/issues/607", State: "open"},
		{Owner: relOwner, Name: relOtherRepo, Number: 59, Title: "Local blocker", URL: "https://github.com/acme/gadgets/issues/59", State: "open"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseBlockedByIssues() = %+v, want %+v", got, want)
	}
}

// An empty dependency list (the common case for an unblocked issue) decodes to
// no issues, not an error.
func TestParseBlockedByIssues_Empty(t *testing.T) {
	got, err := parseBlockedByIssues([]byte(`[]`), "acme", "widgets")
	if err != nil {
		t.Fatalf("parseBlockedByIssues() error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("parseBlockedByIssues([]) = %+v, want empty", got)
	}
}

// Malformed JSON surfaces as an error rather than a silent empty result.
func TestParseBlockedByIssues_Malformed(t *testing.T) {
	if _, err := parseBlockedByIssues([]byte(`not json`), "acme", "widgets"); err == nil {
		t.Fatalf("expected an error decoding malformed dependency output")
	}
}
