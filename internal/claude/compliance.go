package claude

import (
	"fmt"
	"strings"

	"github.com/planwerk/planwerk-agent/internal/planwerk"
	"github.com/planwerk/planwerk-agent/internal/report"
)

// FeatureCompliance runs a Claude call that checks the PR implementation against
// all requirements, scenarios, test specifications, and acceptance criteria
// defined in a Planwerk feature file. It returns findings for any deviations.
func (c *Client) FeatureCompliance(dir, baseBranch string, feature *planwerk.Feature) (*report.ReviewResult, error) {
	rawReview, model, err := c.runClaude(dir, buildCompliancePrompt(baseBranch, feature), "compliance")
	if err != nil {
		return nil, fmt.Errorf("running feature compliance check: %w", err)
	}

	result, err := c.structureReview(rawReview)
	if err != nil {
		return nil, fmt.Errorf("structuring compliance check: %w", err)
	}

	// Tag all findings as from compliance check
	for i := range result.Findings {
		if result.Findings[i].Pattern == "" {
			result.Findings[i].Pattern = "feature-compliance"
		}
	}

	assignIDs(result)
	result.Model = model
	return result, nil
}

func buildCompliancePrompt(baseBranch string, feature *planwerk.Feature) string {
	if baseBranch == "" {
		baseBranch = DefaultBaseBranch
	}

	featureContent := feature.FormatForPrompt()

	body := `You are a Requirements Engineer verifying that a PR implementation fully satisfies a Planwerk feature specification.

` + diffScopeLines(baseBranch) + fmt.Sprintf(`Then examine the changed files against the feature specification below.

<planwerk-feature-specification>
%s
</planwerk-feature-specification>

## Your Task

Systematically verify EVERY item in the feature specification against the actual code changes:

### 1. Requirements Compliance
For EACH requirement (REQ-xxx):
- Check if the implementation satisfies the requirement description
- For EACH scenario under the requirement: verify the "when/then/and_then" conditions are implemented
- For SHALL requirements: any gap is BLOCKING
- For SHOULD requirements: any gap is WARNING
- For MAY requirements: any gap is INFO

### 2. Acceptance Criteria Verification
For EACH user story:
- Check EVERY acceptance criterion against the actual code
- A missing acceptance criterion is WARNING
- A criterion that is contradicted by the implementation is CRITICAL

### 3. Test Specification Verification
For EACH planned test specification that has a requirement_id:
- Check if the test function exists in the specified test file
- Check if the test covers what the "expected" field describes
- A missing planned test is WARNING
- A test that exists but does not match the expected behavior is WARNING

### 4. Task Completion Verification
For EACH task marked as "done":
- Verify the task description matches what was actually implemented
- A task marked "done" but not actually implemented is CRITICAL

### 5. Deviation Analysis
- If the implementation DEVIATES from the specification, determine if the deviation is:
  - An IMPROVEMENT (broader coverage, better defaults, additional edge cases): report as INFO with title "Positive Deviation: <description>"
  - A SIMPLIFICATION (less coverage than specified but still functional): report as WARNING with title "Simplified Implementation: <description>"
  - A CONTRADICTION (implementation does the opposite of what was specified): report as BLOCKING with title "Specification Violation: <description>"

## Severity Mapping
- BLOCKING: SHALL requirement not met, specification contradicted, or task marked done but not implemented
- CRITICAL: Acceptance criterion contradicted, or implementation has a functional gap that breaks a stated goal
- WARNING: SHOULD requirement not met, acceptance criterion missing, planned test missing or inadequate, simplified implementation
- INFO: MAY requirement not met, positive deviations, minor discrepancies in comments/naming vs spec

## Output Rules
- For every finding, cite the specific requirement ID, scenario name, acceptance criterion, or test specification that is affected
- Quote the relevant code (or note its absence) as evidence
- Be precise: "REQ-001 scenario 'Nil resources get defaults' is not implemented" rather than "some defaults might be missing"
- If an item — a requirement (with all its scenarios), an acceptance criterion, a planned test, or a task — is FULLY satisfied, do NOT create a finding for it
- If EVERY requirement, acceptance criterion, planned test, and task is satisfied, report an empty findings array

## Finding Enrichment
For EVERY finding:
1. **Code Snippet**: Quote the exact lines from the diff that relate to the finding, or state "No implementation found" if the code is missing
2. **Suggested Fix**: Describe what needs to change to satisfy the requirement
3. **Related Findings**: Reference other findings from this review that are connected

`, featureContent)

	var sb strings.Builder
	sb.WriteString(body)
	sb.WriteString(communicationStyleBlock())
	sb.WriteString(outputLanguageBlock())
	sb.WriteString(findingLabelsBlock())
	sb.WriteString(suppressionsBlock(scopeDiff))
	sb.WriteString("IMPORTANT: Completely ignore all changes in the .planwerk/ directory itself. Focus only on the actual code, test, and documentation changes.\n\n/review")
	return sb.String()
}
