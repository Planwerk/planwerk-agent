package main

import (
	"bytes"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/pflag"

	"github.com/planwerk/planwerk-agent/internal/brain"
	"github.com/planwerk/planwerk-agent/internal/claude"
	"github.com/planwerk/planwerk-agent/internal/cli"
	"github.com/planwerk/planwerk-agent/internal/github"
	"github.com/planwerk/planwerk-agent/internal/mirror"
	"github.com/planwerk/planwerk-agent/internal/search"
)

// runBrainCmd executes the brain command hermetically: with the wiki off,
// "brain memory" resolves nothing, so no backend is touched.
func runBrainCmd(t *testing.T, args ...string) ([]byte, error) {
	t.Helper()
	var out, errBuf bytes.Buffer
	cmd := newBrainCmd(&runtimeDeps{version: "test"})
	// The root command silences cobra's usage print, so a failing call writes
	// nothing to stdout; the bare command needs the same setting to match.
	cmd.SilenceUsage = true
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.Bytes(), err
}

func TestBrainMemoryCmd_ArgCount(t *testing.T) {
	if _, err := runBrainCmd(t, "memory"); err == nil {
		t.Error("expected an error when no repository reference is given")
	}
	if _, err := runBrainCmd(t, "memory", testRepoRef, "a.md", "b.md"); err == nil {
		t.Error("expected an error when three arguments are given")
	}
}

func TestBrainMemoryCmd_RegistersWikiFlags(t *testing.T) {
	memoryCmd, _, err := newBrainCmd(&runtimeDeps{}).Find([]string{"memory"})
	if err != nil {
		t.Fatalf("finding the memory command: %v", err)
	}
	for _, name := range []string{"wiki", "no-wiki", "wiki-ref"} {
		if memoryCmd.Flags().Lookup(name) == nil {
			t.Errorf("brain memory must expose --%s", name)
		}
	}
}

func TestBrainMemoryCmd_DisabledWikiPrintsNothing(t *testing.T) {
	// No flag, no config file, and an empty PLANWERK_WIKI: the wiki is off, so
	// the command resolves nothing and never reaches the network.
	t.Setenv(envWiki, "")
	out, err := runBrainCmd(t, "memory", testRepoRef)
	if err != nil {
		t.Fatalf("brain memory returned error: %v", err)
	}
	if len(out) != 0 {
		t.Errorf("stdout = %q, want nothing for a disabled wiki", out)
	}
}

