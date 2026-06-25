package capture

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/planwerk/planwerk-review/internal/patterns"
)

// fakeWikiWriter is an offline WikiWriter: it records what Clone and
// ApplyAdditions were called with so a test can assert the write phase rendered
// the accepted pages and cloned the right wiki, without touching git.
type fakeWikiWriter struct {
	cloneCalls   int
	cloneRepo    string
	cloneRef     string
	headSHA      string
	cloneErr     error
	cleanupCalls int

	applyCalls int
	applyDir   string
	applyFiles []patterns.WikiFile
	applyMsg   string
	applyErr   error
}

func (f *fakeWikiWriter) Clone(repo, ref string) (string, string, func(), error) {
	f.cloneCalls++
	f.cloneRepo, f.cloneRef = repo, ref
	if f.cloneErr != nil {
		return "", "", func() {}, f.cloneErr
	}
	return "/tmp/wiki-clone", f.headSHA, func() { f.cleanupCalls++ }, nil
}

func (f *fakeWikiWriter) ApplyAdditions(dir string, files []patterns.WikiFile, msg string) error {
	f.applyCalls++
	f.applyDir, f.applyFiles, f.applyMsg = dir, files, msg
	return f.applyErr
}

func twoPageResult() *CaptureResult {
	return &CaptureResult{
		Patterns: []ProposedPage{
			{Path: "review_patterns/no-raw-sql.md", Kind: KindPattern, Body: "# Review Pattern: No raw SQL\n\nbody"},
		},
		Memory: []ProposedPage{
			{Path: "memory/decision.md", Kind: KindMemory, Body: "A durable decision."},
		},
		WikiRepo:   "owner/repo",
		WikiCommit: "abc1234",
	}
}

func alwaysTTY() bool { return true }
func neverTTY() bool  { return false }

func TestWritePhase_RendersAndPushesAcceptedPages(t *testing.T) {
	writer := &fakeWikiWriter{headSHA: "abc1234"}
	prov := Provenance{Repo: "owner/repo", Issue: 42}

	var buf bytes.Buffer
	// yes=true skips confirmation; isTTY is irrelevant on this path.
	if err := WritePhase(&buf, strings.NewReader(""), neverTTY, true, writer, twoPageResult(), prov, "main"); err != nil {
		t.Fatalf("WritePhase: %v", err)
	}
	if writer.cloneCalls != 1 || writer.cloneRepo != "owner/repo" || writer.cloneRef != "main" {
		t.Errorf("Clone calls=%d repo=%q ref=%q, want 1 / owner/repo / main", writer.cloneCalls, writer.cloneRepo, writer.cloneRef)
	}
	if writer.applyCalls != 1 {
		t.Fatalf("ApplyAdditions called %d times, want 1", writer.applyCalls)
	}
	if len(writer.applyFiles) != 2 {
		t.Fatalf("ApplyAdditions got %d files, want 2 (patterns then memory)", len(writer.applyFiles))
	}
	// Pattern first, then memory — AllPages order — each rendered with its marker.
	if writer.applyFiles[0].Path != "review_patterns/no-raw-sql.md" || writer.applyFiles[1].Path != "memory/decision.md" {
		t.Errorf("file order = %q, %q, want pattern then memory", writer.applyFiles[0].Path, writer.applyFiles[1].Path)
	}
	for _, f := range writer.applyFiles {
		if !strings.HasPrefix(f.Content, prov.Marker()) {
			t.Errorf("page %q content must start with the provenance marker, got:\n%s", f.Path, f.Content)
		}
	}
	if writer.cleanupCalls != 1 {
		t.Errorf("clone cleanup ran %d times, want 1 (deferred)", writer.cleanupCalls)
	}
	if !strings.Contains(buf.String(), "Wrote 2 pages and pushed") {
		t.Errorf("missing the write confirmation:\n%s", buf.String())
	}
}

// TestWritePhase_RefusesNonTTYWithoutYes is the non-TTY guard: without --yes and
// with no terminal to prompt, the phase refuses rather than failing open, and
// the writer is never touched.
func TestWritePhase_RefusesNonTTYWithoutYes(t *testing.T) {
	writer := &fakeWikiWriter{}
	var buf bytes.Buffer
	err := WritePhase(&buf, strings.NewReader(""), neverTTY, false, writer, twoPageResult(), Provenance{Repo: "owner/repo", Issue: 42}, "")
	if err == nil || !strings.Contains(err.Error(), "not a TTY") {
		t.Fatalf("WritePhase err = %v, want a non-TTY refusal", err)
	}
	if writer.cloneCalls != 0 || writer.applyCalls != 0 {
		t.Errorf("the wiki must not be touched on a refused non-TTY write: clone=%d apply=%d", writer.cloneCalls, writer.applyCalls)
	}
}

