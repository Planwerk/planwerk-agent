package claude

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/planwerk/planwerk-agent/internal/report"
)

// fakeClaude puts an executable named `claude` on PATH for the duration of the
// test. body is the shell script that stands in for the CLI, so a test can
// script an exact event stream on stdout, stderr output, exit code, or a hang.
// Without it the runner is never exercised against a real process, which is
// how the timeout diagnosis and the usage accounting on a failed turn both went
// unseen.
func fakeClaude(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "claude"), []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatalf("writing fake claude: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// failureEnvelope is what Claude Code writes to stdout when a turn fails: the
// result event with is_error set and a usage block, followed by a non-zero
// exit.
const failureEnvelope = `{"type":"result","is_error":true,"subtype":"error_max_turns",` +
	`"usage":{"input_tokens":1200,"output_tokens":300,"cache_read_input_tokens":40},` +
	`"total_cost_usd":0.42}` + "\n"

func TestRunSession_TimeoutNamesTheDeadline(t *testing.T) {
	fakeClaude(t, "sleep 30\n")
	c := NewClient(WithTimeout(200 * time.Millisecond))

	start := time.Now()
	_, _, err := c.runSession(runSpec{label: "review", model: "opus", effort: "xhigh"}, "prompt")
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected an error when the invocation outlives the timeout")
	}
	// The kill reaches `claude` but not the grandchild holding the stdout pipe,
	// so without WaitDelay this returns only when the 30s sleep ends.
	if elapsed > 10*time.Second {
		t.Errorf("returned after %s: the deadline did not bound the wait", elapsed)
	}
	if !strings.Contains(err.Error(), "timed out after") {
		t.Errorf("error = %q, want it to name the timeout rather than the kill signal", err)
	}
	if strings.Contains(err.Error(), "signal: killed") {
		t.Errorf("error = %q still reports the kill signal, which reads like a crashed binary", err)
	}
}

func TestRunClaudeStream_TimeoutNamesTheDeadline(t *testing.T) {
	fakeClaude(t, "sleep 30\n")
	c := NewClient(WithTimeout(200*time.Millisecond), WithShowOutput(true))

	start := time.Now()
	_, _, err := c.runSession(runSpec{label: "review", model: "opus", effort: "xhigh"}, "prompt")
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected an error when the streaming invocation outlives the timeout")
	}
	if elapsed > 10*time.Second {
		t.Errorf("returned after %s: the deadline did not bound the wait", elapsed)
	}
	if !strings.Contains(err.Error(), "timed out after") {
		t.Errorf("error = %q, want it to name the timeout", err)
	}
}

func TestRunSession_CountsUsageOfAFailedTurn(t *testing.T) {
	fakeClaude(t, "cat >/dev/null\nprintf '%s' '"+failureEnvelope+"'\nexit 1\n")
	c := NewClient()

	_, _, err := c.runSession(runSpec{label: "implement", model: "opus", effort: "xhigh"}, "prompt")
	if err == nil {
		t.Fatal("expected an error for a failed turn")
	}
	if !strings.Contains(err.Error(), "error_max_turns") {
		t.Errorf("error = %q, want the envelope's reason", err)
	}

	got := c.UsageTotals()
	if got.Calls != 1 {
		t.Errorf("Calls = %d, want 1 — a failed turn still spent its tokens", got.Calls)
	}
	if got.InputTokens != 1200 || got.OutputTokens != 300 || got.CacheReadTokens != 40 {
		t.Errorf("usage = %+v, want the envelope's counts", got)
	}
	if got.CostUSD != 0.42 {
		t.Errorf("CostUSD = %v, want 0.42", got.CostUSD)
	}
	if len(got.Passes) != 1 || got.Passes[0].Pass != "implement" {
		t.Errorf("passes = %+v, want the spend attributed to the implement pass", got.Passes)
	}
}

func TestRunClaudeStream_CountsUsageOfAFailedTurn(t *testing.T) {
	event := `{"type":"result","is_error":true,"subtype":"error_max_turns",` +
		`"usage":{"input_tokens":900,"output_tokens":100},"total_cost_usd":0.25}`
	fakeClaude(t, "cat >/dev/null\nprintf '%s\\n' '"+event+"'\nexit 1\n")
	c := NewClient(WithShowOutput(true))

	_, _, err := c.runSession(runSpec{label: "fix", model: "opus", effort: "xhigh"}, "prompt")
	if err == nil {
		t.Fatal("expected an error for a failed turn")
	}
	got := c.UsageTotals()
	if got.Calls != 1 || got.InputTokens != 900 || got.OutputTokens != 100 || got.CostUSD != 0.25 {
		t.Errorf("usage = %+v, want the failed turn counted once with its own figures", got)
	}
}

