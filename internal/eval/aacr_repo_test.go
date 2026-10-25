package eval

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/planwerk/planwerk-agent/internal/github"
)

// requireGit skips a test that needs the git binary when it is not on PATH.
func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
}

// gitIn runs git in dir and returns its trimmed stdout, failing the test on
// an error.
func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := gitRun(dir, gitTimeout, nil, args...)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// aacrRemote is a repository shaped like an AACR-Bench pull request: the
// merge base, a base branch that moved on after it, and a reviewed revision
// no ref points at, as a force-pushed one is.
type aacrRemote struct {
	dir       string
	mergeBase string // c1
	base      string // main's tip, the export's pr_source_commit
	head      string // c3, the export's pr_target_commit
}

// newAACRRemote builds the remote in a temp dir: c0 and c1 on main, three more
// commits on main touching other.txt, and c3 on a pr branch off c1 touching
// a.go, which is then deleted.
func newAACRRemote(t *testing.T) aacrRemote {
	t.Helper()
	dir := t.TempDir()
	if err := initRepo(dir); err != nil {
		t.Fatal(err)
	}
	// Serve an object no ref reaches, as GitHub serves a force-pushed revision.
	gitIn(t, dir, "config", "uploadpack.allowAnySHA1InWant", "true")
	commit := func(file, content, msg string) string {
		t.Helper()
		writeFile(t, filepath.Join(dir, file), content)
		gitIn(t, dir, "add", "-A")
		gitIn(t, dir, "commit", "-q", "-m", msg)
		return gitIn(t, dir, "rev-parse", "HEAD")
	}
	commit("a.go", "package a\n", "c0")
	r := aacrRemote{dir: dir}
	r.mergeBase = commit("other.txt", "1\n", "c1")
	for _, n := range []string{"2", "3", "4"} {
		r.base = commit("other.txt", n+"\n", "main "+n)
	}
	gitIn(t, dir, "checkout", "-q", "-b", "pr", r.mergeBase)
	r.head = commit("a.go", "package a\n\nfunc A() {}\n", "c3")
	gitIn(t, dir, "checkout", "-q", evalBaseBranch)
	gitIn(t, dir, "branch", "-q", "-D", "pr")
	return r
}

// source returns an AACRSource for r's pull request, cloned from cloneURL.
func (r aacrRemote) source(cloneURL string) *AACRSource {
	return &AACRSource{
		URL: "https://github.com/acme/widget/pull/7", Owner: "acme", Repo: "widget", Number: 7,
		CloneURL: cloneURL, BaseSHA: r.base, HeadSHA: r.head,
	}
}

func TestPrepareAACRFindsTheMergeBase(t *testing.T) {
	requireGit(t)
	remote := newAACRRemote(t)
	root := t.TempDir()

	src := remote.source("file://" + remote.dir)
	if err := prepareAACR(root, []Case{{Name: "widget-7", AACR: src}}, []int{1, 8}, io.Discard); err != nil {
		t.Fatalf("prepareAACR: %v", err)
	}
	if src.MergeBase != remote.mergeBase {
		t.Errorf("MergeBase = %s, want c1 %s", src.MergeBase, remote.mergeBase)
	}
	if want := filepath.Join(root, "acme", "widget", "7.git"); src.Store != want {
		t.Errorf("Store = %s, want %s", src.Store, want)
	}

	// The store already holds both commits and their merge base, so a second
	// run needs no remote at all.
	again := remote.source("file://" + filepath.Join(t.TempDir(), "missing"))
	if err := prepareAACR(root, []Case{{Name: "widget-7", AACR: again}}, []int{1, 8}, io.Discard); err != nil {
		t.Fatalf("prepareAACR on a filled store: %v", err)
	}
	if again.MergeBase != remote.mergeBase || again.Store != src.Store {
		t.Errorf("reused source = %s in %s, want %s in %s", again.MergeBase, again.Store, remote.mergeBase, src.Store)
	}

	t.Run("depths that never reach the merge base name the deepest", func(t *testing.T) {
		shallow := remote.source("file://" + remote.dir)
		err := prepareAACR(t.TempDir(), []Case{{Name: "widget-7", AACR: shallow}}, []int{1}, io.Discard)
		if err == nil || !strings.Contains(err.Error(), "case widget-7 (https://github.com/acme/widget/pull/7): no merge base of "+remote.base+" and "+remote.head+" within 1 commits") {
			t.Errorf("err = %v, want a no-merge-base error", err)
		}
		if shallow.Store != "" || shallow.MergeBase != "" {
			t.Errorf("source = %+v, want Store and MergeBase unset", shallow)
		}
	})
}

