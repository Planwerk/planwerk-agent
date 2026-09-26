package github

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"slices"
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

// maxDependencyEdgesPerIssue bounds how many blockedBy and how many blocking
// edges the relations query pulls per sibling/child Sub Issue. A Sub Issue with
// more dependencies than this is unrealistic; the cap keeps a single GraphQL
// page sufficient. Truncation past it is logged, never silent.
const maxDependencyEdgesPerIssue = 50

// LinkedPR is the minimal view of an open pull request linked to a sibling or
// child Sub Issue via GitHub's closed-by relationship — a Closes/Fixes/Resolves
// reference or a Development-panel link. elaborate/plan/implement surface these
// so a Sub Issue's already-prepared implementation — opened as a PR but not yet
// merged to the default branch — is accounted for instead of rebuilt.
type LinkedPR struct {
	// Owner and Name are the repository the pull request itself lives in, which
	// is not necessarily the Sub Issue's: a closing keyword works across
	// repositories, so a PR in another repository can close this issue. Callers
	// that apply a PR number to a repository — ship marking one ready and merging
	// it — must check these first, and prompts must qualify the reference.
	Owner   string
	Name    string
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

// dependencyEdgesSelection selects a sibling or child node's native
// issue-dependency edges: the issues blocking it and the issues it blocks,
// each with its own repository, since a dependency may cross repositories.
var dependencyEdgesSelection = fmt.Sprintf(
	"blockedBy(first: %[1]d) { totalCount nodes { number state repository { nameWithOwner } } } "+
		"blocking(first: %[1]d) { totalCount nodes { number state repository { nameWithOwner } } }",
	maxDependencyEdgesPerIssue)

// buildRelationsQuery returns the GraphQL query that fetches an issue's parent
// (with the parent's own sub-issues, i.e. the siblings) and the issue's own
// sub-issues (the children) in a single round trip. Bodies are included so
// elaborate/plan can read the Meta Issue and sibling content, not just their
// titles. Each sibling and child node also pulls its open linked PRs via
// closedByPullRequestsReferences(includeClosedPrs: false) so the planning
// context sees a Sub Issue's prepared-but-unmerged implementation; the parent
// (Meta Issue) is not queried for PRs since it has no implementing PR of its
// own. Page sizes are interpolated from maxRelatedSubIssues and
// maxLinkedPRsPerSubIssue so each cap has one source.
//
// Every issue node also selects its own repository { nameWithOwner }: GitHub
// permits a sub-issue to live in a different repository than its parent, so the
// queried repo is not a safe answer for a node's coordinates. Each linked PR
// node selects it for the same reason — a closing keyword resolves across
// repositories, so a PR closing this Sub Issue may live in a third one.
//
// withDependencies adds dependencyEdgesSelection to each sibling and child node;
// the parent and the issue itself get no edges. The edges are best-effort: a
// GraphQL server that does not know blockedBy rejects the whole query, so
// false yields the query without them, byte for byte, for the fallback read.
func buildRelationsQuery(withDependencies bool) string {
	edges := ""
	if withDependencies {
		edges = " " + dependencyEdgesSelection
	}
	return fmt.Sprintf(`query($owner: String!, $name: String!, $number: Int!) {
  viewer { login }
  repository(owner: $owner, name: $name) {
    issue(number: $number) {
      number
      parent {
        number title body url state repository { nameWithOwner }
        subIssues(first: %[1]d) { totalCount nodes { number title body url state repository { nameWithOwner } closedByPullRequestsReferences(first: %[2]d, includeClosedPrs: false) { totalCount nodes { number title url state isDraft repository { nameWithOwner } author { login } } }%[3]s } }
      }
      subIssues(first: %[1]d) { totalCount nodes { number title body url state repository { nameWithOwner } closedByPullRequestsReferences(first: %[2]d, includeClosedPrs: false) { totalCount nodes { number title url state isDraft repository { nameWithOwner } author { login } } }%[3]s } }
    }
  }
}`, maxRelatedSubIssues, maxLinkedPRsPerSubIssue, edges)
}

// GetIssueRelations resolves the Meta/Sub-Issue neighborhood of an issue via a
// single gh GraphQL call. Callers treat a returned error as best-effort: a repo
// without sub-issue relationships, a token lacking the scope, or an older GHES
// that does not expose the fields all surface here and should degrade to "no
// relations" rather than abort the elaborate/plan run.
//
// The siblings' and children's dependency edges are best-effort too. A
// deployment that does not expose issue dependencies rejects the whole query,
// so a failed first read is retried once without the edges rather than losing
// the whole neighborhood (see getIssueRelations). A read that runs out its
// timeout wraps context.DeadlineExceeded, which getIssueRelations does not
// retry.
func (Client) GetIssueRelations(owner, name string, number int) (*IssueRelations, error) {
	return getIssueRelations(func(query string) ([]byte, error) {
		ctx, cancel := context.WithTimeout(context.Background(), ghTimeout)
		defer cancel()
		// owner/name go through -f (verbatim string), not -F: -F type-coerces its
		// value, so a repository literally named "2048", "404" or "null" would reach
		// GraphQL as a number or null and be rejected against String!.
		cmd := exec.CommandContext(ctx, "gh", "api", "graphql",
			"-f", "owner="+owner,
			"-f", "name="+name,
			"-F", "number="+strconv.Itoa(number),
			"-f", "query="+query)
		out, err := cmd.CombinedOutput()
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, fmt.Errorf("gh api graphql sub-issue relations: %w", ctxErr)
		}
		if err != nil {
			return nil, fmt.Errorf("gh api graphql sub-issue relations: %s: %w", strings.TrimSpace(string(out)), err)
		}
		return out, nil
	}, owner, name, number)
}

