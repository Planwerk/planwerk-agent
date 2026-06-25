package github

import (
	"slices"
	"testing"
)

func TestMarkPRReadyArgs(t *testing.T) {
	got := markPRReadyArgs("acme", "widgets", 42)
	want := []string{"pr", "ready", "42", "--repo", "acme/widgets"}
	if !slices.Equal(got, want) {
		t.Fatalf("markPRReadyArgs() = %v, want %v", got, want)
	}
}

func TestPRMergeabilityArgs(t *testing.T) {
	got := prMergeabilityArgs("acme", "widgets", 42)
	want := []string{"pr", "view", "42", "--repo", "acme/widgets",
		"--json", "mergeable,mergeStateStatus,reviewDecision,isDraft,headRefOid"}
	if !slices.Equal(got, want) {
		t.Fatalf("prMergeabilityArgs() = %v, want %v", got, want)
	}
}

func TestMergePRArgs(t *testing.T) {
	tests := []struct {
		name    string
		method  string
		headSHA string
		want    []string
		wantErr bool
	}{
		{"rebase pinned", "rebase", "abc123", []string{"pr", "merge", "42", "--repo", "acme/widgets", "--rebase", "--match-head-commit", "abc123"}, false},
		{"squash pinned", "squash", "abc123", []string{"pr", "merge", "42", "--repo", "acme/widgets", "--squash", "--match-head-commit", "abc123"}, false},
		{"merge pinned", "merge", "abc123", []string{"pr", "merge", "42", "--repo", "acme/widgets", "--merge", "--match-head-commit", "abc123"}, false},
		{"empty sha omits the pin", "rebase", "", []string{"pr", "merge", "42", "--repo", "acme/widgets", "--rebase"}, false},
		{"fast-forward", "fast-forward", "abc123", nil, true},
		{"empty method", "", "abc123", nil, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := mergePRArgs("acme", "widgets", 42, tc.method, tc.headSHA)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("mergePRArgs(%q) expected an error", tc.method)
				}
				return
			}
			if err != nil {
				t.Fatalf("mergePRArgs(%q) error: %v", tc.method, err)
			}
			if !slices.Equal(got, tc.want) {
				t.Fatalf("mergePRArgs(%q) = %v, want %v", tc.method, got, tc.want)
			}
		})
	}
}

func TestMergeabilityCanMerge(t *testing.T) {
	tests := []struct {
		name string
		m    Mergeability
		want bool
	}{
		{"clean approved", Mergeability{Mergeable: "MERGEABLE", MergeStateStatus: "CLEAN", ReviewDecision: "APPROVED"}, true},
		{"clean no review required", Mergeability{Mergeable: "MERGEABLE", MergeStateStatus: "CLEAN", ReviewDecision: ""}, true},
		{"unstable still mergeable", Mergeability{Mergeable: "MERGEABLE", MergeStateStatus: "UNSTABLE"}, true},
		{"still draft", Mergeability{Mergeable: "MERGEABLE", MergeStateStatus: "CLEAN", IsDraft: true}, false},
		{"conflicting", Mergeability{Mergeable: "CONFLICTING", MergeStateStatus: "DIRTY"}, false},
		{"unknown mergeability", Mergeability{Mergeable: "UNKNOWN", MergeStateStatus: "UNKNOWN"}, false},
		{"blocked by protection", Mergeability{Mergeable: "MERGEABLE", MergeStateStatus: "BLOCKED"}, false},
		{"behind base", Mergeability{Mergeable: "MERGEABLE", MergeStateStatus: "BEHIND"}, false},
		{"changes requested", Mergeability{Mergeable: "MERGEABLE", MergeStateStatus: "CLEAN", ReviewDecision: "CHANGES_REQUESTED"}, false},
		{"review required", Mergeability{Mergeable: "MERGEABLE", MergeStateStatus: "CLEAN", ReviewDecision: "REVIEW_REQUIRED"}, false},
		{"zero value", Mergeability{}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.m.CanMerge(); got != tc.want {
				t.Fatalf("CanMerge(%+v) = %v, want %v", tc.m, got, tc.want)
			}
		})
	}
}
