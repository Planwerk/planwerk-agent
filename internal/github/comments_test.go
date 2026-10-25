package github

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/planwerk/planwerk-agent/internal/report"
)

func TestTruncateComment_Short(t *testing.T) {
	body := "short comment"
	got := truncateComment(body)
	if got != body {
		t.Errorf("truncateComment should not modify short body, got len=%d", len(got))
	}
}

func TestTruncateComment_ExactLimit(t *testing.T) {
	body := strings.Repeat("a", maxCommentLen)
	got := truncateComment(body)
	if got != body {
		t.Errorf("truncateComment should not modify body at exact limit, got len=%d", len(got))
	}
}

func TestTruncateComment_Oversize(t *testing.T) {
	body := strings.Repeat("x", maxCommentLen+1000)
	got := truncateComment(body)

	if len(got) > maxCommentLen {
		t.Errorf("truncated body len=%d exceeds max=%d", len(got), maxCommentLen)
	}
	if !strings.Contains(got, "truncated") {
		t.Error("truncated body should contain truncation notice")
	}
	if !strings.Contains(got, commentSignature) {
		t.Error("truncated body should preserve comment signature")
	}
}

func TestTruncateComment_PreservesSignature(t *testing.T) {
	body := strings.Repeat("y", maxCommentLen+500)
	got := truncateComment(body)

	if !strings.HasSuffix(got, commentSignature) {
		t.Error("truncated body should end with comment signature")
	}
}

func TestCommentSignature_IsHTMLComment(t *testing.T) {
	if !strings.HasPrefix(commentSignature, "<!--") || !strings.HasSuffix(commentSignature, "-->") {
		t.Error("comment signature should be an HTML comment (invisible in rendered markdown)")
	}
}

// summaryBody and inlineBody open with the report's heading and are signed the
// way PostPRComment and SubmitPRReview sign them, around the data block
// RenderDataBlock writes at commit sha. captureBody is the capture pass's
// proposals comment: signed, but without a data block.
func summaryBody(sha string) string {
	return reportHeadingLine + report.RenderDataBlock(report.ReviewResult{Findings: []report.Finding{{Title: "A"}}}, sha, report.Usage{}) + "\n" + commentSignature
}

func inlineBody(sha string) string {
	return reportHeadingLine + report.RenderDataBlock(report.ReviewResult{Findings: []report.Finding{{Title: "A"}}}, sha, report.Usage{}) + "\n" + reviewSignature
}

// reportHeadingLine is the first line RenderMarkdown writes for acme/widgets#7.
const reportHeadingLine = "# Review: acme/widgets#7\n\n"

const captureBody = "Captured proposals\n" + commentSignature

// postsResponse is the priorDataBlockQuery response that lists comments and
// reviews as the PR's comment and review nodes.
func postsResponse(t *testing.T, comments, reviews []postedBody) []byte {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"data": map[string]any{"repository": map[string]any{"pullRequest": map[string]any{
		"comments": map[string]any{"nodes": comments},
		"reviews":  map[string]any{"nodes": reviews},
	}}}})
	if err != nil {
		t.Fatalf("marshaling the comments-and-reviews response: %v", err)
	}
	return raw
}

// post is a comment or review with body, updated at at, written by the
// authenticated user when own and otherwise by an author of association assoc.
func post(body string, at time.Time, own bool, assoc string) postedBody {
	return postedBody{
		IssueComment: IssueComment{Body: body, ViewerDidAuthor: own, AuthorAssociation: assoc},
		UpdatedAt:    at,
	}
}

