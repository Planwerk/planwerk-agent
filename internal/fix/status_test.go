package fix

import "testing"

func TestParseStatus(t *testing.T) {
	cases := map[string]Status{
		"## Fix Report\n\nSTATUS: DONE\n":        StatusDone,
		"blah\n**STATUS:** BLOCKED\nmore":        StatusBlocked,
		"- status: needs_context":                StatusNeedsContext,
		"### Status\nSTATUS: DONE_WITH_CONCERNS": StatusDoneWithConcerns,
		"no status line here":                    StatusUnknown,
		"STATUS: something-unrecognized":         StatusUnknown,
		">  STATUS:   done  ":                    StatusDone,
	}
	for report, want := range cases {
		if got := parseStatus(report); got != want {
			t.Errorf("parseStatus(%q) = %q, want %q", report, got, want)
		}
	}
}

func TestStatusShouldEscalate(t *testing.T) {
	escalate := []Status{StatusBlocked, StatusNeedsContext}
	for _, s := range escalate {
		if !s.ShouldEscalate() {
			t.Errorf("%s should escalate", s)
		}
	}
	noEscalate := []Status{StatusDone, StatusDoneWithConcerns, StatusUnknown}
	for _, s := range noEscalate {
		if s.ShouldEscalate() {
			t.Errorf("%s should not escalate", s)
		}
	}
}
