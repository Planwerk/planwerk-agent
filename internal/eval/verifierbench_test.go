package eval

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/planwerk/planwerk-agent/internal/hygiene"
	"github.com/planwerk/planwerk-agent/internal/report"
)

// The second pull request of the bench fixtures, the file their comments
// sit on, and the two categories the bench never draws.
const (
	widget8URL      = "https://github.com/acme/widget/pull/8"
	benchFile       = "store.go"
	catMaintainable = "Maintainability and Readability"
	catPerformance  = "Performance"
)

// benchRow returns an eligible row of widget7URL: a right-side Code Defect
// on lines from-to of benchFile, with label and note.
func benchRow(label, from, to int, note string) AACRRow {
	r := validRow()
	r.Path, r.Label, r.FromLine, r.ToLine, r.Note, r.Category = benchFile, label, from, to, note, aacrCategoryDefect
	return r
}

// benchPool returns n eligible rows of each label, every one with a note of
// its own.
func benchPool(n int) []AACRRow {
	var rows []AACRRow
	for label := range 2 {
		for i := range n {
			rows = append(rows, benchRow(label, i+1, i+1, fmt.Sprintf("comment %d of label %d", i, label)))
		}
	}
	return rows
}

// labelsOf returns the label of each row, in order.
func labelsOf(rows []AACRRow) []int {
	labels := make([]int, len(rows))
	for i, r := range rows {
		labels[i] = r.Label
	}
	return labels
}

func TestSampleVerifierComments(t *testing.T) {
	t.Run("only right-side defects and security claims on a valid range are eligible", func(t *testing.T) {
		defect := benchRow(0, 4, 6, "a race")
		security := benchRow(1, 2, 2, "an injection")
		security.Category = aacrCategorySecurity
		with := func(edit func(*AACRRow)) AACRRow {
			r := benchRow(1, 4, 6, "not drawn")
			edit(&r)
			return r
		}
		rows := []AACRRow{
			with(func(r *AACRRow) { r.Side = aacrLeft }),
			defect,
			with(func(r *AACRRow) { r.FromLine = 0 }),
			with(func(r *AACRRow) { r.FromLine, r.ToLine = 9, 4 }),
			with(func(r *AACRRow) { r.Path = "" }),
			security,
			with(func(r *AACRRow) { r.Label = 2 }),
			with(func(r *AACRRow) { r.Category = catMaintainable }),
			with(func(r *AACRRow) { r.Category = catPerformance }),
		}
		sample, excluded := SampleVerifierComments(rows, 20)
		if !reflect.DeepEqual(sample, []AACRRow{defect, security}) {
			t.Errorf("sample = %+v, want the defect, then the security claim", sample)
		}
		if excluded != 7 {
			t.Errorf("excluded = %d, want 7", excluded)
		}
	})

	t.Run("perLabel caps each label, label 0 first", func(t *testing.T) {
		sample, excluded := SampleVerifierComments(benchPool(6), 3)
		if want := []int{0, 0, 0, 1, 1, 1}; !slices.Equal(labelsOf(sample), want) {
			t.Errorf("labels = %v, want %v", labelsOf(sample), want)
		}
		if excluded != 0 {
			t.Errorf("excluded = %d, want 0", excluded)
		}
	})

	t.Run("the order is the hash order whatever order the rows come in", func(t *testing.T) {
		rows := benchPool(8)
		want, _ := SampleVerifierComments(rows, 5)
		for _, shuffle := range []func([]AACRRow){
			slices.Reverse[[]AACRRow],
			func(r []AACRRow) {
				slices.SortFunc(r, func(a, b AACRRow) int { return strings.Compare(a.Note, b.Note) })
			},
		} {
			shuffled := slices.Clone(rows)
			shuffle(shuffled)
			if got, _ := SampleVerifierComments(shuffled, 5); !reflect.DeepEqual(got, want) {
				t.Errorf("sample of reordered rows = %v, want %v", got, want)
			}
		}
		for _, picks := range [][]AACRRow{want[:5], want[5:]} {
			if !slices.IsSortedFunc(picks, func(a, b AACRRow) int {
				ka, kb := verifierKey(a), verifierKey(b)
				return bytes.Compare(ka[:], kb[:])
			}) {
				t.Errorf("picks %v are not in SHA-256 order", picks)
			}
		}
	})

	t.Run("a larger sample holds the smaller as each label's prefix", func(t *testing.T) {
		rows := benchPool(8)
		small, _ := SampleVerifierComments(rows, 3)
		large, _ := SampleVerifierComments(rows, 6)
		if !reflect.DeepEqual(small[:3], large[:3]) || !reflect.DeepEqual(small[3:], large[6:9]) {
			t.Errorf("sample of 3 = %v, not a per-label prefix of the sample of 6 = %v", small, large)
		}
	})

	t.Run("a pool smaller than perLabel is drawn whole", func(t *testing.T) {
		rows := []AACRRow{benchRow(1, 1, 1, "one"), benchRow(0, 2, 2, "two"), benchRow(0, 3, 3, "three")}
		sample, _ := SampleVerifierComments(rows, 20)
		if want := []int{0, 0, 1}; !slices.Equal(labelsOf(sample), want) {
			t.Errorf("labels = %v, want %v", labelsOf(sample), want)
		}
	})

	t.Run("rows is left as it is", func(t *testing.T) {
		rows := benchPool(4)
		before := slices.Clone(rows)
		SampleVerifierComments(rows, 2)
		if !reflect.DeepEqual(rows, before) {
			t.Error("SampleVerifierComments reordered its input")
		}
	})
}

