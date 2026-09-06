package elaborate

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/planwerk/planwerk-agent/internal/cache"
	"github.com/planwerk/planwerk-agent/internal/github"
	"github.com/planwerk/planwerk-agent/internal/github/githubtest"
)

type fakeClaude struct {
	calls int32
	fn    func(dir string, ctx Context) (*Result, error)
}

func (f *fakeClaude) Elaborate(dir string, ctx Context) (*Result, error) {
	atomic.AddInt32(&f.calls, 1)
	if f.fn == nil {
		return &Result{Description: "desc", Motivation: "motiv"}, nil
	}
	return f.fn(dir, ctx)
}

type fakeReviewer struct {
	calls int32
	fn    func(dir string, ctx Context, draft string) (*ReviewResult, error)
}

func (f *fakeReviewer) ReviewElaboration(dir string, ctx Context, draft string) (*ReviewResult, error) {
	atomic.AddInt32(&f.calls, 1)
	return f.fn(dir, ctx, draft)
}

func reviewLoopGitHub(t *testing.T, repo *github.Repo) *githubtest.Fake {
	t.Helper()
	return &githubtest.Fake{
		GetIssueFn: func(owner, name string, number int) (*github.Issue, error) {
			return &github.Issue{Owner: owner, Name: name, Number: number, Title: "Title", Body: "Body"}, nil
		},
		CloneRepoFn: func(ref string) (*github.Repo, error) { return repo, nil },
	}
}

func TestRun_ReviewLoop_RefinesUntilApproved(t *testing.T) {
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)
	patternDir := seedPatternDir(t)
	gh := reviewLoopGitHub(t, fakeRepo(t, "acme", "widgets"))

	var elabCalls int32
	cl := &fakeClaude{fn: func(dir string, ctx Context) (*Result, error) {
		if atomic.AddInt32(&elabCalls, 1) == 1 {
			return &Result{Title: "Title", Description: "first draft"}, nil
		}
		if ctx.PriorDraft == "" || len(ctx.ReviewGaps) == 0 {
			t.Errorf("refine pass missing prior draft/gaps: %+v", ctx)
		}
		return &Result{Title: "Title", Description: "refined draft"}, nil
	}}
	rv := &fakeReviewer{}
	rv.fn = func(dir string, ctx Context, draft string) (*ReviewResult, error) {
		if atomic.LoadInt32(&rv.calls) == 1 {
			return &ReviewResult{Score: 4, Gaps: []string{"close gap X"}, ToReachTen: "name the io.EOF path"}, nil
		}
		return &ReviewResult{Score: 9}, nil
	}
	r := &Runner{Claude: cl, GitHub: gh, Reviewer: rv}

	opts := baseOpts(patternDir)
	opts.Review = true
	var out bytes.Buffer
	if err := r.Run(&out, opts); err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if got := atomic.LoadInt32(&elabCalls); got != 2 {
		t.Errorf("elaborate calls = %d, want 2 (initial + one refine)", got)
	}
	if got := atomic.LoadInt32(&rv.calls); got != 2 {
		t.Errorf("reviewer calls = %d, want 2", got)
	}
	s := out.String()
	if !strings.Contains(s, "refined draft") || strings.Contains(s, "Reviewer Notes") {
		t.Errorf("expected refined draft and no unresolved-gap note, got:\n%s", s)
	}
	if !strings.Contains(s, "**Executability score:** 9/10") {
		t.Errorf("expected passing score surfaced, got:\n%s", s)
	}
}

func TestRun_ReviewLoop_SurfacesUnresolvedGaps(t *testing.T) {
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)
	patternDir := seedPatternDir(t)
	gh := reviewLoopGitHub(t, fakeRepo(t, "acme", "widgets"))

	// Each refinement genuinely moves the draft — it just never moves it far
	// enough. A fake that returned the same text twice would exit through the
	// unchanged-draft guard instead of exhausting the iteration budget this test
	// is about.
	var elabCalls int32
	cl := &fakeClaude{fn: func(dir string, ctx Context) (*Result, error) {
		n := atomic.AddInt32(&elabCalls, 1)
		return &Result{Title: "Title", Description: fmt.Sprintf("draft revision %d", n)}, nil
	}}
	rv := &fakeReviewer{fn: func(dir string, ctx Context, draft string) (*ReviewResult, error) {
		return &ReviewResult{Score: 4, Gaps: []string{"persistent gap Y"}, ToReachTen: "enumerate the empty-slice path"}, nil
	}}
	r := &Runner{Claude: cl, GitHub: gh, Reviewer: rv}

	opts := baseOpts(patternDir)
	opts.Review = true
	opts.MaxReviewIterations = 2
	var out bytes.Buffer
	if err := r.Run(&out, opts); err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if got := atomic.LoadInt32(&rv.calls); got != 2 {
		t.Errorf("reviewer calls = %d, want 2 (bounded by MaxReviewIterations)", got)
	}
	s := out.String()
	if !strings.Contains(s, "Reviewer Notes (unresolved)") || !strings.Contains(s, "persistent gap Y") {
		t.Errorf("expected unresolved-gap note in output, got:\n%s", s)
	}
	if !strings.Contains(s, "**Executability score:** 4/10") {
		t.Errorf("expected near-miss score surfaced, got:\n%s", s)
	}
	if !strings.Contains(s, "enumerate the empty-slice path") {
		t.Errorf("expected the what-a-10-looks-like target surfaced, got:\n%s", s)
	}
}

