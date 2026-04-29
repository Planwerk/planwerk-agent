package claude

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/planwerk/planwerk-review/internal/doccheck"
	"github.com/planwerk/planwerk-review/internal/patterns"
	"github.com/planwerk/planwerk-review/internal/report"
)

const (
	// claudeTimeout is the maximum time allowed for a Claude Code invocation.
	claudeTimeout = 15 * time.Minute
	// claudeModel pins every Claude Code invocation to Opus 4.7. Opus 4.7
	// follows instructions more literally than earlier models, which matches
	// the strict MUST/NEVER style used throughout the review prompts.
	claudeModel = "claude-opus-4-7"
	// claudeEffort sets the reasoning effort. "max" gives the model the
	// largest thinking budget — reviews are latency-tolerant and benefit
	// from the extra reasoning on tricky findings.
	claudeEffort = "max"
)

// DefaultBaseBranch is the fallback base branch name when none is specified.
const DefaultBaseBranch = "main"

// runClaude invokes `claude -p <prompt> --output-format json` in the given
// directory and returns the extracted text response. The label is used to
// tag elapsed-time progress updates printed to stderr while Claude runs.
func runClaude(dir, prompt, label string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), claudeTimeout)
	defer cancel()

	stopProgress := startProgress(label)
	defer stopProgress()

	cmd := exec.CommandContext(ctx, "claude",
		"-p", prompt,
		"--model", claudeModel,
		"--effort", claudeEffort,
		"--output-format", "json",
	)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return "", fmt.Errorf("claude: %w\nstderr: %s", err, exitErr.Stderr)
		}
		return "", fmt.Errorf("claude: %w", err)
	}
	return extractText(out)
}

// ReviewContext holds all context needed to build the review prompt.
type ReviewContext struct {
	Patterns    []patterns.Pattern
	MaxPatterns int    // max patterns to inject; <= 0 disables truncation
	MaxFindings int    // cap on findings Claude returns; <= 0 disables cap
	BaseBranch  string // PR base branch; empty falls back to DefaultBaseBranch
	PRTitle     string
	PRBody      string
	Checklist   string                    // external checklist content (empty = use built-in)
	CommitLog   string                    // git log output for scope drift detection
	StaleDocs   []doccheck.StaleDocHint   // documentation files that may need updating
	NewFeatures []doccheck.NewFeatureHint // new files that may need documentation
	TodoContent string                    // content of TODOS.md if present
}

// Review invokes `claude /review` in the given directory and returns structured findings.
// It runs two Claude calls:
//  1. `claude /review` to get the unstructured review output
//  2. `claude -p` to structure the output into JSON
func Review(dir string, ctx ReviewContext) (*report.ReviewResult, error) {
	// Step 1: Run /review
	rawReview, err := runReview(dir, ctx)
	if err != nil {
		return nil, fmt.Errorf("running /review: %w", err)
	}

	// Step 2: Structure the output into JSON
	result, err := structureReview(rawReview)
	if err != nil {
		return nil, fmt.Errorf("structuring review output: %w", err)
	}

	assignIDs(result)
	return result, nil
}

// runReview invokes `claude -p` with a prompt that includes patterns and the /review command.
func runReview(dir string, rctx ReviewContext) (string, error) {
	return runClaude(dir, buildReviewPrompt(rctx), "review")
}

