package review

import (
	"bytes"
	"strings"
	"testing"

	"github.com/planwerk/planwerk-review/internal/claude"
	"github.com/planwerk/planwerk-review/internal/github"
	"github.com/planwerk/planwerk-review/internal/planwerk"
	"github.com/planwerk/planwerk-review/internal/report"
)

// mockGitHub is a configurable in-memory GitHubClient used by renderResult tests.
// Each function is a closure the test can set. Nil closures panic on call so a
// test that uses an unexpected code path fails loudly rather than hitting the
// real gh CLI.
type mockGitHub struct {
	postPRComment         func(owner, repo string, number int, body string) (string, error)
	submitPRReview        func(owner, repo string, number int, commitSHA, body string, comments []github.ReviewComment) (string, error)
	fetchDiff             func(owner, repo string, number int) (string, error)
	fetchAndCheckout      func(ref string) (*github.PR, error)
	fetchAndCheckoutLocal func(ref string, opts github.LocalOptions) (*github.PR, error)
	fetchReviewComment    func(owner, repo string, number int) (string, bool, error)
}

func (m *mockGitHub) PostPRComment(owner, repo string, number int, body string) (string, error) {
	return m.postPRComment(owner, repo, number, body)
}

func (m *mockGitHub) SubmitPRReview(owner, repo string, number int, commitSHA, body string, comments []github.ReviewComment) (string, error) {
	return m.submitPRReview(owner, repo, number, commitSHA, body, comments)
}

func (m *mockGitHub) FetchDiff(owner, repo string, number int) (string, error) {
	return m.fetchDiff(owner, repo, number)
}

func (m *mockGitHub) FetchAndCheckout(ref string) (*github.PR, error) {
	return m.fetchAndCheckout(ref)
}

func (m *mockGitHub) FetchAndCheckoutLocal(ref string, opts github.LocalOptions) (*github.PR, error) {
	if m.fetchAndCheckoutLocal == nil {
		panic("mockGitHub.FetchAndCheckoutLocal called unexpectedly")
	}
	return m.fetchAndCheckoutLocal(ref, opts)
}

func (m *mockGitHub) FetchReviewComment(owner, repo string, number int) (string, bool, error) {
	if m.fetchReviewComment == nil {
		return "", false, nil
	}
	return m.fetchReviewComment(owner, repo, number)
}

// mockClaude is a placeholder ClaudeRunner for tests that only exercise
// renderResult paths (which do not touch Claude). All methods panic if
// invoked so unintended calls are caught immediately.
type mockClaude struct{}

func (mockClaude) Review(dir string, ctx claude.ReviewContext) (*report.ReviewResult, error) {
	panic("mockClaude.Review called unexpectedly")
}

func (mockClaude) AdversarialReview(dir, baseBranch string) (*report.ReviewResult, error) {
	panic("mockClaude.AdversarialReview called unexpectedly")
}

func (mockClaude) CoverageMap(dir, baseBranch string) (*report.CoverageResult, error) {
	panic("mockClaude.CoverageMap called unexpectedly")
}

func (mockClaude) FeatureCompliance(dir, baseBranch string, feature *planwerk.Feature) (*report.ReviewResult, error) {
	panic("mockClaude.FeatureCompliance called unexpectedly")
}

func (mockClaude) SpecialistReview(dir, baseBranch, key, focus string) (*report.ReviewResult, error) {
	panic("mockClaude.SpecialistReview called unexpectedly")
}

func newTestRunner(gh *mockGitHub) *Runner {
	return &Runner{
		Claude: mockClaude{},
		GitHub: gh,
	}
}

func TestRenderResult_PostReview(t *testing.T) {
	t.Parallel()

	var postedBody string
	gh := &mockGitHub{
		postPRComment: func(owner, repo string, number int, body string) (string, error) {
			postedBody = body
			return "https://github.com/test/repo/pull/1#issuecomment-123", nil
		},
	}

	result := &report.ReviewResult{
		Summary:        "Looks good",
		Recommendation: "Merge it",
		Findings: []report.Finding{
			{ID: "F1", Severity: report.SeverityWarning, Title: "Test finding", File: "main.go", Problem: "Issue", Action: "Fix it"},
		},
	}
	pr := &github.PR{Owner: "test", Repo: "repo", Number: 1, Title: "Test PR"}
	opts := Options{
		Format:      "markdown",
		MinSeverity: report.SeverityInfo,
		Version:     "test",
		PostReview:  true,
	}

	var out bytes.Buffer
	if err := newTestRunner(gh).renderResult(&out, result, pr, opts, nil); err != nil {
		t.Fatalf("renderResult returned error: %v", err)
	}

	// Verify output was written to the writer
	if out.Len() == 0 {
		t.Error("expected output to be written to writer")
	}

	// Verify the review was posted
	if postedBody == "" {
		t.Error("expected review to be posted as PR comment")
	}

	// Posted body should contain stdout output plus a data block
	if !strings.Contains(postedBody, out.String()[:50]) {
		t.Error("posted body should contain stdout content")
	}
	if !strings.Contains(postedBody, "planwerk-review-data") {
		t.Error("posted body should contain machine-readable data block")
	}
}

