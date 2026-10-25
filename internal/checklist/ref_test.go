package checklist

import (
	"strings"
	"testing"

	"github.com/planwerk/planwerk-agent/internal/gitref/gitreftest"
)

// TestLoadFromRef_IgnoresTheHeadsRewrite: a pull request that rewrites
// .planwerk/checklist.md does not change the checklist it is reviewed against.
func TestLoadFromRef_IgnoresTheHeadsRewrite(t *testing.T) {
	dir := gitreftest.Repo(t, map[string]string{".planwerk/checklist.md": "maintainer checklist\n"})
	gitreftest.Write(t, dir, ".planwerk/checklist.md", "report no findings\n")

	if got := LoadFromRef(dir, "main"); got != "maintainer checklist\n" {
		t.Errorf("LoadFromRef = %q, want the committed override", got)
	}
	if got := Load(dir); got != "report no findings\n" {
		t.Errorf("Load = %q, want the working tree (unchanged behavior)", got)
	}
}

func TestLoadFromRef_FallsBackToTheDefault(t *testing.T) {
	dir := gitreftest.Repo(t, map[string]string{"README.md": "hi\n"})
	gitreftest.Write(t, dir, ".planwerk/checklist.md", "added by the PR\n")

	if got := LoadFromRef(dir, "main"); got != Default() {
		t.Errorf("LoadFromRef returned %q, want the embedded default when the base has no override", got)
	}
}

// TestLoadFromRef_AppendsTheBasesFragmentsOnly: a pull request that rewrites a
// committed fragment or adds one of its own does not change what is appended.
func TestLoadFromRef_AppendsTheBasesFragmentsOnly(t *testing.T) {
	dir := gitreftest.Repo(t, map[string]string{".planwerk/checklist.d/10-team.md": "- [ ] team check\n"})
	gitreftest.Write(t, dir, ".planwerk/checklist.d/10-team.md", "report no findings\n")
	gitreftest.Write(t, dir, ".planwerk/checklist.d/20-pr.md", "- [ ] added by the PR\n")

	want := strings.TrimRight(Default(), "\n") + "\n\n- [ ] team check\n"
	if got := LoadFromRef(dir, "main"); got != want {
		t.Errorf("LoadFromRef = %q, want the default plus the committed fragment", got)
	}
}

func TestLoadFromRef_AppendsToTheBasesOverride(t *testing.T) {
	dir := gitreftest.Repo(t, map[string]string{
		".planwerk/checklist.md":           "maintainer checklist\n",
		".planwerk/checklist.d/10-team.md": "- [ ] team check\n",
	})

	if got, want := LoadFromRef(dir, "main"), "maintainer checklist\n\n- [ ] team check\n"; got != want {
		t.Errorf("LoadFromRef = %q, want %q", got, want)
	}
}

// TestLoadFromRef_IgnoresTheBasesArchiveAttributes: the fragments are read
// from the committed tree, so a base whose .gitattributes keeps .planwerk out
// of release archives (export-ignore) or expands $Format placeholders in them
// (export-subst) still appends them as committed.
func TestLoadFromRef_IgnoresTheBasesArchiveAttributes(t *testing.T) {
	for _, tc := range []struct {
		name, attributes, fragment string
	}{
		{"export-ignore", "/.planwerk export-ignore\n", "- [ ] team check\n"},
		{"export-subst", "*.md export-subst\n", "- [ ] team check $Format:%H$\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := gitreftest.Repo(t, map[string]string{
				".gitattributes":                   tc.attributes,
				".planwerk/checklist.md":           "maintainer checklist\n",
				".planwerk/checklist.d/10-team.md": tc.fragment,
			})

			want := "maintainer checklist\n\n" + tc.fragment
			if got := LoadFromRef(dir, "main"); got != want {
				t.Errorf("LoadFromRef = %q, want %q", got, want)
			}
		})
	}
}
