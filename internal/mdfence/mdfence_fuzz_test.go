package mdfence

import (
	"strings"
	"testing"
)

func FuzzTicks(f *testing.F) {
	f.Add("")
	f.Add("plain")
	f.Add("a`b")
	f.Add("x := ```y```")
	f.Add("a``b````c`d")
	f.Add("ä```ö")
	f.Add("a\n```\nb\n")

	f.Fuzz(func(t *testing.T, s string) {
		ticks := Ticks(s)
		if len(ticks) < 3 || strings.Trim(ticks, "`") != "" {
			t.Fatalf("Ticks(%q) = %q, want a run of at least three backticks", s, ticks)
		}
		// No run inside s may be as long as the fence, or it could close it.
		if strings.Contains(s, ticks) {
			t.Fatalf("Ticks(%q) = %q occurs inside s", s, ticks)
		}
		// The fence is only as long as it has to be.
		if len(ticks) > 3 && !strings.Contains(s, ticks[1:]) {
			t.Fatalf("Ticks(%q) = %q is longer than needed", s, ticks)
		}
		if got, want := Wrap(s, ""), ticks+"\n"+s+"\n"+ticks; got != want {
			t.Fatalf("Wrap(%q, \"\") = %q, want %q", s, got, want)
		}
	})
}

func FuzzSpan(f *testing.F) {
	f.Add("go.sum")
	f.Add("a`b")
	f.Add("`x")
	f.Add(" a ")
	f.Add("<!-- x -->")

	f.Fuzz(func(t *testing.T, s string) {
		if s == "" {
			return // CommonMark has no empty code span.
		}
		span := Span(s)
		ticks := span[:len(span)-len(strings.TrimLeft(span, "`"))]
		if ticks == "" || !strings.HasSuffix(span, ticks) {
			t.Fatalf("Span(%q) = %q, want s between two equal runs of backticks", s, span)
		}
		// A run inside s as long as the delimiter would close the span early.
		if strings.Contains(s, ticks) {
			t.Fatalf("Span(%q) delimiter %q occurs inside s", s, ticks)
		}
		inner := span[len(ticks) : len(span)-len(ticks)]
		if inner != s && inner != " "+s+" " {
			t.Fatalf("Span(%q) = %q, want s itself inside the delimiters, padded by at most one space a side", s, span)
		}
	})
}
