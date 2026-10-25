package claude

import (
	"errors"
	"strings"
	"testing"

	"github.com/planwerk/planwerk-agent/internal/report"
)

// sameIntentFixture is the pair the judge tests and the golden share: a
// reference comment about a map write after Unlock, and a finding on one of
// its lines.
func sameIntentFixture() SameIntentPair {
	return SameIntentPair{
		Path: "store.go", From: 40, To: 44,
		Reference: "The map is written after mu.Unlock(), so two goroutines calling Set race on it.",
		Finding: report.Finding{
			Severity: report.SeverityCritical, File: "store.go", Line: 43,
			Title:       "Concurrent map write in Set",
			Problem:     "Set releases the lock before it assigns s.items[key], so concurrent callers write the map unguarded.",
			CodeSnippet: "s.mu.Unlock()\ns.items[key] = v\n",
		},
	}
}

func TestJudgeSameIntent(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		reply string
		want  bool
	}{
		{`{"reason": "Both name the unguarded map write.", "same_issue": true}`, true},
		{"```json\n{\"reason\": \"One is about naming.\", \"same_issue\": false}\n```", false},
	} {
		c, calls := scriptedClient(t, func(int, runSpec, string) (string, string, error) {
			return tt.reply, "", nil
		}, WithStructureModel(repairTierModel), WithStructureEffort(repairTierEffort))
		got, err := c.JudgeSameIntent(sameIntentFixture())
		if err != nil {
			t.Fatalf("JudgeSameIntent(%q): %v", tt.reply, err)
		}
		if got != tt.want {
			t.Errorf("JudgeSameIntent(%q) = %t, want %t", tt.reply, got, tt.want)
		}
		if len(*calls) != 1 {
			t.Fatalf("ran %d sessions, want 1", len(*calls))
		}
		spec := (*calls)[0].spec
		if spec.label != "eval-judge" || !spec.noTools || spec.dir != "" {
			t.Errorf("spec = %+v, want label eval-judge, no tools and no dir", spec)
		}
		if spec.model != repairTierModel || spec.effort != repairTierEffort {
			t.Errorf("tier = %s/%s, want the structure tier %s/%s", spec.model, spec.effort, repairTierModel, repairTierEffort)
		}
		if (*calls)[0].prompt != buildSameIntentPrompt(sameIntentFixture()) {
			t.Error("the session was not given the same-intent prompt")
		}
	}

	t.Run("a session error is returned", func(t *testing.T) {
		t.Parallel()
		errSession := errors.New("claude unavailable")
		c, _ := scriptedClient(t, func(int, runSpec, string) (string, string, error) {
			return "", "", errSession
		})
		if _, err := c.JudgeSameIntent(sameIntentFixture()); !errors.Is(err, errSession) {
			t.Errorf("err = %v, want the session's error", err)
		}
	})

	t.Run("a reply without a same_issue verdict is an error, not a no", func(t *testing.T) {
		t.Parallel()
		for _, reply := range []string{
			`{"reason": "Both name the unguarded map write."}`,
			`{"reason": "Both name the unguarded map write.", "sameIssue": true}`,
			`{"reason": "Both name the unguarded map write.", "same_issue": null}`,
			"null",
		} {
			c, calls := scriptedClient(t, func(int, runSpec, string) (string, string, error) {
				return reply, "", nil
			})
			got, err := c.JudgeSameIntent(sameIntentFixture())
			if err == nil || !strings.Contains(err.Error(), `no "same_issue" verdict`) {
				t.Errorf("JudgeSameIntent(%q) = %t, %v, want an error naming the missing verdict", reply, got, err)
			}
			if len(*calls) != 1 {
				t.Errorf("JudgeSameIntent(%q) ran %d sessions, want 1", reply, len(*calls))
			}
		}
	})

	t.Run("a malformed reply goes through the repair round", func(t *testing.T) {
		t.Parallel()
		c, calls := scriptedClient(t, func(call int, _ runSpec, _ string) (string, string, error) {
			if call == 1 {
				return `{"reason": "Both name the map write.", "same_issue": true`, "", nil
			}
			return `{"reason": "Both name the map write.", "same_issue": true}`, "", nil
		})
		got, err := c.JudgeSameIntent(sameIntentFixture())
		if err != nil || !got {
			t.Fatalf("JudgeSameIntent = %t, %v, want the repaired true", got, err)
		}
		if len(*calls) != 2 || !strings.HasSuffix((*calls)[1].spec.label, "-repair") {
			t.Errorf("sessions = %+v, want the judgement and one repair", *calls)
		}
	})
}
