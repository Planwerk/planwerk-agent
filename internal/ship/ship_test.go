package ship

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/planwerk/planwerk-agent/internal/github"
)

// shipViewer is the login the fake reports as the authenticated account, and the
// author of the PRs openPR builds — so ship's authorship gate accepts them.
const shipViewer = "planwerk-bot"

// validatedHeadSHA is the head SHA the fake's mergeability snapshot reports; ship
// must pin the merge to it.
const validatedHeadSHA = "deadbeefcafe"

type mergeCall struct {
	number int
	method string
	sha    string
}

// fakeGitHub records every write so tests assert ship's behavior — emitted Meta
// Issue comments, undrafts, and merges — without touching gh or asserting on
// collaborator call order.
type fakeGitHub struct {
	meta     github.Issue
	children []github.Issue // returned as the Meta Issue's Sub Issues
	viewer   string         // authenticated login; defaults to shipViewer when empty

	linkedPRs    map[int][]github.LinkedPR    // sub number -> open linked PRs
	blockedBy    map[int][]github.Issue       // sub number -> blockers
	blockedByErr map[int]error                // sub number -> BlockedByIssues error
	mergeability map[int]*github.Mergeability // PR number -> mergeability
	mergeErr     map[int]error                // PR number -> MergePR error

	relationsCalls int   // counts GetIssueRelations calls
	relationsErr   error // returned by GetIssueRelations on calls after the first (the PR re-read)

	comments []string
	readied  []int
	merged   []mergeCall
	closed   []int
}

func (f *fakeGitHub) GetIssue(owner, name string, number int) (*github.Issue, error) {
	m := f.meta
	m.Owner, m.Name, m.Number = owner, name, number
	return &m, nil
}

func (f *fakeGitHub) GetIssueRelations(owner, name string, number int) (*github.IssueRelations, error) {
	f.relationsCalls++
	if f.relationsCalls > 1 && f.relationsErr != nil {
		return nil, f.relationsErr
	}
	viewer := f.viewer
	if viewer == "" {
		viewer = shipViewer
	}
	kids := make([]github.Issue, 0, len(f.children))
	for _, c := range f.children {
		cc := c
		cc.LinkedPRs = f.linkedPRs[c.Number]
		kids = append(kids, cc)
	}
	return &github.IssueRelations{Viewer: viewer, Children: kids}, nil
}

func (f *fakeGitHub) BlockedByIssues(owner, name string, number int) ([]github.Issue, error) {
	if err := f.blockedByErr[number]; err != nil {
		return nil, err
	}
	return f.blockedBy[number], nil
}

func (f *fakeGitHub) AddIssueComment(owner, name string, number int, body string) (string, error) {
	f.comments = append(f.comments, body)
	return "https://example.test/c", nil
}

func (f *fakeGitHub) MarkPRReady(owner, name string, number int) error {
	f.readied = append(f.readied, number)
	return nil
}

func (f *fakeGitHub) PRMergeability(owner, name string, number int) (*github.Mergeability, error) {
	if m, ok := f.mergeability[number]; ok {
		return m, nil
	}
	return &github.Mergeability{Mergeable: "MERGEABLE", MergeStateStatus: "CLEAN", HeadSHA: validatedHeadSHA}, nil
}

func (f *fakeGitHub) MergePR(owner, name string, number int, method, headSHA string) error {
	if err := f.mergeErr[number]; err != nil {
		return err
	}
	f.merged = append(f.merged, mergeCall{number: number, method: method, sha: headSHA})
	return nil
}

func (f *fakeGitHub) CloseIssue(owner, name string, number int) error {
	f.closed = append(f.closed, number)
	return nil
}

// recorder captures the issue/PR refs the injected implement and fix closures
// were called with, and returns scripted errors keyed by ref.
type recorder struct {
	implementCalls []string
	fixCalls       []string
	implementErr   map[string]error
	fixErr         map[string]error
}