func TestNoteTitle(t *testing.T) {
	long := strings.Repeat("é", verifierTitleRunes+1)
	exact := strings.Repeat("é", verifierTitleRunes)
	tests := []struct {
		name, note, want string
	}{
		{"the first non-blank line, trimmed", "\n  \n  Nil map write  \nThe map is never made.", "Nil map write"},
		{"a blank note has no title", " \n\t\n", ""},
		{"a line of exactly the limit is kept", exact, exact},
		{"a longer line is cut on a rune boundary with an ellipsis", long, strings.Repeat("é", verifierTitleRunes-1) + "…"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := noteTitle(tt.note)
			if got != tt.want {
				t.Errorf("noteTitle = %q, want %q", got, tt.want)
			}
			if !utf8.ValidString(got) || utf8.RuneCountInString(got) > verifierTitleRunes {
				t.Errorf("noteTitle = %q: %d runes, valid UTF-8 %v", got, utf8.RuneCountInString(got), utf8.ValidString(got))
			}
		})
	}
}

func TestAACRFinding(t *testing.T) {
	c := benchRow(0, 4, 6, "  Nil map write\nThe map is never made.\n")
	c.Category = aacrCategorySecurity
	want := report.Finding{
		Severity:    report.SeverityCritical,
		Title:       "Nil map write",
		File:        benchFile,
		Line:        4,
		LineEnd:     6,
		Problem:     "Nil map write\nThe map is never made.",
		CodeSnippet: "a\nb\nc",
	}
	if got := aacrFinding(c, "a\nb\nc"); !reflect.DeepEqual(got, want) {
		t.Errorf("aacrFinding = %+v, want %+v", got, want)
	}
	// The verifier must judge the claim, not the label.
	c.Label = 1
	if got := aacrFinding(c, "a\nb\nc"); !reflect.DeepEqual(got, want) {
		t.Errorf("aacrFinding of the confirmed twin = %+v, want the same finding %+v", got, want)
	}
}

// pathEscapes is how os.Root refuses a path that leaves it.
const pathEscapes = "path escapes from parent"

func TestReadSnippet(t *testing.T) {
	outer := t.TempDir()
	dir := filepath.Join(outer, "checkout")
	writeFile(t, filepath.Join(dir, "src", "a.go"), "l1\nl2\r\nl3\nl4\n")
	writeFile(t, filepath.Join(dir, "empty.go"), "")
	writeFile(t, filepath.Join(outer, "x"), "outside\n")

	for _, tt := range []struct {
		path     string
		from, to int
		want     string
	}{
		{"src/a.go", 2, 3, "l2\nl3"},
		{"src/a.go", 1, 1, "l1"},
		{"src/a.go", 4, 4, "l4"},
	} {
		got, err := readSnippet(dir, tt.path, tt.from, tt.to)
		if err != nil || got != tt.want {
			t.Errorf("readSnippet(%s, %d-%d) = %q, %v, want %q", tt.path, tt.from, tt.to, got, err, tt.want)
		}
	}

	for _, tt := range []struct {
		name, path string
		from, to   int
		want       string
	}{
		{"past the end", "src/a.go", 4, 5, "lines 4-5 past the end of src/a.go (4 lines)"},
		{"an empty file has no lines", "empty.go", 1, 1, "lines 1-1 past the end of empty.go (0 lines)"},
		{"a path that leaves the checkout", "../x", 1, 1, pathEscapes},
		{"no line 0", "src/a.go", 0, 1, "lines 0-1 of src/a.go are no range"},
		{"a reversed range", "src/a.go", 3, 2, "lines 3-2 of src/a.go are no range"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got, err := readSnippet(dir, tt.path, tt.from, tt.to); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("readSnippet = %q, %v, want an error containing %q", got, err, tt.want)
			}
		})
	}

	t.Run("a missing file", func(t *testing.T) {
		if got, err := readSnippet(dir, "src/b.go", 1, 1); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("readSnippet = %q, %v, want a not-exist error", got, err)
		}
	})

	t.Run("a symlink out of the checkout", func(t *testing.T) {
		if err := os.Symlink(filepath.Join(outer, "x"), filepath.Join(dir, "link.go")); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		if got, err := readSnippet(dir, "link.go", 1, 1); err == nil || !strings.Contains(err.Error(), pathEscapes) {
			t.Errorf("readSnippet through a symlink out of the checkout = %q, %v, want an error containing %q", got, err, pathEscapes)
		}
	})
}