// TestRunSession_UsageUncountedWhenEnvelopeCarriesNone guards the other
// direction: a crash with no result event must not record a phantom call.
func TestRunSession_UsageUncountedWhenEnvelopeCarriesNone(t *testing.T) {
	fakeClaude(t, "cat >/dev/null\necho 'unknown flag' >&2\nexit 2\n")
	c := NewClient()

	if _, _, err := c.runSession(runSpec{label: "review", model: "opus", effort: "xhigh"}, "prompt"); err == nil {
		t.Fatal("expected an error")
	}
	if got := c.UsageTotals(); got.Calls != 0 {
		t.Errorf("Calls = %d, want 0 — a crash with no usage block is not a billable call", got.Calls)
	}
}

// TestRunClaudeStream_KeepsStderrTail covers the pipe contract: exec must have
// finished copying stderr before Wait returns, or the tail — the whole
// diagnosis on the error path — can be lost.
func TestRunClaudeStream_KeepsStderrTail(t *testing.T) {
	fakeClaude(t, "cat >/dev/null\ni=0\nwhile [ $i -lt 200 ]; do echo \"stderr line $i\" >&2; i=$((i+1)); done\nexit 1\n")
	c := NewClient(WithShowOutput(true))

	_, _, err := c.runSession(runSpec{label: "review", model: "opus", effort: "xhigh"}, "prompt")
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "stderr line 199") {
		t.Errorf("error dropped the stderr tail: %q", err)
	}
}

// TestRunSession_FeedsPromptOnStdin pins the invocation shape: the prompt
// travels on stdin, never as an argument, and the model id comes from the init
// event.
func TestRunSession_FeedsPromptOnStdin(t *testing.T) {
	dir := t.TempDir()
	seen := filepath.Join(dir, "stdin")
	fakeClaude(t, "cat > "+seen+"\n"+okTurn+"\n")
	c := NewClient()

	out, model, err := c.runSession(runSpec{label: "review", model: "opus", effort: "xhigh"}, "the prompt body")
	if err != nil {
		t.Fatalf("runSession: %v", err)
	}
	if out != "ok" || model != testResolvedModel {
		t.Errorf("out = %q, model = %q", out, model)
	}
	raw, err := os.ReadFile(seen)
	if err != nil {
		t.Fatalf("reading recorded stdin: %v", err)
	}
	if strings.TrimSpace(string(raw)) != "the prompt body" {
		t.Errorf("stdin = %q, want the prompt", raw)
	}
}

// redirectCacheRoot points os.UserCacheDir at root for the duration of the
// test, on darwin ($HOME/Library/Caches) and linux ($XDG_CACHE_HOME) alike, and
// returns the structuring directory structureWorkDir resolves under it.
func redirectCacheRoot(t *testing.T, root string) string {
	t.Helper()
	t.Setenv("HOME", root)
	t.Setenv("XDG_CACHE_HOME", root)
	base, err := os.UserCacheDir()
	if err != nil {
		t.Fatalf("os.UserCacheDir: %v", err)
	}
	return filepath.Join(base, "planwerk-agent", "structure-workdir")
}

// okTurn is the script line of a successful turn: an init event naming the
// resolved model, then a result event.
const okTurn = `printf '%s\n' '{"type":"system","subtype":"init","model":"claude-opus-5-5"}' '{"type":"result","result":"ok"}'`

// structureTurn is okTurn for a structuring session, whose result is JSON.
const structureTurn = `printf '%s\n' '{"type":"system","subtype":"init","model":"claude-sonnet-5"}' '{"type":"result","result":"{}"}'`

// structureRunners are the two ways runSession runs a session once it has
// resolved the structuring directory: quiet, and with its events shown under
// WithShowOutput. The directory tests run on both, so resolving it on one path
// only fails them.
var structureRunners = []struct {
	name       string
	showOutput bool
}{
	{"quiet", false},
	{"shown", true},
}

