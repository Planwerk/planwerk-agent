package impeccable

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

// fakeKit returns a Kit whose launcher is the given shell script body, with
// the reference documents named in guides.
func fakeKit(t *testing.T, script string, guides ...string) Kit {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "impeccable")
	writeFile(t, dir, "scripts/impeccable", "#!/bin/sh\n"+script)
	if err := os.MkdirAll(filepath.Join(dir, "reference"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, g := range guides {
		writeFile(t, dir, "reference/"+g, "# "+g)
	}
	return Kit{SkillDir: dir}
}

func TestFind(t *testing.T) {
	kit := fakeKit(t, "exit 0\n")
	bare := filepath.Join(t.TempDir(), "impeccable") // no launcher
	if err := os.MkdirAll(bare, 0o755); err != nil {
		t.Fatal(err)
	}
	plugins := []claudeplugin.Plugin{
		{Name: "other", Skills: []claudeplugin.Skill{{Name: "polish", Dir: kit.SkillDir}}},
		{Name: "broken", Skills: []claudeplugin.Skill{{Name: "impeccable", Dir: bare}}},
		{Name: "impeccable", Version: "4.7.1", Skills: []claudeplugin.Skill{{Name: "impeccable", Dir: kit.SkillDir}}},
	}
	got, ok := Find(plugins)
	if !ok || got != kit {
		t.Errorf("Find = %+v, %v; want %+v, true", got, ok, kit)
	}
	if _, ok := Find(plugins[:2]); ok {
		t.Error("Find reported a kit for plugins that ship no usable impeccable skill")
	}
}

func TestGuides_ListsTheShippedOnes(t *testing.T) {
	kit := fakeKit(t, "exit 0\n", "harden.md", "critique.md")
	if got := kit.Guides(); len(got) != 1 || got[0].File != "harden.md" || got[0].About == "" {
		t.Errorf("Guides = %v, want [harden.md]", got)
	}
}

func TestDetect_ReturnsRelativeSortedFindings(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, repo, "src/b.css", "b")
	writeFile(t, repo, "src/a.tsx", "a")
	args := filepath.Join(t.TempDir(), "args")
	kit := fakeKit(t, `printf '%s\n' "$@" > `+args+`
printf 'DNT=%s\n' "$DO_NOT_TRACK" >> `+args+`
cat <<EOF
[{"antipattern":"side-tab","name":"Side-tab accent border","severity":"warning","category":"slop","file":"$PWD/src/b.css","line":3,"snippet":"border-left: 4px"},
 {"antipattern":"skipped-heading","name":"Skipped heading","severity":"warning","category":"quality","file":"$PWD/src/a.tsx","line":0,"snippet":"h1 then h3"},
 {"antipattern":"gradient-text","name":"Gradient text","severity":"warning","category":"slop","file":"$PWD/src/b.css","line":1,"snippet":"background-clip: text"}]
EOF
exit 2
`)

	got, err := kit.Detect(repo, []string{"src/b.css", "src/a.tsx", "src/gone.css", "../outside.css"})
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	want := []string{"src/a.tsx:0 skipped-heading", "src/b.css:1 gradient-text", "src/b.css:3 side-tab"}
	if len(got) != len(want) {
		t.Fatalf("Detect = %+v, want %v", got, want)
	}
	for i, f := range got {
		if s := f.File + ":" + strconv.Itoa(f.Line) + " " + f.Rule; s != want[i] {
			t.Errorf("finding %d = %q, want %q", i, s, want[i])
		}
	}

	raw, err := os.ReadFile(args)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(raw), "detect\n--json\n--no-config\n./src/a.tsx\n./src/b.css\nDNT=1\n"; got != want {
		t.Errorf("launcher arguments =\n%s\nwant\n%s", got, want)
	}
}

