package ship

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/planwerk/planwerk-agent/internal/github"
)

// foreignRepo is the second repository these tests place Sub Issues and blockers
// in, held as a constant so the literal is not repeated (goconst).
const foreignRepo = "gadgets"

// localSubRef is the ref ship builds for local Sub Issue #101, asserted often
// enough to be worth naming (goconst).
const localSubRef = "acme/widgets#101"

// foreignSub builds a Sub Issue that lives outside the Meta Issue's repository,
// the shape GitHub returns once a Meta Issue has a sub-issue in another repo.
func foreignSub(number int, state string) github.Issue {
	return github.Issue{
		Owner:  "acme",
		Name:   foreignRepo,
		Number: number,
		Title:  fmt.Sprintf("Counterpart %d", number),
		State:  state,
	}
}

// runShip drives one ship run against the fakes and fails the test on error.
func runShip(t *testing.T, gh *fakeGitHub, rec *recorder, opts Options) string {
	t.Helper()
	var out bytes.Buffer
	if err := newRunner(gh, rec).Run(&out, opts); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	return out.String()
}

// ship resolves the clone, the issue, the pull request and the merge against one
// repository, so a Sub Issue in another repository is reported rather than
// driven. Driving it would implement the Meta Issue's repo against a plan
// written for a different codebase.
func TestRun_ForeignSubIssueIsNotDriven(t *testing.T) {
	gh := &fakeGitHub{
		meta:      github.Issue{Title: "Meta", State: "open"},
		children:  []github.Issue{sub(101, "open"), foreignSub(102, "open")},
		linkedPRs: map[int][]github.LinkedPR{101: openPR(201)},
	}
	rec := &recorder{}
	out := runShip(t, gh, rec, baseOpts())

	if len(rec.implementCalls) != 1 || rec.implementCalls[0] != localSubRef {
		t.Errorf("implementCalls = %v, want only %s", rec.implementCalls, localSubRef)
	}
	for _, call := range rec.implementCalls {
		if bytes.Contains([]byte(call), []byte(foreignRepo)) {
			t.Errorf("implement was called for a foreign Sub Issue: %q", call)
		}
	}
	if len(gh.merged) != 1 || gh.merged[0].number != 201 {
		t.Errorf("merged = %v, want only PR 201", gh.merged)
	}
	// The foreign Sub Issue is undelivered work, so the Meta Issue stays open.
	if len(gh.closed) != 0 {
		t.Errorf("closed = %v, want the Meta Issue left open while a foreign Sub Issue is undelivered", gh.closed)
	}
	if !bytes.Contains([]byte(out), []byte("acme/gadgets#102")) {
		t.Errorf("run output does not name the undriven Sub Issue:\n%s", out)
	}
	if !contains(gh.comments, "acme/gadgets#102") {
		t.Errorf("summary comment does not name the undriven Sub Issue: %v", gh.comments)
	}
}

// Issue numbers are unique per repository, so a Meta Issue's local and foreign
// Sub Issues can share one. The foreign one must still not be driven.
func TestRun_ForeignSubIssueWithCollidingNumberIsNotDriven(t *testing.T) {
	gh := &fakeGitHub{
		meta:      github.Issue{Title: "Meta", State: "open"},
		children:  []github.Issue{sub(101, "open"), foreignSub(101, "open")},
		linkedPRs: map[int][]github.LinkedPR{101: openPR(201)},
	}
	rec := &recorder{}
	runShip(t, gh, rec, baseOpts())

	if len(rec.implementCalls) != 1 || rec.implementCalls[0] != localSubRef {
		t.Errorf("implementCalls = %v, want exactly one call for %s", rec.implementCalls, localSubRef)
	}
}

// The sharpest cross-repo failure: findOpenPR re-reads the relations after the
// partition is gone, matching children by number alone. A foreign child sharing
// the number would contribute ITS pull request, which ship would then undraft
// and merge in the Meta Issue's repository — an unrelated PR, merged. The
// foreign child is listed first so it would win the loop without the repo check.
func TestRun_ForeignChildWithCollidingNumberDoesNotSupplyThePR(t *testing.T) {
	gh := &fakeGitHub{
		meta:             github.Issue{Title: "Meta", State: "open"},
		children:         []github.Issue{foreignSub(101, "open"), sub(101, "open")},
		linkedPRs:        map[int][]github.LinkedPR{101: openPR(201)},
		foreignLinkedPRs: map[int][]github.LinkedPR{101: openPR(999)},
	}
	rec := &recorder{}
	runShip(t, gh, rec, baseOpts())

	if len(gh.readied) != 1 || gh.readied[0] != 201 {
		t.Errorf("readied = %v, want only PR 201; PR 999 lives in another repository", gh.readied)
	}
	if len(gh.merged) != 1 || gh.merged[0].number != 201 {
		t.Errorf("merged = %v, want only PR 201; merging 999 would merge an unrelated pull request", gh.merged)
	}
}