func TestRun_ReviewLoop_DisabledByDefault(t *testing.T) {
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)
	patternDir := seedPatternDir(t)
	gh := reviewLoopGitHub(t, fakeRepo(t, "acme", "widgets"))

	cl := &fakeClaude{fn: func(dir string, ctx Context) (*Result, error) {
		return &Result{Title: "Title", Description: "draft"}, nil
	}}
	rv := &fakeReviewer{fn: func(dir string, ctx Context, draft string) (*ReviewResult, error) {
		t.Fatal("reviewer must not run when opts.Review is false")
		return nil, nil
	}}
	r := &Runner{Claude: cl, GitHub: gh, Reviewer: rv}

	if err := r.Run(&bytes.Buffer{}, baseOpts(patternDir)); err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if got := atomic.LoadInt32(&rv.calls); got != 0 {
		t.Errorf("reviewer calls = %d, want 0 when --review is off", got)
	}
}

func seedPatternDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	body := "# Review Pattern: Test Pattern\n\n**Review-Area**: testing\n\n## What to check\n\n- presence\n"
	if err := os.WriteFile(filepath.Join(dir, "test.md"), []byte(body), 0o600); err != nil {
		t.Fatalf("writing pattern: %v", err)
	}
	return dir
}

func fakeRepo(t *testing.T, owner, name string) *github.Repo {
	t.Helper()
	return &github.Repo{Owner: owner, Name: name, Dir: t.TempDir()}
}

func baseOpts(patternDir string) Options {
	return Options{
		IssueRef:        "acme/widgets#42",
		PatternDirs:     []string{patternDir},
		NoLocalPatterns: true,
		NoRepoPatterns:  true,
		Format:          "markdown",
		Version:         "test",
	}
}

func TestRun_RendersMarkdownAndCachesResult(t *testing.T) {
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	patternDir := seedPatternDir(t)
	repo := fakeRepo(t, "acme", "widgets")
	gh := &githubtest.Fake{
		GetIssueFn: func(owner, name string, number int) (*github.Issue, error) {
			return &github.Issue{Owner: owner, Name: name, Number: number, Title: "Title", Body: "Body", URL: "u"}, nil
		},
		CloneRepoFn: func(ref string) (*github.Repo, error) { return repo, nil },
	}
	cl := &fakeClaude{
		fn: func(dir string, ctx Context) (*Result, error) {
			if ctx.Issue == nil || ctx.Issue.Title != "Title" {
				t.Fatalf("issue not threaded into context: %+v", ctx.Issue)
			}
			return &Result{
				Title:              "Title",
				Description:        "Description body.",
				Motivation:         "Motivation body.",
				AffectedAreas:      []string{"a.go (changes)"},
				AcceptanceCriteria: []string{"AC1", "AC2"},
				NonGoals:           []string{"NG1"},
				References:         []string{"README"},
			}, nil
		},
	}
	r := &Runner{Claude: cl, GitHub: gh}

	var out bytes.Buffer
	if err := r.Run(&out, baseOpts(patternDir)); err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if cl.calls != 1 {
		t.Fatalf("Claude calls = %d, want 1", cl.calls)
	}
	got := out.String()
	for _, want := range []string{"Description body.", "Motivation body.", "a.go (changes)", "[ ] AC1", "NG1", "README"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q\n%s", want, got)
		}
	}

	// Second run with same head should hit the cache and not call Claude again.
	cl2 := &fakeClaude{
		fn: func(dir string, ctx Context) (*Result, error) {
			t.Fatal("Claude must not be called on cache hit")
			return nil, nil
		},
	}
	r2 := &Runner{Claude: cl2, GitHub: gh}
	var out2 bytes.Buffer
	if err := r2.Run(&out2, baseOpts(patternDir)); err != nil {
		t.Fatalf("cache-hit Run error: %v", err)
	}
	if !strings.Contains(out2.String(), "Description body.") {
		t.Errorf("cache-hit output missing description, got:\n%s", out2.String())
	}
}

