// Package impeccable finds the impeccable design skill among the Claude Code
// plugins the operator loads (--claude-plugin, decision 153) and runs its
// deterministic design detector, which needs no model and no API key. The ui
// review specialist reads the skill's reference documents and the detector's
// matches on the changed UI files as leads (decision 155).
//
// Nothing here copies impeccable's guidance: the reference documents are read
// where the installed plugin keeps them, and the detector runs through the
// launcher the plugin ships, so the version the operator installed is the one
// that runs.
package impeccable

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/planwerk/planwerk-agent/internal/claudeplugin"
)

const (
	// skillName is the name the impeccable skill carries in its plugin.
	skillName = "impeccable"
	// detectTimeout bounds one detector run, including the first run's
	// download of the engine binary the launcher fetches when no copy is
	// cached on the machine.
	detectTimeout = 3 * time.Minute
	// maxDetectOutput caps the detector's JSON output read into memory.
	maxDetectOutput = 8 << 20
	// maxTargets caps the files one run scans; a change that touches more
	// UI files is scanned on its first maxTargets, in path order.
	maxTargets = 200
	// exitFindings is the detector's exit code for a scan that completed and
	// matched at least one rule, 0 the code for a clean scan.
	exitFindings = 2
	// exitPartial is its code for a scan that could not read at least one of
	// the files it was given; the JSON it prints still holds the matches in
	// the others.
	exitPartial = 1
)

// Guide is one reference document a review of a diff reads: its file name in
// the skill's reference directory, and what it holds.
type Guide struct {
	File  string
	About string
}

// reviewGuides are the reference documents a review of a diff reads. Guides
// returns the ones the installed version ships.
var reviewGuides = []Guide{
	{File: "audit.md", About: "the technical checks: accessibility, performance, theming, and responsive behavior"},
	{File: "harden.md", About: "the states and edge cases a production interface handles: errors, empty and loading states, and long or translated text"},
}

// Kit is the impeccable skill inside a loaded plugin.
type Kit struct {
	// SkillDir is the skill's directory, absolute.
	SkillDir string
}

// warned holds the skill directories Find has warned about, so a command that
// builds more than one client from the same options warns once.
var warned sync.Map

// Find returns the impeccable skill of the first plugin that ships one with
// its launcher and its reference directory, and false when none does. A skill
// named impeccable that lacks either is logged as a warning, once per skill
// directory, since the ui specialist then runs without the design guidance
// the operator loaded the plugin for.
func Find(plugins []claudeplugin.Plugin) (Kit, bool) {
	for _, p := range plugins {
		for _, s := range p.Skills {
			if s.Name != skillName {
				continue
			}
			k := Kit{SkillDir: s.Dir}
			if isFile(k.launcher()) && isDir(k.ReferenceDir()) {
				return k, true
			}
			if _, seen := warned.LoadOrStore(s.Dir, true); !seen {
				slog.Warn("the impeccable skill has no launcher or no reference directory where this version of planwerk-agent looks; the ui specialist runs without them", "plugin", p.Label(), "skill_dir", s.Dir)
			}
		}
	}
	return Kit{}, false
}

// ReferenceDir returns the directory that holds the skill's reference
// documents.
func (k Kit) ReferenceDir() string { return filepath.Join(k.SkillDir, "reference") }

// ReviewGuides returns every reference document a review of a diff reads,
// whether or not an installed version ships it; Guides narrows it to the ones
// that one does.
func ReviewGuides() []Guide { return slices.Clone(reviewGuides) }

// Guides returns the reference documents a review of a diff reads that this
// version of the skill ships, in a fixed order.
func (k Kit) Guides() []Guide {
	var out []Guide
	for _, g := range reviewGuides {
		if isFile(filepath.Join(k.ReferenceDir(), g.File)) {
			out = append(out, g)
		}
	}
	return out
}

// launcher returns the path of the shell launcher the skill ships, which runs
// the engine binary next to it or in the user's cache.
func (k Kit) launcher() string { return filepath.Join(k.SkillDir, "scripts", "impeccable") }

// Finding is one detector match, as `detect --json` prints it.
type Finding struct {
	Rule     string `json:"antipattern"`
	Name     string `json:"name"`
	Severity string `json:"severity"`
	Category string `json:"category"`
	// File is the path the match is in, relative to the scanned checkout once
	// Detect returns.
	File string `json:"file"`
	// Line is 1-based; 0 means the match has no line, as for a whole page.
	Line    int    `json:"line"`
	Snippet string `json:"snippet"`
}