// Schedule matches blockers to siblings by bare issue number. A blocker in
// another repository that happens to share a sibling's number would invent an
// ordering edge between two unrelated issues, so foreign blockers are dropped
// before the number ever reaches the edge map. Here #101 is "blocked by"
// acme/gadgets#102; without the filter, local #102 would be ordered first.
func TestRun_ForeignBlockerDoesNotInventAnOrderingEdge(t *testing.T) {
	gh := &fakeGitHub{
		meta:      github.Issue{Title: "Meta", State: "open"},
		children:  []github.Issue{sub(101, "open"), sub(102, "open")},
		blockedBy: map[int][]github.Issue{101: {foreignSub(102, "closed")}},
		linkedPRs: map[int][]github.LinkedPR{101: openPR(201), 102: openPR(202)},
	}
	rec := &recorder{}
	runShip(t, gh, rec, baseOpts())

	want := []string{localSubRef, "acme/widgets#102"}
	if len(rec.implementCalls) != len(want) {
		t.Fatalf("implementCalls = %v, want %v", rec.implementCalls, want)
	}
	for i, w := range want {
		if rec.implementCalls[i] != w {
			t.Fatalf("implementCalls = %v, want %v (a foreign blocker must not reorder the schedule)", rec.implementCalls, want)
		}
	}
}

// ship cannot deliver a blocker in another repository, so a Sub Issue waiting on
// an open one is skipped rather than merged ahead of the work it depends on.
// The skip cascades: #102 is blocked by #101, so it is skipped too.
func TestRun_LocalSubBlockedByOpenForeignIssueIsSkipped(t *testing.T) {
	gh := &fakeGitHub{
		meta:     github.Issue{Title: "Meta", State: "open"},
		children: []github.Issue{sub(101, "open"), sub(102, "open")},
		blockedBy: map[int][]github.Issue{
			101: {foreignSub(77, "open")},
			102: {sub(101, "open")},
		},
		linkedPRs: map[int][]github.LinkedPR{101: openPR(201), 102: openPR(202)},
	}
	rec := &recorder{}
	runShip(t, gh, rec, baseOpts())

	if len(rec.implementCalls) != 0 {
		t.Errorf("implementCalls = %v, want none: #101 waits on an open foreign blocker and #102 waits on #101", rec.implementCalls)
	}
	if !contains(gh.comments, "acme/gadgets#77") {
		t.Errorf("no comment names the foreign blocker: %v", gh.comments)
	}
	if len(gh.closed) != 0 {
		t.Errorf("closed = %v, want the Meta Issue left open", gh.closed)
	}
}

// A closed foreign blocker is satisfied work and must not hold its dependent
// back, or a cross-repo counterpart that already landed would freeze the DAG.
func TestRun_LocalSubBlockedByClosedForeignIssueStillShips(t *testing.T) {
	gh := &fakeGitHub{
		meta:      github.Issue{Title: "Meta", State: "open"},
		children:  []github.Issue{sub(101, "open")},
		blockedBy: map[int][]github.Issue{101: {foreignSub(77, "closed")}},
		linkedPRs: map[int][]github.LinkedPR{101: openPR(201)},
	}
	rec := &recorder{}
	runShip(t, gh, rec, baseOpts())

	if len(rec.implementCalls) != 1 || rec.implementCalls[0] != localSubRef {
		t.Errorf("implementCalls = %v, want %s: a closed foreign blocker blocks nothing", rec.implementCalls, localSubRef)
	}
}

// A foreign Sub Issue already closed in its own repository is delivered work, so
// it does not keep the Meta Issue open once the local Sub Issues have landed.
func TestRun_ClosedForeignSubIssueDoesNotHoldTheMetaOpen(t *testing.T) {
	gh := &fakeGitHub{
		meta:      github.Issue{Title: "Meta", State: "open"},
		children:  []github.Issue{sub(101, "open"), foreignSub(102, "closed")},
		linkedPRs: map[int][]github.LinkedPR{101: openPR(201)},
	}
	rec := &recorder{}
	runShip(t, gh, rec, baseOpts())

	if len(gh.closed) != 1 || gh.closed[0] != 42 {
		t.Errorf("closed = %v, want the Meta Issue #42 closed once every Sub Issue is delivered", gh.closed)
	}
}

// A Meta Issue whose every Sub Issue lives elsewhere has nothing to ship here,
// and must not be closed on the strength of work in a repository ship never
// touched.
func TestRun_AllSubIssuesForeign(t *testing.T) {
	gh := &fakeGitHub{
		meta:     github.Issue{Title: "Meta", State: "open"},
		children: []github.Issue{foreignSub(101, "open")},
	}
	rec := &recorder{}
	out := runShip(t, gh, rec, baseOpts())

	if len(rec.implementCalls) != 0 {
		t.Errorf("implementCalls = %v, want none", rec.implementCalls)
	}
	if len(gh.closed) != 0 {
		t.Errorf("closed = %v, want the Meta Issue left open", gh.closed)
	}
	if !bytes.Contains([]byte(out), []byte("acme/gadgets#101")) {
		t.Errorf("run output does not name the undriven Sub Issue:\n%s", out)
	}
}

