package implement

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/planwerk/planwerk-agent/internal/github"
	"github.com/planwerk/planwerk-agent/internal/github/githubtest"
)

// planWithCommits is a plan in the mandated format whose Commit Sequence lists
// three commits.
const planWithCommits = `## Implementation Plan (issue #42)

PLAN_READY — three commits.

### Change Set
1. internal/foo/foo.go — create: the parser
### Commit Sequence
1. Add the foo parser — internal/foo/foo.go; the parser the runner needs
2. Wire the parser into the runner — internal/run/run.go; uses it
3. Document the foo parser — docs/foo.md; user-visible
### Test Plan
1. internal/foo/foo_test.go — the parser's error path
### Status
STATUS: PLAN_READY`

func TestPlannedCommits(t *testing.T) {
	cases := []struct {
		name string
		plan string
		want []string
	}{
		{
			name: "mandated format",
			plan: planWithCommits,
			want: []string{"Add the foo parser", "Wire the parser into the runner", "Document the foo parser"},
		},
		{
			name: "no plan",
			plan: "",
		},
		{
			name: "plan without the section",
			plan: "## Implementation Plan (issue #42)\n\n### Change Set\n1. a.go — create: x\n",
		},
		{
			name: "already implemented",
			plan: "### Commit Sequence\n- none — already implemented at foo.go:12\n### Test Plan\n1. foo_test.go — x\n",
		},
		{
			name: "wrapped subjects and other dashes",
			plan: "### Commit Sequence\n1. **Add foo** — a.go\n2. `Add bar` – b.go\n3. \"Add baz\" -- c.go\n4. Add qux\n",
			want: []string{"Add foo", "Add bar", "Add baz", "Add qux"},
		},
		{
			name: "nested list and fenced block are not commits",
			plan: "### Commit Sequence\n1. Add foo — a.go\n   1. first the type\n   2. then the parser\n```\n7. not a commit\n```\n2. Add bar — b.go\n",
			want: []string{"Add foo", "Add bar"},
		},
		{
			name: "indented plan and a differently cased heading",
			plan: "   ## commit sequence\n   1. Add foo — a.go\n   2. Add bar — b.go\n   ## Test Plan\n   1. foo_test.go — x\n",
			want: []string{"Add foo", "Add bar"},
		},
		{
			name: "a line that starts with an issue reference does not end the section",
			plan: "### Commit Sequence\n1. Add foo — a.go\n#42 asks for it.\n2. Add bar — b.go\n",
			want: []string{"Add foo", "Add bar"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := PlannedCommits(tc.plan); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("PlannedCommits = %q, want %q", got, tc.want)
			}
		})
	}
}

// branch scripts what the watcher's list function returns; a test appends to it
// between polls the way a session commits between ticks.
type branch struct {
	mu      sync.Mutex
	commits []github.BranchCommit
	err     error
}

func (b *branch) list() ([]github.BranchCommit, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]github.BranchCommit(nil), b.commits...), b.err
}

func (b *branch) set(commits ...github.BranchCommit) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.commits = commits
}

// commit builds a branch commit; n keeps author times, and so identities,
// distinct.
func commit(n int, sha, subject, body string) github.BranchCommit {
	return github.BranchCommit{SHA: sha, AuthorTime: int64(1700000000 + n), Subject: subject, Body: body}
}

// watch starts a watcher that never ticks on its own, so a test drives it
// through poll and reads exactly what each poll wrote.
func watch(t *testing.T, b *branch, planned []string) (*commitWatcher, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	cw := startCommitWatcher(&lockedWriter{w: &buf}, b.list, planned, time.Hour)
	t.Cleanup(cw.stop)
	return cw, &buf
}

var threePlanned = []string{"Add the foo parser", "Wire the parser into the runner", "Document the foo parser"}

func TestCommitWatcher_StartsWithTheTotalAndTheFirstPlannedCommit(t *testing.T) {
	_, buf := watch(t, &branch{}, threePlanned)

	want := "\nCommit progress: 0/3 planned commits on the branch.\nNext per plan (1/3): Add the foo parser\n"
	if buf.String() != want {
		t.Errorf("start output = %q, want %q", buf.String(), want)
	}
}