func TestPrepareAACRNamesEveryUnfetchablePR(t *testing.T) {
	requireGit(t)
	remote := newAACRRemote(t)
	missing := "file://" + filepath.Join(t.TempDir(), "missing")
	first, second := remote.source(missing), remote.source(missing)
	second.Number = 8

	var log strings.Builder
	err := prepareAACR(t.TempDir(), []Case{{Name: "widget-7", AACR: first}, {Name: "widget-8", AACR: second}, {Name: "corpus-case"}}, []int{1}, &log)
	if err == nil {
		t.Fatal("prepareAACR = nil error, want one naming both pull requests")
	}
	for _, want := range []string{"case widget-7 (", "case widget-8 (", "git fetch"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %q, want it to contain %q", err, want)
		}
	}
	if want := "preparing widget-7 (acme/widget#7) ...\npreparing widget-8 (acme/widget#8) ...\n"; log.String() != want {
		t.Errorf("log = %q, want %q (the corpus case skipped)", log.String(), want)
	}
}

func TestSetupAACRRepo(t *testing.T) {
	requireGit(t)
	remote := newAACRRemote(t)
	src := remote.source("file://" + remote.dir)
	if err := prepareAACR(t.TempDir(), []Case{{Name: "widget-7", AACR: src}}, []int{8}, io.Discard); err != nil {
		t.Fatalf("prepareAACR: %v", err)
	}

	dir := t.TempDir()
	headSHA, changed, err := setupAACRRepo(dir, src)
	if err != nil {
		t.Fatalf("setupAACRRepo: %v", err)
	}
	if got, want := gitIn(t, dir, "rev-parse", "HEAD^{tree}"), gitIn(t, remote.dir, "rev-parse", remote.head+"^{tree}"); got != want {
		t.Errorf("HEAD's tree = %s, want the revision's %s", got, want)
	}
	if got, want := gitIn(t, dir, "rev-parse", "origin/"+evalBaseBranch+"^{tree}"), gitIn(t, remote.dir, "rev-parse", remote.mergeBase+"^{tree}"); got != want {
		t.Errorf("origin/main's tree = %s, want the merge base's %s", got, want)
	}
	if got := gitIn(t, dir, "rev-parse", evalBaseBranch); got != gitIn(t, dir, "rev-parse", "origin/"+evalBaseBranch) {
		t.Errorf("main = %s, want it at origin/main", got)
	}
	// The base branch moved on after c1, but the diff is the revision's own.
	if !slices.Equal(changed, []string{"a.go"}) {
		t.Errorf("changed = %v, want [a.go], not other.txt", changed)
	}
	if got := gitIn(t, dir, "rev-list", "origin/"+evalBaseBranch+"..HEAD"); got != headSHA {
		t.Errorf("origin/main..HEAD = %q, want the one head commit %s", got, headSHA)
	}
	if got := gitIn(t, dir, "symbolic-ref", "HEAD"); got != "refs/heads/"+evalHeadBranch {
		t.Errorf("HEAD = %s, want refs/heads/%s", got, evalHeadBranch)
	}
	if got, err := os.ReadFile(filepath.Join(dir, "a.go")); err != nil || string(got) != "package a\n\nfunc A() {}\n" {
		t.Errorf("a.go = %q, %v, want the revision's content checked out", got, err)
	}
	// Decision 106's go.mod belongs to the corpus only.
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("stat go.mod = %v, want no go.mod in the worktree", err)
	}
	if _, err := gitRun(dir, gitTimeout, nil, "cat-file", "-e", evalBaseBranch+":go.mod"); err == nil {
		t.Error("main holds a go.mod, want none")
	}
	gitIn(t, dir, "fsck", "--connectivity-only")

	t.Run("checkoutCase hands the pipeline the real pull request with no title", func(t *testing.T) {
		repoDir := t.TempDir()
		pr, err := checkoutCase(repoDir, Case{Name: "widget-7", Expected: Expected{Description: src.URL}, AACR: src})
		if err != nil {
			t.Fatalf("checkoutCase: %v", err)
		}
		if pr.Owner != "acme" || pr.Repo != "widget" || pr.Number != 7 {
			t.Errorf("PR = %s/%s#%d, want acme/widget#7", pr.Owner, pr.Repo, pr.Number)
		}
		if pr.Title != "" || pr.Body != "" {
			t.Errorf("PR title/body = %q/%q, want both empty", pr.Title, pr.Body)
		}
		if head := gitIn(t, repoDir, "rev-parse", "HEAD"); pr.HeadSHA != head {
			t.Errorf("HeadSHA = %s, want the checkout's HEAD %s", pr.HeadSHA, head)
		}
		if pr.BaseBranch != evalBaseBranch || pr.HeadBranch != evalHeadBranch || pr.Dir != repoDir || !pr.Local || !slices.Equal(pr.ChangedFiles, []string{"a.go"}) {
			t.Errorf("PR = %+v, want a local PR on %s..%s in %s changing a.go", pr, evalBaseBranch, evalHeadBranch, repoDir)
		}
		// The review prompt's Changed Files section lists the revision's
		// own change, not the base branch's later commits (decision 138).
		if want := []github.FileChange{{Path: "a.go", Status: github.FileModified, Added: 2}}; !reflect.DeepEqual(pr.FileChanges, want) {
			t.Errorf("FileChanges = %+v, want %+v", pr.FileChanges, want)
		}
	})

	t.Run("an unprepared source is refused", func(t *testing.T) {
		if _, _, err := setupAACRRepo(t.TempDir(), remote.source("file://"+remote.dir)); err == nil || !strings.Contains(err.Error(), "run PrepareAACR first") {
			t.Errorf("setupAACRRepo = %v, want the not-prepared error", err)
		}
	})
}

