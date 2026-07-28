package claude

import (
	"os"
	"strings"
	"testing"

	"github.com/planwerk/planwerk-agent/internal/github"
)

// sharedCrossRepoDoc is the skill-side document carrying the cross-repository
// reference rule. The Go prompt builders restate it for the headless commands,
// so the two are free to drift. They may not: a bare "#N" in an elaborated body
// resolves against whichever repository the body is written to, so a rule that
// holds on one surface and not the other produces cross-references pointing at
// unrelated issues. This is the commits.md / commitTrailerBlock arrangement
// applied to cross-repo references.
const sharedCrossRepoDoc = "../../plugins/planwerk/shared/github.md"

// crossRepoMarkers are the load-bearing phrases of the rule, in the form both
// surfaces must carry. Kept short so an unrelated rewording does not fail the
// test while a dropped rule does.
var crossRepoMarkers = []string{
	"owner/repo#",
	"bare `#",
	"resolves against",
}

// TestSharedCrossRepoDocMatchesRelationsBlock pins the cross-repository
// reference rule across the shared github.md and the prompt block in
// relations.go, which is what tells a headless elaborate or plan session how to
// cite a Sub Issue outside the repository it is planning.
func TestSharedCrossRepoDocMatchesRelationsBlock(t *testing.T) {
	raw, err := os.ReadFile(sharedCrossRepoDoc)
	if err != nil {
		t.Fatalf("reading %s: %v", sharedCrossRepoDoc, err)
	}
	doc := flow(string(raw))

	var sb strings.Builder
	meta := &github.Issue{Owner: "acme", Name: "widgets", Number: 40, Title: "Meta", State: "open"}
	siblings := []github.Issue{{Owner: "acme", Name: "widgets", Number: 43, Title: "Sibling", State: "open"}}
	renderIssueRelations(&sb, testRepoFullName, meta, siblings, nil)
	prompt := flow(sb.String())

	for _, marker := range crossRepoMarkers {
		m := flow(marker)
		if !strings.Contains(doc, m) {
			t.Errorf("%s no longer carries %q; the skill and prompt surfaces have drifted", sharedCrossRepoDoc, marker)
		}
		if !strings.Contains(prompt, m) {
			t.Errorf("the relations prompt block no longer carries %q; the skill and prompt surfaces have drifted", marker)
		}
	}
}
