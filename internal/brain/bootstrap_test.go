package brain

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/planwerk/planwerk-agent/internal/capture"
	"github.com/planwerk/planwerk-agent/internal/github"
	"github.com/planwerk/planwerk-agent/internal/github/githubtest"
	"github.com/planwerk/planwerk-agent/internal/patterns"
	"github.com/planwerk/planwerk-agent/internal/report"
)

const (
	testRepoRef       = "acme/widgets"
	testAnalysisModel = "claude-opus-5-5"
	testReviewModel   = "claude-fable-5-1"
)

// harness wires a Bootstrapper to fakes and records what the run did.
type harness struct {
	t     *testing.T
	dir   string // the state directory
	clone string // the checkout the sessions run in
	src   *fakeSource
	gh    *githubtest.Fake
	wiki  *fakeWiki

	// analyze and review script the two sessions; nil proposes one memory page
	// per unit and accepts every proposal.
	analyze func(ctx UnitContext) (*capture.CaptureResult, error)
	review  func(ctx ReviewContext) (*ReviewResult, error)

	// now scripts the clock, which a unit reads once, after its page files are
	// written; nil is a fixed time.
	now func() time.Time

	analyzed []UnitContext
	reviewed []ReviewContext
	usage    report.Usage
	tty      bool
	answer   string // what the operator types at the confirmation
}

func newHarness(t *testing.T, listing Listing) *harness {
	t.Helper()
	h := &harness{
		t:     t,
		dir:   filepath.Join(t.TempDir(), StateDirName),
		clone: t.TempDir(),
		src:   &fakeSource{listing: listing},
		wiki:  newFakeWiki(t, "wiki-head", nil),
	}
	h.gh = &githubtest.Fake{}
	h.gh.CloneRepoFn = func(string) (*github.Repo, error) {
		// Local keeps Cleanup from deleting the directory between two runs.
		return &github.Repo{Owner: "acme", Name: "widgets", Dir: h.clone, Local: true}, nil
	}
	return h
}

// handClosedIssues is a listing of n issues closed by hand on consecutive
// days, which BuildUnits orders by number.
func handClosedIssues(n int) Listing {
	var l Listing
	for i := 1; i <= n; i++ {
		l.Issues = append(l.Issues, github.ClosedIssue{Number: i, Title: fmt.Sprintf("Issue %d", i), ClosedAt: day(i)})
	}
	return l
}

// memoryPage is a proposal for memory/<slug>.md.
func memoryPage(slug string) capture.ProposedPage {
	return capture.ProposedPage{Path: "memory/" + slug + ".md", Title: slug, Body: "# " + slug + "\n\n**Summary**: About " + slug + "."}
}

func (h *harness) run(opts BootstrapOptions) (string, error) {
	h.t.Helper()
	opts.RepoRef = testRepoRef
	opts.StateDir = h.dir
	b := &Bootstrapper{
		Source: h.src,
		GitHub: h.gh,
		Analyze: func(dir string, ctx UnitContext) (*capture.CaptureResult, error) {
			if dir != h.clone {
				h.t.Errorf("Analyze ran in %q, want the clone %q", dir, h.clone)
			}
			h.analyzed = append(h.analyzed, ctx)
			h.spend(report.Usage{Calls: 2, InputTokens: 100, OutputTokens: 10, CostUSD: 0.5})
			if h.analyze != nil {
				return h.analyze(ctx)
			}
			return &capture.CaptureResult{Memory: []capture.ProposedPage{memoryPage("unit-" + ctx.Unit.Key)}, Model: testAnalysisModel}, nil
		},
		Review: func(_ string, ctx ReviewContext) (*ReviewResult, error) {
			h.reviewed = append(h.reviewed, ctx)
			h.spend(report.Usage{Calls: 2, InputTokens: 50, OutputTokens: 5, CostUSD: 0.25})
			if h.review != nil {
				return h.review(ctx)
			}
			return acceptAll(ctx), nil
		},
		Usage:  func() report.Usage { return h.usage },
		Writer: h.wiki,
		In:     strings.NewReader(h.answer),
		IsTTY:  func() bool { return h.tty },
		Now: func() time.Time {
			if h.now != nil {
				return h.now()
			}
			return time.Date(2026, 10, 1, 14, 0, 0, 0, time.FixedZone("CEST", 2*60*60))
		},
	}
	var out strings.Builder
	err := b.Run(&out, opts)
	return out.String(), err
}

// spend adds one session's usage to what the Usage seam reports, with a
// per-pass breakdown the state must not keep.
func (h *harness) spend(u report.Usage) {
	h.usage = h.usage.Add(u)
	h.usage.Passes = []report.PassUsage{{Pass: "bootstrap-unit", Calls: h.usage.Calls}}
}

// acceptAll gives every proposed page an accept verdict.
func acceptAll(ctx ReviewContext) *ReviewResult {
	res := &ReviewResult{Model: testReviewModel}
	for _, p := range ctx.Proposed {
		res.Pages = append(res.Pages, ReviewedPage{Path: p.Path, Verdict: VerdictAccept})
	}
	return res
}

// state loads the state the run left behind.
func (h *harness) state() *State {
	h.t.Helper()
	s, err := LoadState(h.dir, testRepoRef)
	if err != nil {
		h.t.Fatalf("LoadState: %v", err)
	}
	return s
}

// pages returns the page files of the working set.
func (h *harness) pages() map[string]string {
	h.t.Helper()
	return dirSnapshot(h.t, filepath.Join(h.dir, pagesDirName))
}

// analyzedKeys returns the keys of the units Analyze ran for, in order.
func (h *harness) analyzedKeys() []string {
	keys := make([]string, 0, len(h.analyzed))
	for _, ctx := range h.analyzed {
		keys = append(keys, ctx.Unit.Key)
	}
	return keys
}

