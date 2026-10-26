package claude

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"slices"
	"strings"
	"testing"

	"github.com/planwerk/planwerk-agent/internal/claudeplugin"
	"github.com/planwerk/planwerk-agent/internal/implement"
)

// testPlugins is a plugin with one skill and a plugin with none.
var testPlugins = []claudeplugin.Plugin{
	{Dir: "/plugins/design", Name: "design", Version: "4.7.1", Skills: []claudeplugin.Skill{{Name: "design", Description: "Polish an interface."}}},
	{Dir: "/plugins/hooks-only", Name: "hooks-only"},
}

// flagValues returns the value after every occurrence of flag in args.
func flagValues(args []string, flag string) []string {
	var out []string
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			out = append(out, args[i+1])
		}
	}
	return out
}

// TestClaudeArgs_PluginsReachOnlyTheMutatingSessions locks decision 153: a
// session that may edit loads every plugin through --plugin-dir and carries
// the plugin section in its system prompt; a read-only or structuring session
// loads none and carries no section.
func TestClaudeArgs_PluginsReachOnlyTheMutatingSessions(t *testing.T) {
	t.Parallel()
	c := NewClient(WithPlugins(testPlugins))

	args := c.claudeArgs(runSpec{model: "opus", effort: "xhigh", permissionMode: claudeAutoPermissionMode})
	if got := flagValues(args, "--plugin-dir"); !slices.Equal(got, []string{"/plugins/design", "/plugins/hooks-only"}) {
		t.Errorf("mutating session --plugin-dir values = %v, want both plugin roots in order", got)
	}
	sys := flagValues(args, "--append-system-prompt")
	if len(sys) != 1 || !strings.Contains(sys[0], "`design:design` — Polish an interface.") {
		t.Errorf("mutating session system prompt = %q, want the plugin skill listed", sys)
	}

	for _, spec := range []runSpec{
		{model: "opus", effort: "xhigh", readOnly: true},
		{model: "sonnet", effort: "medium", readOnly: true, noTools: true},
	} {
		args := c.claudeArgs(spec)
		if slices.Contains(args, "--plugin-dir") || slices.Contains(args, "--append-system-prompt") {
			t.Errorf("read-only spec %+v loaded plugins: %v", spec, args)
		}
	}
}

// TestClaudeArgs_ImplementKeepsItsSystemPromptFirst: the implement session's
// status contract (decision 108) and the plugin section share the one
// --append-system-prompt value, the contract first.
func TestClaudeArgs_ImplementKeepsItsSystemPromptFirst(t *testing.T) {
	t.Parallel()
	c := NewClient(WithPlugins(testPlugins))
	args := c.claudeArgs(runSpec{model: "opus", effort: "xhigh", permissionMode: claudeAutoPermissionMode, appendSystemPrompt: ImplementSystemPrompt()})
	sys := flagValues(args, "--append-system-prompt")
	if len(sys) != 1 {
		t.Fatalf("want one --append-system-prompt, got %d: %v", len(sys), args)
	}
	contract := strings.TrimSpace(ImplementSystemPrompt())
	if !strings.HasPrefix(sys[0], contract+"\n\n## Plugins the operator loaded") {
		t.Errorf("system prompt does not open with the status contract followed by the plugin section:\n%s", sys[0])
	}

	plain := NewClient().claudeArgs(runSpec{model: "opus", effort: "xhigh", permissionMode: claudeAutoPermissionMode, appendSystemPrompt: ImplementSystemPrompt()})
	if got := flagValues(plain, "--append-system-prompt"); len(got) != 1 || got[0] != ImplementSystemPrompt() {
		t.Errorf("without plugins the system prompt must stay byte for byte ImplementSystemPrompt(); got %q", got)
	}
}

// TestClaudeCommand_PluginSessionsAskForNoTelemetry: a session that loads
// plugins runs with DO_NOT_TRACK=1, every other one inherits the environment
// untouched (decision 154).
func TestClaudeCommand_PluginSessionsAskForNoTelemetry(t *testing.T) {
	t.Parallel()
	c := NewClient(WithPlugins(testPlugins))
	dir := t.TempDir()
	mutating := c.claudeCommand(t.Context(), runSpec{dir: dir, model: "opus", effort: "xhigh", permissionMode: claudeAutoPermissionMode}, "p")
	if !slices.Contains(mutating.Env, pluginTelemetryEnv) {
		t.Errorf("a session with plugins must run with %s", pluginTelemetryEnv)
	}
	if !slices.Contains(mutating.Env, "PWD="+dir) {
		t.Errorf("a session with plugins must carry the PWD of its checkout %s", dir)
	}
	readOnly := c.claudeCommand(t.Context(), runSpec{model: "opus", effort: "xhigh", readOnly: true}, "p")
	if readOnly.Env != nil {
		t.Errorf("a read-only session must inherit the environment untouched, got %d entries", len(readOnly.Env))
	}
	plain := NewClient().claudeCommand(t.Context(), runSpec{model: "opus", effort: "xhigh", permissionMode: claudeAutoPermissionMode}, "p")
	if plain.Env != nil {
		t.Errorf("a session without plugins must inherit the environment untouched")
	}
}

