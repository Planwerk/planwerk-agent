package mirror

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/planwerk/planwerk-agent/internal/patterns"
)

func TestWiki_ClonesThePinnedWikiAndStoresItsHead(t *testing.T) {
	root := t.TempDir()
	wiki := &fakeWiki{head: testWikiHead}
	s := newSyncer(testGitHub(), wiki)
	opts := Options{Root: root, Wiki: patterns.WikiOptions{Repo: "acme/handbook", Ref: "v2"}}
	dir := mirrorDir(root)

	out, err := runSync(t, s, opts)
	if err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if want := (wikiCall{repo: "acme/handbook", ref: "v2", dest: filepath.Join(dir, "wiki")}); len(wiki.calls) != 1 || wiki.calls[0] != want {
		t.Errorf("clone calls = %+v, want one of %+v", wiki.calls, want)
	}
	if st := readState(t, dir); st.Wiki != (WikiState{Repo: "acme/handbook", Commit: testWikiHead}) {
		t.Errorf("state.Wiki = %+v, want the wiki and its HEAD", st.Wiki)
	}
	if !strings.HasSuffix(out, "wiki: acme/handbook.wiki at 1a2b3c4\n") {
		t.Errorf("output = %q, want the wiki line without \"unchanged\"", out)
	}

	// The same HEAD on the next run is reported as unchanged.
	out, err = runSync(t, s, opts)
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if !strings.HasSuffix(out, "wiki: acme/handbook.wiki at 1a2b3c4, unchanged\n") {
		t.Errorf("output = %q, want the wiki line with \"unchanged\"", out)
	}

	// A new HEAD, and the same HEAD of another wiki, are changes.
	wiki.head = "9f8e7d6c5b4a39281706f5e4d3c2b1a098765432"
	if out, _ = runSync(t, s, opts); !strings.HasSuffix(out, "wiki: acme/handbook.wiki at 9f8e7d6\n") {
		t.Errorf("output = %q, want the new HEAD without \"unchanged\"", out)
	}
	if out, _ = runSync(t, s, Options{Root: root}); !strings.HasSuffix(out, "wiki: acme/widgets.wiki at 9f8e7d6\n") {
		t.Errorf("output = %q, want the repository's own wiki without \"unchanged\"", out)
	}
	if st := readState(t, dir); st.Wiki.Repo != testRepo {
		t.Errorf("state.Wiki = %+v, want the repository's own wiki", st.Wiki)
	}
}

// TestWiki_FailedCloneKeepsTheMirror covers a wiki that cannot be cloned, as
// for a repository without one: the clone and the state of the last run stay,
// and the run still finishes.
func TestWiki_FailedCloneKeepsTheMirror(t *testing.T) {
	root := t.TempDir()
	wiki := &fakeWiki{head: testWikiHead}
	s := newSyncer(testGitHub(), wiki)
	if _, err := runSync(t, s, Options{Root: root}); err != nil {
		t.Fatalf("first Run: %v", err)
	}
	dir := mirrorDir(root)

	wiki.err = errors.New("clone: exit status 128")
	logs := captureLogs(t)
	out, err := runSync(t, s, Options{Root: root})
	if err != nil {
		t.Fatalf("a failed wiki clone must not fail the run: %v", err)
	}
	if !strings.HasSuffix(out, "wiki: acme/widgets.wiki not mirrored\n") {
		t.Errorf("output = %q, want the not-mirrored line", out)
	}
	if home, err := os.ReadFile(filepath.Join(dir, "wiki", "Home.md")); err != nil || string(home) != testWikiHead {
		t.Errorf("the clone of the last run must stay: %q, %v", home, err)
	}
	st := readState(t, dir)
	if st.Wiki != (WikiState{Repo: testRepo, Commit: testWikiHead}) || st.SyncedAt == "" {
		t.Errorf("state = %+v, want the wiki of the last run and a sync time", st)
	}
	for _, want := range []string{"could not mirror the wiki; keeping what the mirror holds", "acme/widgets", "exit status 128"} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("log %q lacks %q", logs, want)
		}
	}
}

// TestWiki_NoWikiOnAFirstRun covers a repository that never had a wiki: the
// state names none, and the run finishes.
func TestWiki_NoWikiOnAFirstRun(t *testing.T) {
	root := t.TempDir()
	s := newSyncer(testGitHub(), &fakeWiki{err: errors.New("repository not found")})
	out, err := runSync(t, s, Options{Root: root})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.HasSuffix(out, "wiki: acme/widgets.wiki not mirrored\n") {
		t.Errorf("output = %q", out)
	}
	st := readState(t, mirrorDir(root))
	if st.Wiki != (WikiState{}) || st.SyncedAt != fixedNowStamp {
		t.Errorf("state = %+v, want no wiki and a finished sync", st)
	}
}

// TestWiki_EmptyWikiHasNoHead covers a wiki whose HEAD cannot be resolved: the
// summary says so, and an empty commit never counts as unchanged.
func TestWiki_EmptyWikiHasNoHead(t *testing.T) {
	root := t.TempDir()
	s := newSyncer(testGitHub(), &fakeWiki{head: ""})
	for range 2 {
		out, err := runSync(t, s, Options{Root: root})
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if !strings.HasSuffix(out, "wiki: acme/widgets.wiki at unknown\n") {
			t.Errorf("output = %q, want an unknown HEAD and no \"unchanged\"", out)
		}
	}
}
