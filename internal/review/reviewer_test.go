package review

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/planwerk/planwerk-agent/internal/claude"
	"github.com/planwerk/planwerk-agent/internal/github"
	"github.com/planwerk/planwerk-agent/internal/github/githubtest"
	"github.com/planwerk/planwerk-agent/internal/hygiene"
	"github.com/planwerk/planwerk-agent/internal/patterns"
	"github.com/planwerk/planwerk-agent/internal/planwerk"
	"github.com/planwerk/planwerk-agent/internal/report"
)

// mockClaude is a placeholder ClaudeRunner for tests that only exercise
// renderResult paths (which do not touch Claude). All methods panic if
// invoked so unintended calls are caught immediately.
type mockClaude struct{}

func (mockClaude) Review(dir string, ctx claude.ReviewContext) (*report.ReviewResult, error) {
	panic("mockClaude.Review called unexpectedly")
}

func (mockClaude) AdversarialReview(dir, baseBranch, sinceRef string, pats []patterns.Pattern, maxPatterns int) (*report.ReviewResult, error) {
	panic("mockClaude.AdversarialReview called unexpectedly")
}

func (mockClaude) CoverageMap(dir, baseBranch string) (*report.CoverageResult, error) {
	panic("mockClaude.CoverageMap called unexpectedly")
}

func (mockClaude) FeatureCompliance(dir, baseBranch string, feature *planwerk.Feature) (*report.ReviewResult, error) {
	panic("mockClaude.FeatureCompliance called unexpectedly")
}

func (mockClaude) SpecialistReview(dir, baseBranch string, sp claude.Specialist, pats []patterns.Pattern, maxPatterns int) (*report.ReviewResult, error) {
	panic("mockClaude.SpecialistReview called unexpectedly")
}

func (mockClaude) DedupFindings(findings []report.Finding) ([][]int, error) {
	panic("mockClaude.DedupFindings called unexpectedly")
}

func (mockClaude) VerifyFindingClaims(dir string, findings []report.Finding) ([]hygiene.ClaimVerdict, error) {
	panic("mockClaude.VerifyFindingClaims called unexpectedly")
}

func (mockClaude) UsageTotals() report.Usage {
	return report.Usage{}
}

func newTestRunner(gh *githubtest.Fake) *Runner {
	return &Runner{
		Claude: mockClaude{},
		GitHub: gh,
	}
}