func assertContains(t *testing.T, out string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

func TestRun_DryRunListsTheUnitsAndTouchesNothing(t *testing.T) {
	l := handClosedIssues(2)
	// Pull request 7 closes both issues and counts once among the pull
	// requests the issue units hold.
	l.PRs = []github.MergedPR{
		{Number: 7, Title: "Fix both", MergedAt: day(2), ClosesIssues: []int{1, 2}},
		{Number: 8, Title: "Fix the second", MergedAt: day(2), ClosesIssues: []int{2}},
		{Number: 9, Title: "Update a dependency", MergedAt: day(1), AuthorIsBot: true},
		{Number: 10, Title: "Tidy the docs", MergedAt: day(3)},
	}
	h := newHarness(t, l)
	writeTree(t, h.clone, map[string]string{"docs/adr/0001-x.md": "# Decision\n"})

	out, err := h.run(BootstrapOptions{DryRun: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	chunk := chunkDocument("docs/adr/0001-x.md", "# Decision\n")[0]
	want := "Units: 4 total, 0 processed, 4 remaining\n" +
		"  2 issues (with 2 pull requests), 1 standalone pull requests, 0 commit ranges, 1 decision document chunks; 1 bot-authored pull requests skipped\n" +
		"issue-1  Issue 1\n" +
		"issue-2  Issue 2\n" +
		"pr-10  Tidy the docs\n" +
		chunk.key() + "  docs/adr/0001-x.md (chunk 1 of 1)\n"
	if out != want {
		t.Errorf("output =\n%s\nwant\n%s", out, want)
	}
	if len(h.analyzed) != 0 || len(h.reviewed) != 0 || h.wiki.clones != 0 {
		t.Errorf("a dry run ran %d analyses, %d reviews, and %d wiki clones, want none", len(h.analyzed), len(h.reviewed), h.wiki.clones)
	}
	if _, err := os.Stat(h.dir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a dry run must not create the state directory (stat err = %v)", err)
	}
}

func TestRun_DryRunLeavesAnExistingStateUntouched(t *testing.T) {
	h := newHarness(t, handClosedIssues(3))
	if _, err := h.run(BootstrapOptions{MaxUnits: 1}); err != nil {
		t.Fatalf("first run: %v", err)
	}
	// A page file without an entry: a real run's Reconcile would delete it.
	writeTree(t, h.dir, map[string]string{"pages/memory/orphan.md": "orphan\n"})
	before := dirSnapshot(t, h.dir)

	out, err := h.run(BootstrapOptions{DryRun: true})
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	assertContains(t, out, "Units: 3 total, 1 processed, 2 remaining\n", "issue-2  Issue 2\nissue-3  Issue 3\n")
	if strings.Contains(out, "issue-1  ") {
		t.Errorf("a processed unit is listed:\n%s", out)
	}
	if after := dirSnapshot(t, h.dir); !reflect.DeepEqual(before, after) {
		t.Error("the dry run changed the state directory")
	}
}

func TestRun_ListingAndCloneErrors(t *testing.T) {
	t.Run("listing", func(t *testing.T) {
		h := newHarness(t, Listing{})
		h.src.listErr = errors.New("gh api graphql (closed issues): rate limit")
		_, err := h.run(BootstrapOptions{})
		if !errors.Is(err, h.src.listErr) || !strings.HasPrefix(err.Error(), "listing the history of acme/widgets: ") {
			t.Errorf("err = %v, want the wrapped listing error", err)
		}
		if _, statErr := os.Stat(h.dir); !errors.Is(statErr, os.ErrNotExist) {
			t.Errorf("a failed listing must not create the state directory (stat err = %v)", statErr)
		}
		if h.gh.Count("CloneRepo") != 0 {
			t.Error("the repository was cloned after a failed listing")
		}
	})

	t.Run("clone", func(t *testing.T) {
		h := newHarness(t, handClosedIssues(1))
		boom := errors.New("gh repo clone: exit status 1")
		h.gh.CloneRepoFn = func(string) (*github.Repo, error) { return nil, boom }
		_, err := h.run(BootstrapOptions{})
		if !errors.Is(err, boom) || !strings.HasPrefix(err.Error(), "cloning repository: ") {
			t.Errorf("err = %v, want the wrapped clone error", err)
		}
	})

	t.Run("repository reference", func(t *testing.T) {
		b := &Bootstrapper{}
		err := b.Run(&strings.Builder{}, BootstrapOptions{RepoRef: "not a ref"})
		if err == nil || !strings.HasPrefix(err.Error(), "parsing repo ref: ") {
			t.Errorf("err = %v, want a repo ref error", err)
		}
	})

	t.Run("missing decision document", func(t *testing.T) {
		h := newHarness(t, handClosedIssues(1))
		_, err := h.run(BootstrapOptions{DecisionDocs: []string{"missing.md"}})
		if err == nil || err.Error() != `decision document "missing.md" not found in the repository` {
			t.Errorf("err = %v", err)
		}
		if len(h.analyzed) != 0 {
			t.Error("a session ran before the decision documents were checked")
		}
	})
}

func TestRun_EmptyRepository(t *testing.T) {
	h := newHarness(t, Listing{})
	out, err := h.run(BootstrapOptions{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	assertContains(t, out, "Units: 0 total, 0 processed, 0 remaining\n", "Models: analysis -, review -\n")
	if len(h.analyzed) != 0 || len(h.reviewed) != 0 {
		t.Errorf("ran %d analyses and %d reviews for an empty repository", len(h.analyzed), len(h.reviewed))
	}
}

// TestRun_ProcessesEveryUnit is the plain run: one page per unit, each with
// its unit's source, the unit records in order, and the usage of the sessions.
func TestRun_ProcessesEveryUnit(t *testing.T) {
	h := newHarness(t, handClosedIssues(2))
	h.src.issues = map[int]*github.IssueThread{1: {Number: 1, Title: "Issue 1", Body: "credentials: AKIAIOSFODNN7EXAMPLE used for S3"}}
	logs := captureLogs(t)

	out, err := h.run(BootstrapOptions{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	assertContains(t, out,
		"[1/2] issue-1: 1 proposed, 1 accepted, 0 rejected\n",
		"[2/2] issue-2: 1 proposed, 1 accepted, 0 rejected\n",
		"Pages: 2 new, 0 updated, 0 unchanged, 0 diverged\n",
		"- `memory/unit-issue-1.md` (new) from acme/widgets#1\n",
		"Models: analysis "+testAnalysisModel+", review "+testReviewModel+"\n",
		"Usage this run: 300 input tokens, 30 output tokens, 8 calls, est. $1.50\n",
		"Usage all runs: 300 input tokens, 30 output tokens, 8 calls, est. $1.50\n",
		"Propose-only: nothing was written to the wiki. The pages are under "+filepath.Join(h.dir, "pages")+"; run again with --write-wiki to push them.\n",
	)
	if len(h.wiki.pushed) != 0 {
		t.Errorf("a run without --write-wiki pushed %d times", len(h.wiki.pushed))
	}

	// The content reaches the analysis redacted, with one warning for the unit.
	if body := h.analyzed[0].Items[0].Body; strings.Contains(body, "AKIAIOSFODNN7EXAMPLE") || !strings.Contains(body, "[REDACTED:aws-access-key-id]") {
		t.Errorf("the analysis saw an unredacted body: %q", body)
	}
	if n := strings.Count(logs.String(), "redacted secrets in the content of a unit"); n != 1 {
		t.Errorf("got %d redaction warnings, want 1:\n%s", n, logs.String())
	}
	// The second unit sees the page the first one wrote.
	if !filepath.IsAbs(h.analyzed[0].PagesDir) || h.analyzed[0].Index != "" {
		t.Errorf("first unit: PagesDir %q, Index %q, want an absolute directory and an empty index", h.analyzed[0].PagesDir, h.analyzed[0].Index)
	}
	if want := "- memory/unit-issue-1.md: unit-issue-1 | About unit-issue-1.\n"; h.analyzed[1].Index != want {
		t.Errorf("second unit's index = %q, want %q", h.analyzed[1].Index, want)
	}
	if len(h.analyzed[0].Patterns) == 0 {
		t.Error("the analysis was given no pattern catalog")
	}

	s := h.state()
	want := []UnitRecord{
		{Key: "issue-1", Title: "Issue 1", Proposed: 1, Accepted: 1, Rejected: []Rejection{}, FinishedAt: "2026-10-01T12:00:00Z"},
		{Key: "issue-2", Title: "Issue 2", Proposed: 1, Accepted: 1, Rejected: []Rejection{}, FinishedAt: "2026-10-01T12:00:00Z"},
	}
	if !reflect.DeepEqual(s.Units, want) {
		t.Errorf("units = %+v\nwant %+v", s.Units, want)
	}
	if got := s.Pages["memory/unit-issue-2.md"]; got != (PageState{Source: "acme/widgets#2"}) {
		t.Errorf("page entry = %+v, want the unit's source and no base", got)
	}
	if want := (report.Usage{Calls: 8, InputTokens: 300, OutputTokens: 30, CostUSD: 1.5}); !reflect.DeepEqual(s.Usage, want) {
		t.Errorf("usage = %+v, want %+v", s.Usage, want)
	}
	if s.WikiCommit != "wiki-head" || s.WikiRepo != testRepoRef {
		t.Errorf("wiki = %s @ %s", s.WikiRepo, s.WikiCommit)
	}
	if got := h.pages()["memory/unit-issue-1.md"]; got != "# unit-issue-1\n\n**Summary**: About unit-issue-1.\n" {
		t.Errorf("page file = %q", got)
	}
	if got := readFile(t, filepath.Join(h.dir, ".gitignore")); got != "*\n" {
		t.Errorf(".gitignore = %q", got)
	}
}

// TestRun_ContentReachesTheAnalysisWithinItsCaps covers the two ends of a
// unit's size: an issue with nothing but a title is one item, and a thread
// past the unit cap reaches the analysis with items left out and counted.
func TestRun_ContentReachesTheAnalysisWithinItsCaps(t *testing.T) {
	h := newHarness(t, handClosedIssues(2))
	long := &github.IssueThread{Number: 2, Title: "Issue 2"}
	for range 20 {
		long.Comments = append(long.Comments, github.ThreadComment{Author: "a", Body: strings.Repeat("x", 40<<10)})
	}
	h.src.issues = map[int]*github.IssueThread{1: {Number: 1, Title: "Issue 1"}, 2: long}

	if _, err := h.run(BootstrapOptions{}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	first, second := h.analyzed[0], h.analyzed[1]
	if len(first.Items) != 1 || first.Items[0].Body != "Title: Issue 1" || first.Omitted != 0 {
		t.Errorf("first unit: %d items, %d omitted, want the title as its one item", len(first.Items), first.Omitted)
	}
	if second.Omitted == 0 || len(second.Items)+second.Omitted != 21 {
		t.Errorf("second unit: %d items and %d omitted, want 21 in total with some omitted", len(second.Items), second.Omitted)
	}
	if body := second.Items[1].Body; !strings.HasSuffix(body, "\n[truncated: 8192 bytes omitted]") || len(body) > maxItemBytes+40 {
		t.Errorf("a 40 KiB comment reached the analysis as %d bytes ending %q", len(body), body[len(body)-40:])
	}
}

func TestRun_NoProposalsNeverReviews(t *testing.T) {
	for name, result := range map[string]*capture.CaptureResult{
		"nil result":   nil,
		"empty result": {Model: testAnalysisModel},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, handClosedIssues(1))
			h.analyze = func(UnitContext) (*capture.CaptureResult, error) { return result, nil }

			out, err := h.run(BootstrapOptions{})
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			assertContains(t, out, "[1/1] issue-1: 0 proposed, 0 accepted, 0 rejected\n", ", review -\n")
			if len(h.reviewed) != 0 {
				t.Errorf("Review ran %d times for a unit without proposals", len(h.reviewed))
			}
			if s := h.state(); len(s.Units) != 1 || s.Units[0].Proposed != 0 || len(s.Pages) != 0 {
				t.Errorf("state = %+v", s)
			}
		})
	}
}

func TestRun_RejectsInvalidAndDuplicatePaths(t *testing.T) {
	h := newHarness(t, handClosedIssues(1))
	h.analyze = func(UnitContext) (*capture.CaptureResult, error) {
		page := func(path string) capture.ProposedPage { return capture.ProposedPage{Path: path, Body: "# x"} }
		return &capture.CaptureResult{
			Patterns: []capture.ProposedPage{page("review_patterns/ok.md"), page("memory/a-memory-page-among-the-patterns.md")},
			Memory: []capture.ProposedPage{
				page("../x.md"), page("memory/Sub/x.md"), page("memory/X.md"), page("notes/x.md"),
				page("review_patterns/a-pattern-among-the-memory-pages.md"),
				page("memory/ok.md"), page("memory/ok.md"), page(""),
			},
		}, nil
	}

	out, err := h.run(BootstrapOptions{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	assertContains(t, out, "[1/1] issue-1: 10 proposed, 2 accepted, 8 rejected\n", "- `memory/X.md` (issue-1): invalid path\n", "- `memory/ok.md` (issue-1): duplicate path\n")

	if len(h.reviewed) != 1 {
		t.Fatalf("Review ran %d times, want 1", len(h.reviewed))
	}
	var seen []string
	for _, p := range h.reviewed[0].Proposed {
		seen = append(seen, p.Kind+" "+p.Path)
	}
	if want := []string{"pattern review_patterns/ok.md", "memory memory/ok.md"}; !slices.Equal(seen, want) {
		t.Errorf("the review saw %q, want %q", seen, want)
	}
	wantRejected := []Rejection{
		{Path: "memory/a-memory-page-among-the-patterns.md", Reason: "invalid path"},
		{Path: "../x.md", Reason: "invalid path"},
		{Path: "memory/Sub/x.md", Reason: "invalid path"},
		{Path: "memory/X.md", Reason: "invalid path"},
		{Path: "notes/x.md", Reason: "invalid path"},
		{Path: "review_patterns/a-pattern-among-the-memory-pages.md", Reason: "invalid path"},
		{Path: "memory/ok.md", Reason: "duplicate path"},
		{Path: "", Reason: "invalid path"},
	}
	if got := h.state().Units[0].Rejected; !slices.Equal(got, wantRejected) {
		t.Errorf("rejected = %+v\nwant %+v", got, wantRejected)
	}
	if got := h.pages(); len(got) != 2 || got["memory/ok.md"] == "" || got["review_patterns/ok.md"] == "" {
		t.Errorf("page files = %v, want only the two valid pages", got)
	}
}

// TestRun_ProposalForAnExistingPageIsAnUpdate covers a page the wiki already
// holds under a name a new page could not take.
func TestRun_ProposalForAnExistingPageIsAnUpdate(t *testing.T) {
	h := newHarness(t, handClosedIssues(1))
	h.wiki = newFakeWiki(t, "wiki-head", map[string]string{"memory/Old Decisions.md": "# Old\n"})
	h.analyze = func(UnitContext) (*capture.CaptureResult, error) {
		return &capture.CaptureResult{Memory: []capture.ProposedPage{
			{Path: "memory/Old Decisions.md", Body: "# Old\n\nRevised.", IsUpdate: false},
			{Path: "memory/fresh.md", Body: "# Fresh", IsUpdate: true},
		}}, nil
	}

	out, err := h.run(BootstrapOptions{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	proposed := h.reviewed[0].Proposed
	if len(proposed) != 2 || !proposed[0].IsUpdate || proposed[1].IsUpdate {
		t.Errorf("IsUpdate = %v, %v, want true for the existing page and false for the new one, whatever the model said", proposed[0].IsUpdate, proposed[1].IsUpdate)
	}
	assertContains(t, out, "Pages: 1 new, 1 updated, 0 unchanged, 0 diverged\n", "- `memory/Old Decisions.md` (update) from acme/widgets#1\n")
	s := h.state()
	if got := s.Pages["memory/Old Decisions.md"]; got.Source != "acme/widgets#1" || got.BaseSHA256 != hashPage("# Old\n") {
		t.Errorf("entry = %+v, want the unit's source and the wiki's base kept", got)
	}
}

func TestRun_Verdicts(t *testing.T) {
	h := newHarness(t, handClosedIssues(1))
	h.analyze = func(UnitContext) (*capture.CaptureResult, error) {
		res := &capture.CaptureResult{}
		for _, slug := range []string{"accepted", "revised", "rejected", "revised-without-body", "unknown-verdict", "no-verdict", "marker", "too-large", "empty", "rejected-without-reason", "at-the-cap"} {
			res.Memory = append(res.Memory, memoryPage(slug))
		}
		res.Memory[6].Body = capture.RenderWithSource("# Marker\n\nBody.\n\n\n", "someone/else#1")
		res.Memory[7].Body = strings.Repeat("x", 70<<10)
		// The body alone fits the cap; the page the push writes, with its
		// provenance marker, does not.
		res.Memory[10].Body = strings.Repeat("x", maxPageBytes-1)
		return res, nil
	}
	h.review = func(ReviewContext) (*ReviewResult, error) {
		return &ReviewResult{Pages: []ReviewedPage{
			{Path: "memory/accepted.md", Verdict: " Accept "},
			{Path: "memory/revised.md", Verdict: "revise", Body: "# Revised by the reviewer\n", Reason: "the summary was vague"},
			{Path: "memory/rejected.md", Verdict: "reject", Reason: "a one-off"},
			{Path: "memory/revised-without-body.md", Verdict: "revise", Reason: "should be shorter"},
			{Path: "memory/unknown-verdict.md", Verdict: "maybe"},
			{Path: "memory/never-\x1b[2Jproposed.md", Verdict: "accept"},
			{Path: "memory/marker.md", Verdict: "accept"},
			{Path: "memory/too-large.md", Verdict: "accept"},
			{Path: "memory/empty.md", Verdict: "revise", Body: capture.MarkerFor("x/y#1") + "\n\n"},
			{Path: "memory/rejected-without-reason.md", Verdict: "reject", Reason: " "},
			{Path: "memory/at-the-cap.md", Verdict: "accept"},
		}}, nil
	}
	logs := captureLogs(t)

	out, err := h.run(BootstrapOptions{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	assertContains(t, out, "[1/1] issue-1: 11 proposed, 3 accepted, 8 rejected\n")

	wantFiles := map[string]string{
		"memory/accepted.md": "# accepted\n\n**Summary**: About accepted.\n",
		"memory/revised.md":  "# Revised by the reviewer\n",
		"memory/marker.md":   "# Marker\n\nBody.\n",
	}
	if got := h.pages(); !reflect.DeepEqual(got, wantFiles) {
		t.Errorf("page files = %v\nwant %v", got, wantFiles)
	}
	wantRejected := []Rejection{
		{Path: "memory/rejected.md", Reason: "a one-off"},
		// The reviser's reason was for a corrected page that never came.
		{Path: "memory/revised-without-body.md", Reason: "revise verdict without a page"},
		{Path: "memory/unknown-verdict.md", Reason: "no review verdict"},
		{Path: "memory/no-verdict.md", Reason: "no review verdict"},
		{Path: "memory/too-large.md", Reason: "page exceeds 64 KiB"},
		{Path: "memory/empty.md", Reason: "empty page"},
		{Path: "memory/rejected-without-reason.md", Reason: "rejected without a reason"},
		{Path: "memory/at-the-cap.md", Reason: "page exceeds 64 KiB"},
	}
	if got := h.state().Units[0].Rejected; !slices.Equal(got, wantRejected) {
		t.Errorf("rejected = %+v\nwant %+v", got, wantRejected)
	}
	if _, ok := h.state().Pages["memory/never-\x1b[2Jproposed.md"]; ok {
		t.Error("a verdict for an unproposed path created a page")
	}
	// The path is logged quoted: the escape character in it reaches the log as
	// the four characters \x1b, which the text handler quotes once more.
	if !strings.Contains(logs.String(), "ignoring a review verdict for a page that was not proposed") || !strings.Contains(logs.String(), `memory/never-\\x1b[2Jproposed.md`) {
		t.Errorf("the verdict for an unproposed path was not logged with its path quoted:\n%s", logs.String())
	}
}

func TestRun_NilReviewRejectsEveryProposal(t *testing.T) {
	h := newHarness(t, handClosedIssues(1))
	h.review = func(ReviewContext) (*ReviewResult, error) { return nil, nil }

	out, err := h.run(BootstrapOptions{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	assertContains(t, out, "[1/1] issue-1: 1 proposed, 0 accepted, 1 rejected\n", "- `memory/unit-issue-1.md` (issue-1): no review verdict\n")
	if got := h.pages(); len(got) != 0 {
		t.Errorf("page files = %v, want none", got)
	}
}

func TestRun_MaxUnitsThenResume(t *testing.T) {
	h := newHarness(t, handClosedIssues(5))

	out, err := h.run(BootstrapOptions{MaxUnits: 2})
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	assertContains(t, out, "Units: 5 total, 0 processed, 5 remaining\n", "[1/5] issue-1:", "[2/5] issue-2:")
	if got := h.analyzedKeys(); !slices.Equal(got, []string{"issue-1", "issue-2"}) {
		t.Fatalf("the first run analyzed %v, want the first two units in order", got)
	}

	out, err = h.run(BootstrapOptions{DryRun: true})
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	assertContains(t, out, "Units: 5 total, 2 processed, 3 remaining\n")

	h.analyzed, h.src.issueCalls = nil, nil
	out, err = h.run(BootstrapOptions{})
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	assertContains(t, out, "[3/5] issue-3:", "[5/5] issue-5:", "Usage this run: 450 input tokens", "Usage all runs: 750 input tokens")
	if got := h.analyzedKeys(); !slices.Equal(got, []string{"issue-3", "issue-4", "issue-5"}) {
		t.Errorf("the second run analyzed %v, want units three to five", got)
	}
	if !slices.Equal(h.src.issueCalls, []int{3, 4, 5}) {
		t.Errorf("the second run read issues %v, want 3, 4, 5", h.src.issueCalls)
	}

	// Every unit is processed now: a third run starts no session.
	h.analyzed, h.reviewed = nil, nil
	out, err = h.run(BootstrapOptions{})
	if err != nil {
		t.Fatalf("third run: %v", err)
	}
	assertContains(t, out, "Units: 5 total, 5 processed, 0 remaining\n", "Models: analysis -, review -\n", "Usage this run: 0 input tokens, 0 output tokens, 0 calls, est. $0.00\n", "Usage all runs: 750 input tokens")
	if len(h.analyzed) != 0 || len(h.reviewed) != 0 {
		t.Errorf("the third run ran %d analyses and %d reviews, want none", len(h.analyzed), len(h.reviewed))
	}
}

// TestRun_StopsAtAFailingUnit covers the four steps a unit can fail at. Each
// stop saves the two processed units and what the failing unit spent, says
// where the run stopped, and is continued by the next run at the same unit.
func TestRun_StopsAtAFailingUnit(t *testing.T) {
	const failing = "issue-3"
	boom := errors.New("api error 429: usage limit reached")
	for _, tc := range []struct {
		step string
		// fail arms the failure for the third unit; the returned func disarms it.
		fail      func(h *harness) (disarm func())
		wantCalls int // Claude calls in the state after the stop
	}{
		{
			step: "content",
			fail: func(h *harness) func() {
				h.src.prErr = boom
				return func() { h.src.prErr = nil }
			},
			wantCalls: 8,
		},
		{
			step: "analysis",
			fail: func(h *harness) func() {
				h.analyze = func(ctx UnitContext) (*capture.CaptureResult, error) {
					if ctx.Unit.Key == failing {
						return nil, boom
					}
					return &capture.CaptureResult{Memory: []capture.ProposedPage{memoryPage("unit-" + ctx.Unit.Key)}}, nil
				}
				return func() { h.analyze = nil }
			},
			wantCalls: 10,
		},
		{
			step: "review",
			fail: func(h *harness) func() {
				h.review = func(ctx ReviewContext) (*ReviewResult, error) {
					if ctx.Unit.Key == failing {
						return nil, boom
					}
					return acceptAll(ctx), nil
				}
				return func() { h.review = nil }
			},
			wantCalls: 12,
		},
		{
			step: "apply",
			fail: func(h *harness) func() {
				// A directory where the page file belongs fails the write for
				// every user, root included.
				blocker := filepath.Join(h.dir, "pages", "memory", "unit-issue-3.md")
				h.analyze = func(ctx UnitContext) (*capture.CaptureResult, error) {
					if ctx.Unit.Key == failing {
						if err := os.MkdirAll(filepath.Join(blocker, "x"), 0o755); err != nil {
							return nil, err
						}
					}
					return &capture.CaptureResult{Memory: []capture.ProposedPage{memoryPage("unit-" + ctx.Unit.Key)}}, nil
				}
				return func() {
					h.analyze = nil
					if err := os.RemoveAll(blocker); err != nil {
						h.t.Fatal(err)
					}
				}
			},
			wantCalls: 12,
		},
	} {
		t.Run(tc.step, func(t *testing.T) {
			l := handClosedIssues(4)
			l.PRs = []github.MergedPR{{Number: 30, MergedAt: day(3), ClosesIssues: []int{3}}}
			h := newHarness(t, l)
			disarm := tc.fail(h)

			out, err := h.run(BootstrapOptions{})
			wantPrefix := "unit issue-3: " + tc.step + ": "
			if err == nil || !strings.HasPrefix(err.Error(), wantPrefix) {
				t.Fatalf("err = %v, want it to start with %q", err, wantPrefix)
			}
			if tc.step != "apply" && !errors.Is(err, boom) {
				t.Errorf("err = %v, want it to wrap the cause", err)
			}
			if tc.step == "analysis" && !strings.Contains(err.Error(), "api error 429") {
				t.Errorf("err = %v, want the usage limit named", err)
			}
			assertContains(t, out, "[2/4] issue-2:", "Stopped at unit issue-3. The state is saved in "+h.dir+"; run the same command again to continue.\n")
			if strings.Contains(out, "Pages:") || strings.Contains(out, "Propose-only") {
				t.Errorf("a stopped run must print no report:\n%s", out)
			}
			s := h.state()
			if len(s.Units) != 2 || s.Units[1].Key != "issue-2" {
				t.Errorf("state holds %+v, want the two processed units", s.Units)
			}
			if s.Usage.Calls != tc.wantCalls {
				t.Errorf("state usage = %d calls, want %d (the failing unit's spend included)", s.Usage.Calls, tc.wantCalls)
			}

			// The same command continues at the unit that failed.
			disarm()
			h.analyzed, h.src.issueCalls = nil, nil
			out, err = h.run(BootstrapOptions{})
			if err != nil {
				t.Fatalf("second run: %v", err)
			}
			assertContains(t, out, "Units: 4 total, 2 processed, 2 remaining\n", "[3/4] issue-3: 1 proposed, 1 accepted, 0 rejected\n", "[4/4] issue-4:")
			if got := h.analyzedKeys(); !slices.Equal(got, []string{"issue-3", "issue-4"}) {
				t.Errorf("the second run analyzed %v, want the failed unit and the one after it", got)
			}
			if !slices.Equal(h.src.issueCalls, []int{3, 4}) {
				t.Errorf("the second run read issues %v, want 3 and 4", h.src.issueCalls)
			}
		})
	}
}

// TestRun_CommitMessageErrorStopsAtContent covers a closer commit the clone
// does not hold.
func TestRun_CommitMessageErrorStopsAtContent(t *testing.T) {
	l := Listing{
		History: []github.HistoryCommit{commit("fix", 1, 0)},
		Issues:  []github.ClosedIssue{{Number: 1, Title: "Issue 1", ClosedAt: day(1), CloserSHA: "fix"}},
	}
	h := newHarness(t, l)
	h.gh.CommitMessageErr = errors.New("git log -1 --format=%B fix: exit status 128: bad object fix")

	out, err := h.run(BootstrapOptions{})
	if !errors.Is(err, h.gh.CommitMessageErr) || !strings.HasPrefix(err.Error(), "unit issue-1: content: ") {
		t.Errorf("err = %v, want the unit stopped at its content", err)
	}
	assertContains(t, out, "Stopped at unit issue-1.")
	if len(h.analyzed) != 0 {
		t.Error("the analysis ran for a unit whose content could not be read")
	}
}

// reportFixture is a run over one unit against a wiki of three pages, one of
// which the operator edited locally while the wiki changed it too. The unit
// adds a page, updates one, and has two proposals rejected.
func reportFixture(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t, handClosedIssues(1))
	h.wiki = newFakeWiki(t, "wiki-head", map[string]string{
		"memory/unchanged.md": "# Unchanged\n",
		"memory/updated.md":   "# Updated\n\nBefore.\n",
		"memory/diverged.md":  "# Diverged\n\nEdited on the wiki.\n",
	})
	s := newState(testRepoRef)
	s.WikiRepo, s.WikiCommit = testRepoRef, "an-older-head"
	s.Pages["memory/diverged.md"] = PageState{BaseSHA256: hashPage("# Diverged\n")}
	s.Usage = report.Usage{Calls: 4, InputTokens: 1000, OutputTokens: 100, CostUSD: 2}
	if err := s.Save(h.dir); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := writePage(h.dir, "memory/diverged.md", "# Diverged\n\nEdited locally."); err != nil {
		t.Fatalf("writePage: %v", err)
	}
	h.analyze = func(UnitContext) (*capture.CaptureResult, error) {
		return &capture.CaptureResult{Model: testAnalysisModel, Memory: []capture.ProposedPage{
			{Path: "memory/new.md", Body: "# New"},
			{Path: "memory/updated.md", Body: "# Updated\n\nAfter."},
			{Path: "memory/one-off.md", Body: "# One-off"},
			{Path: "notes/x.md", Body: "# Elsewhere"},
		}}, nil
	}
	h.review = func(ReviewContext) (*ReviewResult, error) {
		return &ReviewResult{Model: testReviewModel, Pages: []ReviewedPage{
			{Path: "memory/new.md", Verdict: "accept"},
			{Path: "memory/updated.md", Verdict: "accept"},
			{Path: "memory/one-off.md", Verdict: "reject", Reason: "a one-off,\n  not a decision"},
		}}, nil
	}
	return h
}

func TestRun_Report(t *testing.T) {
	h := reportFixture(t)
	out, err := h.run(BootstrapOptions{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := "Units: 1 total, 0 processed, 1 remaining\n" +
		"  1 issues (with 0 pull requests), 0 standalone pull requests, 0 commit ranges, 0 decision document chunks; 0 bot-authored pull requests skipped\n" +
		"[1/1] issue-1: 4 proposed, 2 accepted, 2 rejected\n" +
		"Pages: 1 new, 1 updated, 1 unchanged, 1 diverged\n" +
		"- `memory/new.md` (new) from acme/widgets#1\n" +
		"- `memory/updated.md` (update) from acme/widgets#1\n" +
		"- `notes/x.md` (issue-1): invalid path\n" +
		"- `memory/one-off.md` (issue-1): a one-off, not a decision\n" +
		"- `memory/diverged.md` diverged: the wiki page and the local file both changed\n" +
		"Models: analysis " + testAnalysisModel + ", review " + testReviewModel + "\n" +
		"Usage this run: 150 input tokens, 15 output tokens, 4 calls, est. $0.75\n" +
		"Usage all runs: 1150 input tokens, 115 output tokens, 8 calls, est. $2.75\n" +
		"Propose-only: nothing was written to the wiki. The pages are under " + filepath.Join(h.dir, "pages") + "; run again with --write-wiki to push them.\n"
	if out != want {
		t.Errorf("output =\n%s\nwant\n%s", out, want)
	}
}

func TestRun_WriteWikiRefusesWithoutATerminal(t *testing.T) {
	h := reportFixture(t)
	out, err := h.run(BootstrapOptions{WriteWiki: true})
	const want = "refusing to write to the wiki: brain bootstrap pushes only after a confirmation at a terminal, and stdin is not a TTY"
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v, want %q", err, want)
	}
	if h.wiki.clones != 1 || len(h.wiki.pushed) != 0 {
		t.Errorf("%d wiki clones and %d pushes, want the refresh's clone alone", h.wiki.clones, len(h.wiki.pushed))
	}
	if strings.Contains(out, "will write") {
		t.Errorf("nothing must be listed before the refusal:\n%s", out)
	}
	// The unit is processed all the same: a later run at a terminal pushes.
	if len(h.state().Units) != 1 {
		t.Error("the processed unit was not saved")
	}
}

func TestRun_WriteWikiDeclined(t *testing.T) {
	h := reportFixture(t)
	h.tty, h.answer = true, "n\n"
	out, err := h.run(BootstrapOptions{WriteWiki: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	assertContains(t, out, "Aborted — the wiki was not changed.\n")
	if len(h.wiki.pushed) != 0 || h.wiki.clones != 1 {
		t.Errorf("%d pushes and %d clones after a declined prompt, want 0 and the refresh's clone", len(h.wiki.pushed), h.wiki.clones)
	}
	s := h.state()
	if s.Pages["memory/new.md"].BaseSHA256 != "" || s.Pages["memory/updated.md"].BaseSHA256 != hashPage("# Updated\n\nBefore.\n") {
		t.Errorf("a declined push must leave every page dirty: %+v", s.Pages)
	}
}

// TestRun_WriteWikiPushesAfterAYes walks the push: the listing before the
// question, each page under its own marker, the commit message, clean pages
// afterwards, and a rerun that finds nothing to write. The diverged page takes
// no part in any of it.
func TestRun_WriteWikiPushesAfterAYes(t *testing.T) {
	h := reportFixture(t)
	h.tty, h.answer = true, "y\n"
	out, err := h.run(BootstrapOptions{WriteWiki: true, Wiki: patterns.WikiOptions{Enabled: true, Ref: "pinned"}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	listing := "--write-wiki will write 2 pages to the acme/widgets wiki:\n  memory/new.md (new)\n  memory/updated.md (update)\n"
	question := "Write 2 pages to the acme/widgets wiki and push? (y/N): "
	li, qi := strings.Index(out, listing), strings.Index(out, question)
	if li < 0 || qi < 0 || li > qi {
		t.Errorf("want the listing before the question:\n%s", out)
	}
	assertContains(t, out, "Wrote 2 pages and pushed to the acme/widgets wiki.\n")
	if strings.Contains(out[li:], "diverged.md") {
		t.Errorf("the diverged page is listed for the push:\n%s", out[li:])
	}

	if len(h.wiki.pushed) != 1 {
		t.Fatalf("pushed %d times, want 1", len(h.wiki.pushed))
	}
	files := h.wiki.pushed[0]
	if len(files) != 2 || files[0].Path != "memory/new.md" || files[1].Path != "memory/updated.md" {
		t.Fatalf("pushed %+v, want the new and the updated page", files)
	}
	if want := "<!-- planwerk-agent: captured from acme/widgets#1 -->\n\n# New\n"; files[0].Content != want {
		t.Errorf("pushed content = %q, want %q", files[0].Content, want)
	}
	if want := "Bootstrap 2 pages\n\nWritten by planwerk-agent brain bootstrap from the history of acme/widgets:\n- memory/new.md\n- memory/updated.md\n"; h.wiki.msgs[0] != want {
		t.Errorf("commit message = %q\nwant %q", h.wiki.msgs[0], want)
	}
	if h.wiki.ref != "pinned" {
		t.Errorf("the write cloned ref %q, want the pinned one", h.wiki.ref)
	}
	s := h.state()
	if s.Pages["memory/new.md"].BaseSHA256 != hashPage("# New\n") || s.Pages["memory/updated.md"].BaseSHA256 != hashPage("# Updated\n\nAfter.\n") {
		t.Errorf("a pushed page must be clean: %+v", s.Pages)
	}
	if !s.Pages["memory/diverged.md"].Diverged {
		t.Error("the diverged page lost its flag")
	}

	// The rerun refreshes from the pushed wiki and finds every page clean.
	out, err = h.run(BootstrapOptions{WriteWiki: true})
	if err != nil {
		t.Fatalf("rerun: %v", err)
	}
	assertContains(t, out, "Pages: 0 new, 0 updated, 3 unchanged, 1 diverged\n", "Nothing to write: every page matches the wiki.\n")
	if len(h.wiki.pushed) != 1 {
		t.Errorf("the rerun pushed again")
	}

	// A page the operator edits by hand is pushed as it is, without a marker.
	if err := os.WriteFile(filepath.Join(h.dir, "pages", "memory", "unchanged.md"), []byte("# Unchanged\n\nEdited by hand.\n\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.answer = "y\n"
	out, err = h.run(BootstrapOptions{WriteWiki: true})
	if err != nil {
		t.Fatalf("run after a hand edit: %v", err)
	}
	assertContains(t, out, "- `memory/unchanged.md` (update) from a local edit\n", "--write-wiki will write 1 page to the acme/widgets wiki:\n  memory/unchanged.md (update)\n")
	if len(h.wiki.pushed) != 2 || h.wiki.pushed[1][0].Content != "# Unchanged\n\nEdited by hand.\n" {
		t.Errorf("pushed %+v, want the hand-edited page without a marker", h.wiki.pushed)
	}
	if !strings.HasPrefix(h.wiki.msgs[1], "Bootstrap 1 page\n\n") {
		t.Errorf("commit message = %q, want the singular", h.wiki.msgs[1])
	}
}

func TestRun_WriteWikiWaitsForEveryUnit(t *testing.T) {
	h := newHarness(t, handClosedIssues(2))
	h.tty, h.answer = true, "y\n"
	out, err := h.run(BootstrapOptions{WriteWiki: true, MaxUnits: 1})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	assertContains(t, out, "1 unit remains; the wiki write runs once every unit is processed.\n")
	if len(h.wiki.pushed) != 0 || strings.Contains(out, "will write") {
		t.Errorf("a run with units remaining must push nothing:\n%s", out)
	}

	h3 := newHarness(t, handClosedIssues(3))
	out, err = h3.run(BootstrapOptions{WriteWiki: true, MaxUnits: 1})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	assertContains(t, out, "2 units remain; the wiki write runs once every unit is processed.\n")
}

// TestRun_FailedWikiCloneKeepsTheRunGoing covers a wiki that was never
// initialized: the refresh warns, the units are processed, and the working set
// starts empty.
func TestRun_FailedWikiCloneKeepsTheRunGoing(t *testing.T) {
	h := newHarness(t, handClosedIssues(1))
	h.wiki.cloneErr = errors.New("repository not found")
	logs := captureLogs(t)

	out, err := h.run(BootstrapOptions{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	assertContains(t, out, "[1/1] issue-1: 1 proposed, 1 accepted, 0 rejected\n")
	if !strings.Contains(logs.String(), "could not clone the wiki") {
		t.Errorf("the failed clone was not logged:\n%s", logs.String())
	}
	if s := h.state(); s.WikiCommit != "" {
		t.Errorf("wiki_commit = %q, want it empty after a failed clone", s.WikiCommit)
	}
}

func TestRun_UsesTheConfiguredWiki(t *testing.T) {
	h := newHarness(t, Listing{})
	const handbook = "acme/handbook"
	if _, err := h.run(BootstrapOptions{Wiki: patterns.WikiOptions{Enabled: true, Repo: handbook}}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if h.wiki.repo != handbook || h.state().WikiRepo != handbook {
		t.Errorf("refreshed from %q, state %q, want the configured wiki", h.wiki.repo, h.state().WikiRepo)
	}
}

// TestRun_DropsControlCharactersFromItsOutput proves an issue title cannot
// drive the terminal the run prints to.
func TestRun_DropsControlCharactersFromItsOutput(t *testing.T) {
	l := Listing{Issues: []github.ClosedIssue{{Number: 1, Title: "Clear\x1b[2Jthe screen", ClosedAt: day(1)}}}
	h := newHarness(t, l)
	out, err := h.run(BootstrapOptions{DryRun: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.ContainsRune(out, 0x1b) || !strings.Contains(out, "issue-1  Clear[2Jthe screen\n") {
		t.Errorf("output = %q, want the escape character dropped", out)
	}
}

// TestRun_IssueGainsACloser covers an issue that was processed and is closed
// again later, by a pull request or by a commit: its unit keeps its key and
// runs again with what closed it, and its record is replaced.
func TestRun_IssueGainsACloser(t *testing.T) {
	for _, tc := range []struct {
		name string
		// close closes issue 1 again in the listing.
		close       func(l *Listing)
		wantPRs     []int
		wantCommits []string
	}{
		{
			name: "pull request",
			close: func(l *Listing) {
				l.PRs = []github.MergedPR{{Number: 20, Title: "Fix it for real", MergedAt: day(5), ClosesIssues: []int{1}}}
			},
			wantPRs: []int{20},
		},
		{
			name: "commit",
			close: func(l *Listing) {
				l.History = []github.HistoryCommit{commit("fix", 5, 0)}
				l.Issues[0].CloserSHA = "fix"
			},
			wantCommits: []string{"fix"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, handClosedIssues(2))
			if _, err := h.run(BootstrapOptions{}); err != nil {
				t.Fatalf("first run: %v", err)
			}

			tc.close(&h.src.listing)
			h.analyzed = nil
			out, err := h.run(BootstrapOptions{})
			if err != nil {
				t.Fatalf("second run: %v", err)
			}
			assertContains(t, out, "Units: 2 total, 1 processed, 1 remaining\n", "issue-1: 1 proposed, 1 accepted, 0 rejected\n")
			if got := h.analyzedKeys(); !slices.Equal(got, []string{"issue-1"}) {
				t.Fatalf("the second run analyzed %v, want the issue that was closed again", got)
			}
			if u := h.analyzed[0].Unit; !slices.Equal(u.PRs, tc.wantPRs) || !slices.Equal(u.Commits, tc.wantCommits) {
				t.Errorf("the unit held PRs %v and commits %v, want %v and %v", u.PRs, u.Commits, tc.wantPRs, tc.wantCommits)
			}
			units := h.state().Units
			if len(units) != 2 || units[0].Key != "issue-2" || units[1].Key != "issue-1" {
				t.Fatalf("units = %+v, want one record per unit, the one that ran again last", units)
			}
			if !slices.Equal(units[1].PRs, tc.wantPRs) || !slices.Equal(units[1].Commits, tc.wantCommits) {
				t.Errorf("record = %+v, want it to name what closed the issue", units[1])
			}

			// The record covers the unit now: a third run starts no session.
			h.analyzed = nil
			out, err = h.run(BootstrapOptions{})
			if err != nil {
				t.Fatalf("third run: %v", err)
			}
			assertContains(t, out, "Units: 2 total, 2 processed, 0 remaining\n")
			if len(h.analyzed) != 0 {
				t.Errorf("the third run analyzed %v, want nothing", h.analyzedKeys())
			}
		})
	}
}

// blockStateSaves makes every later save of the state in dir fail, for every
// user: a directory lies where the temporary state file belongs. The returned
// func removes it.
func blockStateSaves(t *testing.T, dir string) (unblock func()) {
	t.Helper()
	blocker := filepath.Join(dir, "state.json.tmp")
	if err := os.MkdirAll(filepath.Join(blocker, "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	return func() {
		t.Helper()
		if err := os.RemoveAll(blocker); err != nil {
			t.Fatal(err)
		}
	}
}

// TestRun_StateSaveFailureIsNotReportedAsSaved covers a state that cannot be
// saved while a unit runs: the run stops at the apply step and does not say
// the state is saved. A unit with an accepted page fails before any page file
// is written, and a unit without one fails at the save of its record.
func TestRun_StateSaveFailureIsNotReportedAsSaved(t *testing.T) {
	for _, tc := range []struct {
		name     string
		proposal []capture.ProposedPage
	}{
		{"with an accepted page", []capture.ProposedPage{memoryPage("decision")}},
		{"without a proposal", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, handClosedIssues(1))
			h.analyze = func(UnitContext) (*capture.CaptureResult, error) {
				blockStateSaves(t, h.dir)
				return &capture.CaptureResult{Memory: tc.proposal}, nil
			}
			out, err := h.run(BootstrapOptions{})
			if err == nil || !strings.HasPrefix(err.Error(), "unit issue-1: apply: ") {
				t.Fatalf("err = %v, want the unit stopped at apply", err)
			}
			if strings.Contains(out, "The state is saved") {
				t.Errorf("a run whose state could not be saved must not say it was:\n%s", out)
			}
			if got := h.pages(); len(got) != 0 {
				t.Errorf("page files = %v, want none: the state could not name the unit as their source", got)
			}
		})
	}
}

// TestRun_StopBetweenThePageWriteAndTheStateSave covers a run that dies after
// a unit updated a page file and before the unit was recorded. The next run
// processes the unit again, its analysis finds the page as it wants it and
// proposes nothing, and the page is still reported and pushed under the unit
// that changed it.
func TestRun_StopBetweenThePageWriteAndTheStateSave(t *testing.T) {
	h := newHarness(t, handClosedIssues(1))
	h.wiki = newFakeWiki(t, "wiki-head", map[string]string{"memory/shared.md": "# Shared\n\nBefore.\n"})
	h.analyze = func(UnitContext) (*capture.CaptureResult, error) {
		return &capture.CaptureResult{Memory: []capture.ProposedPage{{Path: "memory/shared.md", Body: "# Shared\n\nAfter."}}}, nil
	}
	var unblock func()
	h.now = func() time.Time {
		// The page file is written; the unit's record is not saved yet.
		unblock = blockStateSaves(t, h.dir)
		return time.Time{}
	}
	if _, err := h.run(BootstrapOptions{}); err == nil || !strings.HasPrefix(err.Error(), "unit issue-1: apply: ") {
		t.Fatalf("err = %v, want the unit stopped at apply", err)
	}
	if got := h.pages()["memory/shared.md"]; got != "# Shared\n\nAfter.\n" {
		t.Fatalf("page file = %q, want the update on disk", got)
	}
	if s := h.state(); len(s.Units) != 0 {
		t.Fatalf("units = %+v, want the unit not recorded", s.Units)
	}

	unblock()
	h.now = nil
	h.analyze = func(UnitContext) (*capture.CaptureResult, error) { return &capture.CaptureResult{}, nil }
	h.tty, h.answer = true, "y\n"
	out, err := h.run(BootstrapOptions{WriteWiki: true})
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	assertContains(t, out, "[1/1] issue-1: 0 proposed, 0 accepted, 0 rejected\n", "- `memory/shared.md` (update) from acme/widgets#1\n")
	if len(h.wiki.pushed) != 1 || h.wiki.pushed[0][0].Content != "<!-- planwerk-agent: captured from acme/widgets#1 -->\n\n# Shared\n\nAfter.\n" {
		t.Errorf("pushed %+v, want the page under the marker of the unit that changed it", h.wiki.pushed)
	}
}

func TestRun_WriteWikiPushFailureLeavesEveryPageDirty(t *testing.T) {
	h := reportFixture(t)
	h.tty, h.answer = true, "y\n"
	h.wiki.applyErr = errors.New("push rejected")
	_, err := h.run(BootstrapOptions{WriteWiki: true})
	if !errors.Is(err, h.wiki.applyErr) || !strings.HasPrefix(err.Error(), "pushing wiki additions: ") {
		t.Fatalf("err = %v, want the wrapped push error", err)
	}
	s := h.state()
	if s.Pages["memory/new.md"].BaseSHA256 != "" || s.Pages["memory/updated.md"].BaseSHA256 != hashPage("# Updated\n\nBefore.\n") {
		t.Errorf("a failed push must leave every page dirty: %+v", s.Pages)
	}
}

// TestRun_WriteWikiMarksOnlyTheWrittenPagesClean covers a wiki that moves
// between the refresh at the start of the run and the push at its end: the
// update is skipped and stays dirty, so a later run pushes it.
func TestRun_WriteWikiMarksOnlyTheWrittenPagesClean(t *testing.T) {
	h := reportFixture(t)
	h.tty, h.answer = true, "y\n"
	analyze := h.analyze
	h.analyze = func(ctx UnitContext) (*capture.CaptureResult, error) {
		h.wiki.head = "moved-during-the-run"
		return analyze(ctx)
	}
	out, err := h.run(BootstrapOptions{WriteWiki: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	assertContains(t, out, "Skipped memory/updated.md — the wiki changed since it was read", "Wrote 1 page and pushed to the acme/widgets wiki.\n")
	s := h.state()
	if s.Pages["memory/new.md"].BaseSHA256 != hashPage("# New\n") {
		t.Errorf("the pushed page must be clean: %+v", s.Pages["memory/new.md"])
	}
	if s.Pages["memory/updated.md"].BaseSHA256 != hashPage("# Updated\n\nBefore.\n") {
		t.Errorf("the skipped update must stay dirty: %+v", s.Pages["memory/updated.md"])
	}
}

// TestRun_WriteWikiEveryPageSkipped covers a push in which nothing is written:
// the only dirty page is an update and the wiki moved during the run.
func TestRun_WriteWikiEveryPageSkipped(t *testing.T) {
	h := newHarness(t, handClosedIssues(1))
	h.wiki = newFakeWiki(t, "wiki-head", map[string]string{"memory/shared.md": "# Shared\n\nBefore.\n"})
	h.analyze = func(UnitContext) (*capture.CaptureResult, error) {
		h.wiki.head = "moved-during-the-run"
		return &capture.CaptureResult{Memory: []capture.ProposedPage{{Path: "memory/shared.md", Body: "# Shared\n\nAfter."}}}, nil
	}
	h.tty, h.answer = true, "y\n"
	out, err := h.run(BootstrapOptions{WriteWiki: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	assertContains(t, out, "Nothing to write — every page was an update the diverged wiki would clobber.\n")
	if len(h.wiki.pushed) != 0 {
		t.Errorf("pushed %d times, want 0", len(h.wiki.pushed))
	}
	if got := h.state().Pages["memory/shared.md"]; got.BaseSHA256 != hashPage("# Shared\n\nBefore.\n") {
		t.Errorf("the skipped update must stay dirty: %+v", got)
	}
}

// TestRun_WriteWikiKeepsTheMarkerOfAWikiPage covers a page that came from the
// wiki with a provenance marker and that the operator corrects by hand: the
// push writes it under the marker it had.
func TestRun_WriteWikiKeepsTheMarkerOfAWikiPage(t *testing.T) {
	h := newHarness(t, Listing{})
	h.wiki = newFakeWiki(t, "wiki-head", map[string]string{"memory/captured.md": marker + "# Captured\n"})
	if _, err := h.run(BootstrapOptions{}); err != nil {
		t.Fatalf("first run: %v", err)
	}
	if err := os.WriteFile(filepath.Join(h.dir, "pages", "memory", "captured.md"), []byte("# Captured\n\nCorrected by hand.\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	h.tty, h.answer = true, "y\n"
	out, err := h.run(BootstrapOptions{WriteWiki: true})
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	assertContains(t, out, "- `memory/captured.md` (update) from acme/widgets#7\n")
	if want := marker + "# Captured\n\nCorrected by hand.\n"; len(h.wiki.pushed) != 1 || h.wiki.pushed[0][0].Content != want {
		t.Errorf("pushed %+v, want %q", h.wiki.pushed, want)
	}
}

// TestRun_WriteWikiRefusesAnotherWikiThanTheRuns covers a state.json that
// names a wiki the run is not configured for, at the commit the run's wiki is
// at, so the refresh leaves the name alone: the push must not follow it.
func TestRun_WriteWikiRefusesAnotherWikiThanTheRuns(t *testing.T) {
	h := newHarness(t, handClosedIssues(1))
	s := newState(testRepoRef)
	s.WikiRepo, s.WikiCommit = "mallory/notes", "wiki-head"
	if err := s.Save(h.dir); err != nil {
		t.Fatalf("Save: %v", err)
	}
	h.tty, h.answer = true, "y\n"

	out, err := h.run(BootstrapOptions{WriteWiki: true})
	want := "the pages in " + h.dir + ` were last refreshed from the "mallory/notes" wiki, and this run writes to the acme/widgets wiki; `
	if err == nil || !strings.HasPrefix(err.Error(), want) {
		t.Fatalf("err = %v, want it to start with %q", err, want)
	}
	if h.wiki.repo != testRepoRef || len(h.wiki.pushed) != 0 || strings.Contains(out, "will write") {
		t.Errorf("cloned %q, pushed %d times, want only the refresh of the run's wiki:\n%s", h.wiki.repo, len(h.wiki.pushed), out)
	}
}

// TestRun_WriteWikiRefusesASymlinkedMemoryDirectory covers a wiki that holds
// memory/ as a symbolic link to a directory of the operator's machine: the
// push writes nothing through it.
func TestRun_WriteWikiRefusesASymlinkedMemoryDirectory(t *testing.T) {
	h := newHarness(t, handClosedIssues(1))
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(h.wiki.dir, "memory")); err != nil {
		t.Fatal(err)
	}
	h.tty, h.answer = true, "y\n"
	captureLogs(t)

	_, err := h.run(BootstrapOptions{WriteWiki: true})
	const want = "the acme/widgets wiki holds memory as a symbolic link; refusing to write through it"
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v, want %q", err, want)
	}
	if files := dirSnapshot(t, outside); len(h.wiki.pushed) != 0 || len(files) != 0 {
		t.Errorf("pushed %d times and wrote %v where the link points, want neither", len(h.wiki.pushed), files)
	}
	if got := h.state().Pages["memory/unit-issue-1.md"]; got.BaseSHA256 != "" {
		t.Errorf("the page must stay dirty: %+v", got)
	}
}
