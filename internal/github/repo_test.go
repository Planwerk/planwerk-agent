package github

import (
	"os"
	"testing"
)

func TestRepoCleanupNoOpWhenLocal(t *testing.T) {
	dir := t.TempDir()
	r := &Repo{Dir: dir, Local: true}
	r.Cleanup()
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("Local Repo.Cleanup must not remove the working tree: %v", err)
	}

	tmp := t.TempDir()
	nr := &Repo{Dir: tmp}
	nr.Cleanup()
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Fatalf("non-local Repo.Cleanup must remove the temp dir, stat err = %v", err)
	}
}

func TestParseRepoRef_URL(t *testing.T) {
	tests := []struct {
		input     string
		wantOwner string
		wantRepo  string
	}{
		{"https://github.com/planwerk/planwerk-review", "planwerk", "planwerk-review"},
		{"https://github.com/planwerk/planwerk-review/", "planwerk", "planwerk-review"},
		{"https://github.com/planwerk/planwerk-review.git", "planwerk", "planwerk-review"},
		{"https://github.com/org-name/my.repo", "org-name", "my.repo"},
	}

	for _, tt := range tests {
		owner, repo, err := ParseRepoRef(tt.input)
		if err != nil {
			t.Errorf("ParseRepoRef(%q) error: %v", tt.input, err)
			continue
		}
		if owner != tt.wantOwner || repo != tt.wantRepo {
			t.Errorf("ParseRepoRef(%q) = (%q, %q), want (%q, %q)",
				tt.input, owner, repo, tt.wantOwner, tt.wantRepo)
		}
	}
}

func TestParseRepoRef_Short(t *testing.T) {
	tests := []struct {
		input     string
		wantOwner string
		wantRepo  string
	}{
		{"planwerk/planwerk-review", "planwerk", "planwerk-review"},
		{"org-name/my.repo", "org-name", "my.repo"},
		{"user_1/repo_2", "user_1", "repo_2"},
	}

	for _, tt := range tests {
		owner, repo, err := ParseRepoRef(tt.input)
		if err != nil {
			t.Errorf("ParseRepoRef(%q) error: %v", tt.input, err)
			continue
		}
		if owner != tt.wantOwner || repo != tt.wantRepo {
			t.Errorf("ParseRepoRef(%q) = (%q, %q), want (%q, %q)",
				tt.input, owner, repo, tt.wantOwner, tt.wantRepo)
		}
	}
}

func TestParseRepoRef_Invalid(t *testing.T) {
	tests := []string{
		"",
		"just-a-name",
		"https://gitlab.com/owner/repo",
		"owner/repo#123", // This is a PR ref, not a repo ref
	}

	for _, input := range tests {
		_, _, err := ParseRepoRef(input)
		if err == nil {
			t.Errorf("ParseRepoRef(%q) expected error, got nil", input)
		}
	}
}
