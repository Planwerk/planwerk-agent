package github

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

// initGitRepoForDiff builds a temp git repo with two commits and points
// refs/remotes/origin/main at the first commit, so diffNames(dir, "main")
// resolves origin/main...HEAD entirely offline — no network, no real remote.
// The second commit modifies only changed.go.
func initGitRepoForDiff(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	run("init", "-q")
	run("config", "user.email", "t@example.com")
	run("config", "user.name", "tester")
	write("unchanged.go", "package x\n")
	write("changed.go", "package x\n// v1\n")
	run("add", "-A")
	run("commit", "-q", "-m", "first")
	firstSHA := run("rev-parse", "HEAD")
	write("changed.go", "package x\n// v2\n")
	run("add", "-A")
	run("commit", "-q", "-m", "second")
	run("update-ref", "refs/remotes/origin/main", firstSHA)
	return dir
}

func TestDiffNames(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		dir := initGitRepoForDiff(t)
		files, err := diffNames(dir, "main")
		if err != nil {
			t.Fatalf("diffNames returned error: %v", err)
		}
		if len(files) != 1 || files[0] != "changed.go" {
			t.Fatalf("files = %v, want [changed.go]", files)
		}
	})

	t.Run("missing remote", func(t *testing.T) {
		// A repo with one commit and no refs/remotes/origin/main: the diff
		// query fails exactly as it would on a missing remote or auth failure.
		dir := t.TempDir()
		run := func(args ...string) {
			cmd := exec.Command("git", args...)
			cmd.Dir = dir
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("git %v: %v\n%s", args, err, out)
			}
		}
		if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package x\n"), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
		run("init", "-q")
		run("config", "user.email", "t@example.com")
		run("config", "user.name", "tester")
		run("add", "-A")
		run("commit", "-q", "-m", "only")

		files, err := diffNames(dir, "main")
		if err == nil {
			t.Fatal("expected an error when origin/main is missing, got nil")
		}
		if files != nil {
			t.Errorf("files = %v, want nil on error", files)
		}
		if !strings.Contains(err.Error(), "git diff --name-only") {
			t.Errorf("error %q does not name the git command", err)
		}
	})

	t.Run("empty inputs", func(t *testing.T) {
		files, err := diffNames("", "")
		if err != nil {
			t.Errorf("diffNames(\"\", \"\") error = %v, want nil", err)
		}
		if files != nil {
			t.Errorf("files = %v, want nil", files)
		}
	})
}

func TestParseDefaultBranch(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"origin-prefixed", "origin/main", "main"},
		{"master", "origin/master", "master"},
		{"slash in branch name", "origin/feature/x", "feature/x"},
		{"trailing whitespace trimmed", "origin/main\n", "main"},
		{"no prefix returned as-is", "main", "main"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseDefaultBranch(tc.in); got != tc.want {
				t.Errorf("parseDefaultBranch(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestCurrentBranchRef builds an offline git repo with origin/HEAD pointing at
// origin/main and a feature branch checked out, so CurrentBranchRef resolves the
// base (from origin/HEAD) and head (the current branch) without a real remote.
func TestCurrentBranchRef(t *testing.T) {
	dir := initGitRepoForDiff(t)
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	// origin/main already exists (from initGitRepoForDiff); point origin/HEAD at
	// it and move onto a feature branch so head != base.
	run("symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
	run("checkout", "-q", "-b", "implement/issue-42-foo")

	ref, err := CurrentBranchRef(dir)
	if err != nil {
		t.Fatalf("CurrentBranchRef returned error: %v", err)
	}
	if ref.BaseBranch != "main" {
		t.Errorf("BaseBranch = %q, want main (from origin/HEAD)", ref.BaseBranch)
	}
	if ref.HeadBranch != "implement/issue-42-foo" {
		t.Errorf("HeadBranch = %q, want implement/issue-42-foo", ref.HeadBranch)
	}
}

// TestCurrentBranchRefMissingOriginHEAD locks that a checkout whose
// origin/HEAD is unset surfaces an error (rather than a bogus base branch), so
// the implement passes can skip cleanly instead of folding against the wrong base.
func TestCurrentBranchRefMissingOriginHEAD(t *testing.T) {
	dir := initGitRepoForDiff(t) // sets origin/main but not origin/HEAD
	if _, err := CurrentBranchRef(dir); err == nil {
		t.Fatal("expected an error when origin/HEAD is unset, got nil")
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