// --start-at pointed at a Sub Issue ship does not drive must say why. The
// generic "not a Sub Issue" message is true of the schedule but false of the
// Meta Issue, and would send the author looking for a missing link.
func TestRun_StartAtForeignSubIssueExplainsWhy(t *testing.T) {
	gh := &fakeGitHub{
		meta:     github.Issue{Title: "Meta", State: "open"},
		children: []github.Issue{sub(101, "open"), foreignSub(102, "open")},
	}
	opts := baseOpts()
	opts.StartAt = 102
	var out bytes.Buffer
	err := newRunner(gh, &recorder{}).Run(&out, opts)
	if err == nil {
		t.Fatal("Run() error = nil, want an error naming the repository")
	}
	if !bytes.Contains([]byte(err.Error()), []byte("acme/gadgets")) {
		t.Errorf("error = %q, want it to name acme/gadgets", err)
	}
}

// GitHub owner and repository names are case-insensitive, and the Meta Issue's
// coordinates come from the command line while a node's come from the API. A
// case-sensitive comparison would read every Sub Issue as foreign and silently
// ship nothing.
func TestRun_MetaRepoComparisonFoldsCase(t *testing.T) {
	gh := &fakeGitHub{
		meta:      github.Issue{Title: "Meta", State: "open"},
		children:  []github.Issue{sub(101, "open")},
		linkedPRs: map[int][]github.LinkedPR{101: openPR(201)},
	}
	rec := &recorder{}
	opts := baseOpts()
	opts.IssueRef = "Acme/Widgets#42"
	runShip(t, gh, rec, opts)

	if len(rec.implementCalls) != 1 {
		t.Errorf("implementCalls = %v, want one: a differently-cased ref names the same repository", rec.implementCalls)
	}
}

// Schedule keys its DAG by bare issue number, so it must refuse input it cannot
// key rather than collapse two repositories' issues into one node.
func TestSchedule_RejectsChildrenSpanningRepositories(t *testing.T) {
	_, err := Schedule([]github.Issue{sub(101, "open"), foreignSub(102, "open")}, nil)
	if err == nil {
		t.Fatal("Schedule() error = nil, want an error for Sub Issues spanning repositories")
	}
}

// One level below the colliding-child case: the Sub Issue is local, but a
// closing keyword resolves across repositories ("Closes acme/widgets#101" in a
// PR filed in acme/gadgets), so GitHub reports a linked PR that lives elsewhere.
// findOpenPR's number is applied to THIS repository, so accepting it would mark
// ready and merge whichever local PR happens to carry that number. The foreign
// PR is listed first so it would win the loop without the repo check.
func TestRun_ForeignLinkedPRDoesNotSupplyThePR(t *testing.T) {
	foreign := github.LinkedPR{Owner: "acme", Name: foreignRepo, Number: 999, State: "open", IsDraft: true, Author: shipViewer}
	local := github.LinkedPR{Owner: "acme", Name: "widgets", Number: 201, State: "open", IsDraft: true, Author: shipViewer}
	gh := &fakeGitHub{
		meta:      github.Issue{Title: "Meta", State: "open"},
		children:  []github.Issue{sub(101, "open")},
		linkedPRs: map[int][]github.LinkedPR{101: {foreign, local}},
	}
	rec := &recorder{}
	runShip(t, gh, rec, baseOpts())

	if len(gh.readied) != 1 || gh.readied[0] != 201 {
		t.Errorf("readied = %v, want only PR 201; PR 999 lives in another repository", gh.readied)
	}
	if len(gh.merged) != 1 || gh.merged[0].number != 201 {
		t.Errorf("merged = %v, want only PR 201; merging 999 would merge an unrelated pull request", gh.merged)
	}
}

// A Sub Issue whose only linked PR lives in another repository has nothing this
// run can ship. It must not fall back to the foreign number: that would drive an
// unrelated local PR under this Sub Issue's name.
func TestRun_OnlyForeignLinkedPRShipsNothing(t *testing.T) {
	foreign := github.LinkedPR{Owner: "acme", Name: foreignRepo, Number: 999, State: "open", IsDraft: true, Author: shipViewer}
	gh := &fakeGitHub{
		meta:      github.Issue{Title: "Meta", State: "open"},
		children:  []github.Issue{sub(101, "open")},
		linkedPRs: map[int][]github.LinkedPR{101: {foreign}},
	}
	rec := &recorder{}
	runShip(t, gh, rec, baseOpts())

	if len(gh.readied) != 0 || len(gh.merged) != 0 {
		t.Errorf("readied = %v, merged = %v; want neither, the only linked PR is in another repository", gh.readied, gh.merged)
	}
}

// A deployment that does not report a node's repository leaves the coordinates
// empty. That cannot mean "foreign": such a deployment has no cross-repo
// sub-issues, and treating it as foreign would make ship refuse every Sub Issue.
func TestPartitionByRepo_EmptyCoordinatesAreLocal(t *testing.T) {
	children := []github.Issue{{Number: 101, Title: "Sub 101", State: "open"}}
	local, foreign := partitionByRepo("acme", "widgets", children)
	if len(local) != 1 || len(foreign) != 0 {
		t.Errorf("partitionByRepo() = %d local, %d foreign; want 1 local, 0 foreign", len(local), len(foreign))
	}
}
