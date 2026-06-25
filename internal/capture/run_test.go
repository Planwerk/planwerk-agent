package capture

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/planwerk/planwerk-review/internal/patterns"
	"github.com/planwerk/planwerk-review/internal/report"
)

// fakeProposer is an offline Proposer: it records the context it was handed and
// returns a configured result/error so a test can assert what flowed into the
// proposal pass without invoking Claude.
type fakeProposer struct {
	calls  int
	ctx    CaptureContext
	result *CaptureResult
	err    error
}

func (f *fakeProposer) Capture(dir string, ctx CaptureContext) (*CaptureResult, error) {
	f.calls++
	f.ctx = ctx
	return f.result, f.err
}

// recordingPoster is a CommentPoster that records the bodies it was asked to post.
type recordingPoster struct {
	bodies []string
	err    error
}

func (p *recordingPoster) post(body string) (string, error) {
	p.bodies = append(p.bodies, body)
	if p.err != nil {
		return "", p.err
	}
	return "https://example/comment", nil
}

func reviewRequest() Request {
	return Request{
		Dir:        "/checkout",
		Command:    "review",
		Repo:       "owner/repo",
		Number:     7,
		BaseBranch: "main",
		Findings: []report.Finding{
			{Title: "raw SQL", Pattern: "", File: "db.go", Problem: "p", Action: "a"},
		},
		Patterns: []patterns.Pattern{{Name: "Some Other Pattern"}},
		Wiki:     patterns.ResolvedWiki{Repo: "owner/repo.wiki", CommitSHA: "abc1234", Dir: nonexistentWikiDir},
		WikiRef:  "main",
		Version:  "test",
	}
}

// nonexistentWikiDir is a placeholder dir; ReadWikiEntries treats a missing wiki
// dir as "no entries" (it skips absent subdirs), so the tests do not need a real
// wiki on disk to exercise the orchestrator.
const nonexistentWikiDir = "/nonexistent-wiki"

func TestPassRun_ProposeOnlyRendersAndComments(t *testing.T) {
	prop := &fakeProposer{result: twoPageResult()}
	poster := &recordingPoster{}
	writer := &fakeWikiWriter{}
	pass := Pass{Propose: prop, PostComment: poster.post, Writer: writer}

	var buf bytes.Buffer
	if err := pass.Run(&buf, reviewRequest()); err != nil {
		t.Fatalf("propose-only Run returned error: %v", err)
	}

	if prop.calls != 1 {
		t.Fatalf("Proposer.Capture called %d times, want 1", prop.calls)
	}
	// The finding's empty Pattern matches no catalog entry, so it survives the
	// CandidateFindings pre-filter and reaches the proposal pass.
	if len(prop.ctx.Findings) != 1 || prop.ctx.Findings[0].File != "db.go" {
		t.Errorf("proposer got findings %+v, want the single candidate finding", prop.ctx.Findings)
	}
	if prop.ctx.RepoName != "owner/repo" || prop.ctx.IssueNumber != 7 {
		t.Errorf("proposer got repo=%q number=%d, want owner/repo / 7", prop.ctx.RepoName, prop.ctx.IssueNumber)
	}
	if !strings.Contains(buf.String(), "Captured knowledge proposals:") {
		t.Errorf("missing the proposals on the writer:\n%s", buf.String())
	}
	if len(poster.bodies) != 1 {
		t.Fatalf("CommentPoster called %d times, want 1", len(poster.bodies))
	}
	if !strings.Contains(poster.bodies[0], commentFooter("review", "")) {
		t.Errorf("comment missing the review attribution footer:\n%s", poster.bodies[0])
	}
	// Propose-only: the write seam must never be touched.
	if writer.cloneCalls != 0 || writer.applyCalls != 0 {
		t.Errorf("propose-only run must not write: clone=%d apply=%d", writer.cloneCalls, writer.applyCalls)
	}
}

func TestPassRun_CaptureWikiPushesAcceptedPages(t *testing.T) {
	prop := &fakeProposer{result: twoPageResult()}
	writer := &fakeWikiWriter{headSHA: "abc1234"}
	pass := Pass{Propose: prop, Writer: writer, In: strings.NewReader(""), IsTTY: neverTTY}

	req := reviewRequest()
	req.CaptureWiki = true
	req.Yes = true // skip the confirmation in a non-interactive test

	var buf bytes.Buffer
	if err := pass.Run(&buf, req); err != nil {
		t.Fatalf("a successful write-back must return nil, got: %v", err)
	}

	if writer.applyCalls != 1 {
		t.Fatalf("ApplyAdditions called %d times, want 1 under --capture-wiki", writer.applyCalls)
	}
	if len(writer.applyFiles) != 2 {
		t.Fatalf("ApplyAdditions got %d files, want 2 (patterns then memory)", len(writer.applyFiles))
	}
	prov := Provenance{Repo: "owner/repo", Issue: 7}
	for _, f := range writer.applyFiles {
		if !strings.HasPrefix(f.Content, prov.Marker()) {
			t.Errorf("written page %q must start with the provenance marker, got:\n%s", f.Path, f.Content)
		}
	}
}

func TestPassRun_NilResultIsNonFatal(t *testing.T) {
	prop := &fakeProposer{result: nil}
	poster := &recordingPoster{}
	writer := &fakeWikiWriter{}
	pass := Pass{Propose: prop, PostComment: poster.post, Writer: writer}

	var buf bytes.Buffer
	if err := pass.Run(&buf, reviewRequest()); err != nil {
		t.Fatalf("a nil result must be non-fatal, got: %v", err)
	}

	if !strings.Contains(buf.String(), "Capture proposed no new review patterns or memory pages.") {
		t.Errorf("missing the no-proposals note for a nil result:\n%s", buf.String())
	}
	if len(poster.bodies) != 0 {
		t.Errorf("a nil result must post no comment, posted %d", len(poster.bodies))
	}
	if writer.applyCalls != 0 {
		t.Errorf("a nil result must not write: apply=%d", writer.applyCalls)
	}
}