// TestRunClaudeStructure_RunsIsolatedFromTheOperatorsDirectory drives a real
// subprocess to pin what a structuring session is actually given: the empty
// working directory the tier owns under the user cache directory, and an argv
// with the whole built-in toolset removed. Neither is visible from claudeArgs
// alone, since only the spawned process can report the directory it was
// started in.
func TestRunClaudeStructure_RunsIsolatedFromTheOperatorsDirectory(t *testing.T) {
	for _, r := range structureRunners {
		t.Run(r.name, func(t *testing.T) {
			workDir := redirectCacheRoot(t, t.TempDir())

			rec := t.TempDir()
			pwdFile := filepath.Join(rec, "pwd")
			argvFile := filepath.Join(rec, "argv")
			fakeClaude(t, "cat >/dev/null\npwd -P > '"+pwdFile+"'\nprintf '%s\\n' \"$@\" > '"+argvFile+"'\n"+structureTurn+"\n")

			if _, _, err := NewClient(WithShowOutput(r.showOutput)).runClaudeStructure("transcribe this", "structure"); err != nil {
				t.Fatalf("runClaudeStructure: %v", err)
			}

			// pwd -P and EvalSymlinks both resolve the /var -> /private/var indirection
			// macOS puts in front of a temp directory, so the two sides are comparable.
			wantDir, err := filepath.EvalSymlinks(workDir)
			if err != nil {
				t.Fatalf("resolving the structuring directory: %v", err)
			}
			gotDir := strings.TrimSpace(readRecorded(t, pwdFile))
			if gotDir != wantDir {
				t.Errorf("session ran in %q, want the tier's own empty directory %q", gotDir, wantDir)
			}

			argv := recordedArgv(t, argvFile)
			i := slices.Index(argv, "--tools")
			if i == -1 {
				t.Fatalf("argv carries no --tools: %v", argv)
			}
			if i != len(argv)-2 || argv[i+1] != "" {
				t.Errorf("--tools must trail the argv with one empty value; got %v", argv)
			}
			if slices.Contains(argv, "--disallowed-tools") || slices.Contains(argv, "--allowed-tools") {
				t.Errorf("a structuring session must carry neither tool list; got %v", argv)
			}
			if !slices.Contains(argv, "--setting-sources=") || !slices.Contains(argv, "--strict-mcp-config") {
				t.Errorf("a structuring session lost its hermetic flags; got %v", argv)
			}
		})
	}
}

// TestRunClaudeStructure_WorkDirFailureNeverStartsAProcess covers the error path
// of the directory the tier depends on: when it cannot be created the call
// fails with that reason and no session runs, rather than falling back to
// whatever directory the process happens to sit in. The cache root is a regular
// file, so creating a directory below it fails with ENOTDIR.
func TestRunClaudeStructure_WorkDirFailureNeverStartsAProcess(t *testing.T) {
	for _, r := range structureRunners {
		t.Run(r.name, func(t *testing.T) {
			notADir := filepath.Join(t.TempDir(), "file")
			if err := os.WriteFile(notADir, []byte("not a directory"), 0o600); err != nil {
				t.Fatalf("writing the blocking file: %v", err)
			}
			redirectCacheRoot(t, notADir)

			marker := filepath.Join(t.TempDir(), "ran")
			fakeClaude(t, "touch '"+marker+"'\n")

			_, _, err := NewClient(WithShowOutput(r.showOutput)).runClaudeStructure("prompt", "structure")
			if err == nil {
				t.Fatal("expected an error when the structuring working directory cannot be created")
			}
			if !strings.Contains(err.Error(), "creating the structuring working directory") {
				t.Errorf("error = %q, want it to name the working directory as the cause", err)
			}
			if _, statErr := os.Stat(marker); statErr == nil {
				t.Error("the CLI was invoked although the working directory could not be created")
			}
		})
	}
}

// TestRunSession_KeepsTheCallersDirectory covers a spec without noTools: the
// session runs in the directory the caller named, and the structuring
// directory is never created.
func TestRunSession_KeepsTheCallersDirectory(t *testing.T) {
	workDir := redirectCacheRoot(t, t.TempDir())
	work := t.TempDir()

	pwdFile := filepath.Join(t.TempDir(), "pwd")
	fakeClaude(t, "cat >/dev/null\npwd -P > '"+pwdFile+"'\n"+okTurn+"\n")

	if _, _, err := NewClient().runSession(runSpec{dir: work, label: "review", model: "opus", effort: "xhigh"}, "prompt"); err != nil {
		t.Fatalf("runSession: %v", err)
	}

	wantDir, err := filepath.EvalSymlinks(work)
	if err != nil {
		t.Fatalf("resolving the caller's directory: %v", err)
	}
	if gotDir := strings.TrimSpace(readRecorded(t, pwdFile)); gotDir != wantDir {
		t.Errorf("session ran in %q, want the caller's directory %q", gotDir, wantDir)
	}
	if _, statErr := os.Stat(workDir); !errors.Is(statErr, fs.ErrNotExist) {
		t.Errorf("stat %s = %v, want it absent: only a noTools spec creates the structuring directory", workDir, statErr)
	}
}

