package eval

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// The fixture's contexts, as the export spells them.
const (
	ctxDiff = "Diff Level"
	ctxFile = "File Level"
	ctxRepo = "Repo Level"
)

// aacrFixture is the synthetic export under testdata, written for these tests.
var aacrFixture = filepath.Join("testdata", "aacr", "export.json")

// The fixture's first pull request, which most error rows below vary.
const (
	widget7URL  = "https://github.com/acme/widget/pull/7"
	widget7Base = "1111111111111111111111111111111111111111"
	widget7Head = "2222222222222222222222222222222222222222"
)

// validRow returns a well-formed verified right-side row of widget7URL.
func validRow() AACRRow {
	return AACRRow{
		Language: "Go", PRURL: widget7URL, SourceCommit: widget7Base, TargetCommit: widget7Head,
		Note: "a race", Path: "store.go", Side: aacrRight, FromLine: 4, ToLine: 6, Context: ctxDiff, Label: 1,
	}
}

// writeAACRExport writes rows as a JSON array export under t.TempDir and
// returns its path.
func writeAACRExport(t *testing.T, rows ...AACRRow) string {
	t.Helper()
	data, err := json.Marshal(rows)
	if err != nil {
		t.Fatalf("marshal rows: %v", err)
	}
	path := filepath.Join(t.TempDir(), "export.json")
	writeFile(t, path, string(data))
	return path
}

func TestLoadAACR(t *testing.T) {
	exp, err := LoadAACR(aacrFixture)
	if err != nil {
		t.Fatalf("LoadAACR: %v", err)
	}

	// crate/tool#9 holds only label-0 rows and is no candidate.
	if got := caseNamesOf(exp.Cases); !slices.Equal(got, []string{"gadget-3", "widget-12", "widget-7"}) {
		t.Fatalf("cases = %v, want [gadget-3 widget-12 widget-7], sorted by name", got)
	}
	data, err := os.ReadFile(aacrFixture)
	if err != nil {
		t.Fatalf("reading the fixture: %v", err)
	}
	if sum := sha256.Sum256(data); exp.SHA256 != hex.EncodeToString(sum[:]) {
		t.Errorf("SHA256 = %s, want the file's", exp.SHA256)
	}
	if len(exp.Rows) != 12 {
		t.Errorf("Rows = %d, want every one of the export's 12 rows", len(exp.Rows))
	}
	if r := exp.Rows[8]; r.Side != aacrRight {
		t.Errorf("Rows[8].Side = %q, want the export's RIGHT lowercased", r.Side)
	}
	if r := exp.Rows[2]; r.FromLine != 10 || r.ToLine != 12 {
		t.Errorf("Rows[2] lines = %d-%d, want the export's 12-10 ordered to 10-12", r.FromLine, r.ToLine)
	}

	w7 := exp.Cases[2]
	src := w7.AACR
	if src == nil {
		t.Fatal("widget-7 has no AACRSource")
	}
	// The export's source commit is the base and its target commit the head.
	if src.BaseSHA != widget7Base || src.HeadSHA != widget7Head {
		t.Errorf("BaseSHA/HeadSHA = %s/%s, want pr_source_commit %s as the base and pr_target_commit %s as the head",
			src.BaseSHA, src.HeadSHA, widget7Base, widget7Head)
	}
	wantSrc := AACRSource{
		URL: widget7URL, Owner: "acme", Repo: "widget", Number: 7,
		CloneURL: "https://github.com/acme/widget.git", BaseSHA: widget7Base, HeadSHA: widget7Head,
		// Each context holds one scored comment; the tie goes to the one that sorts first.
		Language: "Go", Context: ctxDiff, LeftExcluded: 1,
	}
	gotSrc := *src
	gotSrc.Rows = nil
	if !reflect.DeepEqual(gotSrc, wantSrc) {
		t.Errorf("widget-7 source = %+v, want %+v", gotSrc, wantSrc)
	}
	// The label-0 row stays among the pull request's rows but expects nothing.
	if len(src.Rows) != 5 || src.Rows[1].Label != 0 {
		t.Errorf("widget-7 rows = %+v, want its 5 rows with the label-0 one second", src.Rows)
	}
	wantFindings := []ExpectedFinding{
		{File: "store.go", Line: 40, LineEnd: 44, Comment: "The map is written after Unlock, so two writers race."},
		{File: "api/handler.go", Line: 10, LineEnd: 12, Comment: "The handler ignores the decode error."},
		{File: "store.go", Line: 60, LineEnd: 61, Comment: "Close is never called on the store's file."},
	}
	if !reflect.DeepEqual(w7.Expected.Findings, wantFindings) {
		t.Errorf("widget-7 findings = %+v, want only the verified right-side comments %+v", w7.Expected.Findings, wantFindings)
	}
	if w7.Expected.Description != widget7URL || w7.Expected.Clean {
		t.Errorf("widget-7 Expected = %+v, want the URL as description and not clean", w7.Expected)
	}

	if got := exp.Cases[1].AACR.Context; got != ctxFile {
		t.Errorf("widget-12 Context = %q, want %q, the context of two of its three comments", got, ctxFile)
	}
	if got := exp.Cases[0].AACR; got.Language != "Python" || got.LeftExcluded != 0 || len(exp.Cases[0].Expected.Findings) != 1 {
		t.Errorf("gadget-3 = %+v with %d findings, want Python, no left-side comment and one finding", got, len(exp.Cases[0].Expected.Findings))
	}

	t.Run("a repository name under two owners is prefixed with the owner", func(t *testing.T) {
		other := validRow()
		other.PRURL = "https://github.com/fork/widget/pull/7"
		exp, err := LoadAACR(writeAACRExport(t, validRow(), other))
		if err != nil {
			t.Fatalf("LoadAACR: %v", err)
		}
		if got := caseNamesOf(exp.Cases); !slices.Equal(got, []string{"acme-widget-7", "fork-widget-7"}) {
			t.Errorf("cases = %v, want [acme-widget-7 fork-widget-7]", got)
		}
	})

	t.Run("a second owner whose pull request is no case renames nothing", func(t *testing.T) {
		other := validRow()
		other.PRURL = "https://github.com/fork/widget/pull/7"
		other.Label = 0
		exp, err := LoadAACR(writeAACRExport(t, validRow(), other))
		if err != nil {
			t.Fatalf("LoadAACR: %v", err)
		}
		if got := caseNamesOf(exp.Cases); !slices.Equal(got, []string{"widget-7"}) {
			t.Errorf("cases = %v, want [widget-7]: fork/widget#7 holds only a label-0 row", got)
		}
	})
}

