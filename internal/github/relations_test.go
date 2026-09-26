package github

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// relOwner and relRepo are the repo coordinates the relations tests stamp onto
// parsed issues; kept as constants so the literals are not repeated (goconst).
const (
	relOwner = "acme"
	relRepo  = "widgets"
	// stateOpen is the lowercase issue/PR state the parser normalizes to; held as
	// a constant so the literal is not repeated across assertions (goconst).
	stateOpen = "open"
	// relOtherRepo is the second repository the cross-repo cases place nodes in.
	relOtherRepo = "gadgets"
)

// parentWithSiblings is a relations envelope where issue #3 is a Sub Issue of
// Meta Issue #1, which has three Sub Issues total (#2, #3, #4). #4 is closed.
const parentWithSiblings = `{"data":{"repository":{"issue":{
  "number":3,
  "parent":{
    "number":1,"title":"Meta","body":"Meta body","url":"https://example.com/1","state":"OPEN",
    "subIssues":{"totalCount":3,"nodes":[
      {"number":2,"title":"Sub two","body":"two body","url":"https://example.com/2","state":"OPEN"},
      {"number":3,"title":"Sub three","body":"three body","url":"https://example.com/3","state":"OPEN"},
      {"number":4,"title":"Sub four","body":"four body","url":"https://example.com/4","state":"CLOSED"}
    ]}
  },
  "subIssues":{"totalCount":0,"nodes":[]}
}}}}`

func TestParseIssueRelations_ParentAndSiblings(t *testing.T) {
	rel, err := parseIssueRelations([]byte(parentWithSiblings), relOwner, relRepo, 3)
	if err != nil {
		t.Fatalf("parseIssueRelations() error = %v", err)
	}

	if rel.Parent == nil {
		t.Fatal("Parent is nil, want Meta Issue #1")
	}
	if rel.Parent.Number != 1 || rel.Parent.Title != "Meta" || rel.Parent.Body != "Meta body" {
		t.Errorf("Parent = %+v, want #1 'Meta'/'Meta body'", rel.Parent)
	}
	if rel.Parent.Owner != relOwner || rel.Parent.Name != relRepo {
		t.Errorf("Parent repo coords = %q/%q, want acme/widgets", rel.Parent.Owner, rel.Parent.Name)
	}
	if rel.Parent.State != stateOpen {
		t.Errorf("Parent.State = %q, want lowercase %q", rel.Parent.State, stateOpen)
	}

	// The target issue (#3) must be filtered out of its own sibling list.
	if len(rel.Siblings) != 2 {
		t.Fatalf("len(Siblings) = %d, want 2 (#3 filtered out)", len(rel.Siblings))
	}
	for _, s := range rel.Siblings {
		if s.Number == 3 {
			t.Errorf("Siblings still contains the target issue #3: %+v", s)
		}
		if s.Owner != relOwner || s.Name != relRepo {
			t.Errorf("sibling #%d repo coords = %q/%q, want acme/widgets", s.Number, s.Owner, s.Name)
		}
	}
	// The closed sibling is kept, with its state normalized to lowercase.
	var four *Issue
	for i := range rel.Siblings {
		if rel.Siblings[i].Number == 4 {
			four = &rel.Siblings[i]
		}
	}
	if four == nil {
		t.Fatal("closed sibling #4 missing; closed Sub Issues must be kept")
	}
	if four.State != "closed" {
		t.Errorf("sibling #4 State = %q, want lowercase %q", four.State, "closed")
	}
	assertNoDependencyEdges(t, rel)
}

func TestParseIssueRelations_NoParent(t *testing.T) {
	// An issue that is neither a Sub Issue nor a Meta Issue: parent null, no children.
	const noRelations = `{"data":{"repository":{"issue":{
      "number":7,"parent":null,"subIssues":{"totalCount":0,"nodes":[]}
    }}}}`
	rel, err := parseIssueRelations([]byte(noRelations), relOwner, relRepo, 7)
	if err != nil {
		t.Fatalf("parseIssueRelations() error = %v", err)
	}
	if rel.Parent != nil {
		t.Errorf("Parent = %+v, want nil for an issue with no parent", rel.Parent)
	}
	if len(rel.Siblings) != 0 || len(rel.Children) != 0 {
		t.Errorf("Siblings=%d Children=%d, want 0/0", len(rel.Siblings), len(rel.Children))
	}
	assertNoDependencyEdges(t, rel)
}

