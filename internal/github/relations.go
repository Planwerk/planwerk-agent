package github

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os/exec"
	"strconv"
	"strings"
)

// maxRelatedSubIssues bounds how many sub-issues the relations query pulls for
// the parent (siblings) and the issue itself (children). A Meta Issue with more
// than this many Sub Issues is unrealistic; the cap keeps a single GraphQL page
// sufficient. Truncation past it is logged, never silent.
const maxRelatedSubIssues = 100

// maxLinkedPRsPerSubIssue bounds how many open pull requests the relations query
// pulls per sibling/child Sub Issue via closedByPullRequestsReferences. A Sub
// Issue with more open PRs than this is unrealistic; the cap keeps a single
// GraphQL page sufficient. Truncation past it is logged, never silent.
const maxLinkedPRsPerSubIssue = 10

// LinkedPR is the minimal view of an open pull request linked to a sibling or
// child Sub Issue via GitHub's closed-by relationship — a Closes/Fixes/Resolves
// reference or a Development-panel link. elaborate/plan/implement surface these
// so a Sub Issue's already-prepared implementation — opened as a PR but not yet
// merged to the default branch — is accounted for instead of rebuilt.
type LinkedPR struct {
	Number  int
	Title   string
	URL     string
	State   string // lowercased PR state; always "open" for what GetIssueRelations surfaces
	IsDraft bool
	// Author is the login of the account that opened the PR (empty when the
	// author account is deleted). ship verifies this against the authenticated
	// account before merging, so an attacker-opened PR that merely references the
	// Sub Issue with a closing keyword cannot be picked up and merged.
	Author string
}

// IssueRelations is the Meta/Sub-Issue neighborhood of an issue, used by
// elaborate and plan so they ground a Sub Issue in its larger effort instead of
// in isolation:
//   - Parent is the Meta Issue when the issue is a Sub Issue (nil otherwise).
//   - Siblings are the Meta Issue's other Sub Issues (the issue itself filtered out).
//   - Children are the issue's own Sub Issues, present when the issue is itself a
//     Meta Issue.
type IssueRelations struct {
	Parent   *Issue
	Siblings []Issue
	Children []Issue
	// Viewer is the login of the account gh is authenticated as (GraphQL
	// `viewer`). ship matches a Sub Issue's linked PRs against it so only a PR
	// the authenticated account opened is eligible to be marked ready and merged.
	Viewer string
}

// relationsQuery is the GraphQL query that fetches an issue's parent (with the
// parent's own sub-issues, i.e. the siblings) and the issue's own sub-issues
// (the children) in a single round trip. Bodies are included so elaborate/plan
// can read the Meta Issue and sibling content, not just their titles. Each
// sibling and child node also pulls its open linked PRs via
// closedByPullRequestsReferences(includeClosedPrs: false) so the planning
// context sees a Sub Issue's prepared-but-unmerged implementation; the parent
// (Meta Issue) is not queried for PRs since it has no implementing PR of its
// own. Page sizes are interpolated from maxRelatedSubIssues and
// maxLinkedPRsPerSubIssue so each cap has one source.
var relationsQuery = fmt.Sprintf(`query($owner: String!, $name: String!, $number: Int!) {
  viewer { login }
  repository(owner: $owner, name: $name) {
    issue(number: $number) {
      number
      parent {
        number title body url state
        subIssues(first: %[1]d) { totalCount nodes { number title body url state closedByPullRequestsReferences(first: %[2]d, includeClosedPrs: false) { totalCount nodes { number title url state isDraft author { login } } } } }
      }
      subIssues(first: %[1]d) { totalCount nodes { number title body url state closedByPullRequestsReferences(first: %[2]d, includeClosedPrs: false) { totalCount nodes { number title url state isDraft author { login } } } } }
    }
  }
}`, maxRelatedSubIssues, maxLinkedPRsPerSubIssue)