func TestBrainMemoryCmd_UnknownPageOfADisabledWikiFails(t *testing.T) {
	t.Setenv(envWiki, "")
	out, err := runBrainCmd(t, "memory", testRepoRef, "a.md")
	if err == nil {
		t.Fatal("expected an error for a page the memory does not hold")
	}
	for _, want := range []string{"no project memory page named", `"a.md"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to contain %q", err, want)
		}
	}
	if len(out) != 0 {
		t.Errorf("stdout = %q, want nothing beside the error", out)
	}
}

// sharedMemoryDoc is the plugin document that tells the skills how to call
// `brain memory`.
const sharedMemoryDoc = "../../plugins/planwerk/shared/memory.md"

// TestSharedMemoryDocMatchesCommand keeps the skills' instructions and the
// command they describe together: the document names the invocation, the page
// form that keeps a file name from being read as a flag or by the shell, the
// header the index opens with, and every flag the command registers. A flag
// added to the command, or a renamed header, otherwise leaves the skills
// following a description of a command that no longer exists.
func TestSharedMemoryDocMatchesCommand(t *testing.T) {
	raw, err := os.ReadFile(sharedMemoryDoc)
	if err != nil {
		t.Fatalf("reading %s: %v", sharedMemoryDoc, err)
	}
	doc := string(raw)

	want := []string{"planwerk-agent brain memory <owner/repo>", "planwerk-agent brain memory <owner/repo> -- '<file name>'", "Project memory from "}
	memoryCmd, _, err := newBrainCmd(&runtimeDeps{}).Find([]string{"memory"})
	if err != nil {
		t.Fatalf("finding the memory command: %v", err)
	}
	flags := 0
	memoryCmd.Flags().VisitAll(func(f *pflag.Flag) {
		flags++
		want = append(want, "--"+f.Name)
	})
	if flags == 0 {
		t.Fatal("the memory command registers no flag; the document describes three")
	}

	for _, w := range want {
		if !strings.Contains(doc, w) {
			t.Errorf("%s does not mention %q", sharedMemoryDoc, w)
		}
	}
}

func TestBrainSyncCmd_ArgCount(t *testing.T) {
	if _, err := runBrainCmd(t, "sync"); err == nil {
		t.Error("expected an error when no repository reference is given")
	}
	if _, err := runBrainCmd(t, "sync", testRepoRef, "other/repo"); err == nil {
		t.Error("expected an error when two arguments are given")
	}
}

func TestBrainSyncCmd_RegistersItsFlags(t *testing.T) {
	syncCmd, _, err := newBrainCmd(&runtimeDeps{}).Find([]string{"sync"})
	if err != nil {
		t.Fatalf("finding the sync command: %v", err)
	}
	for _, name := range []string{"full", "wiki-ref"} {
		if syncCmd.Flags().Lookup(name) == nil {
			t.Errorf("brain sync must expose --%s", name)
		}
	}
	// Running the command is the wiki opt-in.
	for _, name := range []string{"wiki", "no-wiki"} {
		if syncCmd.Flags().Lookup(name) != nil {
			t.Errorf("brain sync must not expose --%s", name)
		}
	}
	if got := syncCmd.Flags().Lookup("full").DefValue; got != "false" {
		t.Errorf("--full defaults to %q, want false", got)
	}
}

// TestBrainSyncCmd_RejectsABadReferenceBeforeAnyWork proves a reference that
// does not name a repository fails before gh or git runs.
func TestBrainSyncCmd_RejectsABadReferenceBeforeAnyWork(t *testing.T) {
	bin := t.TempDir()
	mark := filepath.Join(bin, "called")
	for _, tool := range []string{"gh", "git"} {
		script := "#!/bin/sh\necho " + tool + " >> " + mark + "\nexit 1\n"
		if err := os.WriteFile(filepath.Join(bin, tool), []byte(script), 0o755); err != nil {
			t.Fatalf("writing fake %s: %v", tool, err)
		}
	}
	t.Setenv("PATH", bin)

	for _, tc := range []struct{ ref, want string }{
		{"x", "parsing repo ref: "},
		{"../x", "invalid repository ../x for a mirror directory"},
	} {
		out, err := runBrainCmd(t, "sync", tc.ref)
		if err == nil || !strings.HasPrefix(err.Error(), tc.want) {
			t.Errorf("brain sync %s: err = %v, want it to start with %q", tc.ref, err, tc.want)
		}
		if len(out) != 0 {
			t.Errorf("brain sync %s: stdout = %q, want nothing beside the error", tc.ref, out)
		}
	}
	if called, err := os.ReadFile(mark); err == nil {
		t.Errorf("a bad reference must fail before any tool runs; ran: %s", called)
	}
}

// testMirrorDir points the user cache directory at a fresh directory for the
// test and returns the mirror directory of acme/widgets under it.
func testMirrorDir(t *testing.T) string {
	t.Helper()
	cache := t.TempDir()
	t.Setenv("HOME", cache)
	t.Setenv("XDG_CACHE_HOME", cache)
	root, err := mirror.DefaultRoot()
	if err != nil {
		t.Fatalf("mirror.DefaultRoot: %v", err)
	}
	dir, err := mirror.Dir(root, "acme", "widgets")
	if err != nil {
		t.Fatalf("mirror.Dir: %v", err)
	}
	return dir
}

// fakeTools puts the given shell scripts, keyed by tool name, first on PATH
// for the test.
func fakeTools(t *testing.T, scripts map[string]string) {
	t.Helper()
	bin := t.TempDir()
	for tool, script := range scripts {
		if err := os.WriteFile(filepath.Join(bin, tool), []byte("#!/bin/sh\n"+script), 0o755); err != nil {
			t.Fatalf("writing fake %s: %v", tool, err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// emptyRepositoryGH is a fake gh that answers an empty item listing, a
// repository without a default branch, and no token.
const emptyRepositoryGH = `case "$*" in *graphql*) echo '{"data":{"repository":{"defaultBranchRef":null}}}';; esac
exit 0
`

// TestBrainSyncCmd_SyncsIntoTheCacheDirectoryAndFullRebuildsIt runs the
// command: without a root of its own the mirror lands under the user cache
// directory, and --full reaches the run, which deletes what the mirror held.
func TestBrainSyncCmd_SyncsIntoTheCacheDirectoryAndFullRebuildsIt(t *testing.T) {
	dir := testMirrorDir(t)
	t.Setenv(envWikiRef, "")
	// git fails, so the wiki is reported as not mirrored.
	fakeTools(t, map[string]string{"gh": emptyRepositoryGH, "git": "exit 1\n"})

	want := "Mirror of acme/widgets at " + dir + "\n" +
		"items: 0 listed, 0 fetched, 0 in the mirror (0 issues, 0 pull requests)\n" +
		"history: 0 new commits, 0 in the mirror\n" +
		"wiki: acme/widgets.wiki not mirrored\n"
	out, err := runBrainCmd(t, "sync", testRepoRef)
	if err != nil || string(out) != want {
		t.Fatalf("brain sync = %q, %v\nwant %q", out, err, want)
	}

	stale := filepath.Join(dir, "issues", "999.md")
	if err := os.MkdirAll(filepath.Dir(stale), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stale, []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := runBrainCmd(t, "sync", testRepoRef); err != nil {
		t.Fatalf("brain sync: %v", err)
	}
	if _, err := os.Stat(stale); err != nil {
		t.Errorf("a run without --full must delete nothing: %v", err)
	}
	if _, err := runBrainCmd(t, "sync", testRepoRef, "--full"); err != nil {
		t.Fatalf("brain sync --full: %v", err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("--full must delete the mirror first: %v", err)
	}
}

// TestBrainSyncCmd_WikiRefPinsTheClone proves --wiki-ref reaches the clone:
// the fake git records the checkout of the reference.
func TestBrainSyncCmd_WikiRefPinsTheClone(t *testing.T) {
	testMirrorDir(t)
	t.Setenv(envWikiRef, "")
	argvFile := filepath.Join(t.TempDir(), "argv")
	// The clone creates its destination, and rev-parse answers the head.
	fakeTools(t, map[string]string{"gh": emptyRepositoryGH, "git": `echo "$*" >> ` + argvFile + `
case "$1" in clone) mkdir -p "$3";; esac
case "$*" in *rev-parse*) echo 1a2b3c4d5e6f70819293a4b5c6d7e8f901234567;; esac
exit 0
`})

	out, err := runBrainCmd(t, "sync", testRepoRef, "--wiki-ref", "v1")
	if err != nil || !strings.HasSuffix(string(out), "wiki: acme/widgets.wiki at 1a2b3c4\n") {
		t.Fatalf("brain sync --wiki-ref = %q, %v, want the wiki mirrored", out, err)
	}
	argv, err := os.ReadFile(argvFile)
	if err != nil {
		t.Fatalf("reading the recorded git calls: %v", err)
	}
	if want := " checkout --end-of-options v1\n"; !strings.Contains(string(argv), want) {
		t.Errorf("git ran\n%swant a call that ends in %q", argv, want)
	}
}

func TestBrainSearchCmd_RegistersItsFlags(t *testing.T) {
	searchCmd, _, err := newBrainCmd(&runtimeDeps{}).Find([]string{"search"})
	if err != nil {
		t.Fatalf("finding the search command: %v", err)
	}
	for _, name := range []string{"type", "state", "label", "limit", "json", "show"} {
		if searchCmd.Flags().Lookup(name) == nil {
			t.Errorf("brain search must expose --%s", name)
		}
	}
	// The command reads the mirror and nothing else.
	for _, name := range []string{"wiki", "no-wiki", "wiki-ref"} {
		if searchCmd.Flags().Lookup(name) != nil {
			t.Errorf("brain search must not expose --%s", name)
		}
	}
	if got := searchCmd.Flags().Lookup("limit").DefValue; got != "10" {
		t.Errorf("--limit defaults to %q, want 10", got)
	}
}

func TestBrainSearchCmd_NeedsARepository(t *testing.T) {
	if _, err := runBrainCmd(t, "search"); err == nil {
		t.Error("expected an error when no repository reference is given")
	}
}

// searchTestMirror writes a finished mirror of acme/widgets with one issue
// under the user cache directory of the test and returns its directory.
func searchTestMirror(t *testing.T) string {
	t.Helper()
	dir := testMirrorDir(t)
	it := &github.Item{
		Kind: github.ItemKindIssue, Number: 186, Title: "Add a brain sync subcommand", Body: "Items share one cursor.",
		URL: "https://github.com/acme/widgets/issues/186", State: "closed", Author: "alice", AuthorAssociation: "MEMBER",
		CreatedAt: "2026-10-01T08:00:00Z", UpdatedAt: "2026-10-01T09:00:00Z",
	}
	path := mirror.ItemPath(dir, it.Kind, it.Number)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, mirror.Render(testRepoRef, it), 0o600); err != nil {
		t.Fatal(err)
	}
	st, _, err := mirror.LoadState(dir, testRepoRef)
	if err != nil {
		t.Fatal(err)
	}
	st.SyncedAt = "2026-10-02T09:00:00Z"
	if err := st.Save(dir); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestBrainSearchCmd_RejectsBadArgumentsBeforeAnyWork proves each argument
// error is returned before the command reads the mirror: nothing is printed,
// and the index a search would create is not there.
func TestBrainSearchCmd_RejectsBadArgumentsBeforeAnyWork(t *testing.T) {
	dir := searchTestMirror(t)

	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"show with a query", []string{"--show", "issues/186.md:1", "cursor"}, "--show takes no query"},
		{"show with a type", []string{"--show", "issues/186.md:1", "--type", "issue"}, "--show takes no filter"},
		{"show with a state", []string{"--show", "issues/186.md:1", "--state", "open"}, "--show takes no filter"},
		{"show with a label", []string{"--show", "issues/186.md:1", "--label", "bug"}, "--show takes no filter"},
		{"show with a limit", []string{"--show", "issues/186.md:1", "--limit", "5"}, "--show takes no filter"},
		{"neither a query nor show", nil, "a query is required, or --show <id>"},
		{"a filter without a query", []string{"--type", "issue"}, "a query is required, or --show <id>"},
		{"an unknown type", []string{"cursor", "--type", "issue", "--type", "discussion"}, "--type must be one of issue, pull, wiki"},
		{"an unknown state", []string{"cursor", "--state", "draft"}, "--state must be one of open, closed, merged"},
		{"an empty state", []string{"cursor", "--state", ""}, "--state must be one of open, closed, merged"},
		{"a limit of zero", []string{"cursor", "--limit", "0"}, "--limit must be between 1 and 100"},
		{"a limit above the largest", []string{"cursor", "--limit", "101"}, "--limit must be between 1 and 100"},
		{"a limit that is no number", []string{"cursor", "--limit", "many"}, "invalid flag or flag value; see brain search --help"},
		// Without "--" before it, a query word that starts with a dash is a flag.
		{"a query word that looks like a flag", []string{"--cursor"}, "invalid flag or flag value; see brain search --help"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := runBrainCmd(t, append([]string{"search", testRepoRef}, tc.args...)...)
			if err == nil || err.Error() != tc.want {
				t.Errorf("err = %v, want %q", err, tc.want)
			}
			if len(out) != 0 {
				t.Errorf("stdout = %q, want nothing beside the error", out)
			}
		})
	}
	if _, err := os.Stat(filepath.Join(dir, search.IndexFileName)); !os.IsNotExist(err) {
		t.Errorf("an argument error must come before the index is created: %v", err)
	}
}

// TestBrainSearchCmd_SearchesTheMirrorInTheCacheDirectory runs the command:
// without a root of its own it reads the mirror under the user cache
// directory, joins its arguments into the query, and keeps its index beside
// the mirrored files, where brain sync --full removes it with the mirror.
func TestBrainSearchCmd_SearchesTheMirrorInTheCacheDirectory(t *testing.T) {
	dir := searchTestMirror(t)
	index := filepath.Join(dir, search.IndexFileName)

	out, err := runBrainCmd(t, "search", testRepoRef, "one", "cursor")
	want := `Search of acme/widgets, mirror synced 2026-10-02T09:00:00Z: 1 hit