func TestParseIssueRelations_Children(t *testing.T) {
	// Issue #1 is itself a Meta Issue with two Sub Issues and no parent.
	const meta = `{"data":{"repository":{"issue":{
      "number":1,"parent":null,
      "subIssues":{"totalCount":2,"nodes":[
        {"number":2,"title":"Sub two","body":"two","url":"https://example.com/2","state":"OPEN"},
        {"number":3,"title":"Sub three","body":"three","url":"https://example.com/3","state":"CLOSED"}
      ]}
    }}}}`
	rel, err := parseIssueRelations([]byte(meta), relOwner, relRepo, 1)
	if err != nil {
		t.Fatalf("parseIssueRelations() error = %v", err)
	}
	if rel.Parent != nil {
		t.Errorf("Parent = %+v, want nil", rel.Parent)
	}
	if len(rel.Children) != 2 {
		t.Fatalf("len(Children) = %d, want 2", len(rel.Children))
	}
	if rel.Children[1].State != "closed" {
		t.Errorf("Children[1].State = %q, want lowercase closed", rel.Children[1].State)
	}
	// No node carries a repository, so every child falls back to the queried repo.
	for _, c := range rel.Children {
		if c.Owner != relOwner || c.Name != relRepo {
			t.Errorf("child #%d repo coords = %q/%q, want the queried acme/widgets", c.Number, c.Owner, c.Name)
		}
	}
	assertNoDependencyEdges(t, rel)
}

// GitHub lets a sub-issue live in a different repository than its parent, so a
// child's coordinates come from its own repository field. Stamping the queried
// repo onto a foreign child is what would send ship to clone the wrong repo and
// implement whatever issue happens to carry that number.
func TestParseIssueRelations_ForeignChild(t *testing.T) {
	const metaWithForeignChild = `{"data":{"repository":{"issue":{
      "number":1,"parent":null,
      "subIssues":{"totalCount":2,"nodes":[
        {"number":2,"title":"Local","body":"b","url":"https://github.com/acme/widgets/issues/2","state":"OPEN","repository":{"nameWithOwner":"acme/widgets"}},
        {"number":58,"title":"Client counterpart","body":"b","url":"https://github.com/acme/gadgets/issues/58","state":"OPEN","repository":{"nameWithOwner":"acme/gadgets"}}
      ]}
    }}}}`
	rel, err := parseIssueRelations([]byte(metaWithForeignChild), relOwner, relRepo, 1)
	if err != nil {
		t.Fatalf("parseIssueRelations() error = %v", err)
	}
	if len(rel.Children) != 2 {
		t.Fatalf("len(Children) = %d, want 2", len(rel.Children))
	}
	if rel.Children[0].Owner != relOwner || rel.Children[0].Name != relRepo {
		t.Errorf("local child repo coords = %q/%q, want acme/widgets", rel.Children[0].Owner, rel.Children[0].Name)
	}
	foreign := rel.Children[1]
	if foreign.Owner != relOwner || foreign.Name != relOtherRepo {
		t.Errorf("foreign child repo coords = %q/%q, want acme/gadgets", foreign.Owner, foreign.Name)
	}
	assertNoDependencyEdges(t, rel)
}

