package github

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// DefaultIssueListLimit bounds how many issues ListAllIssues fetches. Picked
// to comfortably cover every repo we audit today; callers that need more can
// set a custom limit via ListAllIssuesWithLimit.
const DefaultIssueListLimit = 1000

// ExistingIssue is the minimal view of a GitHub issue used for duplicate
// detection: the visible title and the URL for log/debug output.
type ExistingIssue struct {
	Title string `json:"title"`
	URL   string `json:"url"`
}

// IssueLister returns every existing issue (open and closed) in the given
// repo. It is the injection point that lets the propose/audit pipelines
// substitute a fake implementation in tests.
type IssueLister func(owner, name string) ([]ExistingIssue, error)

// ListAllIssues returns every issue (open and closed) in the repo up to
// DefaultIssueListLimit. The propose and audit pipelines call this once per
// run to build a duplicate-detection index.
func (c Client) ListAllIssues(owner, name string) ([]ExistingIssue, error) {
	return c.ListAllIssuesWithLimit(owner, name, DefaultIssueListLimit)
}

// ListAllIssuesWithLimit is ListAllIssues with an explicit fetch cap.
func (Client) ListAllIssuesWithLimit(owner, name string, limit int) ([]ExistingIssue, error) {
	if limit <= 0 {
		limit = DefaultIssueListLimit
	}
	repo := fmt.Sprintf("%s/%s", owner, name)
	args := []string{"issue", "list",
		"--repo", repo,
		"--state", "all",
		"--limit", fmt.Sprintf("%d", limit),
		"--json", "title,url",
	}

	ctx, cancel := context.WithTimeout(context.Background(), ghTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("gh issue list: %s: %w", strings.TrimSpace(string(out)), err)
	}

	var issues []ExistingIssue
	if err := json.Unmarshal(out, &issues); err != nil {
		return nil, fmt.Errorf("parsing gh issue list output: %w", err)
	}
	return issues, nil
}

// NormalizeIssueTitle produces the canonical form used for duplicate
// detection. The normalization is deliberately minimal so matching is
// resilient to case and trailing punctuation drift but does not collapse
// semantically different titles:
//
//   - leading/trailing whitespace trimmed
//   - internal runs of whitespace collapsed to single spaces
//   - ASCII letters lowercased
//   - trailing ASCII punctuation (. ! ? , ; :) stripped
func NormalizeIssueTitle(s string) string {
	s = strings.TrimSpace(s)
	// Collapse internal whitespace.
	var b strings.Builder
	b.Grow(len(s))
	prevSpace := false
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			if !prevSpace {
				b.WriteByte(' ')
				prevSpace = true
			}
			continue
		}
		prevSpace = false
		b.WriteRune(r)
	}
	s = strings.ToLower(b.String())
	s = strings.TrimRight(s, ".!?,;:")
	s = strings.TrimSpace(s)
	return s
}

// TitleIndex maps a normalized title to one matching existing issue. When
// multiple existing issues share a normalized title, the first one wins —
// callers only need the URL for logging/debug output.
type TitleIndex map[string]ExistingIssue

// BuildTitleIndex indexes existing issues by their normalized title for
// O(1) duplicate lookup.
func BuildTitleIndex(existing []ExistingIssue) TitleIndex {
	idx := make(TitleIndex, len(existing))
	for _, e := range existing {
		key := NormalizeIssueTitle(e.Title)
		if key == "" {
			continue
		}
		if _, seen := idx[key]; !seen {
			idx[key] = e
		}
	}
	return idx
}

// Lookup returns the existing issue whose normalized title matches title, if
// any. The bool is false when no match is found.
func (idx TitleIndex) Lookup(title string) (ExistingIssue, bool) {
	key := NormalizeIssueTitle(title)
	if key == "" {
		return ExistingIssue{}, false
	}
	e, ok := idx[key]
	return e, ok
}
