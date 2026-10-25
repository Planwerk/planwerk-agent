package mdfence

import (
	"strings"
	"testing"
)

func TestTicks(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want int
	}{
		{"empty input", "", 3},
		{"no backtick", "plain", 3},
		{"run of one", "a`b", 3},
		{"run of two", "a" + strings.Repeat("`", 2) + "b", 3},
		{"run of three", "x := ```y```", 4},
		{"run of five", strings.Repeat("`", 5), 6},
		{"longest run wins, not the sum", "a``b````c`d", 5},
		{"multibyte text around the run", "ä```ö", 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got, want := Ticks(tc.in), strings.Repeat("`", tc.want); got != want {
				t.Errorf("Ticks(%q) = %q, want %q", tc.in, got, want)
			}
		})
	}
}

func TestSpan(t *testing.T) {
	for _, tc := range []struct {
		name, in, want string
	}{
		{"plain text", "go.sum", "`go.sum`"},
		{"run of one inside", "a`b", "``a`b``"},
		{"longest run wins", "a``b`c", "```a``b`c```"},
		{"leading backtick is padded", "`x", "`` `x ``"},
		{"trailing backtick is padded", "x`", "`` x` ``"},
		{"surrounding spaces survive the strip", " a ", "`  a  `"},
		{"leading space is padded", " notes.md", "`  notes.md `"},
		{"all spaces stay as they are", "  ", "`  `"},
		{"comment marker stays inside the span", "<!-- x -->", "`<!-- x -->`"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Span(tc.in); got != tc.want {
				t.Errorf("Span(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestWrap(t *testing.T) {
	for _, tc := range []struct {
		name, in, info, want string
	}{
		{"plain text", "abc", "", "```\nabc\n```"},
		{"info string", "x", "suggestion", "```suggestion\nx\n```"},
		{"empty input", "", "", "```\n\n```"},
		{"inner fence and trailing newline", "a\n```\nb\n", "", "````\na\n```\nb\n\n````"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Wrap(tc.in, tc.info); got != tc.want {
				t.Errorf("Wrap(%q, %q) = %q, want %q", tc.in, tc.info, got, tc.want)
			}
		})
	}
}