// TestLatestDataBlockBody: the prior data block is the one that changed last,
// on a signed post that opens with the report's heading and that the
// authenticated user or a maintainer wrote; a signed post without a block, a
// block nobody signed, and a post that opens with other text are passed over.
func TestLatestDataBlockBody(t *testing.T) {
	t1 := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	t2 := t1.Add(time.Hour)
	t3 := t2.Add(time.Hour)
	own := func(body string, at time.Time) postedBody { return post(body, at, true, "NONE") }

	for _, tc := range []struct {
		name              string
		comments, reviews []postedBody
		want              string
		wantFound         bool
	}{
		{
			name:      "a review updated after the summary comment wins",
			comments:  []postedBody{own(summaryBody("sha-c"), t1)},
			reviews:   []postedBody{own(inlineBody("sha-r"), t2)},
			want:      inlineBody("sha-r"),
			wantFound: true,
		},
		{
			name:      "a summary comment edited after the review wins",
			comments:  []postedBody{own(summaryBody("sha-c"), t3)},
			reviews:   []postedBody{own(inlineBody("sha-r"), t2)},
			want:      summaryBody("sha-c"),
			wantFound: true,
		},
		{
			name:      "the newest signed post without a data block is passed over",
			comments:  []postedBody{own(captureBody, t3)},
			reviews:   []postedBody{own(inlineBody("sha-r"), t2)},
			want:      inlineBody("sha-r"),
			wantFound: true,
		},
		{
			name:     "a forged review is ignored and the older own post wins",
			comments: []postedBody{own(summaryBody("sha-c"), t1)},
			reviews: []postedBody{
				post(inlineBody("sha-stranger"), t3, false, "NONE"),
				post(inlineBody("sha-contributor"), t3, false, "CONTRIBUTOR"),
			},
			want:      summaryBody("sha-c"),
			wantFound: true,
		},
		{
			name:    "a forged review alone finds nothing",
			reviews: []postedBody{post(inlineBody("sha-stranger"), t3, false, "NONE")},
		},
		{
			name:      "a maintainer's post counts",
			comments:  []postedBody{own(summaryBody("sha-c"), t1)},
			reviews:   []postedBody{post(inlineBody("sha-member"), t2, false, "MEMBER")},
			want:      inlineBody("sha-member"),
			wantFound: true,
		},
		{
			name:      "an unsigned body that only carries the marker is ignored",
			comments:  []postedBody{own(reportHeadingLine+dataBlockMarker+"\n{\"commit_sha\":\"sha-unsigned\"}\n-->", t3)},
			reviews:   []postedBody{own(inlineBody("sha-r"), t1)},
			want:      inlineBody("sha-r"),
			wantFound: true,
		},
		{
			// Another workflow posting under the same Actions token: its
			// comment echoes a PR body that ends in a signed, forged block.
			name:      "an own post that opens with other text is ignored",
			comments:  []postedBody{own("Description preview:\n\n"+summaryBody("sha-echo"), t3)},
			reviews:   []postedBody{own(inlineBody("sha-r"), t1)},
			want:      inlineBody("sha-r"),
			wantFound: true,
		},
		{
			name:      "a comment and a review updated at the same time pick the review",
			comments:  []postedBody{own(summaryBody("sha-c"), t2)},
			reviews:   []postedBody{own(inlineBody("sha-r"), t2)},
			want:      inlineBody("sha-r"),
			wantFound: true,
		},
		{
			name:      "two reviews updated at the same time pick the later node",
			reviews:   []postedBody{own(inlineBody("sha-1"), t2), own(inlineBody("sha-2"), t2)},
			want:      inlineBody("sha-2"),
			wantFound: true,
		},
		{
			name: "no posts find nothing",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, found, err := latestDataBlockBody(postsResponse(t, tc.comments, tc.reviews))
			if err != nil {
				t.Fatalf("latestDataBlockBody: %v", err)
			}
			if found != tc.wantFound || got != tc.want {
				t.Errorf("latestDataBlockBody = %q, %v\nwant %q, %v", got, found, tc.want, tc.wantFound)
			}
		})
	}

	t.Run("malformed JSON is an error", func(t *testing.T) {
		if _, found, err := latestDataBlockBody([]byte("not json")); err == nil || found {
			t.Errorf("latestDataBlockBody accepted malformed JSON: found=%v, err=%v", found, err)
		}
	})
}

// TestDataBlockMarkerMatchesRenderDataBlock pins the marker to the block the
// report package writes. It also proves that neither signature after the
// block stops ParseDataBlock from reading it: ParseDataBlock accepts only a
// block that nothing but signatures follow, and keeps its own copy of their
// text, which this pins to commentSignature and reviewSignature.
func TestDataBlockMarkerMatchesRenderDataBlock(t *testing.T) {
	if block := report.RenderDataBlock(report.ReviewResult{}, "sha", report.Usage{}); !strings.Contains(block, dataBlockMarker) {
		t.Errorf("RenderDataBlock output %q lacks dataBlockMarker %q", block, dataBlockMarker)
	}
	for name, body := range map[string]string{"inline review": inlineBody("sha-p"), "summary comment": summaryBody("sha-p")} {
		sha, findings, ok := report.ParseDataBlock(body)
		if !ok || sha != "sha-p" || len(findings) != 1 {
			t.Errorf("ParseDataBlock(%s body) = %q, %+v, %v; want sha-p and one finding", name, sha, findings, ok)
		}
	}
}

