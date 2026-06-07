package claude

import (
	"fmt"

	"github.com/planwerk/planwerk-review/internal/report"
)

// AdversarialReview runs an independent adversarial review pass using a fresh Claude context.
// It focuses on security vulnerabilities, failure modes, and attack vectors.
// baseBranch scopes the review to changes relative to the given branch.
func AdversarialReview(dir, baseBranch string) (*report.ReviewResult, error) {
	rawReview, err := runAdversarialReview(dir, baseBranch)
	if err != nil {
		return nil, fmt.Errorf("running adversarial review: %w", err)
	}

	result, err := structureReview(rawReview)
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
	return result, nil
}

func runAdversarialReview(dir, baseBranch string) (string, error) {
	return runClaude(dir, buildAdversarialPrompt(baseBranch), "adversarial")
}

func buildAdversarialPrompt(baseBranch string) string {
	if baseBranch == "" {
		baseBranch = DefaultBaseBranch
	}
	return fmt.Sprintf(`You are a security researcher and chaos engineer performing an adversarial code review.
Your job is to find ways this code will fail in production.

SCOPE: Only review files changed in the current branch compared to origin/%s.
First run: git diff origin/%s --name-only
Then focus your adversarial analysis ONLY on those files.

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

For every finding, describe the SPECIFIC attack vector or failure scenario. Be concrete.
Use severity CRITICAL for exploitable vulnerabilities, WARNING for failure modes, INFO for hardening suggestions.

For every finding you report:
- Quote the exact 3-5 lines of vulnerable/problematic code from the diff
- Provide a concrete proof-of-concept or exploit scenario (for security findings) or failure scenario (for reliability findings)
- Provide the exact fix code for issues that can be auto-fixed
- Rate your confidence: "verified" (exploit confirmed in code), "likely" (strong evidence), "uncertain" (theoretical concern)
- If multiple findings are related (e.g., an injection vector and a missing input validation), note the connection by referencing the other finding's title

IMPORTANT: Completely ignore all changes in the .planwerk/ directory.

`, baseBranch, baseBranch) + communicationStyleBlock() + "/review"
}
