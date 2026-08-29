package report

import "testing"

func TestShortSHA(t *testing.T) {
	cases := map[string]string{
		"abcdef0123456789": "abcdef0",
		"abc":              "abc",
		"  abcdef0123  ":   "abcdef0",
		"":                 "",
	}
	for in, want := range cases {
		if got := ShortSHA(in); got != want {
			t.Errorf("ShortSHA(%q) = %q, want %q", in, got, want)
		}
	}
}
