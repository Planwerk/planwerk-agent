package skills

import (
	"os"
	"path/filepath"
	"testing"
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