func TestGitRun(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	if err := initRepo(dir); err != nil {
		t.Fatal(err)
	}
	out, err := gitRun(dir, gitTimeout, []string{"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=eval.probe", "GIT_CONFIG_VALUE_0=set"}, "config", "eval.probe")
	if err != nil || out != "set" {
		t.Errorf("gitRun = %q, %v, want the trimmed stdout of a git that saw env", out, err)
	}
	if _, err := gitRun(dir, gitTimeout, nil, "rev-parse", "no-such-ref"); err == nil || !strings.Contains(err.Error(), "git rev-parse no-such-ref:") {
		t.Errorf("gitRun = %v, want the command named in the error", err)
	}
}

// TestGitRun_TimeoutIsNotHeldByAChildHoldingItsOutput pins gitWaitDelay: a
// process that outlives a git its timeout killed, with git's output pipes
// still open, as a fetch's remote helper does, must not keep gitRun waiting
// until it ends.
func TestGitRun_TimeoutIsNotHeldByAChildHoldingItsOutput(t *testing.T) {
	requireGit(t)
	start := time.Now()
	_, err := gitRun(t.TempDir(), time.Second, nil, "-c", "alias.hold=!sleep 30 & sleep 30", "hold")
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Errorf("gitRun returned after %s: the child holding git's output held it past the timeout", elapsed)
	}
	if err == nil || !strings.Contains(err.Error(), "signal: killed") {
		t.Errorf("gitRun = %v, want git killed by the timeout", err)
	}
}