func TestCommitWatcher_StartIsSilentWithoutPlannedCommits(t *testing.T) {
	_, buf := watch(t, &branch{}, nil)

	if buf.Len() != 0 {
		t.Errorf("start output = %q, want none when the plan names no commits", buf.String())
	}
}

func TestCommitWatcher_ReportsANewCommitWithItsMessageAndTheNextOne(t *testing.T) {
	b := &branch{}
	cw, buf := watch(t, b, threePlanned)
	buf.Reset()

	b.set(commit(1, "1a2b3c4d5e6f", "Add the foo parser", "The runner needs it.\n\nSigned-off-by: A <a@example.com>"))
	cw.poll()

	want := "\nCommit 1/3: 1a2b3c4 Add the foo parser\n" +
		"\n    The runner needs it.\n\n    Signed-off-by: A <a@example.com>\n\n" +
		"Next per plan (2/3): Wire the parser into the runner\n"
	if buf.String() != want {
		t.Errorf("poll output = %q, want %q", buf.String(), want)
	}

	buf.Reset()
	cw.poll()
	if buf.Len() != 0 {
		t.Errorf("a second poll over the same branch wrote %q, want nothing", buf.String())
	}
}

func TestCommitWatcher_ReportsEveryCommitOfOneTickAndTheNextOnce(t *testing.T) {
	b := &branch{}
	cw, buf := watch(t, b, threePlanned)
	buf.Reset()

	b.set(commit(1, "aaaaaaa1", "Add the foo parser", ""), commit(2, "bbbbbbb2", "Wire the parser into the runner", ""))
	cw.poll()

	want := "\nCommit 1/3: aaaaaaa Add the foo parser\n" +
		"\nCommit 2/3: bbbbbbb Wire the parser into the runner\n" +
		"Next per plan (3/3): Document the foo parser\n"
	if buf.String() != want {
		t.Errorf("poll output = %q, want %q", buf.String(), want)
	}
}

// A resumed branch already carries commits: they are counted, not reported.
func TestCommitWatcher_CountsTheCommitsAlreadyOnTheBranch(t *testing.T) {
	b := &branch{}
	b.set(commit(1, "aaaaaaa1", "Add the foo parser", ""))
	cw, buf := watch(t, b, threePlanned)

	want := "\nCommit progress: 1/3 planned commits on the branch.\nNext per plan (2/3): Wire the parser into the runner\n"
	if buf.String() != want {
		t.Errorf("start output = %q, want %q", buf.String(), want)
	}

	buf.Reset()
	cw.poll()
	if buf.Len() != 0 {
		t.Errorf("poll reported a commit that was on the branch before the watcher started: %q", buf.String())
	}
}

func TestCommitWatcher_SaysWhenEveryPlannedCommitIsThereAndCountsPastThePlan(t *testing.T) {
	b := &branch{}
	cw, buf := watch(t, b, []string{"Add foo"})
	buf.Reset()

	b.set(commit(1, "aaaaaaa1", "Add foo", ""))
	cw.poll()
	if want := "\nCommit 1/1: aaaaaaa Add foo\nAll 1 planned commits are on the branch.\n"; buf.String() != want {
		t.Errorf("poll output = %q, want %q", buf.String(), want)
	}

	buf.Reset()
	b.set(commit(1, "aaaaaaa1", "Add foo", ""), commit(2, "bbbbbbb2", "Fix what the tests found", ""))
	cw.poll()
	if want := "\nCommit 2 (plan: 1): bbbbbbb Fix what the tests found\n"; buf.String() != want {
		t.Errorf("poll output past the plan = %q, want %q", buf.String(), want)
	}
}

func TestCommitWatcher_NumbersCommitsWithoutATotalWhenThePlanNamesNone(t *testing.T) {
	b := &branch{}
	cw, buf := watch(t, b, nil)

	b.set(commit(1, "aaaaaaa1", "Add foo", ""))
	cw.poll()
	if want := "\nCommit 1: aaaaaaa Add foo\n"; buf.String() != want {
		t.Errorf("poll output = %q, want %q", buf.String(), want)
	}
}

