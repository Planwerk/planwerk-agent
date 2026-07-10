package claude

import (
	"errors"
	"sort"
	"sync"
	"testing"

	"github.com/planwerk/planwerk-agent/internal/report"
)

// recordingFanOut runs runSpecialistFanOut with a call func that records which
// specialists were invoked (concurrency-safe) and returns a per-key result or
// error. failKeys errors those specialists; every other invoked specialist
// returns a distinct non-nil result.
func recordingFanOut(t *testing.T, changedFiles []string, failKeys map[string]bool) (called map[string]bool, results []specResultKV) {
	t.Helper()
	var mu sync.Mutex
	called = map[string]bool{}
	out := runSpecialistFanOut(changedFiles, func(sp Specialist) (*report.ReviewResult, error) {
		mu.Lock()
		called[sp.Key] = true
		mu.Unlock()
		if failKeys[sp.Key] {
			return nil, errors.New("boom")
		}
		return &report.ReviewResult{Summary: sp.Key}, nil
	})
	for _, r := range out {
		results = append(results, specResultKV{key: r.Key, summary: r.Result.Summary})
	}
	return called, results
}

type specResultKV struct {
	key     string
	summary string
}

func TestRunSpecialistFanOut_EmptyChangedFilesRunsAll(t *testing.T) {
	// An empty changed-file set means the gate fails open: every specialist runs.
	called, results := recordingFanOut(t, nil, nil)
	if len(called) != len(Specialists) {
		t.Errorf("called %d specialists, want all %d", len(called), len(Specialists))
	}
	if len(results) != len(Specialists) {
		t.Errorf("returned %d results, want %d", len(results), len(Specialists))
	}
	// Result keys and their order align with the registry.
	for i, r := range results {
		if r.key != Specialists[i].Key {
			t.Errorf("result[%d].key = %q, want %q (registry order)", i, r.key, Specialists[i].Key)
		}
		if r.summary != Specialists[i].Key {
			t.Errorf("result[%d] carries summary %q, want its own key %q", i, r.summary, Specialists[i].Key)
		}
	}
}

func TestRunSpecialistFanOut_GatesOutDocsOnly(t *testing.T) {
	// A docs-only diff gates out every RelevanceAnySource / RelevanceRoutes
	// specialist; only the NeverGate ones (security, data-migration) run.
	called, results := recordingFanOut(t, []string{"README.md", "docs/guide.md"}, nil)

	for _, sp := range Specialists {
		want := sp.NeverGate
		if called[sp.Key] != want {
			t.Errorf("specialist %q called=%v, want %v (NeverGate=%v)", sp.Key, called[sp.Key], want, sp.NeverGate)
		}
	}
	gotKeys := resultKeys(results)
	if len(gotKeys) != 2 {
		t.Fatalf("returned %d results, want 2 (the NeverGate specialists): %v", len(gotKeys), gotKeys)
	}
}

func TestRunSpecialistFanOut_FailedSpecialistDropped(t *testing.T) {
	// One specialist errors; it is dropped without sinking the rest.
	called, results := recordingFanOut(t, nil, map[string]bool{"security": true})
	if !called["security"] {
		t.Error("the failing specialist should still have been invoked")
	}
	gotKeys := resultKeys(results)
	for _, k := range gotKeys {
		if k == "security" {
			t.Error("a failed specialist must not appear in the results")
		}
	}
	if len(gotKeys) != len(Specialists)-1 {
		t.Errorf("returned %d results, want %d (all but the failed one)", len(gotKeys), len(Specialists)-1)
	}
}

func resultKeys(results []specResultKV) []string {
	var keys []string
	for _, r := range results {
		keys = append(keys, r.key)
	}
	sort.Strings(keys)
	return keys
}
