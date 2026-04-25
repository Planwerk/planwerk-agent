package patterns

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestIsRemote(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"github:owner/repo", true},
		{"github:owner/repo/sub@v1", true},
		{"git+https://example.com/x.git", true},
		{"git+http://example.com/x.git", true},
		{"./patterns", false},
		{"/abs/path", false},
		{"patterns", false},
		{"https://example.com/x.git", false}, // no git+ prefix → local-looking
	}
	for _, tc := range cases {
		if got := IsRemote(tc.in); got != tc.want {
			t.Errorf("IsRemote(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestParseRemoteURI(t *testing.T) {
	cases := []struct {
		name        string
		in          string
		wantScheme  string
		wantClone   string
		wantRef     string
		wantSubpath string
		wantErr     bool
	}{
		{
			name:       "github simple",
			in:         "github:planwerk/patterns",
			wantScheme: "github",
			wantClone:  "planwerk/patterns",
		},
		{
			name:        "github with subpath",
			in:          "github:planwerk/patterns/security",
			wantScheme:  "github",
			wantClone:   "planwerk/patterns",
			wantSubpath: "security",
		},
		{
			name:        "github with subpath and ref",
			in:          "github:planwerk/patterns/security/web@v1.2.3",
			wantScheme:  "github",
			wantClone:   "planwerk/patterns",
			wantRef:     "v1.2.3",
			wantSubpath: "security/web",
		},
		{
			name:       "github with ref no subpath",
			in:         "github:planwerk/patterns@main",
			wantScheme: "github",
			wantClone:  "planwerk/patterns",
			wantRef:    "main",
		},
		{
			name:       "git https plain",
			in:         "git+https://example.com/x.git",
			wantScheme: "git",
			wantClone:  "https://example.com/x.git",
		},
		{
			name:       "git http plain",
			in:         "git+http://example.com/x.git",
			wantScheme: "git",
			wantClone:  "http://example.com/x.git",
		},
		{
			name:       "git https with ref",
			in:         "git+https://example.com/x.git#v1.0",
			wantScheme: "git",
			wantClone:  "https://example.com/x.git",
			wantRef:    "v1.0",
		},
		{
			name:        "git https with ref and subpath",
			in:          "git+https://example.com/x.git#main:patterns/sec",
			wantScheme:  "git",
			wantClone:   "https://example.com/x.git",
			wantRef:     "main",
			wantSubpath: "patterns/sec",
		},
		{
			name:        "git https with subpath only",
			in:          "git+https://example.com/x.git#:patterns",
			wantScheme:  "git",
			wantClone:   "https://example.com/x.git",
			wantSubpath: "patterns",
		},
		{
			name:    "github invalid",
			in:      "github:not-a-repo",
			wantErr: true,
		},
		{
			name:    "git empty url",
			in:      "git+https://",
			wantErr: false, // trims to https:// — caller's clone will fail loudly
			wantScheme: "git",
			wantClone:  "https://",
		},
		{
			name:    "unknown prefix",
			in:      "ftp://example.com/x",
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, err := parseRemoteURI(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("parseRemoteURI(%q) = no error, want error", tc.in)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseRemoteURI(%q) error: %v", tc.in, err)
			}
			if p.scheme != tc.wantScheme {
				t.Errorf("scheme = %q, want %q", p.scheme, tc.wantScheme)
			}
			if p.cloneURL != tc.wantClone {
				t.Errorf("cloneURL = %q, want %q", p.cloneURL, tc.wantClone)
			}
			if p.ref != tc.wantRef {
				t.Errorf("ref = %q, want %q", p.ref, tc.wantRef)
			}
			if p.subpath != tc.wantSubpath {
				t.Errorf("subpath = %q, want %q", p.subpath, tc.wantSubpath)
			}
		})
	}
}

func TestResolveRemote_EnvVarExpansion(t *testing.T) {
	t.Setenv("PLANWERK_TEST_TOKEN", "secret-token-value")
	cacheDir := t.TempDir()

	var capturedURL string
	restore := stubFetch(func(p parsedURI, dest string) error {
		capturedURL = p.cloneURL
		// Materialize a minimal repo dir so the loader has something to look at.
		return os.MkdirAll(dest, 0o700)
	})
	defer restore()

	src := "git+https://oauth2:${PLANWERK_TEST_TOKEN}@host.example/team/p.git"
	if _, err := ResolveRemote(src, RemoteOptions{CacheDir: cacheDir}); err != nil {
		t.Fatalf("ResolveRemote: %v", err)
	}
	want := "https://oauth2:secret-token-value@host.example/team/p.git"
	if capturedURL != want {
		t.Errorf("env var not expanded: cloneURL = %q, want %q", capturedURL, want)
	}
}

func TestResolveRemote_CachesAcrossCalls(t *testing.T) {
	cacheDir := t.TempDir()
	var calls atomic.Int64
	restore := stubFetch(func(p parsedURI, dest string) error {
		calls.Add(1)
		return os.MkdirAll(dest, 0o700)
	})
	defer restore()

	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	opts := RemoteOptions{
		CacheDir: cacheDir,
		TTL:      time.Hour,
		Now:      func() time.Time { return now },
	}

	// First call → fetches.
	if _, err := ResolveRemote("github:planwerk/patterns", opts); err != nil {
		t.Fatal(err)
	}
	// Second call within TTL → reuses.
	if _, err := ResolveRemote("github:planwerk/patterns", opts); err != nil {
		t.Fatal(err)
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("fetch count = %d, want 1 (second call should hit cache)", got)
	}

	// Advance past TTL → refreshes.
	opts.Now = func() time.Time { return now.Add(2 * time.Hour) }
	if _, err := ResolveRemote("github:planwerk/patterns", opts); err != nil {
		t.Fatal(err)
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("fetch count = %d, want 2 (third call past TTL should refresh)", got)
	}
}

func TestResolveRemote_TTLZeroDisablesRefresh(t *testing.T) {
	cacheDir := t.TempDir()
	var calls atomic.Int64
	restore := stubFetch(func(p parsedURI, dest string) error {
		calls.Add(1)
		return os.MkdirAll(dest, 0o700)
	})
	defer restore()

	opts := RemoteOptions{
		CacheDir: cacheDir,
		TTL:      0,
		// Even years later, TTL=0 means never refresh.
		Now: func() time.Time { return time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC) },
	}
	if _, err := ResolveRemote("github:x/y", opts); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveRemote("github:x/y", opts); err != nil {
		t.Fatal(err)
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("fetch count = %d, want 1 (TTL=0 disables refresh)", got)
	}
}