// Detect runs the detector over files, paths relative to dir, and returns its
// matches in those files, sorted by file and line with each File relative to
// dir; a match the detector reports in any other file is dropped. It scans
// only the files that exist as regular files, at most maxTargets of them, and
// returns nil without running anything when none is left. The scan ignores
// the checkout's own detector configuration (--no-config), so a change cannot
// silence a rule for the files it changes; and it runs with DO_NOT_TRACK=1.
// A scan that could not read some of the files keeps the matches in the rest,
// with a warning. Any other failure, output that does not parse, or the
// timeout is an error, and the caller proceeds without leads.
func (k Kit) Detect(dir string, files []string) ([]Finding, error) {
	targets := scanTargets(dir, files)
	if len(targets) == 0 {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), detectTimeout)
	defer cancel()

	// The launcher is a POSIX shell script; running it through sh spares it
	// the executable bit an archive extraction can drop.
	args := append([]string{k.launcher(), "detect", "--json", "--no-config"}, targets...)
	cmd := exec.CommandContext(ctx, "sh", args...)
	cmd.Dir = dir
	// cmd.Environ, read after Dir is set, carries the PWD of that directory.
	cmd.Env = append(cmd.Environ(), "DO_NOT_TRACK=1")
	cmd.WaitDelay = 2 * time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &limitedBuffer{buf: &stdout, max: maxDetectOutput}
	cmd.Stderr = &limitedBuffer{buf: &stderr, max: 4 << 10}
	err := cmd.Run()
	if ctx.Err() != nil {
		return nil, fmt.Errorf("impeccable detect: timed out after %s", detectTimeout)
	}
	code := 0
	if err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			return nil, fmt.Errorf("impeccable detect: %w", err)
		}
		code = exit.ExitCode()
	}
	var found []Finding
	decodeErr := json.Unmarshal(stdout.Bytes(), &found)
	msg := firstLine(strings.TrimSpace(stderr.String()))
	switch {
	case code == 0 || code == exitFindings:
		if decodeErr != nil {
			return nil, fmt.Errorf("decoding impeccable detect --json: %w", decodeErr)
		}
	case code == exitPartial && decodeErr == nil:
		slog.Warn("the design detector could not scan every changed UI file", "detail", msg)
	case msg != "":
		return nil, fmt.Errorf("impeccable detect: %w: %s", err, msg)
	default:
		return nil, fmt.Errorf("impeccable detect: %w", err)
	}
	// Keep the matches in the files the scan was given. The detector follows
	// a page's relative stylesheet links, which can lead outside the
	// checkout, and a match it reported there would quote that file.
	scanned := make(map[string]bool, len(targets))
	for _, t := range targets {
		scanned[filepath.ToSlash(strings.TrimPrefix(t, "."+string(filepath.Separator)))] = true
	}
	kept := found[:0]
	for _, f := range found {
		f.File = relativeTo(dir, f.File)
		if scanned[f.File] {
			kept = append(kept, f)
		}
	}
	if dropped := len(found) - len(kept); dropped > 0 {
		slog.Debug("dropped design detector matches outside the scanned files", "dropped", dropped)
	}
	found = kept
	slices.SortStableFunc(found, func(a, b Finding) int {
		if c := strings.Compare(a.File, b.File); c != 0 {
			return c
		}
		return a.Line - b.Line
	})
	return found, nil
}

// scanTargets returns the detector arguments for files: each one that is a
// regular file inside dir once every symlink on its path is resolved,
// prefixed with "./" so a name that starts with a dash cannot read as a flag,
// sorted, and capped at maxTargets. A change can commit a directory as a
// symlink to a path outside the checkout and list a file under it as
// deleted; resolving the whole path keeps the detector from reading, and the
// prompt from quoting, a file the machine holds outside the checkout.
func scanTargets(dir string, files []string) []string {
	root, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, f := range files {
		rel := filepath.Clean(filepath.FromSlash(f))
		if rel == "." || filepath.IsAbs(rel) || !inside(root, filepath.Join(root, rel)) {
			continue
		}
		path := filepath.Join(dir, rel)
		fi, err := os.Lstat(path)
		if err != nil || !fi.Mode().IsRegular() {
			continue
		}
		if resolved, err := filepath.EvalSymlinks(path); err != nil || !inside(root, resolved) {
			continue
		}
		out = append(out, "."+string(filepath.Separator)+rel)
	}
	slices.Sort(out)
	out = slices.Compact(out)
	if len(out) > maxTargets {
		slog.Info("the change touches more UI files than the design detector scans", "files", len(out), "scanned", maxTargets)
		out = out[:maxTargets]
	}
	return out
}

// inside reports whether the clean path lies within the clean root.
func inside(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// relativeTo returns path relative to dir when it lies inside it, comparing
// both as given and with symlinks resolved (a temp dir is a symlink on some
// systems), and path unchanged otherwise.
func relativeTo(dir, path string) string {
	if !filepath.IsAbs(path) {
		return filepath.ToSlash(strings.TrimPrefix(path, "./"))
	}
	bases := []string{dir}
	if resolved, err := filepath.EvalSymlinks(dir); err == nil && resolved != dir {
		bases = append(bases, resolved)
	}
	for _, base := range bases {
		rel, err := filepath.Rel(base, path)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return filepath.ToSlash(rel)
		}
	}
	return path
}

// limitedBuffer keeps the first max bytes written to it and drops the rest,
// reporting every write as complete so the child is never blocked on a full
// pipe.
type limitedBuffer struct {
	buf *bytes.Buffer
	max int
}

func (l *limitedBuffer) Write(p []byte) (int, error) {
	if room := l.max - l.buf.Len(); room > 0 {
		l.buf.Write(p[:min(room, len(p))])
	}
	return len(p), nil
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

func isFile(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.Mode().IsRegular()
}

func isDir(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}