// The mirror case: a counterpart issue reads its Meta Issue, which lives in the
// repo the work originated in. Its siblings may sit in either repo.
func TestParseIssueRelations_ForeignParent(t *testing.T) {
	const foreignParent = `{"data":{"repository":{"issue":{
      "number":58,
      "parent":{
        "number":607,"title":"Server feature","body":"b","url":"https://github.com/acme/widgets/issues/607","state":"OPEN","repository":{"nameWithOwner":"acme/widgets"},
        "subIssues":{"totalCount":2,"nodes":[
          {"number":58,"title":"Client","body":"b","url":"https://github.com/acme/gadgets/issues/58","state":"OPEN","repository":{"nameWithOwner":"acme/gadgets"}},
          {"number":9,"title":"Server side","body":"b","url":"https://github.com/acme/widgets/issues/9","state":"OPEN","repository":{"nameWithOwner":"acme/widgets"}}
        ]}
      },
      "subIssues":{"totalCount":0,"nodes":[]}
    }}}}`
	rel, err := parseIssueRelations([]byte(foreignParent), relOwner, relOtherRepo, 58)
	if err != nil {
		t.Fatalf("parseIssueRelations() error = %v", err)
	}
	if rel.Parent == nil {
		t.Fatal("Parent is nil, want Meta Issue #607")
	}
	if rel.Parent.Owner != relOwner || rel.Parent.Name != relRepo {
		t.Errorf("Parent repo coords = %q/%q, want acme/widgets", rel.Parent.Owner, rel.Parent.Name)
	}
	// #58 is the target issue and must be filtered out, leaving the server sibling.
	if len(rel.Siblings) != 1 {
		t.Fatalf("len(Siblings) = %d, want 1 (#58 filtered out)", len(rel.Siblings))
	}
	if rel.Siblings[0].Owner != relOwner || rel.Siblings[0].Name != relRepo {
		t.Errorf("sibling #%d repo coords = %q/%q, want acme/widgets",
			rel.Siblings[0].Number, rel.Siblings[0].Owner, rel.Siblings[0].Name)
	}
	assertNoDependencyEdges(t, rel)
}

// A deployment that does not expose the repository field must keep behaving as
// it did: every node belongs to the repo that was queried.
func TestParseIssueRelations_MissingRepositoryFallsBackToQueried(t *testing.T) {
	rel, err := parseIssueRelations([]byte(parentWithSiblings), relOwner, relRepo, 3)
	if err != nil {
		t.Fatalf("parseIssueRelations() error = %v", err)
	}
	if rel.Parent.Owner != relOwner || rel.Parent.Name != relRepo {
		t.Errorf("Parent repo coords = %q/%q, want the queried acme/widgets", rel.Parent.Owner, rel.Parent.Name)
	}
	for _, s := range rel.Siblings {
		if s.Owner != relOwner || s.Name != relRepo {
			t.Errorf("sibling #%d repo coords = %q/%q, want the queried acme/widgets", s.Number, s.Owner, s.Name)
		}
	}
	assertNoDependencyEdges(t, rel)
}

// parentWithLinkedPRs is a relations envelope where issue #3 is a Sub Issue of
// Meta Issue #1. Sibling #2 carries two open linked PRs (one a draft); sibling
// #4 carries none. The closedByPullRequestsReferences connection only ever
// returns open PRs (the query passes includeClosedPrs:false).
const parentWithLinkedPRs = `{"data":{"repository":{"issue":{
  "number":3,
  "parent":{
    "number":1,"title":"Meta","body":"Meta body","url":"https://example.com/1","state":"OPEN",
    "subIssues":{"totalCount":2,"nodes":[
      {"number":2,"title":"Sub two","body":"two body","url":"https://example.com/2","state":"OPEN",
        "closedByPullRequestsReferences":{"totalCount":2,"nodes":[
          {"number":20,"title":"Implement two","url":"https://example.com/pull/20","state":"OPEN","isDraft":false},
          {"number":21,"title":"WIP two","url":"https://example.com/pull/21","state":"OPEN","isDraft":true}
        ]}},
      {"number":4,"title":"Sub four","body":"four body","url":"https://example.com/4","state":"OPEN",
        "closedByPullRequestsReferences":{"totalCount":0,"nodes":[]}}
    ]}
  },
  "subIssues":{"totalCount":1,"nodes":[
    {"number":9,"title":"Child nine","body":"nine body","url":"https://example.com/9","state":"OPEN",
      "closedByPullRequestsReferences":{"totalCount":1,"nodes":[
        {"number":90,"title":"Implement nine","url":"https://example.com/pull/90","state":"OPEN","isDraft":false}
      ]}}
  ]}
}}}}`

