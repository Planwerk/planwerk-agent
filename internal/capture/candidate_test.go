package capture

import (
	"testing"

	"github.com/planwerk/planwerk-review/internal/patterns"
	"github.com/planwerk/planwerk-review/internal/report"
)

func TestCandidateFindings(t *testing.T) {
	catalog := []patterns.Pattern{
		{Name: "Go Error Wrapping"},
		{Name: "Hardcoded secrets"},
	}

	cases := []struct {
		name    string
		pattern string
		keep    bool
	}{
		{"names a catalog pattern is dropped", "Go Error Wrapping", false},
		{"catalog match is case-insensitive", "go error wrapping", false},
		{"adversarial sentinel is kept", "adversarial-review", true},
		{"unknown pattern is kept", "Some Novel Rule", true},
		{"empty pattern is kept", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := []report.Finding{{Title: "x", Pattern: tc.pattern}}
			got := CandidateFindings(in, catalog)
			if tc.keep && (len(got) != 1 || got[0].Pattern != tc.pattern) {
				t.Fatalf("pattern %q: got %+v, want it kept", tc.pattern, got)
			}
			if !tc.keep && len(got) != 0 {
				t.Fatalf("pattern %q: got %+v, want it dropped", tc.pattern, got)
			}
		})
	}
}

// TestCandidateFindings_EmptyInput proves the edge: no findings yields no
// candidates (a nil slice), never a panic, even with a non-empty catalog.
func TestCandidateFindings_EmptyInput(t *testing.T) {
	got := CandidateFindings(nil, []patterns.Pattern{{Name: "Go Error Wrapping"}})
	if len(got) != 0 {
		t.Fatalf("empty input gave %+v, want no candidates", got)
	}
}

// TestCandidateFindings_NoCatalog proves that with no catalog every finding is a
// candidate — there is nothing to dedup against, so even a finding naming a
// would-be pattern survives.
func TestCandidateFindings_NoCatalog(t *testing.T) {
	in := []report.Finding{{Title: "a", Pattern: "Go Error Wrapping"}, {Title: "b", Pattern: ""}}
	got := CandidateFindings(in, nil)
	if len(got) != 2 {
		t.Fatalf("got %d candidates, want 2 — no catalog means nothing is deduped", len(got))
	}
}
