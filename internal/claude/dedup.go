package claude

import (
	"fmt"
	"strings"

	"github.com/planwerk/planwerk-agent/internal/report"
)

// dedupGroups is the decode target for the structure-tier dedup fallback: each
// inner slice lists the indices (into the findings passed to DedupFindings) of
// one group of findings that describe the same underlying issue. An empty
// duplicate_groups array is the correct answer when nothing is duplicated.
type dedupGroups struct {
	DuplicateGroups [][]int `json:"duplicate_groups"`
}

// DedupFindings asks the structure tier to group findings that describe the same
// underlying issue, returning index groups (never merged findings) so the model
// only classifies and never transcribes finding content — the hygiene package
// folds each group in Go via its merge helper. It backstops the fuzzy merge
// matcher for findings that carry no file to anchor on. Fewer than two findings
// need no call and return no groups.
func (c *Client) DedupFindings(findings []report.Finding) ([][]int, error) {
	if len(findings) < 2 {
		return nil, nil
	}
	text, _, err := c.runClaudeStructure(buildDedupFindingsPrompt(findings), "dedup-findings")
	if err != nil {
		return nil, err
	}
	var groups dedupGroups
	if err := c.decodeJSONWithRepair(text, "finding dedup", &groups); err != nil {
		return nil, err
	}
	return groups.DuplicateGroups, nil
}

// buildDedupFindingsPrompt renders the numbered finding list the structure tier
// groups. It asks ONLY for duplicate groups by index and states that an empty
// duplicate_groups array is the correct answer when nothing is duplicated, so a
// genuinely distinct set is never force-grouped.
func buildDedupFindingsPrompt(findings []report.Finding) string {
	var b strings.Builder
	b.WriteString("Several code-review passes produced the findings below. Some may describe the SAME underlying issue in different words. Group the findings that are duplicates of one another.\n\n")
	b.WriteString(jsonSchemaOnlyLine())
	b.WriteString("\n\n{\n  \"duplicate_groups\": [[0, 3], [1, 5]]\n}\n\n")
	b.WriteString("Each inner array lists the 0-based indices of findings that describe the same issue. Rules:\n")
	b.WriteString("- Group ONLY findings that are genuinely the same issue — same defect, same location, or same root cause.\n")
	b.WriteString("- Every index appears in at most one group, and a group has at least two indices.\n")
	b.WriteString("- Do NOT group findings that merely touch the same file or area but describe different problems.\n")
	b.WriteString("- An empty \"duplicate_groups\" array is the correct answer when nothing is duplicated. Do NOT invent groups.\n\n")
	b.WriteString("<findings>\n")
	for i, f := range findings {
		loc := f.File
		if loc == "" {
			loc = "(no file)"
		}
		if f.Line > 0 {
			loc = fmt.Sprintf("%s:%d", loc, f.Line)
		}
		fmt.Fprintf(&b, "%d. [%s] %s — %s\n   %s\n", i, f.Severity, f.Title, loc, f.Problem)
	}
	b.WriteString("</findings>")
	return b.String()
}