func TestParseIssueRelations_LinkedPRs(t *testing.T) {
	rel, err := parseIssueRelations([]byte(parentWithLinkedPRs), relOwner, relRepo, 3)
	if err != nil {
		t.Fatalf("parseIssueRelations() error = %v", err)
	}

	var two, four *Issue
	for i := range rel.Siblings {
		switch rel.Siblings[i].Number {
		case 2:
			two = &rel.Siblings[i]
		case 4:
			four = &rel.Siblings[i]
		}
	}
	if two == nil || four == nil {
		t.Fatalf("missing siblings: two=%v four=%v", two, four)
	}

	if len(two.LinkedPRs) != 2 {
		t.Fatalf("sibling #2 LinkedPRs = %d, want 2", len(two.LinkedPRs))
	}
	if got := two.LinkedPRs[0]; got.Number != 20 || got.Title != "Implement two" ||
		got.URL != "https://example.com/pull/20" || got.State != stateOpen || got.IsDraft {
		t.Errorf("sibling #2 LinkedPRs[0] = %+v, want #20 open non-draft", got)
	}
	if got := two.LinkedPRs[1]; got.Number != 21 || got.State != stateOpen || !got.IsDraft {
		t.Errorf("sibling #2 LinkedPRs[1] = %+v, want #21 open draft", got)
	}

	// A sub-issue with no linked PRs decodes to a nil slice, not an empty one.
	if four.LinkedPRs != nil {
		t.Errorf("sibling #4 LinkedPRs = %+v, want nil for a Sub Issue with no open PRs", four.LinkedPRs)
	}

	// The Meta Issue itself is never queried for PRs.
	if rel.Parent.LinkedPRs != nil {
		t.Errorf("Parent.LinkedPRs = %+v, want nil (Meta Issue is not queried for PRs)", rel.Parent.LinkedPRs)
	}

	// Children carry their linked PRs too.
	if len(rel.Children) != 1 || len(rel.Children[0].LinkedPRs) != 1 {
		t.Fatalf("Children = %+v, want one child with one linked PR", rel.Children)
	}
	if got := rel.Children[0].LinkedPRs[0]; got.Number != 90 || got.State != stateOpen {
		t.Errorf("child #9 LinkedPRs[0] = %+v, want #90 open", got)
	}
	assertNoDependencyEdges(t, rel)
}

// TestParseIssueRelations_LinkedPRsLowercasesState confirms a GraphQL state enum
// arrives lowercased on LinkedPR, matching the convention the rest of the
// package uses for issue and PR states.
func TestParseIssueRelations_LinkedPRsLowercasesState(t *testing.T) {
	const env = `{"data":{"repository":{"issue":{
      "number":1,"parent":null,
      "subIssues":{"totalCount":1,"nodes":[
        {"number":2,"title":"Sub","body":"b","url":"https://example.com/2","state":"OPEN",
          "closedByPullRequestsReferences":{"totalCount":1,"nodes":[
            {"number":5,"title":"PR","url":"https://example.com/pull/5","state":"OPEN","isDraft":false}
          ]}}
      ]}
    }}}}`
	rel, err := parseIssueRelations([]byte(env), relOwner, relRepo, 1)
	if err != nil {
		t.Fatalf("parseIssueRelations() error = %v", err)
	}
	if len(rel.Children) != 1 || len(rel.Children[0].LinkedPRs) != 1 {
		t.Fatalf("Children = %+v, want one child with one linked PR", rel.Children)
	}
	if got := rel.Children[0].LinkedPRs[0].State; got != stateOpen {
		t.Errorf("LinkedPR.State = %q, want lowercase %q", got, stateOpen)
	}
	assertNoDependencyEdges(t, rel)
}

// A closing keyword resolves across repositories, so a Sub Issue's linked PR can
// live in another one. Its own repository is decoded, since ship applies the PR
// number to the repository it drives and the prompts qualify the reference by it.
// A PR node without a repository falls back to the Sub Issue's, matching the
// issue-node fallback for deployments that do not expose the field.
func TestParseIssueRelations_LinkedPRRepository(t *testing.T) {
	const env = `{"data":{"repository":{"issue":{
      "number":1,"parent":null,
      "subIssues":{"totalCount":1,"nodes":[
        {"number":2,"title":"Sub","body":"b","url":"https://example.com/2","state":"OPEN",
          "repository":{"nameWithOwner":"acme/widgets"},
          "closedByPullRequestsReferences":{"totalCount":3,"nodes":[
            {"number":5,"title":"Local","url":"https://example.com/pull/5","state":"OPEN","repository":{"nameWithOwner":"acme/widgets"}},
            {"number":6,"title":"Foreign","url":"https://example.com/pull/6","state":"OPEN","repository":{"nameWithOwner":"acme/gadgets"}},
            {"number":7,"title":"No repo","url":"https://example.com/pull/7","state":"OPEN"}
          ]}}
      ]}
    }}}}`
	rel, err := parseIssueRelations([]byte(env), relOwner, relRepo, 1)
	if err != nil {
		t.Fatalf("parseIssueRelations() error = %v", err)
	}
	if len(rel.Children) != 1 || len(rel.Children[0].LinkedPRs) != 3 {
		t.Fatalf("Children = %+v, want one child with three linked PRs", rel.Children)
	}
	prs := rel.Children[0].LinkedPRs
	if prs[0].Owner != relOwner || prs[0].Name != relRepo {
		t.Errorf("LinkedPRs[0] = %s/%s, want %s/%s", prs[0].Owner, prs[0].Name, relOwner, relRepo)
	}
	if prs[1].Owner != relOwner || prs[1].Name != "gadgets" {
		t.Errorf("LinkedPRs[1] = %s/%s, want the PR's own repository %s/gadgets", prs[1].Owner, prs[1].Name, relOwner)
	}
	if prs[2].Owner != relOwner || prs[2].Name != relRepo {
		t.Errorf("LinkedPRs[2] = %s/%s, want the fallback %s/%s", prs[2].Owner, prs[2].Name, relOwner, relRepo)
	}
	assertNoDependencyEdges(t, rel)
}

