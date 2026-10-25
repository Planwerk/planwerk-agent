package review

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/planwerk/planwerk-agent/internal/github"
	"github.com/planwerk/planwerk-agent/internal/github/githubtest"
	"github.com/planwerk/planwerk-agent/internal/report"
)

// botThread is an outdated, unresolved thread planwerk-agent opened under the
// Actions token on path, at lines start to line of the commit it was posted
// at, with no reply: every guard but the overlap veto lets it go.
func botThread(id, path string, start, line int) github.ReviewThread {
	return github.ReviewThread{
		ID:                id,
		IsOutdated:        true,
		Path:              path,
		OriginalStartLine: start,
		OriginalLine:      line,
		CommentCount:      1,
		Comments: []github.ReviewThreadComment{
			{Author: "github-actions", AuthorIsBot: true, Body: "**W-001: Old finding** | WARNING\n\n<!-- planwerk-agent-inline -->"},
		},
	}
}

// userThread is botThread opened by the user account login, as a local run
// under a personal token opens one.
func userThread(id, path string, line int, login string) github.ReviewThread {
	t := botThread(id, path, 0, line)
	t.Comments = slices.Clone(t.Comments)
	t.Comments[0].Author = login
	t.Comments[0].AuthorIsBot = false
	return t
}

// at is a finding on file at lines line to lineEnd (0 for a single line).
func at(file string, line, lineEnd int) report.Finding {
	return report.Finding{Severity: report.SeverityWarning, Title: "Live", File: file, Line: line, LineEnd: lineEnd}
}

// errAppTokenRefused is ViewerLogin's error when GitHub refuses to name the
// login of an app's token.
var errAppTokenRefused = fmt.Errorf("gh api graphql (viewer): GraphQL: Resource not accessible by integration (viewer): %w", github.ErrViewerRefused)

func TestClassifyOutdatedThread(t *testing.T) {
	t.Parallel()

	with := func(base github.ReviewThread, edit func(*github.ReviewThread)) github.ReviewThread {
		base.Comments = slices.Clone(base.Comments)
		edit(&base)
		return base
	}
	thread := botThread("RT_1", "a.go", 10, 20)
	tests := []struct {
		name     string
		thread   github.ReviewThread
		viewer   string
		findings []report.Finding
		want     string
	}{
		{name: "not outdated", thread: with(thread, func(t *github.ReviewThread) { t.IsOutdated = false }), want: threadNotOutdated},
		{name: "already resolved", thread: with(thread, func(t *github.ReviewThread) { t.IsResolved = true }), want: threadAlreadyResolved},
		{name: "more comments than returned", thread: with(thread, func(t *github.ReviewThread) { t.CommentCount = 2 }), want: threadPartialView},
		{name: "no comments", thread: with(thread, func(t *github.ReviewThread) { t.Comments = nil; t.CommentCount = 0 }), want: threadPartialView},
		{name: "no inline signature", thread: with(thread, func(t *github.ReviewThread) { t.Comments[0].Body = "**W-001: Old finding** | WARNING" }), want: threadNotOurs},
		{name: "user root under an app token", thread: userThread("RT_1", "a.go", 20, "alice"), want: threadUnverifiedAuthor},
		{name: "user root, another viewer", thread: userThread("RT_1", "a.go", 20, "alice"), viewer: "bob", want: threadUnverifiedAuthor},
		// The resolver would be bob, not the poster, and dismiss the finding.
		{name: "bot root under a personal token", thread: thread, viewer: "bob", want: threadUnverifiedAuthor},
		{name: "no root author", thread: with(thread, func(t *github.ReviewThread) { t.Comments[0].Author = "" }), want: threadUnverifiedAuthor},
		{
			name: "a reply by the same login",
			thread: with(thread, func(t *github.ReviewThread) {
				t.Comments = append(t.Comments, github.ReviewThreadComment{Author: "github-actions", AuthorIsBot: true, Body: "ok"})
				t.CommentCount = 2
			}),
			want: threadReplied,
		},
		{name: "a finding on the file without a line", thread: thread, findings: []report.Finding{at("a.go", 0, 0)}, want: threadUnlocatablePath},
		{name: "original line unknown", thread: with(thread, func(t *github.ReviewThread) { t.OriginalLine = 0 }), want: threadNoLine},
		{name: "single-line thread inside a multi-line finding", thread: botThread("RT_1", "a.go", 0, 12), findings: []report.Finding{at("a.go", 10, 15)}, want: threadOverlap},
		{name: "one shared line", thread: thread, findings: []report.Finding{at("a.go", 19, 40)}, want: threadOverlap},
		{name: "single-line finding on the first line", thread: thread, findings: []report.Finding{at("a.go", 10, 0)}, want: threadOverlap},
		{name: "finding path spelled ./a.go", thread: thread, findings: []report.Finding{at("./a.go", 15, 0)}, want: threadOverlap},
		{name: "bot root under an app token", thread: thread, want: threadResolve},
		{name: "bot root, viewer is the bot", thread: thread, viewer: "github-actions[bot]", want: threadResolve},
		{name: "user root that is the viewer", thread: userThread("RT_1", "a.go", 20, "Planwerk-Bot"), viewer: "planwerk-bot", want: threadResolve},
		{name: "a file-less finding vetoes nothing", thread: thread, findings: []report.Finding{{Severity: report.SeverityWarning, Title: "Scope drift"}}, want: threadResolve},
		{name: "a finding next to the thread", thread: thread, findings: []report.Finding{at("a.go", 21, 30), at("b.go", 15, 0)}, want: threadResolve},
		{name: "start line after the line reads as one line", thread: botThread("RT_1", "a.go", 30, 20), findings: []report.Finding{at("a.go", 25, 0)}, want: threadResolve},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := classifyOutdatedThread(tc.thread, tc.viewer, newLiveSpans(tc.findings)); got != tc.want {
				t.Errorf("classifyOutdatedThread = %q, want %q", got, tc.want)
			}
		})
	}
}