func TestRun_ThreadsMetaAndSiblingContext(t *testing.T) {
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	patternDir := seedPatternDir(t)
	repo := fakeRepo(t, "acme", "widgets")
	gh := &githubtest.Fake{
		GetIssueFn: func(owner, name string, number int) (*github.Issue, error) {
			return &github.Issue{Owner: owner, Name: name, Number: number, Title: "Sub", Body: "Body"}, nil
		},
		GetIssueRelationsFn: func(owner, name string, number int) (*github.IssueRelations, error) {
			return &github.IssueRelations{
				Parent:   &github.Issue{Owner: owner, Name: name, Number: 1, Title: "Meta", Body: "Meta body"},
				Siblings: []github.Issue{{Owner: owner, Name: name, Number: 2, Title: "Sibling", Body: "Sibling body", State: "open"}},
			}, nil
		},
		CloneRepoFn: func(ref string) (*github.Repo, error) { return repo, nil },
	}
	cl := &fakeClaude{fn: func(dir string, ctx Context) (*Result, error) {
		if ctx.MetaIssue == nil || ctx.MetaIssue.Number != 1 {
			t.Fatalf("MetaIssue not threaded into context: %+v", ctx.MetaIssue)
		}
		if len(ctx.SiblingIssues) != 1 || ctx.SiblingIssues[0].Number != 2 {
			t.Fatalf("SiblingIssues not threaded into context: %+v", ctx.SiblingIssues)
		}
		return &Result{Title: "Sub", Description: "d", Motivation: "m"}, nil
	}}
	r := &Runner{Claude: cl, GitHub: gh}

	var out bytes.Buffer
	if err := r.Run(&out, baseOpts(patternDir)); err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if cl.calls != 1 {
		t.Fatalf("Claude calls = %d, want 1", cl.calls)
	}
}

// TestRun_SiblingChangeBustsCache locks the cache-key contribution of the
// relations fingerprint: editing a sibling Sub Issue between two otherwise
// identical runs (same repo head, same issue body) must miss the cache and
// re-elaborate, so a stale sibling cannot leak into the plan.
func TestRun_SiblingChangeBustsCache(t *testing.T) {
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	patternDir := seedPatternDir(t)
	repo := fakeRepo(t, "acme", "widgets")
	siblingBody := "Sibling body v1"
	gh := &githubtest.Fake{
		GetIssueFn: func(owner, name string, number int) (*github.Issue, error) {
			return &github.Issue{Owner: owner, Name: name, Number: number, Title: "Sub", Body: "Body"}, nil
		},
		GetIssueRelationsFn: func(owner, name string, number int) (*github.IssueRelations, error) {
			return &github.IssueRelations{
				Parent:   &github.Issue{Owner: owner, Name: name, Number: 1, Title: "Meta", Body: "Meta body"},
				Siblings: []github.Issue{{Owner: owner, Name: name, Number: 2, Title: "Sibling", Body: siblingBody, State: "open"}},
			}, nil
		},
		CloneRepoFn: func(ref string) (*github.Repo, error) { return repo, nil },
	}
	cl := &fakeClaude{fn: func(dir string, ctx Context) (*Result, error) {
		return &Result{Title: "Sub", Description: "d", Motivation: "m"}, nil
	}}
	r := &Runner{Claude: cl, GitHub: gh}

	var out bytes.Buffer
	if err := r.Run(&out, baseOpts(patternDir)); err != nil {
		t.Fatalf("first Run error: %v", err)
	}
	// Same inputs ⇒ cache hit, no second Claude call.
	if err := r.Run(&out, baseOpts(patternDir)); err != nil {
		t.Fatalf("cache-hit Run error: %v", err)
	}
	if cl.calls != 1 {
		t.Fatalf("after identical re-run, Claude calls = %d, want 1 (cache hit)", cl.calls)
	}
	// Edit the sibling ⇒ relations fingerprint changes ⇒ cache miss.
	siblingBody = "Sibling body v2"
	if err := r.Run(&out, baseOpts(patternDir)); err != nil {
		t.Fatalf("post-edit Run error: %v", err)
	}
	if cl.calls != 2 {
		t.Fatalf("after sibling edit, Claude calls = %d, want 2 (cache miss)", cl.calls)
	}
}

// TestRun_SiblingPRChangeBustsCache locks the cache-key contribution of a
// sibling's linked PRs: when an otherwise-identical sibling gains an open PR
// between two runs (same repo head, same issue and sibling bodies), the
// relations fingerprint must change so the elaboration re-runs and the newly
// prepared implementation is accounted for instead of served from a stale cache.
func TestRun_SiblingPRChangeBustsCache(t *testing.T) {
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	patternDir := seedPatternDir(t)
	repo := fakeRepo(t, "acme", "widgets")
	var siblingPRs []github.LinkedPR
	gh := &githubtest.Fake{
		GetIssueFn: func(owner, name string, number int) (*github.Issue, error) {
			return &github.Issue{Owner: owner, Name: name, Number: number, Title: "Sub", Body: "Body"}, nil
		},
		GetIssueRelationsFn: func(owner, name string, number int) (*github.IssueRelations, error) {
			return &github.IssueRelations{
				Parent:   &github.Issue{Owner: owner, Name: name, Number: 1, Title: "Meta", Body: "Meta body"},
				Siblings: []github.Issue{{Owner: owner, Name: name, Number: 2, Title: "Sibling", Body: "Sibling body", State: "open", LinkedPRs: siblingPRs}},
			}, nil
		},
		CloneRepoFn: func(ref string) (*github.Repo, error) { return repo, nil },
	}
	cl := &fakeClaude{fn: func(dir string, ctx Context) (*Result, error) {
		return &Result{Title: "Sub", Description: "d", Motivation: "m"}, nil
	}}
	r := &Runner{Claude: cl, GitHub: gh}

	var out bytes.Buffer
	if err := r.Run(&out, baseOpts(patternDir)); err != nil {
		t.Fatalf("first Run error: %v", err)
	}
	if err := r.Run(&out, baseOpts(patternDir)); err != nil {
		t.Fatalf("cache-hit Run error: %v", err)
	}
	if cl.calls != 1 {
		t.Fatalf("after identical re-run, Claude calls = %d, want 1 (cache hit)", cl.calls)
	}
	// A sibling gains an open PR ⇒ relations fingerprint changes ⇒ cache miss.
	siblingPRs = []github.LinkedPR{{Number: 9, Title: "Implement sibling", URL: "https://example.com/pull/9", State: "open"}}
	if err := r.Run(&out, baseOpts(patternDir)); err != nil {
		t.Fatalf("post-PR Run error: %v", err)
	}
	if cl.calls != 2 {
		t.Fatalf("after sibling PR appears, Claude calls = %d, want 2 (cache miss)", cl.calls)
	}
}

