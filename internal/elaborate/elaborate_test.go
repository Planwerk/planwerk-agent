package elaborate

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/planwerk/planwerk-review/internal/cache"
	"github.com/planwerk/planwerk-review/internal/github"
)

type fakeGitHub struct {
	getIssue          func(owner, name string, number int) (*github.Issue, error)
	defaultBranchHEAD func(owner, name string) (string, error)
	cloneRepo         func(ref string) (*github.Repo, error)
	editIssueBody     func(owner, name string, number int, body string) error
	addIssueComment   func(owner, name string, number int, body string) (string, error)

	editCalls    int32
	commentCalls int32
}

func (f *fakeGitHub) GetIssue(owner, name string, number int) (*github.Issue, error) {
	return f.getIssue(owner, name, number)
}

func (f *fakeGitHub) DefaultBranchHEAD(owner, name string) (string, error) {
	if f.defaultBranchHEAD == nil {
		return "head-sha", nil
	}
	return f.defaultBranchHEAD(owner, name)
}

func (f *fakeGitHub) CloneRepo(ref string) (*github.Repo, error) {
	return f.cloneRepo(ref)
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
	descIdx := strings.Index(body, "**Description:**")
	motivIdx := strings.Index(body, "**Motivation:**")
	areasIdx := strings.Index(body, "**Affected Areas:**")
	acIdx := strings.Index(body, "**Acceptance Criteria:**")
	ngIdx := strings.Index(body, "**Non-Goals:**")
	refIdx := strings.Index(body, "**References:**")
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
	if !strings.Contains(body, "_Elaborated by [planwerk-review]") {
		t.Errorf("missing footer:\n%s", body)
	}
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