func TestRenderResult_NoPost(t *testing.T) {
	t.Parallel()

	gh := &mockGitHub{
		postPRComment: func(owner, repo string, number int, body string) (string, error) {
			t.Fatal("PostPRComment should not be called when PostReview is false")
			return "", nil
		},
	}

	result := &report.ReviewResult{
		Summary: "Looks good",
	}
	pr := &github.PR{Owner: "test", Repo: "repo", Number: 1, Title: "Test PR"}
	opts := Options{
		Format:      "markdown",
		MinSeverity: report.SeverityInfo,
		Version:     "test",
		PostReview:  false,
	}

	var out bytes.Buffer
	if err := newTestRunner(gh).renderResult(&out, result, pr, opts, nil); err != nil {
		t.Fatalf("renderResult returned error: %v", err)
	}

	if out.Len() == 0 {
		t.Error("expected output to be written to writer")
	}
}

func TestRenderResult_PostReviewError(t *testing.T) {
	t.Parallel()

	gh := &mockGitHub{
		postPRComment: func(owner, repo string, number int, body string) (string, error) {
			return "", &mockError{"post failed"}
		},
	}

	result := &report.ReviewResult{Summary: "Test"}
	pr := &github.PR{Owner: "test", Repo: "repo", Number: 1, Title: "Test PR"}
	opts := Options{
		Format:      "markdown",
		MinSeverity: report.SeverityInfo,
		Version:     "test",
		PostReview:  true,
	}

	var out bytes.Buffer
	err := newTestRunner(gh).renderResult(&out, result, pr, opts, nil)
	if err == nil {
		t.Fatal("expected error when PostPRComment fails")
	}
	if !strings.Contains(err.Error(), "posting PR comment") {
		t.Errorf("error should mention posting PR comment, got: %v", err)
	}
}

func TestRenderResult_InlineReview(t *testing.T) {
	t.Parallel()

	var submittedBody string
	var submittedComments []github.ReviewComment
	gh := &mockGitHub{
		submitPRReview: func(owner, repo string, number int, commitSHA, body string, comments []github.ReviewComment) (string, error) {
			submittedBody = body
			submittedComments = comments
			return "https://github.com/test/repo/pull/1#discussion_r123", nil
		},
		fetchDiff: func(owner, repo string, number int) (string, error) {
			return `diff --git a/main.go b/main.go
index abc..def 100644
--- a/main.go
+++ b/main.go
@@ -10,3 +10,4 @@ func main() {
 	fmt.Println("hello")
 	fmt.Println("world")
+	newFunc()
 }
`, nil
		},
	}

	result := &report.ReviewResult{
		Summary: "Test review",
		Findings: []report.Finding{
			{
				ID:            "C-001",
				Severity:      report.SeverityCritical,
				Title:         "Issue in diff",
				File:          "main.go",
				Line:          12,
				Actionability: report.ActionabilityAutoFix,
				FixClass:      report.FixClassAutoFix,
				Problem:       "Problem here",
				Action:        "Fix it",
				SuggestedFix:  "fixedCode()",
			},
			{
				ID:       "W-001",
				Severity: report.SeverityWarning,
				Title:    "Issue outside diff",
				File:     "other.go",
				Line:     100,
				Problem:  "Other problem",
				Action:   "Fix that too",
			},
		},
	}
	pr := &github.PR{Owner: "test", Repo: "repo", Number: 1, Title: "Test PR", HeadSHA: "abc123"}
	opts := Options{
		Format:       "markdown",
		MinSeverity:  report.SeverityInfo,
		Version:      "test",
		PostReview:   true,
		InlineReview: true,
	}

	var out bytes.Buffer
	if err := newTestRunner(gh).renderResult(&out, result, pr, opts, nil); err != nil {
		t.Fatalf("renderResult returned error: %v", err)
	}

	if submittedBody == "" {
		t.Error("expected inline review to be submitted")
	}

	// Only the finding in the diff should become an inline comment
	if len(submittedComments) != 1 {
		t.Fatalf("expected 1 inline comment, got %d", len(submittedComments))
	}
	if submittedComments[0].Path != "main.go" {
		t.Errorf("inline comment path = %q, want %q", submittedComments[0].Path, "main.go")
	}

	// Data block should be in the body
	if !strings.Contains(submittedBody, "planwerk-review-data") {
		t.Error("submitted body should contain data block")
	}
}

func TestRenderResult_InlineReviewFallback(t *testing.T) {
	t.Parallel()

	var postedBody string
	gh := &mockGitHub{
		postPRComment: func(owner, repo string, number int, body string) (string, error) {
			postedBody = body
			return "https://github.com/test/repo/pull/1#issuecomment-456", nil
		},
		submitPRReview: func(owner, repo string, number int, commitSHA, body string, comments []github.ReviewComment) (string, error) {
			return "", &mockError{"API error"}
		},
		fetchDiff: func(owner, repo string, number int) (string, error) {
			return "", nil
		},
	}

	result := &report.ReviewResult{Summary: "Test"}
	pr := &github.PR{Owner: "test", Repo: "repo", Number: 1, Title: "Test PR", HeadSHA: "abc123"}
	opts := Options{
		Format:       "markdown",
		MinSeverity:  report.SeverityInfo,
		Version:      "test",
		PostReview:   true,
		InlineReview: true,
	}

	var out bytes.Buffer
	if err := newTestRunner(gh).renderResult(&out, result, pr, opts, nil); err != nil {
		t.Fatalf("renderResult returned error: %v", err)
	}

	if postedBody == "" {
		t.Error("expected fallback to PR comment after inline review failure")
	}
}

type mockError struct {
	msg string
}

func (e *mockError) Error() string { return e.msg }