func TestLoadAACRJSONLines(t *testing.T) {
	want, err := LoadAACR(aacrFixture)
	if err != nil {
		t.Fatalf("LoadAACR: %v", err)
	}
	data, err := os.ReadFile(aacrFixture)
	if err != nil {
		t.Fatalf("reading the fixture: %v", err)
	}
	var rows []json.RawMessage
	if err := json.Unmarshal(data, &rows); err != nil {
		t.Fatalf("decoding the fixture: %v", err)
	}
	var lines bytes.Buffer
	for _, r := range rows {
		if err := json.Compact(&lines, r); err != nil {
			t.Fatalf("compacting a row: %v", err)
		}
		lines.WriteByte('\n')
	}
	path := filepath.Join(t.TempDir(), "export.jsonl")
	writeFile(t, path, lines.String())

	got, err := LoadAACR(path)
	if err != nil {
		t.Fatalf("LoadAACR(JSON Lines): %v", err)
	}
	if !reflect.DeepEqual(got.Cases, want.Cases) || !reflect.DeepEqual(got.Rows, want.Rows) {
		t.Errorf("JSON Lines cases = %+v, want the array's %+v", got.Cases, want.Cases)
	}
}

func TestLoadAACRErrors(t *testing.T) {
	with := func(edit func(*AACRRow)) AACRRow {
		r := validRow()
		edit(&r)
		return r
	}
	otherHead := with(func(r *AACRRow) { r.TargetCommit = strings.Repeat("9", 40) })
	otherLanguage := with(func(r *AACRRow) { r.Language = "Rust" })
	unverified := with(func(r *AACRRow) { r.Label = 0 })
	good, err := json.Marshal(validRow())
	if err != nil {
		t.Fatalf("marshal a row: %v", err)
	}
	stringLabel := strings.Replace(string(good), `"label":1`, `"label":"1"`, 1)

	tests := []struct {
		name    string
		content string // a raw export; empty means rows
		rows    []AACRRow
		want    []string
	}{
		{name: "not JSON", content: "not json", want: []string{"parsing AACR-Bench export", "row 0"}},
		{name: "an empty export", content: "[]", want: []string{"holds no rows"}},
		{name: "data after the array", content: "[] []", want: []string{"data follows the array"}},
		{name: "a mistyped field in an array", content: "[" + string(good) + "," + string(good) + "," + stringLabel + "]",
			want: []string{"row 2", "AACRRow.label"}},
		{name: "an array that is never closed", content: "[" + string(good), want: []string{"closing the array", "unexpected EOF"}},
		{name: "a pr_url that is no GitHub pull request", rows: []AACRRow{with(func(r *AACRRow) { r.PRURL = "https://gitlab.com/acme/widget/-/merge_requests/7" })},
			want: []string{"row 0 (https://gitlab.com/acme/widget/-/merge_requests/7)", "not a GitHub pull request URL"}},
		{name: "a pr_url naming a parent directory", rows: []AACRRow{with(func(r *AACRRow) { r.PRURL = "https://github.com/acme/../pull/7" })},
			want: []string{"row 0", `names ".."`}},
		{name: "a short commit", rows: []AACRRow{with(func(r *AACRRow) { r.SourceCommit = "1111111" })},
			want: []string{"row 0 (" + widget7URL + ")", `pr_source_commit "1111111" is not a full commit SHA`}},
		{name: "a missing commit", rows: []AACRRow{validRow(), with(func(r *AACRRow) { r.TargetCommit = "" })},
			want: []string{"row 1 (" + widget7URL + ")", `pr_target_commit "" is not a full commit SHA`}},
		{name: "an empty path", rows: []AACRRow{with(func(r *AACRRow) { r.Path = "" })}, want: []string{"row 0", "path is empty"}},
		{name: "a side that is neither", rows: []AACRRow{with(func(r *AACRRow) { r.Side = "middle" })}, want: []string{"row 0", `side "middle"`}},
		{name: "label 2", rows: []AACRRow{with(func(r *AACRRow) { r.Label = 2 })}, want: []string{"row 0", "label 2 is neither 0 nor 1"}},
		{name: "from_line 0", rows: []AACRRow{with(func(r *AACRRow) { r.FromLine = 0 })}, want: []string{"row 0", "lines 0-6"}},
		{name: "two target commits for one pull request", rows: []AACRRow{validRow(), otherHead},
			want: []string{"pull request " + widget7URL, "row 0 gives pr_target_commit", "row 1 gives"}},
		{name: "two languages for one pull request", rows: []AACRRow{validRow(), otherLanguage},
			want: []string{"pull request " + widget7URL, "project_main_language"}},
		{name: "no verified right-side comment", rows: []AACRRow{unverified},
			want: []string{"no pull request with a verified right-side comment"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "export.json")
			if tt.rows != nil {
				path = writeAACRExport(t, tt.rows...)
			} else {
				writeFile(t, path, tt.content)
			}
			_, err := LoadAACR(path)
			if err == nil {
				t.Fatal("LoadAACR = nil error, want one")
			}
			for _, want := range tt.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("err = %q, want it to contain %q", err, want)
				}
			}
		})
	}

	t.Run("a missing file", func(t *testing.T) {
		_, err := LoadAACR(filepath.Join(t.TempDir(), "missing.json"))
		if err == nil || !strings.Contains(err.Error(), "reading AACR-Bench export") {
			t.Errorf("err = %v, want a reading error", err)
		}
	})
}

