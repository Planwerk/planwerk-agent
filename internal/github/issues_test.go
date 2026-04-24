package github

import "testing"

func TestParseIssueRef(t *testing.T) {
	cases := []struct {
		name      string
		input     string
		wantOwner string
		wantRepo  string
		wantNum   int
		wantErr   bool
	}{
		{"url", "https://github.com/acme/widgets/issues/42", "acme", "widgets", 42, false},
		{"short", "acme/widgets#7", "acme", "widgets", 7, false},
		{"short with whitespace", "  acme/widgets#1  ", "acme", "widgets", 1, false},
		{"invalid empty", "", "", "", 0, true},
		{"invalid pull url", "https://github.com/acme/widgets/pull/3", "", "", 0, true},
		{"invalid format", "acme widgets 1", "", "", 0, true},
		{"invalid owner chars", "ac me/widgets#1", "", "", 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			owner, repo, num, err := ParseIssueRef(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got owner=%q repo=%q num=%d", owner, repo, num)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if owner != tc.wantOwner || repo != tc.wantRepo || num != tc.wantNum {
				t.Fatalf("got (%q, %q, %d), want (%q, %q, %d)", owner, repo, num, tc.wantOwner, tc.wantRepo, tc.wantNum)
			}
		})
	}
}
