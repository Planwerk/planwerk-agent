package github

import (
	"slices"
	"strings"
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

func TestCloseIssueArgs(t *testing.T) {
	got := closeIssueArgs("acme", "widgets", 99)
	want := []string{"issue", "close", "99", "--repo", "acme/widgets"}
	if !slices.Equal(got, want) {
		t.Fatalf("closeIssueArgs() = %v, want %v", got, want)
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

// TestTrustedComments_KeepsOnlyTheViewerAndMaintainers: a plan, report, or
// continuation comment is recognized by text anyone can type, so only the
// authenticated user's own comments and the maintainers' survive.
func TestTrustedComments_KeepsOnlyTheViewerAndMaintainers(t *testing.T) {
	in := []IssueComment{
		{ID: "own", ViewerDidAuthor: true, AuthorAssociation: "NONE"},
		{ID: "owner", AuthorAssociation: "OWNER"},
		{ID: "member", AuthorAssociation: "MEMBER"},
		{ID: "collab", AuthorAssociation: "COLLABORATOR"},
		{ID: "contributor", AuthorAssociation: "CONTRIBUTOR"},
		{ID: "stranger", AuthorAssociation: "NONE"},
		{ID: "unknown"},
	}
	got := trustedComments(in)
	var ids []string
	for _, c := range got {
		ids = append(ids, c.ID)
	}
	if want := "own owner member collab"; strings.Join(ids, " ") != want {
		t.Errorf("trusted = %v, want %s", ids, want)
	}
}

func TestParseIssueComments_ReadsTheAuthorFields(t *testing.T) {
	out := []byte(`{"comments":[{"id":"IC_1","body":"b","authorAssociation":"MEMBER","viewerDidAuthor":true}]}`)
	got, err := parseIssueComments(out)
	if err != nil || len(got) != 1 || got[0].AuthorAssociation != "MEMBER" || !got[0].ViewerDidAuthor {
		t.Fatalf("parseIssueComments = %+v, %v", got, err)
	}
}