func TestRun_NoCacheBypassesCache(t *testing.T) {
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	patternDir := seedPatternDir(t)
	repo := fakeRepo(t, "acme", "widgets")
	gh := &githubtest.Fake{
		GetIssueFn: func(owner, name string, number int) (*github.Issue, error) {
			return &github.Issue{Owner: owner, Name: name, Number: number, Title: "T", Body: "B"}, nil
		},
		CloneRepoFn: func(ref string) (*github.Repo, error) { return repo, nil },
	}
	cl := &fakeClaude{
		fn: func(dir string, ctx Context) (*Result, error) {
			return &Result{Description: "fresh"}, nil
		},
	}
	r := &Runner{Claude: cl, GitHub: gh}

	opts := baseOpts(patternDir)
	if err := r.Run(&bytes.Buffer{}, opts); err != nil {
		t.Fatalf("first Run: %v", err)
	}
	opts.NoCache = true
	if err := r.Run(&bytes.Buffer{}, opts); err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if cl.calls != 2 {
		t.Errorf("Claude calls = %d, want 2 (NoCache bypasses cache)", cl.calls)
	}
}

func TestRun_UpdateModes(t *testing.T) {
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	patternDir := seedPatternDir(t)
	repo := fakeRepo(t, "acme", "widgets")
	gh := &githubtest.Fake{
		GetIssueFn: func(owner, name string, number int) (*github.Issue, error) {
			return &github.Issue{Owner: owner, Name: name, Number: number, Title: "T", Body: "B"}, nil
		},
		CloneRepoFn: func(ref string) (*github.Repo, error) { return repo, nil },
	}
	cl := &fakeClaude{}
	r := &Runner{Claude: cl, GitHub: gh}

	t.Run("UpdateNone leaves issue alone", func(t *testing.T) {
		opts := baseOpts(patternDir)
		opts.NoCache = true
		if err := r.Run(&bytes.Buffer{}, opts); err != nil {
			t.Fatalf("Run: %v", err)
		}
		if gh.Count("EditIssueBody") != 0 || gh.Count("AddIssueComment") != 0 {
			t.Errorf("UpdateNone should not call edit/comment, got edit=%d comment=%d", gh.Count("EditIssueBody"), gh.Count("AddIssueComment"))
		}
	})

	t.Run("UpdateReplace edits body", func(t *testing.T) {
		opts := baseOpts(patternDir)
		opts.NoCache = true
		opts.UpdateMode = UpdateReplace
		if err := r.Run(&bytes.Buffer{}, opts); err != nil {
			t.Fatalf("Run: %v", err)
		}
		if gh.Count("EditIssueBody") != 1 {
			t.Errorf("UpdateReplace should call EditIssueBody once, got %d", gh.Count("EditIssueBody"))
		}
	})

	t.Run("UpdateComment posts comment", func(t *testing.T) {
		opts := baseOpts(patternDir)
		opts.NoCache = true
		opts.UpdateMode = UpdateComment
		if err := r.Run(&bytes.Buffer{}, opts); err != nil {
			t.Fatalf("Run: %v", err)
		}
		if gh.Count("AddIssueComment") != 1 {
			t.Errorf("UpdateComment should call AddIssueComment once, got %d", gh.Count("AddIssueComment"))
		}
	})
}

func TestRun_GetIssueErrorPropagates(t *testing.T) {
	gh := &githubtest.Fake{
		GetIssueFn: func(owner, name string, number int) (*github.Issue, error) {
			return nil, errors.New("gh boom")
		},
	}
	r := &Runner{Claude: &fakeClaude{}, GitHub: gh}
	err := r.Run(&bytes.Buffer{}, baseOpts(t.TempDir()))
	if err == nil || !strings.Contains(err.Error(), "fetching issue") {
		t.Fatalf("expected fetching issue error, got: %v", err)
	}
}

func TestRun_InvalidIssueRefFailsBeforeFetch(t *testing.T) {
	gh := &githubtest.Fake{
		GetIssueFn: func(owner, name string, number int) (*github.Issue, error) {
			t.Fatal("GetIssue must not be called for invalid ref")
			return nil, nil
		},
	}
	r := &Runner{Claude: &fakeClaude{}, GitHub: gh}
	opts := baseOpts(t.TempDir())
	opts.IssueRef = "not a ref"
	err := r.Run(&bytes.Buffer{}, opts)
	if err == nil || !strings.Contains(err.Error(), "parsing issue ref") {
		t.Fatalf("expected parse error, got: %v", err)
	}
}

