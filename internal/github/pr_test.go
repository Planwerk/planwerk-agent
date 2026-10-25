package github

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
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
			ref:    "https://github.com/planwerk/planwerk-agent/pull/42",
			owner:  "planwerk",
			repo:   "planwerk-agent",
			number: 42,
		},
		{
			name:   "short form",
			ref:    "planwerk/planwerk-agent#42",
			owner:  "planwerk",
			repo:   "planwerk-agent",
			number: 42,
		},
		{
			name:   "short form with whitespace",
			ref:    "  planwerk/planwerk-agent#42  ",
			owner:  "planwerk",
			repo:   "planwerk-agent",
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
			envRepo:    "planwerk/planwerk-agent",
			ref:        "21",
			wantOwner:  "planwerk",
			wantRepo:   "planwerk-agent",
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
// refs/remotes/origin/main at the first commit, so DiffNames(dir, "main")
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
		files, err := client.DiffNames(dir, "main")
		if err != nil {
			t.Fatalf("DiffNames returned error: %v", err)
		}
		if len(files) != 1 || files[0] != "changed.go" {
			t.Fatalf("files = %v, want [changed.go]", files)
		}
	})

	t.Run("non-ASCII path", func(t *testing.T) {
		// git's default core.quotePath=true, set in the repo config, overrides
		// a developer's global setting, so the path comes back as stored only
		// through the -c option. The NFC escape is what macOS git
		// (core.precomposeUnicode) returns as well.
		const name = "\u00e4.go"
		dir := initGitRepoForDiff(t)
		git(t, dir, "config", "core.quotePath", "true")
		writeRepoFile(t, dir, name, "package x\n")
		git(t, dir, "add", "-A")
		git(t, dir, "commit", "-q", "-m", "third")

		files, err := client.DiffNames(dir, "main")
		if err != nil {
			t.Fatalf("DiffNames returned error: %v", err)
		}
		if want := []string{"changed.go", name}; !slices.Equal(files, want) {
			t.Fatalf("files = %q, want %q", files, want)
		}
	})

	t.Run("whitespace at either end", func(t *testing.T) {
		// git prints both names raw and they come first and last in its
		// output, so any trimming of a line, or of the whole output, cuts a
		// byte off. strings.TrimSpace also strips U+00A0 (NO-BREAK SPACE).
		const leading, trailing = " lead.go", "trail.go "
		dir := initGitRepoForDiff(t)
		git(t, dir, "config", "core.quotePath", "true")
		writeRepoFile(t, dir, leading, "package x\n")
		writeRepoFile(t, dir, trailing, "package x\n")
		git(t, dir, "add", "-A")
		git(t, dir, "commit", "-q", "-m", "third")

		files, err := client.DiffNames(dir, "main")
		if err != nil {
			t.Fatalf("DiffNames returned error: %v", err)
		}
		if want := []string{leading, "changed.go", trailing}; !slices.Equal(files, want) {
			t.Fatalf("files = %q, want %q", files, want)
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

		files, err := client.DiffNames(dir, "main")
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
		files, err := client.DiffNames("", "")
		if err != nil {
			t.Errorf("DiffNames(\"\", \"\") error = %v, want nil", err)
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
	run("checkout", "-q", "-b", testFeatureBranch)

	ref, err := client.CurrentBranchRef(dir)
	if err != nil {
		t.Fatalf("CurrentBranchRef returned error: %v", err)
	}
	if ref.BaseBranch != "main" {
		t.Errorf("BaseBranch = %q, want main (from origin/HEAD)", ref.BaseBranch)
	}
	if ref.HeadBranch != testFeatureBranch {
		t.Errorf("HeadBranch = %q, want implement/issue-42-foo", ref.HeadBranch)
	}
}

// TestCurrentBranchRefMissingOriginHEAD locks that a checkout whose
// origin/HEAD is unset surfaces an error (rather than a bogus base branch), so
// the implement passes can skip cleanly instead of folding against the wrong base.
func TestCurrentBranchRefMissingOriginHEAD(t *testing.T) {
	dir := initGitRepoForDiff(t) // sets origin/main but not origin/HEAD
	if _, err := client.CurrentBranchRef(dir); err == nil {
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

// TestHasCommit locks the diff-base guard a resumed review round relies on: an
// existing commit id (full or abbreviated) is present, an unknown one, an empty
// one, and a non-commit ref are not — none of them as an error.
func TestHasCommit(t *testing.T) {
	dir := initGitRepoForDiff(t)
	head, err := client.HeadSHA(dir)
	if err != nil {
		t.Fatalf("HeadSHA: %v", err)
	}
	if !client.HasCommit(dir, head) {
		t.Errorf("HasCommit(HEAD %s) = false, want true", head)
	}
	if !client.HasCommit(dir, head[:8]) {
		t.Errorf("HasCommit(abbreviated %s) = false, want true", head[:8])
	}
	for _, sha := range []string{"", "0123456789abcdef0123456789abcdef01234567", "not-a-sha"} {
		if client.HasCommit(dir, sha) {
			t.Errorf("HasCommit(%q) = true, want false", sha)
		}
	}
}

// TestPinHead locks the head check itself (TestFetchAndCheckoutPinsHead and
// TestOpenLocalPRPinsHead lock its wiring): a checkout at the reported head
// passes without a re-read; a head that moved during the clone is taken once
// the re-read confirms it; an unconfirmed mismatch fails with ErrHeadMismatch
// and the mode's remedy; and pr.HeadSHA is left as reported on every error.
func TestPinHead(t *testing.T) {
	dir := initGitRepoForDiff(t)
	head, err := client.HeadSHA(dir)
	if err != nil {
		t.Fatalf("HeadSHA: %v", err)
	}
	first, err := gitOutput(dir, "rev-parse", "HEAD~1")
	if err != nil {
		t.Fatalf("rev-parse HEAD~1: %v", err)
	}
	newPR := func(reported string) *PR {
		return &PR{Owner: "acme", Repo: "widgets", Number: 7, HeadBranch: "feature", Dir: dir, HeadSHA: reported}
	}
	noRefresh := func(t *testing.T) func() (string, error) {
		return func() (string, error) {
			t.Fatal("refresh called, want no re-read")
			return "", nil
		}
	}

	t.Run("match", func(t *testing.T) {
		pr := newPR(head)
		if err := pinHead(pr, noRefresh(t)); err != nil {
			t.Fatalf("pinHead: %v", err)
		}
		if pr.HeadSHA != head {
			t.Errorf("HeadSHA = %s, want %s", pr.HeadSHA, head)
		}
	})

	t.Run("moved head is adopted", func(t *testing.T) {
		pr := newPR(first)
		calls := 0
		err := pinHead(pr, func() (string, error) {
			calls++
			return head, nil
		})
		if err != nil {
			t.Fatalf("pinHead: %v", err)
		}
		if pr.HeadSHA != head {
			t.Errorf("HeadSHA = %s, want the checked-out %s", pr.HeadSHA, head)
		}
		if calls != 1 {
			t.Errorf("refresh calls = %d, want 1", calls)
		}
	})

	t.Run("clone mismatch fails", func(t *testing.T) {
		pr := newPR(first)
		err := pinHead(pr, func() (string, error) { return first, nil })
		if !errors.Is(err, ErrHeadMismatch) {
			t.Fatalf("err = %v, want ErrHeadMismatch", err)
		}
		if pr.HeadSHA != first {
			t.Errorf("HeadSHA = %s, want it left at %s", pr.HeadSHA, first)
		}
		msg := err.Error()
		for _, want := range []string{first, head, "re-run"} {
			if !strings.Contains(msg, want) {
				t.Errorf("error %q does not contain %q", msg, want)
			}
		}
		if strings.Contains(msg, "--local") {
			t.Errorf("error %q names --local for a clone run", msg)
		}
	})

	t.Run("local mismatch names the remedy", func(t *testing.T) {
		pr := newPR(first)
		pr.Local = true
		err := pinHead(pr, func() (string, error) { return first, nil })
		if !errors.Is(err, ErrHeadMismatch) {
			t.Fatalf("err = %v, want ErrHeadMismatch", err)
		}
		if pr.HeadSHA != first {
			t.Errorf("HeadSHA = %s, want it left at %s", pr.HeadSHA, first)
		}
		msg := err.Error()
		for _, want := range []string{"gh pr checkout --force 7", "--local", first, head} {
			if !strings.Contains(msg, want) {
				t.Errorf("error %q does not contain %q", msg, want)
			}
		}
	})

	t.Run("re-read failure", func(t *testing.T) {
		pr := newPR(first)
		boom := errors.New("boom")
		err := pinHead(pr, func() (string, error) { return "", boom })
		if err == nil {
			t.Fatal("pinHead returned nil, want the re-read error")
		}
		if errors.Is(err, ErrHeadMismatch) {
			t.Errorf("err = %v, want a re-read error, not ErrHeadMismatch", err)
		}
		if !errors.Is(err, boom) {
			t.Errorf("err = %v, want it to wrap the re-read error", err)
		}
		if pr.HeadSHA != first {
			t.Errorf("HeadSHA = %s, want it left at %s", pr.HeadSHA, first)
		}
	})

	t.Run("unresolvable HEAD", func(t *testing.T) {
		empty := t.TempDir()
		if out, err := exec.Command("git", "-C", empty, "init", "-q").CombinedOutput(); err != nil {
			t.Fatalf("git init: %v\n%s", err, out)
		}
		pr := newPR(head)
		pr.Dir = empty
		err := pinHead(pr, noRefresh(t))
		if err == nil {
			t.Fatal("pinHead returned nil for a repository without commits")
		}
		if !strings.Contains(err.Error(), "rev-parse") {
			t.Errorf("error %q does not name rev-parse", err)
		}
		if pr.HeadSHA != head {
			t.Errorf("HeadSHA = %s, want it left at %s", pr.HeadSHA, head)
		}
	})
}

// fakeCheckoutGH puts a gh on PATH that serves FetchAndCheckout and
// OpenLocalPR without GitHub. `repo clone` clones fixture into the target
// directory; `pr checkout` leaves the checkout where it is, which is what
// gh's fast-forward does to a branch that is ahead; `pr view` reports PR 7
// with head reported on its first call and reread on every later one. It
// returns the file every call's argv is recorded in (ghCalls reads it).
func fakeCheckoutGH(t *testing.T, fixture, reported, reread string) string {
	t.Helper()
	state := t.TempDir()
	argvFile := filepath.Join(state, "argv")
	viewed := filepath.Join(state, "viewed")
	fakeGH(t, recordArgv(argvFile)+`case "$1 $2" in
"repo clone") exec git clone -q '`+fixture+`' "$4" ;;
"pr checkout") exit 0 ;;
"pr view")
  sha='`+reported+`'
  if [ -e '`+viewed+`' ]; then sha='`+reread+`'; fi
  : > '`+viewed+`'
  printf '{"number":7,"title":"t","body":"","headRefOid":"%s","baseRefName":"","headRefName":"feature"}\n' "$sha" ;;
*) echo "unexpected gh call: $*" >&2; exit 1 ;;
esac
`)
	return argvFile
}

// ghCalls returns the argv of every call recorded in argvFile (recordArgv)
// whose leading arguments are sub, such as "pr", "view".
func ghCalls(t *testing.T, argvFile string, sub ...string) [][]string {
	t.Helper()
	raw, err := os.ReadFile(argvFile)
	if err != nil {
		t.Fatalf("reading recorded argv: %v", err)
	}
	var calls [][]string
	for _, call := range strings.Split(strings.TrimSuffix(string(raw), "----\n"), "----\n") {
		argv := strings.Split(strings.TrimRight(call, "\n"), "\n")
		if len(argv) >= len(sub) && slices.Equal(argv[:len(sub)], sub) {
			calls = append(calls, argv)
		}
	}
	return calls
}

// TestFetchAndCheckoutPinsHead locks the head check FetchAndCheckout runs on
// its temp-dir clone: a clone at the reported head is not re-read, a moved
// head is taken after one re-read with the first read's query, and an
// unconfirmed mismatch fails with ErrHeadMismatch and removes the clone, which
// the caller never receives to Cleanup.
func TestFetchAndCheckoutPinsHead(t *testing.T) {
	fixture := initGitRepoForDiff(t)
	head, err := client.HeadSHA(fixture)
	if err != nil {
		t.Fatalf("HeadSHA: %v", err)
	}
	first, err := gitOutput(fixture, "rev-parse", "HEAD~1")
	if err != nil {
		t.Fatalf("rev-parse HEAD~1: %v", err)
	}

	for _, tc := range []struct {
		name             string
		reported, reread string
		wantViews        int
		wantMismatch     bool
	}{
		{name: "clone at the reported head", reported: head, reread: first, wantViews: 1},
		{name: "moved head is adopted", reported: first, reread: head, wantViews: 2},
		{name: "unconfirmed mismatch removes the clone", reported: first, reread: first, wantViews: 2, wantMismatch: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tmp := t.TempDir()
			t.Setenv("TMPDIR", tmp)
			argvFile := fakeCheckoutGH(t, fixture, tc.reported, tc.reread)

			pr, err := client.FetchAndCheckout("acme/widgets#7")
			clones := ghCalls(t, argvFile, "repo", "clone")
			if len(clones) != 1 || len(clones[0]) < 4 || filepath.Dir(clones[0][3]) != tmp {
				t.Fatalf("repo clone calls = %q, want one into a directory under %s", clones, tmp)
			}
			cloneDir := clones[0][3]
			if tc.wantMismatch {
				if !errors.Is(err, ErrHeadMismatch) {
					t.Fatalf("err = %v, want ErrHeadMismatch", err)
				}
				if pr != nil {
					t.Errorf("pr = %+v, want nil on a pin failure", pr)
				}
				if _, statErr := os.Stat(cloneDir); !os.IsNotExist(statErr) {
					t.Errorf("clone %s left behind after the pin failure (stat: %v)", cloneDir, statErr)
				}
			} else {
				if err != nil {
					t.Fatalf("FetchAndCheckout: %v", err)
				}
				t.Cleanup(pr.Cleanup)
				if pr.Dir != cloneDir {
					t.Errorf("Dir = %s, want the clone %s", pr.Dir, cloneDir)
				}
				if pr.HeadSHA != head {
					t.Errorf("HeadSHA = %s, want the checked-out %s", pr.HeadSHA, head)
				}
			}

			views := ghCalls(t, argvFile, "pr", "view")
			if len(views) != tc.wantViews {
				t.Fatalf("pr view calls = %d, want %d", len(views), tc.wantViews)
			}
			for _, v := range views[1:] {
				if !slices.Equal(v, views[0]) {
					t.Errorf("re-read %q, want the first read's query %q", v, views[0])
				}
			}
		})
	}
}

// TestGitBytes_DoesNotWaitOnAGrandchildHoldingItsOutput pins gitWaitDelay: a
// process git leaves running with the output pipes open, as a lazy fetch in a
// blobless PR clone can during DiffFileChanges, must not keep gitBytes waiting
// until it ends.
func TestGitBytes_DoesNotWaitOnAGrandchildHoldingItsOutput(t *testing.T) {
	t.Parallel()
	start := time.Now()
	_, err := gitBytes(t.TempDir(), "-c", "alias.hold=!sleep 30 &", "hold")
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Errorf("gitBytes returned after %s: the grandchild held it until it ended", elapsed)
	}
	if !errors.Is(err, exec.ErrWaitDelay) {
		t.Errorf("err = %v, want exec.ErrWaitDelay once the pipes were closed", err)
	}
}
