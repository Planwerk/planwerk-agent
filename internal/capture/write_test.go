package capture

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/planwerk/planwerk-agent/internal/patterns"
)

// fakeWikiWriter is an offline WikiWriter: it records what Clone and
// ApplyAdditions were called with so a test can assert the write phase rendered
// the accepted pages and cloned the right wiki, without touching git.
type fakeWikiWriter struct {
	cloneCalls int
	cloneRepo  string
	cloneRef   string
	// cloneDir is the clone root Clone reports; empty reports a path below
	// the null device, which can hold no entry on any host, so the clone
	// holds no page.
	cloneDir     string
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
	dir := f.cloneDir
	if dir == "" {
		dir = filepath.Join(os.DevNull, "wiki-clone")
	}
	return dir, f.headSHA, func() { f.cleanupCalls++ }, nil
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
	const want = "refusing to write to the wiki without confirmation: stdin is not a TTY; re-run with --yes to confirm non-interactively"
	if !errors.Is(err, ErrNoTerminal) || err.Error() != want {
		t.Fatalf("WritePhase err = %v, want %q", err, want)
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
	if want := "Note: the wiki moved since these pages were read (abc1234 → def5678); writing new pages and skipping updates"; !strings.Contains(buf.String(), want) {
		t.Errorf("missing %q in:\n%s", want, buf.String())
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

// twoPageRequest is a write of one new and one updated page under --write-wiki.
func twoPageRequest() WriteRequest {
	return WriteRequest{
		Flag:       "--write-wiki",
		WikiRepo:   "owner/repo",
		WikiCommit: "abc1234",
		Pages: []PageWrite{
			{Path: "memory/new.md", Content: "new\n"},
			{Path: "memory/known.md", Content: "known\n", IsUpdate: true},
		},
		CommitMsg: func(written []PageWrite) string { return "Write " + countedPages(len(written)) },
		Yes:       true,
	}
}

// TestWritePages_SkipsNewPageThatExistsInTheClone is the guard against
// overwriting a page the run never read: a new page whose path the fresh clone
// holds is skipped and reported, and the other page is still pushed.
func TestWritePages_SkipsNewPageThatExistsInTheClone(t *testing.T) {
	clone := t.TempDir()
	if err := os.MkdirAll(filepath.Join(clone, "memory"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(clone, "memory", "new.md"), []byte("someone else's page\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writer := &fakeWikiWriter{cloneDir: clone, headSHA: "abc1234"}

	var buf bytes.Buffer
	written, err := WritePages(&buf, strings.NewReader(""), neverTTY, writer, twoPageRequest())
	if err != nil {
		t.Fatalf("WritePages: %v", err)
	}
	if len(written) != 1 || written[0] != "memory/known.md" {
		t.Errorf("written = %v, want only memory/known.md", written)
	}
	if len(writer.applyFiles) != 1 || writer.applyFiles[0].Path != "memory/known.md" {
		t.Errorf("pushed files = %+v, want only memory/known.md", writer.applyFiles)
	}
	if writer.applyMsg != "Write 1 page" {
		t.Errorf("commit message = %q, want it rendered for the one written page", writer.applyMsg)
	}
	if want := "Skipped memory/new.md — it exists on the wiki, and this run did not read it."; !strings.Contains(buf.String(), want) {
		t.Errorf("missing %q in:\n%s", want, buf.String())
	}
}

// TestWritePages_EveryNewPageExistsInTheClone proves a write whose only page
// is skipped by that guard pushes nothing and says so.
func TestWritePages_EveryNewPageExistsInTheClone(t *testing.T) {
	clone := t.TempDir()
	if err := os.MkdirAll(filepath.Join(clone, "memory"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(clone, "memory", "new.md"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writer := &fakeWikiWriter{cloneDir: clone}
	req := twoPageRequest()
	req.Pages = req.Pages[:1]

	var buf bytes.Buffer
	written, err := WritePages(&buf, strings.NewReader(""), neverTTY, writer, req)
	if err != nil || written != nil {
		t.Fatalf("WritePages = %v, %v, want nil, nil", written, err)
	}
	if writer.applyCalls != 0 {
		t.Errorf("ApplyAdditions called %d times, want 0", writer.applyCalls)
	}
	if !strings.Contains(buf.String(), "Nothing to write — every page was skipped.") {
		t.Errorf("missing the all-skipped note:\n%s", buf.String())
	}
}

// TestWritePages_NoPagesIsSilent proves an empty request neither prompts nor
// clones nor prints.
func TestWritePages_NoPagesIsSilent(t *testing.T) {
	writer := &fakeWikiWriter{}
	req := twoPageRequest()
	req.Pages = nil
	req.Yes = false

	var buf bytes.Buffer
	written, err := WritePages(&buf, strings.NewReader(""), neverTTY, writer, req)
	if err != nil || written != nil {
		t.Fatalf("WritePages = %v, %v, want nil, nil", written, err)
	}
	if writer.cloneCalls != 0 || buf.Len() != 0 {
		t.Errorf("an empty request must do nothing: clone=%d output=%q", writer.cloneCalls, buf.String())
	}
}

func TestWritePages_CloneErrorSurfaces(t *testing.T) {
	cloneErr := errors.New("wiki not initialized")
	writer := &fakeWikiWriter{cloneErr: cloneErr}
	var buf bytes.Buffer
	_, err := WritePages(&buf, strings.NewReader(""), neverTTY, writer, twoPageRequest())
	if !errors.Is(err, cloneErr) || !strings.Contains(err.Error(), "cloning wiki for write-back: ") {
		t.Fatalf("WritePages err = %v, want the wrapped clone error", err)
	}
	if writer.applyCalls != 0 {
		t.Errorf("nothing must be pushed after a failed clone: apply=%d", writer.applyCalls)
	}
}

func TestWritePages_PushErrorSurfaces(t *testing.T) {
	pushErr := errors.New("push rejected")
	writer := &fakeWikiWriter{applyErr: pushErr, headSHA: "abc1234"}
	var buf bytes.Buffer
	written, err := WritePages(&buf, strings.NewReader(""), neverTTY, writer, twoPageRequest())
	if !errors.Is(err, pushErr) || !strings.Contains(err.Error(), "pushing wiki additions: ") {
		t.Fatalf("WritePages err = %v, want the wrapped push error", err)
	}
	if written != nil {
		t.Errorf("written = %v, want nil after a failed push", written)
	}
}

// TestWritePages_ListsUnderItsFlag proves the listing names the caller's flag
// and each page's verb before the question, and that a declined prompt writes
// nothing.
func TestWritePages_ListsUnderItsFlag(t *testing.T) {
	writer := &fakeWikiWriter{}
	req := twoPageRequest()
	req.Yes = false

	var buf bytes.Buffer
	written, err := WritePages(&buf, strings.NewReader("n\n"), alwaysTTY, writer, req)
	if err != nil || written != nil {
		t.Fatalf("WritePages = %v, %v, want nil, nil on a declined prompt", written, err)
	}
	out := buf.String()
	listing := "--write-wiki will write 2 pages to the owner/repo wiki:\n  memory/new.md (new)\n  memory/known.md (update)\n"
	question := "Write 2 pages to the owner/repo wiki and push? (y/N): "
	li, qi := strings.Index(out, listing), strings.Index(out, question)
	if li < 0 || qi < 0 || li > qi {
		t.Errorf("want the listing before the question, got:\n%s", out)
	}
	if writer.cloneCalls != 0 {
		t.Errorf("a declined prompt must not clone: clone=%d", writer.cloneCalls)
	}
}

// TestWritePages_RefusesWithoutATerminal proves the refusal names no flag of
// its own: which flag confirms a write is the caller's to say.
func TestWritePages_RefusesWithoutATerminal(t *testing.T) {
	writer := &fakeWikiWriter{}
	req := twoPageRequest()
	req.Yes = false

	var buf bytes.Buffer
	written, err := WritePages(&buf, strings.NewReader(""), neverTTY, writer, req)
	if !errors.Is(err, ErrNoTerminal) || strings.Contains(err.Error(), "--yes") || written != nil {
		t.Fatalf("WritePages = %v, %v, want ErrNoTerminal without a flag hint", written, err)
	}
	if writer.cloneCalls != 0 {
		t.Errorf("a refused write must not clone: clone=%d", writer.cloneCalls)
	}
}

// TestWritePages_RefusesASymlinkedPageDirectory is the guard against a wiki
// that holds one of its page directories as a symbolic link: nothing is
// pushed, and nothing appears where the link points.
func TestWritePages_RefusesASymlinkedPageDirectory(t *testing.T) {
	for _, sub := range []string{"memory", "review_patterns"} {
		t.Run(sub, func(t *testing.T) {
			clone, outside := t.TempDir(), t.TempDir()
			if err := os.Symlink(outside, filepath.Join(clone, sub)); err != nil {
				t.Fatal(err)
			}
			writer := &fakeWikiWriter{cloneDir: clone, headSHA: "abc1234"}
			req := twoPageRequest()
			req.Pages = []PageWrite{{Path: sub + "/new.md", Content: "new\n"}}

			var buf bytes.Buffer
			written, err := WritePages(&buf, strings.NewReader(""), neverTTY, writer, req)
			want := "the owner/repo wiki holds " + sub + " as a symbolic link; refusing to write through it"
			if err == nil || err.Error() != want || written != nil {
				t.Fatalf("WritePages = %v, %v, want %q", written, err, want)
			}
			if writer.applyCalls != 0 {
				t.Errorf("ApplyAdditions called %d times, want 0", writer.applyCalls)
			}
			if writer.cleanupCalls != 1 {
				t.Errorf("clone cleanup ran %d times, want 1", writer.cleanupCalls)
			}
		})
	}
}

// TestWritePhase_RefusesASymlinkedPageDirectory proves the capture route
// carries the same guard: a wiki that holds memory as a symbolic link gets no
// page, and nothing is written where the link points.
func TestWritePhase_RefusesASymlinkedPageDirectory(t *testing.T) {
	clone, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(clone, "memory")); err != nil {
		t.Fatal(err)
	}
	writer := &fakeWikiWriter{cloneDir: clone, headSHA: "abc1234"}

	var buf bytes.Buffer
	err := WritePhase(&buf, strings.NewReader(""), neverTTY, true, writer, twoPageResult(), Provenance{Repo: "owner/repo", Issue: 42}, "")
	if err == nil || !strings.Contains(err.Error(), "holds memory as a symbolic link") {
		t.Fatalf("WritePhase err = %v, want the symbolic link refused", err)
	}
	if writer.applyCalls != 0 {
		t.Errorf("ApplyAdditions called %d times, want 0", writer.applyCalls)
	}
}