func TestBuildIssueBodySectionsAndOrder(t *testing.T) {
	r := &Result{
		Description:        "desc",
		Motivation:         "motiv",
		AffectedAreas:      []string{"a", "", "b"},
		AcceptanceCriteria: []string{"AC1"},
		NonGoals:           []string{"NG"},
		References:         []string{"REF"},
	}
	body := BuildIssueBody(r)
	// section order + checklist syntax
	descIdx := strings.Index(body, "## Description")
	motivIdx := strings.Index(body, "## Motivation")
	areasIdx := strings.Index(body, "## Affected Areas")
	acIdx := strings.Index(body, "## Acceptance Criteria")
	ngIdx := strings.Index(body, "## Non-Goals")
	refIdx := strings.Index(body, "## References")
	for _, p := range []int{descIdx, motivIdx, areasIdx, acIdx, ngIdx, refIdx} {
		if p < 0 {
			t.Fatalf("missing section in body:\n%s", body)
		}
	}
	if descIdx >= motivIdx || motivIdx >= areasIdx || areasIdx >= acIdx || acIdx >= ngIdx || ngIdx >= refIdx {
		t.Fatalf("sections out of order:\n%s", body)
	}
	if !strings.Contains(body, "- [ ] AC1") {
		t.Errorf("acceptance criteria should render as checkbox, got:\n%s", body)
	}
	if strings.Contains(body, "- \n") {
		t.Errorf("blank affected-area entry should be skipped:\n%s", body)
	}
	if !strings.Contains(body, "_Elaborated by [planwerk-agent]") {
		t.Errorf("missing footer:\n%s", body)
	}
}

func TestBuildIssueBody_ReviewScoreAndNotes(t *testing.T) {
	score := func(n int) *int { return &n }

	t.Run("near-miss renders score, notes, and target in order", func(t *testing.T) {
		body := BuildIssueBody(&Result{
			Description:    "desc",
			ReviewScore:    score(4),
			UnresolvedGaps: []string{"persistent gap Y"},
			ReviewTarget:   "enumerate the empty-slice path",
		})
		scoreIdx := strings.Index(body, "**Executability score:** 4/10")
		notesIdx := strings.Index(body, "**Reviewer Notes (unresolved):**")
		gapIdx := strings.Index(body, "persistent gap Y")
		targetIdx := strings.Index(body, "What a 10/10 plan would look like: enumerate the empty-slice path")
		for name, idx := range map[string]int{"score": scoreIdx, "notes": notesIdx, "gap": gapIdx, "target": targetIdx} {
			if idx < 0 {
				t.Fatalf("missing %s in body:\n%s", name, body)
			}
		}
		if scoreIdx >= notesIdx || notesIdx >= gapIdx || gapIdx >= targetIdx {
			t.Fatalf("score/notes/gap/target out of order:\n%s", body)
		}
	})

	t.Run("clean pass renders the score line and no notes block", func(t *testing.T) {
		body := BuildIssueBody(&Result{Description: "desc", ReviewScore: score(10)})
		if !strings.Contains(body, "**Executability score:** 10/10") {
			t.Errorf("expected score line, got:\n%s", body)
		}
		if strings.Contains(body, "Reviewer Notes") {
			t.Errorf("clean pass should not render a notes block, got:\n%s", body)
		}
	})

	t.Run("no reviewer pass renders neither", func(t *testing.T) {
		body := BuildIssueBody(&Result{Description: "desc"})
		if strings.Contains(body, "Executability score") || strings.Contains(body, "Reviewer Notes") {
			t.Errorf("non-review run should render no score or notes, got:\n%s", body)
		}
	})
}

func TestBuildIssueBody_UserStories(t *testing.T) {
	t.Run("populated renders the section between Motivation and Affected Areas", func(t *testing.T) {
		body := BuildIssueBody(&Result{
			Description:   "desc",
			Motivation:    "motiv",
			UserStories:   []Story{{Role: "maintainer", Want: "X", SoThat: "Y", Criteria: []string{"crit one"}}},
			AffectedAreas: []string{"a.go"},
		})
		if !strings.Contains(body, "## User Stories") {
			t.Fatalf("missing User Stories header:\n%s", body)
		}
		if !strings.Contains(body, "- As a maintainer, I want X, so that Y") {
			t.Errorf("story not rendered as a role/want/so-that line:\n%s", body)
		}
		if !strings.Contains(body, "  - crit one") {
			t.Errorf("story criterion not nested under the story:\n%s", body)
		}
		motivIdx := strings.Index(body, "## Motivation")
		storiesIdx := strings.Index(body, "## User Stories")
		areasIdx := strings.Index(body, "## Affected Areas")
		if motivIdx >= storiesIdx || storiesIdx >= areasIdx {
			t.Fatalf("User Stories out of order (motiv=%d stories=%d areas=%d):\n%s", motivIdx, storiesIdx, areasIdx, body)
		}
	})

	t.Run("empty omits the section entirely", func(t *testing.T) {
		body := BuildIssueBody(&Result{Description: "desc", Motivation: "motiv"})
		if strings.Contains(body, "## User Stories") {
			t.Errorf("empty UserStories should render no header or filler, got:\n%s", body)
		}
	})

	t.Run("all-blank story is skipped", func(t *testing.T) {
		body := BuildIssueBody(&Result{
			Description: "desc",
			UserStories: []Story{{Role: " ", Want: "", SoThat: "\t"}},
		})
		if strings.Contains(body, "As a") {
			t.Errorf("a story with blank role/want/so-that should be skipped, got:\n%s", body)
		}
		if strings.Contains(body, "## User Stories") {
			t.Errorf("an all-blank story should leave no orphaned header, got:\n%s", body)
		}
	})
}

