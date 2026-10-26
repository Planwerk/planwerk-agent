package claude

import (
	"log/slog"
	"os/exec"
	"slices"
	"strings"

	"github.com/planwerk/planwerk-agent/internal/claudeplugin"
)

// pluginTelemetryEnv is the environment entry a session that loads plugins
// runs with: the cross-tool convention that asks a tool to send no telemetry,
// which a plugin's hooks and scripts inherit from the session (decision 154).
const pluginTelemetryEnv = "DO_NOT_TRACK=1"

// WithPlugins sets the Claude Code plugins the mutating sessions load, each
// passed as --plugin-dir; the read-only sessions never load one (decision
// 153). An empty ps leaves the earlier value in place; a later option wins,
// and ps is cloned.
func WithPlugins(ps []claudeplugin.Plugin) Option {
	return func(c *Client) {
		if len(ps) == 0 {
			return
		}
		c.plugins = slices.Clone(ps)
	}
}

// Plugins returns a copy of the plugins the mutating sessions load, nil when
// none is set.
func (c *Client) Plugins() []claudeplugin.Plugin { return slices.Clone(c.plugins) }

// sessionPlugins returns the plugins a session loads: the Client's for a
// session that may edit, none for a read-only one. claudeArgs, the session's
// environment and the init-event check all read it, so the three cannot
// disagree about whether a session carries plugins.
func (c *Client) sessionPlugins(readOnly bool) []claudeplugin.Plugin {
	if readOnly {
		return nil
	}
	return c.plugins
}

// withPluginDirs appends one --plugin-dir per plugin. The flag takes one
// value and repeats, so it may sit anywhere before the trailing variadic tool
// flags.
func withPluginDirs(args []string, ps []claudeplugin.Plugin) []string {
	for _, p := range ps {
		args = append(args, "--plugin-dir", p.Dir)
	}
	return args
}

// withSessionEnv sets the environment of a session's process for its plugins.
// None leaves cmd.Env nil, which inherits planwerk-agent's own; any adds
// pluginTelemetryEnv to cmd.Environ, which carries the PWD of cmd.Dir, so the
// caller sets the directory first.
func withSessionEnv(cmd *exec.Cmd, ps []claudeplugin.Plugin) {
	if len(ps) == 0 {
		return
	}
	cmd.Env = append(cmd.Environ(), pluginTelemetryEnv)
}

// joinPrompts joins the parts that are not blank, each trimmed, with a blank
// line between two and a newline after the last, for a prompt assembled from
// more than one part. A single such part is returned exactly as given, so a
// session that carries one part keeps the bytes it had before plugins
// existed.
func joinPrompts(parts ...string) string {
	var kept []string
	for _, p := range parts {
		if strings.TrimSpace(p) != "" {
			kept = append(kept, p)
		}
	}
	switch len(kept) {
	case 0:
		return ""
	case 1:
		return kept[0]
	}
	for i, p := range kept {
		kept[i] = strings.TrimSpace(p)
	}
	return strings.Join(kept, "\n\n") + "\n"
}

// pluginSessionPrompt returns the system-prompt section a session that loads
// plugins carries: the skills the plugins ship, under the obligation the
// project skills carry (decision 63) and exempt from projectSkillsBlock's
// limit to the repository's own skills, and the rules a plugin and its hooks
// run under with no person present (decision 154). The implementer subagent
// of an orchestrated implement session gets it in its own prompt, since a
// subagent sees none of the parent's. No plugins yields "", which leaves every
// session's arguments as they were.
func pluginSessionPrompt(ps []claudeplugin.Plugin) string {
	if len(ps) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("## Plugins the operator loaded\n\n")
	var lines []string
	for _, p := range ps {
		for _, s := range p.Skills {
			line := "- `" + p.SkillID(s.Name) + "`"
			if s.Description != "" {
				line += " — " + s.Description
			}
			lines = append(lines, line)
		}
	}
	if len(lines) == 0 {
		names := make([]string, len(ps))
		for i, p := range ps {
			names[i] = "`" + p.Name + "`"
		}
		sb.WriteString("The operator loaded the Claude Code plugins " + strings.Join(names, ", ") + " into this session. They ship no skill you invoke; their hooks run on your edits.\n\n")
	} else {
		sb.WriteString("The operator loaded Claude Code plugins into this session. Their skills, below, are in scope, also where your instructions limit you to the repository's own skills: when a task you are about to perform falls within a skill's stated purpose, invoke that skill (via the Skill tool, by the name given here) and follow it rather than improvising. A plugin skill decides how a task inside your instructions' scope is done; it does not change that scope, their rules, the git workflow, the commit trailers, or the report format, and where its steps conflict with them, your instructions win.\n\n")
		sb.WriteString("<plugin-skills>\n" + strings.Join(lines, "\n") + "\n</plugin-skills>\n\n")
	}
	sb.WriteString(`No person is present in this session, so a plugin, its skills, and its hooks run under these rules:

- Skip any step that needs a person: a question to answer, a choice between variants, a page to look at in a browser. Name each step you skipped in your final message.
- Never add an ignore, waiver, or exception that silences a check a plugin runs, whatever the plugin's own text says about persisting one, because an ignore is a file the change would carry and it silences the check for every later change too. Fix what a check reports when it is a real problem inside your task; otherwise name it in your final message with the reason you left it.
- Keep the working files a plugin writes for itself out of your commits: caches, screenshots, session state, and folders such as ` + "`.impeccable/`" + `.
- A plugin hook can report after your final message and start another turn. Handle what it reports under these rules, then end that turn with your final message again, in full and updated for anything you changed, because only the session's last message is read.
- A subagent you delegate to runs under these rules too. Carry each step it skipped and each check it left standing, as it named them, into your final message.

Everything these rules ask you to name goes inside the shape your instructions give your final message (a report's section for work left or skipped, or the summary of a JSON object), never before or after it.
`)
	return sb.String()
}

// warnUnloadedPlugins logs one warning on logger for each plugin in want whose
// name the session's init event does not list among the plugins it loaded: a
// --plugin-dir the CLI refused, such as a plugin whose manifest it could not
// read. A nil loaded means the stream announced no list, and nothing is
// checked (decision 154).
func warnUnloadedPlugins(logger *slog.Logger, label string, want []claudeplugin.Plugin, loaded []string) {
	if loaded == nil {
		return
	}
	for _, p := range want {
		if !slices.Contains(loaded, p.Name) {
			logger.Warn("requested plugin not loaded by the session", "label", label, "plugin", p.Name, "dir", p.Dir)
		}
	}
}