func (rec *recorder) implementFn(w io.Writer, ref string) error {
	rec.implementCalls = append(rec.implementCalls, ref)
	return rec.implementErr[ref]
}

func (rec *recorder) fixFn(w io.Writer, ref string) error {
	rec.fixCalls = append(rec.fixCalls, ref)
	return rec.fixErr[ref]
}

func sub(number int, state string) github.Issue {
	return github.Issue{Owner: "acme", Name: "widgets", Number: number, Title: fmt.Sprintf("Sub %d", number), State: state}
}

func openPR(number int) []github.LinkedPR {
	return []github.LinkedPR{{Number: number, State: "open", IsDraft: true, Author: shipViewer}}
}

func newRunner(gh *fakeGitHub, rec *recorder) *Runner {
	return &Runner{GitHub: gh, Implement: rec.implementFn, Fix: rec.fixFn}
}

func baseOpts() Options {
	return Options{IssueRef: "acme/widgets#42"}
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if strings.Contains(s, needle) {
			return true
		}
	}
	return false
}

func TestRun_GreenCIMerges(t *testing.T) {
	gh := &fakeGitHub{
		meta:      github.Issue{Title: "Meta", State: "open"},
		children:  []github.Issue{sub(101, "open")},
		linkedPRs: map[int][]github.LinkedPR{101: openPR(201)},
	}
	rec := &recorder{}
	if err := newRunner(gh, rec).Run(&bytes.Buffer{}, baseOpts()); err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if len(rec.implementCalls) != 1 || rec.implementCalls[0] != "acme/widgets#101" {
		t.Fatalf("implement calls = %v, want [acme/widgets#101]", rec.implementCalls)
	}
	if len(gh.readied) != 1 || gh.readied[0] != 201 {
		t.Fatalf("readied = %v, want [201]", gh.readied)
	}
	if len(gh.merged) != 1 || gh.merged[0] != (mergeCall{201, "rebase", validatedHeadSHA}) {
		t.Fatalf("merged = %v, want [{201 rebase %s}]", gh.merged, validatedHeadSHA)
	}
	if len(gh.closed) != 1 || gh.closed[0] != 42 {
		t.Fatalf("closed = %v, want [42] (all delivered closes the meta)", gh.closed)
	}
}

func TestRun_RedCISkips(t *testing.T) {
	// fix.Run could not make CI green: the Sub Issue is skipped, not merged.
	gh := &fakeGitHub{
		meta:      github.Issue{Title: "Meta", State: "open"},
		children:  []github.Issue{sub(101, "open")},
		linkedPRs: map[int][]github.LinkedPR{101: openPR(201)},
	}
	rec := &recorder{fixErr: map[string]error{"acme/widgets#201": errors.New("max iterations")}}
	if err := newRunner(gh, rec).Run(&bytes.Buffer{}, baseOpts()); err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if len(gh.merged) != 0 {
		t.Fatalf("merged = %v, want none (red CI must not merge)", gh.merged)
	}
	if len(gh.closed) != 0 {
		t.Fatalf("closed = %v, want none (a skipped sub leaves the meta open)", gh.closed)
	}
	if !contains(gh.comments, "did not go green") {
		t.Fatalf("expected a skip comment about CI, got %v", gh.comments)
	}
}

