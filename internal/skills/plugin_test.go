package skills

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// pluginRoot is the Claude Code plugin this repository ships as a marketplace:
// the interactive draft/elaborate/meta/revisit skills, three of which replaced
// the subcommands of the same names.
const pluginRoot = "../../plugins/planwerk"

// marketplaceManifest is the repo-root marketplace catalog Claude Code reads
// when a user runs `claude plugin marketplace add planwerk/planwerk-agent`.
const marketplaceManifest = "../../.claude-plugin/marketplace.json"

// wantSkills is the skill set the plugin ships, in the sorted order the
// discovered set is compared against. Adding or removing one is a deliberate
// act, so it is pinned here rather than discovered.
var wantSkills = []string{"draft", "elaborate", "meta", "revisit"}

// skillDirRef matches a `${CLAUDE_SKILL_DIR}/<path>` reference in a SKILL.md
// body. Claude Code expands the variable to the skill's own directory, so every
// captured path must resolve to a file that exists in the shipped plugin.
var skillDirRef = regexp.MustCompile(`\$\{CLAUDE_SKILL_DIR\}/([^\s` + "`" + `)]+)`)

// maxDescriptionLen bounds a skill description. Claude Code injects every
// shipped skill's description into the system prompt to route on, so the four
// descriptions are paid for on every turn of every session — whether or not a
// skill is invoked. The limit is Anthropic's documented one.
const maxDescriptionLen = 1024

// descriptionTrigger matches the "use when / use before / use after" clause a
// description needs. A description that says only what a skill does gives the
// model nothing to route on, and the skill is never chosen.
var descriptionTrigger = regexp.MustCompile(`(?i)\buse (this )?when\b|\buse (before|after|during)\b`)

// descriptionSequencing matches a step sequence in a description (", then …").
// A description must say what the skill does and when to reach for it, never how
// it proceeds: the description sits in the system prompt while the body does
// not, so a workflow summary invites the model to follow the summary instead of
// reading the skill — and a two-step gloss of a seven-phase skill drops the
// gates. This catches the one tell we shipped ("…, then file it"); it is a
// narrow heuristic, not a parser, and the doctrine in
// docs/explanation/prompt-design.md is the real rule.
var descriptionSequencing = regexp.MustCompile(`,\s+then\s`)

// TestPluginSkillsParse guards the shipped plugin the same way the golden tests
// guard the prompt builders: the skills must be discoverable, identify
// themselves, and their shared-reference paths must resolve. A renamed file
// under plugins/planwerk/shared/ otherwise breaks every skill silently, at the
// user's first invocation rather than in CI.
func TestPluginSkillsParse(t *testing.T) {
	var got []string
	for _, name := range wantSkills {
		dir := filepath.Join(pluginRoot, "skills", name)
		path := filepath.Join(dir, "SKILL.md")

		skill, ok := parseSkill(path, name)
		if !ok {
			t.Errorf("skill %q: %s has no parsable YAML frontmatter", name, path)
			continue
		}
		got = append(got, skill.Name)

		if skill.Name != name {
			t.Errorf("skill %q: frontmatter name is %q; it must match the directory name so `/planwerk:%s` resolves", name, skill.Name, name)
		}
		if skill.Description == "" {
			t.Errorf("skill %q: frontmatter carries no description; Claude Code selects a skill by its description", name)
		}
		assertDescriptionRoutes(t, name, skill.Description)

		assertSkillDirRefsResolve(t, name, dir, path)
	}

	sort.Strings(got)
	if strings.Join(got, ",") != strings.Join(wantSkills, ",") {
		t.Errorf("shipped skills = %v, want %v", got, wantSkills)
	}
}

// assertDescriptionRoutes checks the three properties a description needs to do
// its one job: fit the system-prompt budget it is charged against, carry a
// trigger the model can route on, and describe the skill rather than summarize
// its workflow.
func assertDescriptionRoutes(t *testing.T, name, description string) {
	t.Helper()
	if n := len(description); n > maxDescriptionLen {
		t.Errorf("skill %q: description is %d characters, over the %d limit; it is injected into the system prompt of every session", name, n, maxDescriptionLen)
	}
	if !descriptionTrigger.MatchString(description) {
		t.Errorf("skill %q: description carries no \"Use when …\" trigger, so nothing tells the model when to reach for it", name)
	}
	if m := descriptionSequencing.FindString(description); m != "" {
		t.Errorf("skill %q: description sequences steps (%q); say what the skill does and when to use it, never how it proceeds — the body carries the workflow", name, strings.TrimSpace(m))
	}
}

// assertSkillDirRefsResolve checks that every ${CLAUDE_SKILL_DIR}-relative path
// a SKILL.md points at exists on disk. The plugin is copied wholesale into the
// user's plugin cache, so a path that resolves here resolves there too.
func assertSkillDirRefsResolve(t *testing.T, name, dir, path string) {
	t.Helper()
	body, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Errorf("skill %q: reading %s: %v", name, path, err)
		return
	}
	refs := skillDirRef.FindAllStringSubmatch(string(body), -1)
	if len(refs) == 0 {
		t.Errorf("skill %q: references no shared documents; the house format and interaction doctrine are shared, not restated per skill", name)
		return
	}
	for _, m := range refs {
		target := filepath.Join(dir, m[1])
		if _, err := os.Stat(target); err != nil {
			t.Errorf("skill %q: references %s, which does not resolve to a file (%v)", name, m[0], err)
		}
	}
}

// TestMarketplaceManifest checks the catalog entry points at the plugin
// directory that actually exists, since a wrong `source` fails only at install
// time on a user's machine.
func TestMarketplaceManifest(t *testing.T) {
	raw, err := os.ReadFile(filepath.Clean(marketplaceManifest))
	if err != nil {
		t.Fatalf("reading %s: %v", marketplaceManifest, err)
	}
	var mkt struct {
		Name    string `json:"name"`
		Plugins []struct {
			Name   string `json:"name"`
			Source string `json:"source"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal(raw, &mkt); err != nil {
		t.Fatalf("parsing %s: %v", marketplaceManifest, err)
	}
	if mkt.Name == "" {
		t.Error("marketplace manifest has no name")
	}
	if len(mkt.Plugins) == 0 {
		t.Fatal("marketplace manifest lists no plugins")
	}
	for _, p := range mkt.Plugins {
		manifest := filepath.Join("../..", filepath.Clean(p.Source), ".claude-plugin", "plugin.json")
		if _, err := os.Stat(manifest); err != nil {
			t.Errorf("plugin %q: source %q has no .claude-plugin/plugin.json (%v)", p.Name, p.Source, err)
		}
	}
}