// benchCheckout is a CheckoutFn stub: it writes a pull request's files into a
// fresh directory, fails for a pull request it holds no files for, and records
// the cleanups that ran.
type benchCheckout struct {
	t       *testing.T
	files   map[string]map[string]string // pr_url → path → content
	cleaned []string
}

func (c *benchCheckout) checkout(prURL, commit string) (string, func(), error) {
	files, ok := c.files[prURL]
	if !ok {
		return "", nil, errors.New("no such revision")
	}
	if commit != widget7Head {
		c.t.Errorf("checkout of %s at %s, want the rows' target commit %s", prURL, commit, widget7Head)
	}
	dir := c.t.TempDir()
	for path, content := range files {
		writeFile(c.t, filepath.Join(dir, filepath.FromSlash(path)), content)
	}
	return dir, func() { c.cleaned = append(c.cleaned, prURL) }, nil
}

// benchFixture is the sample TestRunVerifierBench runs: eight comments on
// widget#7, one of them on lines its file does not have, and one on widget#8,
// whose checkout fails. It returns the stub that checks them out.
func benchFixture(t *testing.T) ([]AACRRow, *benchCheckout) {
	var lines strings.Builder
	for i := 1; i <= 10; i++ {
		fmt.Fprintf(&lines, "line %d\n", i)
	}
	other := benchRow(1, 1, 1, "b0")
	other.PRURL = widget8URL
	sample := []AACRRow{
		benchRow(0, 1, 1, "a0"),
		benchRow(0, 2, 2, "a1"),
		benchRow(1, 3, 3, "a2"),
		benchRow(0, 9, 12, "past the end"),
		benchRow(1, 4, 5, "a3"),
		benchRow(1, 5, 5, "a4"),
		benchRow(0, 6, 6, "a5"),
		benchRow(1, 7, 8, "a6"),
		other,
	}
	return sample, &benchCheckout{t: t, files: map[string]map[string]string{widget7URL: {benchFile: lines.String()}}}
}

// rate returns a pointer to v, for comparing a VerdictTally.
func rate(v float64) *float64 { return &v }