// buildReviewPrompt constructs a prompt that includes patterns and triggers /review.
func buildReviewPrompt(ctx ReviewContext) string {
	var sb strings.Builder

	baseBranch := ctx.BaseBranch
	if baseBranch == "" {
		baseBranch = DefaultBaseBranch
	}

	// Review scope: pin /review to the cumulative PR diff so multi-commit PRs
	// are reviewed as a whole instead of just the latest (or first) commit.
	fmt.Fprintf(&sb, `## Review Scope (MANDATORY)

Review the FULL pull request diff — every commit between origin/%s and HEAD must be considered together as one cumulative change set.

- Run `+"`"+`git diff origin/%s...HEAD`+"`"+` to see the cumulative diff and `+"`"+`git log origin/%s..HEAD --oneline`+"`"+` to enumerate every commit on the branch.
- Every added/modified line in any commit on this branch is in scope, regardless of which commit introduced it.
- Do NOT restrict the review to HEAD alone, to the most recent commit, to the first commit, or to the working-tree diff. All commits on the branch are part of this PR.
- When a later commit fixes or supersedes something an earlier commit introduced, judge the final state — do not flag the intermediate state.

`, baseBranch, baseBranch, baseBranch)

	// Staff Engineer persona
	sb.WriteString(`You are a Staff Engineer performing a thorough code review. Apply these thinking patterns:
- "What happens at 10x scale?" — Consider load, data volume, and concurrent users
- "What's the blast radius?" — If this code fails, what else breaks?
- "What happens at 3am?" — Is the error path clear? Will oncall understand the logs?
- "Would a new team member understand this?" — Is the intent clear from the code?
- "Where are the tests?" — Does every new behavior have a test? Can I see the test fail if the code is wrong?
- "Would I find this in the docs?" — If I were a new user/developer, could I discover this feature or API from the documentation?

`)

	// Review patterns (grouped by category: technology, design-principle, project)
	if len(ctx.Patterns) > 0 {
		sb.WriteString("Before running the review, consider these review patterns grouped by category. Flag violations in your review, noting the pattern source when referencing best practice patterns:\n\n")
		sb.WriteString("<review-patterns>\n")
		sb.WriteString(patterns.FormatGroupedForPrompt(ctx.Patterns, ctx.MaxPatterns))
		sb.WriteString("</review-patterns>\n\n")
	}

	// Scope Drift Detection
	if ctx.PRTitle != "" || ctx.PRBody != "" || ctx.CommitLog != "" {
		sb.WriteString("## Scope Analysis (run FIRST, before code quality review)\n\n")
		if ctx.PRTitle != "" {
			fmt.Fprintf(&sb, "PR Title: %s\n", ctx.PRTitle)
		}
		if ctx.PRBody != "" {
			sb.WriteString("<pr-body>\n")
			sb.WriteString(ctx.PRBody)
			sb.WriteString("\n</pr-body>\n")
		}
		if ctx.CommitLog != "" {
			sb.WriteString("<commit-log>\n")
			sb.WriteString(ctx.CommitLog)
			sb.WriteString("\n</commit-log>\n")
		}
		sb.WriteString(`
Before reviewing code quality, check:
1. SCOPE CREEP: Are there files changed that seem unrelated to the PR title/description? Also cross-reference with commit messages — do any commits address unrelated concerns? Flag each as WARNING with title "Scope Creep: <file or area>"
2. MISSING REQUIREMENTS: Are there requirements mentioned in the PR description that are NOT addressed in the diff? Flag each as WARNING with title "Missing Requirement: <requirement>"
3. COMMIT COHERENCE: Do the commit messages tell a coherent story? Are there commits that seem to belong to a different PR? Flag as INFO with title "Commit Coherence: <observation>"

`)
	}

	// Review checklist (always provided — checklist.Load() returns embedded default as fallback)
	if ctx.Checklist != "" {
		sb.WriteString(ctx.Checklist)
		sb.WriteString("\n\n")
	}

	// Test & Documentation Verification
	sb.WriteString(`## Test & Documentation Verification

After completing the checklist, perform these additional checks:

### Test Completeness
For every NEW or SIGNIFICANTLY MODIFIED function/method/class in the diff:
1. Identify ALL testing conventions used in this project. Check for:
   - Unit tests: _test.go, test_*.py, *.test.ts, *.spec.ts, __tests__/
   - Integration tests: tests/integration/, test/integration/
   - E2E tests: e2e/, tests/e2e/, chainsaw/, .chainsaw/, chainsaw-test.yaml
   - Kubernetes/Infrastructure E2E: Chainsaw test manifests (chainsaw-test.yaml with apiVersion: chainsaw.kyverno.io), Helm chart tests (tests/), kuttl tests
2. Check whether the PR includes corresponding test additions or modifications FOR EACH test type the project uses
3. If the project already has unit tests, integration tests, or E2E tests, new code must include matching test types — as comprehensively as the project already does. This is CRITICAL: if a project has Chainsaw E2E tests for existing features, new features MUST also have Chainsaw tests.
4. Actively search for test directories: look for chainsaw/, e2e/, tests/, test/ directories in the repository. If they exist and contain tests for similar features, flag missing tests for the new code.
5. If no test exists for new code:
   - If the project has tests elsewhere: flag as WARNING with title "Missing Tests: <function/file>"
   - If the project has E2E tests elsewhere but none for new code: flag as WARNING with title "Missing E2E Tests: <feature/component>"
   - If the project has no test convention at all: flag as INFO with title "No Test Convention Detected"
6. Do NOT flag: trivial getters/setters, simple delegation methods, or configuration constants

### Documentation Completeness
For every NEW public API, CLI flag, configuration option, or user-facing behavior change:
1. Check whether the PR modifies documentation files (README, CHANGELOG, doc comments)
2. If new public API is exported but not documented: flag as WARNING with title "Missing Documentation: <item>"
3. If a CLI flag or config option is added without being documented: flag as WARNING with title "Undocumented Flag/Config: <name>"
4. If existing documentation references changed behavior but was not updated: flag as WARNING with title "Stale Documentation: <file>"
5. Do NOT flag: internal/private API changes, refactoring that preserves existing behavior

`)

	sb.WriteString("### Documentation Structure & Quality\n")
	sb.WriteString("When the diff touches any documentation-like path (`*.md`, `*.rst`, `*.adoc`, `docs/**`, `README*`, `CHANGELOG*`) OR consists of comment-only changes (godoc, docstrings, JSDoc, rustdoc), apply the `Documentation Structure (Diátaxis)` review pattern in addition to the completeness checks above:\n\n")
	sb.WriteString("1. Identify each changed page's intended Diátaxis mode (Tutorial / How-To / Reference / Explanation) from its location (`docs/tutorials/`, `docs/how-to/`, `docs/reference/`, `docs/explanation/`), title, or content. Flag any section that drifts into a different mode as WARNING with title \"Diátaxis Drift: <page> mixes <claimed-mode> with <actual-mode>\".\n")
	sb.WriteString("2. Comment-only diffs are in scope. A doc-comment that paraphrases the code (WHAT instead of WHY) is a finding even when no code line moved — flag as INFO with title \"Comment Restates Code: <file>:<line>\".\n")
	sb.WriteString("3. For fenced code blocks in docs, verify they still match the current API/CLI/config. Flag mismatches as CRITICAL with title \"Stale Doc Example: <file>:<approx line>\".\n")
	sb.WriteString("4. Removed or renamed public APIs, CLI flags, or config keys MUST carry an explicit deprecation block with a migration path. Flag missing deprecation as WARNING with title \"Missing Deprecation Notice: <name>\".\n")
	sb.WriteString("5. Flag terminology drift (one concept named two ways across the docs touched in this PR) as INFO with title \"Terminology Drift: <term-A>/<term-B>\".\n\n")

	// Dependency Freshness & Maintenance Verification
	sb.WriteString(`## Dependency Freshness & Maintenance Verification

When the diff introduces ANY new dependency, you MUST verify its freshness and maintenance status.

### What counts as a new dependency
- A new entry in go.mod, requirements.txt, pyproject.toml, package.json, Cargo.toml, pom.xml, build.gradle, Gemfile
- A new GitHub Action (uses: owner/action@version) in workflow YAML files
- A new container image (FROM image:tag or image references in Kubernetes manifests, docker-compose, Helm values)
- A new Helm chart dependency in Chart.yaml
- Any other external dependency introduced for the first time

### What to check for each new dependency
1. **Version Currency**: Is the pinned version the latest stable release? If the diff pins an old version (e.g. v1.2 when v3.1 is current), flag it. Minor version lag (one or two patch versions behind) is acceptable; major version lag is not.
2. **Active Maintenance**: Does the project show signs of active maintenance (recent commits, releases within the last 12 months, responsive issue tracker)? If the repository is archived, abandoned, or has had no activity for over a year, flag it.
3. **Deprecation Status**: Has the project been officially deprecated or superseded by a replacement? Check for deprecation notices in the repository README, GitHub archive status, or well-known replacements (e.g. actions/create-release is deprecated in favor of softprops/action-gh-release).

### Severity guidance
- Using a deprecated dependency: flag as CRITICAL with title "Deprecated Dependency: <name>"
- Using an unmaintained dependency (archived/abandoned): flag as CRITICAL with title "Unmaintained Dependency: <name>"
- Using a significantly outdated version when a current version exists: flag as WARNING with title "Outdated Dependency: <name> uses <version>, latest is <latest>"
- Include the recommended replacement or current version in the suggested fix.

`)

	// New feature documentation hints
	if len(ctx.NewFeatures) > 0 {
		sb.WriteString("## New Feature Documentation Hints\n\n")
		sb.WriteString("The following new files were added in this PR and may need documentation:\n\n")
		for _, nf := range ctx.NewFeatures {
			fmt.Fprintf(&sb, "- %s (%s)\n", nf.File, nf.Description)
		}
		sb.WriteString("\nCheck whether these additions are reflected in README or other documentation. Flag as WARNING with title \"Missing Documentation: <file>\" if documentation is missing for user-facing additions.\n\n")
	}

	// TODO cross-reference
	if ctx.TodoContent != "" {
		sb.WriteString("## TODO Cross-Reference\n\n")
		sb.WriteString("The project has a TODOS.md file. Review the PR against these open items:\n\n")
		sb.WriteString("<todos-content>\n")
		sb.WriteString(ctx.TodoContent)
		sb.WriteString("\n</todos-content>\n\n")
		sb.WriteString("Check:\n")
		sb.WriteString("1. Does this PR complete any TODO items? If so, flag as INFO with title \"TODO Completed: <item>\"\n")
		sb.WriteString("2. Does this PR introduce work that should be tracked as a TODO? If so, flag as INFO with title \"New TODO Needed: <description>\"\n\n")
	}

	// Documentation staleness hints
	if len(ctx.StaleDocs) > 0 {
		sb.WriteString("## Documentation Staleness Hints\n\n")
		sb.WriteString("The following documentation files may need updating based on code changes in this PR:\n\n")
		for _, doc := range ctx.StaleDocs {
			fmt.Fprintf(&sb, "- %s references code in %s which was modified\n", doc.DocFile, strings.Join(doc.RelatedDirs, ", "))
		}
		sb.WriteString("\nConsider flagging as INFO with title \"Stale Documentation: <file>\" if the docs are actually outdated.\n\n")
	}

	// False-positive suppressions
	sb.WriteString(`## Suppressions — DO NOT flag these

- TODO/FIXME comments that reference an issue tracker (e.g. TODO(#123))
- Missing tests for trivial getters/setters, simple delegation methods, or configuration constants — this does NOT suppress missing tests for functions with logic or branching
- Import ordering or formatting differences (these are handled by formatters)
- Variable naming that follows the project's existing conventions, even if you'd prefer different names
- Missing documentation on unexported/private functions or internal implementation details — this does NOT suppress missing documentation for new public APIs, CLI flags, or user-facing behavior changes
- Minor style preferences that don't affect correctness or readability
- "X is redundant with Y" when the redundancy is harmless and aids readability (defense in depth)
- Threshold or constant comments that would rot faster than the code they describe
- Assertions that already cover the behavior being tested (e.g. "this assertion could be tighter")
- Consistency-only suggestions ("use X style everywhere") with no correctness impact
- Issues that are already addressed elsewhere in the same diff — read the FULL diff before commenting
- Suggestions to "add logging" when the error path already returns a descriptive error
- "Consider using X library" when the current approach works correctly — this does NOT suppress flagging deprecated, unmaintained, or severely outdated versions of NEWLY INTRODUCED dependencies
- Code that was not changed in this diff — only review and comment on added or modified lines, never on unchanged surrounding context

`)

	// Verification of claims — anti-hallucination rules
	sb.WriteString(`## Verification of Claims

These rules are MANDATORY. Violating them produces a misleading review.

- NEVER say "this is probably tested" — name the specific test file and test function, or flag as "test coverage unknown"
- NEVER say "this is handled elsewhere" — cite the exact file and line that handles it, or say "not verified"
- NEVER say "the caller validates this" — name the caller and the validation, or say "unverified assumption"
- NEVER assume error handling exists unless you can see it in the diff or trace it in the codebase
- If you are uncertain whether something is a real issue, say "UNVERIFIED: [claim]" rather than presenting it as fact
- When referencing code outside the diff, always prefix with the file path (e.g. "In cmd/main.go:42, ...")

`)

	// Anti-sycophancy rules
	sb.WriteString(`## Communication Style

Be direct and decisive in your findings. Do NOT hedge:
- Do NOT write "you might want to consider..." — state what IS wrong
- Do NOT write "this could potentially cause..." — state what WILL happen
- Do NOT write "it might be worth looking into..." — state the specific problem
- Take a clear position on every finding. If something is wrong, say it is wrong.
- If something is fine, do not mention it at all.

`)

	// Finding enrichment for machine processing
	sb.WriteString(`## Finding Enrichment

For EVERY finding you report, you MUST include:

1. **Code Snippet**: Quote the exact 3-5 lines of problematic code from the diff. Use the original line numbers. If the issue spans multiple lines, include all affected lines.

2. **Suggested Fix**: For auto-fix findings, provide the EXACT replacement code that resolves the issue. The fix code MUST:
   - Use the exact indentation from the original file
   - Contain NO markdown fences, NO comments, NO explanations — pure replacement code only
   - Be directly copy-paste ready with no placeholders or "..." elisions
   For needs-discussion and architectural findings, describe the fix approach concretely.

3. **Line Range**: When a finding affects multiple lines, specify both the start line and end line.

4. **Confidence Level**: Rate your confidence for each finding:
   - "verified": You can see the bug/issue directly in the diff with certainty (e.g., nil dereference, SQL injection, wrong return type)
   - "likely": Strong evidence but depends on context outside the diff (e.g., missing error handling where the caller might handle it)
   - "uncertain": Potential issue that requires investigation (e.g., possible race condition, performance concern under load)

5. **Related Findings**: If two or more findings are connected (e.g., a missing nil check and a missing test for that nil check), note the relationship by referencing the other finding's title.

6. **Fix Options** (REQUIRED for needs-discussion and architectural findings; OMIT for auto-fix):
   Provide 2-3 alternative approaches (label them A, B, C). For each option supply: ` + "`approach`" + ` (one sentence), ` + "`pros`" + `, ` + "`cons`" + `, ` + "`effort`" + ` (LOW | MED | HIGH), and ` + "`risk_if_skipped`" + ` (what happens if this option is NOT chosen).
   Then pick exactly ONE option as the recommendation in ` + "`recommended_option`" + ` (matching the chosen option's id) and justify it in ` + "`recommendation_reasoning`" + ` (1-2 sentences referencing codebase patterns, the relevant review-pattern source, or project constraints).
   For ` + "`auto-fix`" + ` findings DO NOT emit options — the single ` + "`suggested_fix`" + ` from rule 2 is the entire output.

`)

	// Finding limit
	if ctx.MaxFindings > 0 {
		fmt.Fprintf(&sb, "## Finding Budget\n\nReport at most %d findings. Prioritize BLOCKING > CRITICAL > WARNING > INFO. If more exist, keep the highest-severity and most representative ones.\n\n", ctx.MaxFindings)
	}

	// Review Summary instructions
	sb.WriteString(`## Review Summary

At the end of your review, write a brief overall summary (2-4 sentences) that:
1. Mentions what the PR does well (if anything stands out positively)
2. Highlights the most important issues found
3. Gives an overall assessment of the PR quality
Keep it balanced and constructive — acknowledge good work, but be direct about problems.

`)

	sb.WriteString("IMPORTANT: Completely ignore all changes in the .planwerk/ directory. Do not create any findings for files inside .planwerk/. These are project management artifacts that are always expected in the diff.\n\n")
	sb.WriteString("/review")

	return sb.String()
}

