package claude

import (
	"errors"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/planwerk/planwerk-review/internal/address"
	"github.com/planwerk/planwerk-review/internal/audit"
	"github.com/planwerk/planwerk-review/internal/doccheck"
	"github.com/planwerk/planwerk-review/internal/draft"
	"github.com/planwerk/planwerk-review/internal/elaborate"
	"github.com/planwerk/planwerk-review/internal/fix"
	"github.com/planwerk/planwerk-review/internal/gapanalysis"
	"github.com/planwerk/planwerk-review/internal/github"
	"github.com/planwerk/planwerk-review/internal/implement"
	"github.com/planwerk/planwerk-review/internal/meta"
	"github.com/planwerk/planwerk-review/internal/patterns"
	"github.com/planwerk/planwerk-review/internal/planwerk"
	"github.com/planwerk/planwerk-review/internal/propose"
	"github.com/planwerk/planwerk-review/internal/rebase"
	"github.com/planwerk/planwerk-review/internal/report"
	"github.com/planwerk/planwerk-review/internal/reviewprepared"
)

// updateGolden regenerates the prompt golden files under testdata/prompts/.
// Run `go test ./internal/claude -update` after an intentional prompt change.
var updateGolden = flag.Bool("update", false, "regenerate prompt golden files")

func assertGoldenPrompt(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", "prompts", name+".golden")
	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("creating golden dir: %v", err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("writing golden %s: %v", path, err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading golden %s: %v (run `go test ./internal/claude -update` to generate)", path, err)
	}
	if got != string(want) {
		t.Errorf("prompt %s differs from golden file %s.\nRun `go test ./internal/claude -update` if the change is intentional.\n\n--- want ---\n%s\n--- got ---\n%s", name, path, string(want), got)
	}
}

// goldenPatterns returns a deterministic pattern set covering both the
// technology and design-principle categories so FormatGroupedForPrompt
// emits both XML blocks in the snapshot.
func goldenPatterns() []patterns.Pattern {
	return []patterns.Pattern{
		{
			Name:          "Hardcoded secrets",
			ReviewArea:    "security",
			DetectionHint: "literal API keys or passwords in source",
			Severity:      "CRITICAL",
			Category:      "design-principle",
			Sources:       []patterns.Source{{Title: "OWASP ASVS", URL: "https://owasp.org/asvs"}},
			Body:          "## Rule\nSecrets MUST be loaded from environment variables or a secret manager.",
		},
		{
			Name:          "Missing context.Context parameter",
			ReviewArea:    "reliability",
			DetectionHint: "long-running Go functions without ctx",
			Severity:      "WARNING",
			Category:      "technology",
			AppliesWhen:   []string{"go"},
			Body:          "## Rule\nLong-running functions MUST accept a context.Context.",
		},
	}
}

func goldenReviewContext() ReviewContext {
	return ReviewContext{
		Patterns:    goldenPatterns(),
		MaxPatterns: 0,
		BaseBranch:  "develop",
		PRTitle:     "feat: add snapshot tests for prompt builders",
		PRBody:      "Adds golden-file tests for every prompt builder.\n\nFixes #3",
		Checklist:   "## Review Checklist\n- Verify prompt coverage\n- Verify golden files exist",
		CommitLog:   "abc1234 feat: add snapshot tests\ndef5678 chore: add -update flag",
		StaleDocs: []doccheck.StaleDocHint{
			{DocFile: "README.md", RelatedDirs: []string{"internal/claude", "internal/patterns"}},
		},
		NewFeatures: []doccheck.NewFeatureHint{
			{File: "internal/claude/prompts_golden_test.go", Description: "new test file"},
		},
		TodoContent: "- [ ] Add coverage map snapshot\n- [x] Add review prompt snapshot",
	}
}

func goldenAuditContext() audit.AuditContext {
	return audit.AuditContext{
		Patterns:    goldenPatterns(),
		MaxPatterns: 0,
		MaxFindings: 25,
		RepoName:    "planwerk/planwerk-review",
	}
}

func goldenAnalysisContext() propose.AnalysisContext {
	return propose.AnalysisContext{
		Patterns:    goldenPatterns(),
		MaxPatterns: 0,
		RepoName:    "planwerk/planwerk-review",
	}
}

func goldenFeature() *planwerk.Feature {
	return &planwerk.Feature{
		FeatureID:   "CC-0042",
		Title:       "Snapshot tests for prompt builders",
		Slug:        "prompt-snapshot-tests",
		Status:      "in-progress",
		Description: "Lock the prompt surface with golden files so drift shows up in PR diffs.",
		Stories: []planwerk.Story{
			{
				Title:    "Detect prompt drift",
				Role:     "maintainer",
				Want:     "failing tests when prompt text changes",
				SoThat:   "unintended prompt mutations cannot ship silently",
				Criteria: []string{"Golden file exists for every builder", "Tests fail when the prompt changes"},
			},
		},
		Requirements: []planwerk.Requirement{
			{
				ID:          "REQ-001",
				Description: "Snapshot tests MUST cover every prompt builder",
				Priority:    "SHALL",
				Rationale:   "Prompt mutations otherwise go unreviewed.",
				Scenarios: []planwerk.Scenario{
					{
						Name:    "Prompt text unchanged",
						When:    "the prompt builder is invoked with fixed input",
						Then:    "the output MUST equal the golden file byte-for-byte",
						AndThen: []string{"the test MUST pass"},
					},
				},
			},
		},
		Tasks: []planwerk.Task{
			{ID: "T1", Title: "Add golden helper", Description: "Write -update helper", Status: "done", Requirements: []string{"REQ-001"}},
		},
		TestSpecifications: []planwerk.TestSpecification{
			{
				TestFile:      "internal/claude/prompts_golden_test.go",
				TestFunction:  "TestBuildReviewPrompt_Golden",
				Story:         "Detect prompt drift",
				Expected:      "review prompt matches golden file",
				RequirementID: "REQ-001",
			},
		},
	}
}

func goldenElaborateContext() elaborate.Context {
	return elaborate.Context{
		Patterns:    goldenPatterns(),
		MaxPatterns: 0,
		RepoName:    "planwerk/planwerk-review",
		Issue: &github.Issue{
			Number: 42,
			Title:  "Add snapshot tests for prompt builders",
			URL:    "https://github.com/planwerk/planwerk-review/issues/42",
			Body:   "Lock the prompt surface with golden files so drift shows up in PR diffs.",
			State:  "open",
		},
	}
}

func goldenDraftContext() draft.Context {
	return draft.Context{
		Seed: "add a dark mode toggle to the settings page",
		Answers: []draft.QA{
			{Question: "Who benefits from this?", Answer: "Users who work at night."},
			{Question: "Any hard constraints?", Answer: "Must respect the OS-level preference."},
		},
	}
}

func TestBuildReviewPrompt_Golden(t *testing.T) {
	assertGoldenPrompt(t, "review", buildReviewPrompt(goldenReviewContext()))
}

func TestBuildDraftPrompt_Golden(t *testing.T) {
	assertGoldenPrompt(t, "draft", BuildDraftPrompt(goldenDraftContext()))
}

func goldenMetaContext() meta.Context {
	return meta.Context{
		Issue: &github.Issue{
			Number: 78,
			Title:  "Restructure the documentation site",
			URL:    "https://github.com/planwerk/planwerk-review/issues/78",
			Body: "Split the docs overhaul into lettered workstreams.\n\n" +
				"## Workstreams\n\n" +
				"- A. Reorganize the reference section\n" +
				"- B. Rewrite the how-to guides\n" +
				"- C. Add an explanation section",
			State: "open",
		},
	}
}

func TestBuildMetaPrompt_Golden(t *testing.T) {
	assertGoldenPrompt(t, "meta", BuildMetaPrompt(goldenMetaContext()))
}

func TestBuildDraftQuestionsPrompt_Golden(t *testing.T) {
	assertGoldenPrompt(t, "draft_questions", buildDraftQuestionsPrompt("add a dark mode toggle to the settings page"))
}

func TestBuildBareDraftPrompt_Golden(t *testing.T) {
	assertGoldenPrompt(t, "draft_bare", BuildBareDraftPrompt("add a dark mode toggle to the settings page"))
}

// goldenMetaIssue and goldenSiblingIssues describe the Meta/Sub-Issue
// neighborhood shared by the elaborate and plan "meta" goldens: the source
// issue (#42) is a Sub Issue of Meta Issue #40, which has two other Sub Issues —
// an open follow-up (#43) that already carries an open PR and a draft PR, and a
// closed, already-implemented one (#41) with no open PRs. The PRs exercise the
// <linked-prs> sub-block and its open/draft labels.
func goldenMetaIssue() *github.Issue {
	return &github.Issue{
		Owner:  "planwerk",
		Name:   "planwerk-review",
		Number: 40,
		Title:  "Lock the prompt surface against drift",
		URL:    "https://github.com/planwerk/planwerk-review/issues/40",
		Body:   "Split the prompt-safety work into self-contained Sub Issues.\n\n- Golden snapshot tests (this batch)\n- Mutation tests over the goldens (follow-up)",
		State:  "open",
	}
}

func goldenSiblingIssues() []github.Issue {
	return []github.Issue{
		{
			Owner: "planwerk", Name: "planwerk-review",
			Number: 43,
			Title:  "Add mutation tests for prompt builders",
			URL:    "https://github.com/planwerk/planwerk-review/issues/43",
			Body:   "Follow-up to the golden tests: ensure the goldens actually fail on meaningful prompt drift.",
			State:  "open",
			LinkedPRs: []github.LinkedPR{
				{Number: 57, Title: "Add mutation testing harness", URL: "https://github.com/planwerk/planwerk-review/pull/57", State: "open"},
				{Number: 58, Title: "WIP: mutate prompt builders", URL: "https://github.com/planwerk/planwerk-review/pull/58", State: "open", IsDraft: true},
			},
		},
		{
			Owner: "planwerk", Name: "planwerk-review",
			Number: 41,
			Title:  "Extract shared prompt blocks",
			URL:    "https://github.com/planwerk/planwerk-review/issues/41",
			Body:   "Already done: the shared prompt blocks live in components.go.",
			State:  "closed",
		},
	}
}

func goldenElaborateMetaContext() elaborate.Context {
	ctx := goldenElaborateContext()
	ctx.MetaIssue = goldenMetaIssue()
	ctx.SiblingIssues = goldenSiblingIssues()
	return ctx
}

func TestBuildElaboratePrompt_Golden(t *testing.T) {
	assertGoldenPrompt(t, "elaborate", buildElaboratePrompt(goldenElaborateContext()))
}

// TestBuildElaboratePrompt_Meta_Golden locks the shape when the source issue is
// a Sub Issue: the "## Meta / Sub-Issue Context" section renders the Meta Issue
// and sibling Sub Issues with the cross-issue scoping guidance.
func TestBuildElaboratePrompt_Meta_Golden(t *testing.T) {
	assertGoldenPrompt(t, "elaborate_meta", buildElaboratePrompt(goldenElaborateMetaContext()))
}

func TestBuildElaborateReviewPrompt_Golden(t *testing.T) {
	draft := "**Description:**\n\nAdd golden files for every prompt builder.\n\n**Acceptance Criteria:**\n\n- [ ] A golden file exists for every builder\n"
	assertGoldenPrompt(t, "elaborate_review", buildElaborateReviewPrompt(goldenElaborateContext(), draft))
}

func TestBuildAuditPrompt_Golden(t *testing.T) {
	assertGoldenPrompt(t, "audit", buildAuditPrompt(goldenAuditContext()))
}

func TestBuildAnalysisPrompt_Golden(t *testing.T) {
	assertGoldenPrompt(t, "analysis", buildAnalysisPrompt(goldenAnalysisContext()))
}

// TestBuildAnalysisPrompt_NoPatterns locks the fallback shape used when no
// patterns are loaded: the prompt MUST still render, without the
// pattern-injection blocks.
func TestBuildAnalysisPrompt_NoPatterns(t *testing.T) {
	assertGoldenPrompt(t, "analysis_no_patterns", buildAnalysisPrompt(propose.AnalysisContext{}))
}

func TestBuildAdversarialPrompt_Golden(t *testing.T) {
	assertGoldenPrompt(t, "adversarial", buildAdversarialPrompt("develop"))
}

func TestBuildCompliancePrompt_Golden(t *testing.T) {
	assertGoldenPrompt(t, "compliance", buildCompliancePrompt("develop", goldenFeature()))
}

func TestBuildCoveragePrompt_Golden(t *testing.T) {
	assertGoldenPrompt(t, "coverage", buildCoveragePrompt("develop"))
}

func goldenImplementContext() implement.Context {
	return implement.Context{
		Patterns:     goldenPatterns(),
		MaxPatterns:  0,
		RepoFullName: "planwerk/planwerk-review",
		IssueNumber:  42,
		IssueTitle:   "Add snapshot tests for prompt builders",
		IssueBody:    "## Description\n\nLock the prompt surface with golden files so drift shows up in PR diffs.\n\n## Acceptance Criteria\n- Golden file exists for every builder\n",
		IssueURL:     "https://github.com/planwerk/planwerk-review/issues/42",
		IssueState:   "open",
	}
}

func TestBuildImplementPrompt_Golden(t *testing.T) {
	assertGoldenPrompt(t, "implement", BuildImplementPrompt(goldenImplementContext()))
}

// TestBuildImplementPromptWithPlan_Golden locks the shape used when a
// planning session preceded the implement session: the plan is embedded
// verbatim and workflow step 3 switches from PLAN to VALIDATE.
func TestBuildImplementPromptWithPlan_Golden(t *testing.T) {
	ctx := goldenImplementContext()
	ctx.Plan = "## Implementation Plan (issue #42)\n\n### Summary\n- Add a golden file per prompt builder and a -update helper.\n\n### Status\nSTATUS: PLAN_READY"
	assertGoldenPrompt(t, "implement_with_plan", BuildImplementPrompt(ctx))
}

func goldenPlanMetaContext() implement.Context {
	ctx := goldenImplementContext()
	ctx.MetaIssue = goldenMetaIssue()
	ctx.SiblingIssues = goldenSiblingIssues()
	return ctx
}

func TestBuildPlanPrompt_Golden(t *testing.T) {
	assertGoldenPrompt(t, "plan", BuildPlanPrompt(goldenImplementContext()))
}

// TestBuildPlanPrompt_Meta_Golden locks the shape when the issue being planned
// is a Sub Issue: BuildPlanPrompt renders the "## Meta / Sub-Issue Context"
// section so the planning session scopes to this issue's slice and references
// siblings for adjacent parts.
func TestBuildPlanPrompt_Meta_Golden(t *testing.T) {
	assertGoldenPrompt(t, "plan_meta", BuildPlanPrompt(goldenPlanMetaContext()))
}

// TestBuildSimplifyFindPrompt_Golden locks the read-only ponytail-style finder
// prompt: the decision ladder, the delete-list framing, and the hard guardrail
// that keeps validation/error-handling/security/accessibility/tests off the
// flag list.
func TestBuildSimplifyFindPrompt_Golden(t *testing.T) {
	assertGoldenPrompt(t, "simplify_find", buildSimplifyFindPrompt("develop"))
}

func goldenSimplifyApplyContext() implement.SimplifyApplyContext {
	return implement.SimplifyApplyContext{
		RepoFullName: "planwerk/planwerk-review",
		BaseBranch:   "main",
		Findings: []report.Finding{
			{
				Severity:    report.SeverityWarning,
				Title:       "Single-implementation interface adds indirection",
				File:        "internal/claude/runner.go",
				Problem:     "Runner is an interface with one implementation and no test mocks.",
				Action:      "Drop the interface; call the concrete *Client directly.",
				CodeSnippet: "type Runner interface {\n\tRun(dir string) error\n}",
			},
		},
		Patterns:    goldenPatterns(),
		MaxPatterns: 0,
	}
}

// TestBuildSimplifyApplyPrompt_Golden locks the apply prompt: the findings
// delete-list, the hard guardrail, and the fold/autosquash steps that fold each
// removal into the commit it belongs to on the local branch (no push).
func TestBuildSimplifyApplyPrompt_Golden(t *testing.T) {
	assertGoldenPrompt(t, "simplify_apply", BuildSimplifyApplyPrompt(goldenSimplifyApplyContext()))
}

func goldenReviewApplyContext() implement.ReviewApplyContext {
	return implement.ReviewApplyContext{
		RepoFullName: "planwerk/planwerk-review",
		BaseBranch:   "main",
		Findings: []report.Finding{
			{
				Severity:     report.SeverityCritical,
				Title:        "Unparameterized SQL query allows injection",
				File:         "internal/store/query.go",
				Problem:      "User input is concatenated straight into the SQL string.",
				SuggestedFix: "Use a parameterized query with bind variables instead of string concatenation.",
				CodeSnippet:  "db.Query(\"SELECT * FROM users WHERE name = '\" + name + \"'\")",
			},
		},
		Patterns:    goldenPatterns(),
		MaxPatterns: 0,
	}
}

// TestBuildReviewApplyPrompt_Golden locks the review-apply prompt: the findings
// fix-list, the regression-test requirement, and the fold/autosquash steps that
// fold each fix into the commit it belongs to on the local branch (no push).
func TestBuildReviewApplyPrompt_Golden(t *testing.T) {
	assertGoldenPrompt(t, "review_apply", BuildReviewApplyPrompt(goldenReviewApplyContext()))
}

func goldenFinalizeContext() implement.FinalizeContext {
	return implement.FinalizeContext{
		RepoFullName: "planwerk/planwerk-review",
		IssueNumber:  42,
		IssueTitle:   "Add snapshot tests for prompt builders",
	}
}

// TestBuildFinalizePrompt_Golden locks the finalize prompt: the session resolves
// the base branch and change set itself, pushes the branch, and opens the draft
// PR with the mandatory "Closes #N" link — and opens nothing when the branch
// carries no commits.
func TestBuildFinalizePrompt_Golden(t *testing.T) {
	assertGoldenPrompt(t, "finalize", BuildFinalizePrompt(goldenFinalizeContext()))
}

func goldenFixContext() fix.Context {
	return fix.Context{
		RepoFullName:  "planwerk/planwerk-review",
		PRNumber:      42,
		PRTitle:       "Add the snapshot tests",
		HeadBranch:    "feat/snapshot-tests",
		BaseBranch:    "main",
		HeadSHA:       "abc1234def5678",
		Iteration:     2,
		MaxIterations: 5,
		FailedChecks: []fix.FailedCheck{
			{
				Name:          "test",
				Conclusion:    "failure",
				HTMLURL:       "https://github.com/planwerk/planwerk-review/actions/runs/99",
				OutputTitle:   "1 failing test",
				OutputSummary: "--- FAIL: TestParse",
				Logs:          "--- FAIL: TestParse (0.00s)\n    parse_test.go:12: got 1, want 2\nFAIL",
				WorkflowRunID: 99,
			},
		},
		Patterns:    goldenPatterns(),
		MaxPatterns: 0,
		Fixup:       true,
	}
}

// TestBuildFixPrompt_Golden locks the default fix prompt: each change is folded
// into the commit it belongs to (git commit --fixup + git rebase --autosquash)
// and published with git push --force-with-lease. This is independent of
// --local — the same fixup strategy is the default in temp-dir and local runs.
func TestBuildFixPrompt_Golden(t *testing.T) {
	assertGoldenPrompt(t, "fix", BuildFixPrompt(goldenFixContext()))
}

// TestBuildFixPrompt_NoFixup_Golden locks the --no-fixup fix prompt: a single
// on-top follow-up commit, a normal push, and the "NEVER force-push" hard rule.
func TestBuildFixPrompt_NoFixup_Golden(t *testing.T) {
	ctx := goldenFixContext()
	ctx.Fixup = false
	assertGoldenPrompt(t, "fix_no_fixup", BuildFixPrompt(ctx))
}

func goldenBareFixContext() fix.BareContext {
	return fix.BareContext{
		RepoFullName: "planwerk/planwerk-review",
		PRNumber:     42,
		TechTags:     []string{"go"},
		PatternCatalog: []patterns.CatalogReference{
			{
				Name:       "Hardcoded secrets",
				Severity:   "CRITICAL",
				Category:   "design-principle",
				ReviewArea: "security",
				URL:        "https://raw.githubusercontent.com/planwerk/planwerk-review/main/internal/patterns/patterns/hardcoded-secrets.md",
			},
		},
		BundledURLBase: "https://raw.githubusercontent.com/planwerk/planwerk-review/main/internal/patterns/patterns",
		Fixup:          true,
	}
}

// TestBuildBareFixPrompt_Golden locks the portable self-contained fix prompt for
// the default fixup strategy: discover the base branch, fold via
// fixup/autosquash, and publish with git push --force-with-lease.
func TestBuildBareFixPrompt_Golden(t *testing.T) {
	assertGoldenPrompt(t, "fix_bare", BuildBareFixPrompt(goldenBareFixContext()))
}

// TestBuildBareFixPrompt_NoFixup_Golden locks the --no-fixup bare prompt: a
// single on-top follow-up commit and a normal push.
func TestBuildBareFixPrompt_NoFixup_Golden(t *testing.T) {
	ctx := goldenBareFixContext()
	ctx.Fixup = false
	assertGoldenPrompt(t, "fix_bare_no_fixup", BuildBareFixPrompt(ctx))
}

func goldenAddressContext() address.Context {
	return address.Context{
		RepoFullName: "planwerk/planwerk-review",
		PRNumber:     42,
		PRTitle:      "Add the snapshot tests",
		HeadBranch:   "feat/snapshot-tests",
		BaseBranch:   "main",
		Threads: []github.ReviewThread{
			{
				ID:       "PRRT_kwDOAbc123",
				Path:     "internal/claude/runner.go",
				Line:     42,
				DiffHunk: "@@ -40,3 +40,3 @@\n-\trunClaude(dir, prompt)\n+\trunSession(dir, prompt)",
				Comments: []github.ReviewThreadComment{
					{Author: "reviewer", Body: "This should call runSession, not runClaude.", CreatedAt: "2026-06-01T10:00:00Z"},
					{Author: "author", Body: "Will fix.", CreatedAt: "2026-06-01T11:00:00Z"},
				},
			},
		},
		OneCommitPerThread: true,
		Patterns:           goldenPatterns(),
		MaxPatterns:        0,
	}
}

func goldenBareAddressContext() address.BareContext {
	return address.BareContext{
		RepoFullName: "planwerk/planwerk-review",
		PRNumber:     42,
		TechTags:     []string{"go"},
		PatternCatalog: []patterns.CatalogReference{
			{
				Name:       "Hardcoded secrets",
				Severity:   "CRITICAL",
				Category:   "design-principle",
				ReviewArea: "security",
				URL:        "https://raw.githubusercontent.com/planwerk/planwerk-review/main/internal/patterns/patterns/hardcoded-secrets.md",
			},
		},
		BundledURLBase: "https://raw.githubusercontent.com/planwerk/planwerk-review/main/internal/patterns/patterns",
	}
}

// TestBuildAddressPrompt_Golden locks the per-thread address prompt: address
// the thread, commit one focused follow-up commit, never push.
func TestBuildAddressPrompt_Golden(t *testing.T) {
	assertGoldenPrompt(t, "address", BuildAddressPrompt(goldenAddressContext()))
}

// TestBuildAddressPrompt_Aggregate_Golden locks the aggregate variant: fold
// every selected thread into one commit.
func TestBuildAddressPrompt_Aggregate_Golden(t *testing.T) {
	ctx := goldenAddressContext()
	ctx.OneCommitPerThread = false
	assertGoldenPrompt(t, "address_aggregate", BuildAddressPrompt(ctx))
}

// TestBuildBareAddressPrompt_Golden locks the portable self-contained prompt.
func TestBuildBareAddressPrompt_Golden(t *testing.T) {
	assertGoldenPrompt(t, "address_bare", BuildBareAddressPrompt(goldenBareAddressContext()))
}

func goldenRebaseConflictContext() rebase.ConflictContext {
	return rebase.ConflictContext{
		RepoFullName:    "planwerk/planwerk-review",
		PRNumber:        42,
		Onto:            "main",
		HeadBranch:      "feat/snapshot-tests",
		Commit:          github.Commit{SHA: "abc1234def5678", Subject: "Add the snapshot helper"},
		ConflictedFiles: []string{"internal/claude/runner.go", "internal/claude/runner_test.go"},
		Patterns:        goldenPatterns(),
		MaxPatterns:     0,
	}
}

func goldenRebaseAnalysisContext() rebase.AnalysisContext {
	return rebase.AnalysisContext{
		RepoFullName:    "planwerk/planwerk-review",
		PRNumber:        42,
		Onto:            "main",
		RebasedCommits:  []github.Commit{{SHA: "1111111aaaa", Subject: "Add the snapshot helper"}, {SHA: "2222222bbbb", Subject: "Wire the helper into the runner"}},
		UpstreamCommits: []github.Commit{{SHA: "9999999zzzz", Subject: "Rename runClaude to runSession"}},
		Patterns:        goldenPatterns(),
		MaxPatterns:     0,
	}
}

func goldenRebaseApplyContext() rebase.ApplyContext {
	return rebase.ApplyContext{
		RepoFullName: "planwerk/planwerk-review",
		PRNumber:     42,
		Onto:         "main",
		HeadBranch:   "feat/snapshot-tests",
		Analysis: report.RebaseAnalysis{
			Commits: []report.CommitAnalysis{
				{
					SHA:     "1111111aaaa",
					Subject: "Add the snapshot helper",
					Adjustments: []report.Adjustment{
						{
							Kind:        "renamed-symbol",
							File:        "internal/claude/runner.go",
							Detail:      "Upstream renamed runClaude to runSession; this commit still calls runClaude.",
							Action:      "Call runSession instead of runClaude.",
							UpstreamRef: "9999999 Rename runClaude to runSession",
							Confidence:  "verified",
						},
					},
				},
			},
			Summary:        "One commit references a renamed symbol.",
			Recommendation: "Apply the rename before pushing.",
		},
		Patterns:    goldenPatterns(),
		MaxPatterns: 0,
	}
}

func goldenBareRebaseContext() rebase.BareContext {
	return rebase.BareContext{
		RepoFullName: "planwerk/planwerk-review",
		PRNumber:     42,
		Onto:         "main",
		TechTags:     []string{"go"},
		PatternCatalog: []patterns.CatalogReference{
			{
				Name:       "Hardcoded secrets",
				Severity:   "CRITICAL",
				Category:   "design-principle",
				ReviewArea: "security",
				URL:        "https://raw.githubusercontent.com/planwerk/planwerk-review/main/internal/patterns/patterns/hardcoded-secrets.md",
			},
		},
		BundledURLBase: "https://raw.githubusercontent.com/planwerk/planwerk-review/main/internal/patterns/patterns",
	}
}

// TestBuildRebaseConflictPrompt_Golden locks the conflict-resolution prompt:
// resolve semantically, git add the resolved files, never continue or push.
func TestBuildRebaseConflictPrompt_Golden(t *testing.T) {
	assertGoldenPrompt(t, "rebase_conflict", BuildRebaseConflictPrompt(goldenRebaseConflictContext()))
}

// TestBuildRebaseAnalysisPrompt_Golden locks the structured analysis prompt.
func TestBuildRebaseAnalysisPrompt_Golden(t *testing.T) {
	assertGoldenPrompt(t, "rebase_analysis", BuildRebaseAnalysisPrompt(goldenRebaseAnalysisContext()))
}

// TestBuildRebaseAnalysisPrompt_NoPatterns locks the fallback shape used when
// no patterns are loaded: the prompt MUST still render, without the
// pattern-injection block.
func TestBuildRebaseAnalysisPrompt_NoPatterns(t *testing.T) {
	ctx := goldenRebaseAnalysisContext()
	ctx.Patterns = nil
	assertGoldenPrompt(t, "rebase_analysis_no_patterns", BuildRebaseAnalysisPrompt(ctx))
}

// TestBuildRebaseApplyPrompt_Golden locks the apply prompt: fold the
// adjustments into the commits they belong to, never push.
func TestBuildRebaseApplyPrompt_Golden(t *testing.T) {
	assertGoldenPrompt(t, "rebase_apply", BuildRebaseApplyPrompt(goldenRebaseApplyContext()))
}

// TestBuildBareRebasePrompt_Golden locks the portable self-contained prompt.
func TestBuildBareRebasePrompt_Golden(t *testing.T) {
	assertGoldenPrompt(t, "rebase_bare", BuildBareRebasePrompt(goldenBareRebaseContext()))
}

// The remaining goldens net the prompt builders that previously had no
// snapshot, so the prompt-design audit has a safety net across every builder
// (see docs/explanation/prompt-design.md).

// TestBuildStructurePrompt_Golden locks the review-structuring prompt: the
// finding JSON schema, the severity/actionability/confidence enums, and the
// "extract only findings actually present, invent none" rule.
func TestBuildStructurePrompt_Golden(t *testing.T) {
	raw := "## Findings\n\n- W: runner.go:42 — single-implementation interface adds indirection.\n"
	assertGoldenPrompt(t, "structure", buildStructurePrompt(raw))
}

// TestBuildProposalStructurePrompt_Golden locks the propose-structuring prompt:
// the proposal JSON schema, the priority/category/scope vocabularies, and the
// 5-20 proposal budget.
func TestBuildProposalStructurePrompt_Golden(t *testing.T) {
	raw := "The repo is a Go CLI. Suggestion: add golden tests for the prompt builders.\n"
	assertGoldenPrompt(t, "proposal_structure", buildProposalStructurePrompt(raw))
}

// TestBuildElaborateStructurePrompt_Golden locks the elaborate-structuring
// prompt, including the verbatim source title pinned into the field rules.
func TestBuildElaborateStructurePrompt_Golden(t *testing.T) {
	raw := "**Description:**\n\nAdd golden files for every prompt builder.\n\n**Acceptance Criteria:**\n\n- [ ] A golden file exists for every builder\n"
	assertGoldenPrompt(t, "elaborate_structure", buildElaborateStructurePrompt(raw, goldenElaborateContext()))
}

// TestBuildRepairPrompt_Golden locks the malformed-JSON repair prompt: the
// parse error is fed back verbatim and the model is told to output only the
// corrected JSON.
func TestBuildRepairPrompt_Golden(t *testing.T) {
	parseErr := errors.New("invalid character ']' looking for beginning of value")
	malformed := `{"findings": [}`
	assertGoldenPrompt(t, "repair", buildRepairPrompt(malformed, parseErr))
}

// TestBuildValidationRepairPrompt_Golden locks the schema-repair prompt: the
// validation error is fed back and the finding-schema rules are restated so the
// model fixes the offending finding without inventing or dropping findings.
func TestBuildValidationRepairPrompt_Golden(t *testing.T) {
	validationErr := errors.New(`finding 0: "title" must be a non-empty string`)
	invalid := `{"findings":[{"title":"","severity":"WARNING","confidence":"likely"}]}`
	assertGoldenPrompt(t, "validation_repair", buildValidationRepairPrompt(invalid, validationErr))
}

// TestBuildSpecialistPrompt_Golden locks the fan-out specialist prompt for the
// security domain: the domain-scoped framing, the shared communication/output
// blocks, and the finding-enrichment tail.
func TestBuildSpecialistPrompt_Golden(t *testing.T) {
	assertGoldenPrompt(t, "specialist_security", buildSpecialistPrompt("develop", Specialists[0].Key, Specialists[0].Focus))
}

// TestBuildVerifyImplementationPrompt_Golden locks the independent
// implementation-verification prompt: the "do NOT trust the implementation"
// framing, the change-set discovery steps, and the per-criterion classification.
func TestBuildVerifyImplementationPrompt_Golden(t *testing.T) {
	ctx := goldenImplementContext()
	assertGoldenPrompt(t, "verify_implementation", buildVerifyImplementationPrompt(ctx.IssueTitle, ctx.IssueBody))
}

func goldenBareImplementContext() implement.BareContext {
	return implement.BareContext{
		RepoFullName: "planwerk/planwerk-review",
		IssueNumber:  42,
		TechTags:     []string{"go"},
		PatternCatalog: []patterns.CatalogReference{
			{
				Name:       "Hardcoded secrets",
				Severity:   "CRITICAL",
				Category:   "design-principle",
				ReviewArea: "security",
				URL:        "https://raw.githubusercontent.com/planwerk/planwerk-review/main/internal/patterns/patterns/hardcoded-secrets.md",
			},
		},
		BundledURLBase: "https://raw.githubusercontent.com/planwerk/planwerk-review/main/internal/patterns/patterns",
	}
}

// TestBuildBareImplementPrompt_Golden locks the portable self-contained
// implement prompt: it fetches the issue itself, implements, pushes a branch,
// opens a draft PR, and reports.
func TestBuildBareImplementPrompt_Golden(t *testing.T) {
	assertGoldenPrompt(t, "implement_bare", BuildBareImplementPrompt(goldenBareImplementContext()))
}

func goldenGapAnalysisContext() gapanalysis.AnalysisContext {
	f := goldenFeature()
	f.FilePath = ".planwerk/completed/CC-0042-prompt-snapshot-tests.json"
	return gapanalysis.AnalysisContext{
		Features:    []*planwerk.Feature{f},
		Patterns:    goldenPatterns(),
		MaxPatterns: 0,
		RepoName:    "planwerk/planwerk-review",
	}
}

// TestBuildGapAnalysisPrompt_Golden locks the spec-vs-code gap analysis prompt:
// the four gap-type checks, the severity mapping, and the mandatory
// evidence-citation rules.
func TestBuildGapAnalysisPrompt_Golden(t *testing.T) {
	assertGoldenPrompt(t, "gap_analysis", buildGapAnalysisPrompt(goldenGapAnalysisContext()))
}

// TestBuildGapStructurePrompt_Golden locks the gap-structuring prompt: the
// per-feature gap JSON schema and the "never BLOCKING" severity rule.
func TestBuildGapStructurePrompt_Golden(t *testing.T) {
	raw := "Feature CC-0042: missing_test for TestBuildReviewPrompt_Golden — no such test found.\n"
	assertGoldenPrompt(t, "gap_structure", buildGapStructurePrompt(raw))
}

func goldenReviewPreparedContext(includeImproved bool) reviewprepared.AnalysisContext {
	f := goldenFeature()
	f.FilePath = ".planwerk/prepared/CC-0042-prompt-snapshot-tests.json"
	return reviewprepared.AnalysisContext{
		Features: []reviewprepared.PreparedFeature{
			{
				Feature: f,
				Raw:     []byte(`{"feature_id":"CC-0042","title":"Snapshot tests for prompt builders","status":"prepared"}`),
			},
		},
		Patterns:        goldenPatterns(),
		MaxPatterns:     0,
		RepoName:        "planwerk/planwerk-review",
		IncludeImproved: includeImproved,
	}
}

// TestBuildReviewPreparedPrompt_Golden locks the prepared-spec review prompt
// with the improved-JSON rewrite block enabled.
func TestBuildReviewPreparedPrompt_Golden(t *testing.T) {
	assertGoldenPrompt(t, "review_prepared", buildReviewPreparedPrompt(goldenReviewPreparedContext(true)))
}

// TestBuildReviewPreparedPrompt_NoImproved_Golden locks the fallback shape used
// when IncludeImproved is false: the "## Improved JSON" rewrite block is omitted.
func TestBuildReviewPreparedPrompt_NoImproved_Golden(t *testing.T) {
	assertGoldenPrompt(t, "review_prepared_no_improved", buildReviewPreparedPrompt(goldenReviewPreparedContext(false)))
}

// TestBuildReviewPreparedStructurePrompt_Golden locks the prepared-review
// structuring prompt with the improved_json schema field present.
func TestBuildReviewPreparedStructurePrompt_Golden(t *testing.T) {
	raw := "Feature CC-0042: stories[0].criteria[0] uses the vague verb \"handle\".\n"
	assertGoldenPrompt(t, "review_prepared_structure", buildReviewPreparedStructurePrompt(raw, true))
}

// TestBuildReviewPreparedStructurePrompt_NoImproved_Golden locks the structuring
// prompt when improved_json is not requested.
func TestBuildReviewPreparedStructurePrompt_NoImproved_Golden(t *testing.T) {
	raw := "Feature CC-0042: stories[0].criteria[0] uses the vague verb \"handle\".\n"
	assertGoldenPrompt(t, "review_prepared_structure_no_improved", buildReviewPreparedStructurePrompt(raw, false))
}