func TestRunVerifierBench(t *testing.T) {
	sample, stub := benchFixture(t)
	var batches [][]report.Finding
	verify := func(dir string, findings []report.Finding) ([]hygiene.ClaimVerdict, error) {
		if _, err := os.Stat(filepath.Join(dir, benchFile)); err != nil {
			t.Errorf("verify ran outside the checkout: %v", err)
		}
		batches = append(batches, findings)
		// The same answers for every call; in a call of two, every index
		// past 1 is out of range.
		return []hygiene.ClaimVerdict{
			{Index: 0, Verdict: "refuted", Evidence: "store.go:2", Reason: "guarded above"},
			{Index: 1, Verdict: "unverifiable", Reason: "needs the deployed config"},
			{Index: 3, Verdict: "likely", Evidence: "store.go:4"},
			{Index: 4, Verdict: "refuted", Evidence: "store.go:5"},
			{Index: 4, Verdict: "confirmed"},
			{Index: 9, Verdict: "refuted"},
		}, nil
	}
	var progress strings.Builder
	rep, err := RunVerifierBench(sample, stub.checkout, verify, &progress)
	if err != nil {
		t.Fatalf("RunVerifierBench: %v", err)
	}

	var sizes []int
	for _, b := range batches {
		sizes = append(sizes, len(b))
	}
	if !slices.Equal(sizes, []int{5, 2}) {
		t.Fatalf("batch sizes = %v, want calls of 5 and 2", sizes)
	}
	if want := aacrFinding(sample[4], "line 4\nline 5"); !reflect.DeepEqual(batches[0][3], want) {
		t.Errorf("finding 3 of call 1 = %+v, want %+v", batches[0][3], want)
	}
	for _, b := range batches {
		for _, f := range b {
			if f.Line == 9 || f.Problem == "b0" {
				t.Errorf("a skipped comment reached the verifier: %+v", f)
			}
		}
	}

	// Each sent comment of widget#7 starts on a line of its own.
	type outcome struct {
		line             int
		outcome, verdict string
	}
	var got []outcome
	for _, s := range rep.Samples {
		got = append(got, outcome{s.LineStart, s.Outcome, s.Verdict})
	}
	want := []outcome{
		{1, hygiene.VerdictRefuted, "refuted"},
		{2, hygiene.VerdictUnverifiable, "unverifiable"},
		{3, report.ClaimCheckNoVerdict, ""},
		{4, hygiene.VerdictConfirmed, "likely"},
		{5, hygiene.VerdictRefuted, "refuted"},
		{6, hygiene.VerdictRefuted, "refuted"},
		{7, hygiene.VerdictUnverifiable, "unverifiable"},
	}
	if !slices.Equal(got, want) {
		t.Errorf("outcomes = %v, want %v", got, want)
	}
	if s := rep.Samples[0]; s.Evidence != "store.go:2" || s.Reason != "guarded above" || s.PRURL != widget7URL ||
		s.Commit != widget7Head || s.File != benchFile || s.LineStart != 1 || s.LineEnd != 1 || s.Label != 0 ||
		s.Category != aacrCategoryDefect || s.Context != ctxDiff {
		t.Errorf("sample 0 = %+v, want the comment's place, label and the verifier's evidence and reason", s)
	}
	if s := rep.Samples[4]; s.Evidence != "store.go:5" {
		t.Errorf("sample 4 = %+v, want the refutation's evidence, which the later confirmation does not undo", s)
	}

	wantLabels := []LabelTally{
		{Label: 0, VerdictTally: VerdictTally{Sent: 3, Refuted: 2, Unverifiable: 1, RefuteRate: rate(2.0 / 3), UnverifiableRate: rate(1.0 / 3)}},
		{Label: 1, VerdictTally: VerdictTally{Sent: 4, Confirmed: 1, Refuted: 1, Unverifiable: 1, NoVerdict: 1, RefuteRate: rate(0.25), UnverifiableRate: rate(0.25)}},
	}
	if !reflect.DeepEqual(rep.Labels, wantLabels) {
		t.Errorf("labels = %+v, want %+v", rep.Labels, wantLabels)
	}
	if want := (VerdictTally{Sent: 7, Confirmed: 1, Refuted: 3, Unverifiable: 2, NoVerdict: 1, RefuteRate: rate(3.0 / 7), UnverifiableRate: rate(2.0 / 7)}); !reflect.DeepEqual(rep.All, want) {
		t.Errorf("all = %+v, want %+v", rep.All, want)
	}

	wantSkipped := []SkippedComment{
		{PRURL: widget7URL, File: benchFile, LineStart: 9, LineEnd: 12, Label: 0, Reason: "lines 9-12 past the end of store.go (10 lines)"},
		{PRURL: widget8URL, File: benchFile, LineStart: 1, LineEnd: 1, Label: 1, Reason: "checkout: no such revision"},
	}
	if !reflect.DeepEqual(rep.Skipped, wantSkipped) {
		t.Errorf("skipped = %+v, want %+v", rep.Skipped, wantSkipped)
	}
	if !slices.Equal(stub.cleaned, []string{widget7URL}) {
		t.Errorf("cleanups = %v, want widget#7's alone", stub.cleaned)
	}

	const plan = "verifier bench: 9 comments over 2 pull requests, up to 3 verifier calls\n"
	if !strings.HasPrefix(progress.String(), plan) {
		t.Errorf("progress = %q, want it to begin with %q", progress.String(), plan)
	}
	for _, line := range []string{
		"verifying " + widget7URL + " (5 comments, call 1/3) ...\n",
		"verifying " + widget7URL + " (2 comments, call 2/3) ...\n",
	} {
		if !strings.Contains(progress.String(), line) {
			t.Errorf("progress lacks %q:\n%s", line, progress.String())
		}
	}

	t.Run("a verify error ends the bench after the checkout's cleanup", func(t *testing.T) {
		sample, stub := benchFixture(t)
		timedOut := errors.New("claude timed out")
		_, err := RunVerifierBench(sample, stub.checkout, func(string, []report.Finding) ([]hygiene.ClaimVerdict, error) {
			return nil, timedOut
		}, io.Discard)
		if !errors.Is(err, timedOut) || !strings.Contains(err.Error(), "verifying "+widget7URL+" at "+widget7Head+": ") {
			t.Errorf("err = %v, want the verify error naming the pull request and commit", err)
		}
		if !slices.Equal(stub.cleaned, []string{widget7URL}) {
			t.Errorf("cleanups = %v, want widget#7's", stub.cleaned)
		}
	})

	t.Run("a run that sends nothing is an error naming the first skip", func(t *testing.T) {
		sample, stub := benchFixture(t)
		_, err := RunVerifierBench(sample[len(sample)-1:], stub.checkout, func(string, []report.Finding) ([]hygiene.ClaimVerdict, error) {
			t.Error("verify called for a comment whose checkout failed")
			return nil, nil
		}, io.Discard)
		if err == nil || !strings.Contains(err.Error(), "no AACR-Bench comment reached the verifier") || !strings.Contains(err.Error(), "checkout: no such revision") {
			t.Errorf("err = %v, want the no-comment error with the first skip's reason", err)
		}
	})
}