func TestRenderResult_PostReview(t *testing.T) {
	t.Parallel()

	var postedBody string
	gh := &githubtest.Fake{
		PostPRCommentFn: func(owner, repo string, number int, body string) (string, error) {
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
		Gates: &report.GateStats{
			Snippet: &report.SnippetGateStats{Examined: 1, Demoted: 0},
			Claim:   &report.ClaimGateStats{Sent: 1, Verdicts: 1, Refuted: 0},
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
	if !strings.Contains(postedBody, "planwerk-agent-data") {
		t.Error("posted body should contain machine-readable data block")
	}
	// The posted data block carries the run-level gate records so a report reader
	// (and the eval harness) accumulates the refuted/examined ratio from history.
	if !strings.Contains(postedBody, `"gates":{`) {
		t.Error("posted data block should carry the run-level gate records")
	}
}

func TestRenderResult_NoPost(t *testing.T) {
	t.Parallel()

	gh := &githubtest.Fake{
		PostPRCommentFn: func(owner, repo string, number int, body string) (string, error) {
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

// TestRenderResult_SARIFIsTheWholeOutput: under --format sarif the log is the
// only thing on stdout. The coverage map, which follows the JSON document under
// --format json, is left out, and nothing reaches GitHub, not even the prior
// review's read: the command refuses --post-review and --inline with sarif, and
// renderResult returns the log ahead of the code those flags reach, so each
// case sets one of them to prove the early return holds (decision 134).
func TestRenderResult_SARIFIsTheWholeOutput(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		inline bool
	}{
		{name: "post-review"},
		{name: "inline", inline: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			gh := &githubtest.Fake{
				PostPRCommentFn: func(owner, repo string, number int, body string) (string, error) {
					t.Fatal("a SARIF run must not post a PR comment")
					return "", nil
				},
				SubmitPRReviewFn: func(owner, repo string, number int, commitSHA, body string, comments []github.ReviewComment) (string, error) {
					t.Fatal("a SARIF run must not submit a review")
					return "", nil
				},
				FetchReviewCommentFn: func(owner, repo string, number int) (string, bool, error) {
					t.Fatal("a SARIF run must not read the prior review")
					return "", false, nil
				},
				FetchReviewThreadsFn: func(owner, repo string, number int) ([]github.ReviewThread, error) {
					t.Fatal("a SARIF run must not read the review threads")
					return nil, nil
				},
			}
			result := &report.ReviewResult{
				Summary: "One finding",
				Findings: []report.Finding{
					{ID: "W-1", Severity: report.SeverityWarning, Title: "raw SQL", File: "db.go", Line: 3, Problem: "p", Action: "a"},
				},
			}
			coverage := &report.CoverageResult{Entries: []report.CoverageEntry{
				{Function: "Query", File: "db.go", Rating: "GAP"},
			}}
			pr := &github.PR{Owner: "test", Repo: "repo", Number: 1, Title: "Test PR"}
			opts := Options{
				Format:       "sarif",
				MinSeverity:  report.SeverityInfo,
				Version:      "test",
				PostReview:   !tc.inline,
				InlineReview: tc.inline,
			}

			var out bytes.Buffer
			if err := newTestRunner(gh).renderResult(&out, result, pr, opts, coverage); err != nil {
				t.Fatalf("renderResult returned error: %v", err)
			}
			var doc map[string]any
			if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
				t.Fatalf("stdout is not one JSON document: %v\n%s", err, out.String())
			}
			if !strings.Contains(out.String(), `"version": "2.1.0"`) {
				t.Errorf("stdout is not a SARIF 2.1.0 log:\n%s", out.String())
			}
			if strings.Contains(out.String(), "Test Coverage Map") {
				t.Errorf("the coverage map must not follow the SARIF log:\n%s", out.String())
			}
		})
	}
}

func TestRenderResult_PostReviewError(t *testing.T) {
	t.Parallel()

	gh := &githubtest.Fake{
		PostPRCommentFn: func(owner, repo string, number int, body string) (string, error) {
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

// inlineTestDiff puts main.go lines 10-13 on the right side of the PR diff.
const inlineTestDiff = `diff --git a/main.go b/main.go
index abc..def 100644
--- a/main.go
+++ b/main.go
@@ -10,3 +10,4 @@ func main() {
 	fmt.Println("hello")
 	fmt.Println("world")
+	newFunc()
 }
`

// inlineTestResult holds one finding on a right-side line of inlineTestDiff
// and one outside the diff.
func inlineTestResult() *report.ReviewResult {
	return &report.ReviewResult{
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
}

// testSummaryURL and testReviewURL are the URLs the fakes answer a posted
// summary comment and a submitted review with.
const (
	testSummaryURL = "https://github.com/test/repo/pull/1#issuecomment-1"
	testReviewURL  = "https://github.com/test/repo/pull/1#pullrequestreview-1"
)

// inlineTestHeadSHA is the head commit inlineTestPR is posted at.
const inlineTestHeadSHA = "abc123"

// inlineTestPR is the pull request the inline tests post on.
func inlineTestPR() *github.PR {
	return &github.PR{Owner: "test", Repo: "repo", Number: 1, Title: "Test PR", HeadSHA: inlineTestHeadSHA}
}

// inlineTestOpts are the Options of an --inline run.
func inlineTestOpts() Options {
	return Options{
		Format:       "markdown",
		MinSeverity:  report.SeverityInfo,
		Version:      "test",
		PostReview:   true,
		InlineReview: true,
	}
}

// TestRenderResult_InlineReview: the report and its data block go into the
// summary comment, and the review carries only the inline comments and a body
// that names their count and the head commit and links that comment (decision
// 133).
func TestRenderResult_InlineReview(t *testing.T) {
	t.Parallel()

	const summaryURL = "https://github.com/test/repo/pull/1#issuecomment-123"
	var (
		summary, submittedBody string
		submittedComments      []github.ReviewComment
		posts                  []string
	)
	gh := &githubtest.Fake{
		PostPRCommentFn: func(owner, repo string, number int, body string) (string, error) {
			posts = append(posts, "PostPRComment")
			summary = body
			return summaryURL, nil
		},
		SubmitPRReviewFn: func(owner, repo string, number int, commitSHA, body string, comments []github.ReviewComment) (string, error) {
			posts = append(posts, "SubmitPRReview")
			submittedBody = body
			submittedComments = comments
			return testReviewURL, nil
		},
		FetchDiffFn: func(owner, repo string, number int) (string, error) {
			return inlineTestDiff, nil
		},
	}
	pr := inlineTestPR()

	var out bytes.Buffer
	if err := newTestRunner(gh).renderResult(&out, inlineTestResult(), pr, inlineTestOpts(), nil); err != nil {
		t.Fatalf("renderResult returned error: %v", err)
	}

	if !strings.HasPrefix(summary, out.String()) {
		t.Errorf("summary comment does not start with the report:\n%s", summary)
	}
	if sha, findings, ok := report.ParseDataBlock(summary); !ok || sha != inlineTestHeadSHA || len(findings) != 2 {
		t.Errorf("summary data block = %q, %d findings (ok=%v); want abc123 and both findings", sha, len(findings), ok)
	}

	// Only the finding in the diff becomes an inline comment.
	if len(submittedComments) != 1 || submittedComments[0].Path != "main.go" {
		t.Fatalf("inline comments = %+v, want one on main.go", submittedComments)
	}
	for _, want := range []string{"1 inline comment(s) at " + inlineTestHeadSHA, "(" + summaryURL + ")"} {
		if !strings.Contains(submittedBody, want) {
			t.Errorf("review body %q lacks %q", submittedBody, want)
		}
	}
	if strings.Contains(submittedBody, "planwerk-agent-data") || strings.Contains(submittedBody, "Issue outside diff") {
		t.Errorf("review body repeats the summary:\n%s", submittedBody)
	}
	// The summary goes first, so on a first run it sits above the review.
	if want := []string{"PostPRComment", "SubmitPRReview"}; !slices.Equal(posts, want) {
		t.Errorf("posts = %v, want %v", posts, want)
	}
}

// TestRenderResult_InlineReviewWithoutInlineCommentsPostsNoReview: a run with
// nothing to place on the diff updates the summary comment and posts no
// review, so re-reviews do not pile up summaries. A diff that cannot be
// fetched places nothing either.
func TestRenderResult_InlineReviewWithoutInlineCommentsPostsNoReview(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		diff    string
		diffErr error
	}{
		{name: "every finding outside the diff", diff: "diff --git a/x.go b/x.go\n--- a/x.go\n+++ b/x.go\n@@ -1 +1 @@\n-a\n+b\n"},
		{name: "the diff cannot be fetched", diffErr: &mockError{"HTTP 502"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			gh := &githubtest.Fake{
				FetchDiffFn: func(owner, repo string, number int) (string, error) { return tc.diff, tc.diffErr },
			}
			pr := inlineTestPR()
			if err := newTestRunner(gh).renderResult(&bytes.Buffer{}, inlineTestResult(), pr, inlineTestOpts(), nil); err != nil {
				t.Fatalf("renderResult returned error: %v", err)
			}
			if n := gh.Count("SubmitPRReview"); n != 0 {
				t.Errorf("SubmitPRReview calls = %d, want 0", n)
			}
			if n := gh.Count("PostPRComment"); n != 1 {
				t.Errorf("PostPRComment calls = %d, want 1", n)
			}
		})
	}
}

// TestRenderResult_InlineReviewSkipsUnanchored: a finding whose quote pins no
// single place gets no inline comment, even on a line inside the diff, and
// stays in the summary comment (decision 139).
func TestRenderResult_InlineReviewSkipsUnanchored(t *testing.T) {
	t.Parallel()

	var (
		summary           string
		submittedComments []github.ReviewComment
	)
	gh := &githubtest.Fake{
		PostPRCommentFn: func(owner, repo string, number int, body string) (string, error) {
			summary = body
			return testSummaryURL, nil
		},
		SubmitPRReviewFn: func(owner, repo string, number int, commitSHA, body string, comments []github.ReviewComment) (string, error) {
			submittedComments = comments
			return testReviewURL, nil
		},
		FetchDiffFn: func(owner, repo string, number int) (string, error) {
			return inlineTestDiff, nil
		},
	}

	result := &report.ReviewResult{
		Summary: "Test review",
		Findings: []report.Finding{
			{ID: "C-001", Severity: report.SeverityCritical, Title: "Anchored issue", File: "main.go", Line: 12, Problem: "p", Action: "a", LineCheck: report.LineCheckAnchored},
			{ID: "C-002", Severity: report.SeverityCritical, Title: "Unanchored issue", File: "main.go", Line: 11, Problem: "p", Action: "a", LineCheck: report.LineCheckUnanchored},
		},
	}

	if err := newTestRunner(gh).renderResult(&bytes.Buffer{}, result, inlineTestPR(), inlineTestOpts(), nil); err != nil {
		t.Fatalf("renderResult returned error: %v", err)
	}
	if len(submittedComments) != 1 || submittedComments[0].Line != 12 {
		t.Fatalf("submitted comments = %+v, want one, on line 12", submittedComments)
	}
	for _, c := range submittedComments {
		if strings.Contains(c.Body, "Unanchored issue") {
			t.Errorf("the unanchored finding got an inline comment:\n%s", c.Body)
		}
	}
	// The data block lists every finding, so look at the report above it.
	rendered, _, _ := strings.Cut(summary, "<!-- planwerk-agent-data")
	if !strings.Contains(rendered, "### C-002: Unanchored issue") {
		t.Errorf("the unanchored finding should stay in the summary report:\n%s", summary)
	}
}

// TestRenderResult_InlineReviewFailureKeepsTheSummary: when the review is
// refused, the summary comment already holds every finding and its data block,
// so the run succeeds without posting the summary a second time.
func TestRenderResult_InlineReviewFailureKeepsTheSummary(t *testing.T) {
	t.Parallel()

	gh := &githubtest.Fake{
		SubmitPRReviewFn: func(owner, repo string, number int, commitSHA, body string, comments []github.ReviewComment) (string, error) {
			return "", &mockError{"API error"}
		},
		FetchDiffFn: func(owner, repo string, number int) (string, error) {
			return inlineTestDiff, nil
		},
	}
	pr := inlineTestPR()

	if err := newTestRunner(gh).renderResult(&bytes.Buffer{}, inlineTestResult(), pr, inlineTestOpts(), nil); err != nil {
		t.Fatalf("renderResult returned error: %v", err)
	}
	if n := gh.Count("SubmitPRReview"); n != 1 {
		t.Errorf("SubmitPRReview calls = %d, want 1", n)
	}
	comments := gh.Comments()
	if len(comments) != 1 {
		t.Fatalf("posted comments = %d, want the summary once", len(comments))
	}
	sha, findings, ok := report.ParseDataBlock(comments[0])
	if !ok || sha != inlineTestHeadSHA || len(findings) != 2 {
		t.Errorf("summary data block = %q, %d findings (ok=%v); want abc123 and both findings", sha, len(findings), ok)
	}
}

// TestRenderResult_InlineSummaryFailureMovesTheSummaryIntoTheReview: when the
// summary comment is refused, the review carries the report and the data
// block, as an inline review did before the summary moved, so the next
// re-review still finds this run's findings.
func TestRenderResult_InlineSummaryFailureMovesTheSummaryIntoTheReview(t *testing.T) {
	t.Parallel()

	var submittedBody string
	gh := &githubtest.Fake{
		CommentErr: &mockError{"comments disabled"},
		SubmitPRReviewFn: func(owner, repo string, number int, commitSHA, body string, comments []github.ReviewComment) (string, error) {
			submittedBody = body
			return testReviewURL, nil
		},
		FetchDiffFn: func(owner, repo string, number int) (string, error) {
			return inlineTestDiff, nil
		},
	}
	pr := inlineTestPR()

	var out bytes.Buffer
	if err := newTestRunner(gh).renderResult(&out, inlineTestResult(), pr, inlineTestOpts(), nil); err != nil {
		t.Fatalf("renderResult returned error: %v", err)
	}
	if !strings.HasPrefix(submittedBody, out.String()) {
		t.Errorf("review body does not start with the report:\n%s", submittedBody)
	}
	if sha, findings, ok := report.ParseDataBlock(submittedBody); !ok || sha != inlineTestHeadSHA || len(findings) != 2 {
		t.Errorf("review data block = %q, %d findings (ok=%v); want abc123 and both findings", sha, len(findings), ok)
	}
}

// TestRenderResult_InlineBothPostsFailReturnsError: the run fails only when
// neither the summary comment nor the review reached the PR, and the error
// names both.
func TestRenderResult_InlineBothPostsFailReturnsError(t *testing.T) {
	t.Parallel()

	gh := &githubtest.Fake{
		CommentErr: &mockError{"comments disabled"},
		SubmitPRReviewFn: func(owner, repo string, number int, commitSHA, body string, comments []github.ReviewComment) (string, error) {
			return "", &mockError{"reviews disabled"}
		},
		FetchDiffFn: func(owner, repo string, number int) (string, error) {
			return inlineTestDiff, nil
		},
	}
	pr := inlineTestPR()

	err := newTestRunner(gh).renderResult(&bytes.Buffer{}, inlineTestResult(), pr, inlineTestOpts(), nil)
	if err == nil {
		t.Fatal("expected an error when both posts fail")
	}
	for _, want := range []string{"summary comment: comments disabled", "review: reviews disabled"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
	}
}

// TestInlineReviewBody links the summary comment only by an https URL without
// whitespace, and names it otherwise.
func TestInlineReviewBody(t *testing.T) {
	t.Parallel()

	const sha = "0123456789abcdef0123456789abcdef01234567"
	for _, tc := range []struct {
		name, url, want string
	}{
		{
			name: "https URL",
			url:  "https://github.com/o/r/pull/1#issuecomment-5",
			want: "planwerk-agent posted 3 inline comment(s) at 0123456. The summary and every finding are in [the planwerk-agent comment](https://github.com/o/r/pull/1#issuecomment-5).",
		},
		{
			name: "not a URL",
			url:  `{"data":{}}`,
			want: "planwerk-agent posted 3 inline comment(s) at 0123456. The summary and every finding are in the planwerk-agent comment on this pull request.",
		},
		{
			name: "URL followed by text",
			url:  "https://github.com/o/r/pull/1#issuecomment-5 (edited)",
			want: "planwerk-agent posted 3 inline comment(s) at 0123456. The summary and every finding are in the planwerk-agent comment on this pull request.",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := inlineReviewBody(3, sha, tc.url); got != tc.want {
				t.Errorf("inlineReviewBody =\n%s\nwant\n%s", got, tc.want)
			}
		})
	}
}

// TestRenderResult_ResolveOutdatedOffByDefault: an --inline run without
// --resolve-outdated resolves nothing and never reads the login, even with a
// thread every guard would let go. It counts resolves rather than thread
// reads, because the suppression reads the threads anyway.
func TestRenderResult_ResolveOutdatedOffByDefault(t *testing.T) {
	t.Parallel()

	gh := &githubtest.Fake{
		Threads:     []github.ReviewThread{botThread("RT_1", "gone.go", 5, 5)},
		Viewer:      "planwerk-bot",
		FetchDiffFn: func(owner, repo string, number int) (string, error) { return inlineTestDiff, nil },
	}
	pr := inlineTestPR()
	if err := newTestRunner(gh).renderResult(&bytes.Buffer{}, inlineTestResult(), pr, inlineTestOpts(), nil); err != nil {
		t.Fatalf("renderResult returned error: %v", err)
	}
	if n := gh.Count("ResolveReviewThread"); n != 0 {
		t.Errorf("ResolveReviewThread calls = %d, want 0", n)
	}
	if n := gh.Count("ViewerLogin"); n != 0 {
		t.Errorf("ViewerLogin calls = %d, want 0", n)
	}
}

// TestRenderResult_ResolveOutdatedReusesTheSuppressionThreads: with
// --resolve-outdated the run resolves from the threads the suppression already
// read, with one read in all, and checks them against the full result: a
// finding the suppression left out still keeps its thread open.
func TestRenderResult_ResolveOutdatedReusesTheSuppressionThreads(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "kept.go"), []byte("package x\n\nfunc f() {\n\tdefer f.Close()\n}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A maintainer resolved dismissed's thread, so the suppression leaves it
	// out of the report; its old outdated thread on kept.go:4 must stay open.
	dismissed := report.Finding{
		ID: "W-001", Severity: report.SeverityWarning, Confidence: report.ConfidenceVerified,
		Title: "Unchecked Close", File: "kept.go", Line: 4, CodeSnippet: "defer f.Close()", Problem: "p", Action: "a",
	}
	gh := &githubtest.Fake{
		Threads: []github.ReviewThread{
			ownThread(dismissed, "github-actions", true, "maintainer"),
			botThread("RT_kept", "kept.go", 3, 4),
			botThread("RT_gone", "gone.go", 5, 5),
		},
		Viewer:      "github-actions[bot]",
		FetchDiffFn: func(owner, repo string, number int) (string, error) { return inlineTestDiff, nil },
	}
	runner := newTestRunner(gh)
	runner.Pause = func(time.Duration) {}
	pr := inlineTestPR()
	pr.Dir = dir
	opts := inlineTestOpts()
	opts.ResolveOutdated = true

	var out bytes.Buffer
	if err := runner.renderResult(&out, &report.ReviewResult{Summary: "S", Findings: []report.Finding{dismissed}}, pr, opts, nil); err != nil {
		t.Fatalf("renderResult returned error: %v", err)
	}
	if !strings.Contains(out.String(), "inline thread was resolved") {
		t.Fatalf("the suppression did not dismiss W-001:\n%s", out.String())
	}
	if got := gh.Resolved(); !slices.Equal(got, []string{"RT_gone"}) {
		t.Errorf("resolved = %v, want [RT_gone]", got)
	}
	if n := gh.Count("FetchReviewThreads"); n != 1 {
		t.Errorf("FetchReviewThreads calls = %d, want 1", n)
	}
}

// TestRenderResult_ResolveOutdatedCountsUnanchoredFindings: an unanchored
// finding gets no inline comment, but it is still live, so the line the model
// gave it keeps an outdated thread it overlaps open (decisions 133 and 139).
func TestRenderResult_ResolveOutdatedCountsUnanchoredFindings(t *testing.T) {
	t.Parallel()

	unanchored := report.Finding{
		ID: "W-001", Severity: report.SeverityWarning, Confidence: report.ConfidenceVerified,
		Title: "Unchecked Close", File: "kept.go", Line: 4, CodeSnippet: "defer f.Close()", Problem: "p", Action: "a",
		LineCheck: report.LineCheckUnanchored,
	}
	gh := &githubtest.Fake{
		Threads: []github.ReviewThread{
			botThread("RT_kept", "kept.go", 3, 4),
			botThread("RT_gone", "gone.go", 5, 5),
		},
		Viewer:      "github-actions[bot]",
		FetchDiffFn: func(owner, repo string, number int) (string, error) { return inlineTestDiff, nil },
	}
	runner := newTestRunner(gh)
	runner.Pause = func(time.Duration) {}
	opts := inlineTestOpts()
	opts.ResolveOutdated = true

	if err := runner.renderResult(&bytes.Buffer{}, &report.ReviewResult{Summary: "S", Findings: []report.Finding{unanchored}}, inlineTestPR(), opts, nil); err != nil {
		t.Fatalf("renderResult returned error: %v", err)
	}
	if n := gh.Count("SubmitPRReview"); n != 0 {
		t.Errorf("SubmitPRReview calls = %d, want 0: the unanchored finding gets no inline comment", n)
	}
	if got := gh.Resolved(); !slices.Equal(got, []string{"RT_gone"}) {
		t.Errorf("resolved = %v, want [RT_gone]", got)
	}
}

// inlineCommentIDs returns the finding ID of each inline comment, read from the
// "**<ID>: " header report.FormatInlineComment writes.
func inlineCommentIDs(comments []github.ReviewComment) []string {
	ids := make([]string, 0, len(comments))
	for _, c := range comments {
		id, _, _ := strings.Cut(strings.TrimPrefix(c.Body, "**"), ":")
		ids = append(ids, id)
	}
	return ids
}

// TestRenderResult_InlineReviewAppliesReportFilters pins that --inline comments
// only on the findings the summary lists in a severity section (decision 129).
// A finding below --min-severity or --min-confidence is in no section and gets
// no comment; an Unverified one stays in the summary's Unverified section and
// gets none either. The summary comment's data block keeps every finding for
// the next re-review.
func TestRenderResult_InlineReviewAppliesReportFilters(t *testing.T) {
	t.Parallel()

	// Every finding sits on main.go:12, a right-side line of inlineTestDiff, so
	// only the filters can keep one from becoming an inline comment.
	onDiff := func(id string, sev report.Severity, conf report.Confidence, title string) report.Finding {
		return report.Finding{ID: id, Severity: sev, Confidence: conf, Title: title, File: "main.go", Line: 12, Problem: "Problem", Action: "Fix it"}
	}
	snippetDemoted := onDiff("W-001", report.SeverityWarning, report.ConfidenceUncertain, "Quote not found")
	snippetDemoted.SnippetCheck = "demoted: quoted code not found in the checkout"
	refuted := onDiff("C-002", report.SeverityCritical, report.ConfidenceUncertain, "Refuted claim")
	refuted.VerificationNote = "refuted: guarded at line 50"

	tests := []struct {
		name          string
		minSeverity   report.Severity
		minConfidence report.Confidence
		findings      []report.Finding
		wantInline    []string
		// wantUnverified lists the IDs the stdout report shows in its
		// Unverified / Low-Confidence section.
		wantUnverified []string
		// wantAbsent lists the IDs the stdout report leaves out entirely.
		wantAbsent []string
	}{
		{
			name:        "unverified findings stay in the summary",
			minSeverity: report.SeverityInfo,
			findings: []report.Finding{
				onDiff("C-001", report.SeverityCritical, report.ConfidenceVerified, "Verified bug"),
				snippetDemoted,
				refuted,
				// Uncertain without a refutation: it keeps its severity section,
				// and with it its inline comment.
				onDiff("B-001", report.SeverityBlocking, report.ConfidenceUncertain, "Unconfirmed blocker"),
			},
			wantInline:     []string{"C-001", "B-001"},
			wantUnverified: []string{"W-001", "C-002"},
		},
		{
			name:        "min-severity",
			minSeverity: report.SeverityWarning,
			findings: []report.Finding{
				onDiff("W-003", report.SeverityWarning, report.ConfidenceLikely, "Likely warning"),
				onDiff("I-001", report.SeverityInfo, report.ConfidenceVerified, "Verified note"),
			},
			wantInline: []string{"W-003"},
			wantAbsent: []string{"I-001"},
		},
		{
			name:          "min-confidence",
			minSeverity:   report.SeverityInfo,
			minConfidence: report.ConfidenceVerified,
			findings: []report.Finding{
				onDiff("C-001", report.SeverityCritical, report.ConfidenceVerified, "Verified bug"),
				onDiff("C-003", report.SeverityCritical, report.ConfidenceLikely, "Likely bug"),
			},
			wantInline: []string{"C-001"},
			wantAbsent: []string{"C-003"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var summary string
			var submittedComments []github.ReviewComment
			gh := &githubtest.Fake{
				PostPRCommentFn: func(owner, repo string, number int, body string) (string, error) {
					summary = body
					return testSummaryURL, nil
				},
				SubmitPRReviewFn: func(owner, repo string, number int, commitSHA, body string, comments []github.ReviewComment) (string, error) {
					submittedComments = comments
					return "https://github.com/test/repo/pull/1#discussion_r123", nil
				},
				FetchDiffFn: func(owner, repo string, number int) (string, error) {
					return inlineTestDiff, nil
				},
			}
			result := &report.ReviewResult{Summary: "Test review", Findings: tc.findings}
			pr := &github.PR{Owner: "test", Repo: "repo", Number: 1, Title: "Test PR", HeadSHA: "abc123"}
			opts := Options{
				Format:        "markdown",
				Version:       "test",
				PostReview:    true,
				InlineReview:  true,
				MinSeverity:   tc.minSeverity,
				MinConfidence: tc.minConfidence,
			}

			var out bytes.Buffer
			if err := newTestRunner(gh).renderResult(&out, result, pr, opts, nil); err != nil {
				t.Fatalf("renderResult returned error: %v", err)
			}

			if got := inlineCommentIDs(submittedComments); !slices.Equal(got, tc.wantInline) {
				t.Errorf("inline comment IDs = %v, want %v", got, tc.wantInline)
			}

			// Assert on stdout, not on the summary comment: the data block's
			// JSON names every finding.
			stdout := out.String()
			if len(tc.wantUnverified) > 0 {
				header := fmt.Sprintf("## Unverified / Low-Confidence (%d)", len(tc.wantUnverified))
				_, section, ok := strings.Cut(stdout, header)
				if !ok {
					t.Fatalf("stdout has no %q section:\n%s", header, stdout)
				}
				for _, id := range tc.wantUnverified {
					if !strings.Contains(section, "### "+id+": ") {
						t.Errorf("%s is missing from the Unverified section:\n%s", id, section)
					}
				}
			}
			for _, id := range tc.wantAbsent {
				if strings.Contains(stdout, id) {
					t.Errorf("stdout shows %s, which the thresholds leave out:\n%s", id, stdout)
				}
			}

			_, prior, ok := report.ParseDataBlock(summary)
			if !ok || len(prior) != len(tc.findings) {
				t.Errorf("data block = %d findings (ok=%v), want all %d", len(prior), ok, len(tc.findings))
			}
		})
	}
}

// TestInlineReviewComments_SkippedFindingsDoNotUseTheCap pins that the filters
// run before the maxInlineComments cap: a full cap's worth of Unverified
// findings, and another of unanchored ones, still leave room for the eligible
// finding after them.
func TestInlineReviewComments_SkippedFindingsDoNotUseTheCap(t *testing.T) {
	t.Parallel()

	var findings []report.Finding
	for i := range maxInlineComments {
		findings = append(findings, report.Finding{
			ID: fmt.Sprintf("W-%03d", i+1), Severity: report.SeverityWarning, Confidence: report.ConfidenceUncertain,
			Title: "Uncertain nit", File: "main.go", Line: 12, Problem: "p", Action: "a",
		})
	}
	for i := range maxInlineComments {
		findings = append(findings, report.Finding{
			ID: fmt.Sprintf("W-U%02d", i+1), Severity: report.SeverityWarning, Confidence: report.ConfidenceLikely,
			Title: "Unplaced warning", File: "main.go", Line: 12, Problem: "p", Action: "a",
			LineCheck: report.LineCheckUnanchored,
		})
	}
	findings = append(findings, report.Finding{
		ID: "W-LAST", Severity: report.SeverityWarning, Confidence: report.ConfidenceLikely,
		Title: "Likely warning", File: "main.go", Line: 12, Problem: "p", Action: "a",
	})

	got := inlineReviewComments(findings, github.ParseDiff(inlineTestDiff), report.SeverityInfo, "")
	if ids := inlineCommentIDs(got); !slices.Equal(ids, []string{"W-LAST"}) {
		t.Errorf("inline comment IDs = %v, want [W-LAST]", ids)
	}
}

// TestRenderResult_InlineReSuppressesOnlyWhatTheLastReviewListed runs two
// --inline reviews of the same unchanged file. A re-review counts a prior
// finding as already reported only when the last summary listed it in a
// severity section (decision 129). So a finding the first run kept off the
// diff, refuted or below --min-confidence, gets its inline comment once the
// second run confirms it, and one refuted twice stays in the Unverified
// section; only the ones the first run listed in a severity section are
// suppressed: the one it posted, and the one it kept off the diff because its
// quote pins no single place (decision 139).
func TestRenderResult_InlineReSuppressesOnlyWhatTheLastReviewListed(t *testing.T) {
	t.Parallel()

	confirmed := report.Finding{
		ID: "C-001", Severity: report.SeverityCritical, Confidence: report.ConfidenceVerified,
		Title: "Bug R", File: "unchanged.go", Line: 5, Problem: "p", Action: "a",
	}
	refuted := confirmed
	refuted.Confidence = report.ConfidenceUncertain
	refuted.VerificationNote = "refuted: guarded at line 2"
	likely := confirmed
	likely.Confidence = report.ConfidenceLikely
	unanchored := confirmed
	unanchored.LineCheck = report.LineCheckUnanchored

	tests := []struct {
		name          string
		minConfidence report.Confidence
		first, second report.Finding
		// wantFirst and wantSecond are the inline comment IDs each run posts.
		wantFirst, wantSecond []string
		// wantSuppressed is whether the second run suppresses its finding as
		// previously reported instead of listing it.
		wantSuppressed bool
	}{
		{name: "refuted, then confirmed", first: refuted, second: confirmed, wantSecond: []string{"C-001"}},
		{name: "below min-confidence, then above it", minConfidence: report.ConfidenceVerified, first: likely, second: confirmed, wantSecond: []string{"C-001"}},
		{name: "refuted twice", first: refuted, second: refuted},
		{name: "posted, then repeated", first: confirmed, second: confirmed, wantFirst: []string{"C-001"}, wantSuppressed: true},
		{name: "unanchored, then repeated", first: unanchored, second: unanchored, wantSuppressed: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// The first run reviews firstSHA, and only changed.go changes after
			// it, so the second run sees unchanged.go unchanged.
			dir, firstSHA := initGitRepoTwoCommits(t)
			pr := &github.PR{Owner: "test", Repo: "repo", Number: 1, Title: "Test PR", HeadSHA: firstSHA, Dir: dir}
			var (
				lastBody string
				comments []github.ReviewComment
			)
			gh := &githubtest.Fake{
				// The second run reads the summary comment the first one posted.
				FetchReviewCommentFn: func(owner, repo string, number int) (string, bool, error) {
					return lastBody, lastBody != "", nil
				},
				PostPRCommentFn: func(owner, repo string, number int, body string) (string, error) {
					lastBody = body
					return testSummaryURL, nil
				},
				FetchDiffFn: func(owner, repo string, number int) (string, error) {
					return "diff --git a/unchanged.go b/unchanged.go\n--- /dev/null\n+++ b/unchanged.go\n@@ -0,0 +1,10 @@\n" + strings.Repeat("+line\n", 10), nil
				},
				SubmitPRReviewFn: func(owner, repo string, number int, commitSHA, body string, cs []github.ReviewComment) (string, error) {
					comments = cs
					return testReviewURL, nil
				},
			}
			opts := Options{
				Format:        "markdown",
				Version:       "test",
				PostReview:    true,
				InlineReview:  true,
				MinSeverity:   report.SeverityInfo,
				MinConfidence: tc.minConfidence,
			}
			review := func(f report.Finding) (ids []string, stdout string) {
				comments = nil
				var out bytes.Buffer
				result := &report.ReviewResult{Summary: "S", Findings: []report.Finding{f}}
				if err := newTestRunner(gh).renderResult(&out, result, pr, opts, nil); err != nil {
					t.Fatalf("renderResult returned error: %v", err)
				}
				return inlineCommentIDs(comments), out.String()
			}

			if got, _ := review(tc.first); !slices.Equal(got, tc.wantFirst) {
				t.Fatalf("first run: inline comment IDs = %v, want %v", got, tc.wantFirst)
			}
			got, stdout := review(tc.second)
			if !slices.Equal(got, tc.wantSecond) {
				t.Errorf("second run: inline comment IDs = %v, want %v", got, tc.wantSecond)
			}
			if listed := strings.Contains(stdout, "### C-001: "); listed == tc.wantSuppressed {
				t.Errorf("second run: report lists C-001 = %v, want %v:\n%s", listed, !tc.wantSuppressed, stdout)
			}
			if noted := strings.Contains(stdout, "suppressed as previously reported"); noted != tc.wantSuppressed {
				t.Errorf("second run: suppression note = %v, want %v:\n%s", noted, tc.wantSuppressed, stdout)
			}
		})
	}
}

// ownThread is a review thread planwerk-agent opened for f, the way GitHub
// returns it: its first comment is f's inline comment as SubmitPRReview posts
// it, inline signature included, written by author in review PRR_1.
func ownThread(f report.Finding, author string, resolved bool, resolvedBy string) github.ReviewThread {
	return github.ReviewThread{
		ID:         "RT_" + f.ID,
		IsResolved: resolved,
		ResolvedBy: resolvedBy,
		Path:       f.File,
		Line:       f.Line,
		Comments: []github.ReviewThreadComment{
			{Author: author, Body: report.FormatInlineComment(f) + "\n<!-- planwerk-agent-inline -->", ReviewID: "PRR_1"},
		},
	}
}

// postedIn returns t as the review review opened it.
func postedIn(t github.ReviewThread, review string) github.ReviewThread {
	t.ID += "-" + review
	t.Comments = slices.Clone(t.Comments)
	t.Comments[0].ReviewID = review
	return t
}

func TestDismissedFingerprints(t *testing.T) {
	t.Parallel()

	a := report.Finding{ID: "W-001", Severity: report.SeverityWarning, Title: "A", File: "a.go", CodeSnippet: "defer f.Close()", Problem: "p"}
	b := report.Finding{ID: "W-002", Severity: report.SeverityWarning, Title: "B", File: "b.go", CodeSnippet: "db.Exec(query)", Problem: "p"}
	reposted := report.Finding{ID: "W-003", Severity: report.SeverityWarning, Title: "C", File: "c.go", CodeSnippet: "go run()", Problem: "p"}
	noReview := report.Finding{ID: "W-004", Severity: report.SeverityWarning, Title: "D", File: "d.go", CodeSnippet: "os.Exit(1)", Problem: "p"}
	unmarked := ownThread(a, "planwerk-bot", true, "maintainer")
	unmarked.Comments[0].Body = "**W-009: Posted before the marker** | WARNING\n\n<!-- planwerk-agent-inline -->"
	foreign := ownThread(a, "alice", true, "maintainer")
	foreign.Comments[0].Body = report.FormatInlineComment(a)

	threads := []github.ReviewThread{
		ownThread(a, "planwerk-bot", true, "maintainer"),
		// The posting identity resolved it, compared case-insensitively: a
		// local run's own resolve under a personal token.
		ownThread(a, "planwerk-bot", true, "Planwerk-Bot"),
		// GitHub names no resolver, as when the Actions bot resolves its own
		// thread.
		ownThread(a, "github-actions", true, ""),
		// Not resolved; the resolver is set only to isolate the check.
		ownThread(a, "planwerk-bot", false, "maintainer"),
		foreign,
		unmarked,
		// Two findings on the same code in one review are two.
		ownThread(b, "planwerk-bot", true, "alice"),
		ownThread(b, "planwerk-bot", true, "maintainer"),
		// One finding a later run posted again, its file having changed while
		// its first thread was open, is one.
		ownThread(reposted, "planwerk-bot", true, "maintainer"),
		postedIn(ownThread(reposted, "planwerk-bot", true, "maintainer"), "PRR_2"),
		postedIn(ownThread(reposted, "planwerk-bot", true, "maintainer"), "PRR_3"),
		// A thread GitHub names no review for adds to no other.
		postedIn(ownThread(noReview, "planwerk-bot", true, "maintainer"), ""),
		postedIn(ownThread(noReview, "planwerk-bot", true, "maintainer"), ""),
	}

	got := dismissedFingerprints(threads)
	want := map[string]int{
		report.FindingFingerprint(a):        1,
		report.FindingFingerprint(b):        2,
		report.FindingFingerprint(reposted): 1,
		report.FindingFingerprint(noReview): 1,
	}
	if !maps.Equal(got, want) {
		t.Errorf("dismissedFingerprints = %v, want %v", got, want)
	}
}

// TestRenderResult_ResolvedThreadDismissesFinding: a finding whose own inline
// thread a maintainer resolved stays out of the report sections while the code
// it quotes is unchanged, even with no prior data block and a reworded title
// on another line. The note lists it and the data block keeps it (decision
// 132). Each subtest breaks one condition, and the finding is reported again.
func TestRenderResult_ResolvedThreadDismissesFinding(t *testing.T) {
	t.Parallel()

	const source = "package main\n\nfunc save(f *os.File) error {\n\tdefer f.Close()\n\treturn writeAll(f, payload)\n}\n"
	finding := report.Finding{
		ID: "W-001", Severity: report.SeverityWarning, Confidence: report.ConfidenceVerified,
		Title: "Unchecked Close error", File: "main.go", Line: 4,
		CodeSnippet: "defer f.Close()\nreturn writeAll(f, payload)", Problem: "p", Action: "a",
	}
	// posted is the same finding as the earlier run that opened the thread
	// worded and placed it.
	posted := finding
	posted.Title = "Close error ignored"
	posted.Line = 3

	tests := []struct {
		name          string
		resolvedBy    string
		unresolved    bool
		source        string
		threadsErr    error
		wantDismissed bool
	}{
		{name: "resolved by a maintainer", resolvedBy: "maintainer", source: source, wantDismissed: true},
		{name: "resolved by the posting account", resolvedBy: "planwerk-bot", source: source},
		{name: "no resolver named", source: source},
		{name: "quoted code edited", resolvedBy: "maintainer", source: strings.Replace(source, "payload", "body", 1)},
		{name: "thread unresolved", resolvedBy: "maintainer", unresolved: true, source: source},
		{name: "threads unavailable", resolvedBy: "maintainer", source: source, threadsErr: &mockError{"graphql: rate limited"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(tc.source), 0o600); err != nil {
				t.Fatal(err)
			}
			var postedBody string
			gh := &githubtest.Fake{
				Threads:    []github.ReviewThread{ownThread(posted, "planwerk-bot", !tc.unresolved, tc.resolvedBy)},
				ThreadsErr: tc.threadsErr,
				PostPRCommentFn: func(owner, repo string, number int, body string) (string, error) {
					postedBody = body
					return testSummaryURL, nil
				},
			}
			pr := &github.PR{Owner: "test", Repo: "repo", Number: 1, Title: "Test PR", HeadSHA: "abc123", Dir: dir}
			opts := Options{Format: "markdown", MinSeverity: report.SeverityInfo, Version: "test", PostReview: true}
			result := &report.ReviewResult{Summary: "S", Findings: []report.Finding{finding}}

			var out bytes.Buffer
			if err := newTestRunner(gh).renderResult(&out, result, pr, opts, nil); err != nil {
				t.Fatalf("renderResult returned error: %v", err)
			}
			stdout := out.String()
			if listed := strings.Contains(stdout, "### W-001: "); listed == tc.wantDismissed {
				t.Errorf("report lists W-001 = %v, want %v:\n%s", listed, !tc.wantDismissed, stdout)
			}
			if noted := strings.Contains(stdout, "inline thread was resolved"); noted != tc.wantDismissed {
				t.Errorf("dismissal note = %v, want %v:\n%s", noted, tc.wantDismissed, stdout)
			}
			if strings.Contains(stdout, "[!NOTE]") && !tc.wantDismissed {
				t.Errorf("a note was written though nothing was left out:\n%s", stdout)
			}
			_, findings, ok := report.ParseDataBlock(postedBody)
			if !ok || len(findings) != 1 || findings[0].ID != "W-001" {
				t.Errorf("posted data block = %+v (ok=%v), want it to keep W-001", findings, ok)
			}
		})
	}
}

// TestRenderResult_DismissedFindingGetsNoInlineComment: under --inline a
// dismissed finding gets no new inline comment, while the summary comment's
// data block still carries it.
func TestRenderResult_DismissedFindingGetsNoInlineComment(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	const source = "package main\n\nfunc main() {\n\tfmt.Println(\"hello\")\n\tfmt.Println(\"world\")\n\tnewFunc()\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	// Both findings sit on right-side lines of inlineTestDiff.
	dismissed := report.Finding{
		ID: "W-001", Severity: report.SeverityWarning, Confidence: report.ConfidenceVerified,
		Title: "Unexplained call", File: "main.go", Line: 12, CodeSnippet: "newFunc()", Problem: "p", Action: "a",
	}
	listed := report.Finding{
		ID: "W-002", Severity: report.SeverityWarning, Confidence: report.ConfidenceVerified,
		Title: "Debug output", File: "main.go", Line: 11, CodeSnippet: `fmt.Println("world")`, Problem: "p", Action: "a",
	}

	var (
		body     string
		comments []github.ReviewComment
	)
	gh := &githubtest.Fake{
		Threads:     []github.ReviewThread{ownThread(dismissed, "github-actions", true, "maintainer")},
		FetchDiffFn: func(owner, repo string, number int) (string, error) { return inlineTestDiff, nil },
		PostPRCommentFn: func(owner, repo string, number int, b string) (string, error) {
			body = b
			return testSummaryURL, nil
		},
		SubmitPRReviewFn: func(owner, repo string, number int, commitSHA, b string, cs []github.ReviewComment) (string, error) {
			comments = cs
			return testReviewURL, nil
		},
	}
	pr := &github.PR{Owner: "test", Repo: "repo", Number: 1, Title: "Test PR", HeadSHA: "abc123", Dir: dir}
	opts := Options{Format: "markdown", MinSeverity: report.SeverityInfo, Version: "test", PostReview: true, InlineReview: true}
	result := &report.ReviewResult{Summary: "S", Findings: []report.Finding{dismissed, listed}}

	if err := newTestRunner(gh).renderResult(&bytes.Buffer{}, result, pr, opts, nil); err != nil {
		t.Fatalf("renderResult returned error: %v", err)
	}
	if ids := inlineCommentIDs(comments); !slices.Equal(ids, []string{"W-002"}) {
		t.Errorf("inline comment IDs = %v, want [W-002]", ids)
	}
	_, findings, ok := report.ParseDataBlock(body)
	if !ok || len(findings) != 2 {
		t.Errorf("summary comment data block = %+v (ok=%v), want both findings", findings, ok)
	}
}

// TestWriteSuppressionNote pins the note's shape: one alert with a group per
// reason. With only previously reported findings it is the note from before
// resolved threads dismissed anything, byte for byte.
func TestWriteSuppressionNote(t *testing.T) {
	t.Parallel()

	nit := report.Finding{Title: "Nit A", File: "unchanged.go"}
	bug := report.Finding{Title: "Bug B", File: "changed.go"}
	tests := []struct {
		name string
		s    suppressed
		want string
	}{
		{
			name: "previously reported only",
			s:    suppressed{unchanged: []report.Finding{nit, bug}},
			want: "> [!NOTE]\n> 2 finding(s) suppressed as previously reported on unchanged files since the last review:\n> - Nit A (unchanged.go)\n> - Bug B (changed.go)\n\n",
		},
		{
			name: "dismissed only",
			s:    suppressed{dismissed: []report.Finding{bug}},
			want: "> [!NOTE]\n> 1 finding(s) suppressed because their inline thread was resolved and the quoted code is unchanged:\n> - Bug B (changed.go)\n\n",
		},
		{
			name: "both",
			s:    suppressed{unchanged: []report.Finding{nit}, dismissed: []report.Finding{bug}},
			want: "> [!NOTE]\n> 1 finding(s) suppressed as previously reported on unchanged files since the last review:\n> - Nit A (unchanged.go)\n>\n" +
				"> 1 finding(s) suppressed because their inline thread was resolved and the quoted code is unchanged:\n> - Bug B (changed.go)\n\n",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var out bytes.Buffer
			writeSuppressionNote(&out, tc.s)
			if got := out.String(); got != tc.want {
				t.Errorf("note =\n%s\nwant\n%s", got, tc.want)
			}
		})
	}
}

type mockError struct {
	msg string
}

func (e *mockError) Error() string { return e.msg }
