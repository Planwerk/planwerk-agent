package sync

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func sampleResult() SyncResult {
	return SyncResult{
		WikiRepo:   "acme/widgets",
		WikiCommit: "0123456789abcdef",
		Model:      "claude-opus-4-8",
		Entries: []FlaggedEntry{
			{
				Path:           wikiPatternPath,
				Kind:           KindPattern,
				Classification: ClassStale,
				Reason:         "references internal/db/legacy.go which no longer exists",
				Confidence:     "verified",
			},
			{
				Path:           "memory/old-decision.md",
				Kind:           KindMemory,
				Classification: ClassRedundant,
				Reason:         "duplicates memory/decisions.md",
				SupersededBy:   "memory/decisions.md",
				Confidence:     "likely",
			},
		},
	}
}

func TestRenderMarkdown_HeaderSectionsAndPruneHint(t *testing.T) {
	var w bytes.Buffer
	NewRenderer(&w).RenderMarkdown(sampleResult(), "acme/widgets", "v1.2.3")
	out := w.String()

	for _, want := range []string{
		"# Wiki Sync: acme/widgets",
		"acme/widgets.wiki @ 0123456", // provenance header line
		"## Stale (1)",
		wikiPatternPath,
		"references internal/db/legacy.go which no longer exists",
		"## Redundant (1)",
		"memory/old-decision.md",
		"superseded by `memory/decisions.md`",
		"--prune", // footer points users at the write phase
	} {
		if !strings.Contains(out, want) {
			t.Errorf("markdown missing %q:\n%s", want, out)
		}
	}
}

func TestRenderMarkdown_InSyncWikiSaysSo(t *testing.T) {
	var w bytes.Buffer
	NewRenderer(&w).RenderMarkdown(SyncResult{WikiRepo: "acme/widgets"}, "acme/widgets", "v1")
	out := w.String()
	if !strings.Contains(out, "in sync with the code") {
		t.Errorf("a clean wiki should report being in sync, got:\n%s", out)
	}
	if strings.Contains(out, "## Stale") || strings.Contains(out, "--prune") {
		t.Errorf("a clean wiki should not render sections or a prune hint:\n%s", out)
	}
}

func TestRenderJSON_RoundTrips(t *testing.T) {
	var w bytes.Buffer
	if err := NewRenderer(&w).RenderJSON(sampleResult()); err != nil {
		t.Fatalf("RenderJSON: %v", err)
	}

	var got SyncResult
	if err := json.Unmarshal(w.Bytes(), &got); err != nil {
		t.Fatalf("decoding rendered JSON: %v", err)
	}
	if len(got.Entries) != 2 {
		t.Fatalf("round-trip entries = %d, want 2", len(got.Entries))
	}
	if got.Entries[0].Path != wikiPatternPath || got.Entries[0].Classification != ClassStale {
		t.Errorf("first entry did not round-trip: %+v", got.Entries[0])
	}
	// Provenance and model are threaded per-run, not serialized.
	if got.WikiRepo != "" || got.Model != "" {
		t.Errorf("json:\"-\" fields should not round-trip, got repo=%q model=%q", got.WikiRepo, got.Model)
	}
}

func TestSyncResult_PartitionersAndDeletionPaths(t *testing.T) {
	r := sampleResult()
	// A duplicate path (same entry flagged twice) must collapse to one deletion.
	r.Entries = append(r.Entries, FlaggedEntry{
		Path:           wikiPatternPath,
		Kind:           KindPattern,
		Classification: ClassRedundant,
		Reason:         "also superseded",
	})

	if got := len(r.Stale()); got != 1 {
		t.Errorf("Stale count = %d, want 1", got)
	}
	if got := len(r.Redundant()); got != 2 {
		t.Errorf("Redundant count = %d, want 2", got)
	}
	paths := r.DeletionPaths()
	if len(paths) != 2 {
		t.Fatalf("DeletionPaths = %v, want 2 unique paths", paths)
	}
}