1. issue #186 [closed] Add a brain sync subcommand
   body by alice (MEMBER) on 2026-10-01T08:00:00Z, block 2 of 2
   https://github.com/acme/widgets/issues/186
   id: issues/186.md:2
   Items share one cursor.
`
	if err != nil || string(out) != want {
		t.Fatalf("brain search = %q, %v\nwant %q", out, err, want)
	}
	if _, err := os.Stat(index); err != nil {
		t.Errorf("a search must create the index in the mirror directory: %v", err)
	}

	// A word after "--" is a query word, whatever it starts with, and a flag
	// before the "--" is still a flag.
	for _, args := range [][]string{{"--", "--cursor"}, {"--type", "issue", "--", "--cursor", "-x"}} {
		out, err = runBrainCmd(t, append([]string{"search", testRepoRef}, args...)...)
		if err != nil || string(out) != want {
			t.Errorf("brain search %v = %q, %v\nwant %q", args, out, err, want)
		}
	}

	out, err = runBrainCmd(t, "search", testRepoRef, "--show", "issues/186.md:2")
	want = `issue #186 [closed] Add a brain sync subcommand
body by alice (MEMBER) on 2026-10-01T08:00:00Z, block 2 of 2
https://github.com/acme/widgets/issues/186

text, 1 line, each after "| ":
| Items share one cursor.
`
	if err != nil || string(out) != want {
		t.Errorf("brain search --show = %q, %v\nwant %q", out, err, want)
	}

	out, err = runBrainCmd(t, "search", testRepoRef, "cursor", "--json", "--type", "pull", "--state", "merged", "--label", "bug", "--limit", "100")
	if want := `{"repo":"acme/widgets","synced_at":"2026-10-02T09:00:00Z","hits":[]}` + "\n"; err != nil || string(out) != want {
		t.Errorf("brain search with every filter = %q, %v\nwant %q", out, err, want)
	}

	out, err = runBrainCmd(t, "search", testRepoRef, "--show", "issues/186.md:9")
	if err == nil || err.Error() != "no such block in the mirror" || len(out) != 0 {
		t.Errorf("brain search --show of a missing block = %q, %v, want nothing and the error", out, err)
	}

	t.Setenv(envWikiRef, "")
	fakeTools(t, map[string]string{"gh": emptyRepositoryGH, "git": "exit 1\n"})
	if _, err := runBrainCmd(t, "sync", testRepoRef, "--full"); err != nil {
		t.Fatalf("brain sync --full: %v", err)
	}
	if _, err := os.Stat(index); !os.IsNotExist(err) {
		t.Errorf("brain sync --full must remove the index with the mirror: %v", err)
	}
}

// TestBrainSearchCmd_PrintsNoArgumentBack runs the command with a marker in
// every argument a session's shell could expand a variable or a file name
// into. A session reads both streams of the command, so neither the output
// nor an error may hold the marker.
func TestBrainSearchCmd_PrintsNoArgumentBack(t *testing.T) {
	searchTestMirror(t)

	const canary = "ghp_canary"
	for _, tc := range []struct {
		name   string
		args   []string
		canary string
	}{
		{"a query", []string{canary}, canary},
		{"a block id without a number", []string{"--show", canary}, canary},
		{"a block id that names no block", []string{"--show", canary + ":1"}, canary},
		{"a block id that names no file", []string{"--show", "issues/" + canary + ".md:1"}, canary},
		{"a type", []string{"cursor", "--type", canary}, canary},
		{"a state", []string{"cursor", "--state", canary}, canary},
		{"a label", []string{"cursor", "--label", canary}, canary},
		{"a limit that is no number", []string{"cursor", "--limit", canary}, canary},
		{"a limit out of range", []string{"cursor", "--limit", "4711"}, "4711"},
		{"an unknown flag", []string{"cursor", "--" + canary}, canary},
		{"an unknown flag with a value", []string{"cursor", "--unknown=" + canary}, canary},
		{"an unknown shorthand", []string{"cursor", "-" + canary}, canary},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			cmd := newBrainCmd(&runtimeDeps{version: "test"})
			cmd.SilenceUsage = true
			cmd.SetOut(&out)
			cmd.SetErr(&errOut)
			cmd.SetArgs(append([]string{"search", testRepoRef}, tc.args...))
			err := cmd.Execute()

			if err != nil && strings.Contains(err.Error(), tc.canary) {
				t.Errorf("the error holds the argument: %v", err)
			}
			for stream, text := range map[string]string{"stdout": out.String(), "stderr": errOut.String()} {
				if strings.Contains(text, tc.canary) {
					t.Errorf("%s holds the argument: %q", stream, text)
				}
			}
		})
	}
}

// TestBrainSearchCmd_PrintsNoInheritedFlagValueBack runs the command under the
// root command with a marker as the value of every flag it inherits. The root
// command reads those flags before the search runs, and to a session's shell
// their values are arguments like every other.
func TestBrainSearchCmd_PrintsNoInheritedFlagValueBack(t *testing.T) {
	searchTestMirror(t)
	// The root command sets the default logger up.
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })

	const canary = "ghp_canary"
	newRootCmd(&runtimeDeps{}).PersistentFlags().VisitAll(func(f *pflag.Flag) {
		t.Run(f.Name, func(t *testing.T) {
			deps := &runtimeDeps{version: "test"}
			root := newRootCmd(deps)
			root.AddCommand(newBrainCmd(deps))
			var out, errOut bytes.Buffer
			root.SetOut(&out)
			root.SetErr(&errOut)
			root.SetArgs([]string{"brain", "search", testRepoRef, "cursor", "--" + f.Name + "=" + canary})
			err := root.Execute()

			if err != nil && strings.Contains(err.Error(), canary) {
				t.Errorf("the error holds the value: %v", err)
			}
			for stream, text := range map[string]string{"stdout": out.String(), "stderr": errOut.String()} {
				if strings.Contains(text, canary) {
					t.Errorf("%s holds the value: %q", stream, text)
				}
			}
		})
	})
}

// TestBrainSearchCmd_ReadsNoConfigFile runs the command under the root
// command in a working directory whose .planwerk/config.yaml this binary
// cannot parse. A session runs the command in the checkout under review, so
// that file is one the author of the reviewed change wrote, and it must not
// stop the search. Every other command still fails on it.
func TestBrainSearchCmd_ReadsNoConfigFile(t *testing.T) {
	searchTestMirror(t)
	t.Chdir(t.TempDir())
	if err := os.MkdirAll(filepath.Dir(cli.DefaultConfigPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cli.DefaultConfigPath, []byte("no_such_key: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// The root command sets the default logger up.
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })

	run := func(args ...string) (string, *runtimeDeps, error) {
		deps := &runtimeDeps{version: "test"}
		root := newRootCmd(deps)
		root.AddCommand(newBrainCmd(deps))
		var out bytes.Buffer
		root.SetOut(&out)
		root.SetErr(io.Discard)
		root.SetArgs(args)
		err := root.Execute()
		return out.String(), deps, err
	}

	out, deps, err := run("brain", "search", testRepoRef, "cursor")
	if err != nil || !strings.HasPrefix(out, "Search of acme/widgets, mirror synced 2026-10-02T09:00:00Z: 1 hit\n") {
		t.Errorf("brain search beside a config file that does not parse = %q, %v, want the hit", out, err)
	}
	if deps.claude != nil {
		t.Error("brain search must build no Claude client")
	}

	t.Setenv(envWiki, "")
	if _, _, err := run("brain", "memory", testRepoRef); err == nil || !strings.HasPrefix(err.Error(), "parse config "+cli.DefaultConfigPath+": ") {
		t.Errorf("brain memory beside the same file = %v, want the parse error", err)
	}
}

// TestBrainSearchCmd_StopsWithoutAMirror proves the command never syncs: a
// repository without a mirror is an error that names the command to run, and
// no tool runs.
func TestBrainSearchCmd_StopsWithoutAMirror(t *testing.T) {
	dir := testMirrorDir(t)
	mark := filepath.Join(t.TempDir(), "called")
	fakeTools(t, map[string]string{
		"gh":  "echo gh >> " + mark + "\nexit 1\n",
		"git": "echo git >> " + mark + "\nexit 1\n",
	})

	out, err := runBrainCmd(t, "search", testRepoRef, "cursor")
	want := "no mirror of acme/widgets at " + dir + `; run "planwerk-agent brain sync acme/widgets" first`
	if err == nil || err.Error() != want || len(out) != 0 {
		t.Errorf("brain search = %q, %v\nwant nothing and %s", out, err, want)
	}
	if called, readErr := os.ReadFile(mark); readErr == nil {
		t.Errorf("brain search must not reach GitHub or git; ran: %s", called)
	}
}

// The compiled-in analysis and review tiers, pinned by value so a changed
// default fails here.
const (
	wantAnalysisModel  = "haiku"
	wantAnalysisEffort = "xhigh"
	wantReviewModel    = "haiku"
	wantReviewEffort   = "xhigh"
)

func TestBrainBootstrapCmd_RegistersItsFlags(t *testing.T) {
	bootstrapCmd, _, err := newBrainCmd(&runtimeDeps{}).Find([]string{"bootstrap"})
	if err != nil {
		t.Fatalf("finding the bootstrap command: %v", err)
	}
	for _, name := range []string{"dry-run", "max-units", "write-wiki", "wiki-ref", "analysis-model", "analysis-effort", "review-model", "review-effort", "decision-docs", "no-decision-docs", "source"} {
		if bootstrapCmd.Flags().Lookup(name) == nil {
			t.Errorf("brain bootstrap must expose --%s", name)
		}
	}
	// Running the command is the wiki opt-in, and the push has no flag that
	// skips its confirmation.
	for _, name := range []string{"wiki", "no-wiki", "yes"} {
		if bootstrapCmd.Flags().Lookup(name) != nil {
			t.Errorf("brain bootstrap must not expose --%s", name)
		}
	}
	if got := bootstrapCmd.Flags().Lookup("analysis-model").DefValue; got != wantAnalysisModel {
		t.Errorf("--analysis-model defaults to %q, want %s", got, wantAnalysisModel)
	}
	if got := bootstrapCmd.Flags().Lookup("analysis-effort").DefValue; got != wantAnalysisEffort {
		t.Errorf("--analysis-effort defaults to %q, want %s", got, wantAnalysisEffort)
	}
	if got := bootstrapCmd.Flags().Lookup("review-model").DefValue; got != wantReviewModel {
		t.Errorf("--review-model defaults to %q, want %s", got, wantReviewModel)
	}
	if got := bootstrapCmd.Flags().Lookup("review-effort").DefValue; got != wantReviewEffort {
		t.Errorf("--review-effort defaults to %q, want %s", got, wantReviewEffort)
	}
	// The mirror is read only on request.
	if got := bootstrapCmd.Flags().Lookup("source").DefValue; got != "api" {
		t.Errorf("--source defaults to %q, want api", got)
	}
}

func TestBrainBootstrapCmd_ArgCount(t *testing.T) {
	if _, err := runBrainCmd(t, "bootstrap"); err == nil {
		t.Error("expected an error when no repository reference is given")
	}
	if _, err := runBrainCmd(t, "bootstrap", testRepoRef, "other/repo"); err == nil {
		t.Error("expected an error when two arguments are given")
	}
}

// TestBrainBootstrapCmd_RejectsBadFlagsBeforeAnyWork proves each flag error is
// returned before the command reaches GitHub, git, or Claude: every tool the
// run would call is replaced by a script that leaves a mark.
func TestBrainBootstrapCmd_RejectsBadFlagsBeforeAnyWork(t *testing.T) {
	bin := t.TempDir()
	mark := filepath.Join(bin, "called")
	for _, tool := range []string{"gh", "git", "claude"} {
		script := "#!/bin/sh\necho " + tool + " >> " + mark + "\nexit 1\n"
		if err := os.WriteFile(filepath.Join(bin, tool), []byte(script), 0o755); err != nil {
			t.Fatalf("writing fake %s: %v", tool, err)
		}
	}
	t.Setenv("PATH", bin)
	t.Setenv(envBrainAnalysisEffort, "")
	t.Setenv(envBrainReviewEffort, "")
	t.Chdir(t.TempDir())

	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"dry run with write", []string{"--dry-run", "--write-wiki"}, "--dry-run and --write-wiki are mutually exclusive"},
		{"documents with none", []string{"--decision-docs", "a.md", "--no-decision-docs"}, "--decision-docs and --no-decision-docs are mutually exclusive"},
		{"negative unit count", []string{"--max-units", "-1"}, "--max-units must be >= 0, got -1"},
		{"unknown analysis effort", []string{"--analysis-effort", "huge"}, `invalid --analysis-effort "huge": must be one of low, medium, high, xhigh, max (env: PLANWERK_BRAIN_ANALYSIS_EFFORT)`},
		{"unknown review effort", []string{"--review-effort", "huge"}, `invalid --review-effort "huge": must be one of low, medium, high, xhigh, max (env: PLANWERK_BRAIN_REVIEW_EFFORT)`},
		{"unknown source", []string{"--source", "x"}, `--source must be "api" or "mirror", got "x"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := runBrainCmd(t, append([]string{"bootstrap", testRepoRef}, tc.args...)...)
			if err == nil || err.Error() != tc.want {
				t.Errorf("err = %v, want %q", err, tc.want)
			}
			if len(out) != 0 {
				t.Errorf("stdout = %q, want nothing beside the error", out)
			}
		})
	}
	if called, err := os.ReadFile(mark); err == nil {
		t.Errorf("a flag error must come before any tool runs; ran: %s", called)
	}
	if _, err := os.Stat(".planwerk-brain-sync"); err == nil {
		t.Error("a flag error must not create the state directory")
	}
}