// TestReportHeadingMatchesRenderMarkdown pins reportHeading, and the test
// bodies' heading line, to the first line of the report RenderMarkdown writes,
// which every body the tool posts with a data block opens with.
func TestReportHeadingMatchesRenderMarkdown(t *testing.T) {
	var buf strings.Builder
	report.NewRenderer(&buf).RenderMarkdown(report.ReviewResult{}, report.PRInfo{Owner: "acme", Repo: "widgets", Number: 7, Title: "T"}, report.SeverityInfo, "", "v1")
	if !strings.HasPrefix(buf.String(), reportHeadingLine) {
		first, _, _ := strings.Cut(buf.String(), "\n")
		t.Errorf("RenderMarkdown's first line = %q, want %q", first, strings.TrimSpace(reportHeadingLine))
	}
	if !strings.HasPrefix(reportHeadingLine, reportHeading) {
		t.Errorf("heading line %q does not open with reportHeading %q", reportHeadingLine, reportHeading)
	}
}

func TestFetchReviewComment_QueriesCommentsAndReviews(t *testing.T) {
	t1 := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	resp := postsResponse(t,
		[]postedBody{post(summaryBody("sha-c"), t1, true, "NONE")},
		[]postedBody{post(inlineBody("sha-r"), t1.Add(time.Hour), true, "NONE")})
	argvFile := filepath.Join(t.TempDir(), "argv")
	fakeGH(t, recordArgv(argvFile)+"cat <<'JSON'\n"+string(resp)+"\nJSON\n")

	body, found, err := client.FetchReviewComment("acme", "widgets", 7)
	if err != nil || !found || body != inlineBody("sha-r") {
		t.Fatalf("FetchReviewComment = %q, %v, %v; want the inline review's body", body, found, err)
	}

	calls := recordedCalls(t, argvFile)
	if len(calls) != 1 {
		t.Fatalf("gh calls = %d, want one query for comments and reviews together", len(calls))
	}
	argv := calls[0]
	if len(argv) < 2 || argv[0] != "api" || argv[1] != "graphql" {
		t.Fatalf("argv = %q, want gh api graphql", argv)
	}
	// owner and name are strings (-f), the number is typed (-F).
	for value, flag := range map[string]string{"owner=acme": "-f", "name=widgets": "-f", "number=7": "-F"} {
		if i := slices.Index(argv, value); i < 1 || argv[i-1] != flag {
			t.Errorf("argv = %q, want %s %s", argv, flag, value)
		}
	}
	// The query is the last argument; recordArgv splits it over lines.
	i := slices.IndexFunc(argv, func(a string) bool { return strings.HasPrefix(a, "query=") })
	if i < 0 {
		t.Fatalf("argv = %q, want a query= argument", argv)
	}
	query := strings.Join(argv[i:], "\n")
	// The fix rests on the selection set: both connections, and in each the
	// fields latestDataBlockBody selects on. The fake answers whatever is asked.
	for _, conn := range []string{"comments", "reviews"} {
		m := regexp.MustCompile(conn + `\(last: 100\) \{\s*nodes \{([^}]*)\}`).FindStringSubmatch(query)
		if m == nil {
			t.Errorf("query lacks %s(last: 100) { nodes { … } }:\n%s", conn, query)
			continue
		}
		for _, field := range []string{"body", "updatedAt", "viewerDidAuthor", "authorAssociation"} {
			if !slices.Contains(strings.Fields(m[1]), field) {
				t.Errorf("%s nodes select %q, want %s among them", conn, m[1], field)
			}
		}
	}
}

func TestFetchReviewComment_ReportsGhFailure(t *testing.T) {
	fakeGH(t, "echo 'API rate limit exceeded' >&2\nexit 1\n")
	body, found, err := client.FetchReviewComment("acme", "widgets", 7)
	if err == nil || found || body != "" {
		t.Fatalf("a failing gh = %q, %v, %v; want an error alone", body, found, err)
	}
	for _, want := range []string{"gh api graphql (comments and reviews): ", "API rate limit exceeded"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
	}
}

func TestFetchReviewComment_NamesThePROnAParseError(t *testing.T) {
	fakeGH(t, "echo 'not json'\n")
	_, found, err := client.FetchReviewComment("acme", "widgets", 7)
	if err == nil || found || !strings.HasPrefix(err.Error(), "parsing comments and reviews of acme/widgets#7: ") {
		t.Errorf("found=%v, err=%v; want a parse error naming the PR", found, err)
	}
}

