package meta

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/planwerk/planwerk-review/internal/github"
)

type createdIssue struct {
	title  string
	body   string
	labels []string
}

type subLink struct {
	parent int
	child  int
}

// fakeGitHub records every write so tests can assert the exact create/link/edit
// sequence without touching gh. Created issues get sequential numbers starting
// at 101 (distinct from the meta number) and a parseable URL.
type fakeGitHub struct {
	issue *github.Issue

	created    []createdIssue
	links      []subLink
	editBodies []string

	createErrOnTitle string // CreateIssueWithLabels fails when title == this
	linkErrOnChild   int    // AddSubIssue fails when childNumber == this
	editErr          error

	nextNumber int
}

func (f *fakeGitHub) GetIssue(owner, name string, number int) (*github.Issue, error) {
	if f.issue != nil {
		return f.issue, nil
	}
	return &github.Issue{Owner: owner, Name: name, Number: number, Title: "Meta", Body: "Body"}, nil
}

func (f *fakeGitHub) CreateIssueWithLabels(owner, name, title, body string, labels []string) (string, error) {
	if title == f.createErrOnTitle {
		return "", errors.New("gh boom")
	}
	f.created = append(f.created, createdIssue{title: title, body: body, labels: labels})
	if f.nextNumber == 0 {
		f.nextNumber = 101
	}
	num := f.nextNumber
	f.nextNumber++
	return fmt.Sprintf("https://github.com/%s/%s/issues/%d", owner, name, num), nil
}

func (f *fakeGitHub) AddSubIssue(owner, name string, parentNumber, childNumber int) error {
	f.links = append(f.links, subLink{parent: parentNumber, child: childNumber})
	if childNumber == f.linkErrOnChild {
		return errors.New("gh link boom")
	}
	return nil
}

func (f *fakeGitHub) EditIssueBody(owner, name string, number int, body string) error {
	if f.editErr != nil {
		return f.editErr
	}
	f.editBodies = append(f.editBodies, body)
	return nil
}

func splitter(result *Result) MetaFn {
	return func(ctx Context) (*Result, error) { return result, nil }
}

func twoPackageResult() *Result {
	return &Result{
		SubIssues: []SubIssue{
			{Key: "a", Title: "Foundation", Description: "Lay the groundwork.", Scope: "Large"},
			{Key: "b", Title: "Rollout", Description: "Build on the foundation.", Scope: "Medium"},
		},
		MetaBody: "## Work packages\n\n- Foundation {{sub:a}}\n- Rollout {{sub:b}}\n",
	}
}

func baseOpts() Options {
	return Options{IssueRef: "acme/widgets#42", Format: "markdown", Version: "test"}
}

func TestRun_HappyPath_CreatesLinksAndSyncsBody(t *testing.T) {
	gh := &fakeGitHub{}
	r := &Runner{Claude: metaFnAdapter{fn: splitter(twoPackageResult())}, GitHub: gh}

	var out bytes.Buffer
	if err := r.Run(&out, baseOpts()); err != nil {
		t.Fatalf("Run error: %v", err)
	}

	// Created in returned order.
	if len(gh.created) != 2 {
		t.Fatalf("created = %d issues, want 2", len(gh.created))
	}
	if gh.created[0].title != "Foundation" || gh.created[1].title != "Rollout" {
		t.Errorf("created order wrong: %q then %q", gh.created[0].title, gh.created[1].title)
	}
	if !strings.Contains(gh.created[0].body, "Split from #42") {
		t.Errorf("sub-issue body missing meta back-reference:\n%s", gh.created[0].body)
	}

	// Each child linked to the meta issue with its parsed number.
	want := []subLink{{parent: 42, child: 101}, {parent: 42, child: 102}}
	if len(gh.links) != 2 || gh.links[0] != want[0] || gh.links[1] != want[1] {
		t.Errorf("links = %+v, want %+v", gh.links, want)
	}

	// Body edited exactly once, references back-filled, no dangling tokens.
	if len(gh.editBodies) != 1 {
		t.Fatalf("editBodies = %d, want 1", len(gh.editBodies))
	}
	edited := gh.editBodies[0]
	for _, want := range []string{"- Foundation #101", "- Rollout #102"} {
		if !strings.Contains(edited, want) {
			t.Errorf("edited body missing %q:\n%s", want, edited)
		}
	}
	if strings.Contains(edited, "{{sub:") {
		t.Errorf("edited body still carries a placeholder:\n%s", edited)
	}

	// The markdown preview reports the created/linked status.
	if !strings.Contains(out.String(), "linked") {
		t.Errorf("preview missing linked status:\n%s", out.String())
	}
}

func TestRun_DryRun_NoWrites(t *testing.T) {
	gh := &fakeGitHub{}
	r := &Runner{Claude: metaFnAdapter{fn: splitter(twoPackageResult())}, GitHub: gh}

	opts := baseOpts()
	opts.DryRun = true
	var out bytes.Buffer
	if err := r.Run(&out, opts); err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if len(gh.created) != 0 || len(gh.links) != 0 || len(gh.editBodies) != 0 {
		t.Errorf("dry-run wrote to GitHub: created=%d links=%d edits=%d", len(gh.created), len(gh.links), len(gh.editBodies))
	}
	if !strings.Contains(out.String(), "planned") {
		t.Errorf("dry-run preview should mark sub-issues as planned:\n%s", out.String())
	}
}