// TestWritePhase_DeclinedConfirmationDoesNotWrite proves an interactive "n"
// aborts cleanly: no clone, no push, no error.
func TestWritePhase_DeclinedConfirmationDoesNotWrite(t *testing.T) {
	writer := &fakeWikiWriter{}
	var buf bytes.Buffer
	if err := WritePhase(&buf, strings.NewReader("n\n"), alwaysTTY, false, writer, twoPageResult(), Provenance{Repo: "owner/repo", Issue: 42}, ""); err != nil {
		t.Fatalf("WritePhase returned %v, want nil on a declined prompt", err)
	}
	if writer.cloneCalls != 0 || writer.applyCalls != 0 {
		t.Errorf("a declined prompt must not write: clone=%d apply=%d", writer.cloneCalls, writer.applyCalls)
	}
	if !strings.Contains(buf.String(), "Aborted") {
		t.Errorf("missing the abort note:\n%s", buf.String())
	}
}

// TestWritePhase_YesConfirmsInteractively proves a "y" line at a real TTY
// confirms the write.
func TestWritePhase_YesConfirmsInteractively(t *testing.T) {
	writer := &fakeWikiWriter{}
	var buf bytes.Buffer
	if err := WritePhase(&buf, strings.NewReader("y\n"), alwaysTTY, false, writer, twoPageResult(), Provenance{Repo: "owner/repo", Issue: 42}, ""); err != nil {
		t.Fatalf("WritePhase returned %v, want nil", err)
	}
	if writer.applyCalls != 1 {
		t.Errorf("ApplyAdditions called %d times, want 1 after a confirmed prompt", writer.applyCalls)
	}
}

// TestWritePhase_NoProposalsIsNoop proves an empty result writes nothing without
// prompting or cloning.
func TestWritePhase_NoProposalsIsNoop(t *testing.T) {
	writer := &fakeWikiWriter{}
	result := &CaptureResult{WikiRepo: "owner/repo"}
	var buf bytes.Buffer
	if err := WritePhase(&buf, strings.NewReader(""), neverTTY, false, writer, result, Provenance{Repo: "owner/repo", Issue: 42}, ""); err != nil {
		t.Fatalf("WritePhase returned %v, want nil for an empty result", err)
	}
	if writer.cloneCalls != 0 || writer.applyCalls != 0 {
		t.Errorf("an empty result must not touch the wiki: clone=%d apply=%d", writer.cloneCalls, writer.applyCalls)
	}
	if !strings.Contains(buf.String(), "Nothing to write") {
		t.Errorf("missing the no-op note:\n%s", buf.String())
	}
}

// TestWritePhase_RejectsUnsafePaths is the path-traversal guard: a model-authored
// path that escapes the wiki root, is absolute or non-canonical, or falls outside
// the review_patterns/ and memory/ allowlist must abort the whole write before the
// wiki is cloned or any page is written — no good page is written alongside it.
func TestWritePhase_RejectsUnsafePaths(t *testing.T) {
	cases := []struct {
		name string
		path string
	}{
		{"parent traversal", "../../../../home/runner/.ssh/authorized_keys"},
		{"absolute", "/etc/cron.d/evil"},
		{"non-canonical", "review_patterns/../../../etc/passwd"},
		{"outside allowlist", "Home.md"},
		{"empty", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			writer := &fakeWikiWriter{}
			result := &CaptureResult{
				Patterns:   []ProposedPage{{Path: tc.path, Kind: KindPattern, Body: "x"}},
				WikiRepo:   "owner/repo",
				WikiCommit: "abc1234",
			}
			var buf bytes.Buffer
			err := WritePhase(&buf, strings.NewReader(""), neverTTY, true, writer, result, Provenance{Repo: "owner/repo", Issue: 42}, "")
			if err == nil {
				t.Fatalf("WritePhase accepted unsafe path %q, want a rejection", tc.path)
			}
			if writer.cloneCalls != 0 || writer.applyCalls != 0 {
				t.Errorf("an unsafe path must abort before touching the wiki: clone=%d apply=%d", writer.cloneCalls, writer.applyCalls)
			}
		})
	}
}