// TestParseIssueRelations_ViewerAndPRAuthor confirms the authenticated account
// (GraphQL viewer) and each linked PR's author login are decoded — ship gates an
// autonomous merge on the PR being authored by the authenticated account.
func TestParseIssueRelations_ViewerAndPRAuthor(t *testing.T) {
	const env = `{"data":{
      "viewer":{"login":"planwerk-bot"},
      "repository":{"issue":{
        "number":1,"parent":null,
        "subIssues":{"totalCount":1,"nodes":[
          {"number":2,"title":"Sub","body":"b","url":"https://example.com/2","state":"OPEN",
            "closedByPullRequestsReferences":{"totalCount":1,"nodes":[
              {"number":5,"title":"PR","url":"https://example.com/pull/5","state":"OPEN","isDraft":false,"author":{"login":"planwerk-bot"}}
            ]}}
      ]}
    }}}}`
	rel, err := parseIssueRelations([]byte(env), relOwner, relRepo, 1)
	if err != nil {
		t.Fatalf("parseIssueRelations() error = %v", err)
	}
	if rel.Viewer != "planwerk-bot" {
		t.Errorf("Viewer = %q, want %q", rel.Viewer, "planwerk-bot")
	}
	if len(rel.Children) != 1 || len(rel.Children[0].LinkedPRs) != 1 {
		t.Fatalf("Children = %+v, want one child with one linked PR", rel.Children)
	}
	if got := rel.Children[0].LinkedPRs[0].Author; got != "planwerk-bot" {
		t.Errorf("LinkedPR.Author = %q, want %q", got, "planwerk-bot")
	}
	assertNoDependencyEdges(t, rel)
}

func TestParseIssueRelations_InvalidJSON(t *testing.T) {
	if _, err := parseIssueRelations([]byte("not json"), relOwner, relRepo, 1); err == nil {
		t.Fatal("parseIssueRelations() error = nil, want a decode error")
	}
}

// assertNoDependencyEdges fails when any issue in rel carries a dependency
// edge: a fixture without edge fields, or the fallback query, must decode to
// nil edges everywhere.
func assertNoDependencyEdges(t *testing.T, rel *IssueRelations) {
	t.Helper()
	issues := append(append([]Issue{}, rel.Siblings...), rel.Children...)
	if rel.Parent != nil {
		issues = append(issues, *rel.Parent)
	}
	for _, i := range issues {
		if i.BlockedBy != nil || i.Blocking != nil {
			t.Errorf("issue #%d BlockedBy=%+v Blocking=%+v, want nil edges", i.Number, i.BlockedBy, i.Blocking)
		}
	}
}

// edgeField is the dependency field the edge-free query must not select.
const edgeField = "blockedBy"

