// Package diagramdesign finds the diagram-design skill among the Claude Code
// plugins the operator loads (--claude-plugin, decision 153) and runs the two
// checkers it ships, which need no model, no API key, and nothing beyond
// Python's standard library. The diagram review specialist reads the skill's
// reference documents and the checkers' reports on the changed HTML diagrams
// as leads (decision 179), and the sessions that edit draw a diagram with the
// skill and run the same checkers on it before they commit it (decision 180).
//
// Nothing here copies diagram-design's guidance: the reference documents are
// read where the installed plugin keeps them, and the checkers run from its
// scripts directory, so the version the operator installed is the one that
// runs.
package diagramdesign

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/planwerk/planwerk-agent/internal/claudeplugin"
	"github.com/planwerk/planwerk-agent/internal/pluginscan"
)

const (
	// skillName is the name the diagram-design skill carries in its plugin.
	skillName = "diagram-design"
	// python runs the checkers. They are Python scripts that import only
	// the standard library.
	python = "python3"
	// checkTimeout bounds one Check, every checker run on every file
	// together.
	checkTimeout = 3 * time.Minute
	// maxCheckOutput caps what one checker run prints that is read into
	// memory.
	maxCheckOutput = 1 << 20
	// maxTargets caps the files one Check reads; a change that touches more
	// HTML diagrams is checked on its first maxTargets, in path order.
	maxTargets = 100
	// exitFailures is the checkers' exit code for a file they found at least
	// one problem in, 0 the code for a clean one.
	exitFailures = 1
)

// The checkers the skill ships, by their file name in its scripts directory.
const (
	// SelfCheck holds a diagram to the skill's accessible-SVG contract, its
	// single-file rules, document completeness, and its viewBox.
	SelfCheck = "self_check.py"
	// VerifyGeometry holds it to the skill's connector rules and finds label
	// masks a later node covers.
	VerifyGeometry = "verify_geometry.py"
)

// Guide is one reference document a review of a diagram reads: its file name
// in the skill's reference directory, and what it holds.
type Guide struct {
	File  string
	About string
}

// reviewGuides are the reference documents a review of a diagram reads.
// Guides returns the ones the installed version ships.
var reviewGuides = []Guide{
	{File: "primitives-core.md", About: "the connector rules and the accessible SVG contract a diagram drawn with the skill keeps"},
	{File: "layout-budget.md", About: "the complexity budget of each diagram type: how many nodes, connectors, lanes, or series one view holds"},
}

// Kit is the diagram-design skill inside a loaded plugin.
type Kit struct {
	// SkillDir is the skill's directory, absolute.
	SkillDir string
	// Skill is the name the Skill tool knows the skill by:
	// "<plugin>:diagram-design".
	Skill string
}

// warned holds the skill directories Find has warned about, so a command that
// builds more than one client from the same options warns once.
var warned sync.Map

// Find returns the diagram-design skill of the first plugin that ships one
// with both checkers and its reference directory, and false when none does.
// A skill named diagram-design that lacks any of them is logged as a warning,
// once per skill directory, since the diagram specialist then runs without
// the guidance the operator loaded the plugin for.
func Find(plugins []claudeplugin.Plugin) (Kit, bool) {
	for _, p := range plugins {
		for _, s := range p.Skills {
			if s.Name != skillName {
				continue
			}
			k := Kit{SkillDir: s.Dir, Skill: p.SkillID(s.Name)}
			if isFile(k.Script(SelfCheck)) && isFile(k.Script(VerifyGeometry)) && isDir(k.ReferenceDir()) {
				return k, true
			}
			if _, seen := warned.LoadOrStore(s.Dir, true); !seen {
				slog.Warn("the diagram-design skill has no checkers or no reference directory where this version of planwerk-agent looks; the diagram specialist runs without them", "plugin", p.Label(), "skill_dir", s.Dir)
			}
		}
	}
	return Kit{}, false
}

// ReferenceDir returns the directory that holds the skill's reference
// documents.
func (k Kit) ReferenceDir() string { return filepath.Join(k.SkillDir, "references") }

// ReviewGuides returns every reference document a review of a diagram reads,
// whether or not an installed version ships it; Guides narrows it to the ones
// that one does.
func ReviewGuides() []Guide { return slices.Clone(reviewGuides) }

// Guides returns the reference documents a review of a diagram reads that
// this version of the skill ships, in a fixed order.
func (k Kit) Guides() []Guide {
	var out []Guide
	for _, g := range reviewGuides {
		if isFile(filepath.Join(k.ReferenceDir(), g.File)) {
			out = append(out, g)
		}
	}
	return out
}

// HasTypeReferences reports whether this version of the skill ships the
// per-type references, type-<name>.md, that hold each diagram type's layout
// rules and anti-patterns.
func (k Kit) HasTypeReferences() bool {
	m, err := filepath.Glob(filepath.Join(k.ReferenceDir(), "type-*.md"))
	return err == nil && len(m) > 0
}

// Script returns the absolute path of the named checker, SelfCheck or
// VerifyGeometry, in the skill's scripts directory.
func (k Kit) Script(name string) string { return filepath.Join(k.SkillDir, "scripts", name) }