// resolveRunner is a Runner whose pause records its waits instead of sleeping.
func resolveRunner(gh *githubtest.Fake) (*Runner, *[]time.Duration) {
	var pauses []time.Duration
	r := newTestRunner(gh)
	r.Pause = func(d time.Duration) { pauses = append(pauses, d) }
	return r, &pauses
}

// TestResolveOutdatedThreads_ResolvesOnlyCandidates hands the method a thread
// for every reason, under a personal token's login, and checks that it
// resolves exactly the candidates, in order, and logs each kept thread with
// its reason and a counts line.
func TestResolveOutdatedThreads_ResolvesOnlyCandidates(t *testing.T) {
	// Not t.Parallel(): it replaces the default logger.
	var logBuf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	const viewer = "planwerk-bot"
	mine := func(id, path string, start, line int) github.ReviewThread {
		th := userThread(id, path, line, viewer)
		th.OriginalStartLine = start
		return th
	}
	notOutdated := mine("RT_current", "a.go", 0, 1)
	notOutdated.IsOutdated = false
	resolved := mine("RT_resolved", "a.go", 0, 2)
	resolved.IsResolved = true
	foreign := mine("RT_foreign", "a.go", 0, 3)
	foreign.Comments = []github.ReviewThreadComment{{Author: "alice", Body: "please rename"}}
	replied := mine("RT_replied", "a.go", 0, 4)
	replied.Comments = append(replied.Comments, github.ReviewThreadComment{Author: "alice", Body: "won't fix"})
	replied.CommentCount = 2
	partial := mine("RT_partial", "a.go", 0, 5)
	partial.CommentCount = 3
	threads := []github.ReviewThread{
		notOutdated,
		mine("RT_mine", "a.go", 0, 6),
		resolved,
		foreign,
		botThread("RT_bot", "a.go", 0, 7),
		userThread("RT_stranger", "a.go", 8, "mallory"),
		replied,
		partial,
		mine("RT_noline", "a.go", 0, 0),
		mine("RT_overlap", "a.go", 40, 42),
		mine("RT_unlocated", "c.go", 0, 9),
		mine("RT_other_file", "b.go", 40, 42),
		userThread("RT_case", "a.go", 10, "Planwerk-Bot"),
	}
	findings := []report.Finding{at("a.go", 41, 0), at("c.go", 0, 0)}

	gh := &githubtest.Fake{Viewer: viewer}
	r, pauses := resolveRunner(gh)
	r.resolveOutdatedThreads(&github.PR{Owner: "o", Repo: "r", Number: 1}, findings, threads)

	if got, want := gh.Resolved(), []string{"RT_mine", "RT_other_file", "RT_case"}; !slices.Equal(got, want) {
		t.Errorf("resolved = %v, want %v", got, want)
	}
	if want := []time.Duration{resolvePause, resolvePause}; !slices.Equal(*pauses, want) {
		t.Errorf("pauses = %v, want %v, one between two resolves", *pauses, want)
	}
	logs := logBuf.String()
	for _, want := range []string{
		"thread=RT_bot path=a.go line=7 reason=unverified_author",
		"thread=RT_replied path=a.go line=4 reason=replied",
		"thread=RT_overlap path=a.go line=42 reason=overlap",
		`msg="resolved outdated review thread" thread=RT_other_file path=b.go line=42`,
		"threads=13 candidates=3 resolved=3 kept_open=already_resolved:1,no_line:1,not_ours:1,not_outdated:1,overlap:1,partial_view:1,replied:1,unlocatable_path:1,unverified_author:2",
	} {
		if !strings.Contains(logs, want) {
			t.Errorf("log lacks %q:\n%s", want, logs)
		}
	}
	if strings.Contains(logs, "thread=RT_current") || strings.Contains(logs, "thread=RT_resolved") {
		t.Errorf("log names a thread that is current or already resolved:\n%s", logs)
	}
}

