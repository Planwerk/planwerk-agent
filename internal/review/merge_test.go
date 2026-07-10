package review

import (
	"testing"

	"github.com/planwerk/planwerk-agent/internal/report"
)

func TestAppendSummaryNote(t *testing.T) {
	r := &report.ReviewResult{Summary: "Clean review"}
	appendSummaryNote(r, "includes adversarial review pass")
	if r.Summary != "Clean review (includes adversarial review pass)" {
		t.Errorf("got %q", r.Summary)
	}
	// Empty summary stays empty.
	empty := &report.ReviewResult{}
	appendSummaryNote(empty, "note")
	if empty.Summary != "" {
		t.Errorf("empty summary must stay empty, got %q", empty.Summary)
	}
}
