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
