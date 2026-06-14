package draft

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestBuildIssueBody(t *testing.T) {
	body := BuildIssueBody(&Result{
		Title:       "Add a dark mode toggle",
		Description: "Users want a dark theme.",
		Motivation:  "Reduces eye strain at night.",
		Scope:       "Small",
	})

	if !strings.HasPrefix(body, "**Category**: feature | **Scope**: Small\n\n") {
		t.Errorf("body missing the Category/Scope header line:\n%s", body)
	}
	for _, want := range []string{
		"## Description\n\nUsers want a dark theme.",
		"## Motivation\n\nReduces eye strain at night.",
		"_Drafted by [planwerk-review]",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q:\n%s", want, body)
		}
	}
	// draft describes, it does not plan: no elaboration sections.
	for _, absent := range []string{"Affected Areas", "Acceptance Criteria", "Non-Goals"} {
		if strings.Contains(body, absent) {
			t.Errorf("body must not contain the elaboration section %q:\n%s", absent, body)
		}
	}
}

func TestBuildIssueBody_DefaultsScope(t *testing.T) {
	body := BuildIssueBody(&Result{Title: "T", Description: "d", Motivation: "m"})
	if !strings.Contains(body, "**Scope**: Medium") {
		t.Errorf("blank scope should default to Medium:\n%s", body)
	}
}

func TestRenderJSON_RoundTrips(t *testing.T) {
	in := Result{Title: "T", Description: "d", Motivation: "m", Scope: "Large", Body: "body"}
	var buf bytes.Buffer
	if err := NewRenderer(&buf).RenderJSON(in); err != nil {
		t.Fatalf("RenderJSON: %v", err)
	}
	var out Result
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("round-trip unmarshal: %v", err)
	}
	if out != in {
		t.Errorf("round-trip mismatch: got %+v, want %+v", out, in)
	}
}