func TestBuildRelationsQuery(t *testing.T) {
	plain := buildRelationsQuery(false)
	withEdges := buildRelationsQuery(true)

	for _, field := range []string{edgeField, "blocking"} {
		if strings.Contains(plain, field) {
			t.Errorf("buildRelationsQuery(false) selects %q; the fallback query must carry no edges", field)
		}
	}
	for _, sel := range []string{"blockedBy(first: 50)", "blocking(first: 50)"} {
		if n := strings.Count(withEdges, sel); n != 2 {
			t.Errorf("buildRelationsQuery(true) carries %q %d times, want 2 (siblings and children)", sel, n)
		}
	}
	if got := strings.ReplaceAll(withEdges, " "+dependencyEdgesSelection, ""); got != plain {
		t.Errorf("removing the edge selection does not yield the plain query:\n%s\n---\n%s", got, plain)
	}
	// The edges sit inside each sub-issue node, after its linked-PR selection.
	// A misplaced verb would fail here rather than on GitHub.
	const prSelectionEnd = "author { login } } }"
	if n := strings.Count(plain, prSelectionEnd+" } }"); n != 2 {
		t.Errorf("plain query closes %d sub-issue nodes after the linked-PR selection, want 2", n)
	}
	if n := strings.Count(withEdges, prSelectionEnd+" "+dependencyEdgesSelection+" } }"); n != 2 {
		t.Errorf("edge selection follows the linked-PR selection %d times, want 2", n)
	}
}

// relationsRunner is a fake run seam for getIssueRelations, standing in for gh:
// it records every query it is handed and answers through respond.
type relationsRunner struct {
	queries []string
	respond func(query string) ([]byte, error)
}

func (r *relationsRunner) run(query string) ([]byte, error) {
	r.queries = append(r.queries, query)
	return r.respond(query)
}

// parentWithDependencyEdges is a relations envelope where issue #3 is a Sub
// Issue of Meta Issue #1 with one other sibling, #2, and is itself the parent
// of child #5. Sibling #2 and child #5 carry the same dependency edges, listed
// out of order and one of them in another repository.
const parentWithDependencyEdges = `{"data":{"repository":{"issue":{
  "number":3,
  "parent":{
    "number":1,"title":"Meta","body":"Meta body","url":"https://example.com/1","state":"OPEN",
    "subIssues":{"totalCount":2,"nodes":[
      {"number":2,"title":"Sub two","body":"two body","url":"https://example.com/2","state":"OPEN",
        "blockedBy":{"totalCount":2,"nodes":[
          {"number":7,"state":"OPEN","repository":{"nameWithOwner":"acme/widgets"}},
          {"number":3,"state":"CLOSED","repository":{"nameWithOwner":"acme/widgets"}}
        ]},
        "blocking":{"totalCount":1,"nodes":[
          {"number":9,"state":"OPEN","repository":{"nameWithOwner":"Other/Repo"}}
        ]}},
      {"number":3,"title":"Sub three","body":"three body","url":"https://example.com/3","state":"OPEN"}
    ]}
  },
  "subIssues":{"totalCount":1,"nodes":[
    {"number":5,"title":"Child five","body":"five body","url":"https://example.com/5","state":"OPEN",
      "blockedBy":{"totalCount":2,"nodes":[
        {"number":7,"state":"OPEN","repository":{"nameWithOwner":"acme/widgets"}},
        {"number":3,"state":"CLOSED","repository":{"nameWithOwner":"acme/widgets"}}
      ]},
      "blocking":{"totalCount":1,"nodes":[
        {"number":9,"state":"OPEN","repository":{"nameWithOwner":"Other/Repo"}}
      ]}}
  ]}
}}}}`

func TestParseIssueRelations_DependencyEdges(t *testing.T) {
	rel, err := parseIssueRelations([]byte(parentWithDependencyEdges), relOwner, relRepo, 3)
	if err != nil {
		t.Fatalf("parseIssueRelations() error = %v", err)
	}
	wantBlockedBy := []Issue{
		{Owner: relOwner, Name: relRepo, Number: 3, State: "closed"},
		{Owner: relOwner, Name: relRepo, Number: 7, State: stateOpen},
	}
	wantBlocking := []Issue{{Owner: "Other", Name: "Repo", Number: 9, State: stateOpen}}

	if len(rel.Siblings) != 1 || len(rel.Children) != 1 {
		t.Fatalf("Siblings=%d Children=%d, want 1/1", len(rel.Siblings), len(rel.Children))
	}
	for _, i := range []Issue{rel.Siblings[0], rel.Children[0]} {
		assertIssues(t, fmt.Sprintf("#%d BlockedBy", i.Number), i.BlockedBy, wantBlockedBy)
		assertIssues(t, fmt.Sprintf("#%d Blocking", i.Number), i.Blocking, wantBlocking)
	}
	if rel.Parent.BlockedBy != nil || rel.Parent.Blocking != nil {
		t.Errorf("Parent edges = %+v / %+v, want nil (the Meta Issue is not queried for edges)", rel.Parent.BlockedBy, rel.Parent.Blocking)
	}
}