// TestResolveOutdatedThreads_ItsResolvesDismissNothing: a thread the run
// resolves never dismisses its finding on a later run (decision 132). Under a
// personal token it resolves only the token's own threads, so the resolver is
// the poster; under an app's token, whose login GitHub refused, only a bot's,
// which an app resolves with no resolver named. A personal token whose login
// failed to load for another reason resolves nothing: resolving the bot's
// thread under it would name a resolver other than the poster, a dismissal.
func TestResolveOutdatedThreads_ItsResolvesDismissNothing(t *testing.T) {
	t.Parallel()

	f := report.Finding{ID: "W-001", Severity: report.SeverityWarning, Title: "Old", File: "a.go", Line: 3, CodeSnippet: "x := 1", Problem: "p", Action: "a"}
	outdated := func(author string, bot bool) github.ReviewThread {
		th := ownThread(f, author, false, "")
		th.ID += "-" + author
		th.IsOutdated = true
		th.OriginalLine = 3
		th.CommentCount = 1
		th.Comments[0].AuthorIsBot = bot
		return th
	}
	threads := []github.ReviewThread{outdated("github-actions", true), outdated("alice", false)}
	for _, tc := range []struct {
		name, viewer, resolver string
		viewerErr              error
		wantResolved           []string
	}{
		{name: "personal token", viewer: "alice", resolver: "alice", wantResolved: []string{"RT_W-001-alice"}},
		{name: "app token", viewerErr: errAppTokenRefused, wantResolved: []string{"RT_W-001-github-actions"}},
		{name: "personal token whose login failed to load", viewerErr: errors.New("gh api graphql (viewer): net/http: TLS handshake timeout"), resolver: "alice"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			gh := &githubtest.Fake{Viewer: tc.viewer, ViewerErr: tc.viewerErr}
			r, _ := resolveRunner(gh)
			r.resolveOutdatedThreads(&github.PR{Owner: "o", Repo: "r", Number: 1}, nil, threads)
			if got := gh.Resolved(); !slices.Equal(got, tc.wantResolved) {
				t.Fatalf("resolved = %v, want %v", got, tc.wantResolved)
			}
			// The threads as the next run reads them.
			var after []github.ReviewThread
			for _, th := range threads {
				if slices.Contains(gh.Resolved(), th.ID) {
					th.IsResolved, th.ResolvedBy = true, tc.resolver
				}
				after = append(after, th)
			}
			if fps := dismissedFingerprints(after); len(fps) != 0 {
				t.Errorf("dismissedFingerprints = %v, want none", fps)
			}
		})
	}
}

// TestResolveOutdatedThreads_StopsAtFirstRefusal: a refused mutation, as under
// a token without contents: write, ends the resolving for the run and does
// not fail it.
func TestResolveOutdatedThreads_StopsAtFirstRefusal(t *testing.T) {
	t.Parallel()

	gh := &githubtest.Fake{Viewer: "github-actions[bot]", ResolveErr: fmt.Errorf("Resource not accessible by integration")}
	r, _ := resolveRunner(gh)
	threads := []github.ReviewThread{botThread("RT_1", "a.go", 0, 1), botThread("RT_2", "a.go", 0, 2), botThread("RT_3", "a.go", 0, 3)}
	r.resolveOutdatedThreads(&github.PR{Owner: "o", Repo: "r", Number: 1}, nil, threads)
	if n := gh.Count("ResolveReviewThread"); n != 1 {
		t.Errorf("ResolveReviewThread calls = %d, want 1", n)
	}
}

// TestResolveOutdatedThreads_CapsPerRun: past maxResolvePerRun candidates the
// rest wait for the next run, with a pause between every two resolves.
func TestResolveOutdatedThreads_CapsPerRun(t *testing.T) {
	t.Parallel()

	var threads []github.ReviewThread
	for i := range maxResolvePerRun + 1 {
		threads = append(threads, botThread(fmt.Sprintf("RT_%d", i), "a.go", 0, i+1))
	}
	gh := &githubtest.Fake{Viewer: "github-actions[bot]"}
	r, pauses := resolveRunner(gh)
	r.resolveOutdatedThreads(&github.PR{Owner: "o", Repo: "r", Number: 1}, nil, threads)
	if n := gh.Count("ResolveReviewThread"); n != maxResolvePerRun {
		t.Errorf("ResolveReviewThread calls = %d, want %d", n, maxResolvePerRun)
	}
	if len(*pauses) != maxResolvePerRun-1 {
		t.Errorf("pauses = %d, want %d", len(*pauses), maxResolvePerRun-1)
	}
}