func TestPassRun_NoProposalsIsNoop(t *testing.T) {
	prop := &fakeProposer{result: &CaptureResult{}} // HasProposals() == false
	poster := &recordingPoster{}
	pass := Pass{Propose: prop, PostComment: poster.post}

	var buf bytes.Buffer
	req := reviewRequest()
	req.CaptureWiki = true // even with the gate on, no proposals means no write
	if err := pass.Run(&buf, req); err != nil {
		t.Fatalf("no proposals must be non-fatal even under --capture-wiki, got: %v", err)
	}

	if !strings.Contains(buf.String(), "Capture proposed no new review patterns or memory pages.") {
		t.Errorf("missing the no-proposals note:\n%s", buf.String())
	}
	if len(poster.bodies) != 0 {
		t.Errorf("no proposals must post no comment, posted %d", len(poster.bodies))
	}
}

func TestPassRun_ProposeErrorIsNonFatal(t *testing.T) {
	prop := &fakeProposer{err: errors.New("proposal exploded")}
	poster := &recordingPoster{}
	writer := &fakeWikiWriter{}
	pass := Pass{Propose: prop, PostComment: poster.post, Writer: writer}

	var buf bytes.Buffer
	req := reviewRequest()
	req.CaptureWiki = true
	if err := pass.Run(&buf, req); err != nil {
		t.Fatalf("a proposal failure must be non-fatal, got: %v", err)
	}

	if !strings.Contains(buf.String(), "Capture pass could not run") {
		t.Errorf("missing the non-fatal proposal-failure note:\n%s", buf.String())
	}
	if len(poster.bodies) != 0 || writer.applyCalls != 0 {
		t.Errorf("a proposal error must neither comment nor write: comments=%d apply=%d", len(poster.bodies), writer.applyCalls)
	}
}

// TestPassRun_NilPosterStillWrites is the audit shape: no comment poster, but the
// gated write-back still runs and renders without panicking.
func TestPassRun_NilPosterStillWrites(t *testing.T) {
	prop := &fakeProposer{result: twoPageResult()}
	writer := &fakeWikiWriter{headSHA: "abc1234"}
	pass := Pass{Propose: prop, Writer: writer, In: strings.NewReader(""), IsTTY: neverTTY} // PostComment nil

	var buf bytes.Buffer
	req := reviewRequest()
	req.Command = "audit"
	req.Number = 0
	req.BaseBranch = ""
	req.CaptureWiki = true
	req.Yes = true
	if err := pass.Run(&buf, req); err != nil {
		t.Fatalf("nil-poster write-back must succeed, got: %v", err)
	}

	if !strings.Contains(buf.String(), "Captured knowledge proposals:") {
		t.Errorf("nil-poster run must still render the proposals:\n%s", buf.String())
	}
	if writer.applyCalls != 1 {
		t.Errorf("nil-poster run must still write under --capture-wiki: apply=%d", writer.applyCalls)
	}
}

// TestPassRun_CommentErrorIsNonFatal proves a failed comment post does not abort
// the pass — the write-back still runs.
func TestPassRun_CommentErrorIsNonFatal(t *testing.T) {
	prop := &fakeProposer{result: twoPageResult()}
	poster := &recordingPoster{err: errors.New("github down")}
	writer := &fakeWikiWriter{headSHA: "abc1234"}
	pass := Pass{Propose: prop, PostComment: poster.post, Writer: writer, In: strings.NewReader(""), IsTTY: neverTTY}

	var buf bytes.Buffer
	req := reviewRequest()
	req.CaptureWiki = true
	req.Yes = true
	if err := pass.Run(&buf, req); err != nil {
		t.Fatalf("a comment failure must be non-fatal, got: %v", err)
	}

	if !strings.Contains(buf.String(), "Could not post the capture proposals as a comment") {
		t.Errorf("missing the non-fatal comment-failure note:\n%s", buf.String())
	}
	if writer.applyCalls != 1 {
		t.Errorf("a comment failure must not block the write-back: apply=%d", writer.applyCalls)
	}
}

// TestPassRun_CaptureWikiPushFailureIsFatal proves the #4 fix: an explicitly
// requested --capture-wiki push that fails returns a non-nil error (so the
// surrounding command exits non-zero) instead of being swallowed into a green
// no-op, while the failure is still surfaced to the writer.
func TestPassRun_CaptureWikiPushFailureIsFatal(t *testing.T) {
	prop := &fakeProposer{result: twoPageResult()}
	writer := &fakeWikiWriter{headSHA: "abc1234", applyErr: errors.New("push rejected")}
	pass := Pass{Propose: prop, Writer: writer, In: strings.NewReader(""), IsTTY: neverTTY}

	req := reviewRequest()
	req.CaptureWiki = true
	req.Yes = true

	var buf bytes.Buffer
	err := pass.Run(&buf, req)
	if err == nil {
		t.Fatal("a failed --capture-wiki push must return a non-nil error, got nil")
	}
	if !strings.Contains(err.Error(), "push rejected") {
		t.Errorf("error should wrap the push failure, got: %v", err)
	}
	if !strings.Contains(buf.String(), "Capture write-back could not run") {
		t.Errorf("the push failure must still be surfaced to the writer:\n%s", buf.String())
	}
}