// structureReview calls Claude to convert unstructured review text into JSON.
// If the first attempt produces invalid JSON, it retries once with the parse
// error included so Claude can correct the output.
func structureReview(rawReview string) (*report.ReviewResult, error) {
	text, err := runClaude("", buildStructurePrompt(rawReview), "structure")
	if err != nil {
		return nil, err
	}

	text = stripMarkdownFences(text)

	var result report.ReviewResult
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		// Retry once with the error fed back to Claude for correction.
		text, retryErr := runClaude("", buildRepairPrompt(text, err), "repair")
		if retryErr != nil {
			return nil, fmt.Errorf("parsing structured review as JSON: %w\nraw output:\n%s", err, text)
		}
		text = stripMarkdownFences(text)
		if retryErr := json.Unmarshal([]byte(text), &result); retryErr != nil {
			return nil, fmt.Errorf("parsing structured review as JSON (after retry): %w\nraw output:\n%s", retryErr, text)
		}
	}

	return &result, nil
}

// buildRepairPrompt asks Claude to fix malformed JSON using the parse error.
func buildRepairPrompt(malformedJSON string, parseErr error) string {
	return `The following JSON is malformed. The Go JSON parser reported this error:

` + parseErr.Error() + `

Fix the JSON so it is valid. Output ONLY the corrected JSON, nothing else.

<malformed-json>
` + malformedJSON + `
</malformed-json>`
}

