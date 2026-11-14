package implement

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The skills find the posted plan by hand, following the shared document the
// plugin ships, while mostRecentPlanComment does it in code. This test pins the
// document to the constants the lookup keys on, the way
// internal/github/continuation_doc_test.go pins the continuation markers.
const sharedGitHubDoc = "../../plugins/planwerk/shared/github.md"

func readSharedGitHubDoc(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Clean(sharedGitHubDoc))
	if err != nil {
		t.Fatalf("reading %s: %v", sharedGitHubDoc, err)
	}
	return string(raw)
}

// TestSharedGitHubDocFindsThePlanAsTheCommandDoes fails when the query the
// skills run stops selecting on the heading and the footer marker
// mostRecentPlanComment requires.
func TestSharedGitHubDocFindsThePlanAsTheCommandDoes(t *testing.T) {
	doc := readSharedGitHubDoc(t)
	// The doc's footer text stops before the link's URL.
	footer, _, found := strings.Cut(planCommentMarker, "(")
	if !found {
		t.Fatalf("planCommentMarker %q no longer ends in a Markdown link", planCommentMarker)
	}
	query := `select(.body | contains("` + planHeading + `") and contains("` + footer + `"))] | last | select(. != null) | "\(.createdAt)\n\(.body)"'`
	if !strings.Contains(doc, query) {
		t.Errorf("%s does not read the most recent plan comment with\n%s", sharedGitHubDoc, query)
	}
}