func TestRun_BlockedImplementSkipsTransitively(t *testing.T) {
	// 101 (blocker) fails to implement; 102 is blocked by 101; 103 is independent.
	// Expect 101 skipped, 102 skipped without even attempting implement, 103
	// merged.
	gh := &fakeGitHub{
		meta:     github.Issue{Title: "Meta", State: "open"},
		children: []github.Issue{sub(101, "open"), sub(102, "open"), sub(103, "open")},
		blockedBy: map[int][]github.Issue{
			102: {sub(101, "open")},
		},
		linkedPRs: map[int][]github.LinkedPR{
			101: openPR(201),
			102: openPR(202),
			103: openPR(203),
		},
	}
	rec := &recorder{implementErr: map[string]error{"acme/widgets#101": errors.New("BLOCKED")}}
	if err := newRunner(gh, rec).Run(&bytes.Buffer{}, baseOpts()); err != nil {
		t.Fatalf("Run error: %v", err)
	}
	// 102 must never be implemented: its blocker failed.
	for _, ref := range rec.implementCalls {
		if ref == "acme/widgets#102" {
			t.Fatalf("102 should not be implemented when its blocker failed; calls=%v", rec.implementCalls)
		}
	}
	// Only the independent 103 merged.
	if len(gh.merged) != 1 || gh.merged[0].number != 203 {
		t.Fatalf("merged = %v, want only PR 203", gh.merged)
	}
	if len(gh.closed) != 0 {
		t.Fatalf("closed = %v, want none (not every sub delivered)", gh.closed)
	}
	if !contains(gh.comments, "blocked by #101") {
		t.Fatalf("expected a skip-because-blocked comment, got %v", gh.comments)
	}
}

func TestRun_NoMergeStopsAtGreenCI(t *testing.T) {
	gh := &fakeGitHub{
		meta:      github.Issue{Title: "Meta", State: "open"},
		children:  []github.Issue{sub(101, "open")},
		linkedPRs: map[int][]github.LinkedPR{101: openPR(201)},
	}
	rec := &recorder{}
	opts := baseOpts()
	opts.NoMerge = true
	if err := newRunner(gh, rec).Run(&bytes.Buffer{}, opts); err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if len(rec.fixCalls) != 1 {
		t.Fatalf("fix calls = %v, want the CI loop to still run under --no-merge", rec.fixCalls)
	}
	if len(gh.merged) != 0 {
		t.Fatalf("merged = %v, want none under --no-merge", gh.merged)
	}
	if len(gh.closed) != 0 {
		t.Fatalf("closed = %v, want none under --no-merge", gh.closed)
	}
}

func TestRun_DryRunCallsNothing(t *testing.T) {
	gh := &fakeGitHub{
		meta:      github.Issue{Title: "Meta", State: "open"},
		children:  []github.Issue{sub(101, "open"), sub(102, "open")},
		blockedBy: map[int][]github.Issue{102: {sub(101, "open")}},
	}
	rec := &recorder{}
	opts := baseOpts()
	opts.DryRun = true
	var out bytes.Buffer
	if err := newRunner(gh, rec).Run(&out, opts); err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if len(rec.implementCalls) != 0 || len(rec.fixCalls) != 0 {
		t.Fatalf("dry-run invoked implement/fix: implement=%v fix=%v", rec.implementCalls, rec.fixCalls)
	}
	if len(gh.merged) != 0 || len(gh.comments) != 0 {
		t.Fatalf("dry-run wrote to GitHub: merged=%v comments=%d", gh.merged, len(gh.comments))
	}
	if !strings.Contains(out.String(), "[dry-run] ship plan") {
		t.Fatalf("dry-run should print the plan, got:\n%s", out.String())
	}
}

func TestRun_AlreadyMergedSubIssueResumes(t *testing.T) {
	// 101 is already closed (delivered on a previous run); 102 depends on it.
	// 101 must be skipped without implementing, and 102 must still proceed.
	gh := &fakeGitHub{
		meta:      github.Issue{Title: "Meta", State: "open"},
		children:  []github.Issue{sub(101, "closed"), sub(102, "open")},
		blockedBy: map[int][]github.Issue{102: {sub(101, "closed")}},
		linkedPRs: map[int][]github.LinkedPR{102: openPR(202)},
	}
	rec := &recorder{}
	if err := newRunner(gh, rec).Run(&bytes.Buffer{}, baseOpts()); err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if contains(rec.implementCalls, "acme/widgets#101") {
		t.Fatalf("an already-closed sub must not be re-implemented; calls=%v", rec.implementCalls)
	}
	if len(rec.implementCalls) != 1 || rec.implementCalls[0] != "acme/widgets#102" {
		t.Fatalf("implement calls = %v, want only 102", rec.implementCalls)
	}
	if len(gh.merged) != 1 || gh.merged[0].number != 202 {
		t.Fatalf("merged = %v, want PR 202", gh.merged)
	}
}

