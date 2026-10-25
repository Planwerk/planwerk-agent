package hygiene

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/planwerk/planwerk-agent/internal/report"
)

func writeChangedFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	path := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func TestVerifySnippets(t *testing.T) {
	dir := t.TempDir()
	writeChangedFile(t, dir, "internal/foo/foo.go", "func Foo() error {\n\treturn db.Exec(query)\n}\n")

	result := &report.ReviewResult{
		Findings: []report.Finding{
			// Present (different indentation than the file → still matches).
			{Title: "real", Confidence: report.ConfidenceVerified, CodeSnippet: "return db.Exec(query)"},
			// Fabricated — not in any changed file.
			{Title: "hallucinated", Confidence: report.ConfidenceVerified, CodeSnippet: "user.DeleteAllRecords()"},
			// No snippet at all → unverifiable.
			{Title: "no-snippet", Confidence: report.ConfidenceLikely, CodeSnippet: ""},
			// Already uncertain → left alone, not counted.
			{Title: "already-uncertain", Confidence: report.ConfidenceUncertain, CodeSnippet: "whatever"},
		},
	}

	demoted := VerifySnippets(result, dir, []string{"internal/foo/foo.go"})
	if demoted != 2 {
		t.Errorf("demoted = %d, want 2 (hallucinated + no-snippet)", demoted)
	}
	want := map[string]report.Confidence{
		"real":              report.ConfidenceVerified,
		"hallucinated":      report.ConfidenceUncertain,
		"no-snippet":        report.ConfidenceUncertain,
		"already-uncertain": report.ConfidenceUncertain,
	}
	// Every examined finding records what the gate decided; the already-uncertain
	// finding is skipped (not examined) and so stays unstamped.
	wantCheck := map[string]string{
		"real":              report.SnippetCheckPassed,
		"hallucinated":      snippetReasonNotFound,
		"no-snippet":        snippetReasonNoQuote,
		"already-uncertain": "",
	}
	for _, f := range result.Findings {
		if f.Confidence != want[f.Title] {
			t.Errorf("%s: confidence = %q, want %q", f.Title, f.Confidence, want[f.Title])
		}
		if f.SnippetCheck != wantCheck[f.Title] {
			t.Errorf("%s: snippet check = %q, want %q", f.Title, f.SnippetCheck, wantCheck[f.Title])
		}
	}
	// The run-level counts must record what the gate examined and demoted, so a
	// clean pass is distinguishable from a skipped gate (Story 1).
	if result.Gates == nil || result.Gates.Snippet == nil {
		t.Fatalf("gate stats not recorded: %+v", result.Gates)
	}
	if got := *result.Gates.Snippet; got != (report.SnippetGateStats{Examined: 3, Demoted: 2}) {
		t.Errorf("snippet stats = %+v, want {Examined:3 Demoted:2}", got)
	}
}