// v4UUID matches the RFC 4122 version-4 UUID newSessionID generates.
var v4UUID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// TestRunSession_RecordsHowTheSessionRan drives a quiet session end to end: the
// argv asks for the event stream and pins a fresh session id, and the pass
// records the call, the result event's turns and denials, every tool call by
// name, and the pinned id the stream did not repeat.
func TestRunSession_RecordsHowTheSessionRan(t *testing.T) {
	argvFile := filepath.Join(t.TempDir(), "argv")
	fakeClaude(t, "cat >/dev/null\nprintf '%s\\n' \"$@\" > '"+argvFile+"'\nprintf '%s\\n' "+
		`'{"type":"system","subtype":"init","model":"claude-opus-5-5","tools":["Read","Grep"]}' `+
		`'{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Read"},{"type":"tool_use","name":"Grep"}]}}' `+
		`'{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Read"}]}}' `+
		`'{"type":"result","result":"ok","num_turns":3,"permission_denials":[{"tool_name":"Bash","tool_use_id":"t1","tool_input":{}}],`+
		`"usage":{"input_tokens":100,"output_tokens":10},"total_cost_usd":0.02}'`+"\n")
	c := NewClient()

	out, model, err := c.runSession(runSpec{label: "review", model: "opus", effort: "xhigh", readOnly: true}, "prompt")
	if err != nil {
		t.Fatalf("runSession: %v", err)
	}
	if out != "ok" || model != testResolvedModel {
		t.Errorf("out = %q, model = %q", out, model)
	}

	argv := recordedArgv(t, argvFile)
	if i := slices.Index(argv, "--output-format"); i == -1 || !slices.Equal(argv[i+1:i+3], []string{"stream-json", "--verbose"}) {
		t.Errorf("argv lacks --output-format stream-json --verbose: %v", argv)
	}
	i := slices.Index(argv, "--session-id")
	if i == -1 || !v4UUID.MatchString(argv[i+1]) {
		t.Fatalf("argv pins no v4 session id: %v", argv)
	}

	want := []report.PassUsage{{
		Pass: "review", Calls: 1, InputTokens: 100, OutputTokens: 10, CostUSD: 0.02,
		NumTurns: 3, PermissionDenials: 1,
		ToolUses:   map[string]int{"Read": 2, "Grep": 1},
		SessionIDs: []string{argv[i+1]},
	}}
	if got := c.UsageTotals().Passes; !reflect.DeepEqual(got, want) {
		t.Errorf("Passes = %+v, want %+v", got, want)
	}
}

// TestRunSession_ResumedTurnKeepsItsSessionID covers the completion nudge's
// resumed turn: it continues the session it names with --resume and is never
// pinned to a second, fresh id.
func TestRunSession_ResumedTurnKeepsItsSessionID(t *testing.T) {
	const id = "123e4567-e89b-42d3-a456-426614174000"
	argvFile := filepath.Join(t.TempDir(), "argv")
	fakeClaude(t, "cat >/dev/null\nprintf '%s\\n' \"$@\" > '"+argvFile+"'\n"+okTurn+"\n")

	if _, _, err := NewClient().runSession(runSpec{label: "implement", model: "opus", effort: "xhigh", sessionID: id, resume: true}, "nudge"); err != nil {
		t.Fatalf("runSession: %v", err)
	}
	argv := recordedArgv(t, argvFile)
	if i := slices.Index(argv, "--resume"); i == -1 || argv[i+1] != id {
		t.Errorf("argv does not resume %s: %v", id, argv)
	}
	if slices.Contains(argv, "--session-id") {
		t.Errorf("a resumed turn carries --session-id: %v", argv)
	}
}

// TestRunSession_TimedOutSessionKeepsItsID covers a session the deadline ends:
// the timeout error is unchanged, and the pass still records the id the stream
// announced and the tool calls made before the kill, without billing a call.
func TestRunSession_TimedOutSessionKeepsItsID(t *testing.T) {
	const id = "0b6f4a8e-6a4c-4c1e-9a55-3f0d2c1b7e21"
	fakeClaude(t, "cat >/dev/null\nprintf '%s\\n' "+
		`'{"type":"system","subtype":"init","session_id":"`+id+`"}' `+
		`'{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash"}]}}'`+
		"\nsleep 30\n")
	c := NewClient(WithTimeout(time.Second))

	_, _, err := c.runSession(runSpec{label: "review", model: "opus", effort: "xhigh"}, "prompt")
	if err == nil || !strings.Contains(err.Error(), "timed out after") {
		t.Fatalf("err = %v, want the timeout", err)
	}
	want := []report.PassUsage{{Pass: "review", ToolUses: map[string]int{"Bash": 1}, SessionIDs: []string{id}}}
	if got := c.UsageTotals().Passes; !reflect.DeepEqual(got, want) {
		t.Errorf("Passes = %+v, want %+v", got, want)
	}
}