func TestPluginSessionPrompt(t *testing.T) {
	t.Parallel()
	if got := pluginSessionPrompt(nil); got != "" {
		t.Errorf("no plugins rendered %q, want nothing", got)
	}
	full := pluginSessionPrompt(testPlugins)
	for _, want := range []string{
		"<plugin-skills>\n- `design:design` — Polish an interface.\n</plugin-skills>",
		"your instructions win",
		"Skip any step that needs a person",
		"Never add an ignore, waiver, or exception that silences a check a plugin runs",
		"`.impeccable/`",
		"end that turn with your final message again, in full and updated for anything you changed",
		"Everything these rules ask you to name goes inside the shape your instructions give your final message",
	} {
		if !strings.Contains(full, want) {
			t.Errorf("plugin section lacks %q:\n%s", want, full)
		}
	}
	hooksOnly := pluginSessionPrompt(testPlugins[1:])
	if strings.Contains(hooksOnly, "<plugin-skills>") || !strings.Contains(hooksOnly, "`hooks-only`") || !strings.Contains(hooksOnly, "No person is present") {
		t.Errorf("a plugin without skills must be named, keep the rules, and list no skills:\n%s", hooksOnly)
	}
}

func TestJoinPrompts(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		parts []string
		want  string
	}{
		{nil, ""},
		{[]string{"", " \n"}, ""},
		{[]string{"one\n", ""}, "one\n"},
		{[]string{"one\n", "two\n"}, "one\n\ntwo\n"},
	} {
		if got := joinPrompts(tc.parts...); got != tc.want {
			t.Errorf("joinPrompts(%q) = %q, want %q", tc.parts, got, tc.want)
		}
	}
}

func TestWarnUnloadedPlugins(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	warnUnloadedPlugins(logger, "implement", testPlugins, []string{"design", "cc-plugin-telemetry"})
	if out := buf.String(); !strings.Contains(out, "plugin=hooks-only") || strings.Contains(out, "plugin=design") {
		t.Errorf("want one warning, for hooks-only; got:\n%s", out)
	}
	buf.Reset()
	warnUnloadedPlugins(logger, "implement", testPlugins, nil)
	if buf.Len() != 0 {
		t.Errorf("a stream without a plugin list must not warn; got:\n%s", buf.String())
	}
}

func TestHandleStreamLine_RecordsTheLoadedPlugins(t *testing.T) {
	t.Parallel()
	out := &streamOutcome{}
	line := `{"type":"system","subtype":"init","plugins":[{"name":"impeccable","path":"/c/impeccable/4.7.1","version":"4.7.1"},{"name":"cc-plugin-telemetry","path":"builtin"}]}`
	handleStreamLine([]byte(line), "implement", quietStreamSink{}, out)
	if !slices.Equal(out.loadedPlugins, []string{"impeccable", "cc-plugin-telemetry"}) {
		t.Errorf("loadedPlugins = %v, want both names in order", out.loadedPlugins)
	}

	// A null list is no list: warnUnloadedPlugins must then check nothing.
	null := &streamOutcome{}
	handleStreamLine([]byte(`{"type":"system","subtype":"init","plugins":null}`), "implement", quietStreamSink{}, null)
	if null.loadedPlugins != nil {
		t.Errorf("a null plugin list recorded %#v, want nil", null.loadedPlugins)
	}
}

// TestImplementAgentsJSON_WorkerCarriesThePluginRules: the implementer
// subagent runs the plugins' hooks too but sees none of the parent's system
// prompt, so its own prompt ends with the plugin section; without plugins it
// is buildImplementerAgentPrompt's (decision 154).
func TestImplementAgentsJSON_WorkerCarriesThePluginRules(t *testing.T) {
	t.Parallel()
	ctx := implement.Context{WorkerModel: "sonnet"}
	raw, err := implementAgentsJSON(ctx, testPlugins)
	if err != nil {
		t.Fatal(err)
	}
	var defs map[string]agentDefinition
	if err := json.Unmarshal([]byte(raw), &defs); err != nil {
		t.Fatal(err)
	}
	got := defs[implementerAgentName].Prompt
	if !strings.HasPrefix(got, strings.TrimSpace(buildImplementerAgentPrompt(""))) || !strings.HasSuffix(got, pluginSessionPrompt(testPlugins)) {
		t.Errorf("worker prompt must be the implementer prompt followed by the plugin section:\n%s", got)
	}
}