// TestResolveOutdatedThreads_ListingFailureResolvesNothing: when the threads
// cannot be read, the suppression hands over none, and the run resolves
// nothing, reads no login, and still succeeds.
func TestResolveOutdatedThreads_ListingFailureResolvesNothing(t *testing.T) {
	t.Parallel()

	gh := &githubtest.Fake{
		ThreadsErr:  fmt.Errorf("graphql: rate limited"),
		FetchDiffFn: func(owner, repo string, number int) (string, error) { return inlineTestDiff, nil },
	}
	r, _ := resolveRunner(gh)
	opts := inlineTestOpts()
	opts.ResolveOutdated = true
	if err := r.renderResult(&bytes.Buffer{}, inlineTestResult(), inlineTestPR(), opts, nil); err != nil {
		t.Fatalf("renderResult returned error: %v", err)
	}
	if n := gh.Count("FetchReviewThreads"); n != 1 {
		t.Errorf("FetchReviewThreads calls = %d, want 1", n)
	}
	if n := gh.Count("ResolveReviewThread") + gh.Count("ViewerLogin"); n != 0 {
		t.Errorf("ResolveReviewThread and ViewerLogin calls = %d, want 0", n)
	}
}

// TestResolveOutdatedThreads_AppTokenResolvesOnlyBotThreads: when GitHub
// refuses to name the login of an app's token, a thread a user account opened
// may be a quote of the tool's comment and stays open.
func TestResolveOutdatedThreads_AppTokenResolvesOnlyBotThreads(t *testing.T) {
	t.Parallel()

	gh := &githubtest.Fake{ViewerErr: errAppTokenRefused}
	r, _ := resolveRunner(gh)
	threads := []github.ReviewThread{userThread("RT_user", "a.go", 1, "planwerk-bot"), botThread("RT_bot", "a.go", 0, 2)}
	r.resolveOutdatedThreads(&github.PR{Owner: "o", Repo: "r", Number: 1}, nil, threads)
	if got := gh.Resolved(); !slices.Equal(got, []string{"RT_bot"}) {
		t.Errorf("resolved = %v, want [RT_bot]", got)
	}
}

// TestResolveOutdatedThreads_UnreadableLoginResolvesNothing: a login that
// fails to load for any other reason, or comes back empty, may belong to a
// personal token, under which resolving a bot's thread would dismiss its
// finding (decision 132). The run resolves nothing and still succeeds.
func TestResolveOutdatedThreads_UnreadableLoginResolvesNothing(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name      string
		viewerErr error
	}{
		{name: "the lookup timed out", viewerErr: errors.New("gh api graphql (viewer): net/http: TLS handshake timeout")},
		{name: "the login is empty"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			gh := &githubtest.Fake{ViewerErr: tc.viewerErr}
			r, _ := resolveRunner(gh)
			threads := []github.ReviewThread{botThread("RT_bot", "a.go", 0, 2), userThread("RT_user", "a.go", 1, "alice")}
			r.resolveOutdatedThreads(&github.PR{Owner: "o", Repo: "r", Number: 1}, nil, threads)
			if n := gh.Count("ResolveReviewThread"); n != 0 {
				t.Errorf("ResolveReviewThread calls = %d, want 0", n)
			}
		})
	}
}

func TestComparePath(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ in, want string }{
		{"internal/a.go", "internal/a.go"},
		{"./internal/a.go", "internal/a.go"},
		{"/internal/a.go", "internal/a.go"},
		{".//internal/a.go", "internal/a.go"},
		{` internal\a.go `, "internal/a.go"},
		{"\t./internal/a.go", "internal/a.go"},
		{"internal/./a.go", "internal/./a.go"},
		{"Internal/A.go", "Internal/A.go"},
		{"", ""},
	} {
		if got := comparePath(tc.in); got != tc.want {
			t.Errorf("comparePath(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestFormatReasonCounts(t *testing.T) {
	t.Parallel()

	if got := formatReasonCounts(nil); got != "none" {
		t.Errorf("formatReasonCounts(nil) = %q, want none", got)
	}
	got := formatReasonCounts(map[string]int{threadReplied: 2, threadNotOutdated: 7, threadOverlap: 1})
	if want := "not_outdated:7,overlap:1,replied:2"; got != want {
		t.Errorf("formatReasonCounts = %q, want %q", got, want)
	}
}