// TestWritePhase_RejectsDuplicatePaths proves two proposed pages targeting the
// same wiki path abort the whole write rather than silently collapsing to one
// committed file (the second os.WriteFile would overwrite the first while the
// operator was told both were written).
func TestWritePhase_RejectsDuplicatePaths(t *testing.T) {
	writer := &fakeWikiWriter{}
	result := &CaptureResult{
		Patterns: []ProposedPage{{Path: "review_patterns/dup.md", Kind: KindPattern, Body: "first"}},
		Memory:   []ProposedPage{{Path: "review_patterns/dup.md", Kind: KindMemory, Body: "second"}},
		WikiRepo: "owner/repo",
	}
	var buf bytes.Buffer
	err := WritePhase(&buf, strings.NewReader(""), neverTTY, true, writer, result, Provenance{Repo: "owner/repo", Issue: 42}, "")
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("WritePhase err = %v, want a duplicate-path rejection", err)
	}
	if writer.applyCalls != 0 {
		t.Errorf("a duplicate path must abort before writing: apply=%d", writer.applyCalls)
	}
}

// TestWritePhase_SkipsUpdatesWhenWikiDiverged proves a wiki that moved since the
// proposal pass does not get its updated pages overwritten from a stale snapshot:
// IsUpdate pages are skipped and reported, while additive new pages are still
// written.
func TestWritePhase_SkipsUpdatesWhenWikiDiverged(t *testing.T) {
	writer := &fakeWikiWriter{headSHA: "def5678"} // != WikiCommit below
	result := &CaptureResult{
		Patterns: []ProposedPage{
			{Path: "review_patterns/new.md", Kind: KindPattern, Body: "new page", IsUpdate: false},
			{Path: "review_patterns/edited.md", Kind: KindPattern, Body: "stale replacement", IsUpdate: true},
		},
		WikiRepo:   "owner/repo",
		WikiCommit: "abc1234",
	}
	var buf bytes.Buffer
	if err := WritePhase(&buf, strings.NewReader(""), neverTTY, true, writer, result, Provenance{Repo: "owner/repo", Issue: 42}, ""); err != nil {
		t.Fatalf("WritePhase: %v", err)
	}
	if writer.applyCalls != 1 {
		t.Fatalf("ApplyAdditions called %d times, want 1", writer.applyCalls)
	}
	if len(writer.applyFiles) != 1 || writer.applyFiles[0].Path != "review_patterns/new.md" {
		t.Errorf("only the new page should be written, got %+v", writer.applyFiles)
	}
	if !strings.Contains(buf.String(), "Skipped review_patterns/edited.md") {
		t.Errorf("the diverged update must be reported as skipped:\n%s", buf.String())
	}
}

// TestWritePhase_AllUpdatesSkippedWhenWikiDiverged proves that when every accepted
// page is an update and the wiki diverged, nothing is pushed — the run degrades to
// a reported no-op rather than clobbering newer edits.
func TestWritePhase_AllUpdatesSkippedWhenWikiDiverged(t *testing.T) {
	writer := &fakeWikiWriter{headSHA: "def5678"}
	result := &CaptureResult{
		Patterns:   []ProposedPage{{Path: "review_patterns/edited.md", Kind: KindPattern, Body: "stale", IsUpdate: true}},
		WikiRepo:   "owner/repo",
		WikiCommit: "abc1234",
	}
	var buf bytes.Buffer
	if err := WritePhase(&buf, strings.NewReader(""), neverTTY, true, writer, result, Provenance{Repo: "owner/repo", Issue: 42}, ""); err != nil {
		t.Fatalf("WritePhase: %v", err)
	}
	if writer.applyCalls != 0 {
		t.Errorf("nothing should be pushed when every page is a clobbering update: apply=%d", writer.applyCalls)
	}
	if !strings.Contains(buf.String(), "Nothing to write") {
		t.Errorf("missing the all-skipped note:\n%s", buf.String())
	}
}

// TestWritePhase_ApplyErrorSurfaces proves a push failure is wrapped and
// returned (the implement caller treats it as non-fatal, but the phase itself
// must surface it rather than swallow it).
func TestWritePhase_ApplyErrorSurfaces(t *testing.T) {
	writer := &fakeWikiWriter{applyErr: errors.New("push rejected")}
	var buf bytes.Buffer
	err := WritePhase(&buf, strings.NewReader(""), neverTTY, true, writer, twoPageResult(), Provenance{Repo: "owner/repo", Issue: 42}, "")
	if err == nil || !strings.Contains(err.Error(), "pushing wiki additions") {
		t.Fatalf("WritePhase err = %v, want a wrapped push error", err)
	}
	if writer.cleanupCalls != 1 {
		t.Errorf("clone cleanup must still run on a push error, ran %d times", writer.cleanupCalls)
	}
}