func TestResolveRemote_Subpath(t *testing.T) {
	cacheDir := t.TempDir()
	restore := stubFetch(func(p parsedURI, dest string) error {
		// Materialize a repo with a subpath the URI references.
		return os.MkdirAll(filepath.Join(dest, "patterns", "security"), 0o700)
	})
	defer restore()

	dir, err := ResolveRemote("github:x/y/patterns/security", RemoteOptions{CacheDir: cacheDir})
	if err != nil {
		t.Fatalf("ResolveRemote: %v", err)
	}
	if !strings.HasSuffix(filepath.ToSlash(dir), "patterns/security") {
		t.Errorf("resolved dir = %q, expected to end in patterns/security", dir)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("resolved subpath should exist: %v", err)
	}
}

func TestResolveRemote_SubpathMissing(t *testing.T) {
	cacheDir := t.TempDir()
	restore := stubFetch(func(p parsedURI, dest string) error {
		return os.MkdirAll(dest, 0o700) // no subpath created
	})
	defer restore()

	_, err := ResolveRemote("github:x/y/missing", RemoteOptions{CacheDir: cacheDir})
	if err == nil {
		t.Fatal("expected error for missing subpath")
	}
	if !strings.Contains(err.Error(), "missing") {
		t.Errorf("error should name the missing subpath: %v", err)
	}
}

func TestResolveRemote_FetchFailure(t *testing.T) {
	cacheDir := t.TempDir()
	restore := stubFetch(func(p parsedURI, dest string) error {
		return errors.New("simulated clone failure")
	})
	defer restore()

	_, err := ResolveRemote("github:x/y", RemoteOptions{CacheDir: cacheDir})
	if err == nil {
		t.Fatal("expected error from failed fetch")
	}
	if !strings.Contains(err.Error(), "simulated clone failure") {
		t.Errorf("error should wrap underlying failure: %v", err)
	}
}

func TestResolveRemote_RejectsLocalPath(t *testing.T) {
	_, err := ResolveRemote("./patterns", RemoteOptions{})
	if err == nil {
		t.Fatal("expected error for local path")
	}
}

// stubFetch swaps the package-level fetchRemote with a test fake and returns
// a restore function to undo it.
func stubFetch(fn func(p parsedURI, dest string) error) func() {
	old := fetchRemote
	fetchRemote = fn
	return func() { fetchRemote = old }
}
