// Package claudeplugin resolves the Claude Code plugins an operator loads into
// the mutating sessions with --claude-plugin (decision 153). A plugin is named
// either by the id Claude Code installed it under ("impeccable@impeccable"),
// which resolves to the installed version's cache directory as `claude plugin
// list --json` reports it, or by its root directory. The sessions run with
// --setting-sources project (decision 45), which drops the user settings that
// enable an installed plugin, so a plugin reaches a session only through the
// --plugin-dir flag the runner passes for each one resolved here.
//
// Load reads what the prompts need from a plugin: its manifest's name and
// version, and the model-invocable skills it ships. It reads nothing else and
// runs nothing from the plugin.
package claudeplugin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/planwerk/planwerk-agent/internal/skills"
)

// listTimeout bounds `claude plugin list --json`, which reads local files and
// starts no session.
const listTimeout = 30 * time.Second

// manifestPath is where a plugin root keeps its manifest.
const manifestPath = ".claude-plugin/plugin.json"

// defaultSkillsDir is the skills directory Claude Code reads in every plugin;
// a manifest's own skills paths add to it rather than replace it.
const defaultSkillsDir = "skills"

// installID is the shape of an installed plugin's id: the plugin's name, "@",
// and the marketplace it came from.
var installID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*@[A-Za-z0-9][A-Za-z0-9._-]*$`)

// Plugin is one plugin a mutating session loads.
type Plugin struct {
	// Dir is the plugin's root directory, absolute: the directory that holds
	// .claude-plugin/plugin.json, which the runner passes to --plugin-dir.
	Dir string
	// Name and Version are the manifest's. Version is "" when the manifest
	// names none.
	Name    string
	Version string
	// Skills are the skills the plugin ships that the model may invoke,
	// sorted by name.
	Skills []Skill
}

// Skill is one skill a plugin ships.
type Skill struct {
	// Name is the skill's own name; the Skill tool knows it as
	// Plugin.SkillID(Name).
	Name        string
	Description string
	// Dir is the directory that holds the skill's SKILL.md.
	Dir string
}

// SkillID returns the name the Skill tool knows the plugin's skill name by:
// the plugin's name, a colon, and the skill's.
func (p Plugin) SkillID(name string) string { return p.Name + ":" + name }

// Label returns "<name>@<version>", or the name alone when the manifest names
// no version. The run fingerprint and the log lines carry it.
func (p Plugin) Label() string {
	if p.Version == "" {
		return p.Name
	}
	return p.Name + "@" + p.Version
}

// Installed is one entry of `claude plugin list --json`.
type Installed struct {
	ID          string `json:"id"`
	Version     string `json:"version"`
	InstallPath string `json:"installPath"`
}

// Lister returns the plugins Claude Code has installed. ListInstalled is the
// production one; tests pass their own.
type Lister func() ([]Installed, error)

// ListInstalled runs `claude plugin list --json` and decodes its output.
func ListInstalled() ([]Installed, error) {
	ctx, cancel := context.WithTimeout(context.Background(), listTimeout)
	defer cancel()
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "claude", "plugin", "list", "--json")
	cmd.Stderr = &stderr
	cmd.WaitDelay = 2 * time.Second
	out, err := cmd.Output()
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return nil, fmt.Errorf("claude plugin list --json: %w: %s", err, msg)
		}
		return nil, fmt.Errorf("claude plugin list --json: %w", err)
	}
	var installed []Installed
	if err := json.Unmarshal(out, &installed); err != nil {
		return nil, fmt.Errorf("decoding claude plugin list --json: %w", err)
	}
	return installed, nil
}

// Resolve resolves each value to the plugin it names, in order. A value that
// is a directory is read as a plugin root; a value of the form
// name@marketplace is looked up among the installed plugins, which list is
// called for at most once, and its install path is read as the plugin root;
// a manifest that names no version takes the installed one, so the run's
// fingerprint moves when the plugin is updated. Blank values are skipped, and
// a value that resolves to a plugin already resolved adds nothing. Any other
// value, an id that is not installed, a directory that is not a plugin, or a
// second plugin of a name already resolved fails the whole call: a run the
// operator asked to load a plugin into must not start without it, and Claude
// Code loads only one plugin of a name.
func Resolve(values []string, list Lister) ([]Plugin, error) {
	var (
		out       []Plugin
		installed []Installed
		listed    bool
	)
	for _, raw := range values {
		v := strings.TrimSpace(raw)
		if v == "" {
			continue
		}
		var dir, installedVersion string
		switch fi, err := os.Stat(v); {
		case err == nil && fi.IsDir():
			dir = v
		case installID.MatchString(v):
			if !listed {
				var err error
				if installed, err = list(); err != nil {
					return nil, fmt.Errorf("--claude-plugin %s: listing the installed plugins: %w", v, err)
				}
				listed = true
			}
			in, err := installedPlugin(v, installed)
			if err != nil {
				return nil, err
			}
			dir, installedVersion = in.InstallPath, in.Version
		default:
			return nil, fmt.Errorf("--claude-plugin %s: neither a plugin directory nor an installed plugin id (name@marketplace)", v)
		}
		p, err := Load(dir)
		if err != nil {
			return nil, fmt.Errorf("--claude-plugin %s: %w", v, err)
		}
		if p.Version == "" {
			p.Version = strings.TrimSpace(installedVersion)
		}
		if slices.ContainsFunc(out, func(q Plugin) bool { return q.Dir == p.Dir }) {
			continue
		}
		if slices.ContainsFunc(out, func(q Plugin) bool { return q.Name == p.Name }) {
			return nil, fmt.Errorf("--claude-plugin %s: a plugin named %s is already loaded from another directory", v, p.Name)
		}
		out = append(out, p)
	}
	return out, nil
}

// installedPlugin returns the entry of the installed plugin id, or an error
// that lists the ids that are installed.
func installedPlugin(id string, installed []Installed) (Installed, error) {
	ids := make([]string, 0, len(installed))
	for _, in := range installed {
		if in.ID == id {
			if in.InstallPath == "" {
				return Installed{}, fmt.Errorf("--claude-plugin %s: claude plugin list reports no install path for it", id)
			}
			return in, nil
		}
		ids = append(ids, in.ID)
	}
	if len(ids) == 0 {
		return Installed{}, fmt.Errorf("--claude-plugin %s: not installed, and no plugin is (claude plugin install %s)", id, id)
	}
	slices.Sort(ids)
	return Installed{}, fmt.Errorf("--claude-plugin %s: not installed; installed: %s", id, strings.Join(ids, ", "))
}

// manifest is the part of .claude-plugin/plugin.json Load reads. Skills is a
// path or a list of paths, relative to the plugin root.
type manifest struct {
	Name    string          `json:"name"`
	Version string          `json:"version"`
	Skills  json.RawMessage `json:"skills"`
}

// Load reads the plugin whose root is dir: its manifest's name and version,
// and the skills under the default skills directory and every skills path the
// manifest names. A skill path that leaves the plugin root is an error, as is
// a directory without a manifest or a manifest without a name. A skill whose
// SKILL.md does not parse, or that is closed to model invocation, is left out,
// the way skills.Load leaves out a project skill.
func Load(dir string) (Plugin, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return Plugin{}, fmt.Errorf("resolving %s: %w", dir, err)
	}
	data, err := os.ReadFile(filepath.Join(abs, filepath.FromSlash(manifestPath)))
	if errors.Is(err, os.ErrNotExist) {
		return Plugin{}, fmt.Errorf("%s is not a Claude Code plugin: it has no %s", abs, manifestPath)
	}
	if err != nil {
		return Plugin{}, fmt.Errorf("reading the manifest of %s: %w", abs, err)
	}
	var m manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return Plugin{}, fmt.Errorf("decoding %s in %s: %w", manifestPath, abs, err)
	}
	name := strings.TrimSpace(m.Name)
	if name == "" {
		return Plugin{}, fmt.Errorf("%s in %s names no plugin", manifestPath, abs)
	}
	roots, err := skillRoots(abs, m.Skills)
	if err != nil {
		return Plugin{}, err
	}
	return Plugin{Dir: abs, Name: name, Version: strings.TrimSpace(m.Version), Skills: loadSkills(roots)}, nil
}

// skillRoots returns the absolute skills directories of the plugin at root:
// the default one, then each the manifest's skills field names, without
// repeats. The field may be a string or a list of strings.
func skillRoots(root string, field json.RawMessage) ([]string, error) {
	paths := []string{defaultSkillsDir}
	if len(field) > 0 && string(field) != "null" {
		var one string
		var many []string
		switch {
		case json.Unmarshal(field, &one) == nil:
			paths = append(paths, one)
		case json.Unmarshal(field, &many) == nil:
			paths = append(paths, many...)
		default:
			return nil, fmt.Errorf("%s in %s: skills must be a path or a list of paths", manifestPath, root)
		}
	}
	var roots []string
	for _, p := range paths {
		abs := filepath.Join(root, filepath.FromSlash(p))
		rel, err := filepath.Rel(root, abs)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("%s in %s: skills path %q leaves the plugin", manifestPath, root, p)
		}
		if !slices.Contains(roots, abs) {
			roots = append(roots, abs)
		}
	}
	return roots, nil
}

// loadSkills reads the skills of every directory under roots that holds a
// SKILL.md, sorted by name; a root that does not exist adds none. A skill
// directory may be a symlink, as a plugin under development often links its
// skills in.
func loadSkills(roots []string) []Skill {
	var out []Skill
	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, e := range entries {
			dir := filepath.Join(root, e.Name())
			if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
				continue
			}
			s, ok := skills.ParseFile(filepath.Join(dir, "SKILL.md"))
			if !ok || slices.ContainsFunc(out, func(o Skill) bool { return o.Name == s.Name }) {
				continue
			}
			out = append(out, Skill{Name: s.Name, Description: s.Description, Dir: dir})
		}
	}
	slices.SortFunc(out, func(a, b Skill) int { return strings.Compare(a.Name, b.Name) })
	return out
}
