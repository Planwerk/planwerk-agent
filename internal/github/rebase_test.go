package github

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// git runs a git subcommand in dir and fails the test on a non-zero exit.
func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_EDITOR=true", "GIT_SEQUENCE_EDITOR=true")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// gitOut runs a git subcommand in dir and returns its trimmed stdout.
func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}

func writeRepoFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

// initRebaseRepo builds a working repo on branch main with one base commit and
// a bare origin remote that has main (and refs/remotes/origin/main) tracking
// it. It returns the work dir, the bare remote path, and the base commit SHA.
func initRebaseRepo(t *testing.T) (dir, bare, baseSHA string) {
	t.Helper()
	dir = t.TempDir()
	bare = t.TempDir()
	git(t, bare, "init", "--bare", "-q")
	git(t, dir, "init", "-q")
	git(t, dir, "config", "user.email", "t@example.com")
	git(t, dir, "config", "user.name", "tester")
	writeRepoFile(t, dir, "base.txt", "line1\nline2\nline3\n")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-q", "-m", "base commit")
	git(t, dir, "branch", "-M", "main")
	git(t, dir, "remote", "add", "origin", bare)
	git(t, dir, "push", "-q", "-u", "origin", "main")
	return dir, bare, gitOut(t, dir, "rev-parse", "HEAD")
}

// advanceOrigin commits a change to name on main, pushes it, and refreshes the
// origin/main tracking ref — simulating upstream commits that entered the base
// after the PR forked.
func advanceOrigin(t *testing.T, dir, name, content, subject string) {
	t.Helper()
	git(t, dir, "checkout", "-q", "main")
	writeRepoFile(t, dir, name, content)
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-q", "-m", subject)
	git(t, dir, "push", "-q", "origin", "main")
	git(t, dir, "fetch", "-q", "origin")
}

func TestMergeBase(t *testing.T) {
	dir, _, baseSHA := initRebaseRepo(t)

	git(t, dir, "checkout", "-q", "-b", "feature")
	writeRepoFile(t, dir, "feature.txt", "f\n")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-q", "-m", "feature commit")

	advanceOrigin(t, dir, "upstream.txt", "u\n", "upstream commit")

	got, err := MergeBase(dir, "feature", "origin/main")
	if err != nil {
		t.Fatalf("MergeBase: %v", err)
	}
	if got != baseSHA {
		t.Errorf("MergeBase = %q, want base %q", got, baseSHA)
	}
}

func TestCommitsInRange(t *testing.T) {
	dir, _, baseSHA := initRebaseRepo(t)

	git(t, dir, "checkout", "-q", "-b", "feature")
	writeRepoFile(t, dir, "f1.txt", "1\n")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-q", "-m", "feature one")
	writeRepoFile(t, dir, "f2.txt", "2\n")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-q", "-m", "feature two")

	advanceOrigin(t, dir, "u1.txt", "u\n", "upstream one")

	// Replay range: feature commits not on origin/main, oldest first.
	replay, err := CommitsInRange(dir, "origin/main..feature")
	if err != nil {
		t.Fatalf("CommitsInRange replay: %v", err)
	}
	if len(replay) != 2 {
		t.Fatalf("replay len = %d, want 2: %+v", len(replay), replay)
	}
	if replay[0].Subject != "feature one" || replay[1].Subject != "feature two" {
		t.Errorf("replay order wrong: %+v", replay)
	}
	if replay[0].SHA == "" {
		t.Error("replay commit missing SHA")
	}

	// Upstream range: commits that entered the base after the fork point.
	upstream, err := CommitsInRange(dir, baseSHA+"..origin/main")
	if err != nil {
		t.Fatalf("CommitsInRange upstream: %v", err)
	}
	if len(upstream) != 1 || upstream[0].Subject != "upstream one" {
		t.Errorf("upstream = %+v, want one 'upstream one'", upstream)
	}
}