func buildStructurePrompt(rawReview string) string {
	return `Convert the following code review output into structured JSON. Extract every finding mentioned.

Output ONLY valid JSON matching this exact schema (no markdown fences, no surrounding text):

{
  "findings": [
    {
      "id": "",
      "severity": "BLOCKING|CRITICAL|WARNING|INFO",
      "title": "Short title",
      "file": "path/to/file.go",
      "line": 42,
      "line_end": 45,
      "pattern": "Pattern name if triggered by a review pattern, otherwise omit",
      "actionability": "auto-fix|needs-discussion|architectural",
      "confidence": "verified|likely|uncertain",
      "problem": "Description of the problem",
      "action": "What should be done to fix it",
      "code_snippet": "The exact problematic lines from the diff, preserving indentation",
      "suggested_fix": "The exact replacement code for auto-fix findings (no markdown fences, no comments, correct indentation), or a concrete description for others",
      "fix_options": [
        {
          "id": "A",
          "approach": "One-sentence summary of the fix approach",
          "pros": "Short list of benefits",
          "cons": "Short list of drawbacks",
          "effort": "LOW|MED|HIGH",
          "risk_if_skipped": "What goes wrong if this option is NOT chosen"
        }
      ],
      "recommended_option": "A",
      "recommendation_reasoning": "1-2 sentences citing codebase pattern, review-pattern source, or project constraint",
      "related_to": ["titles of related findings from this review"]
    }
  ],
  "summary": "Overall summary: what was done well, key issues found, and overall quality assessment (2-4 sentences, balanced and constructive)",
  "recommendation": "Whether the PR should be merged and under what conditions"
}

Severity levels:
- BLOCKING: Fundamental architecture or security issues — PR must not be merged
- CRITICAL: Bugs, security vulnerabilities, severe problems — must be fixed before merge
- WARNING: Code quality issues, potential problems — should be fixed
- INFO: Style suggestions, minor improvements — optional

Actionability classification (determines fix approach):
- auto-fix: A senior engineer would apply this fix without discussion (dead code removal, N+1 query fixes, stale comment cleanup, magic number extraction, missing error wrapping, simple nil checks). These will be marked as AUTO-FIX — an agent should apply them directly.
- needs-discussion: Requires team input before fixing (security fixes, race condition resolutions, API/design changes, anything changing observable behavior). These will be marked as ASK — requires human confirmation.
- architectural: Fundamental design issue that needs a broader conversation (wrong abstraction, missing layer, significant refactor needed). These will be marked as ASK.

Confidence levels:
- verified: The issue is directly visible in the code with certainty
- likely: Strong evidence but depends on context outside the diff
- uncertain: Potential issue that requires further investigation

Field rules:
- Leave the "id" field as an empty string — it will be assigned automatically.
- "code_snippet": REQUIRED for every finding. Quote the exact lines from the diff.
- "suggested_fix": REQUIRED for auto-fix findings. Must contain ONLY the replacement code — no markdown fences, no inline comments explaining the fix, correct indentation from the original file. For other findings, provide a concrete description of what to change.
- "line_end": Include when the finding spans multiple lines. Omit if it is a single-line issue.
- "confidence": REQUIRED for every finding.
- "fix_options": REQUIRED (2-3 entries) for findings with actionability "needs-discussion" or "architectural". MUST be omitted (or an empty array) for "auto-fix" findings. Each entry: id ("A","B","C",…), approach, pros, cons, effort (LOW|MED|HIGH), risk_if_skipped.
- "recommended_option": REQUIRED when fix_options is set. Must equal one of the fix_options ids. Omit when fix_options is empty.
- "recommendation_reasoning": REQUIRED when recommended_option is set; 1-2 sentences. Omit otherwise.
- "related_to": Include titles of other findings in this review that are related. Use an empty array if none.
- If there are no findings, return an empty findings array.

<review-output>
` + rawReview + `
</review-output>`
}

