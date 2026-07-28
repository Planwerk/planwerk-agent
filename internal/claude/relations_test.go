package claude

import (
	"strings"
	"testing"

	"github.com/planwerk/planwerk-agent/internal/github"
)

// testRepoFullName is the repository these tests render relations for; a
// related issue outside it must be cited as owner/repo#N.
const testRepoFullName = "acme/widgets"

func TestRenderIssueRelations_StandaloneRendersNothing(t *testing.T) {
	var sb strings.Builder
	renderIssueRelations(&sb, testRepoFullName, nil, nil, nil)
	if sb.Len() != 0 {
		t.Fatalf("renderIssueRelations rendered %q for a standalone issue, want nothing", sb.String())
	}
}

func TestRenderIssueRelations_MetaAndSiblings(t *testing.T) {
	var sb strings.Builder
	meta := &github.Issue{Number: 1, Title: "Meta", Body: "Meta body", State: "open"}
	siblings := []github.Issue{
		{Number: 2, Title: "Open sibling", Body: "open body", State: "open"},
		{Number: 3, Title: "Closed sibling", Body: "closed body", State: "closed"},
	}
	renderIssueRelations(&sb, testRepoFullName, meta, siblings, nil)
	got := sb.String()

	for _, want := range []string{
		"## Meta / Sub-Issue Context",
		"Sub Issue** of a Meta Issue",
		"<meta-issue number=1 state=open>",
		"**Meta Issue #1**: Meta",
		"Meta body",
		"<sibling-sub-issues>",
		"<sibling number=2 state=open>",
		"<sibling number=3 state=closed>",
		"the remaining X is handled by #K",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("rendered section missing %q\n---\n%s", want, got)
		}
	}
}

func TestRenderIssueRelations_NoSiblingsNote(t *testing.T) {
	var sb strings.Builder
	meta := &github.Issue{Number: 1, Title: "Meta", Body: "Meta body", State: "open"}
	renderIssueRelations(&sb, testRepoFullName, meta, nil, nil)
	if !strings.Contains(sb.String(), "no siblings yet") {
		t.Errorf("expected the no-siblings note for a lone Sub Issue\n%s", sb.String())
	}
}

func TestRenderIssueRelations_ChildrenWhenIssueIsItselfMeta(t *testing.T) {
	var sb strings.Builder
	children := []github.Issue{
		{Number: 2, Title: "Child one", Body: "child body", State: "open"},
	}
	renderIssueRelations(&sb, testRepoFullName, nil, nil, children)
	got := sb.String()
	for _, want := range []string{
		"## Meta / Sub-Issue Context",
		"itself a **Meta Issue**",
		"<child-sub-issues>",
		"<sub-issue number=2 state=open>",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("rendered children section missing %q\n---\n%s", want, got)
		}
	}
}

func TestRenderIssueRelations_LinkedPRsRendered(t *testing.T) {
	var sb strings.Builder
	meta := &github.Issue{Number: 1, Title: "Meta", Body: "Meta body", State: "open"}
	siblings := []github.Issue{
		{Number: 2, Title: "Sibling with PRs", Body: "body", State: "open", LinkedPRs: []github.LinkedPR{
			{Number: 20, Title: "Implement it", URL: "https://example.com/pull/20", State: "open"},
			{Number: 21, Title: "WIP", URL: "https://example.com/pull/21", State: "open", IsDraft: true},
		}},
		{Number: 3, Title: "Sibling without PRs", Body: "body", State: "open"},
	}
	renderIssueRelations(&sb, testRepoFullName, meta, siblings, nil)
	got := sb.String()

	for _, want := range []string{
		"- PR #20 (open): Implement it — https://example.com/pull/20",
		"- PR #21 (draft): WIP — https://example.com/pull/21",
		"open pull request",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("rendered section missing %q\n---\n%s", want, got)
		}
	}

	// The sibling without PRs must not emit a <linked-prs> block. Match the
	// opening tag as its own line ("<linked-prs>\n") to distinguish a real block
	// from the guidance prose, which mentions `<linked-prs>` inline.
	if n := strings.Count(got, "<linked-prs>\n"); n != 1 {
		t.Errorf("got %d <linked-prs> blocks, want 1 (only the sibling with PRs)\n---\n%s", n, got)
	}
}

func TestRenderIssueRelations_NoLinkedPRsEmitsNoBlock(t *testing.T) {
	var sb strings.Builder
	meta := &github.Issue{Number: 1, Title: "Meta", Body: "Meta body", State: "open"}
	siblings := []github.Issue{{Number: 2, Title: "Sibling", Body: "body", State: "open"}}
	renderIssueRelations(&sb, testRepoFullName, meta, siblings, nil)
	// Match the opening tag as its own line so the guidance prose's inline
	// mention of `<linked-prs>` does not register as a rendered block.
	if strings.Contains(sb.String(), "<linked-prs>\n") {
		t.Errorf("rendered a <linked-prs> block for a Sub Issue with no PRs\n%s", sb.String())
	}
}

func TestRenderIssueRelations_EmptyStateFallsBackToUnknown(t *testing.T) {
	var sb strings.Builder
	meta := &github.Issue{Number: 1, Title: "Meta", Body: "", State: ""}
	renderIssueRelations(&sb, testRepoFullName, meta, nil, nil)
	if !strings.Contains(sb.String(), "<meta-issue number=1 state=unknown>") {
		t.Errorf("expected state=unknown fallback\n%s", sb.String())
	}
}

// A sibling or child in another repository must be labelled with its full
// owner/repo#N. In GitHub markdown a bare "#N" resolves against the repository
// the elaborated body is written to, so an unqualified cross-repo reference
// would link to whatever issue happens to carry that number there.
func TestRenderIssueRelations_QualifiesForeignSubIssues(t *testing.T) {
	var sb strings.Builder
	meta := &github.Issue{Owner: "acme", Name: "widgets", Number: 40, Title: "Meta", State: "open"}
	siblings := []github.Issue{
		{Owner: "acme", Name: "widgets", Number: 43, Title: "Server side", State: "open"},
		{Owner: "acme", Name: "gadgets", Number: 58, Title: "Client counterpart", State: "open"},
	}
	renderIssueRelations(&sb, testRepoFullName, meta, siblings, nil)
	got := sb.String()

	if !strings.Contains(got, "**acme/gadgets#58**") {
		t.Errorf("foreign sibling is not qualified with its repository:\n%s", got)
	}
	if !strings.Contains(got, "**#43**") {
		t.Errorf("same-repo sibling should stay a bare #43:\n%s", got)
	}
	if strings.Contains(got, "**#58**") {
		t.Errorf("foreign sibling rendered as a bare #58, which resolves to the wrong repository:\n%s", got)
	}
}

// The repository is not always known to the prompt builder. With no repository
// to compare against, every issue renders bare — the pre-existing shape.
func TestRenderIssueRelations_UnknownRepoRendersBareNumbers(t *testing.T) {
	var sb strings.Builder
	meta := &github.Issue{Number: 40, Title: "Meta", State: "open"}
	siblings := []github.Issue{{Number: 43, Title: "Sibling", State: "open"}}
	renderIssueRelations(&sb, "", meta, siblings, nil)
	if got := sb.String(); !strings.Contains(got, "**#43**") {
		t.Errorf("want a bare #43 when the repository is unknown:\n%s", got)
	}
}
