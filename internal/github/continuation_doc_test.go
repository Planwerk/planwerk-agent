package github

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The skills split and merge an issue body by hand, following the shared
// documents the plugin ships, while this package does it in code. The two are
// free to drift; they may not: a marker the skill writes and the merge does
// not recognise is a plan every command reads truncated. These tests pin the
// documents to the constants, the way shared_commits_test.go pins the commit
// doctrine to the trailer block.
const (
	sharedIssueFormatDoc = "../../plugins/planwerk/shared/issue-format.md"
	sharedGitHubDoc      = "../../plugins/planwerk/shared/github.md"
)

func readSharedDoc(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(raw)
}

// TestSharedIssueFormatDocMatchesContinuationContract fails when the writing
// rule the skills follow stops agreeing with SplitIssueBody on the markers,
// the visible lines the merge strips by prefix, the cap, or the part size.
func TestSharedIssueFormatDocMatchesContinuationContract(t *testing.T) {
	doc := readSharedDoc(t, sharedIssueFormatDoc)

	continuedMarker := fmt.Sprintf(continuedMarkerFmt, 1, 0)
	continuedMarker = strings.Replace(continuedMarker, "1/0", "1/N", 1)
	continuationMarker := fmt.Sprintf(continuationMarkerFmt, 0, 0)
	continuationMarker = strings.Replace(continuationMarker, "0/0", "k/N", 1)

	for _, tc := range []struct {
		rule   string
		marker string
	}{
		{"the body's marker", continuedMarker},
		{"a continuation comment's marker", continuationMarker},
		{"the pointer line the merge strips by prefix", continuedPointerPrefix},
		{"the note line the merge strips by prefix", continuationNotePrefix},
		{"GitHub's cap", "65,536"},
		{"the pointer for two parts, as SplitIssueBody renders it", "a comment below (part 2 of 2"},
		{"the pointer for more parts, as SplitIssueBody renders it", "comments below (parts 2"},
		{"the marker counts the body", "`1/2` and `2/2`"},
		{"the footer stays on the body", "Detach the footer"},
		{"a rewrite keeps the comments in step", "A rewrite owns the continuations"},
		{"a comment run is refused on a continued body", "Never post one on an issue whose body\nis itself continued"},
	} {
		if !strings.Contains(doc, tc.marker) {
			t.Errorf("%s: %s does not mention %q", tc.rule, sharedIssueFormatDoc, tc.marker)
		}
	}

	// The pointer SplitIssueBody writes must start with the prefix the doc
	// shows, or a body the command wrote would keep its pointer line after a
	// skill merged it by the doc.
	for _, n := range []int{2, 3} {
		if p := continuedPointer(n, nil); !strings.HasPrefix(p, continuedPointerPrefix) {
			t.Errorf("continuedPointer(%d) does not start with %q: %q", n, continuedPointerPrefix, p)
		}
	}

	// The doc's part size must leave room for the marker lines and a footer
	// within the cap, or a part written by the doc's rule is rejected.
	const docPartSize = 64000
	if !strings.Contains(doc, "at most 64,000 characters") {
		t.Errorf("%s no longer states the part size this test checks (64,000)", sharedIssueFormatDoc)
	}
	footer := "---\n\n_Elaborated by [planwerk-agent](https://github.com/planwerk/planwerk-agent) with Claude:claude-fable-5-1_"
	if limit := MaxIssueBodyLen - continuationReserve - len(footer); docPartSize > limit {
		t.Errorf("the doc's part size %d exceeds the %d SplitIssueBody keeps for a part", docPartSize, limit)
	}
}

// TestSharedGitHubDocMatchesContinuationContract fails when the reading rule
// the skills follow stops agreeing with MergeContinuations on how a continued
// body announces itself and how its parts are found.
func TestSharedGitHubDocMatchesContinuationContract(t *testing.T) {
	doc := readSharedDoc(t, sharedGitHubDoc)

	// Every comment SplitIssueBody writes starts with the continuation marker,
	// which is what the doc's jq filter selects on.
	filter := `startswith("<!-- planwerk-agent:continuation")`
	if !strings.Contains(doc, filter) {
		t.Fatalf("%s does not list the parts with %s", sharedGitHubDoc, filter)
	}
	prefix := strings.TrimSuffix(strings.TrimPrefix(filter, `startswith("`), `")`)
	if !strings.HasPrefix(continuationMarkerFmt, prefix) {
		t.Errorf("the doc's filter %q does not match the marker format %q", prefix, continuationMarkerFmt)
	}
	doc2 := strings.Repeat("## S\n\npara\n\n", 9000) + "---\n\n_Elaborated by x with Claude_\n"
	parts := SplitIssueBody(doc2)
	if len(parts) < 2 {
		t.Fatalf("expected a split, got %d part(s)", len(parts))
	}
	for i, p := range parts[1:] {
		if !strings.HasPrefix(p, prefix) {
			t.Errorf("comment %d does not start with the prefix the doc filters on: %q", i+2, p[:40])
		}
	}

	for _, tc := range []struct {
		rule   string
		marker string
	}{
		{"the body's marker", "<!-- planwerk-agent:continued 1/N -->"},
		{"the parts are read in full", "| .body'"},
		{"the parts are edited by id", `gh api -X PATCH "repos/<owner/repo>/issues/comments/<id>"`},
		{"the parts are deleted by id", `gh api -X DELETE "repos/<owner/repo>/issues/comments/<id>"`},
		{"a missing part aborts", "missing content"},
	} {
		if !strings.Contains(doc, tc.marker) {
			t.Errorf("%s: %s does not mention %q", tc.rule, sharedGitHubDoc, tc.marker)
		}
	}
}
