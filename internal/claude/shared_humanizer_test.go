package claude

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The de-AI writing rules live on three surfaces — the full catalog the
// humanize skill reads (shared/humanizer.md), the compact "Signs of AI
// writing" section every artifact-writing skill honors (house-style.md), and
// the prompt blocks in this package the unattended sessions honor
// (aiWritingTellsBullets, bannedVocabularyLine) — so the three are free to
// drift. They may not: prose that one surface bans and another produces is the
// same defect whichever session wrote it. This is the commits.md /
// commitTrailerBlock arrangement applied to prose: duplication bounded
// mechanically rather than by discipline. Decision #80.
const (
	sharedHumanizerDoc  = "../../plugins/planwerk/shared/humanizer.md"
	sharedHouseStyleDoc = "../../plugins/planwerk/shared/house-style.md"
)

// TestSharedHumanizerDocMatchesPromptBlocks fails when the three surfaces stop
// agreeing on a writing tell. Each marker is a distinct rule, so a failure
// names the rule that went missing rather than reporting that two blobs
// differ.
func TestSharedHumanizerDocMatchesPromptBlocks(t *testing.T) {
	surfaces := map[string]string{
		sharedHumanizerDoc:  readSharedDoc(t, sharedHumanizerDoc),
		sharedHouseStyleDoc: readSharedDoc(t, sharedHouseStyleDoc),
		"docProseBlock()":   docProseBlock(),
	}

	for _, tc := range []struct {
		rule   string
		marker string
	}{
		{"copula avoidance is banned", `"serves as"`},
		{"significance inflation is banned", "pivotal moment"},
		{"fake-depth participle clauses are banned", `"-ing" clauses`},
		{"negative parallelisms are banned", "not only"},
		{"filler phrases are banned", "it is important to note"},
		{"generic upbeat conclusions are banned", "concrete fact"},
		{"em dashes are avoided in prose", "em dash"},
	} {
		for name, text := range surfaces {
			if !strings.Contains(flow(text), flow(tc.marker)) {
				t.Errorf("%s: %s does not mention %q", tc.rule, name, tc.marker)
			}
		}
	}
}

// TestSharedHumanizerDocMatchesBannedVocabulary fails when the vocabulary ban
// diverges between the shared docs and bannedVocabularyLine(). The words are a
// sample, not the list: one marker per source list that was merged (the
// original gstack/econ set and the humanizer additions), plus the two
// qualified entries whose qualifier keeps the ban from over-triggering.
func TestSharedHumanizerDocMatchesBannedVocabulary(t *testing.T) {
	surfaces := map[string]string{
		sharedHumanizerDoc:       readSharedDoc(t, sharedHumanizerDoc),
		sharedHouseStyleDoc:      readSharedDoc(t, sharedHouseStyleDoc),
		"bannedVocabularyLine()": bannedVocabularyLine(),
	}

	for _, marker := range []string{
		"delve",
		"pave the way",
		"tapestry",
		"a testament to",
		"leverage (as a verb)",
		"robust (outside its statistical sense)",
	} {
		for name, text := range surfaces {
			if !strings.Contains(flow(text), flow(marker)) {
				t.Errorf("vocabulary ban: %s does not mention %q", name, marker)
			}
		}
	}
}

// TestSharedHouseStyleMatchesProseStyleBlock fails when the prose rules that
// predate the humanizer catalog diverge between house-style.md and
// proseStyleBlock(). The two carried the same bullets by discipline alone and
// had already drifted once (the throat-clearing example lists) before this
// pinned them.
func TestSharedHouseStyleMatchesProseStyleBlock(t *testing.T) {
	surfaces := map[string]string{
		sharedHouseStyleDoc: readSharedDoc(t, sharedHouseStyleDoc),
		"proseStyleBlock()": proseStyleBlock(),
	}

	for _, tc := range []struct {
		rule   string
		marker string
	}{
		{"lead-first", "State the one core point in the first sentence"},
		{"concreteness yields to accuracy", "subordinate to accuracy"},
		{"unknowns are marked, not invented", "mark it as an assumption"},
		{"filler openers are cut", "throat-clearing"},
		{"the example lists agree", `"It should be noted that"`},
		{"inventories are enumerated, not summarized", "enumerated or it is not summarized"},
		{"the partial-summary examples agree", `"nothing else notable"`},
		{"rhythm varies", "Vary sentence length"},
	} {
		for name, text := range surfaces {
			if !strings.Contains(flow(text), flow(tc.marker)) {
				t.Errorf("%s: %s does not mention %q", tc.rule, name, tc.marker)
			}
		}
	}
}

// flow is prose() plus whitespace collapsing: the markdown docs hard-wrap at
// 80 columns, so a marker like "leverage (as a verb)" can straddle a line
// break there while the Go blocks carry it on one line. A rule is the same
// rule wherever the wrap falls.
func flow(s string) string {
	return strings.Join(strings.Fields(prose(s)), " ")
}

func readSharedDoc(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(raw)
}