// TestVerifySnippets_DiffMarkers locks the quote-or-demote gate against
// snippets that carry leading +/- diff markers (issue #156, defect 1): a
// finding quoting its snippet verbatim from `git diff` output must pass the
// gate rather than be falsely demoted to uncertain.
func TestVerifySnippets_DiffMarkers(t *testing.T) {
	dir := t.TempDir()
	writeChangedFile(t, dir, "internal/foo/foo.go", "func Foo() error {\n\treturn db.Exec(query)\n}\n")
	writeChangedFile(t, dir, "docs/list.md", "- item one\n- item two\n")
	changed := []string{"internal/foo/foo.go", "docs/list.md"}

	cases := []struct {
		name    string
		snippet string
		want    report.Confidence
	}{
		// Copied straight out of `git diff`: every line carries a leading '+'.
		{"markers on all lines", "+func Foo() error {\n+\treturn db.Exec(query)", report.ConfidenceVerified},
		// Mixed diff context: an unchanged context line plus an added line.
		{"markers on some lines", " func Foo() error {\n+\treturn db.Exec(query)", report.ConfidenceVerified},
		// Pre-existing base case: a plain quote with no markers still matches.
		{"no markers", "return db.Exec(query)", report.ConfidenceVerified},
		// Markers are not a free pass: a fabricated snippet is still demoted.
		{"fabricated with markers", "+user.DeleteAllRecords()", report.ConfidenceUncertain},
		// Genuine leading-dash content (a markdown bullet) quoted verbatim from
		// the file still matches: the single marker is stripped off the needle.
		{"leading-dash markdown bullet", "- item one", report.ConfidenceVerified},
		// An added line whose own content begins with '-' is quoted from the
		// diff with a double marker ('+- item one'). The on-disk file carries the
		// genuine '- item one'; stripping exactly one marker off the needle must
		// leave '- item one' so it still matches. This is the double-marker case
		// the prior single-marker fix missed (issue #156, defect 1).
		{"added line whose content starts with a dash", "+- item one", report.ConfidenceVerified},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := &report.ReviewResult{
				Findings: []report.Finding{
					{Title: tc.name, Confidence: report.ConfidenceVerified, CodeSnippet: tc.snippet},
				},
			}
			VerifySnippets(result, dir, changed)
			if got := result.Findings[0].Confidence; got != tc.want {
				t.Errorf("confidence = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestVerifySnippets_NoGroundTruthSkips(t *testing.T) {
	dir := t.TempDir() // no changed files written
	result := &report.ReviewResult{
		Findings: []report.Finding{
			{Title: "x", Confidence: report.ConfidenceVerified, CodeSnippet: "anything()"},
		},
	}
	// Empty/unreadable change set → gate is skipped, nothing demoted.
	if n := VerifySnippets(result, dir, []string{"missing.go"}); n != 0 {
		t.Errorf("demoted = %d, want 0 when no content can be loaded", n)
	}
	if result.Findings[0].Confidence != report.ConfidenceVerified {
		t.Error("finding must not be demoted when there is no ground truth")
	}
	// A skipped gate records nothing and stamps nothing — a nil Gates.Snippet is
	// how a reader tells "gate skipped" apart from "gate ran and passed all".
	if result.Gates != nil {
		t.Errorf("skipped gate must not record stats, got %+v", result.Gates)
	}
	if result.Findings[0].SnippetCheck != "" {
		t.Errorf("skipped gate must not stamp a finding, got %q", result.Findings[0].SnippetCheck)
	}
}

func TestVerifySnippets_PathEscapeIgnored(t *testing.T) {
	dir := t.TempDir()
	writeChangedFile(t, dir, "ok.go", "safeContent()")
	result := &report.ReviewResult{
		Findings: []report.Finding{{Title: "x", Confidence: report.ConfidenceVerified, CodeSnippet: "safeContent()"}},
	}
	// A path-escaping entry must be skipped without panicking; the in-tree file
	// still provides the haystack so the legitimate finding survives.
	if n := VerifySnippets(result, dir, []string{"../../../etc/passwd", "ok.go"}); n != 0 {
		t.Errorf("demoted = %d, want 0", n)
	}
}

func TestVerifySnippets_NilResult(t *testing.T) {
	// A nil result is a documented no-op; it must not panic.
	if n := VerifySnippets(nil, t.TempDir(), []string{"ok.go"}); n != 0 {
		t.Errorf("demoted = %d, want 0 for a nil result", n)
	}
}

// TestVerifySnippets_MultilineComment locks the comment-insensitive match: a
// finding that quotes a multi-line // comment as continuous prose — without
// reproducing the interior // marker the source carries at each line break —
// must pass rather than be demoted (the false-demotion class that withheld a
// valid "misleading comment" finding). The same comment quoted WITH its markers
// must pass too.
func TestVerifySnippets_MultilineComment(t *testing.T) {
	dir := t.TempDir()
	writeChangedFile(t, dir, "svc/reconciler.go",
		"func sweep() {\n"+
			"\t// timeout is a legal edge from every\n"+
			"\t// non-terminal status, so this never errors\n"+
			"\trun()\n"+
			"}\n")
	result := &report.ReviewResult{
		Findings: []report.Finding{
			// Quoted as flowing prose, no interior // — spans the two comment lines.
			{Title: "prose", Confidence: report.ConfidenceVerified, CodeSnippet: "timeout is a legal edge from every non-terminal status"},
			// The same comment quoted verbatim WITH the // markers.
			{Title: "with-markers", Confidence: report.ConfidenceVerified, CodeSnippet: "// timeout is a legal edge from every\n// non-terminal status"},
			// A comment line that is genuinely NOT in the file is still demoted —
			// the loosening does not turn the gate into a free pass.
			{Title: "fabricated", Confidence: report.ConfidenceVerified, CodeSnippet: "// this comment does not exist anywhere"},
		},
	}
	demoted := VerifySnippets(result, dir, []string{"svc/reconciler.go"})
	if demoted != 1 {
		t.Errorf("demoted = %d, want 1 (only the fabricated comment)", demoted)
	}
	want := map[string]report.Confidence{
		"prose":        report.ConfidenceVerified,
		"with-markers": report.ConfidenceVerified,
		"fabricated":   report.ConfidenceUncertain,
	}
	for _, f := range result.Findings {
		if f.Confidence != want[f.Title] {
			t.Errorf("%s: confidence = %q, want %q", f.Title, f.Confidence, want[f.Title])
		}
	}
}

// TestVerifySnippets_RecoversOutsideDiff locks the checkout-wide fallback: a real
// finding whose quoted code lives in a file the change did NOT modify (the
// cross-file class the changed-files haystack is blind to) must be verified and
// left actionable, not demoted — while a snippet found nowhere in the checkout
// is still demoted.
func TestVerifySnippets_RecoversOutsideDiff(t *testing.T) {
	dir := t.TempDir()
	// The changed file — a new caller added by the diff.
	writeChangedFile(t, dir, "internal/svc/reconciler.go", "func sweep() {\n\tfor range xs {\n\t\texec.Record()\n\t}\n}\n")
	// An UNCHANGED file elsewhere in the checkout holding the quoted evidence.
	writeChangedFile(t, dir, "internal/svc/execution.go", "func (e Execution) Record() {\n\tinvocations := make([]Inv, len(e.invocations))\n\tcopy(invocations, e.invocations)\n}\n")

	result := &report.ReviewResult{
		Findings: []report.Finding{
			// Evidence in the unchanged file → recovered, not demoted.
			{Title: "cross-file", Confidence: report.ConfidenceLikely, CodeSnippet: "invocations := make([]Inv, len(e.invocations))"},
			// Evidence in the changed file → passes on the primary haystack.
			{Title: "in-diff", Confidence: report.ConfidenceLikely, CodeSnippet: "exec.Record()"},
			// Nowhere in the checkout → still demoted as hallucinated.
			{Title: "hallucinated", Confidence: report.ConfidenceLikely, CodeSnippet: "user.DeleteAllRecords()"},
		},
	}

	// Only reconciler.go is in the change set; execution.go is not.
	demoted := VerifySnippets(result, dir, []string{"internal/svc/reconciler.go"})
	if demoted != 1 {
		t.Errorf("demoted = %d, want 1 (only the hallucinated finding)", demoted)
	}
	wantConf := map[string]report.Confidence{
		"cross-file":   report.ConfidenceLikely,    // recovered → confidence untouched
		"in-diff":      report.ConfidenceLikely,    // passed on the diff
		"hallucinated": report.ConfidenceUncertain, // demoted
	}
	wantCheck := map[string]string{
		"cross-file":   snippetPassedOutsideDiff,
		"in-diff":      report.SnippetCheckPassed,
		"hallucinated": snippetReasonNotFound,
	}
	for _, f := range result.Findings {
		if f.Confidence != wantConf[f.Title] {
			t.Errorf("%s: confidence = %q, want %q", f.Title, f.Confidence, wantConf[f.Title])
		}
		if f.SnippetCheck != wantCheck[f.Title] {
			t.Errorf("%s: snippet check = %q, want %q", f.Title, f.SnippetCheck, wantCheck[f.Title])
		}
	}
	// A recovered finding is NOT unverified — it must survive the apply gate.
	for _, f := range result.Findings {
		if f.Title == "cross-file" && f.Unverified() {
			t.Error("cross-file finding recovered from the checkout must not be Unverified")
		}
	}
	if result.Gates == nil || result.Gates.Snippet == nil {
		t.Fatalf("gate stats not recorded: %+v", result.Gates)
	}
	if got := *result.Gates.Snippet; got != (report.SnippetGateStats{Examined: 3, Demoted: 1, RecoveredOutsideDiff: 1}) {
		t.Errorf("snippet stats = %+v, want {Examined:3 Demoted:1 RecoveredOutsideDiff:1}", got)
	}
}

// TestVerifySnippets_PartialLineMatch locks the loosening in #77: a snippet that
// is NOT present as one contiguous block — because the model elided a line,
// reconstructed a signature, or quoted non-adjacent lines together — must still
// pass when one real distinctive line resolves, rather than being buried in the
// Unverified section. A snippet whose every distinctive line is fabricated is
// still demoted, so the looser match is not a free pass.
func TestVerifySnippets_PartialLineMatch(t *testing.T) {
	dir := t.TempDir()
	writeChangedFile(t, dir, "internal/api/verifier.go",
		"func Verify(env Envelope) error {\n"+
			"\tif time.Since(env.Timestamp) > freshnessWindow {\n"+
			"\t\treturn ErrStale\n"+
			"\t}\n"+
			"\treturn verifySignature(env.Signature, env.Payload)\n"+
			"}\n")
	changed := []string{"internal/api/verifier.go"}

	cases := []struct {
		name    string
		snippet string
		want    report.Confidence
	}{
		// Real code with the middle elided by an ellipsis and the signature
		// reconstructed with `...` args: no contiguous block matches, yet a
		// distinctive line (the return) is present, so the finding stays actionable.
		{
			"elided middle line",
			"func Verify(...) error {\n\t// ... omitted ...\n\treturn verifySignature(env.Signature, env.Payload)",
			report.ConfidenceVerified,
		},
		// Two real lines quoted non-adjacently (the reviewer joined them).
		{
			"non-adjacent lines joined",
			"if time.Since(env.Timestamp) > freshnessWindow {\n\treturn verifySignature(env.Signature, env.Payload)",
			report.ConfidenceVerified,
		},
		// Every distinctive line is fabricated → still demoted.
		{
			"fully fabricated block",
			"if db.DropAllTables(ctx) != nil {\n\tuser.PurgeEverything(ctx, opts)\n\treturn wipeDisk()",
			report.ConfidenceUncertain,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := &report.ReviewResult{
				Findings: []report.Finding{
					{Title: tc.name, Confidence: report.ConfidenceVerified, CodeSnippet: tc.snippet},
				},
			}
			VerifySnippets(result, dir, changed)
			if got := result.Findings[0].Confidence; got != tc.want {
				t.Errorf("confidence = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestSnippetInFile pins the whole-snippet match a resolved thread's dismissal
// rests on: whitespace and diff markers aside, every line of the quote must
// still be in the file, in one block. One edited line voids it even where
// VerifySnippets' line-level match would still pass the snippet.
func TestSnippetInFile(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "repo")
	const content = "func flush(f *os.File) error {\n" +
		"\tif err := writeTrailer(f, checksum); err != nil {\n" +
		"\t\treturn fmt.Errorf(\"flushing %s: %w\", f.Name(), err)\n" +
		"\t}\n" +
		"\treturn nil\n" +
		"}\n"
	writeChangedFile(t, dir, "main.go", content)
	writeChangedFile(t, root, "escape.go", content)
	// edited.go is main.go after a push that changed one line of the quote.
	editedContent := strings.Replace(content, "checksum", "digest", 1)
	writeChangedFile(t, dir, "edited.go", editedContent)
	// Lists whose items start at column 0 with the '-' stripDiffMarkers would
	// read as a diff marker.
	writeChangedFile(t, dir, "steps.yml", "- name: Checkout\n  uses: actions/checkout@v4\n- name: Build\n  run: make\n")
	writeChangedFile(t, dir, "README.md", "# Setup\n\n- Install Go 1.26\n- Run make plugin-validate\n+ Optional: run make eval\n")

	const quote = "if err := writeTrailer(f, checksum); err != nil {\n\treturn fmt.Errorf(\"flushing %s: %w\", f.Name(), err)"
	if !located(matchable(editedContent), matchableSnippet(quote), distinctiveSnippetLines(quote)) {
		t.Fatal("the line-level match no longer passes the quote against edited.go, so that case below shows nothing")
	}

	for _, tc := range []struct {
		name, dir, file, snippet string
		want                     bool
	}{
		{name: "the quote as written", dir: dir, file: "main.go", snippet: quote, want: true},
		{name: "different indentation", dir: dir, file: "main.go", snippet: "    if err := writeTrailer(f, checksum); err != nil {\n  return fmt.Errorf(\"flushing %s: %w\", f.Name(), err)", want: true},
		{name: "leading diff markers", dir: dir, file: "main.go", snippet: "+\tif err := writeTrailer(f, checksum); err != nil {\n+\t\treturn fmt.Errorf(\"flushing %s: %w\", f.Name(), err)", want: true},
		{name: "YAML step list quoted verbatim", dir: dir, file: "steps.yml", snippet: "- name: Checkout\n  uses: actions/checkout@v4\n- name: Build", want: true},
		{name: "YAML step list quoted from the diff", dir: dir, file: "steps.yml", snippet: "+- name: Checkout\n+  uses: actions/checkout@v4\n+- name: Build", want: true},
		{name: "Markdown list quoted verbatim", dir: dir, file: "README.md", snippet: "- Install Go 1.26\n- Run make plugin-validate\n+ Optional: run make eval", want: true},
		{name: "YAML step list with a step edited", dir: dir, file: "steps.yml", snippet: "- name: Checkout\n  uses: actions/checkout@v3\n- name: Build"},
		{name: "one line of the quote edited in the file", dir: dir, file: "edited.go", snippet: quote},
		{name: "missing file", dir: dir, file: "gone.go", snippet: quote},
		{name: "path leaving dir", dir: dir, file: "../escape.go", snippet: quote},
		{name: "empty snippet", dir: dir, file: "main.go", snippet: " \n\t"},
		{name: "empty dir", dir: "", file: "main.go", snippet: quote},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := SnippetInFile(tc.dir, tc.file, tc.snippet); got != tc.want {
				t.Errorf("SnippetInFile(%q, %q, %q) = %v, want %v", tc.dir, tc.file, tc.snippet, got, tc.want)
			}
		})
	}
}

// TestVerifySnippetsInCheckout locks the audit's snippet gate: with no diff the
// whole checkout is the ground truth, so a quote from any file passes and a
// quote found nowhere is demoted. A finding that quotes nothing, cites no line,
// and names a path the checkout lacks is the audit's missing-file finding and
// is kept; every other finding that quotes nothing is demoted.
func TestVerifySnippetsInCheckout(t *testing.T) {
	dir := t.TempDir()
	writeChangedFile(t, dir, "internal/foo/foo.go", "func Foo() error {\n\treturn db.Exec(query)\n}\n")
	writeChangedFile(t, dir, "internal/bar/bar.go", "func Bar() { cache.Invalidate(key) }\n")

	result := &report.ReviewResult{
		Findings: []report.Finding{
			// Quoted from the file it cites.
			{Title: "own-file", File: "internal/foo/foo.go", Line: 2, Confidence: report.ConfidenceVerified, CodeSnippet: "return db.Exec(query)"},
			// Quoted from another file in the checkout: real code, so it passes,
			// and an audit never counts it as recovered outside a diff.
			{Title: "other-file", File: "internal/foo/foo.go", Line: 1, Confidence: report.ConfidenceVerified, CodeSnippet: "cache.Invalidate(key)"},
			// Nowhere in the checkout.
			{Title: "hallucinated", File: "internal/foo/foo.go", Line: 2, Confidence: report.ConfidenceLikely, CodeSnippet: "user.DeleteAllRecords()"},
			// The missing-file finding the audit prompt allows.
			{Title: "missing-file", File: "CHANGELOG.md", Confidence: report.ConfidenceLikely},
			// No quote, and the cited file exists: nothing proves the claim.
			{Title: "no-quote-existing-file", File: "internal/foo/foo.go", Confidence: report.ConfidenceLikely},
			// A cited line is a claim about code that should be quoted.
			{Title: "no-quote-with-line", File: "MISSING.md", Line: 3, Confidence: report.ConfidenceLikely},
			// A path that leaves the checkout is not evidence of anything.
			{Title: "no-quote-escaping-path", File: "../outside.md", Confidence: report.ConfidenceLikely},
			// Already uncertain → left alone, not counted.
			{Title: "already-uncertain", File: "CHANGELOG.md", Confidence: report.ConfidenceUncertain},
		},
	}

	if n := VerifySnippetsInCheckout(result, dir); n != 4 {
		t.Errorf("demoted = %d, want 4", n)
	}
	wantConf := map[string]report.Confidence{
		"own-file":               report.ConfidenceVerified,
		"other-file":             report.ConfidenceVerified,
		"hallucinated":           report.ConfidenceUncertain,
		"missing-file":           report.ConfidenceLikely,
		"no-quote-existing-file": report.ConfidenceUncertain,
		"no-quote-with-line":     report.ConfidenceUncertain,
		"no-quote-escaping-path": report.ConfidenceUncertain,
		"already-uncertain":      report.ConfidenceUncertain,
	}
	wantCheck := map[string]string{
		"own-file":               report.SnippetCheckPassed,
		"other-file":             report.SnippetCheckPassed,
		"hallucinated":           snippetReasonNotFound,
		"missing-file":           snippetVerifiedMissingFile,
		"no-quote-existing-file": snippetReasonNoQuote,
		"no-quote-with-line":     snippetReasonNoQuote,
		"no-quote-escaping-path": snippetReasonNoQuote,
		"already-uncertain":      "",
	}
	for _, f := range result.Findings {
		if f.Confidence != wantConf[f.Title] {
			t.Errorf("%s: confidence = %q, want %q", f.Title, f.Confidence, wantConf[f.Title])
		}
		if f.SnippetCheck != wantCheck[f.Title] {
			t.Errorf("%s: snippet check = %q, want %q", f.Title, f.SnippetCheck, wantCheck[f.Title])
		}
	}
	if result.Gates == nil || result.Gates.Snippet == nil {
		t.Fatalf("gate stats not recorded: %+v", result.Gates)
	}
	if got := *result.Gates.Snippet; got != (report.SnippetGateStats{Examined: 7, Demoted: 4}) {
		t.Errorf("snippet stats = %+v, want {Examined:7 Demoted:4}", got)
	}
}

// TestVerifySnippetsInCheckout_CitedFileAlwaysSearched locks the load order:
// the checkout walk skips vendor/ (and stops at its byte budget), so a quote
// from a cited file the walk would never reach must still pass. A finding that
// does not cite that file gets only what the walk reads.
func TestVerifySnippetsInCheckout_CitedFileAlwaysSearched(t *testing.T) {
	dir := t.TempDir()
	writeChangedFile(t, dir, "internal/foo/foo.go", "func Foo() error {\n\treturn db.Exec(query)\n}\n")
	writeChangedFile(t, dir, "vendor/lib/lib.go", "func Vendored() { thirdParty.Call(x) }\n")

	cited := &report.ReviewResult{Findings: []report.Finding{
		{Title: "cites vendor", File: "vendor/lib/lib.go", Line: 1, Confidence: report.ConfidenceLikely, CodeSnippet: "thirdParty.Call(x)"},
	}}
	if n := VerifySnippetsInCheckout(cited, dir); n != 0 {
		t.Errorf("demoted = %d, want 0 for a quote from the cited file", n)
	}
	if got := cited.Findings[0].SnippetCheck; got != report.SnippetCheckPassed {
		t.Errorf("cited vendored file: snippet check = %q, want %q", got, report.SnippetCheckPassed)
	}

	uncited := &report.ReviewResult{Findings: []report.Finding{
		{Title: "cites foo", File: "internal/foo/foo.go", Line: 2, Confidence: report.ConfidenceLikely, CodeSnippet: "thirdParty.Call(x)"},
	}}
	if n := VerifySnippetsInCheckout(uncited, dir); n != 1 {
		t.Errorf("demoted = %d, want 1 when no finding cites the vendored file", n)
	}
	if got := uncited.Findings[0].SnippetCheck; got != snippetReasonNotFound {
		t.Errorf("uncited vendored quote: snippet check = %q, want %q", got, snippetReasonNotFound)
	}
}

// TestVerifySnippetsInCheckout_ReadsOnlyRegularFiles locks the read guard on an
// untrusted checkout: a symlink is never read through, whether a finding cites
// it or only the checkout walk meets it, and a cited file over
// maxHaystackFileBytes is left unread, as the walk leaves it. Each quote exists
// only behind the guard, so a read through it would pass the finding; a link
// to /dev/zero or a pipe would have stalled the gate instead.
func TestVerifySnippetsInCheckout_ReadsOnlyRegularFiles(t *testing.T) {
	const secret = "leaked := readOutsideSecret(token)"
	outside := t.TempDir()
	writeChangedFile(t, outside, "secret.go", "package secret\n"+secret+"\n")
	const oversized = "total := computeOversizedTotal(items)"
	dir := t.TempDir()
	writeChangedFile(t, dir, "a.go", "package a\n")
	writeChangedFile(t, dir, "big.go", strings.Repeat("x", maxHaystackFileBytes)+"\n"+oversized+"\n")
	if err := os.Symlink(filepath.Join(outside, "secret.go"), filepath.Join(dir, "link.go")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	for _, tc := range []struct {
		name string
		f    report.Finding
	}{
		{"cited symlink", report.Finding{File: "link.go", Line: 2, CodeSnippet: secret}},
		{"symlink the walk meets", report.Finding{File: "a.go", Line: 1, CodeSnippet: secret}},
		{"oversized cited file", report.Finding{File: "big.go", Line: 2, CodeSnippet: oversized}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.f.Title, tc.f.Confidence = tc.name, report.ConfidenceLikely
			result := &report.ReviewResult{Findings: []report.Finding{tc.f}}
			if n := VerifySnippetsInCheckout(result, dir); n != 1 {
				t.Errorf("demoted = %d, want 1: the quote is readable only behind the guard", n)
			}
			if got := result.Findings[0].SnippetCheck; got != snippetReasonNotFound {
				t.Errorf("snippet check = %q, want %q", got, snippetReasonNotFound)
			}
		})
	}
}

// TestVerifySnippets_FallbackSkipsSymlinks: the review's checkout fallback
// shares the audit's walk, so a symlink out of the checkout recovers nothing
// there either.
func TestVerifySnippets_FallbackSkipsSymlinks(t *testing.T) {
	const secret = "leaked := readOutsideSecret(token)"
	outside := t.TempDir()
	writeChangedFile(t, outside, "secret.go", "package secret\n"+secret+"\n")
	dir := t.TempDir()
	writeChangedFile(t, dir, "a.go", "package a\n")
	if err := os.Symlink(filepath.Join(outside, "secret.go"), filepath.Join(dir, "link.go")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	result := &report.ReviewResult{Findings: []report.Finding{
		{Title: "outside", Confidence: report.ConfidenceLikely, CodeSnippet: secret},
	}}
	if n := VerifySnippets(result, dir, []string{"a.go"}); n != 1 {
		t.Errorf("demoted = %d, want 1", n)
	}
	if got := result.Findings[0].SnippetCheck; got != snippetReasonNotFound {
		t.Errorf("snippet check = %q, want %q", got, snippetReasonNotFound)
	}
	if result.Gates == nil || result.Gates.Snippet == nil {
		t.Fatalf("gate stats not recorded: %+v", result.Gates)
	}
	if got := *result.Gates.Snippet; got != (report.SnippetGateStats{Examined: 1, Demoted: 1}) {
		t.Errorf("snippet stats = %+v, want {Examined:1 Demoted:1}", got)
	}
}

// TestVerifySnippetsInCheckout_EmptyCheckoutSkips: a checkout with no readable
// text is no ground truth, so the gate demotes nothing, stamps nothing, the
// missing-file rule included, and leaves Gates.Snippet nil.
func TestVerifySnippetsInCheckout_EmptyCheckoutSkips(t *testing.T) {
	result := &report.ReviewResult{
		Findings: []report.Finding{
			{Title: "quote", File: "a.go", Line: 1, Confidence: report.ConfidenceVerified, CodeSnippet: "anything()"},
			{Title: "missing-file", File: "CHANGELOG.md", Confidence: report.ConfidenceLikely},
		},
	}
	if n := VerifySnippetsInCheckout(result, t.TempDir()); n != 0 {
		t.Errorf("demoted = %d, want 0 on an empty checkout", n)
	}
	if result.Gates != nil {
		t.Errorf("skipped gate must not record stats, got %+v", result.Gates)
	}
	for _, f := range result.Findings {
		if f.SnippetCheck != "" {
			t.Errorf("%s: skipped gate must not stamp a finding, got %q", f.Title, f.SnippetCheck)
		}
	}
	if result.Findings[0].Confidence != report.ConfidenceVerified {
		t.Error("finding must not be demoted when there is no ground truth")
	}
}

func TestVerifySnippetsInCheckout_NilResult(t *testing.T) {
	// A nil result is a documented no-op; it must not panic.
	if n := VerifySnippetsInCheckout(nil, t.TempDir()); n != 0 {
		t.Errorf("demoted = %d, want 0 for a nil result", n)
	}
}
