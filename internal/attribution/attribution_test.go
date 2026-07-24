package attribution

import "testing"

func TestAssistantWith_FallsBackToBareClaudeWithoutModel(t *testing.T) {
	if got := AssistantWith(""); got != "with Claude" {
		t.Errorf("AssistantWith(\"\") = %q, want %q", got, "with Claude")
	}
	// A whitespace-only id also falls back to the bare marker.
	if got := AssistantWith("   "); got != "with Claude" {
		t.Errorf("AssistantWith(whitespace) = %q, want %q", got, "with Claude")
	}
}

func TestAssistantWith_NamesResolvedModel(t *testing.T) {
	if got, want := AssistantWith("claude-opus-5"), "with Claude:claude-opus-5"; got != want {
		t.Errorf("AssistantWith() = %q, want %q", got, want)
	}
}

func TestAssistantWith_TrimsWhitespace(t *testing.T) {
	if got, want := AssistantWith("  claude-fable-5  "), "with Claude:claude-fable-5"; got != want {
		t.Errorf("AssistantWith(padded) = %q, want %q", got, want)
	}
}

// resetVersion clears the package-level build version after a test so the
// process-wide state does not leak between cases.
func resetVersion(t *testing.T) {
	t.Helper()
	t.Cleanup(func() { SetVersion("") })
}

func TestToolWithVersion_AppendsVersionToLink(t *testing.T) {
	if got, want := ToolWithVersion("e1efd0d"), Link+" e1efd0d"; got != want {
		t.Errorf("ToolWithVersion(%q) = %q, want %q", "e1efd0d", got, want)
	}
}

func TestToolWithVersion_FallsBackToBareLinkWithoutVersion(t *testing.T) {
	if got := ToolWithVersion(""); got != Link {
		t.Errorf("ToolWithVersion(\"\") = %q, want the bare link %q", got, Link)
	}
	if got := ToolWithVersion("   "); got != Link {
		t.Errorf("ToolWithVersion(whitespace) = %q, want the bare link %q", got, Link)
	}
}

func TestTool_NamesRecordedVersion(t *testing.T) {
	resetVersion(t)
	SetVersion("e1efd0d")
	if got, want := Tool(), Link+" e1efd0d"; got != want {
		t.Errorf("Tool() = %q, want %q", got, want)
	}
	if got := Version(); got != "e1efd0d" {
		t.Errorf("Version() = %q, want %q", got, "e1efd0d")
	}
}

func TestTool_FallsBackToBareLinkWithoutVersion(t *testing.T) {
	resetVersion(t)
	SetVersion("")
	if got := Tool(); got != Link {
		t.Errorf("Tool() with no version = %q, want the bare link %q", got, Link)
	}
}

func TestSetVersion_TrimsWhitespaceAndClears(t *testing.T) {
	resetVersion(t)
	SetVersion("  e1efd0d  ")
	if got, want := Tool(), Link+" e1efd0d"; got != want {
		t.Errorf("Tool() after padded SetVersion = %q, want %q", got, want)
	}
	// A whitespace-only version clears the record back to the bare link.
	SetVersion("   ")
	if got := Tool(); got != Link {
		t.Errorf("Tool() after clearing = %q, want the bare link %q", got, Link)
	}
}

func TestAssistantMarker_IsPrefixOfNamedClause(t *testing.T) {
	// The detection marker must be a prefix of the rendered clause, so a footer
	// posted under one model is still matched after the default model changes.
	if got := AssistantWith("claude-opus-5"); got[:len(AssistantMarker)] != AssistantMarker {
		t.Errorf("AssistantWith() = %q does not start with AssistantMarker %q", got, AssistantMarker)
	}
}