func TestParseIssueRelations_DependencyEdgesEmpty(t *testing.T) {
	const env = `{"data":{"repository":{"issue":{
      "number":1,"parent":null,
      "subIssues":{"totalCount":1,"nodes":[
        {"number":2,"title":"Sub","body":"b","url":"https://example.com/2","state":"OPEN",
          "blockedBy":{"totalCount":0,"nodes":[]},
          "blocking":{"totalCount":0,"nodes":[]}}
      ]}
    }}}}`
	rel, err := parseIssueRelations([]byte(env), relOwner, relRepo, 1)
	if err != nil {
		t.Fatalf("parseIssueRelations() error = %v", err)
	}
	if len(rel.Children) != 1 {
		t.Fatalf("len(Children) = %d, want 1", len(rel.Children))
	}
	assertNoDependencyEdges(t, rel)
}

// An edge node without a repository belongs to the Sub Issue that carries it,
// not to the queried repository: the Sub Issue here lives in acme/gadgets.
func TestParseIssueRelations_DependencyEdgeMissingRepository(t *testing.T) {
	const env = `{"data":{"repository":{"issue":{
      "number":1,"parent":null,
      "subIssues":{"totalCount":1,"nodes":[
        {"number":2,"title":"Sub","body":"b","url":"https://example.com/2","state":"OPEN",
          "repository":{"nameWithOwner":"acme/gadgets"},
          "blockedBy":{"totalCount":1,"nodes":[{"number":4,"state":"OPEN"}]}}
      ]}
    }}}}`
	rel, err := parseIssueRelations([]byte(env), relOwner, relRepo, 1)
	if err != nil {
		t.Fatalf("parseIssueRelations() error = %v", err)
	}
	if len(rel.Children) != 1 {
		t.Fatalf("len(Children) = %d, want 1", len(rel.Children))
	}
	assertIssues(t, "BlockedBy", rel.Children[0].BlockedBy,
		[]Issue{{Owner: relOwner, Name: relOtherRepo, Number: 4, State: stateOpen}})
	if rel.Children[0].Blocking != nil {
		t.Errorf("Blocking = %+v, want nil for an absent connection", rel.Children[0].Blocking)
	}
}

// The order is the repository compared case-insensitively, then the number, so
// an uppercase owner does not sort ahead of every lowercase one.
func TestNodeDependencies_SortsByRepositoryThenNumber(t *testing.T) {
	conn := graphqlDependencies{TotalCount: 4, Nodes: []graphqlDependencyNode{
		{Number: 9, State: "OPEN", Repository: graphqlRepoRef{NameWithOwner: "Other/Repo"}},
		{Number: 4, State: "OPEN", Repository: graphqlRepoRef{NameWithOwner: "acme/widgets"}},
		{Number: 2, State: "OPEN", Repository: graphqlRepoRef{NameWithOwner: "other/alpha"}},
		{Number: 1, State: "OPEN"},
	}}
	got := nodeDependencies(conn, "blocked_by", 2, relOwner, relRepo)
	assertIssues(t, "sorted edges", got, []Issue{
		{Owner: relOwner, Name: relRepo, Number: 1, State: stateOpen},
		{Owner: relOwner, Name: relRepo, Number: 4, State: stateOpen},
		{Owner: "other", Name: "alpha", Number: 2, State: stateOpen},
		{Owner: "Other", Name: "Repo", Number: 9, State: stateOpen},
	})
}

// assertIssues compares dependency entries on the fields an edge carries.
func assertIssues(t *testing.T, what string, got, want []Issue) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s = %+v, want %+v", what, got, want)
	}
	for i := range want {
		g, w := got[i], want[i]
		if g.Owner != w.Owner || g.Name != w.Name || g.Number != w.Number || g.State != w.State {
			t.Errorf("%s[%d] = %s/%s#%d %s, want %s/%s#%d %s", what, i, g.Owner, g.Name, g.Number, g.State, w.Owner, w.Name, w.Number, w.State)
		}
	}
}

