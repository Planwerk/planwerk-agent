package claude

import (
	"fmt"

	"github.com/planwerk/planwerk-agent/internal/patterns"
	"github.com/planwerk/planwerk-agent/internal/report"
)

// AdversarialReview runs an independent adversarial review pass using a fresh Claude context.
// It focuses on security vulnerabilities, failure modes, and attack vectors.
// baseBranch scopes the review to changes relative to the given branch. pats is
// the project review-pattern catalog, injected so a pass inspecting a fresh diff
// applies the same patterns a later review of that diff would (maxPatterns
// budgets how many are rendered); an empty catalog leaves the prompt unchanged.
func (c *Client) AdversarialReview(dir, baseBranch string, pats []patterns.Pattern, maxPatterns int) (*report.ReviewResult, error) {
	rawReview, model, err := c.runAdversarialReview(dir, baseBranch, pats, maxPatterns)
	if err != nil {
		return nil, fmt.Errorf("running adversarial review: %w", err)
	}

	result, err := c.structureReview(rawReview)
	if err != nil {
		return nil, fmt.Errorf("structuring adversarial review: %w", err)
	}

	// Tag all findings as from adversarial review
	for i := range result.Findings {
		if result.Findings[i].Pattern == "" {
			result.Findings[i].Pattern = "adversarial-review"
		}
	}

	assignIDs(result)
	result.Model = model
	return result, nil
}

func (c *Client) runAdversarialReview(dir, baseBranch string, pats []patterns.Pattern, maxPatterns int) (text, model string, err error) {
	return c.runClaude(dir, buildAdversarialPrompt(baseBranch, pats, maxPatterns), "adversarial")
}

func buildAdversarialPrompt(baseBranch string, pats []patterns.Pattern, maxPatterns int) string {
	if baseBranch == "" {
		baseBranch = DefaultBaseBranch
	}
	return `You are a security researcher and chaos engineer performing an adversarial code review.
Your job is to find ways this code will fail in production.

` + diffScopeLines(baseBranch) + `Then focus your adversarial analysis ONLY on those files.

Think like:
- An attacker: How can this code be exploited? SQL injection, auth bypass, SSRF, path traversal, XSS, CSRF?
- A chaos engineer: What happens when dependencies fail? Network partitions? Disk full? OOM? Clock skew?
- A malicious insider: Could this code be used to exfiltrate data or escalate privileges?
- Murphy's Law: What is the worst thing that can happen with valid but unexpected input?

Focus ONLY on:
1. Security vulnerabilities (injection, auth bypass, crypto weaknesses, SSRF, path traversal)
2. Failure modes (what breaks when a dependency is unavailable, slow, or returns unexpected data?)
3. Race conditions and concurrency issues (TOCTOU, double-submit, concurrent mutations)
4. Data integrity risks (partial writes, lost updates, silent data corruption)
5. Denial of service vectors (unbounded allocations, CPU-intensive regexes, amplification attacks)

DO NOT comment on:
- Code style, naming, or formatting
- Missing documentation or comments
- General best practices without a concrete exploit or failure scenario
- Anything that is merely "not ideal" but has no realistic failure mode

For every finding, describe the SPECIFIC attack vector or failure scenario.
Use severity CRITICAL for exploitable vulnerabilities, WARNING for failure modes, INFO for hardening suggestions.
A finding whose confidence is "uncertain" MUST NOT be CRITICAL: a theoretical exploit you cannot ground in a quoted line caps at WARNING.

For every finding you report:
- Quote the exact 3-5 lines of vulnerable/problematic code from the diff. If you cannot quote the triggering line(s), set confidence to "uncertain" — NEVER invent, paraphrase, or reconstruct a snippet (QUOTE-OR-DEMOTE).
- Provide a concrete proof-of-concept or exploit scenario (for security findings) or failure scenario (for reliability findings)
- Provide the exact fix code for issues that can be auto-fixed
- If multiple findings are related (e.g., an injection vector and a missing input validation), note the connection by referencing the other finding's title

An empty findings array is the correct answer when the diff yields no concrete attack vector or failure scenario — do NOT manufacture a speculative finding to appear productive.

` + finderPatternCatalog("## Project review patterns\n\nApply these project review patterns where they intersect the focus areas above — a pass inspecting a fresh diff should know the same patterns a later review of that diff would apply. They do NOT widen your scope: the Focus ONLY and DO NOT comment on rules above still bound what you report.", pats, maxPatterns) + planwerkIgnoreLine() + communicationStyleBlock() + outputLanguageBlock() + findingLabelsBlock() + "/review"
}
