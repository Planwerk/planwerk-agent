package meta

import (
	"strings"
	"testing"
)

func TestApplyMetaReferences(t *testing.T) {
	cases := []struct {
		name        string
		body        string
		refs        map[string]int
		want        string
		wantAllDone bool
	}{
		{
			name:        "all placeholders resolved",
			body:        "- Foundation {{sub:a}}\n- Rollout {{sub:b}}\n",
			refs:        map[string]int{"a": 531, "b": 542},
			want:        "- Foundation #531\n- Rollout #542\n",
			wantAllDone: true,
		},
		{
			name:        "partial substitution leaves a dangling token",
			body:        "- Foundation {{sub:a}}\n- Rollout {{sub:b}}\n",
			refs:        map[string]int{"a": 531},
			want:        "- Foundation #531\n- Rollout {{sub:b}}\n",
			wantAllDone: false,
		},
		{
			name:        "body without placeholders is unchanged",
			body:        "Just prose, no work-package list.\n",
			refs:        map[string]int{"a": 531},
			want:        "Just prose, no work-package list.\n",
			wantAllDone: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, allDone := applyMetaReferences(tc.body, tc.refs)
			if got != tc.want {
				t.Errorf("body = %q, want %q", got, tc.want)
			}
			if allDone != tc.wantAllDone {
				t.Errorf("allResolved = %v, want %v", allDone, tc.wantAllDone)
			}
		})
	}
}

func TestBuildSubIssueBody(t *testing.T) {
	body := BuildSubIssueBody(525, SubIssue{
		Key:         "a",
		Title:       "Foundation",
		Description: "Lay the groundwork.",
		Motivation:  "Everything else builds on it.",
		Scope:       "Large",
	})
	for _, want := range []string{
		"**Category**: feature | **Scope**: Large",
		"## Description\n\nLay the groundwork.",
		"## Motivation\n\nEverything else builds on it.",
		"_Split from #525 by [planwerk-review](https://github.com/planwerk/planwerk-review) with Claude Code_",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q\n%s", want, body)
		}
	}
}

func TestBuildSubIssueBodyDefaultsScopeToMedium(t *testing.T) {
	body := BuildSubIssueBody(1, SubIssue{Key: "a", Title: "T", Description: "D"})
	if !strings.Contains(body, "**Scope**: Medium") {
		t.Errorf("blank scope should default to Medium, got:\n%s", body)
	}
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name    string
		result  Result
		wantErr string
	}{
		{
			name: "valid split",
			result: Result{
				SubIssues: []SubIssue{
					{Key: "a", Title: "T1", Description: "D1", Scope: "Small"},
					{Key: "b", Title: "T2", Description: "D2"},
				},
				MetaBody: "- one {{sub:a}}\n- two {{sub:b}}\n",
			},
		},
		{
			name:    "empty key",
			result:  Result{SubIssues: []SubIssue{{Key: "  ", Title: "T", Description: "D"}}},
			wantErr: "empty key",
		},
		{
			name: "duplicate key",
			result: Result{SubIssues: []SubIssue{
				{Key: "a", Title: "T1", Description: "D1"},
				{Key: "a", Title: "T2", Description: "D2"},
			}},
			wantErr: "duplicate key",
		},
		{
			name:    "empty title",
			result:  Result{SubIssues: []SubIssue{{Key: "a", Title: " ", Description: "D"}}},
			wantErr: "empty title",
		},
		{
			name:    "empty description",
			result:  Result{SubIssues: []SubIssue{{Key: "a", Title: "T", Description: ""}}},
			wantErr: "empty description",
		},
		{
			name:    "off-enum scope",
			result:  Result{SubIssues: []SubIssue{{Key: "a", Title: "T", Description: "D", Scope: "Huge"}}},
			wantErr: "must be one of Small, Medium, Large",
		},
		{
			name: "undeclared meta-body key",
			result: Result{
				SubIssues: []SubIssue{{Key: "a", Title: "T", Description: "D"}},
				MetaBody:  "- one {{sub:a}}\n- ghost {{sub:zzz}}\n",
			},
			wantErr: "undeclared sub-issue key",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.result.Validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Validate() = %v, want error containing %q", err, tc.wantErr)
			}
		})
	}
}