func TestRun_FillsTitleFromIssueWhenClaudeOmitsIt(t *testing.T) {
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	patternDir := seedPatternDir(t)
	repo := fakeRepo(t, "acme", "widgets")
	gh := &githubtest.Fake{
		GetIssueFn: func(owner, name string, number int) (*github.Issue, error) {
			return &github.Issue{Owner: owner, Name: name, Number: number, Title: "Original Title", Body: "B"}, nil
		},
		CloneRepoFn: func(ref string) (*github.Repo, error) { return repo, nil },
	}
	cl := &fakeClaude{
		fn: func(dir string, ctx Context) (*Result, error) {
			return &Result{Description: "d"}, nil
		},
	}
	r := &Runner{Claude: cl, GitHub: gh}

	var out bytes.Buffer
	if err := r.Run(&out, baseOpts(patternDir)); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out.String(), "Original Title") {
		t.Errorf("output missing original title, got:\n%s", out.String())
	}
}

func TestRun_LocalUsesCwd(t *testing.T) {
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	patternDir := seedPatternDir(t)
	repo := fakeRepo(t, "acme", "widgets")
	gh := &githubtest.Fake{
		GetIssueFn: func(owner, name string, number int) (*github.Issue, error) {
			return &github.Issue{Owner: owner, Name: name, Number: number, Title: "Title", Body: "Body", URL: "u"}, nil
		},
		CloneRepoFn: func(ref string) (*github.Repo, error) { return repo, nil },
	}
	r := &Runner{Claude: &fakeClaude{}, GitHub: gh}

	opts := baseOpts(patternDir)
	opts.Local = true
	opts.NoCache = true

	if err := r.Run(&bytes.Buffer{}, opts); err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if gh.Count("UseLocalRepo") != 1 {
		t.Errorf("UseLocalRepo calls = %d, want 1", gh.Count("UseLocalRepo"))
	}
	if gh.Count("CloneRepo") != 0 {
		t.Errorf("CloneRepo calls = %d, want 0 in local mode", gh.Count("CloneRepo"))
	}
	if _, err := os.Stat(repo.Dir); err != nil {
		t.Fatalf("local checkout must survive the run: %v", err)
	}
}

// TestRun_ReviewLoop_StopsOnUnchangedDraft covers the loop's one remaining way
// to spin without improving: a refinement that hands back the draft it was
// given. Re-reviewing identical text cannot produce a different verdict, so the
// loop must stop after the first such round — spending one reviewer call, not
// three — and publish the gaps it already knows about.
func TestRun_ReviewLoop_StopsOnUnchangedDraft(t *testing.T) {
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)
	patternDir := seedPatternDir(t)
	gh := reviewLoopGitHub(t, fakeRepo(t, "acme", "widgets"))

	// Every call renders the same body, so the refined draft equals the prior one.
	var elabCalls int32
	cl := &fakeClaude{fn: func(dir string, ctx Context) (*Result, error) {
		atomic.AddInt32(&elabCalls, 1)
		return &Result{Title: "Title", Description: "an unmoved draft"}, nil
	}}
	rv := &fakeReviewer{}
	rv.fn = func(dir string, ctx Context, draft string) (*ReviewResult, error) {
		return &ReviewResult{Score: 4, Gaps: []string{"close gap X"}, ToReachTen: "name the io.EOF path"}, nil
	}
	r := &Runner{Claude: cl, GitHub: gh, Reviewer: rv}

	opts := baseOpts(patternDir)
	opts.Review = true
	var out bytes.Buffer
	if err := r.Run(&out, opts); err != nil {
		t.Fatalf("Run error: %v", err)
	}

	if got := atomic.LoadInt32(&rv.calls); got != 1 {
		t.Errorf("reviewer calls = %d, want 1 (an unchanged draft cannot score differently)", got)
	}
	if got := atomic.LoadInt32(&elabCalls); got != 2 {
		t.Errorf("elaborate calls = %d, want 2 (initial + the one refine that changed nothing)", got)
	}
	body := out.String()
	if !strings.Contains(body, "Reviewer Notes (unresolved):") {
		t.Error("a loop that stopped short must surface its unresolved gaps, not publish silently")
	}
	if !strings.Contains(body, "close gap X") {
		t.Error("the gap the reviewer reported is missing from the published body")
	}
	if !strings.Contains(body, "**Executability score:** 4/10") {
		t.Error("the near-miss score must stay visible on the published body")
	}
}