// TestBrainBootstrapCmd_SourceMirrorReadsTheMirrorAndNotTheAPI runs the
// command with --source mirror and no mirror on disk. The run lists the
// history before it clones, so it ends at the missing mirror: the error proves
// the mirror source reached the run, and no tool ran for the listing.
func TestBrainBootstrapCmd_SourceMirrorReadsTheMirrorAndNotTheAPI(t *testing.T) {
	dir := testMirrorDir(t)
	t.Chdir(t.TempDir())
	mark := filepath.Join(t.TempDir(), "called")
	fakeTools(t, map[string]string{
		"gh":  "echo gh >> " + mark + "\nexit 1\n",
		"git": "echo git >> " + mark + "\nexit 1\n",
	})

	_, err := runBrainCmd(t, "bootstrap", testRepoRef, "--source", "mirror", "--dry-run")
	want := "listing the history of acme/widgets: no mirror of acme/widgets at " + dir + `; run "planwerk-agent brain sync acme/widgets" first`
	if err == nil || err.Error() != want {
		t.Errorf("err = %v\nwant %s", err, want)
	}
	if called, readErr := os.ReadFile(mark); readErr == nil {
		t.Errorf("--source mirror must not reach the GitHub API; ran: %s", called)
	}
}

func TestBrainBootstrapSource(t *testing.T) {
	t.Run("api is the default reader", func(t *testing.T) {
		// A nil Source is what the run takes as the GitHub API.
		src, err := brainBootstrapSource("api", testRepoRef)
		if err != nil || src != nil {
			t.Errorf("source = %#v, %v, want nil, nil", src, err)
		}
	})

	t.Run("mirror reads the repository's mirror", func(t *testing.T) {
		src, err := brainBootstrapSource("mirror", "Acme/Widgets")
		if err != nil {
			t.Fatalf("brainBootstrapSource: %v", err)
		}
		ms, ok := src.(brain.MirrorSource)
		if !ok {
			t.Fatalf("source = %#v, want a brain.MirrorSource", src)
		}
		if want := filepath.Join("brain", "acme", "widgets"); !strings.HasSuffix(ms.Dir, want) {
			t.Errorf("mirror directory = %q, want it to end in %q", ms.Dir, want)
		}
	})

	// The mirror never falls back to the temp directory.
	t.Run("mirror without a user cache directory", func(t *testing.T) {
		t.Setenv("HOME", "")
		t.Setenv("XDG_CACHE_HOME", "")
		if _, err := os.UserCacheDir(); err == nil {
			t.Skip("the platform resolves a cache directory without HOME")
		}
		src, err := brainBootstrapSource("mirror", testRepoRef)
		if err == nil || src != nil || !strings.HasPrefix(err.Error(), "resolving the user cache directory for the mirror: ") {
			t.Errorf("source = %#v, %v, want the cache directory's error", src, err)
		}
	})

	for _, tc := range []struct{ name, source, ref, want string }{
		{"mirror with a reference that names no repository", "mirror", "x", "parsing repo ref: "},
		{"mirror with a repository that leaves the root", "mirror", "../x", "invalid repository ../x for a mirror directory"},
		{"an unknown source", "x", testRepoRef, `--source must be "api" or "mirror", got "x"`},
		{"an empty source", "", testRepoRef, `--source must be "api" or "mirror", got ""`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src, err := brainBootstrapSource(tc.source, tc.ref)
			if err == nil || !strings.HasPrefix(err.Error(), tc.want) || src != nil {
				t.Errorf("source = %#v, %v, want the error %q", src, err, tc.want)
			}
		})
	}
}

