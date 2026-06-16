package address

import (
	"bytes"
	"strings"
	"testing"

	"github.com/planwerk/planwerk-review/internal/github"
)

func selectorThreads() []github.ReviewThread {
	return []github.ReviewThread{
		{ID: threadID1, Path: "a.go", Line: 10, Comments: []github.ReviewThreadComment{{Author: "rev", Body: "rename this\nwith more detail"}}},
		{ID: threadID2, Path: "b.go", Line: 20, Comments: []github.ReviewThreadComment{{Author: "rev", Body: "add a guard"}}},
		{ID: threadID3, Path: "c.go", Line: 30, Comments: []github.ReviewThreadComment{{Author: "rev", Body: "fix the typo"}}},
	}
}

func TestRunInteractiveThreadSelection_SelectsAndSkips(t *testing.T) {
	var buf bytes.Buffer
	// Address RT_1, skip RT_2, address RT_3.
	in := strings.NewReader("y\nn\ny\n")
	got, err := RunInteractiveThreadSelection(&buf, in, selectorThreads())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 || got[0].ID != threadID1 || got[1].ID != threadID3 {
		t.Errorf("selected %v, want [RT_1 RT_3]", ids(got))
	}
	// The one-line excerpt should be shown, not the multi-line body.
	if !strings.Contains(buf.String(), "rename this") {
		t.Errorf("missing thread excerpt in output: %s", buf.String())
	}
	if !strings.Contains(buf.String(), "a.go:10") {
		t.Errorf("missing thread location in output: %s", buf.String())
	}
}

func TestRunInteractiveThreadSelection_QuitMidway(t *testing.T) {
	var buf bytes.Buffer
	// Address RT_1, then quit — RT_3 must never be offered.
	in := strings.NewReader("y\nq\n")
	got, err := RunInteractiveThreadSelection(&buf, in, selectorThreads())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].ID != threadID1 {
		t.Errorf("selected %v, want [RT_1]", ids(got))
	}
	if strings.Contains(buf.String(), "Thread 3/3") {
		t.Errorf("third thread should not have been offered after quit: %s", buf.String())
	}
}

func TestRunInteractiveThreadSelection_EOFFinishesWithSelection(t *testing.T) {
	var buf bytes.Buffer
	// Stream ends after the first answer (no trailing newline on EOF): the
	// selector must finish with what was chosen rather than erroring.
	in := strings.NewReader("y\n")
	got, err := RunInteractiveThreadSelection(&buf, in, selectorThreads())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].ID != threadID1 {
		t.Errorf("selected %v, want [RT_1] on early EOF", ids(got))
	}
}

func ids(threads []github.ReviewThread) []string {
	out := make([]string, len(threads))
	for i, t := range threads {
		out[i] = t.ID
	}
	return out
}
