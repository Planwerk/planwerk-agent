package claude

import (
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/planwerk/planwerk-agent/internal/patterns"
	"github.com/planwerk/planwerk-agent/internal/planwerk"
)

// sentPrompt is one session a Client method started: the label it ran under
// and the prompt it was sent.
type sentPrompt struct {
	label  string
	prompt string
}

// recordingClient returns a Client whose sessions record their label and
// prompt and then fail, so no decode or repair session follows.
func recordingClient() (*Client, func() []sentPrompt) {
	var (
		mu   sync.Mutex
		sent []sentPrompt
	)
	c := &Client{sessionFn: func(spec runSpec, prompt string) (string, string, error) {
		mu.Lock()
		defer mu.Unlock()
		sent = append(sent, sentPrompt{label: spec.label, prompt: prompt})
		return "", "", errors.New("recorded")
	}}
	return c, func() []sentPrompt {
		mu.Lock()
		defer mu.Unlock()
		return append([]sentPrompt(nil), sent...)
	}
}

// TestReviewPassPrompts_MatchTheSessions locks that review --dry-run sizes the
// prompts the sessions are sent: each Client method of a review pass, run
// through sessionFn, sends exactly the label and prompt ReviewPassPrompts
// returns for it, in dispatch order, for a review run as one session and for
// a partitioned one (decision 143).
func TestReviewPassPrompts_MatchTheSessions(t *testing.T) {
	t.Parallel()
	review := ReviewContext{
		Patterns:   goldenPatterns(),
		BaseBranch: "develop",
		PRTitle:    "Add the widget cache",
		PRBody:     "Caches widgets for a minute.",
		Diff:       goldenDiff(),
	}
	partition := func(i int) ReviewContext {
		c := review
		c.Partition = PartitionScope{Index: i, Count: 2, Files: []string{"a.go"}, Others: []string{"b.go"}}
		if i == 2 {
			c.Partition.Files, c.Partition.Others = c.Partition.Others, c.Partition.Files
		}
		return c
	}
	feature := &planwerk.Feature{FeatureID: "CC-0042", Title: "Widget cache"}

	for _, tc := range []struct {
		name    string
		reviews []ReviewContext
	}{
		{"one review session", []ReviewContext{review}},
		{"partitioned", []ReviewContext{partition(1), partition(2)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c, sent := recordingClient()
			// A dir of "" writes no diff, so the passes that write their own
			// read the zero Diff.
			for _, rctx := range tc.reviews {
				_, _ = c.Review("", rctx)
			}
			_, _ = c.AdversarialReview("", "develop", "", goldenPatterns(), 0)
			_, _ = c.CoverageMap("", "develop")
			_, _ = c.FeatureCompliance("", "develop", feature)
			for _, sp := range Specialists {
				_, _ = c.SpecialistReview("", "develop", sp, goldenPatterns(), 0)
			}

			got := ReviewPassPrompts(ReviewPasses{
				Reviews:     tc.reviews,
				BaseBranch:  "develop",
				Patterns:    goldenPatterns(),
				Adversarial: true,
				Coverage:    true,
				Feature:     feature,
				Specialists: Specialists,
			})
			want := sent()
			if len(got) != len(want) {
				t.Fatalf("ReviewPassPrompts returned %d prompts, the sessions were sent %d", len(got), len(want))
			}
			for i, w := range want {
				if got[i].Label != w.label {
					t.Errorf("prompt %d: label %q, want the session's %q", i, got[i].Label, w.label)
				}
				if got[i].Prompt != w.prompt {
					t.Errorf("prompt %d (%s): the prompt differs from what the session was sent", i, w.label)
				}
			}
		})
	}
}

// TestReviewPassPrompts_CountsThePatternsEachPromptCarries locks the per-pass
// pattern counts the dry run prints: the bodies each prompt carries after
// area scoping, the --scope-patterns split and --max-patterns, checked
// against the bodies found in the prompt itself (decision 143).
func TestReviewPassPrompts_CountsThePatternsEachPromptCarries(t *testing.T) {
	t.Parallel()
	pats := goldenPatterns()
	bodies := func(prompt string) int {
		n := 0
		for _, body := range []string{"Secrets MUST be loaded", "Long-running functions MUST accept"} {
			if strings.Contains(prompt, body) {
				n++
			}
		}
		return n
	}
	scoped := ReviewContext{Patterns: pats, Scope: &patterns.FileScope{Inline: pats[:1], Rest: pats[1:]}, Catalog: goldenCatalog()}
	unwritten := ReviewContext{Patterns: pats, Scope: &patterns.FileScope{Inline: pats[:1], Rest: pats[1:]}}
	tester := Specialists[2]
	if tester.Key != "testing" {
		t.Fatalf("Specialists[2] is %q, want testing", tester.Key)
	}

	for _, tc := range []struct {
		max  int
		want map[string]int
	}{
		{0, map[string]int{"review": 2, "scoped": 1, "unwritten": 2, "adversarial": 2, "coverage": 0, "compliance": 0, "specialist-security": 1, "specialist-data-migration": 1, "specialist-testing": 2}},
		{1, map[string]int{"review": 1, "scoped": 1, "unwritten": 1, "adversarial": 1, "coverage": 0, "compliance": 0, "specialist-security": 1, "specialist-data-migration": 1, "specialist-testing": 1}},
	} {
		review, sc, uw := ReviewContext{Patterns: pats, MaxPatterns: tc.max}, scoped, unwritten
		sc.MaxPatterns, uw.MaxPatterns = tc.max, tc.max
		got := ReviewPassPrompts(ReviewPasses{
			Reviews:     []ReviewContext{review, sc, uw},
			Patterns:    pats,
			MaxPatterns: tc.max,
			Adversarial: true,
			Coverage:    true,
			Feature:     &planwerk.Feature{FeatureID: "CC-0042"},
			// security and data-migration match one area each; testing
			// matches none and falls back to the whole catalog.
			Specialists: []Specialist{Specialists[0], Specialists[1], tester},
		})
		counts := make(map[string]int, len(got))
		for i, pp := range got {
			name := pp.Label
			switch i {
			case 1:
				name = "scoped"
			case 2:
				name = "unwritten"
			}
			counts[name] = pp.Patterns
			if n := bodies(pp.Prompt); n != pp.Patterns {
				t.Errorf("max %d, %s: Patterns = %d, but the prompt carries %d bodies", tc.max, name, pp.Patterns, n)
			}
		}
		if !reflect.DeepEqual(counts, tc.want) {
			t.Errorf("max %d: pattern counts = %v, want %v", tc.max, counts, tc.want)
		}
	}
}