func TestResolveBrainAnalysisTier(t *testing.T) {
	t.Run("defaults without a flag or a variable", func(t *testing.T) {
		t.Setenv(envBrainAnalysisModel, "")
		t.Setenv(envBrainAnalysisEffort, "")
		m, e, err := resolveBrainAnalysisTier(wantAnalysisModel, false, wantAnalysisEffort, false)
		if err != nil || m != wantAnalysisModel || e != wantAnalysisEffort {
			t.Errorf("tier = %q/%q, %v, want %s/%s", m, e, err, wantAnalysisModel, wantAnalysisEffort)
		}
	})

	// The analysis has its own tier: the main tier's variables do not reach it.
	t.Run("the main tier's variables are ignored", func(t *testing.T) {
		t.Setenv(envBrainAnalysisModel, "")
		t.Setenv(envBrainAnalysisEffort, "")
		t.Setenv(envClaudeModel, "opus")
		t.Setenv(envClaudeEffort, "max")
		m, e, err := resolveBrainAnalysisTier(wantAnalysisModel, false, wantAnalysisEffort, false)
		if err != nil || m != wantAnalysisModel || e != wantAnalysisEffort {
			t.Errorf("tier = %q/%q, %v, want %s/%s", m, e, err, wantAnalysisModel, wantAnalysisEffort)
		}
	})

	t.Run("the variables replace the defaults", func(t *testing.T) {
		const varModel, varEffort = "opus", "max"
		t.Setenv(envBrainAnalysisModel, " "+varModel+" ")
		t.Setenv(envBrainAnalysisEffort, varEffort)
		m, e, err := resolveBrainAnalysisTier(wantAnalysisModel, false, wantAnalysisEffort, false)
		if err != nil || m != varModel || e != varEffort {
			t.Errorf("tier = %q/%q, %v, want %s/%s", m, e, err, varModel, varEffort)
		}
	})

	t.Run("a flag wins over its variable", func(t *testing.T) {
		t.Setenv(envBrainAnalysisModel, "opus")
		t.Setenv(envBrainAnalysisEffort, "max")
		const flagModel, flagEffort = "sonnet", "low"
		m, e, err := resolveBrainAnalysisTier(flagModel, true, flagEffort, true)
		if err != nil || m != flagModel || e != flagEffort {
			t.Fatalf("tier = %q/%q, %v, want %s/%s", m, e, err, flagModel, flagEffort)
		}
		client := claude.NewClient(claude.WithBrainAnalysisModel(m), claude.WithBrainAnalysisEffort(e))
		if gotModel, gotEffort := client.BrainAnalysisTier(); gotModel != flagModel || gotEffort != flagEffort {
			t.Errorf("client tier = %q/%q, want %s/%s", gotModel, gotEffort, flagModel, flagEffort)
		}
	})

	t.Run("an unknown effort from the variable is rejected", func(t *testing.T) {
		t.Setenv(envBrainAnalysisEffort, "huge")
		_, _, err := resolveBrainAnalysisTier(wantAnalysisModel, false, wantAnalysisEffort, false)
		if err == nil || !strings.Contains(err.Error(), "--analysis-effort") || !strings.Contains(err.Error(), "PLANWERK_BRAIN_ANALYSIS_EFFORT") {
			t.Errorf("err = %v, want it to name the flag and the variable", err)
		}
	})
}