func TestDetect_CleanScanAndNothingToScan(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, repo, "index.html", "<p>")
	kit := fakeKit(t, "echo '[]'\nexit 0\n")
	got, err := kit.Detect(repo, []string{"index.html"})
	if err != nil || len(got) != 0 {
		t.Errorf("Detect on a clean scan = %v, %v; want no findings and no error", got, err)
	}

	marker := filepath.Join(t.TempDir(), "ran")
	never := fakeKit(t, "touch "+marker+"\n")
	if got, err := never.Detect(repo, []string{"deleted.css"}); got != nil || err != nil {
		t.Errorf("Detect with no existing target = %v, %v; want nil, nil", got, err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("Detect ran the launcher with nothing to scan")
	}
}

func TestDetect_Fails(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, repo, "a.css", "a")
	cases := map[string]struct {
		script string
		want   string
	}{
		"failed scan":    {"echo 'cannot scan a.css' >&2\nexit 1\n", "cannot scan a.css"},
		"garbled":        {"echo 'not json'\nexit 2\n", "decoding impeccable detect --json"},
		"missing engine": {"exit 127\n", "exit status 127"},
		"crashed":        {"echo 'panic: engine' >&2\nexit 3\n", "panic: engine"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := fakeKit(t, tc.script).Detect(repo, []string{"a.css"})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Detect error = %v, want one containing %q", err, tc.want)
			}
		})
	}
}

func TestDetect_KeepsThePartOfAPartialScan(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, repo, "a.css", "a")
	kit := fakeKit(t, `echo 'Warning: cannot access ./b.css' >&2
echo '[{"antipattern":"side-tab","file":"a.css","line":2}]'
exit 1
`)
	got, err := kit.Detect(repo, []string{"a.css"})
	if err != nil || len(got) != 1 || got[0].File != "a.css" || got[0].Line != 2 {
		t.Errorf("Detect on a partial scan = %+v, %v; want the one match in a.css", got, err)
	}
}

func TestScanTargets_Caps(t *testing.T) {
	repo := t.TempDir()
	var files []string
	for i := range maxTargets + 5 {
		name := "f" + strconv.Itoa(1000+i) + ".css"
		writeFile(t, repo, name, "x")
		files = append(files, name)
	}
	if got := scanTargets(repo, files); len(got) != maxTargets || got[0] != "./f1000.css" {
		t.Errorf("scanTargets kept %d files starting with %q, want %d starting with ./f1000.css", len(got), got[0], maxTargets)
	}
}

// TestScanTargets_StaysInsideTheCheckout: a change that commits a directory
// as a symlink to a path outside the checkout cannot make the detector read a
// file there, whether the symlink is the file itself or a directory above it.
func TestScanTargets_StaysInsideTheCheckout(t *testing.T) {
	outside := t.TempDir()
	writeFile(t, outside, "secret.html", "<h1>secret</h1>")
	repo := t.TempDir()
	writeFile(t, repo, "web/ok.html", "<p>")
	if err := os.Symlink(outside, filepath.Join(repo, "linked")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.html"), filepath.Join(repo, "page.html")); err != nil {
		t.Fatal(err)
	}
	got := scanTargets(repo, []string{"linked/secret.html", "page.html", "web/ok.html", "../" + filepath.Base(outside) + "/secret.html"})
	if len(got) != 1 || got[0] != "./web/ok.html" {
		t.Errorf("scanTargets = %v, want only ./web/ok.html", got)
	}
}

// TestDetect_KeepsOnlyTheScannedFiles: a match the detector reports in a file
// it was not given (a stylesheet a scanned page links to, possibly outside the
// checkout) never reaches the caller, so its text cannot be quoted.
func TestDetect_KeepsOnlyTheScannedFiles(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, repo, "index.html", "<p>")
	kit := fakeKit(t, `cat <<EOF
[{"antipattern":"skipped-heading","file":"$PWD/index.html","line":1},
 {"antipattern":"low-contrast","file":"$PWD/../outside.css","line":4,"snippet":"secret"},
 {"antipattern":"low-contrast","file":"theme.css","line":2}]
EOF
exit 2
`)
	got, err := kit.Detect(repo, []string{"index.html"})
	if err != nil || len(got) != 1 || got[0].File != "index.html" {
		t.Errorf("Detect = %+v, %v; want the one match in index.html", got, err)
	}
}
