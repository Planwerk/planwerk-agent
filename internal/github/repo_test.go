package github

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
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
		{"https://github.com/planwerk/planwerk-agent", "planwerk", "planwerk-agent"},
		{"https://github.com/planwerk/planwerk-agent/", "planwerk", "planwerk-agent"},
		{"https://github.com/planwerk/planwerk-agent.git", "planwerk", "planwerk-agent"},
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
		{"planwerk/planwerk-agent", "planwerk", "planwerk-agent"},
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

// TestDefaultBranchHEAD_PassesOwnerAndNameAsStrings pins the gh flag form for
// the GraphQL string variables. `-F` type-coerces its value, so a repository
// literally named "2048", "404" or "null" would reach GraphQL as a number or
// null and be rejected against String!; `-f` passes it verbatim. The fake gh
// records its argv so the assertion needs no network.
func TestDefaultBranchHEAD_PassesOwnerAndNameAsStrings(t *testing.T) {
	dir := t.TempDir()
	argvFile := filepath.Join(dir, "argv")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + argvFile + "\necho deadbeef\n"
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(script), 0o755); err != nil {
		t.Fatalf("writing fake gh: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	sha, err := DefaultBranchHEAD("2048", "404")
	if err != nil {
		t.Fatalf("DefaultBranchHEAD: %v", err)
	}
	if sha != "deadbeef" {
		t.Errorf("sha = %q, want deadbeef", sha)
	}

	raw, err := os.ReadFile(argvFile)
	if err != nil {
		t.Fatalf("reading recorded argv: %v", err)
	}
	argv := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	for _, want := range []string{"owner=2048", "name=404"} {
		i := slices.Index(argv, want)
		if i <= 0 {
			t.Fatalf("argv %q carries no %q", argv, want)
		}
		if argv[i-1] != "-f" {
			t.Errorf("%q passed with %q, want -f (a string variable must not be type-coerced)", want, argv[i-1])
		}
	}
}