func TestResolveBrainReviewTier(t *testing.T) {
	t.Run("defaults without a flag or a variable", func(t *testing.T) {
		t.Setenv(envBrainReviewModel, "")
		t.Setenv(envBrainReviewEffort, "")
		m, e, err := resolveBrainReviewTier(wantReviewModel, false, wantReviewEffort, false)
		if err != nil || m != wantReviewModel || e != wantReviewEffort {
			t.Errorf("tier = %q/%q, %v, want %s/%s", m, e, err, wantReviewModel, wantReviewEffort)
		}
	})

	t.Run("the variables replace the defaults", func(t *testing.T) {
		t.Setenv(envBrainReviewModel, " opus ")
		t.Setenv(envBrainReviewEffort, "max")
		m, e, err := resolveBrainReviewTier(wantReviewModel, false, wantReviewEffort, false)
		if err != nil || m != "opus" || e != "max" {
			t.Errorf("tier = %q/%q, %v, want opus/max", m, e, err)
		}
	})

	t.Run("a flag wins over its variable", func(t *testing.T) {
		t.Setenv(envBrainReviewModel, "opus")
		t.Setenv(envBrainReviewEffort, "max")
		const flagModel, flagEffort = "sonnet", "low"
		m, e, err := resolveBrainReviewTier(flagModel, true, flagEffort, true)
		if err != nil || m != flagModel || e != flagEffort {
			t.Fatalf("tier = %q/%q, %v, want %s/%s", m, e, err, flagModel, flagEffort)
		}
		client := claude.NewClient(claude.WithBrainReviewModel(m), claude.WithBrainReviewEffort(e))
		if gotModel, gotEffort := client.BrainReviewTier(); gotModel != flagModel || gotEffort != flagEffort {
			t.Errorf("client tier = %q/%q, want %s/%s", gotModel, gotEffort, flagModel, flagEffort)
		}
	})

	t.Run("an unknown effort from the variable is rejected", func(t *testing.T) {
		t.Setenv(envBrainReviewEffort, "huge")
		_, _, err := resolveBrainReviewTier(wantReviewModel, false, wantReviewEffort, false)
		if err == nil || !strings.Contains(err.Error(), "--review-effort") || !strings.Contains(err.Error(), "PLANWERK_BRAIN_REVIEW_EFFORT") {
			t.Errorf("err = %v, want it to name the flag and the variable", err)
		}
	})
}
