// Package skills discovers the Claude Code Agent Skills a target repository
// ships under .claude/skills/ so the orchestrated implement/fix/address prompts
// can list them and oblige the session to use a matching one instead of
// improvising. It reads only the identifying frontmatter (name + description) —
// enough for the prompt to name the skill and say what it is for; the full
// SKILL.md body loads lazily when the session invokes the skill.
//
// Discovery is scoped to the repo's own .claude/skills/ — never the invoking
// user's ~/.claude/skills — so the set is reproducible from the checkout alone,
// in keeping with the hermetic-session design (design decision #45).
package skills

import (
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"
)

// maxSkillsInPrompt caps how many discovered skills are injected into a prompt.
// A repo's own .claude/skills/ is naturally small, so this is a defensive bound
// against prompt bloat rather than a routine limit; when it trips, the extra
// skills are dropped (in name order) and a warning is logged. Mirrors the
// patterns package's prompt-budget truncation.
const maxSkillsInPrompt = 40

// Skill is a project-provided Agent Skill discovered in the target repo's
// .claude/skills/ directory. Only the identity the prompt needs is loaded.
type Skill struct {
	Name        string
	Description string
}

// skillFrontmatter is the subset of a SKILL.md YAML frontmatter we read.
type skillFrontmatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

// Load discovers the Agent Skills committed under <repoDir>/.claude/skills/ and
// returns them sorted by name. Each skill lives in its own directory with a
// SKILL.md carrying YAML frontmatter (name, description); Load reads only that
// frontmatter.
//
// It is best-effort: a missing directory returns nil, and an unreadable or
// malformed SKILL.md is skipped (and logged) rather than failing the caller —
// the same non-fatal posture the pattern loader takes, so a corrupt skill never
// blocks an implementation. The result is capped at maxSkillsInPrompt.
func Load(repoDir string) []Skill {
	if repoDir == "" {
		return nil
	}
	root := filepath.Join(repoDir, ".claude", "skills")
	entries, err := os.ReadDir(root)
	if err != nil {
		if !os.IsNotExist(err) {
			slog.Warn("reading skills directory failed; continuing without project skills", "dir", root, "err", err)
		}
		return nil
	}

	var out []Skill
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		path := filepath.Join(root, e.Name(), "SKILL.md")
		if s, ok := parseSkill(path, e.Name()); ok {
			out = append(out, s)
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	if len(out) > maxSkillsInPrompt {
		slog.Warn("project skills truncated for prompt budget", "loaded", len(out), "kept", maxSkillsInPrompt)
		out = out[:maxSkillsInPrompt]
	}
	if len(out) > 0 {
		slog.Info("loaded project skills", "count", len(out))
	}
	return out
}

// parseSkill reads name + description from a SKILL.md's YAML frontmatter. The
// skill directory name is the fallback identity when the frontmatter omits
// name. Returns ok=false when the file is absent, carries no frontmatter, or
// yields no usable name.
func parseSkill(path, dirName string) (Skill, bool) {
	content, err := os.ReadFile(path)
	if err != nil {
		return Skill{}, false
	}
	fm, ok := extractFrontmatter(string(content))
	if !ok {
		return Skill{}, false
	}
	var meta skillFrontmatter
	if err := yaml.Unmarshal([]byte(fm), &meta); err != nil {
		slog.Warn("parsing SKILL.md frontmatter failed; skipping skill", "path", path, "err", err)
		return Skill{}, false
	}
	name := strings.TrimSpace(meta.Name)
	if name == "" {
		name = strings.TrimSpace(dirName)
	}
	if name == "" {
		return Skill{}, false
	}
	return Skill{Name: name, Description: strings.TrimSpace(meta.Description)}, true
}

// extractFrontmatter returns the YAML block a SKILL.md opens with, delimited by
// a leading "---" fence and the next "---" line. ok is false when the content
// does not open with a fence or the closing fence is missing. CRLF is
// normalized first so a Windows-authored SKILL.md parses the same.
func extractFrontmatter(content string) (string, bool) {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	rest, ok := strings.CutPrefix(content, "---\n")
	if !ok {
		return "", false
	}
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return "", false
	}
	return rest[:end], true
}