// The simplify and review passes rewrite the branch with an autosquash rebase:
// every commit gets a new SHA and keeps its author date and subject.
func TestCommitWatcher_DoesNotReportARewrittenCommitAgain(t *testing.T) {
	b := &branch{}
	cw, buf := watch(t, b, threePlanned)
	b.set(commit(1, "aaaaaaa1", "Add the foo parser", ""), commit(2, "bbbbbbb2", "Wire the parser into the runner", ""))
	cw.poll()
	buf.Reset()

	b.set(commit(1, "ccccccc3", "Add the foo parser", ""), commit(2, "ddddddd4", "Wire the parser into the runner", ""))
	cw.poll()
	if buf.Len() != 0 {
		t.Errorf("poll reported rewritten commits as new: %q", buf.String())
	}
}

func TestCommitWatcher_ReportsAFixupCommitWithoutAPosition(t *testing.T) {
	b := &branch{}
	cw, buf := watch(t, b, threePlanned)
	b.set(commit(1, "aaaaaaa1", "Add the foo parser", ""))
	cw.poll()
	buf.Reset()

	b.set(commit(1, "aaaaaaa1", "Add the foo parser", ""), commit(2, "bbbbbbb2", "fixup! Add the foo parser", ""))
	cw.poll()
	if want := "\nFixup commit: bbbbbbb fixup! Add the foo parser\n"; buf.String() != want {
		t.Errorf("poll output = %q, want %q", buf.String(), want)
	}

	// The fixup does not take a position from the next real commit.
	buf.Reset()
	b.set(commit(1, "aaaaaaa1", "Add the foo parser", ""), commit(2, "bbbbbbb2", "fixup! Add the foo parser", ""), commit(3, "ccccccc3", "Wire the parser into the runner", ""))
	cw.poll()
	if !strings.HasPrefix(buf.String(), "\nCommit 2/3: ccccccc ") {
		t.Errorf("poll output = %q, want the commit after the fixup counted as 2/3", buf.String())
	}
}

func TestCommitWatcher_SessionDoneReportsTheLastCommitsAndDropsThePlan(t *testing.T) {
	b := &branch{}
	cw, buf := watch(t, b, threePlanned)
	buf.Reset()

	b.set(commit(1, "aaaaaaa1", "Add the foo parser", ""))
	cw.sessionDone()
	if !strings.Contains(buf.String(), "Commit 1/3: aaaaaaa Add the foo parser") {
		t.Errorf("sessionDone did not report the session's last commit: %q", buf.String())
	}

	buf.Reset()
	b.set(commit(1, "aaaaaaa1", "Add the foo parser", ""), commit(2, "bbbbbbb2", "Close the verification gap", ""))
	cw.poll()
	if want := "\nCommit 2: bbbbbbb Close the verification gap\n"; buf.String() != want {
		t.Errorf("poll output after the session = %q, want %q (no plan position, no next commit)", buf.String(), want)
	}
}

func TestCommitWatcher_StopReportsWhatWasCommittedSinceTheLastTick(t *testing.T) {
	b := &branch{}
	var buf bytes.Buffer
	cw := startCommitWatcher(&lockedWriter{w: &buf}, b.list, nil, time.Hour)

	b.set(commit(1, "aaaaaaa1", "Add foo", ""))
	cw.stop()
	if !strings.Contains(buf.String(), "Commit 1: aaaaaaa Add foo") {
		t.Errorf("stop did not report the last commit: %q", buf.String())
	}
}

