package diagramdesign

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/planwerk/planwerk-agent/internal/claudeplugin"
)

// writeFile writes content to dir/rel, creating the parent directories.
func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// fakePython puts a python3 on PATH that drops -I and runs the script it is
// given with sh, so a test's checkers are shell scripts and the test needs no
// Python. It writes each argv it receives, one argument per line and a blank
// line after, to the returned file.
func fakePython(t *testing.T) string {
	t.Helper()
	bin := t.TempDir()
	calls := filepath.Join(t.TempDir(), "calls")
	writeFile(t, bin, "python3", `#!/bin/sh
printf '%s\n' "$@" "DNT=$DO_NOT_TRACK" "" >> `+calls+`
[ "$1" = "-I" ] && shift
exec sh "$@"
`)
	if err := os.Chmod(filepath.Join(bin, "python3"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return calls
}

// fakeKit returns a Kit whose two checkers are the given shell script bodies,
// with the reference documents named in guides.
func fakeKit(t *testing.T, selfCheck, geometry string, guides ...string) Kit {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "diagram-design")
	writeFile(t, dir, "scripts/"+SelfCheck, selfCheck)
	writeFile(t, dir, "scripts/"+VerifyGeometry, geometry)
	if err := os.MkdirAll(filepath.Join(dir, "references"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, g := range guides {
		writeFile(t, dir, "references/"+g, "# "+g)
	}
	return Kit{SkillDir: dir}
}

// The scripts a clean run of each checker amounts to: both name the file the
// way the real ones do.
const (
	cleanSelfCheck = "echo \"OK ${1#./}\"\n"
	cleanGeometry  = "echo 'Summary: 1 file(s) checked, 0 finding(s).'\n"
)

func TestFind(t *testing.T) {
	kit := fakeKit(t, cleanSelfCheck, cleanGeometry)
	noGeometry := filepath.Join(t.TempDir(), "diagram-design")
	writeFile(t, noGeometry, "scripts/"+SelfCheck, cleanSelfCheck)
	writeFile(t, noGeometry, "references/style-guide.md", "# Style")
	plugins := []claudeplugin.Plugin{
		{Name: "other", Skills: []claudeplugin.Skill{{Name: "export-diagram", Dir: kit.SkillDir}}},
		{Name: "broken", Skills: []claudeplugin.Skill{{Name: "diagram-design", Dir: noGeometry}}},
		{Name: "diagram-design", Version: "2.6.181", Skills: []claudeplugin.Skill{{Name: "diagram-design", Dir: kit.SkillDir}}},
	}
	got, ok := Find(plugins)
	if want := (Kit{SkillDir: kit.SkillDir, Skill: "diagram-design:diagram-design"}); !ok || got != want {
		t.Errorf("Find = %+v, %v; want %+v, true", got, ok, want)
	}
	if _, ok := Find(plugins[:2]); ok {
		t.Error("Find reported a kit for plugins that ship no usable diagram-design skill")
	}
}

func TestGuides_ListsTheShippedOnes(t *testing.T) {
	kit := fakeKit(t, cleanSelfCheck, cleanGeometry, "layout-budget.md", "style-guide.md")
	if got := kit.Guides(); len(got) != 1 || got[0].File != "layout-budget.md" || got[0].About == "" {
		t.Errorf("Guides = %v, want [layout-budget.md]", got)
	}
	if kit.HasTypeReferences() {
		t.Error("HasTypeReferences without a type-*.md")
	}
	writeFile(t, kit.SkillDir, "references/type-flowchart.md", "# Flowchart")
	if !kit.HasTypeReferences() {
		t.Error("HasTypeReferences = false with type-flowchart.md shipped")
	}
}

// TestCheck_ReturnsBothCheckersReportsSorted: each checker runs once per
// file, under python3 -I with DO_NOT_TRACK=1, and their reports come back
// sorted by file and line with the file's path from the checkout; a line
// verify_geometry.py prints without a number stays at line 0.
func TestCheck_ReturnsBothCheckersReportsSorted(t *testing.T) {
	calls := fakePython(t)
	repo := t.TempDir()
	writeFile(t, repo, "docs/diagrams/b.html", "<svg>")
	writeFile(t, repo, "docs/diagrams/a.html", "<svg>")
	kit := fakeKit(t, `f=${1#./}
case "$f" in
*a.html) printf 'FAIL %s\n  - svg 1 needs role=img\n  - svg 1 has no <title>\n' "$f"; exit 1 ;;
*) echo "OK $f" ;;
esac
`, `case "$1" in
*b.html) echo 'b.html:84: connector has a diagonal segment'; echo 'b.html: label mask clipped by node'; echo 'Summary: 1 file(s) checked, 2 finding(s).'; exit 1 ;;
*) echo 'Summary: 1 file(s) checked, 0 finding(s).' ;;
esac
`)
	got, err := kit.Check(repo, []string{"docs/diagrams/b.html", "docs/diagrams/a.html", "docs/diagrams/gone.html", "../outside.html"})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	want := []string{
		"docs/diagrams/a.html:0 self_check.py svg 1 needs role=img",
		"docs/diagrams/a.html:0 self_check.py svg 1 has no <title>",
		"docs/diagrams/b.html:0 verify_geometry.py label mask clipped by node",
		"docs/diagrams/b.html:84 verify_geometry.py connector has a diagonal segment",
	}
	if len(got) != len(want) {
		t.Fatalf("Check = %+v, want %v", got, want)
	}
	for i, f := range got {
		if s := f.File + ":" + strconv.Itoa(f.Line) + " " + f.Check + " " + f.Message; s != want[i] {
			t.Errorf("finding %d = %q, want %q", i, s, want[i])
		}
	}

	raw, err := os.ReadFile(calls)
	if err != nil {
		t.Fatal(err)
	}
	argv := strings.Split(strings.TrimSpace(string(raw)), "\n\n")
	if len(argv) != 4 {
		t.Fatalf("want two checker runs per existing file, got %d:\n%s", len(argv), raw)
	}
	wantFirst := "-I\n" + filepath.Join(kit.SkillDir, "scripts", SelfCheck) + "\n./docs/diagrams/a.html\nDNT=1"
	if argv[0] != wantFirst {
		t.Errorf("first checker run =\n%s\nwant\n%s", argv[0], wantFirst)
	}
}

func TestCheck_CleanFilesAndNothingToCheck(t *testing.T) {
	calls := fakePython(t)
	repo := t.TempDir()
	writeFile(t, repo, "diagrams/flow.html", "<svg>")
	kit := fakeKit(t, cleanSelfCheck, cleanGeometry)
	got, err := kit.Check(repo, []string{"diagrams/flow.html"})
	if err != nil || len(got) != 0 {
		t.Errorf("Check on clean files = %v, %v; want no findings and no error", got, err)
	}

	if err := os.Remove(calls); err != nil {
		t.Fatal(err)
	}
	if got, err := kit.Check(repo, []string{"diagrams/deleted.html"}); got != nil || err != nil {
		t.Errorf("Check with no existing target = %v, %v; want nil, nil", got, err)
	}
	if _, err := os.Stat(calls); err == nil {
		t.Error("Check ran a checker with nothing to check")
	}
}

func TestCheck_Fails(t *testing.T) {
	fakePython(t)
	repo := t.TempDir()
	writeFile(t, repo, "diagrams/flow.html", "<svg>")
	cases := map[string]struct {
		selfCheck, geometry string
		want                string
	}{
		"crashed":          {"echo 'Traceback (most recent call last):' >&2\nexit 2\n", cleanGeometry, "self_check.py: exit status 2: Traceback"},
		"missing":          {"exit 127\n", cleanGeometry, "exit status 127"},
		"no status line":   {"echo 'something else'\nexit 1\n", cleanGeometry, "self_check.py diagrams/flow.html: output without the line"},
		"no summary":       {cleanSelfCheck, "echo 'flow.html:3: x'\nexit 1\n", "verify_geometry.py diagrams/flow.html: output without the line"},
		"geometry crashed": {cleanSelfCheck, "echo 'SyntaxError: bad' >&2\nexit 1\n", "verify_geometry.py diagrams/flow.html: output without the line"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := fakeKit(t, tc.selfCheck, tc.geometry).Check(repo, []string{"diagrams/flow.html"})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Check error = %v, want one containing %q", err, tc.want)
			}
		})
	}
}

func TestCheck_Caps(t *testing.T) {
	calls := fakePython(t)
	repo := t.TempDir()
	var files []string
	for i := range maxTargets + 5 {
		name := "diagrams/f" + strconv.Itoa(1000+i) + ".html"
		writeFile(t, repo, name, "<svg>")
		files = append(files, name)
	}
	if _, err := fakeKit(t, cleanSelfCheck, cleanGeometry).Check(repo, files); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(calls)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(raw), "DNT=1"); n != 2*maxTargets {
		t.Errorf("ran %d checkers, want two for each of the first %d files", n, maxTargets)
	}
	if strings.Contains(string(raw), "f1100.html") {
		t.Error("checked a file past the cap")
	}
}