// continuedBody and continuationComment are a house-format issue whose body
// reached GitHub's cap and continues in one comment (see github.SplitIssueBody).
const (
	continuedBody = "**Category**: feature | **Scope**: Large\n\n## Description\n\nIntro.\n\n" +
		"<!-- planwerk-agent:continued 1/2 -->\n_This body continues in a comment below (part 2 of 2: Acceptance Criteria)._\n\n" +
		"---\n\n_Elaborated by [planwerk-agent](https://github.com/planwerk/planwerk-agent) with Claude_\n"
	continuationComment = "<!-- planwerk-agent:continuation 2/2 -->\n_Issue body, continued (part 2 of 2)._\n\n" +
		"## Acceptance Criteria\n\n- [ ] Criterion from the continuation\n"
)

func TestRun_MergesContinuedSourceBody(t *testing.T) {
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	patternDir := seedPatternDir(t)
	repo := fakeRepo(t, "acme", "widgets")
	gh := &githubtest.Fake{
		GetIssueFn: func(owner, name string, number int) (*github.Issue, error) {
			return &github.Issue{Owner: owner, Name: name, Number: number, Title: "T", Body: continuedBody}, nil
		},
		IssueComments: []github.IssueComment{{ID: "c2", Body: continuationComment}},
		CloneRepoFn:   func(ref string) (*github.Repo, error) { return repo, nil },
	}
	var seen string
	cl := &fakeClaude{fn: func(dir string, ctx Context) (*Result, error) {
		seen = ctx.Issue.Body
		return &Result{Description: "d", Motivation: "m"}, nil
	}}
	r := &Runner{Claude: cl, GitHub: gh}
	opts := baseOpts(patternDir)
	opts.NoCache = true
	if err := r.Run(&bytes.Buffer{}, opts); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if github.IsContinued(seen) {
		t.Error("the elaboration must see the merged body, not the continued marker")
	}
	if !strings.Contains(seen, "Criterion from the continuation") {
		t.Errorf("the continuation's section did not reach the elaboration prompt:\n%s", seen)
	}
}

func TestRun_ContinuedSourceBodyWithMissingPartAborts(t *testing.T) {
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	patternDir := seedPatternDir(t)
	gh := &githubtest.Fake{
		GetIssueFn: func(owner, name string, number int) (*github.Issue, error) {
			return &github.Issue{Owner: owner, Name: name, Number: number, Title: "T", Body: continuedBody}, nil
		},
	}
	cl := &fakeClaude{}
	r := &Runner{Claude: cl, GitHub: gh}
	opts := baseOpts(patternDir)
	opts.NoCache = true
	err := r.Run(&bytes.Buffer{}, opts)
	if err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("a continued body whose part is missing must abort, got %v", err)
	}
	if atomic.LoadInt32(&cl.calls) != 0 {
		t.Error("no elaboration may run against a truncated body")
	}
}

func TestRun_UpdateReplace_ContinuesOversizedBody(t *testing.T) {
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	patternDir := seedPatternDir(t)
	repo := fakeRepo(t, "acme", "widgets")
	var (
		editedID, editedBody string
		deletedIDs           []string
	)
	gh := &githubtest.Fake{
		GetIssueFn: func(owner, name string, number int) (*github.Issue, error) {
			return &github.Issue{Owner: owner, Name: name, Number: number, Title: "T", Body: "B"}, nil
		},
		CloneRepoFn: func(ref string) (*github.Repo, error) { return repo, nil },
		// An earlier, longer write left two continuations; a plan comment sits
		// between them and must be left alone.
		IssueComments: []github.IssueComment{
			{ID: "old2", Body: "<!-- planwerk-agent:continuation 2/3 -->\n\nold two"},
			{ID: "plan", Body: "## Implementation Plan (issue #42)\n\nSTATUS: PLAN_READY"},
			{ID: "old3", Body: "<!-- planwerk-agent:continuation 3/3 -->\n\nold three"},
		},
		EditIssueCommentFn:   func(id, body string) error { editedID, editedBody = id, body; return nil },
		DeleteIssueCommentFn: func(id string) error { deletedIDs = append(deletedIDs, id); return nil },
	}
	// ~90 KB of Description: a body and exactly one continuation.
	huge := strings.Repeat("A paragraph of plan prose, repeated until the body is far over GitHub's cap.\n\n", 1150)
	cl := &fakeClaude{fn: func(dir string, ctx Context) (*Result, error) {
		return &Result{Description: huge, Motivation: "m", AcceptanceCriteria: []string{"Do the thing"}}, nil
	}}
	r := &Runner{Claude: cl, GitHub: gh}
	opts := baseOpts(patternDir)
	opts.NoCache = true
	opts.UpdateMode = UpdateReplace
	var out bytes.Buffer
	if err := r.Run(&out, opts); err != nil {
		t.Fatalf("Run: %v", err)
	}

	edits := gh.Edits()
	if len(edits) != 1 {
		t.Fatalf("EditIssueBody called %d times, want 1", len(edits))
	}
	body := edits[0]
	if len(body) > github.MaxIssueBodyLen {
		t.Errorf("the written body is %d bytes, over the %d cap", len(body), github.MaxIssueBodyLen)
	}
	if !github.IsContinued(body) {
		t.Error("an oversized body must be written with the continued marker")
	}
	if !strings.Contains(body, "_Elaborated by [planwerk-agent](") {
		t.Error("the footer must stay on the body")
	}
	if gh.Count("AddIssueComment") != 0 || editedID != "old2" || len(deletedIDs) != 1 || deletedIDs[0] != "old3" {
		t.Errorf("the first existing continuation must be rewritten and the surplus one deleted; added=%d edited=%q deleted=%v",
			gh.Count("AddIssueComment"), editedID, deletedIDs)
	}
	if !strings.Contains(editedBody, "## Acceptance Criteria") || !strings.Contains(editedBody, "- [ ] Do the thing") {
		t.Errorf("the criteria must land in the continuation:\n%s", editedBody[:200])
	}
	merged, err := github.MergeContinuations(body, []github.IssueComment{{ID: "old2", Body: editedBody}})
	if err != nil {
		t.Fatalf("merging what was written: %v", err)
	}
	if !strings.Contains(out.String(), merged) {
		t.Error("the body and its continuation must merge back into the rendered elaboration")
	}
}