// Finding is one problem a checker reported.
type Finding struct {
	// Check is the checker's file name, SelfCheck or VerifyGeometry.
	Check string
	// File is the path of the diagram, relative to the checked checkout.
	File string
	// Line is 1-based; 0 means the report names no line.
	Line    int
	Message string
}

// Check runs both checkers over files, paths relative to dir, one file at a
// time, since verify_geometry.py names a file by its base name alone. It
// returns their reports sorted by file and line, each File relative to dir.
// It checks only the files that exist as regular files inside dir
// (pluginscan.Targets), at most maxTargets of them, and returns nil without
// running anything when none is left. The checkers run as `python3 -I`, so
// neither the environment nor a directory on the path changes which modules
// they import, with DO_NOT_TRACK=1 and dir as their working directory. An exit
// code other than 0 or exitFailures, output without the line a finished run
// prints, or the timeout is an error, and the caller proceeds without leads.
func (k Kit) Check(dir string, files []string) ([]Finding, error) {
	targets := pluginscan.Targets(dir, files)
	if len(targets) == 0 {
		return nil, nil
	}
	if len(targets) > maxTargets {
		slog.Info("the change touches more HTML diagrams than the diagram checkers read", "files", len(targets), "checked", maxTargets)
		targets = targets[:maxTargets]
	}
	ctx, cancel := context.WithTimeout(context.Background(), checkTimeout)
	defer cancel()

	var found []Finding
	for _, t := range targets {
		for _, check := range []string{SelfCheck, VerifyGeometry} {
			out, err := k.run(ctx, dir, check, t)
			if err != nil {
				return nil, err
			}
			parse := parseSelfCheck
			if check == VerifyGeometry {
				parse = parseGeometry
			}
			fs, ok := parse(out, t)
			if !ok {
				return nil, fmt.Errorf("%s %s: output without the line a finished run prints", check, pluginscan.Path(t))
			}
			found = append(found, fs...)
		}
	}
	slices.SortStableFunc(found, func(a, b Finding) int {
		if c := strings.Compare(a.File, b.File); c != 0 {
			return c
		}
		return a.Line - b.Line
	})
	return found, nil
}

// run runs one checker on one target and returns what it printed to stdout.
func (k Kit) run(ctx context.Context, dir, check, target string) (string, error) {
	cmd := exec.CommandContext(ctx, python, "-I", k.Script(check), target)
	cmd.Dir = dir
	// cmd.Environ, read after Dir is set, carries the PWD of that directory.
	cmd.Env = append(cmd.Environ(), "DO_NOT_TRACK=1")
	cmd.WaitDelay = 2 * time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &pluginscan.LimitedBuffer{Buf: &stdout, Max: maxCheckOutput}
	cmd.Stderr = &pluginscan.LimitedBuffer{Buf: &stderr, Max: 4 << 10}
	err := cmd.Run()
	if ctx.Err() != nil {
		return "", fmt.Errorf("diagram-design checkers: timed out after %s", checkTimeout)
	}
	if err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != exitFailures {
			if msg := pluginscan.FirstLine(strings.TrimSpace(stderr.String())); msg != "" {
				return "", fmt.Errorf("%s: %w: %s", check, err, msg)
			}
			return "", fmt.Errorf("%s: %w", check, err)
		}
	}
	return stdout.String(), nil
}

// parseSelfCheck reads what self_check.py prints for one file: "OK <path>" for
// a clean one, or "FAIL <path>" followed by one "  - <problem>" line per
// problem. ok is false when neither status line is there.
func parseSelfCheck(out, target string) (found []Finding, ok bool) {
	file := pluginscan.Path(target)
	for line := range strings.Lines(out) {
		line = strings.TrimRight(line, "\r\n")
		switch {
		case strings.HasPrefix(line, "OK "), strings.HasPrefix(line, "FAIL "):
			ok = true
		case ok && strings.HasPrefix(line, "  - "):
			found = append(found, Finding{Check: SelfCheck, File: file, Message: strings.TrimPrefix(line, "  - ")})
		}
	}
	return found, ok
}

// parseGeometry reads what verify_geometry.py prints for one file: one
// "<base name>[:<line>]: <problem>" line per problem, then a "Summary:" line.
// ok is false when the summary is missing.
func parseGeometry(out, target string) (found []Finding, ok bool) {
	file := pluginscan.Path(target)
	prefix := filepath.Base(target) + ":"
	for line := range strings.Lines(out) {
		line = strings.TrimRight(line, "\r\n")
		if strings.HasPrefix(line, "Summary:") {
			ok = true
			continue
		}
		rest, named := strings.CutPrefix(line, prefix)
		if !named || ok {
			continue
		}
		f := Finding{Check: VerifyGeometry, File: file}
		if num, msg, cut := strings.Cut(rest, ":"); cut {
			if n, err := strconv.Atoi(num); err == nil && n > 0 {
				f.Line, rest = n, msg
			}
		}
		f.Message = strings.TrimSpace(rest)
		found = append(found, f)
	}
	return found, ok
}

func isFile(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.Mode().IsRegular()
}

func isDir(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}