func TestGetIssueRelations_WithEdges(t *testing.T) {
	r := &relationsRunner{respond: func(string) ([]byte, error) { return []byte(parentWithDependencyEdges), nil }}
	rel, err := getIssueRelations(r.run, relOwner, relRepo, 3)
	if err != nil {
		t.Fatalf("getIssueRelations() error = %v", err)
	}
	if len(r.queries) != 1 {
		t.Fatalf("run called %d times, want 1", len(r.queries))
	}
	if !strings.Contains(r.queries[0], edgeField) {
		t.Errorf("first query does not select %s:\n%s", edgeField, r.queries[0])
	}
	if len(rel.Siblings) != 1 || len(rel.Siblings[0].BlockedBy) != 2 || len(rel.Siblings[0].Blocking) != 1 {
		t.Errorf("Siblings = %+v, want sibling #2 with 2 blockers and 1 blocked issue", rel.Siblings)
	}
}

// A deployment that does not know blockedBy rejects the whole query. The
// neighborhood must still load, without edges, from the plain query.
func TestGetIssueRelations_FallsBackWithoutEdges(t *testing.T) {
	r := &relationsRunner{respond: func(query string) ([]byte, error) {
		if strings.Contains(query, edgeField) {
			return nil, errors.New("Field 'blockedBy' doesn't exist on type 'Issue'")
		}
		return []byte(parentWithSiblings), nil
	}}
	rel, err := getIssueRelations(r.run, relOwner, relRepo, 3)
	if err != nil {
		t.Fatalf("getIssueRelations() error = %v, want the fallback to succeed", err)
	}
	if len(r.queries) != 2 {
		t.Fatalf("run called %d times, want 2 (edges, then the fallback)", len(r.queries))
	}
	if strings.Contains(r.queries[1], edgeField) {
		t.Errorf("fallback query still selects %s:\n%s", edgeField, r.queries[1])
	}
	if rel.Parent == nil || len(rel.Siblings) != 2 {
		t.Fatalf("Parent=%v Siblings=%d, want Meta Issue #1 and 2 siblings", rel.Parent, len(rel.Siblings))
	}
	assertNoDependencyEdges(t, rel)
}

func TestGetIssueRelations_BothQueriesFail(t *testing.T) {
	sentinel := errors.New("gh: HTTP 502")
	r := &relationsRunner{respond: func(string) ([]byte, error) { return nil, sentinel }}
	rel, err := getIssueRelations(r.run, relOwner, relRepo, 3)
	if rel != nil {
		t.Errorf("relations = %+v, want nil when both reads fail", rel)
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("error = %v, want the fallback read's error", err)
	}
	if len(r.queries) != 2 {
		t.Errorf("run called %d times, want 2", len(r.queries))
	}
}

// A first read that timed out says nothing about the edges, and the retry
// would wait out a second full timeout against the same hung GitHub.
func TestGetIssueRelations_TimeoutNotRetried(t *testing.T) {
	r := &relationsRunner{respond: func(string) ([]byte, error) {
		return nil, fmt.Errorf("gh api graphql sub-issue relations: %w", context.DeadlineExceeded)
	}}
	rel, err := getIssueRelations(r.run, relOwner, relRepo, 3)
	if rel != nil {
		t.Errorf("relations = %+v, want nil after a timeout", rel)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("error = %v, want one wrapping context.DeadlineExceeded", err)
	}
	if len(r.queries) != 1 {
		t.Errorf("run called %d times, want 1 (a timeout is not retried)", len(r.queries))
	}
}

// Output that does not parse came from a server that answered, so the edges
// were not what it rejected: the parse error is returned without a retry.
func TestGetIssueRelations_MalformedOutputNotRetried(t *testing.T) {
	r := &relationsRunner{respond: func(string) ([]byte, error) { return []byte("not json"), nil }}
	_, err := getIssueRelations(r.run, relOwner, relRepo, 3)
	if err == nil || !strings.HasPrefix(err.Error(), "parsing gh api graphql sub-issue relations:") {
		t.Errorf("error = %v, want the parse error", err)
	}
	if len(r.queries) != 1 {
		t.Errorf("run called %d times, want 1 (a parse error is not retried)", len(r.queries))
	}
}
