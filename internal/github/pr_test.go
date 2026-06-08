package github

import (
	"os"
	"testing"
)

func TestParseRef(t *testing.T) {
	t.Setenv("GITHUB_REPOSITORY", "")
	tests := []struct {
		name    string
		ref     string
		owner   string
		repo    string
		number  int
		wantErr bool
	}{
		{
			name:   "URL form",
			ref:    "https://github.com/planwerk/planwerk-review/pull/42",
			owner:  "planwerk",
			repo:   "planwerk-review",
			number: 42,
		},
		{
			name:   "short form",
			ref:    "planwerk/planwerk-review#42",
			owner:  "planwerk",
			repo:   "planwerk-review",
			number: 42,
		},
		{
			name:   "short form with whitespace",
			ref:    "  planwerk/planwerk-review#42  ",
			owner:  "planwerk",
			repo:   "planwerk-review",
			number: 42,
		},
		{
			name:   "dots and underscores in names",
			ref:    "my.org/my_repo#1",
			owner:  "my.org",
			repo:   "my_repo",
			number: 1,
		},
		{
			name:    "empty string",
			ref:     "",
			wantErr: true,
		},
		{
			name:    "missing number",
			ref:     "owner/repo",
			wantErr: true,
		},
		{
			name:    "invalid owner characters",
			ref:     "ow ner/repo#1",
			wantErr: true,
		},
		{
			name:    "invalid repo characters",
			ref:     "owner/re po#1",
			wantErr: true,
		},
		{
			name:    "bare number without GITHUB_REPOSITORY",
			ref:     "21",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			owner, repo, number, err := ParseRef(tt.ref)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got owner=%q repo=%q number=%d", owner, repo, number)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if owner != tt.owner {
				t.Errorf("owner = %q, want %q", owner, tt.owner)
			}
			if repo != tt.repo {
				t.Errorf("repo = %q, want %q", repo, tt.repo)
			}
			if number != tt.number {
				t.Errorf("number = %d, want %d", number, tt.number)
			}
		})
	}
}

func TestParseRefBareNumberWithGitHubRepository(t *testing.T) {
	tests := []struct {
		name        string
		envRepo     string
		ref         string
		wantOwner   string
		wantRepo    string
		wantNumber  int
		wantErr     bool
	}{
		{
			name:       "bare number resolves via GITHUB_REPOSITORY",
			envRepo:    "planwerk/planwerk-review",
			ref:        "21",
			wantOwner:  "planwerk",
			wantRepo:   "planwerk-review",
			wantNumber: 21,
		},
		{
			name:    "malformed GITHUB_REPOSITORY rejected",
			envRepo: "no-slash",
			ref:     "21",
			wantErr: true,
		},
		{
			name:    "GITHUB_REPOSITORY with invalid characters rejected",
			envRepo: "bad owner/repo",
			ref:     "21",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("GITHUB_REPOSITORY", tt.envRepo)
			owner, repo, number, err := ParseRef(tt.ref)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got owner=%q repo=%q number=%d", owner, repo, number)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if owner != tt.wantOwner || repo != tt.wantRepo || number != tt.wantNumber {
				t.Errorf("got %s/%s#%d, want %s/%s#%d", owner, repo, number, tt.wantOwner, tt.wantRepo, tt.wantNumber)
			}
		})
	}
}

func TestPRCleanupNoOpWhenLocal(t *testing.T) {
	dir := t.TempDir()
	pr := &PR{Dir: dir, Local: true}
	pr.Cleanup()
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("Local PR.Cleanup must not remove the working tree: %v", err)
	}

	// A non-local PR must still clean up.
	tmp := t.TempDir()
	np := &PR{Dir: tmp}
	np.Cleanup()
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Fatalf("non-local PR.Cleanup must remove the temp dir, stat err = %v", err)
	}
}