func TestRun_UnmergeablePRSkipped(t *testing.T) {
	// CI is green but the PR is blocked by branch protection: ship must skip it,
	// never force the merge.
	gh := &fakeGitHub{
		meta:      github.Issue{Title: "Meta", State: "open"},
		children:  []github.Issue{sub(101, "open")},
		linkedPRs: map[int][]github.LinkedPR{101: openPR(201)},
		mergeability: map[int]*github.Mergeability{
			201: {Mergeable: "MERGEABLE", MergeStateStatus: "BLOCKED", ReviewDecision: "REVIEW_REQUIRED"},
		},
	}
	rec := &recorder{}
	if err := newRunner(gh, rec).Run(&bytes.Buffer{}, baseOpts()); err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if len(gh.merged) != 0 {
		t.Fatalf("merged = %v, want none (unmergeable PR must not be force-merged)", gh.merged)
	}
	if !contains(gh.comments, "not mergeable") {
		t.Fatalf("expected a not-mergeable skip comment, got %v", gh.comments)
	}
}

func TestRun_NothingToShipCountsAsDelivered(t *testing.T) {
	// implement opened no PR (empty change set). The sub is treated as delivered
	// and its dependent still ships.
	gh := &fakeGitHub{
		meta:      github.Issue{Title: "Meta", State: "open"},
		children:  []github.Issue{sub(101, "open"), sub(102, "open")},
		blockedBy: map[int][]github.Issue{102: {sub(101, "open")}},
		linkedPRs: map[int][]github.LinkedPR{102: openPR(202)}, // 101 has no PR
	}
	rec := &recorder{}
	if err := newRunner(gh, rec).Run(&bytes.Buffer{}, baseOpts()); err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if len(gh.merged) != 1 || gh.merged[0].number != 202 {
		t.Fatalf("merged = %v, want PR 202 (dependent ships once blocker is delivered)", gh.merged)
	}
	if len(gh.closed) != 1 {
		t.Fatalf("closed = %v, want the meta closed (all delivered)", gh.closed)
	}
}

func TestRun_NoSubIssues(t *testing.T) {
	gh := &fakeGitHub{meta: github.Issue{Title: "Meta", State: "open"}}
	rec := &recorder{}
	var out bytes.Buffer
	if err := newRunner(gh, rec).Run(&out, baseOpts()); err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if !strings.Contains(out.String(), "no Sub Issues") {
		t.Fatalf("expected a no-sub-issues notice, got:\n%s", out.String())
	}
}

func TestRun_StartAtNotASubIssue(t *testing.T) {
	gh := &fakeGitHub{
		meta:     github.Issue{Title: "Meta", State: "open"},
		children: []github.Issue{sub(101, "open")},
	}
	rec := &recorder{}
	opts := baseOpts()
	opts.StartAt = 999
	err := newRunner(gh, rec).Run(&bytes.Buffer{}, opts)
	if err == nil || !strings.Contains(err.Error(), "--start-at") {
		t.Fatalf("expected a --start-at validation error, got %v", err)
	}
}

func TestRun_InvalidMergeMethod(t *testing.T) {
	gh := &fakeGitHub{meta: github.Issue{Title: "Meta", State: "open"}}
	rec := &recorder{}
	opts := baseOpts()
	opts.MergeMethod = "fast-forward"
	if err := newRunner(gh, rec).Run(&bytes.Buffer{}, opts); err == nil {
		t.Fatalf("expected an unknown-merge-method error")
	}
}