// TestApplyVerdictsActsAsTheGate feeds the same verdicts for one finding to
// applyVerdicts and to hygiene.VerifyClaims, so the bench's outcome is held to
// the decision the gate takes, whatever order a duplicate verdict comes in.
func TestApplyVerdictsActsAsTheGate(t *testing.T) {
	refuted := hygiene.ClaimVerdict{Verdict: "refuted", Evidence: "store.go:2", Reason: "guarded above"}
	refutedAgain := hygiene.ClaimVerdict{Verdict: "refuted", Reason: "never nil"}
	confirmed := hygiene.ClaimVerdict{Verdict: "confirmed", Evidence: "store.go:4"}
	unverifiable := hygiene.ClaimVerdict{Verdict: "unverifiable", Reason: "needs the deployed config"}
	tests := []struct {
		name     string
		verdicts []hygiene.ClaimVerdict
		want     hygiene.ClaimVerdict // the verdict whose outcome and words the sample keeps
	}{
		{"a confirmation does not undo a refutation", []hygiene.ClaimVerdict{refuted, confirmed}, refuted},
		{"an unverifiable verdict does not undo a refutation", []hygiene.ClaimVerdict{refuted, unverifiable}, refuted},
		{"a refutation overrides a confirmation", []hygiene.ClaimVerdict{confirmed, refuted}, refuted},
		{"the last of two refutations gives the reason", []hygiene.ClaimVerdict{refuted, refutedAgain}, refutedAgain},
		{"an unverifiable verdict does not undo a confirmation", []hygiene.ClaimVerdict{confirmed, unverifiable}, confirmed},
		{"a confirmation overrides an unverifiable verdict", []hygiene.ClaimVerdict{unverifiable, confirmed}, confirmed},
		{"an unverifiable verdict alone", []hygiene.ClaimVerdict{unverifiable}, unverifiable},
		{"no verdict", nil, hygiene.ClaimVerdict{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			samples := make([]VerifierSample, 1)
			applyVerdicts(samples, []int{0}, tt.verdicts)
			wantOutcome := report.ClaimCheckNoVerdict
			if tt.want.Verdict != "" {
				wantOutcome = hygiene.ClassifyVerdict(tt.want.Verdict)
			}
			s := samples[0]
			if s.Outcome != wantOutcome || s.Verdict != tt.want.Verdict || s.Evidence != tt.want.Evidence || s.Reason != tt.want.Reason {
				t.Errorf("sample = %+v, want outcome %s with the words of %+v", s, wantOutcome, tt.want)
			}

			result := &report.ReviewResult{Findings: []report.Finding{{Severity: report.SeverityCritical, Confidence: report.ConfidenceLikely}}}
			hygiene.VerifyClaims(result, t.TempDir(), func(string, []report.Finding) ([]hygiene.ClaimVerdict, error) {
				return tt.verdicts, nil
			})
			f := result.Findings[0]
			// The gate records an unverifiable verdict as no verdict at all.
			gate := report.ClaimCheckNoVerdict
			switch {
			case f.Confidence == report.ConfidenceUncertain:
				gate = hygiene.VerdictRefuted
				if f.VerificationNote != "refuted: "+s.Reason {
					t.Errorf("gate note = %q, want the reason the sample keeps, %q", f.VerificationNote, s.Reason)
				}
			case f.ClaimCheck == report.ClaimCheckConfirmed:
				gate = hygiene.VerdictConfirmed
			}
			bench := s.Outcome
			if bench == hygiene.VerdictUnverifiable {
				bench = report.ClaimCheckNoVerdict
			}
			if bench != gate {
				t.Errorf("bench outcome %s, gate decision %s (finding %+v): the bench must measure the gate's decision", s.Outcome, gate, f)
			}
		})
	}
}