type claudeResponse struct {
	Result string `json:"result"`
}

// extractText extracts the text content from Claude's JSON output envelope.
func extractText(raw []byte) (string, error) {
	var resp claudeResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		// Fall back to treating the entire output as the response text
		return string(raw), nil
	}
	return resp.Result, nil
}

// stripMarkdownFences removes ```json ... ``` wrapping that LLMs frequently add.
func stripMarkdownFences(s string) string {
	s = strings.TrimSpace(s)
	// Only strip when the block is well-formed: opening fence, newline,
	// and a matching closing fence. Otherwise leave the input untouched
	// so downstream parsers see the original content.
	if !strings.HasPrefix(s, "```") || !strings.HasSuffix(s, "```") {
		return s
	}
	idx := strings.Index(s, "\n")
	if idx == -1 {
		return s
	}
	s = s[idx+1:]
	s = strings.TrimSuffix(s, "```")
	return strings.TrimSpace(s)
}

func assignIDs(result *report.ReviewResult) {
	counters := map[report.Severity]int{
		report.SeverityBlocking: 0,
		report.SeverityCritical: 0,
		report.SeverityWarning:  0,
		report.SeverityInfo:     0,
	}
	prefixes := map[report.Severity]string{
		report.SeverityBlocking: "B",
		report.SeverityCritical: "C",
		report.SeverityWarning:  "W",
		report.SeverityInfo:     "I",
	}

	for i := range result.Findings {
		sev := report.Severity(strings.ToUpper(string(result.Findings[i].Severity)))
		result.Findings[i].Severity = sev
		result.Findings[i].Actionability = report.NormalizeActionability(string(result.Findings[i].Actionability))
		result.Findings[i].FixClass = report.DeriveFixClass(result.Findings[i].Actionability)
		result.Findings[i].Confidence = report.NormalizeConfidence(string(result.Findings[i].Confidence))
		// Auto-fix findings carry a single SuggestedFix, never an option set.
		// Strip stray options so consumers don't render a confusing table next
		// to a copy-paste-ready replacement.
		if result.Findings[i].Actionability == report.ActionabilityAutoFix {
			result.Findings[i].FixOptions = nil
			result.Findings[i].RecommendedOption = ""
			result.Findings[i].RecommendationReasoning = ""
		} else if result.Findings[i].RecommendedOption != "" {
			// Drop a recommended_option that doesn't match any option ID —
			// otherwise the renderer would point at a non-existent row.
			rec := strings.TrimSpace(result.Findings[i].RecommendedOption)
			match := false
			for _, opt := range result.Findings[i].FixOptions {
				if strings.EqualFold(strings.TrimSpace(opt.ID), rec) {
					match = true
					break
				}
			}
			if !match {
				result.Findings[i].RecommendedOption = ""
				result.Findings[i].RecommendationReasoning = ""
			}
		}
		counters[sev]++
		prefix := prefixes[sev]
		if prefix == "" {
			prefix = "X"
		}
		result.Findings[i].ID = fmt.Sprintf("%s-%03d", prefix, counters[sev])
	}

	// Resolve related_to references: map titles to assigned IDs
	titleToID := make(map[string]string)
	for _, f := range result.Findings {
		titleToID[strings.ToLower(strings.TrimSpace(f.Title))] = f.ID
	}
	for i := range result.Findings {
		for j, ref := range result.Findings[i].RelatedTo {
			if id, ok := titleToID[strings.ToLower(strings.TrimSpace(ref))]; ok {
				result.Findings[i].RelatedTo[j] = id
			}
		}
	}
}
