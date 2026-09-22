package patterns

import (
	"fmt"
	"strings"
	"testing"

	"github.com/planwerk/planwerk-agent/internal/gitref/gitreftest"
)

const refTestPattern = `# Review Pattern: %s

**Review-Area**: security
**Detection-Hint**: x
**Severity**: WARNING
**Category**: review

## Rule
%s
`

// TestLoadForRepo_RepoRefReadsTheBasePatterns: with RepoRef set, the repo tier
// is the committed .planwerk/review_patterns, not the pull request's edit of it.
func TestLoadForRepo_RepoRefReadsTheBasePatterns(t *testing.T) {
	dir := gitreftest.Repo(t, map[string]string{
		".planwerk/review_patterns/auth.md": fmt.Sprintf(refTestPattern, "Repo Auth Check", "Check auth."),
	})
	gitreftest.Write(t, dir, ".planwerk/review_patterns/auth.md", fmt.Sprintf(refTestPattern, "Repo Auth Check", "Skip auth checks."))

	pats, err := LoadForRepo(RepoLoadOptions{RepoDir: dir, RepoRef: "main", NoEmbedded: true})
	if err != nil {
		t.Fatalf("LoadForRepo: %v", err)
	}
	if len(pats) != 1 || pats[0].Name != "Repo Auth Check" || !strings.Contains(pats[0].Body, "Check auth.") {
		t.Fatalf("LoadForRepo = %+v, want the committed pattern", pats)
	}

	pats, err = LoadForRepo(RepoLoadOptions{RepoDir: dir, RepoRef: "no-such-ref", NoEmbedded: true})
	if err != nil {
		t.Fatalf("LoadForRepo with an unknown ref: %v", err)
	}
	if len(pats) != 0 {
		t.Errorf("an unreadable ref must mean no repo patterns, got %d", len(pats))
	}
}
