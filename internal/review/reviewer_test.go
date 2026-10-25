package review

import (
	"bytes"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

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

func TestRenderResult_InlineReview(t *testing.T) {
	t.Parallel()

	var submittedBody string
	var submittedComments []github.ReviewComment
	gh := &githubtest.Fake{
		SubmitPRReviewFn: func(owner, repo string, number int, commitSHA, body string, comments []github.ReviewComment) (string, error) {
			submittedBody = body
			submittedComments = comments
			return "https://github.com/test/repo/pull/1#discussion_r123", nil
		},
		FetchDiffFn: func(owner, repo string, number int) (string, error) {
			return inlineTestDiff, nil
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
	if !strings.Contains(submittedBody, "planwerk-agent-data") {
		t.Error("submitted body should contain data block")
	}
}

func TestRenderResult_InlineReviewFallback(t *testing.T) {
	t.Parallel()

	var postedBody string
	gh := &githubtest.Fake{
		PostPRCommentFn: func(owner, repo string, number int, body string) (string, error) {
			postedBody = body
			return "https://github.com/test/repo/pull/1#issuecomment-456", nil
		},
		SubmitPRReviewFn: func(owner, repo string, number int, commitSHA, body string, comments []github.ReviewComment) (string, error) {
			return "", &mockError{"API error"}
		},
		FetchDiffFn: func(owner, repo string, number int) (string, error) {
			return "", nil
		},
	}

	result := &report.ReviewResult{Summary: "Test", Findings: []report.Finding{
		{ID: "W-001", Title: "Unmapped", File: "main.go", Line: 3, Severity: report.SeverityWarning, Problem: "p", Action: "a"},
	}}
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
		t.Fatal("expected fallback to PR comment after inline review failure")
	}
	// The fallback carries the data block the inline review would have, so the
	// next re-review still finds this run's findings.
	if !strings.Contains(postedBody, "planwerk-agent-data") {
		t.Fatalf("fallback comment carries no data block:\n%s", postedBody)
	}
	sha, findings, ok := report.ParseDataBlock(postedBody)
	if !ok || sha != "abc123" || len(findings) != 1 || findings[0].Title != "Unmapped" {
		t.Errorf("fallback data block = %q, %+v, %v; want the head SHA and the run's finding", sha, findings, ok)
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
// gets none either. The data block keeps every finding for the next re-review.
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

			var submittedBody string
			var submittedComments []github.ReviewComment
			gh := &githubtest.Fake{
				SubmitPRReviewFn: func(owner, repo string, number int, commitSHA, body string, comments []github.ReviewComment) (string, error) {
					submittedBody = body
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

			// Assert on stdout, not on the review body: the data block's JSON
			// names every finding.
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

			_, prior, ok := report.ParseDataBlock(submittedBody)
			if !ok || len(prior) != len(tc.findings) {
				t.Errorf("data block = %d findings (ok=%v), want all %d", len(prior), ok, len(tc.findings))
			}
		})
	}
}

// TestInlineReviewComments_SkippedFindingsDoNotUseTheCap pins that the filters
// run before the maxInlineComments cap: a full cap's worth of Unverified
// findings still leaves room for the eligible finding after them.
func TestInlineReviewComments_SkippedFindingsDoNotUseTheCap(t *testing.T) {
	t.Parallel()

	var findings []report.Finding
	for i := range maxInlineComments {
		findings = append(findings, report.Finding{
			ID: fmt.Sprintf("W-%03d", i+1), Severity: report.SeverityWarning, Confidence: report.ConfidenceUncertain,
			Title: "Uncertain nit", File: "main.go", Line: 12, Problem: "p", Action: "a",
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
// section; only the one the first run posted is suppressed.
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
				// The second run reads the review the first one submitted.
				FetchReviewCommentFn: func(owner, repo string, number int) (string, bool, error) {
					return lastBody, lastBody != "", nil
				},
				FetchDiffFn: func(owner, repo string, number int) (string, error) {
					return "diff --git a/unchanged.go b/unchanged.go\n--- /dev/null\n+++ b/unchanged.go\n@@ -0,0 +1,10 @@\n" + strings.Repeat("+line\n", 10), nil
				},
				SubmitPRReviewFn: func(owner, repo string, number int, commitSHA, body string, cs []github.ReviewComment) (string, error) {
					lastBody, comments = body, cs
					return "https://github.com/test/repo/pull/1#pullrequestreview-1", nil
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
					return "https://github.com/test/repo/pull/1#issuecomment-1", nil
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
// dismissed finding gets no new inline comment, while the review body's data
// block still carries it.
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
		SubmitPRReviewFn: func(owner, repo string, number int, commitSHA, b string, cs []github.ReviewComment) (string, error) {
			body, comments = b, cs
			return "https://github.com/test/repo/pull/1#pullrequestreview-1", nil
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
		t.Errorf("review body data block = %+v (ok=%v), want both findings", findings, ok)
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