func TestRun_UpdateComment_SplitsOversizedBody(t *testing.T) {
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	patternDir := seedPatternDir(t)
	repo := fakeRepo(t, "acme", "widgets")
	gh := &githubtest.Fake{
		GetIssueFn: func(owner, name string, number int) (*github.Issue, error) {
			return &github.Issue{Owner: owner, Name: name, Number: number, Title: "T", Body: "B"}, nil
		},
		CloneRepoFn: func(ref string) (*github.Repo, error) { return repo, nil },
	}
	huge := strings.Repeat("A paragraph of plan prose, repeated until the body is far over GitHub's cap.\n\n", 1150)
	cl := &fakeClaude{fn: func(dir string, ctx Context) (*Result, error) {
		return &Result{Description: huge, Motivation: "m"}, nil
	}}
	r := &Runner{Claude: cl, GitHub: gh}
	opts := baseOpts(patternDir)
	opts.NoCache = true
	opts.UpdateMode = UpdateComment
	if err := r.Run(&bytes.Buffer{}, opts); err != nil {
		t.Fatalf("Run: %v", err)
	}
	comments := gh.Comments()
	if len(comments) != 2 {
		t.Fatalf("an oversized elaboration is posted as %d comment(s), want 2", len(comments))
	}
	for i, c := range comments {
		if len(c) > github.MaxIssueBodyLen {
			t.Errorf("comment %d is %d bytes, over the cap", i+1, len(c))
		}
	}
	if !strings.HasPrefix(comments[1], "<!-- planwerk-agent:continuation 2/2 -->") {
		t.Errorf("the second comment must be marked as the continuation of the first; got %q", comments[1][:60])
	}
}

func TestRun_ReviewLoop_SizeGapRefinesAcceptedDraft(t *testing.T) {
	restore := cache.SetDir(t.TempDir())
	t.Cleanup(restore)

	patternDir := seedPatternDir(t)
	repo := fakeRepo(t, "acme", "widgets")
	gh := reviewLoopGitHub(t, repo)

	long := strings.Repeat("word ", BodyBudget/5+500)
	var gapsSeen []string
	cl := &fakeClaude{fn: func(dir string, ctx Context) (*Result, error) {
		if ctx.PriorDraft == "" {
			return &Result{Description: long, Motivation: "m"}, nil
		}
		gapsSeen = ctx.ReviewGaps
		return &Result{Description: "Tightened.", Motivation: "m"}, nil
	}}
	// The reviewer is content with both drafts: only the size keeps the
	// first one in the loop.
	rv := &fakeReviewer{fn: func(dir string, ctx Context, draft string) (*ReviewResult, error) {
		return &ReviewResult{Score: 9}, nil
	}}
	r := &Runner{Claude: cl, GitHub: gh, Reviewer: rv}
	opts := baseOpts(patternDir)
	opts.NoCache = true
	opts.Review = true
	var out bytes.Buffer
	if err := r.Run(&out, opts); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := atomic.LoadInt32(&cl.calls); got != 2 {
		t.Errorf("elaborate called %d times, want 2 (draft, then one size refinement)", got)
	}
	if len(gapsSeen) != 1 || !strings.HasPrefix(gapsSeen[0], "Size") || !strings.Contains(gapsSeen[0], fmt.Sprint(BodyBudget)) {
		t.Errorf("the refinement must be handed the size gap alone, got %q", gapsSeen)
	}
	if strings.Contains(out.String(), "Reviewer Notes") {
		t.Error("a refinement that came under budget leaves no unresolved gap")
	}
	if !strings.Contains(out.String(), "Tightened.") {
		t.Error("the tightened draft must be the one rendered")
	}
}
