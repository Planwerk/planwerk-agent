package prompt

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/planwerk/planwerk-review/internal/github"
)

type fakeGH struct {
	get func(owner, name string, number int) (*github.Issue, error)
}

func (f *fakeGH) GetIssue(owner, name string, number int) (*github.Issue, error) {
	return f.get(owner, name, number)
}

func TestRun_AutoModePicksFixForAuditTitles(t *testing.T) {
	gh := &fakeGH{get: func(owner, name string, number int) (*github.Issue, error) {
		return &github.Issue{
			Owner: owner, Name: name, Number: number,
			Title: "[BLOCKING] SQL Injection (db/users.go:42)",
			Body:  "Body.",
		}, nil
	}}
	r := &Runner{GitHub: gh}
	var out bytes.Buffer
	if err := r.Run(&out, Options{IssueRef: "acme/widgets#1", Mode: ModeAuto, Version: "v"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "fixing a finding raised by planwerk-review's audit") {
		t.Errorf("expected fix-mode header, got:\n%s", got)
	}
	if !strings.Contains(got, "Fixes #N") {
		t.Errorf("fix-mode footer should request a fixes-N commit, got:\n%s", got)
	}
}

func TestRun_AutoModePicksImplementForProposalTitles(t *testing.T) {
	gh := &fakeGH{get: func(owner, name string, number int) (*github.Issue, error) {
		return &github.Issue{
			Owner: owner, Name: name, Number: number,
			Title: "Add label registry",
			Body:  "Plan.",
		}, nil
	}}
	r := &Runner{GitHub: gh}
	var out bytes.Buffer
	if err := r.Run(&out, Options{IssueRef: "acme/widgets#1", Mode: ModeAuto}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out.String(), "implementing a feature") {
		t.Errorf("expected implement-mode header, got:\n%s", out.String())
	}
}

func TestRun_ExplicitModeOverridesAuto(t *testing.T) {
	gh := &fakeGH{get: func(owner, name string, number int) (*github.Issue, error) {
		return &github.Issue{
			Owner: owner, Name: name, Number: number,
			Title: "[BLOCKING] really an audit",
			Body:  "B",
		}, nil
	}}
	r := &Runner{GitHub: gh}
	var out bytes.Buffer
	if err := r.Run(&out, Options{IssueRef: "acme/widgets#1", Mode: ModeImplement}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out.String(), "implementing a feature") {
		t.Errorf("explicit ModeImplement should win, got:\n%s", out.String())
	}
}

func TestRun_IncludesIssueMetadata(t *testing.T) {
	gh := &fakeGH{get: func(owner, name string, number int) (*github.Issue, error) {
		return &github.Issue{
			Owner: owner, Name: name, Number: number,
			Title: "T", Body: "Body content.",
			URL: "https://example/issues/1", State: "OPEN",
		}, nil
	}}
	r := &Runner{GitHub: gh}
	var out bytes.Buffer
	if err := r.Run(&out, Options{IssueRef: "acme/widgets#1", Mode: ModeImplement, Version: "v0"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := out.String()
	for _, want := range []string{"acme/widgets", "Issue**: #1 — T", "https://example/issues/1", "OPEN", "Body content.", "planwerk-review v0"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q\n%s", want, got)
		}
	}
}

func TestRun_GetIssueErrorPropagates(t *testing.T) {
	gh := &fakeGH{get: func(owner, name string, number int) (*github.Issue, error) { return nil, errors.New("nope") }}
	r := &Runner{GitHub: gh}
	err := r.Run(&bytes.Buffer{}, Options{IssueRef: "acme/widgets#1"})
	if err == nil || !strings.Contains(err.Error(), "fetching issue") {
		t.Fatalf("expected fetching-issue error, got: %v", err)
	}
}

func TestRun_InvalidRefFailsBeforeFetch(t *testing.T) {
	gh := &fakeGH{get: func(owner, name string, number int) (*github.Issue, error) {
		t.Fatal("GetIssue must not be called for invalid ref")
		return nil, nil
	}}
	r := &Runner{GitHub: gh}
	err := r.Run(&bytes.Buffer{}, Options{IssueRef: "garbage"})
	if err == nil || !strings.Contains(err.Error(), "parsing issue ref") {
		t.Fatalf("expected parse error, got: %v", err)
	}
}

func TestInferModeAllAuditPrefixes(t *testing.T) {
	for _, p := range []string{"[BLOCKING]", "[CRITICAL]", "[WARNING]", "[INFO]"} {
		iss := &github.Issue{Title: p + " whatever"}
		if got := inferMode(iss); got != ModeFix {
			t.Errorf("inferMode(%q) = %v, want ModeFix", iss.Title, got)
		}
	}
	if got := inferMode(&github.Issue{Title: "Add new feature"}); got != ModeImplement {
		t.Errorf("inferMode for plain title = %v, want ModeImplement", got)
	}
}