// GetIssueRelations resolves the Meta/Sub-Issue neighborhood of an issue via a
// single gh GraphQL call. Callers treat a returned error as best-effort: a repo
// without sub-issue relationships, a token lacking the scope, or an older GHES
// that does not expose the fields all surface here and should degrade to "no
// relations" rather than abort the elaborate/plan run.
func GetIssueRelations(owner, name string, number int) (*IssueRelations, error) {
	ctx, cancel := context.WithTimeout(context.Background(), ghTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", "api", "graphql",
		"-F", "owner="+owner,
		"-F", "name="+name,
		"-F", "number="+strconv.Itoa(number),
		"-f", "query="+relationsQuery)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("gh api graphql sub-issue relations: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return parseIssueRelations(out, owner, name, number)
}

// graphqlIssueNode is the minimal issue projection the relations query returns
// for the parent and for each sub-issue node. ClosedByPRs carries the open
// linked PRs requested for sub-issue nodes; it stays zero-valued for the parent,
// which the query does not ask for PRs.
type graphqlIssueNode struct {
	Number      int              `json:"number"`
	Title       string           `json:"title"`
	Body        string           `json:"body"`
	URL         string           `json:"url"`
	State       string           `json:"state"`
	ClosedByPRs graphqlLinkedPRs `json:"closedByPullRequestsReferences"`
}

// graphqlLinkedPRNode is the minimal PR projection the relations query returns
// for each entry of a sub-issue's closedByPullRequestsReferences connection.
type graphqlLinkedPRNode struct {
	Number  int    `json:"number"`
	Title   string `json:"title"`
	URL     string `json:"url"`
	State   string `json:"state"`
	IsDraft bool   `json:"isDraft"`
	Author  struct {
		Login string `json:"login"`
	} `json:"author"`
}

// graphqlLinkedPRs is the connection wrapper around a sub-issue's linked PR
// nodes, carrying totalCount so truncation past maxLinkedPRsPerSubIssue is
// detectable.
type graphqlLinkedPRs struct {
	TotalCount int                   `json:"totalCount"`
	Nodes      []graphqlLinkedPRNode `json:"nodes"`
}

// graphqlSubIssues is the connection wrapper around a list of sub-issue nodes,
// carrying totalCount so truncation past maxRelatedSubIssues is detectable.
type graphqlSubIssues struct {
	TotalCount int                `json:"totalCount"`
	Nodes      []graphqlIssueNode `json:"nodes"`
}

// graphqlRelationsResponse mirrors the gh api graphql envelope for relationsQuery.
// Parent is a pointer so a missing parent (the issue is not a Sub Issue) decodes
// to nil rather than a zero-valued issue.
type graphqlRelationsResponse struct {
	Data struct {
		Viewer struct {
			Login string `json:"login"`
		} `json:"viewer"`
		Repository struct {
			Issue struct {
				Number int `json:"number"`
				Parent *struct {
					graphqlIssueNode
					SubIssues graphqlSubIssues `json:"subIssues"`
				} `json:"parent"`
				SubIssues graphqlSubIssues `json:"subIssues"`
			} `json:"issue"`
		} `json:"repository"`
	} `json:"data"`
}

// parseIssueRelations decodes the relations GraphQL envelope into IssueRelations.
// It filters the target issue out of the sibling list, stamps Owner/Name onto
// every returned issue, and normalizes the GraphQL state enum (OPEN/CLOSED) to
// the lowercase form the rest of the github package uses (open/closed). Sibling
// or child lists truncated past maxRelatedSubIssues are logged, never dropped
// silently. Kept separate from GetIssueRelations so the decode is unit-testable
// without invoking gh.
func parseIssueRelations(out []byte, owner, name string, number int) (*IssueRelations, error) {
	var resp graphqlRelationsResponse
	if err := json.Unmarshal(out, &resp); err != nil {
		return nil, fmt.Errorf("parsing gh api graphql sub-issue relations: %w", err)
	}

	issue := resp.Data.Repository.Issue
	rel := &IssueRelations{Viewer: resp.Data.Viewer.Login}

	if p := issue.Parent; p != nil {
		parent := toIssue(p.graphqlIssueNode, owner, name)
		rel.Parent = &parent
		rel.Siblings = nodesToIssues(p.SubIssues.Nodes, owner, name, number)
		warnIfTruncated(p.SubIssues, "sibling", number)
	}

	rel.Children = nodesToIssues(issue.SubIssues.Nodes, owner, name, 0)
	warnIfTruncated(issue.SubIssues, "child", number)

	return rel, nil
}

// nodesToIssues converts sub-issue nodes to github.Issue values, dropping the
// node whose number equals exclude (used to filter the target issue out of its
// own sibling list; pass 0 to keep every node, since issue numbers start at 1).
func nodesToIssues(nodes []graphqlIssueNode, owner, name string, exclude int) []Issue {
	var issues []Issue
	for _, n := range nodes {
		if n.Number == exclude {
			continue
		}
		issues = append(issues, toIssue(n, owner, name))
	}
	return issues
}

// toIssue maps a GraphQL node onto the package's Issue type, stamping the repo
// coordinates, lowercasing the state enum to match GetIssue's convention, and
// attaching any open linked PRs the node carries.
func toIssue(n graphqlIssueNode, owner, name string) Issue {
	return Issue{
		Owner:     owner,
		Name:      name,
		Number:    n.Number,
		Title:     n.Title,
		Body:      n.Body,
		URL:       n.URL,
		State:     strings.ToLower(n.State),
		LinkedPRs: nodeLinkedPRs(n),
	}
}

// nodeLinkedPRs maps the open pull requests a sub-issue node carries via
// closedByPullRequestsReferences onto []LinkedPR, lowercasing each PR state to
// match the package's convention. A connection whose totalCount exceeds the
// fetched node count is logged so a Sub Issue with more open PRs than
// maxLinkedPRsPerSubIssue does not silently drop the overflow from the planning
// context. Returns nil when the node has no linked PRs (the parent node always
// does, since the query does not request PRs for it).
func nodeLinkedPRs(n graphqlIssueNode) []LinkedPR {
	conn := n.ClosedByPRs
	if conn.TotalCount > len(conn.Nodes) {
		slog.Warn("linked PRs truncated; some are omitted from the planning context",
			"issue", n.Number, "total", conn.TotalCount, "fetched", len(conn.Nodes), "cap", maxLinkedPRsPerSubIssue)
	}
	var prs []LinkedPR
	for _, p := range conn.Nodes {
		prs = append(prs, LinkedPR{
			Number:  p.Number,
			Title:   p.Title,
			URL:     p.URL,
			State:   strings.ToLower(p.State),
			IsDraft: p.IsDraft,
			Author:  p.Author.Login,
		})
	}
	return prs
}

// warnIfTruncated logs when a sub-issue connection returned fewer nodes than its
// totalCount, so a Meta Issue with more than maxRelatedSubIssues Sub Issues does
// not silently drop the overflow from the planning context.
func warnIfTruncated(conn graphqlSubIssues, kind string, number int) {
	if conn.TotalCount > len(conn.Nodes) {
		slog.Warn("sub-issue relations truncated; some are omitted from the planning context",
			"issue", number, "kind", kind, "total", conn.TotalCount, "fetched", len(conn.Nodes), "cap", maxRelatedSubIssues)
	}
}
