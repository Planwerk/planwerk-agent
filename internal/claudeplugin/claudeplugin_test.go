package claudeplugin

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
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

// skillMD returns a SKILL.md with the given frontmatter lines.
func skillMD(lines ...string) string {
	return "---\n" + strings.Join(lines, "\n") + "\n---\n\nBody.\n"
}

// newPlugin writes a plugin root with the given manifest and returns its
// directory.
func newPlugin(t *testing.T, manifest string) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, dir, manifestPath, manifest)
	return dir
}

func TestLoad_ReadsTheManifestAndTheSkills(t *testing.T) {
	dir := newPlugin(t, `{"name": "design", "version": "4.7.1", "skills": "./extra/"}`)
	writeFile(t, dir, "skills/polish/SKILL.md", skillMD("name: polish", "description: Polish an interface."))
	writeFile(t, dir, "extra/audit/SKILL.md", skillMD("name: audit", `description: "Audit: checks."`))
	writeFile(t, dir, "skills/hidden/SKILL.md", skillMD("name: hidden", "disable-model-invocation: true"))
	writeFile(t, dir, "skills/notes/README.md", "not a skill")

	p, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if p.Dir != dir || p.Name != "design" || p.Version != "4.7.1" {
		t.Errorf("Load = %+v, want dir %s, name design, version 4.7.1", p, dir)
	}
	want := []Skill{
		{Name: "audit", Description: "Audit: checks.", Dir: filepath.Join(dir, "extra", "audit")},
		{Name: "polish", Description: "Polish an interface.", Dir: filepath.Join(dir, "skills", "polish")},
	}
	if len(p.Skills) != len(want) {
		t.Fatalf("Skills = %+v, want %+v", p.Skills, want)
	}
	for i := range want {
		if p.Skills[i] != want[i] {
			t.Errorf("Skills[%d] = %+v, want %+v", i, p.Skills[i], want[i])
		}
	}
	if got := p.SkillID("audit"); got != "design:audit" {
		t.Errorf("SkillID = %q, want design:audit", got)
	}
	if got := p.Label(); got != "design@4.7.1" {
		t.Errorf("Label = %q, want design@4.7.1", got)
	}
}

func TestLoad_AcceptsAListOfSkillPathsAndNoVersion(t *testing.T) {
	dir := newPlugin(t, `{"name": "kit", "skills": ["./a", "./skills"]}`)
	writeFile(t, dir, "a/one/SKILL.md", skillMD("description: First."))

	p, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if p.Label() != "kit" {
		t.Errorf("Label = %q, want kit", p.Label())
	}
	if len(p.Skills) != 1 || p.Skills[0].Name != "one" {
		t.Errorf("Skills = %+v, want the one skill named after its directory", p.Skills)
	}
}

func TestLoad_Rejects(t *testing.T) {
	cases := map[string]struct {
		manifest string // "" writes none
		want     string
	}{
		"no manifest":          {"", "has no .claude-plugin/plugin.json"},
		"no name":              {`{"version": "1"}`, "names no plugin"},
		"malformed":            {`{`, "decoding"},
		"skills outside":       {`{"name": "x", "skills": "../elsewhere"}`, "leaves the plugin"},
		"skills of wrong type": {`{"name": "x", "skills": 3}`, "must be a path or a list"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if tc.manifest != "" {
				writeFile(t, dir, manifestPath, tc.manifest)
			}
			_, err := Load(dir)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Load error = %v, want one containing %q", err, tc.want)
			}
		})
	}
}

func TestResolve_ReadsADirectory(t *testing.T) {
	dir := newPlugin(t, `{"name": "design"}`)
	got, err := Resolve([]string{dir}, func() ([]Installed, error) {
		t.Fatal("a directory must not list the installed plugins")
		return nil, nil
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(got) != 1 || got[0].Dir != dir {
		t.Errorf("Resolve = %+v, want the plugin at %s", got, dir)
	}
}

func TestResolve_LooksUpInstalledIDsOnce(t *testing.T) {
	a := newPlugin(t, `{"name": "a", "version": "1.0.0"}`)
	b := newPlugin(t, `{"name": "b"}`)
	calls := 0
	list := func() ([]Installed, error) {
		calls++
		return []Installed{{ID: "a@market", Version: "1.0.0", InstallPath: a}, {ID: "b@market", InstallPath: b}}, nil
	}

	got, err := Resolve([]string{" a@market ", "", "b@market", "a@market", a}, list)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if calls != 1 {
		t.Errorf("listed the installed plugins %d times, want once", calls)
	}
	if len(got) != 2 || got[0].Name != "a" || got[1].Name != "b" {
		t.Errorf("Resolve = %+v, want a then b, each once", got)
	}
}

func TestResolve_Fails(t *testing.T) {
	installed := []Installed{{ID: "z@m", InstallPath: "/nowhere"}, {ID: "y@m", InstallPath: ""}}
	list := func() ([]Installed, error) { return installed, nil }
	cases := map[string]struct {
		value string
		list  Lister
		want  string
	}{
		"neither":         {"not a plugin", list, "neither a plugin directory nor an installed plugin id"},
		"not installed":   {"a@m", list, "not installed; installed: y@m, z@m"},
		"nothing at all":  {"a@m", func() ([]Installed, error) { return nil, nil }, "no plugin is (claude plugin install a@m)"},
		"no install path": {"y@m", list, "reports no install path"},
		"gone from disk":  {"z@m", list, "is not a Claude Code plugin"},
		"list fails":      {"a@m", func() ([]Installed, error) { return nil, errors.New("boom") }, "listing the installed plugins: boom"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Resolve([]string{tc.value}, tc.list)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Resolve error = %v, want one containing %q", err, tc.want)
			}
		})
	}
}

func TestResolve_RejectsASecondPluginOfTheSameName(t *testing.T) {
	a := newPlugin(t, `{"name": "design"}`)
	b := newPlugin(t, `{"name": "design"}`)
	_, err := Resolve([]string{a, b}, nil)
	if err == nil || !strings.Contains(err.Error(), "a plugin named design is already loaded from another directory") {
		t.Errorf("Resolve error = %v, want the duplicate name refused", err)
	}
}

func TestResolve_TakesTheInstalledVersionWhenTheManifestNamesNone(t *testing.T) {
	dir := newPlugin(t, `{"name": "design"}`)
	got, err := Resolve([]string{"design@market"}, func() ([]Installed, error) {
		return []Installed{{ID: "design@market", Version: "3f2a9c1", InstallPath: dir}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Label() != "design@3f2a9c1" {
		t.Errorf("Label = %q, want the installed version design@3f2a9c1", got[0].Label())
	}
}

func TestLoad_FollowsASymlinkedSkillDirectory(t *testing.T) {
	dir := newPlugin(t, `{"name": "design"}`)
	src := t.TempDir()
	writeFile(t, src, "SKILL.md", skillMD("name: linked", "description: Linked in."))
	if err := os.MkdirAll(filepath.Join(dir, "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(src, filepath.Join(dir, "skills", "linked")); err != nil {
		t.Fatal(err)
	}
	p, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Skills) != 1 || p.Skills[0].Name != "linked" {
		t.Errorf("Skills = %+v, want the symlinked skill", p.Skills)
	}
}