// sampleCase builds an AACR-Bench case of the given stratum, named and keyed
// by name.
func sampleCase(name, language, context string) Case {
	return Case{Name: name, AACR: &AACRSource{URL: "https://github.com/acme/" + name + "/pull/1", Language: language, Context: context}}
}

// stratumOf names a sampled case's stratum as "<language>/<context>".
func stratumOf(c Case) string { return c.AACR.Language + "/" + c.AACR.Context }

func TestSampleAACR(t *testing.T) {
	// Three languages with one case in every context.
	var full []Case
	for _, lang := range []string{"Go", "Python", "Rust"} {
		for _, ctx := range []string{ctxDiff, ctxFile, ctxRepo} {
			full = append(full, sampleCase(strings.ToLower(lang)+"-"+strings.Fields(ctx)[0], lang, ctx))
		}
	}

	t.Run("one pick per language rotates the contexts", func(t *testing.T) {
		got := SampleAACR(full, 3)
		strata := make([]string, 0, len(got))
		for _, c := range got {
			strata = append(strata, stratumOf(c))
		}
		if want := []string{"Go/Diff Level", "Python/File Level", "Rust/Repo Level"}; !slices.Equal(strata, want) {
			t.Errorf("strata = %v, want %v", strata, want)
		}
	})

	t.Run("the next round moves every language on by one context", func(t *testing.T) {
		got := caseNamesOf(SampleAACR(full, 6))
		want := []string{"go-Diff", "go-File", "python-File", "python-Repo", "rust-Diff", "rust-Repo"}
		if !slices.Equal(got, want) {
			t.Errorf("sample = %v, want %v", got, want)
		}
	})

	t.Run("the order of the input does not matter", func(t *testing.T) {
		reversed := slices.Clone(full)
		slices.Reverse(reversed)
		for n := 1; n <= len(full); n++ {
			if a, b := caseNamesOf(SampleAACR(full, n)), caseNamesOf(SampleAACR(reversed, n)); !slices.Equal(a, b) {
				t.Errorf("n=%d: sample = %v, from the reversed input %v", n, a, b)
			}
		}
	})

	t.Run("a context the language lacks falls through to its next one", func(t *testing.T) {
		// Rust's turn wants File Level and has none, so it takes Diff Level
		// before Go's second round could take go-b.
		cases := []Case{sampleCase("go-a", "Go", ctxDiff), sampleCase("go-b", "Go", ctxFile), sampleCase("rust-a", "Rust", ctxDiff)}
		if got := caseNamesOf(SampleAACR(cases, 2)); !slices.Equal(got, []string{"go-a", "rust-a"}) {
			t.Errorf("sample = %v, want [go-a rust-a]", got)
		}
	})

	t.Run("a language with nothing left is skipped", func(t *testing.T) {
		// Round 0 takes go-a and rust-b; in round 1 Go has nothing left and
		// Rust takes rust-c.
		cases := []Case{sampleCase("go-a", "Go", ctxDiff), sampleCase("rust-a", "Rust", ctxDiff), sampleCase("rust-b", "Rust", ctxFile), sampleCase("rust-c", "Rust", ctxRepo)}
		if got := caseNamesOf(SampleAACR(cases, 3)); !slices.Equal(got, []string{"go-a", "rust-b", "rust-c"}) {
			t.Errorf("sample = %v, want [go-a rust-b rust-c]", got)
		}
	})

	t.Run("within one stratum the URL's SHA-256 orders the cases", func(t *testing.T) {
		cases := []Case{sampleCase("a", "Go", ctxDiff), sampleCase("b", "Go", ctxDiff), sampleCase("c", "Go", ctxDiff)}
		first := slices.MinFunc(cases, func(x, y Case) int {
			hx, hy := sha256.Sum256([]byte(x.AACR.URL)), sha256.Sum256([]byte(y.AACR.URL))
			return bytes.Compare(hx[:], hy[:])
		})
		if got := caseNamesOf(SampleAACR(cases, 1)); !slices.Equal(got, []string{first.Name}) {
			t.Errorf("sample = %v, want [%s], the lowest hash", got, first.Name)
		}
	})

	t.Run("n at or above the candidates returns all, sorted by name", func(t *testing.T) {
		reversed := slices.Clone(full)
		slices.Reverse(reversed)
		want := caseNamesOf(full)
		slices.Sort(want)
		for _, n := range []int{len(full), len(full) + 5} {
			if got := caseNamesOf(SampleAACR(reversed, n)); !slices.Equal(got, want) {
				t.Errorf("n=%d: sample = %v, want %v", n, got, want)
			}
		}
	})
}

