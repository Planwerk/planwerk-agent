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
)

type fakeGitHub struct {
	getIssue          func(owner, name string, number int) (*github.Issue, error)
	getIssueRelations func(owner, name string, number int) (*github.IssueRelations, error)
	defaultBranchHEAD func(owner, name string) (string, error)
	cloneRepo         func(ref string) (*github.Repo, error)
	editIssueBody     func(owner, name string, number int, body string) error
	addIssueComment   func(owner, name string, number int, body string) (string, error)

	editCalls    int32
	commentCalls int32

	cloneCalls      atomic.Int32
	cloneLocalCalls atomic.Int32
}

func (f *fakeGitHub) GetIssue(owner, name string, number int) (*github.Issue, error) {
	return f.getIssue(owner, name, number)
}

// GetIssueRelations defaults to "no relations" (the issue stands alone) so
// tests that do not exercise the Meta/sibling path need not set it.
func (f *fakeGitHub) GetIssueRelations(owner, name string, number int) (*github.IssueRelations, error) {
	if f.getIssueRelations == nil {
		return &github.IssueRelations{}, nil
	}
	return f.getIssueRelations(owner, name, number)
}

func (f *fakeGitHub) DefaultBranchHEAD(owner, name string) (string, error) {
	if f.defaultBranchHEAD == nil {
		return "head-sha", nil
	}
	return f.defaultBranchHEAD(owner, name)
}

func (f *fakeGitHub) CloneRepo(ref string) (*github.Repo, error) {
	f.cloneCalls.Add(1)
	return f.cloneRepo(ref)
}

// CloneRepoLocal mirrors github.UseLocalRepo: it returns a Local repo so
// Cleanup is a no-op.
func (f *fakeGitHub) CloneRepoLocal(ref string, _ github.LocalOptions) (*github.Repo, error) {
	f.cloneLocalCalls.Add(1)
	repo, err := f.cloneRepo(ref)
	if err != nil {
		return nil, err
	}
	repo.Local = true
	return repo, nil
}

func (f *fakeGitHub) EditIssueBody(owner, name string, number int, body string) error {
	atomic.AddInt32(&f.editCalls, 1)
	if f.editIssueBody == nil {
		return nil
	}
	return f.editIssueBody(owner, name, number, body)
}

func (f *fakeGitHub) AddIssueComment(owner, name string, number int, body string) (string, error) {
	atomic.AddInt32(&f.commentCalls, 1)
	if f.addIssueComment == nil {
		return "https://example/comment", nil
	}
	return f.addIssueComment(owner, name, number, body)
}

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

