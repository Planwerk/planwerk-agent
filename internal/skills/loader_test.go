package skills

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/planwerk/planwerk-agent/internal/gitref/gitreftest"
)

// writeSkill creates <root>/.claude/skills/<dir>/SKILL.md with the given body.
func writeSkill(t *testing.T, root, dir, body string) {
	t.Helper()
	skillDir := filepath.Join(root, ".claude", "skills", dir)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", skillDir, err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}
}

func TestLoad_ReturnsSkillsSortedByName(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "drift-check", "---\nname: drift-check\ndescription: Reconcile spec/code drift.\n---\n\n# Drift\nbody\n")
	writeSkill(t, root, "zeta", "---\nname: alpha-workflow\ndescription: Runs first.\n---\nbody\n")

	got := Load(root)
	if len(got) != 2 {
		t.Fatalf("want 2 skills, got %d: %+v", len(got), got)
	}
	// Sorted by frontmatter name, not directory name: alpha-workflow < drift-check.
	if got[0].Name != "alpha-workflow" || got[1].Name != "drift-check" {
		t.Fatalf("skills not sorted by name: %+v", got)
	}
	if got[1].Description != "Reconcile spec/code drift." {
		t.Fatalf("unexpected description: %q", got[1].Description)
	}
}

func TestLoad_MissingDirectoryReturnsNil(t *testing.T) {
	if got := Load(t.TempDir()); got != nil {
		t.Fatalf("want nil for repo without .claude/skills, got %+v", got)
	}
}

func TestLoad_EmptyRepoDirReturnsNil(t *testing.T) {
	if got := Load(""); got != nil {
		t.Fatalf("want nil for empty repoDir, got %+v", got)
	}
}

func TestLoad_SkipsMalformedAndFrontmatterless(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "good", "---\nname: good\ndescription: Fine.\n---\nbody\n")
	// No frontmatter fence at all.
	writeSkill(t, root, "no-fence", "# Just a heading\nno frontmatter here\n")
	// Opening fence but never closed.
	writeSkill(t, root, "unterminated", "---\nname: broken\ndescription: never closes\n")
	// Frontmatter present but not valid YAML (unclosed flow sequence).
	writeSkill(t, root, "bad-yaml", "---\nname: [unclosed\n---\nbody\n")

	got := Load(root)
	if len(got) != 1 || got[0].Name != "good" {
		t.Fatalf("want only the well-formed skill, got %+v", got)
	}
}

func TestLoad_FallsBackToDirNameWhenFrontmatterOmitsName(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "my-skill", "---\ndescription: No name field.\n---\nbody\n")

	got := Load(root)
	if len(got) != 1 || got[0].Name != "my-skill" {
		t.Fatalf("want fallback to directory name my-skill, got %+v", got)
	}
	if got[0].Description != "No name field." {
		t.Fatalf("unexpected description: %q", got[0].Description)
	}
}

func TestLoad_IgnoresLooseFilesAndMissingSKILLmd(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "real", "---\nname: real\ndescription: ok\n---\n")
	// A loose file directly under skills/ (not a skill directory).
	skillsRoot := filepath.Join(root, ".claude", "skills")
	if err := os.WriteFile(filepath.Join(skillsRoot, "README.md"), []byte("not a skill"), 0o644); err != nil {
		t.Fatalf("write loose file: %v", err)
	}
	// A directory with no SKILL.md.
	if err := os.MkdirAll(filepath.Join(skillsRoot, "empty-dir"), 0o755); err != nil {
		t.Fatalf("mkdir empty-dir: %v", err)
	}

	got := Load(root)
	if len(got) != 1 || got[0].Name != "real" {
		t.Fatalf("want only the real skill, got %+v", got)
	}
}

func TestLoad_IgnoresExtraFrontmatterKeys(t *testing.T) {
	root := t.TempDir()
	// A realistic SKILL.md carries more than name+description; the extra keys
	// must not break parsing or bleed into the loaded skill.
	writeSkill(t, root, "rich", "---\nname: rich-skill\ndescription: Has extra keys.\nallowed-tools: [Read, Grep]\nlicense: MIT\nmetadata:\n  type: reference\n---\n\n# Body\n")

	got := Load(root)
	if len(got) != 1 || got[0].Name != "rich-skill" || got[0].Description != "Has extra keys." {
		t.Fatalf("extra frontmatter keys not ignored cleanly: %+v", got)
	}
}