// getIssueRelations reads the relations through run, which executes one GraphQL
// query and returns its raw output. It asks for the dependency edges first; when
// that read fails it logs the error and reads again without them, returning the
// second read's error unchanged if that fails too. A first read that timed out
// (an error wrapping context.DeadlineExceeded) is returned without a retry: a
// hung GitHub would make the retry wait out a second full timeout for the same
// answer. Output that does not parse is returned as a parse error without a
// retry: the server answered, so the edges were not what it rejected. Kept
// separate from GetIssueRelations so the fallback is unit-testable without
// invoking gh.
func getIssueRelations(run func(query string) ([]byte, error), owner, name string, number int) (*IssueRelations, error) {
	out, err := run(buildRelationsQuery(true))
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		slog.Warn("sub-issue relations read with dependency edges failed; reading them without edges", "issue", number, "err", err)
		if out, err = run(buildRelationsQuery(false)); err != nil {
			return nil, err
		}
	}
	return parseIssueRelations(out, owner, name, number)
}

// graphqlIssueNode is the minimal issue projection the relations query returns
// for the parent and for each sub-issue node. ClosedByPRs carries the open
// linked PRs requested for sub-issue nodes; it stays zero-valued for the parent,
// which the query does not ask for PRs.
// Repository carries the node's own repository, which may differ from the
// queried one; it stays zero-valued on a deployment that does not expose the
// field, which toIssue treats as "same repo as the query".
// BlockedBy and Blocking carry the dependency edges requested for sub-issue
// nodes; they stay zero-valued for the parent and when the query without edges
// was used.
type graphqlIssueNode struct {
	Number      int                 `json:"number"`
	Title       string              `json:"title"`
	Body        string              `json:"body"`
	URL         string              `json:"url"`
	State       string              `json:"state"`
	Repository  graphqlRepoRef      `json:"repository"`
	ClosedByPRs graphqlLinkedPRs    `json:"closedByPullRequestsReferences"`
	BlockedBy   graphqlDependencies `json:"blockedBy"`
	Blocking    graphqlDependencies `json:"blocking"`
}

// graphqlRepoRef is the repository projection each issue node carries, in the
// owner/name form GitHub returns for nameWithOwner.
type graphqlRepoRef struct {
	NameWithOwner string `json:"nameWithOwner"`
}