func TestRun_OnlyViewerAuthoredPRIsShipped(t *testing.T) {
	// The Sub Issue links two open PRs: one an attacker opened by writing
	// "Closes #101" in a fork PR (listed first), and the one the authenticated
	// account opened. ship must ignore the attacker's PR and ship only its own —
	// never undraft or merge a PR it did not author.
	gh := &fakeGitHub{
		meta:     github.Issue{Title: "Meta", State: "open"},
		children: []github.Issue{sub(101, "open")},
		linkedPRs: map[int][]github.LinkedPR{
			101: {
				{Number: 201, State: "open", IsDraft: false, Author: "mallory"},
				{Number: 202, State: "open", IsDraft: true, Author: shipViewer},
			},
		},
	}
	rec := &recorder{}
	if err := newRunner(gh, rec).Run(&bytes.Buffer{}, baseOpts()); err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if len(gh.readied) != 1 || gh.readied[0] != 202 {
		t.Fatalf("readied = %v, want only the viewer-authored PR 202", gh.readied)
	}
	if len(gh.merged) != 1 || gh.merged[0].number != 202 {
		t.Fatalf("merged = %v, want only the viewer-authored PR 202 (never the attacker's 201)", gh.merged)
	}
}

func TestRun_MergePinnedToValidatedHeadSHA(t *testing.T) {
	// The merge must be pinned to the head SHA the mergeability check validated,
	// so a commit pushed after CI went green cannot ride in.
	gh := &fakeGitHub{
		meta:      github.Issue{Title: "Meta", State: "open"},
		children:  []github.Issue{sub(101, "open")},
		linkedPRs: map[int][]github.LinkedPR{101: openPR(201)},
	}
	rec := &recorder{}
	if err := newRunner(gh, rec).Run(&bytes.Buffer{}, baseOpts()); err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if len(gh.merged) != 1 || gh.merged[0].sha != validatedHeadSHA {
		t.Fatalf("merged = %v, want PR 201 pinned to %s", gh.merged, validatedHeadSHA)
	}
}

func TestRun_RelationsReReadErrorSkips(t *testing.T) {
	// implement ran, but the relations re-read that finds the opened PR hit a
	// transient error. ship must skip the Sub Issue (done=false), not mistake the
	// failure for "nothing to ship" and close the Meta Issue as fully delivered.
	gh := &fakeGitHub{
		meta:         github.Issue{Title: "Meta", State: "open"},
		children:     []github.Issue{sub(101, "open")},
		linkedPRs:    map[int][]github.LinkedPR{101: openPR(201)},
		relationsErr: errors.New("502 Bad Gateway"),
	}
	rec := &recorder{}
	if err := newRunner(gh, rec).Run(&bytes.Buffer{}, baseOpts()); err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if len(gh.merged) != 0 {
		t.Fatalf("merged = %v, want none (a failed PR re-read must not deliver)", gh.merged)
	}
	if len(gh.closed) != 0 {
		t.Fatalf("closed = %v, want none (an un-delivered sub leaves the meta open)", gh.closed)
	}
	if !contains(gh.comments, "could not re-read relations") {
		t.Fatalf("expected a skip comment about the relations re-read, got %v", gh.comments)
	}
}

func TestRun_DependencyReadErrorAborts(t *testing.T) {
	// A transient failure reading a Sub Issue's blocked_by dependencies must abort
	// the run rather than silently scheduling the issue as unblocked and risking a
	// merge ahead of its real blocker.
	gh := &fakeGitHub{
		meta:         github.Issue{Title: "Meta", State: "open"},
		children:     []github.Issue{sub(101, "open"), sub(102, "open")},
		blockedByErr: map[int]error{102: errors.New("502 Bad Gateway")},
	}
	rec := &recorder{}
	err := newRunner(gh, rec).Run(&bytes.Buffer{}, baseOpts())
	if err == nil || !strings.Contains(err.Error(), "dependencies") {
		t.Fatalf("expected a dependency-read error to abort the run, got %v", err)
	}
	if len(rec.implementCalls) != 0 {
		t.Fatalf("implement must not run when scheduling aborted; calls=%v", rec.implementCalls)
	}
	if len(gh.merged) != 0 {
		t.Fatalf("merged = %v, want none (aborted run merges nothing)", gh.merged)
	}
}