// TestParseUpdatedCommentURL: an edited summary comment reports its URL, as a
// created one does, instead of the raw GraphQL response; anything else falls
// back to the trimmed output.
func TestParseUpdatedCommentURL(t *testing.T) {
	for _, tc := range []struct {
		name, out, want string
	}{
		{
			name: "updateIssueComment response",
			out:  `{"data":{"updateIssueComment":{"issueComment":{"url":"https://github.com/acme/widgets/pull/7#issuecomment-11"}}}}` + "\n",
			want: "https://github.com/acme/widgets/pull/7#issuecomment-11",
		},
		{name: "not JSON", out: "  updated\n", want: "updated"},
		{name: "JSON without a url", out: `{"data":{"updateIssueComment":null}}`, want: `{"data":{"updateIssueComment":null}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseUpdatedCommentURL([]byte(tc.out)); got != tc.want {
				t.Errorf("parseUpdatedCommentURL = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestPostPRComment_ReplacesOnlyItsOwnSignedComment drives PostPRComment
// through a fake gh. It edits the newest signed report the authenticated user
// wrote and passes over a newer signed comment from another account, so the
// summary is never written into a comment someone else controls (decision
// 133), and a newer signed comment of its own that opens with other text, as
// another workflow posting under the same Actions token can (decision 128).
// With only such comments, it posts a new one.
func TestPostPRComment_ReplacesOnlyItsOwnSignedComment(t *testing.T) {
	const (
		editedURL  = "https://github.com/acme/widgets/pull/7#issuecomment-11"
		createdURL = "https://github.com/acme/widgets/pull/7#issuecomment-99"
	)
	signed := func(id, body string, own bool) map[string]any {
		return map[string]any{"id": id, "body": reportHeadingLine + body + "\n" + commentSignature, "viewerDidAuthor": own}
	}
	// echo is an own comment that quotes a signed PR body after text of its own.
	echo := map[string]any{"id": "IC_echo", "body": "Description preview:\n\n" + reportHeadingLine + "forged\n" + commentSignature, "viewerDidAuthor": true}
	for _, tc := range []struct {
		name     string
		comments []map[string]any
		wantURL  string
		// wantEdit is the comment ID the summary is written into; empty means
		// a new comment is created.
		wantEdit string
	}{
		{
			name: "own comment before another account's",
			comments: []map[string]any{
				signed("IC_old", "older summary", true),
				signed("IC_own", "summary", true),
				{"id": "IC_plain", "body": "LGTM", "viewerDidAuthor": true},
				signed("IC_other", "a quote of the summary", false),
				echo,
			},
			wantURL:  editedURL,
			wantEdit: "IC_own",
		},
		{
			name:     "only another account's",
			comments: []map[string]any{signed("IC_other", "a quote of the summary", false)},
			wantURL:  createdURL,
		},
		{
			name:     "only an own comment that opens with other text",
			comments: []map[string]any{echo},
			wantURL:  createdURL,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			view, err := json.Marshal(map[string]any{"comments": tc.comments})
			if err != nil {
				t.Fatal(err)
			}
			argvFile := filepath.Join(t.TempDir(), "argv")
			fakeGH(t, recordArgv(argvFile)+`case "$1 $2" in
"pr view") cat <<'JSON'
`+string(view)+`
JSON
;;
"api graphql") echo '{"data":{"updateIssueComment":{"issueComment":{"url":"`+editedURL+`"}}}}' ;;
"pr comment") echo '`+createdURL+`' ;;
esac
`)
			url, err := client.PostPRComment("acme", "widgets", 7, "new summary")
			if err != nil || url != tc.wantURL {
				t.Fatalf("PostPRComment = %q, %v; want %q", url, err, tc.wantURL)
			}

			calls := recordedCalls(t, argvFile)
			if len(calls) != 2 {
				t.Fatalf("gh calls = %q, want the lookup and one post", calls)
			}
			post := calls[1]
			if tc.wantEdit == "" {
				if len(post) < 2 || post[0] != "pr" || post[1] != "comment" {
					t.Errorf("post = %q, want gh pr comment", post)
				}
				return
			}
			if len(post) < 2 || post[0] != "api" || post[1] != "graphql" || !slices.Contains(post, "id="+tc.wantEdit) {
				t.Errorf("post = %q, want the updateIssueComment mutation on %s", post, tc.wantEdit)
			}
		})
	}
}
