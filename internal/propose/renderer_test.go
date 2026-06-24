package propose

import (
	"bytes"
	"strings"
	"testing"
)

// TestRenderMarkdown_WikiProvenance verifies the proposal header carries the
// resolved wiki commit when the analysis was grounded against a wiki, and omits
// the line otherwise so wiki-less proposals render unchanged.
func TestRenderMarkdown_WikiProvenance(t *testing.T) {
	t.Run("renders the wiki line when a commit was recorded", func(t *testing.T) {
		result := ProposalResult{
			RepositoryOverview: "overview",
			WikiRepo:           "acme/widgets",
			WikiCommit:         "abc1234def",
		}
		var buf bytes.Buffer
		NewRenderer(&buf).RenderMarkdown(result, "acme/widgets", "v1")
		if !strings.Contains(buf.String(), "> Wiki: acme/widgets.wiki @ abc1234\n") {
			t.Errorf("expected the abbreviated wiki provenance line, got:\n%s", buf.String())
		}
	})

	t.Run("omits the wiki line when no commit was recorded", func(t *testing.T) {
		var buf bytes.Buffer
		NewRenderer(&buf).RenderMarkdown(ProposalResult{RepositoryOverview: "overview"}, "acme/widgets", "v1")
		if strings.Contains(buf.String(), "Wiki:") {
			t.Errorf("wiki-less proposals must not render a Wiki line, got:\n%s", buf.String())
		}
	})
}