func TestStartRebaseClean(t *testing.T) {
	dir, _, _ := initRebaseRepo(t)

	git(t, dir, "checkout", "-q", "-b", "feature")
	writeRepoFile(t, dir, "feature.txt", "f\n")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-q", "-m", "feature commit")

	// Upstream touches a different file — no textual conflict.
	advanceOrigin(t, dir, "upstream.txt", "u\n", "upstream commit")
	git(t, dir, "checkout", "-q", "feature")

	state, err := StartRebase(dir, "main")
	if err != nil {
		t.Fatalf("StartRebase: %v", err)
	}
	if !state.Done || state.Conflicted {
		t.Fatalf("state = %+v, want Done", state)
	}
	// The rebased branch now sits on top of origin/main and still carries its
	// own commit (preserved, not squashed).
	rebased, err := CommitsInRange(dir, "origin/main..HEAD")
	if err != nil {
		t.Fatalf("CommitsInRange: %v", err)
	}
	if len(rebased) != 1 || rebased[0].Subject != "feature commit" {
		t.Errorf("rebased = %+v, want one 'feature commit'", rebased)
	}
}

func TestStartRebaseConflictThenAbort(t *testing.T) {
	dir, _, _ := initRebaseRepo(t)

	git(t, dir, "checkout", "-q", "-b", "feature")
	writeRepoFile(t, dir, "base.txt", "line1\nFEATURE\nline3\n")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-q", "-m", "feature edits line2")
	featureTip := gitOut(t, dir, "rev-parse", "HEAD")

	// Upstream edits the same line differently → conflict on rebase.
	advanceOrigin(t, dir, "base.txt", "line1\nUPSTREAM\nline3\n", "upstream edits line2")
	git(t, dir, "checkout", "-q", "feature")

	state, err := StartRebase(dir, "main")
	if err != nil {
		t.Fatalf("StartRebase: %v", err)
	}
	if !state.Conflicted {
		t.Fatalf("state = %+v, want Conflicted", state)
	}
	if len(state.ConflictedFiles) != 1 || state.ConflictedFiles[0] != "base.txt" {
		t.Errorf("ConflictedFiles = %v, want [base.txt]", state.ConflictedFiles)
	}
	if state.StoppedSubject != "feature edits line2" {
		t.Errorf("StoppedSubject = %q, want 'feature edits line2'", state.StoppedSubject)
	}

	if err := RebaseAbort(dir); err != nil {
		t.Fatalf("RebaseAbort: %v", err)
	}
	if got := gitOut(t, dir, "rev-parse", "HEAD"); got != featureTip {
		t.Errorf("after abort HEAD = %q, want feature tip %q", got, featureTip)
	}
}

func TestResetHard(t *testing.T) {
	dir, _, _ := initRebaseRepo(t)
	orig := gitOut(t, dir, "rev-parse", "HEAD")

	writeRepoFile(t, dir, "scratch.txt", "x\n")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-q", "-m", "scratch")

	if err := ResetHard(dir, orig); err != nil {
		t.Fatalf("ResetHard: %v", err)
	}
	if got := gitOut(t, dir, "rev-parse", "HEAD"); got != orig {
		t.Errorf("after ResetHard HEAD = %q, want %q", got, orig)
	}
	if _, err := os.Stat(filepath.Join(dir, "scratch.txt")); !os.IsNotExist(err) {
		t.Errorf("ResetHard left scratch.txt behind (err=%v)", err)
	}
}

func TestForceWithLeasePush(t *testing.T) {
	dir, bare, _ := initRebaseRepo(t)

	git(t, dir, "checkout", "-q", "-b", "feature")
	writeRepoFile(t, dir, "feature.txt", "f\n")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-q", "-m", "feature commit")
	git(t, dir, "push", "-q", "-u", "origin", "feature")

	advanceOrigin(t, dir, "upstream.txt", "u\n", "upstream commit")
	git(t, dir, "checkout", "-q", "feature")

	if state, err := StartRebase(dir, "main"); err != nil || !state.Done {
		t.Fatalf("StartRebase state=%+v err=%v", state, err)
	}
	rewritten := gitOut(t, dir, "rev-parse", "HEAD")

	if err := ForceWithLeasePush(dir, "feature"); err != nil {
		t.Fatalf("ForceWithLeasePush: %v", err)
	}
	if got := gitOut(t, bare, "rev-parse", "feature"); got != rewritten {
		t.Errorf("remote feature = %q, want rewritten %q", got, rewritten)
	}
}