func TestRun_EmptySplit_NoWrites(t *testing.T) {
	gh := &fakeGitHub{}
	empty := &Result{MetaBody: "Nothing to split here.\n"}
	r := &Runner{Claude: metaFnAdapter{fn: splitter(empty)}, GitHub: gh}

	var out bytes.Buffer
	if err := r.Run(&out, baseOpts()); err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if len(gh.created) != 0 || len(gh.links) != 0 || len(gh.editBodies) != 0 {
		t.Errorf("empty split wrote to GitHub: created=%d links=%d edits=%d", len(gh.created), len(gh.links), len(gh.editBodies))
	}
	if !strings.Contains(out.String(), "No work packages") {
		t.Errorf("empty split should say no work packages:\n%s", out.String())
	}
}

func TestRun_LinkFailureContinues(t *testing.T) {
	gh := &fakeGitHub{linkErrOnChild: 102}
	result := twoPackageResult()
	r := &Runner{Claude: metaFnAdapter{fn: splitter(result)}, GitHub: gh}

	if err := r.Run(&bytes.Buffer{}, baseOpts()); err != nil {
		t.Fatalf("Run error: %v", err)
	}
	// Both sub-issues created and both link attempts made.
	if len(gh.created) != 2 {
		t.Fatalf("created = %d, want 2 (link failure must not abort)", len(gh.created))
	}
	if len(gh.links) != 2 {
		t.Fatalf("links attempted = %d, want 2", len(gh.links))
	}
	if !result.SubIssues[0].Linked {
		t.Errorf("first sub-issue should be linked")
	}
	if result.SubIssues[1].Linked || result.SubIssues[1].LinkError == "" {
		t.Errorf("second sub-issue should record a link error, got linked=%v err=%q",
			result.SubIssues[1].Linked, result.SubIssues[1].LinkError)
	}
	// Body still synced — the references are independent of the native link.
	if len(gh.editBodies) != 1 {
		t.Errorf("body should still sync after a link failure, edits=%d", len(gh.editBodies))
	}
}

func TestRun_UnresolvedPlaceholderSkipsBodyEdit(t *testing.T) {
	gh := &fakeGitHub{}
	// The second placeholder carries inner whitespace, so the exact-match
	// substitution leaves it dangling. The runner must refuse to write a body
	// that still carries a {{sub: token rather than commit a broken reference.
	result := &Result{
		SubIssues: []SubIssue{
			{Key: "a", Title: "Foundation", Description: "D1"},
			{Key: "b", Title: "Rollout", Description: "D2"},
		},
		MetaBody: "- Foundation {{sub:a}}\n- Rollout {{sub: b }}\n",
	}
	r := &Runner{Claude: metaFnAdapter{fn: splitter(result)}, GitHub: gh}

	if err := r.Run(&bytes.Buffer{}, baseOpts()); err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if len(gh.created) != 2 || len(gh.links) != 2 {
		t.Fatalf("expected both sub-issues created and linked, got created=%d links=%d", len(gh.created), len(gh.links))
	}
	if len(gh.editBodies) != 0 {
		t.Errorf("body edit must be skipped when a placeholder cannot resolve, edits=%d", len(gh.editBodies))
	}
}

func TestRun_LabelsPropagateToSubIssues(t *testing.T) {
	gh := &fakeGitHub{}
	r := &Runner{Claude: metaFnAdapter{fn: splitter(twoPackageResult())}, GitHub: gh}

	opts := baseOpts()
	opts.Labels = []string{"enhancement", "meta-child"}
	if err := r.Run(&bytes.Buffer{}, opts); err != nil {
		t.Fatalf("Run error: %v", err)
	}
	for i, c := range gh.created {
		if len(c.labels) != 2 || c.labels[0] != "enhancement" || c.labels[1] != "meta-child" {
			t.Errorf("created[%d].labels = %v, want [enhancement meta-child]", i, c.labels)
		}
	}
}

func TestRun_InvalidIssueRefFailsBeforeFetch(t *testing.T) {
	gh := &fakeGitHub{}
	r := &Runner{Claude: metaFnAdapter{fn: splitter(twoPackageResult())}, GitHub: gh}
	opts := baseOpts()
	opts.IssueRef = "not a ref"
	err := r.Run(&bytes.Buffer{}, opts)
	if err == nil || !strings.Contains(err.Error(), "parsing issue ref") {
		t.Fatalf("expected parse error, got %v", err)
	}
}

func TestRun_InvalidSplitRejected(t *testing.T) {
	gh := &fakeGitHub{}
	bad := &Result{SubIssues: []SubIssue{{Key: "a", Title: "", Description: "D"}}}
	r := &Runner{Claude: metaFnAdapter{fn: splitter(bad)}, GitHub: gh}
	err := r.Run(&bytes.Buffer{}, baseOpts())
	if err == nil || !strings.Contains(err.Error(), "invalid meta split") {
		t.Fatalf("expected invalid-split error, got %v", err)
	}
	if len(gh.created) != 0 {
		t.Errorf("no sub-issues should be filed for an invalid split, got %d", len(gh.created))
	}
}
