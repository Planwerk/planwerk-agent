package claude

import (
	"errors"
	"strings"
	"testing"

	"github.com/planwerk/planwerk-review/internal/address"
	"github.com/planwerk/planwerk-review/internal/github"
	"github.com/planwerk/planwerk-review/internal/report"
)

func addressTestThreads() []github.ReviewThread {
	return []github.ReviewThread{
		{
			ID:       "RT_1",
			Path:     "internal/foo/bar.go",
			Line:     42,
			DiffHunk: "@@ -40,3 +40,3 @@\n-oldHelper()\n+newHelper()",
			Comments: []github.ReviewThreadComment{
				{Author: "reviewer", Body: "Please rename oldHelper to newHelper."},
				{Author: "author", Body: "Good catch, will do."},
			},
		},
	}
}

func addressTestContext() address.Context {
	return address.Context{
		RepoFullName:       "planwerk/planwerk-review",
		PRNumber:           42,
		PRTitle:            "Add the snapshot tests",
		HeadBranch:         "feat/snapshot-tests",
		BaseBranch:         "main",
		Threads:            addressTestThreads(),
		OneCommitPerThread: true,
	}
}

// TestDecodeAddressResult_Valid exercises the exact decode path Address uses:
// a fenced AddressResult JSON payload decodes without invoking the repair path.
func TestDecodeAddressResult_Valid(t *testing.T) {
	called := false
	restore := repairJSON
	repairJSON = func(*Client, string, error, string) (string, error) {
		called = true
		return "", errors.New("repair must not be called for valid JSON")
	}
	t.Cleanup(func() { repairJSON = restore })

	const payload = "```json\n{\"threads\":[{\"thread_id\":\"RT_1\",\"status\":\"DONE\",\"summary\":\"renamed\",\"files\":[\"internal/foo/bar.go\"]}],\"summary\":\"done\",\"status\":\"DONE\"}\n```"
	var got report.AddressResult
	if err := (&Client{}).decodeJSONWithRepair(payload, "structured address-result", &got); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if called {
		t.Error("repair was called for valid JSON")
	}
	if len(got.Threads) != 1 || got.Threads[0].ThreadID != "RT_1" || got.Status != "DONE" {
		t.Errorf("decoded %+v, want one DONE thread RT_1", got)
	}
}

// TestDecodeAddressResult_Repairs covers the repair fallback: a truncated
// payload is repaired once and then decodes, so a one-character glitch in the
// session's output does not fail the run.
func TestDecodeAddressResult_Repairs(t *testing.T) {
	restore := repairJSON
	repairJSON = func(_ *Client, malformed string, parseErr error, label string) (string, error) {
		if parseErr == nil {
			t.Error("repair should receive the original parse error")
		}
		return `{"threads":null,"summary":"recovered","status":"DONE"}`, nil
	}
	t.Cleanup(func() { repairJSON = restore })

	var got report.AddressResult
	if err := (&Client{}).decodeJSONWithRepair(`{"threads":null,"summary":"recovered","status":"DONE"`, "structured address-result", &got); err != nil {
		t.Fatalf("expected repair to succeed, got: %v", err)
	}
	if got.Summary != "recovered" || got.Status != "DONE" {
		t.Errorf("decoded %+v after repair, want recovered/DONE", got)
	}
}

func TestBuildAddressPrompt_PerThread(t *testing.T) {
	got := BuildAddressPrompt(addressTestContext())

	// The full comment chain must be present, not just the first line.
	if !strings.Contains(got, "Please rename oldHelper to newHelper.") ||
		!strings.Contains(got, "Good catch, will do.") {
		t.Error("prompt is missing the full comment chain")
	}
	// The diff hunk the comment is anchored to must be embedded.
	if !strings.Contains(got, "+newHelper()") {
		t.Error("prompt is missing the anchored diff hunk")
	}
	// The thread id and anchor location must be present.
	if !strings.Contains(got, "Thread RT_1") || !strings.Contains(got, "internal/foo/bar.go:42") {
		t.Error("prompt is missing the thread id / anchor location")
	}
	// The session must commit but not push.
	if !strings.Contains(got, "Do NOT push") {
		t.Error("prompt must instruct the session not to push (orchestrator owns the push)")
	}
	// The structured JSON output shape must be mandated.
	if !strings.Contains(got, `"thread_id"`) || !strings.Contains(got, "DONE|DONE_WITH_CONCERNS|BLOCKED|NEEDS_CONTEXT") {
		t.Error("prompt is missing the structured JSON output shape")
	}
	// Per-thread mode commits one focused follow-up commit.
	if !strings.Contains(got, "ONE focused follow-up commit") {
		t.Error("per-thread prompt should describe one focused commit")
	}
}

func TestBuildAddressPrompt_Aggregate(t *testing.T) {
	ctx := addressTestContext()
	ctx.OneCommitPerThread = false
	ctx.Threads = append(ctx.Threads, github.ReviewThread{
		ID:   "RT_2",
		Path: "internal/foo/baz.go",
		Line: 7,
		Comments: []github.ReviewThreadComment{
			{Author: "reviewer", Body: "Add a guard for the empty case."},
		},
	})
	got := BuildAddressPrompt(ctx)

	if !strings.Contains(got, "ONE aggregate follow-up commit") {
		t.Error("aggregate prompt should describe one aggregate commit")
	}
	if !strings.Contains(got, "Thread RT_1") || !strings.Contains(got, "Thread RT_2") {
		t.Error("aggregate prompt should list every selected thread")
	}
}

// TestFormatAddressThreads_Empty covers the edge case of no threads: the
// renderer emits a placeholder rather than an empty section.
func TestFormatAddressThreads_Empty(t *testing.T) {
	if got := formatAddressThreads(nil); strings.TrimSpace(got) != "(none)" {
		t.Errorf("formatAddressThreads(nil) = %q, want (none)", got)
	}
}

func TestBuildBareAddressPrompt(t *testing.T) {
	got := BuildBareAddressPrompt(address.BareContext{
		RepoFullName: "planwerk/planwerk-review",
		PRNumber:     42,
		TechTags:     []string{"go"},
	})
	// The bare prompt drives a manual session that fetches the threads itself.
	if !strings.Contains(got, "reviewThreads(first: 100)") {
		t.Error("bare prompt should instruct the session to fetch review threads via GraphQL")
	}
	// It pushes follow-up commits with a plain push (no force).
	if !strings.Contains(got, "git push origin HEAD") {
		t.Error("bare prompt should push follow-up commits to the head branch")
	}
	// It must skip resolved threads and the tool's own inline findings.
	if !strings.Contains(got, "isResolved") {
		t.Error("bare prompt should tell the session to skip resolved threads")
	}
}
