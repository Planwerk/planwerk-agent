package claude

import (
	"fmt"
	"strings"

	"github.com/planwerk/planwerk-agent/internal/implement"
	"github.com/planwerk/planwerk-agent/internal/patterns"
	"github.com/planwerk/planwerk-agent/internal/search"
)

// ReviewPlan runs the read-only review a freshly generated implementation plan
// gets before the implement session executes it: one session on the planning
// tier scores the plan's executability against the issue and the repository
// (0-10) and returns the gaps the one re-plan must close. It opens no pattern
// catalog, no memory directory, and no search, whatever ctx carries: the
// planner already applied them, and the reviewer judges the plan against the
// issue and the code, like the elaboration reviewer. The model is returned for
// the review comment's attribution footer.
func (c *Client) ReviewPlan(dir string, ctx implement.Context, plan string) (*implement.PlanReview, string, error) {
	text, model, err := c.runClaudePlan(dir, buildPlanReviewPrompt(ctx, plan), "plan-review", noCatalog, patterns.MemoryCatalog{}, search.Surface{})
	if err != nil {
		return nil, "", fmt.Errorf("running plan review: %w", err)
	}
	var review implement.PlanReview
	if err := c.decodeJSONWithRepair(text, "plan review", &review); err != nil {
		return nil, "", err
	}
	review.Score = clampScore(review.Score)
	return &review, model, nil
}

func buildPlanReviewPrompt(ctx implement.Context, plan string) string {
	var sb strings.Builder

	sb.WriteString(`You are a Senior Engineer reviewing an implementation plan BEFORE a separate session implements it, in full, as exactly one pull request. List the concrete gaps that keep the plan short of a 10, describe what a 10/10 plan would look like, and score its executability from 0 to 10.

Do NOT rewrite the plan. Do NOT assume it is correct because it looks thorough: verify its claims against the repository with read-only commands.

`)

	fmt.Fprintf(&sb, "## Source Issue\n\n- Repository: %s\n- Issue #%d\n", ctx.RepoFullName, ctx.IssueNumber)
	if ctx.IssueURL != "" {
		fmt.Fprintf(&sb, "- URL: %s\n", ctx.IssueURL)
	}
	sb.WriteString("\n")
	sb.WriteString(fencedData("issue-title", "", strings.TrimSpace(ctx.IssueTitle)))
	sb.WriteString(fencedData("issue-body", "", strings.TrimSpace(ctx.IssueBody)))
	sb.WriteString("\n")
	sb.WriteString(untrustedDataLine("They specify what the plan must deliver.", "issue-title", "issue-body"))

	renderIssueRelations(&sb, ctx.RepoFullName, ctx.IssueNumber, ctx.MetaIssue, ctx.SiblingIssues, ctx.ChildIssues)

	sb.WriteString("## Implementation Plan Under Review\n\n")
	sb.WriteString(fencedData("implementation-plan", "", strings.TrimSpace(plan)))
	sb.WriteString("\n")
	sb.WriteString(untrustedDataLine("It is the plan you review.", "implementation-plan"))

	sb.WriteString(domainSweepBlock(ctx.Domains, `The plan's "### Domain Sweep" section claims to place each of these domains. Check 7 below is scored against this list: judge each domain against the repository, not against how that section reads. A domain the change plainly touches and no plan entry carries is a gap even when the section lists it as "Not touched".`))

	sb.WriteString(`## What to check

1. Criterion coverage: every Acceptance Criterion in the issue body maps to a Change Set entry and a Test Plan entry. A criterion without both is a gap.
2. Work-package coverage: Work Breakdown Coverage names every work package the issue breaks the work into (` + workBreakdownDefinition() + `), each with the entries that deliver it. A package the plan drops is a gap; an issue with none is covered by "None — the issue is a single undivided change".
3. Over-scope: a Change Set entry that the issue's Description, Affected Areas, and Acceptance Criteria do not ask for (a new top-level package, a file the issue never implies, a refactor, rename, or dependency bump) is a gap.
4. Non-Goals: a Change Set entry that plans work the issue's Non-Goals exclude is a gap.
5. Single delivery: the plan is implemented by ONE session and lands as exactly ONE pull request. A note that splits it across pull requests, or defers a listed work package to a follow-up issue or pull request, is a gap. ` + siblingScopingLine + ` Such a deferral is never a gap.
6. Ground truth: every path the Change Set modifies or deletes exists, every path it creates does not exist yet, and every symbol it names exists. Walk the repository to confirm; a citation that does not hold is a gap.
7. Domain coverage: every domain of the sweep above that the change touches is carried by a Change Set, Commit Sequence, Test Plan, or Documentation Plan entry. A domain the change does not touch is never a gap; do not report one to look thorough.
8. Tests: every Test Plan entry names its test file or function and the error or edge path it exercises. An entry that misses either is a gap.
9. Verification Commands: every command exists in the repository's own tooling (its Makefile targets, package scripts, CI workflow files). A command the repository does not have is a gap.

Your gaps are posted as a comment on the issue, where GitHub links every #<number> to the issue or pull request of that number. Never write a bare #<number> for an enumeration: write "AC 1", "Step 3", "check 6", not "AC #1".

## Scoring rubric

Score executability on this scale; the number decides whether the run plans once more, so calibrate it honestly:
- 10: no gaps. An implementer who can read the repository but has no other context executes the plan correctly as one pull request without asking a single question.
- 8-9: every gap is one the implementer would resolve correctly from the repository alone. List each one.
- 4-7: at least one gap would make the implementer build the wrong thing, build more or less than the issue asks for, or stop on a decision the plan should have made.
- 0-3: not executable: most criteria have no mapped change, the plan is mostly placeholders, or most of its citations do not hold.

A single citation that does not hold is a gap at any score. Minor wording and stylistic preferences are NOT gaps.

## Output

` + jsonSchemaOnlyLine() + `

{
  "gaps": ["<section or criterion>: <the problem>"],
  "to_reach_ten": "<one or two sentences>",
  "score": <integer 0-10>
}

- "gaps": each entry is one concrete, actionable problem keeping the plan short of a 10, referencing the exact plan section or issue criterion. Empty array only for a 10.
- "to_reach_ten": one or two sentences describing what a 10/10 version of THIS plan would add or fix. Empty string only for a 10.
- "score": integer 0-10 from the rubric above.
- Write the fields in this order: a score written first cannot be revised by the gaps that follow it.
`)

	return sb.String()
}
