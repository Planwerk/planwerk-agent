package capture

import (
	"strings"
	"testing"

	"github.com/planwerk/planwerk-review/internal/sync"
)

func TestHasProposals(t *testing.T) {
	cases := []struct {
		name   string
		result CaptureResult
		want   bool
	}{
		{"empty", CaptureResult{}, false},
		{"only patterns", CaptureResult{Patterns: []ProposedPage{{Path: "review_patterns/a.md"}}}, true},
		{"only memory", CaptureResult{Memory: []ProposedPage{{Path: "memory/a.md"}}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.result.HasProposals(); got != tc.want {
				t.Errorf("HasProposals() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestMarkUpdates proves IsUpdate is set only when a proposed page's Path matches
// an enumerated wiki entry: a re-proposed stable slug is an update, a fresh slug
// is new. The match is computed from the entries, never trusted from the model.
func TestMarkUpdates(t *testing.T) {
	entries := []sync.Entry{
		{Path: "memory/decisions.md", Kind: sync.KindMemory},
		{Path: "review_patterns/no-raw-sql.md", Kind: sync.KindPattern},
	}
	result := &CaptureResult{
		Patterns: []ProposedPage{
			{Path: "review_patterns/no-raw-sql.md", Kind: KindPattern},      // existing → update
			{Path: "review_patterns/bounded-retries.md", Kind: KindPattern}, // new
		},
		Memory: []ProposedPage{
			{Path: "memory/decisions.md", Kind: KindMemory},      // existing → update
			{Path: "memory/capture-design.md", Kind: KindMemory}, // new
		},
	}

	MarkUpdates(result, entries)

	if !result.Patterns[0].IsUpdate {
		t.Error("re-proposed existing pattern path should be marked as an update")
	}
	if result.Patterns[1].IsUpdate {
		t.Error("fresh pattern slug should not be marked as an update")
	}
	if !result.Memory[0].IsUpdate {
		t.Error("re-proposed existing memory path should be marked as an update")
	}
	if result.Memory[1].IsUpdate {
		t.Error("fresh memory slug should not be marked as an update")
	}
}

// TestMarkUpdates_NilResult proves the edge: a nil result is a no-op, not a
// panic, so a failed structuring step that returns nil cannot crash the pass.
func TestMarkUpdates_NilResult(t *testing.T) {
	MarkUpdates(nil, []sync.Entry{{Path: "memory/a.md"}})
}

// TestAllPages_PatternsThenMemory proves AllPages flattens the result in a
// stable order — patterns first, then memory — so the write phase pushes a
// deterministic page list. The empty case returns an empty slice, not nil-panic.
func TestAllPages_PatternsThenMemory(t *testing.T) {
	result := CaptureResult{
		Patterns: []ProposedPage{
			{Path: "review_patterns/a.md", Kind: KindPattern},
			{Path: "review_patterns/b.md", Kind: KindPattern},
		},
		Memory: []ProposedPage{
			{Path: "memory/c.md", Kind: KindMemory},
		},
	}
	pages := result.AllPages()
	gotPaths := make([]string, len(pages))
	for i, p := range pages {
		gotPaths[i] = p.Path
	}
	want := []string{"review_patterns/a.md", "review_patterns/b.md", "memory/c.md"}
	if strings.Join(gotPaths, ",") != strings.Join(want, ",") {
		t.Errorf("AllPages order = %v, want %v", gotPaths, want)
	}

	if got := (CaptureResult{}).AllPages(); len(got) != 0 {
		t.Errorf("AllPages of an empty result = %v, want empty", got)
	}
}
