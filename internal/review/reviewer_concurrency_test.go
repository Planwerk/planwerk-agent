package review

import (
	"bytes"
	"strings"
	"sync"
	"testing"

	"github.com/planwerk/planwerk-agent/internal/claude"
	"github.com/planwerk/planwerk-agent/internal/github"
	"github.com/planwerk/planwerk-agent/internal/report"
)

// TestRunner_ConcurrentRunsDoNotLeakModel runs two Runners with distinct
// resolved models concurrently and asserts each rendered report names its own
// model in the attribution footer — never the other's. Before the model was
// threaded per-run on the result, it lived in a package-level global that two
// concurrent runs would overwrite, leaking one run's model into the other's
// footer. Run under -race to also catch any reintroduced shared state.
func TestRunner_ConcurrentRunsDoNotLeakModel(t *testing.T) {
	makeRunner := func(model string) *Runner {
		pr := fakePR(t, "acme", "widgets", 1, "sha-"+model)
		cl := &configurableClaude{
			// Mirror what claude.Client.Review does: stamp the result with the
			// model the session resolved, so the renderer threads it into the
			// footer.
			review: func(_ string, _ claude.ReviewContext) (*report.ReviewResult, error) {
				return &report.ReviewResult{Summary: "ok", Model: model}, nil
			},
		}
		gh := &mockGitHub{
			fetchAndCheckout: func(string) (*github.PR, error) { return pr, nil },
		}
		return &Runner{Claude: cl, GitHub: gh}
	}

	const modelA, modelB = "claude-opus-5", "claude-fable-5"
	rA, rB := makeRunner(modelA), makeRunner(modelB)

	optsA := baseOpts()
	optsA.NoCache = true
	optsB := baseOpts()
	optsB.NoCache = true

	var (
		outA, outB bytes.Buffer
		errA, errB error
		wg         sync.WaitGroup
	)
	wg.Add(2)
	go func() { defer wg.Done(); errA = rA.Run(&outA, optsA) }()
	go func() { defer wg.Done(); errB = rB.Run(&outB, optsB) }()
	wg.Wait()

	if errA != nil {
		t.Fatalf("runner A Run returned error: %v", errA)
	}
	if errB != nil {
		t.Fatalf("runner B Run returned error: %v", errB)
	}

	gotA, gotB := outA.String(), outB.String()
	if !strings.Contains(gotA, "with Claude:"+modelA) {
		t.Errorf("runner A footer missing its own model %q:\n%s", modelA, gotA)
	}
	if strings.Contains(gotA, modelB) {
		t.Errorf("runner A footer leaked runner B's model %q:\n%s", modelB, gotA)
	}
	if !strings.Contains(gotB, "with Claude:"+modelB) {
		t.Errorf("runner B footer missing its own model %q:\n%s", modelB, gotB)
	}
	if strings.Contains(gotB, modelA) {
		t.Errorf("runner B footer leaked runner A's model %q:\n%s", modelA, gotB)
	}
}
