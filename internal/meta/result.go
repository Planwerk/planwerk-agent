package meta

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// SubIssue is one draft-depth Sub Issue carved out of a Meta Issue. The first
// group of fields is decoded from Claude's split output; the second group is
// filled in at run time as the Sub Issue is created and linked. Title and
// Description carry the house draft format — deliberately no acceptance
// criteria, affected areas, or implementation steps (that stays the job of the
// separate elaborate and implement commands).
type SubIssue struct {
	Key         string `json:"key"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Motivation  string `json:"motivation"`
	Scope       string `json:"scope"`

	// Runtime fields, populated as the Sub Issue is filed and linked.
	Number    int    `json:"number,omitempty"`
	URL       string `json:"url,omitempty"`
	Linked    bool   `json:"linked,omitempty"`
	LinkError string `json:"linkError,omitempty"`
}

// Result is the structured output of a meta split: the Sub Issues to file and
// the Meta Issue body carrying {{sub:KEY}} placeholders where each freshly
// created reference is back-filled.
type Result struct {
	SubIssues []SubIssue `json:"subIssues"`
	MetaBody  string     `json:"metaBody"`
}

// validScopes is the set of accepted Scope values, matching the house draft
// format. An empty scope is tolerated and defaults to Medium.
var validScopes = map[string]bool{"Small": true, "Medium": true, "Large": true}

// metaRefRe matches a {{sub:KEY}} placeholder and captures KEY. Used to verify
// every placeholder in the Meta body references a declared Sub Issue.
var metaRefRe = regexp.MustCompile(`\{\{sub:([^}]+)\}\}`)

// metaRefPlaceholder returns the placeholder token the split prompt inserts on
// a work-package line so the runner can substitute the created issue reference
// deterministically.
func metaRefPlaceholder(key string) string {
	return "{{sub:" + key + "}}"
}

// BuildSubIssueBody renders a Sub Issue body in the house draft format: a
// Category/Scope header line, Description and Motivation sections, and a
// generated-by footer that points back at the Meta Issue. It deliberately stops
// at draft depth — no acceptance criteria, affected areas, or implementation
// steps. Scope defaults to Medium when the model leaves it blank.
func BuildSubIssueBody(metaNumber int, s SubIssue) string {
	var b strings.Builder

	scope := strings.TrimSpace(s.Scope)
	if scope == "" {
		scope = "Medium"
	}
	fmt.Fprintf(&b, "**Category**: feature | **Scope**: %s\n\n", scope)

	if d := strings.TrimSpace(s.Description); d != "" {
		fmt.Fprintf(&b, "## Description\n\n%s\n\n", d)
	}

	if m := strings.TrimSpace(s.Motivation); m != "" {
		fmt.Fprintf(&b, "## Motivation\n\n%s\n\n", m)
	}

	fmt.Fprintf(&b, "---\n\n_Split from #%d by [planwerk-review](https://github.com/planwerk/planwerk-review) with Claude Code_\n", metaNumber)
	return b.String()
}

// applyMetaReferences substitutes each {{sub:KEY}} placeholder in body with the
// `#<number>` reference of the created Sub Issue. It returns the rewritten body
// and whether every placeholder resolved — a body still carrying a {{sub:
// token after substitution is reported as not fully resolved so the caller can
// refuse to write a body with dangling placeholders.
func applyMetaReferences(body string, refs map[string]int) (string, bool) {
	for key, number := range refs {
		body = strings.ReplaceAll(body, metaRefPlaceholder(key), "#"+strconv.Itoa(number))
	}
	allResolved := !strings.Contains(body, "{{sub:")
	return body, allResolved
}

// Validate rejects a malformed split so the run fails at the boundary instead
// of filing broken Sub Issues: keys must be present and unique, each Sub Issue
// needs a title and description, any Scope must be on-enum (empty tolerated),
// and every {{sub:KEY}} placeholder in the Meta body must reference a declared
// Sub Issue.
func (r *Result) Validate() error {
	if r == nil {
		return fmt.Errorf("nil split result")
	}
	seen := make(map[string]bool, len(r.SubIssues))
	for i, s := range r.SubIssues {
		key := strings.TrimSpace(s.Key)
		if key == "" {
			return fmt.Errorf("sub-issue %d: empty key", i+1)
		}
		if seen[key] {
			return fmt.Errorf("sub-issue %d: duplicate key %q", i+1, key)
		}
		seen[key] = true
		if strings.TrimSpace(s.Title) == "" {
			return fmt.Errorf("sub-issue %q: empty title", key)
		}
		if strings.TrimSpace(s.Description) == "" {
			return fmt.Errorf("sub-issue %q: empty description", key)
		}
		if scope := strings.TrimSpace(s.Scope); scope != "" && !validScopes[scope] {
			return fmt.Errorf("sub-issue %q: scope %q must be one of Small, Medium, Large", key, s.Scope)
		}
	}
	for _, m := range metaRefRe.FindAllStringSubmatch(r.MetaBody, -1) {
		key := strings.TrimSpace(m[1])
		if !seen[key] {
			return fmt.Errorf("meta body references undeclared sub-issue key %q", key)
		}
	}
	return nil
}