func TestTallyVerdictsUndefinedRates(t *testing.T) {
	labels, all := tallyVerdicts([]VerifierSample{{Label: 1, Outcome: hygiene.VerdictRefuted}})
	if l := labels[0]; l.Label != 0 || l.Sent != 0 || l.RefuteRate != nil || l.UnverifiableRate != nil {
		t.Errorf("label 0 = %+v, want nothing sent and nil rates", l)
	}
	if l := labels[1]; l.Label != 1 || l.RefuteRate == nil || *l.RefuteRate != 1 || all.Sent != 1 {
		t.Errorf("label 1 = %+v, all = %+v, want one refuted comment", l, all)
	}
	data, err := json.Marshal(labels[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"label":0`, `"sent":0`, `"refute_rate":null`, `"unverifiable_rate":null`} {
		if !strings.Contains(string(data), want) {
			t.Errorf("label 0 JSON = %s, want it to hold %s", data, want)
		}
	}
	if _, all := tallyVerdicts(nil); all.RefuteRate != nil || all.UnverifiableRate != nil {
		t.Errorf("all of nothing = %+v, want nil rates", all)
	}
}

// benchReport is a filled VerifierReport for the render tests: three
// comments of label 0 and one of label 1 sent, one skipped.
func benchReport() VerifierReport {
	samples := []VerifierSample{
		{PRURL: widget7URL, Commit: widget7Head, File: benchFile, LineStart: 1, LineEnd: 1, Label: 0, Category: aacrCategoryDefect, Context: ctxDiff, Outcome: hygiene.VerdictRefuted, Verdict: "refuted", Evidence: "store.go:1", Reason: "guarded"},
		{PRURL: widget7URL, Commit: widget7Head, File: benchFile, LineStart: 2, LineEnd: 2, Label: 0, Outcome: hygiene.VerdictConfirmed, Verdict: "confirmed"},
		{PRURL: widget7URL, Commit: widget7Head, File: benchFile, LineStart: 3, LineEnd: 3, Label: 0, Outcome: hygiene.VerdictUnverifiable, Verdict: "unverifiable"},
		{PRURL: widget7URL, Commit: widget7Head, File: benchFile, LineStart: 4, LineEnd: 4, Label: 1, Outcome: report.ClaimCheckNoVerdict},
	}
	rep := VerifierReport{
		PerLabel: 20,
		Excluded: 1056,
		Tiers:    sampleTiers(),
		Skipped:  []SkippedComment{{PRURL: widget8URL, File: benchFile, LineStart: 9, LineEnd: 12, Label: 1, Reason: "checkout: no such revision"}},
		Samples:  samples,
		Usage:    report.Usage{Calls: 2, InputTokens: 1000, CacheReadTokens: 400, CacheCreationTokens: 100, OutputTokens: 300, CostUSD: 1.234},
	}
	rep.Labels, rep.All = tallyVerdicts(samples)
	return rep
}

func TestRenderVerifierTable(t *testing.T) {
	var buf strings.Builder
	RenderVerifierTable(&buf, benchReport())
	out := buf.String()

	head := lineStarting(t, out, "LABEL ")
	if f := strings.Fields(head); !slices.Equal(f, []string{"LABEL", "SENT", "CONFIRMED", "REFUTED", "UNVERIFIABLE", "NO-VERDICT", "REFUTED%", "UNVERIFIABLE%"}) {
		t.Errorf("header = %v", f)
	}
	for prefix, want := range map[string][]string{
		"0 expert-rejected ":  {"0", "expert-rejected", "3", "1", "1", "1", "0", "33%", "33%"},
		"1 expert-confirmed ": {"1", "expert-confirmed", "1", "0", "0", "0", "1", "0%", "0%"},
		"ALL ":                {"ALL", "4", "1", "1", "1", "1", "25%", "25%"},
	} {
		if f := strings.Fields(lineStarting(t, out, prefix)); !slices.Equal(f, want) {
			t.Errorf("row %q = %v, want %v", prefix, f, want)
		}
	}
	if rule := lineStarting(t, out, "---"); len(rule) != len(head) {
		t.Errorf("rule is %d wide, header %d:\n%s", len(rule), len(head), out)
	}
	for _, want := range []string{
		"\nREFUTED% = refuted/sent: the share the gate would demote. UNVERIFIABLE% = unverifiable/sent.\n" +
			"A verifier that tells the labels apart refutes label 0 far more often than label 1.\n",
		"\nSAMPLE: 20 per label; 1056 rows not eligible; 1 skipped\n" +
			"  SKIPPED " + widget8URL + " store.go:9-12 (label 1): checkout: no such revision\n" +
			"TIERS: " + sampleTiersLine + " (the verifier runs on the finder tier)\n" +
			"COST: 2 calls, 1500 prompt tokens, 300 output tokens, $1.23\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}

	t.Run("a label with nothing sent shows n/a", func(t *testing.T) {
		rep := benchReport()
		rep.Labels, rep.All = tallyVerdicts(rep.Samples[:3])
		var buf strings.Builder
		RenderVerifierTable(&buf, rep)
		if f := strings.Fields(lineStarting(t, buf.String(), "1 expert-confirmed ")); !slices.Equal(f[len(f)-2:], []string{"n/a", "n/a"}) {
			t.Errorf("label 1 row = %v, want n/a rates", f)
		}
	})
}

func TestRenderVerifierJSONFields(t *testing.T) {
	var buf strings.Builder
	rep := benchReport()
	rep.Skipped = []SkippedComment{}
	if err := RenderVerifierJSON(&buf, rep); err != nil {
		t.Fatalf("RenderVerifierJSON: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(buf.String()), &got); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	for _, key := range []string{"per_label", "excluded", "tiers", "labels", "all", "skipped", "samples", "usage"} {
		if _, ok := got[key]; !ok {
			t.Errorf("report lacks %q:\n%s", key, buf.String())
		}
	}
	if got["per_label"] != float64(20) || got["excluded"] != float64(1056) {
		t.Errorf("per_label, excluded = %v, %v, want 20, 1056", got["per_label"], got["excluded"])
	}
	if tiers, _ := got["tiers"].(map[string]any); tiers["finder_effort"] != "high" {
		t.Errorf("tiers = %v, want the recorded tiers", got["tiers"])
	}
	labels, _ := got["labels"].([]any)
	if len(labels) != 2 {
		t.Fatalf("labels = %v, want two", got["labels"])
	}
	first, _ := labels[0].(map[string]any)
	for key, want := range map[string]any{"label": float64(0), "sent": float64(3), "confirmed": float64(1), "refuted": float64(1), "unverifiable": float64(1), "no_verdict": float64(0), "refute_rate": 1.0 / 3, "unverifiable_rate": 1.0 / 3} {
		if first[key] != want {
			t.Errorf("labels[0].%s = %v, want %v", key, first[key], want)
		}
	}
	if all, _ := got["all"].(map[string]any); all["sent"] != float64(4) || all["no_verdict"] != float64(1) {
		t.Errorf("all = %v, want 4 sent, 1 without a verdict", got["all"])
	}
	if skipped, ok := got["skipped"].([]any); !ok || len(skipped) != 0 {
		t.Errorf("skipped = %#v, want []", got["skipped"])
	}
	samples, _ := got["samples"].([]any)
	if s, _ := samples[0].(map[string]any); s["outcome"] != hygiene.VerdictRefuted || s["evidence"] != "store.go:1" || s["line_start"] != float64(1) || s["commit"] != widget7Head {
		t.Errorf("samples[0] = %v, want the refuted comment with its evidence", samples[0])
	}
	if s, _ := samples[3].(map[string]any); s["outcome"] != report.ClaimCheckNoVerdict {
		t.Errorf("samples[3].outcome = %v, want %s", s["outcome"], report.ClaimCheckNoVerdict)
	} else if _, ok := s["verdict"]; ok {
		t.Errorf("samples[3] = %v, want no verdict key for a comment without one", s)
	}
	if usage, _ := got["usage"].(map[string]any); usage["calls"] != float64(2) {
		t.Errorf("usage = %v, want 2 calls", got["usage"])
	}
}

func TestCheckoutAACRHeadRefusesBadInput(t *testing.T) {
	// No git on PATH: a value that got past the checks would fail on the
	// missing binary instead of on its own error.
	t.Setenv("PATH", t.TempDir())
	for _, tt := range []struct {
		name, prURL, commit, want string
	}{
		{"a URL off GitHub", "https://gitlab.com/acme/widget/pull/7", widget7Head, "not a GitHub pull request URL"},
		{"a URL with no pull number", "https://github.com/acme/widget", widget7Head, "not a GitHub pull request URL"},
		{"a commit that reads as an option", widget7URL, "--upload-pack=touch /tmp/pwned", "is not a full commit SHA"},
		{"a short commit", widget7URL, "2222222", "is not a full commit SHA"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := CheckoutAACRHead(t.TempDir(), tt.prURL, tt.commit)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("CheckoutAACRHead = %v, want an error containing %q", err, tt.want)
			}
		})
	}
}

// TestCheckoutAACRHeadKeepsDatasetValuesInsideArguments runs CheckoutAACRHead
// against a fake git that records its argv and fails the fetch. The pr_url
// check admits an owner that starts with a dash, so the owner must reach git
// only inside the store path and the clone URL, never as an argument of its
// own.
func TestCheckoutAACRHeadKeepsDatasetValuesInsideArguments(t *testing.T) {
	bin := t.TempDir()
	argv := filepath.Join(bin, "argv")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" >> " + argv + "\ncase \"$1\" in init) exit 0 ;; esac\nexit 1\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o755); err != nil {
		t.Fatalf("writing fake git: %v", err)
	}
	t.Setenv("PATH", bin)

	root := t.TempDir()
	store := filepath.Join(root, "--upload-pack", "widget", "7.git")
	// The fake init creates nothing, and the later calls run in the store.
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := CheckoutAACRHead(root, "https://github.com/--upload-pack/widget/pull/7", widget7Head); err == nil {
		t.Fatal("CheckoutAACRHead = nil error, want the fake fetch's failure")
	}
	raw, err := os.ReadFile(argv)
	if err != nil {
		t.Fatalf("reading argv: %v", err)
	}
	args := strings.Split(strings.TrimSpace(string(raw)), "\n")
	for _, a := range args {
		if strings.HasPrefix(a, "--upload-pack") {
			t.Errorf("git got the owner as an argument of its own: %q in %q", a, args)
		}
	}
	for _, want := range []string{
		store,
		"https://github.com/--upload-pack/widget.git",
		"+" + widget7Head + ":" + aacrVerifierRef,
	} {
		if !slices.Contains(args, want) {
			t.Errorf("git argv %q lacks %q", args, want)
		}
	}
}

func TestCheckoutAACRCommit(t *testing.T) {
	requireGit(t)
	remote := newAACRRemote(t)
	store := filepath.Join(t.TempDir(), "acme", "widget", "7.git")

	dir, cleanup, err := checkoutAACRCommit(store, "file://"+remote.dir, remote.head)
	if err != nil {
		t.Fatalf("checkoutAACRCommit: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(dir, "a.go")); err != nil || string(got) != "package a\n\nfunc A() {}\n" {
		t.Errorf("a.go = %q, %v, want the revision's content checked out", got, err)
	}
	if got, want := gitIn(t, dir, "rev-parse", "HEAD^{tree}"), gitIn(t, remote.dir, "rev-parse", remote.head+"^{tree}"); got != want {
		t.Errorf("HEAD's tree = %s, want the revision's %s", got, want)
	}
	// The store is shallow; the checkout's history must still walk.
	gitIn(t, dir, "log", "--oneline")
	if got := gitIn(t, store, "rev-parse", aacrVerifierRef); got != remote.head {
		t.Errorf("%s = %s, want the revision %s, so the fetch stays reachable", aacrVerifierRef, got, remote.head)
	}
	cleanup()
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("stat after cleanup = %v, want the checkout removed", err)
	}

	t.Run("a store that holds the commit is reused without a fetch", func(t *testing.T) {
		dir, cleanup, err := checkoutAACRCommit(store, "file://"+filepath.Join(t.TempDir(), "missing"), remote.head)
		if err != nil {
			t.Fatalf("checkoutAACRCommit from the filled store: %v", err)
		}
		defer cleanup()
		if _, err := os.Stat(filepath.Join(dir, "a.go")); err != nil {
			t.Errorf("a.go: %v, want it checked out", err)
		}
	})

	t.Run("a commit the remote does not have fails", func(t *testing.T) {
		missing := strings.Repeat("3", 40)
		if _, _, err := checkoutAACRCommit(filepath.Join(t.TempDir(), "7.git"), "file://"+remote.dir, missing); err == nil || !strings.Contains(err.Error(), "git fetch") {
			t.Errorf("checkoutAACRCommit = %v, want the fetch's error", err)
		}
	})

	t.Run("PrepareAACR's store serves CheckoutAACRHead with no network", func(t *testing.T) {
		root := t.TempDir()
		src := remote.source("file://" + remote.dir)
		if err := prepareAACR(root, []Case{{Name: "widget-7", AACR: src}}, []int{8}, io.Discard); err != nil {
			t.Fatalf("prepareAACR: %v", err)
		}
		dir, cleanup, err := CheckoutAACRHead(root, widget7URL, strings.ToUpper(remote.head))
		if err != nil {
			t.Fatalf("CheckoutAACRHead: %v", err)
		}
		defer cleanup()
		if got, want := gitIn(t, dir, "rev-parse", "HEAD^{tree}"), gitIn(t, remote.dir, "rev-parse", remote.head+"^{tree}"); got != want {
			t.Errorf("HEAD's tree = %s, want the revision's %s", got, want)
		}
	})

	t.Run("a store the bench filled first finds the merge base at PrepareAACR's first depth", func(t *testing.T) {
		root := t.TempDir()
		store := aacrStorePath(root, "acme", "widget", 7)
		_, cleanup, err := checkoutAACRCommit(store, "file://"+remote.dir, remote.head)
		if err != nil {
			t.Fatalf("checkoutAACRCommit: %v", err)
		}
		cleanup()
		// 8 finds the merge base in a fresh store. Had the bench's depth-1
		// fetch left the revision under PrepareAACR's ref, the fetch at 8
		// would skip it, deepen the base alone, and miss the merge base.
		src := remote.source("file://" + remote.dir)
		if err := prepareAACR(root, []Case{{Name: "widget-7", AACR: src}}, []int{8}, io.Discard); err != nil {
			t.Fatalf("prepareAACR after the bench: %v", err)
		}
		if src.MergeBase != remote.mergeBase || src.Store != store {
			t.Errorf("source = %s in %s, want %s in %s", src.MergeBase, src.Store, remote.mergeBase, store)
		}
	})
}
