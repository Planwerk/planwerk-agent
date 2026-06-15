package github

import (
	"slices"
	"testing"
)

func TestCreateIssueArgs(t *testing.T) {
	cases := []struct {
		name   string
		labels []string
		want   []string
	}{
		{
			name:   "no labels",
			labels: nil,
			want:   []string{"issue", "create", "--repo", "acme/widgets", "--title", "T", "--body", "B"},
		},
		{
			name:   "one label",
			labels: []string{"enhancement"},
			want:   []string{"issue", "create", "--repo", "acme/widgets", "--title", "T", "--body", "B", "--label", "enhancement"},
		},
		{
			name:   "multiple labels each repeated",
			labels: []string{"enhancement", "needs-triage"},
			want:   []string{"issue", "create", "--repo", "acme/widgets", "--title", "T", "--body", "B", "--label", "enhancement", "--label", "needs-triage"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := createIssueArgs("acme/widgets", "T", "B", tc.labels)
			if !slices.Equal(got, tc.want) {
				t.Fatalf("createIssueArgs() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestParseIssueComments(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    []string // comment bodies, in order
		wantErr bool
	}{
		{
			name: "two comments preserve order",
			in:   `{"comments":[{"body":"first"},{"body":"second"}]}`,
			want: []string{"first", "second"},
		},
		{
			name: "empty comments array",
			in:   `{"comments":[]}`,
			want: nil,
		},
		{
			name: "missing comments key",
			in:   `{}`,
			want: nil,
		},
		{
			name:    "malformed json",
			in:      `{"comments":[`,
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseIssueComments([]byte(tc.in))
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %d comments, want %d: %+v", len(got), len(tc.want), got)
			}
			for i, body := range tc.want {
				if got[i].Body != body {
					t.Errorf("comment[%d].Body = %q, want %q", i, got[i].Body, body)
				}
			}
		})
	}
}

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