// graphqlLinkedPRNode is the minimal PR projection the relations query returns
// for each entry of a sub-issue's closedByPullRequestsReferences connection.
type graphqlLinkedPRNode struct {
	Number     int            `json:"number"`
	Title      string         `json:"title"`
	URL        string         `json:"url"`
	State      string         `json:"state"`
	IsDraft    bool           `json:"isDraft"`
	Repository graphqlRepoRef `json:"repository"`
	Author     struct {
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

// graphqlDependencyNode is the minimal issue projection the relations query
// returns for each entry of a sub-issue's blockedBy or blocking connection.
type graphqlDependencyNode struct {
	Number     int            `json:"number"`
	State      string         `json:"state"`
	Repository graphqlRepoRef `json:"repository"`
}

// graphqlDependencies is the connection wrapper around a sub-issue's dependency
// nodes, carrying totalCount so truncation past maxDependencyEdgesPerIssue is
// detectable.
type graphqlDependencies struct {
	TotalCount int                     `json:"totalCount"`
	Nodes      []graphqlDependencyNode `json:"nodes"`
}

// graphqlSubIssues is the connection wrapper around a list of sub-issue nodes,
// carrying totalCount so truncation past maxRelatedSubIssues is detectable.
type graphqlSubIssues struct {
	TotalCount int                `json:"totalCount"`
	Nodes      []graphqlIssueNode `json:"nodes"`
}

// graphqlRelationsResponse mirrors the gh api graphql envelope for buildRelationsQuery.
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

// toIssue maps a GraphQL node onto the package's Issue type, lowercasing the
// state enum to match GetIssue's convention and attaching any open linked PRs
// and dependency edges the node carries.
//
// The repo coordinates come from the node's own repository when the query
// returned one, since a parent or sub-issue may live in a different repository.
// owner/name are the fallback for a deployment that does not expose the field,
// which keeps single-repo neighborhoods behaving exactly as before.
func toIssue(n graphqlIssueNode, owner, name string) Issue {
	if o, nm, ok := splitFullName(n.Repository.NameWithOwner); ok {
		owner, name = o, nm
	}
	return Issue{
		Owner:     owner,
		Name:      name,
		Number:    n.Number,
		Title:     n.Title,
		Body:      n.Body,
		URL:       n.URL,
		State:     strings.ToLower(n.State),
		LinkedPRs: nodeLinkedPRs(n, owner, name),
		BlockedBy: nodeDependencies(n.BlockedBy, "blocked_by", n.Number, owner, name),
		Blocking:  nodeDependencies(n.Blocking, "blocking", n.Number, owner, name),
	}
}

// nodeDependencies maps one of a sub-issue node's dependency connections
// (kind names which, for the log) onto []Issue, lowercasing each state. The
// result is sorted by repository, case-insensitively, then by number, so the
// same edges render the same prompt and cache key whatever order GitHub
// returns them in. A connection whose totalCount exceeds the fetched node count
// is logged. Returns nil when the connection has no nodes.
//
// Each entry's coordinates come from its own repository, since a dependency
// may cross repositories: the Sub Issue's repo (issueOwner/issueName) is only
// the fallback for a deployment that does not return the field, the rule
// nodeLinkedPRs follows.
func nodeDependencies(conn graphqlDependencies, kind string, issueNumber int, issueOwner, issueName string) []Issue {
	if conn.TotalCount > len(conn.Nodes) {
		slog.Warn("dependency edges truncated; some are omitted from the planning context",
			"issue", issueNumber, "kind", kind, "total", conn.TotalCount, "fetched", len(conn.Nodes), "cap", maxDependencyEdgesPerIssue)
	}
	var deps []Issue
	for _, d := range conn.Nodes {
		owner, name := issueOwner, issueName
		if o, nm, ok := splitFullName(d.Repository.NameWithOwner); ok {
			owner, name = o, nm
		}
		deps = append(deps, Issue{Owner: owner, Name: name, Number: d.Number, State: strings.ToLower(d.State)})
	}
	slices.SortFunc(deps, func(a, b Issue) int {
		return cmp.Or(
			strings.Compare(strings.ToLower(a.Owner+"/"+a.Name), strings.ToLower(b.Owner+"/"+b.Name)),
			cmp.Compare(a.Number, b.Number),
		)
	})
	return deps
}

// nodeLinkedPRs maps the open pull requests a sub-issue node carries via
// closedByPullRequestsReferences onto []LinkedPR, lowercasing each PR state to
// match the package's convention. A connection whose totalCount exceeds the
// fetched node count is logged so a Sub Issue with more open PRs than
// maxLinkedPRsPerSubIssue does not silently drop the overflow from the planning
// context. Returns nil when the node has no linked PRs (the parent node always
// does, since the query does not request PRs for it).
//
// Each PR's coordinates come from its own repository, since a closing keyword
// crosses repositories: the Sub Issue's repo (issueOwner/issueName) is only the
// fallback for a deployment that does not return the field.
func nodeLinkedPRs(n graphqlIssueNode, issueOwner, issueName string) []LinkedPR {
	conn := n.ClosedByPRs
	if conn.TotalCount > len(conn.Nodes) {
		slog.Warn("linked PRs truncated; some are omitted from the planning context",
			"issue", n.Number, "total", conn.TotalCount, "fetched", len(conn.Nodes), "cap", maxLinkedPRsPerSubIssue)
	}
	var prs []LinkedPR
	for _, p := range conn.Nodes {
		owner, name := issueOwner, issueName
		if o, nm, ok := splitFullName(p.Repository.NameWithOwner); ok {
			owner, name = o, nm
		}
		prs = append(prs, LinkedPR{
			Owner:   owner,
			Name:    name,
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