// emptyResultTurn is the script line of a turn that ended cleanly with an
// empty result and no assistant text before it: an init event naming the
// resolved model, then a successful result event without text.
const emptyResultTurn = `printf '%s\n' '{"type":"system","subtype":"init","model":"claude-opus-5-5"}' ` +
	`'{"type":"result","subtype":"success","is_error":false,"result":""}'`

// narratedEmptyResultTurn is emptyResultTurn with an assistant text before
// the result event.
const narratedEmptyResultTurn = `printf '%s\n' '{"type":"system","subtype":"init","model":"claude-opus-5-5"}' ` +
	`'{"type":"assistant","message":{"content":[{"type":"text","text":"Waiting for the tests."}]}}' ` +
	`'{"type":"result","subtype":"success","is_error":false,"result":""}'`

// TestRunSession_EmptyResultIsAnEmptyOutput covers a turn that ended cleanly
// with an empty result, quiet and shown alike: the session returns "" with a
// nil error and the init event's model. Without assistant text that is not a
// "produced no result" error, and with it the text does not stand in for the
// result.
func TestRunSession_EmptyResultIsAnEmptyOutput(t *testing.T) {
	for _, turn := range []struct{ name, script string }{
		{"silent", emptyResultTurn},
		{"narrated", narratedEmptyResultTurn},
	} {
		for _, sink := range []struct {
			name       string
			showOutput bool
		}{{"quiet", false}, {"shown", true}} {
			t.Run(turn.name+"/"+sink.name, func(t *testing.T) {
				fakeClaude(t, "cat >/dev/null\n"+turn.script+"\n")
				c := NewClient(WithShowOutput(sink.showOutput))

				out, model, err := c.runSession(runSpec{label: "implement", model: "opus", effort: "xhigh"}, "prompt")
				if err != nil {
					t.Fatalf("runSession: %v, want the empty result without an error", err)
				}
				if out != "" || model != testResolvedModel {
					t.Errorf("out = %q, model = %q, want the result event's empty text and the init model", out, model)
				}
				if got := c.UsageTotals().Calls; got != 1 {
					t.Errorf("Calls = %d, want 1", got)
				}
			})
		}
	}
}

// TestRunWithCompletionNudge_ResumesASessionWithAnEmptyResult drives the
// completion nudge against a real process whose every turn ends with an empty
// result: the empty output fails the report gate, not the run, so the session
// is resumed maxCompletionNudges times and its last output comes back with a
// nil error.
func TestRunWithCompletionNudge_ResumesASessionWithAnEmptyResult(t *testing.T) {
	calls := filepath.Join(t.TempDir(), "calls")
	fakeClaude(t, "cat >/dev/null\nmode=fresh\nfor a in \"$@\"; do [ \"$a\" = --resume ] && mode=resumed; done\n"+
		"echo $mode >> '"+calls+"'\n"+emptyResultTurn+"\n")

	out, _, err := NewClient().runWithCompletionNudge(runSpec{label: "implement", model: "opus", effort: "xhigh"},
		"prompt", implementReportHeading, implementReportStatusChoices)
	if err != nil {
		t.Fatalf("runWithCompletionNudge: %v, want the incomplete output without an error", err)
	}
	if out != "" {
		t.Errorf("out = %q, want the last turn's empty result", out)
	}
	if got, want := readRecorded(t, calls), "fresh\n"+strings.Repeat("resumed\n", maxCompletionNudges); got != want {
		t.Errorf("turns = %q, want %q: a fresh turn, then one resumed turn per nudge", got, want)
	}
}

// readRecorded returns what the scripted claude wrote to path.
func readRecorded(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(raw)
}

// recordedArgv splits the one-argument-per-line file the scripted claude wrote.
// The trailing newline yields a final empty element that is not an argument;
// every other empty line is one, which is how the empty value after --tools
// survives the round trip.
func recordedArgv(t *testing.T, path string) []string {
	t.Helper()
	lines := strings.Split(readRecorded(t, path), "\n")
	return lines[:len(lines)-1]
}