func TestCommitWatcher_PollsInTheBackground(t *testing.T) {
	b := &branch{}
	var buf bytes.Buffer
	out := &lockedWriter{w: &buf}
	cw := startCommitWatcher(out, b.list, nil, time.Millisecond)
	defer cw.stop()

	b.set(commit(1, "aaaaaaa1", "Add foo", ""))
	deadline := time.Now().Add(5 * time.Second)
	for {
		out.mu.Lock()
		got := buf.String()
		out.mu.Unlock()
		if strings.Contains(got, "Commit 1: aaaaaaa Add foo") {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the watcher did not report the commit on its own; output: %q", got)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestCommitWatcher_AListFailureWritesNothing(t *testing.T) {
	b := &branch{err: errors.New("no origin/HEAD")}
	cw, buf := watch(t, b, nil)

	cw.poll()
	cw.poll()
	if buf.Len() != 0 {
		t.Errorf("a failing list wrote %q to the run's output, want nothing", buf.String())
	}
}

func TestCommitWatcher_NilIsANoOp(t *testing.T) {
	var cw *commitWatcher
	cw.sessionDone()
	cw.stop()
}

// TestRun_ReportsCommitProgress drives a full run whose implement session
// commits twice: the output carries the plan's total up front, each commit with
// its position, and the commits before the implementation report.
func TestRun_ReportsCommitProgress(t *testing.T) {
	gh := &githubtest.Fake{Issue: sampleIssue(), Dir: t.TempDir()}
	b := &branch{}
	gh.CommitsAheadFn = func(string) ([]github.BranchCommit, error) { return b.list() }
	cl := &fakeClaude{report: validImplReport}
	cl.onImplement = func(Context) {
		b.set(commit(1, "aaaaaaa1", "Add the foo parser", "Why it exists."), commit(2, "bbbbbbb2", "Wire the parser into the runner", ""))
	}
	r := newRunner(gh, cl)
	r.Planner = &fakePlanner{plan: planWithCommits}
	r.CommitPollInterval = time.Hour

	var buf bytes.Buffer
	if err := r.Run(&buf, Options{IssueRef: "owner/repo#42"}); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	out := buf.String()
	for _, want := range []string{
		"Commit progress: 0/3 planned commits on the branch.\nNext per plan (1/3): Add the foo parser\n",
		"Commit 1/3: aaaaaaa Add the foo parser\n\n    Why it exists.\n",
		"Commit 2/3: bbbbbbb Wire the parser into the runner\nNext per plan (3/3): Document the foo parser\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output is missing %q:\n%s", want, out)
		}
	}
	if commits, report := strings.Index(out, "Commit 2/3"), strings.Index(out, "Claude implementation report"); commits < 0 || report < commits {
		t.Errorf("the session's commits must be reported before its implementation report:\n%s", out)
	}
	if gh.Calls("CommitsAhead")[0].Args[0] != gh.Dir {
		t.Errorf("the watcher listed %v, want the clone %q", gh.Calls("CommitsAhead")[0].Args, gh.Dir)
	}
}

func TestRun_NoCommitProgressNeverListsTheBranch(t *testing.T) {
	gh := &githubtest.Fake{Issue: sampleIssue(), Dir: t.TempDir()}
	gh.AheadCommits = [][]github.BranchCommit{{commit(1, "aaaaaaa1", "Add the foo parser", "")}}
	cl := &fakeClaude{report: validImplReport}
	r := newRunner(gh, cl)
	r.Planner = &fakePlanner{plan: planWithCommits}

	var buf bytes.Buffer
	if err := r.Run(&buf, Options{IssueRef: "owner/repo#42", NoCommitProgress: true}); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if n := gh.Count("CommitsAhead"); n != 0 {
		t.Errorf("CommitsAhead called %d times under --no-commit-progress, want 0", n)
	}
	if strings.Contains(buf.String(), "Commit progress") {
		t.Errorf("output carries commit progress under --no-commit-progress:\n%s", buf.String())
	}
}

// A branch an earlier run finished implementing has no planned commit left: the
// run skips the implement session and prints no plan position.
func TestRun_CommitProgressHasNoPlanOnAFullyImplementedResume(t *testing.T) {
	gh := &githubtest.Fake{
		Issue:       sampleIssue(),
		Dir:         t.TempDir(),
		ResumeState: &github.ResumeState{Branch: testResumeBranch, Commits: []github.Commit{{SHA: "aaaaaaa1", Subject: "Add the foo parser"}}},
		IssueComments: []github.IssueComment{
			{Body: formatPlanComment(planWithCommits, "")},
			{Body: formatReportComment(validImplReport, "")},
		},
	}
	cl := &fakeClaude{report: validImplReport}
	r := newRunner(gh, cl)
	r.Planner = &fakePlanner{plan: planWithCommits}

	var buf bytes.Buffer
	if err := r.Run(&buf, Options{IssueRef: "owner/repo#42"}); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if cl.called.Load() != 0 {
		t.Fatalf("Claude.Implement called %d times, want 0 on a fully implemented resume", cl.called.Load())
	}
	if strings.Contains(buf.String(), "Commit progress") {
		t.Errorf("output counts against a plan the earlier run finished:\n%s", buf.String())
	}
}