func TestFindAACRCase(t *testing.T) {
	cases := []Case{sampleCase("widget-7", "Go", ctxDiff), sampleCase("gadget-3", "Python", ctxFile)}
	got, err := FindAACRCase(cases, "gadget-3")
	if err != nil || got.Name != "gadget-3" {
		t.Errorf("FindAACRCase(gadget-3) = %q, %v, want the case", got.Name, err)
	}
	if _, err := FindAACRCase(cases, "widget-8"); err == nil || !strings.Contains(err.Error(), `no AACR-Bench case "widget-8" among the export's 2 candidates`) {
		t.Errorf("FindAACRCase(widget-8) = %v, want a not-found error", err)
	}
}

func TestDefaultAACRCacheDir(t *testing.T) {
	want := filepath.Join(os.TempDir(), "planwerk-eval-aacr")
	if dir, err := os.UserCacheDir(); err == nil {
		want = filepath.Join(dir, "planwerk-eval", "aacr")
	}
	if got := DefaultAACRCacheDir(); got != want {
		t.Errorf("DefaultAACRCacheDir() = %q, want %q", got, want)
	}
}

// caseNamesOf returns the names of cases in order.
func caseNamesOf(cases []Case) []string {
	names := make([]string, 0, len(cases))
	for _, c := range cases {
		names = append(names, c.Name)
	}
	return names
}