func TestLoad_HandlesCRLFFrontmatter(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "crlf", "---\r\nname: crlf-skill\r\ndescription: Windows line endings.\r\n---\r\nbody\r\n")

	got := Load(root)
	if len(got) != 1 || got[0].Name != "crlf-skill" {
		t.Fatalf("want crlf-skill parsed, got %+v", got)
	}
	if got[0].Description != "Windows line endings." {
		t.Fatalf("unexpected description: %q", got[0].Description)
	}
}

func TestLoad_SkipsSkillsClosedToModelInvocation(t *testing.T) {
	root := t.TempDir()
	// Claude Code removes a skill with disable-model-invocation from the
	// model's context and blocks the Skill tool from running it, so the
	// prompt must not oblige a session to invoke one. The key is read the
	// way Claude Code reads it: true, yes, on, and 1 in any case set it.
	writeSkill(t, root, "closed-true", "---\nname: closed-true\ndescription: Author only.\ndisable-model-invocation: true\n---\nbody\n")
	writeSkill(t, root, "closed-yes", "---\nname: closed-yes\ndescription: Author only.\ndisable-model-invocation: Yes\n---\nbody\n")
	writeSkill(t, root, "closed-one", "---\nname: closed-one\ndescription: Author only.\ndisable-model-invocation: 1\n---\nbody\n")
	writeSkill(t, root, "open-false", "---\nname: open-false\ndescription: Model may invoke.\ndisable-model-invocation: false\n---\nbody\n")
	writeSkill(t, root, "open-absent", "---\nname: open-absent\ndescription: Model may invoke.\n---\nbody\n")

	got := Load(root)
	if len(got) != 2 || got[0].Name != "open-absent" || got[1].Name != "open-false" {
		t.Fatalf("want only the skills open to model invocation, got %+v", got)
	}
}

// TestLoadShared_ListsTheUnchangedAndNamesTheRest is the property the function
// exists for: a session in a pull request's checkout is handed the skills the
// pull request left alone, and the names of the ones it added or changed, so
// the caller can deny those to the Skill tool; a removed skill is in neither.
func TestLoadShared_ListsTheUnchangedAndNamesTheRest(t *testing.T) {
	dir := gitreftest.Repo(t, map[string]string{
		".claude/skills/kept/SKILL.md":    "---\nname: kept\ndescription: Kept as it was.\n---\n\nBody.\n",
		".claude/skills/edited/SKILL.md":  "---\nname: edited\ndescription: At the base.\n---\n\nBase body.\n",
		".claude/skills/removed/SKILL.md": "---\nname: removed\ndescription: Gone on the head.\n---\n\nBody.\n",
	})
	writeSkill(t, dir, "edited", "---\nname: edited\ndescription: At the base.\n---\n\nThe pull request's body.\n")
	writeSkill(t, dir, "added", "---\nname: added-alias\ndescription: New on the head.\n---\n\nBody.\n")
	if err := os.RemoveAll(filepath.Join(dir, ".claude", "skills", "removed")); err != nil {
		t.Fatal(err)
	}

	shared, changed := LoadShared(dir, "main")
	if want := []Skill{{Name: "kept", Description: "Kept as it was."}}; !reflect.DeepEqual(shared, want) {
		t.Errorf("shared = %+v, want %+v", shared, want)
	}
	if want := []string{"added", "added-alias", "edited"}; !slices.Equal(changed, want) {
		t.Errorf("changed = %v, want %v", changed, want)
	}
}

// TestLoadShared_UnreadableRefNamesEveryWorkingTreeSkill: without a base to
// vouch for a skill, nothing is shared and every skill is named, so a session
// never follows a skill the base cannot be read for.
func TestLoadShared_UnreadableRefNamesEveryWorkingTreeSkill(t *testing.T) {
	dir := gitreftest.Repo(t, map[string]string{
		".claude/skills/kept/SKILL.md": "---\nname: kept\ndescription: Kept.\n---\n\nBody.\n",
	})
	shared, changed := LoadShared(dir, "no-such-ref")
	if shared != nil {
		t.Errorf("shared = %+v, want none", shared)
	}
	if want := []string{"kept"}; !slices.Equal(changed, want) {
		t.Errorf("changed = %v, want %v", changed, want)
	}
	if s, c := LoadShared(t.TempDir(), "main"); s != nil || c != nil {
		t.Errorf("a checkout without skills = %+v, %v; want nothing", s, c)
	}
}