func reviewLoopGitHub(t *testing.T, repo *github.Repo) *fakeGitHub {
	t.Helper()
	return &fakeGitHub{
		getIssue: func(owner, name string, number int) (*github.Issue, error) {
			return &github.Issue{Owner: owner, Name: name, Number: number, Title: "Title", Body: "Body"}, nil
		},
		cloneRepo: func(ref string) (*github.Repo, error) { return repo, nil },
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
	gh := &fakeGitHub{
		getIssue: func(owner, name string, number int) (*github.Issue, error) {
			return &github.Issue{Owner: owner, Name: name, Number: number, Title: "Title", Body: "Body", URL: "u"}, nil
		},
		cloneRepo: func(ref string) (*github.Repo, error) { return repo, nil },
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
	gh := &fakeGitHub{
		getIssue: func(owner, name string, number int) (*github.Issue, error) {
			return &github.Issue{Owner: owner, Name: name, Number: number, Title: "Sub", Body: "Body"}, nil
		},
		getIssueRelations: func(owner, name string, number int) (*github.IssueRelations, error) {
			return &github.IssueRelations{
				Parent:   &github.Issue{Owner: owner, Name: name, Number: 1, Title: "Meta", Body: "Meta body"},
				Siblings: []github.Issue{{Owner: owner, Name: name, Number: 2, Title: "Sibling", Body: "Sibling body", State: "open"}},
			}, nil
		},
		cloneRepo: func(ref string) (*github.Repo, error) { return repo, nil },
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
	gh := &fakeGitHub{
		getIssue: func(owner, name string, number int) (*github.Issue, error) {
			return &github.Issue{Owner: owner, Name: name, Number: number, Title: "Sub", Body: "Body"}, nil
		},
		getIssueRelations: func(owner, name string, number int) (*github.IssueRelations, error) {
			return &github.IssueRelations{
				Parent:   &github.Issue{Owner: owner, Name: name, Number: 1, Title: "Meta", Body: "Meta body"},
				Siblings: []github.Issue{{Owner: owner, Name: name, Number: 2, Title: "Sibling", Body: siblingBody, State: "open"}},
			}, nil
		},
		cloneRepo: func(ref string) (*github.Repo, error) { return repo, nil },
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
	gh := &fakeGitHub{
		getIssue: func(owner, name string, number int) (*github.Issue, error) {
			return &github.Issue{Owner: owner, Name: name, Number: number, Title: "Sub", Body: "Body"}, nil
		},
		getIssueRelations: func(owner, name string, number int) (*github.IssueRelations, error) {
			return &github.IssueRelations{
				Parent:   &github.Issue{Owner: owner, Name: name, Number: 1, Title: "Meta", Body: "Meta body"},
				Siblings: []github.Issue{{Owner: owner, Name: name, Number: 2, Title: "Sibling", Body: "Sibling body", State: "open", LinkedPRs: siblingPRs}},
			}, nil
		},
		cloneRepo: func(ref string) (*github.Repo, error) { return repo, nil },
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
	gh := &fakeGitHub{
		getIssue: func(owner, name string, number int) (*github.Issue, error) {
			return &github.Issue{Owner: owner, Name: name, Number: number, Title: "T", Body: "B"}, nil
		},
		cloneRepo: func(ref string) (*github.Repo, error) { return repo, nil },
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
	gh := &fakeGitHub{
		getIssue: func(owner, name string, number int) (*github.Issue, error) {
			return &github.Issue{Owner: owner, Name: name, Number: number, Title: "T", Body: "B"}, nil
		},
		cloneRepo: func(ref string) (*github.Repo, error) { return repo, nil },
	}
	cl := &fakeClaude{}
	r := &Runner{Claude: cl, GitHub: gh}

	t.Run("UpdateNone leaves issue alone", func(t *testing.T) {
		opts := baseOpts(patternDir)
		opts.NoCache = true
		if err := r.Run(&bytes.Buffer{}, opts); err != nil {
			t.Fatalf("Run: %v", err)
		}
		if gh.editCalls != 0 || gh.commentCalls != 0 {
			t.Errorf("UpdateNone should not call edit/comment, got edit=%d comment=%d", gh.editCalls, gh.commentCalls)
		}
	})

	t.Run("UpdateReplace edits body", func(t *testing.T) {
		opts := baseOpts(patternDir)
		opts.NoCache = true
		opts.UpdateMode = UpdateReplace
		if err := r.Run(&bytes.Buffer{}, opts); err != nil {
			t.Fatalf("Run: %v", err)
		}
		if gh.editCalls != 1 {
			t.Errorf("UpdateReplace should call EditIssueBody once, got %d", gh.editCalls)
		}
	})

	t.Run("UpdateComment posts comment", func(t *testing.T) {
		opts := baseOpts(patternDir)
		opts.NoCache = true
		opts.UpdateMode = UpdateComment
		if err := r.Run(&bytes.Buffer{}, opts); err != nil {
			t.Fatalf("Run: %v", err)
		}
		if gh.commentCalls != 1 {
			t.Errorf("UpdateComment should call AddIssueComment once, got %d", gh.commentCalls)
		}
	})
}

func TestRun_GetIssueErrorPropagates(t *testing.T) {
	gh := &fakeGitHub{
		getIssue: func(owner, name string, number int) (*github.Issue, error) {
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
	gh := &fakeGitHub{
		getIssue: func(owner, name string, number int) (*github.Issue, error) {
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
	gh := &fakeGitHub{
		getIssue: func(owner, name string, number int) (*github.Issue, error) {
			return &github.Issue{Owner: owner, Name: name, Number: number, Title: "Original Title", Body: "B"}, nil
		},
		cloneRepo: func(ref string) (*github.Repo, error) { return repo, nil },
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
	gh := &fakeGitHub{
		getIssue: func(owner, name string, number int) (*github.Issue, error) {
			return &github.Issue{Owner: owner, Name: name, Number: number, Title: "Title", Body: "Body", URL: "u"}, nil
		},
		cloneRepo: func(ref string) (*github.Repo, error) { return repo, nil },
	}
	r := &Runner{Claude: &fakeClaude{}, GitHub: gh}

	opts := baseOpts(patternDir)
	opts.Local = true
	opts.NoCache = true

	if err := r.Run(&bytes.Buffer{}, opts); err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if gh.cloneLocalCalls.Load() != 1 {
		t.Errorf("CloneRepoLocal calls = %d, want 1", gh.cloneLocalCalls.Load())
	}
	if gh.cloneCalls.Load() != 0 {
		t.Errorf("CloneRepo calls = %d, want 0 in local mode", gh.cloneCalls.Load())
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
