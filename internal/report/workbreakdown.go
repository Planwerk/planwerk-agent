package report

import (
	"regexp"
	"slices"
	"strings"
)

// Work package states an implementation report's "### Work Breakdown
// Coverage" entries carry, in the shape "- <package> — <state> — <evidence>".
const (
	WorkPackageDone       = "done"
	WorkPackagePartial    = "partial"
	WorkPackageNotStarted = "not started"
)

// workBreakdownHeading is the report section WorkBreakdownStates reads.
const workBreakdownHeading = "Work Breakdown Coverage"

// entrySeparators are the separators an entry's state follows, in the order
// entryState tries them: the template's spaced em dash, then a spaced en dash
// or hyphen, then a colon.
var entrySeparators = []*regexp.Regexp{
	regexp.MustCompile(`\s+—+\s+`),
	regexp.MustCompile(`\s+[–-]+\s+`),
	regexp.MustCompile(`:\s+`),
}

// stateWord matches a state at the start of an entry's state field,
// tolerating bold, italic, and code decoration around it.
var stateWord = regexp.MustCompile("(?i)^[*_`]*(done|partial|not started)[*_`]*(?:[\\s—–:(,.;]|$)")

// entryFieldSep splits an entry on every separator kind at once.
var entryFieldSep = regexp.MustCompile(`\s+[—–-]+\s+|:\s+`)

// stateField matches a field that is a state word and nothing else.
var stateField = regexp.MustCompile("(?i)^[*_`]*(done|partial|not started)[*_`]*[.;]?$")

// fenceOpen matches a fenced code block's opening line after its indent:
// three or more backticks with no backtick after them, or three or more tildes.
var fenceOpen = regexp.MustCompile("^(`{3,})[^`]*$|^(~{3,})")

// noPackageSentinel matches the "None — the issue is a single undivided
// change" entry: "None" alone or followed by a spaced dash, so "None handling"
// and "None-handling" still name packages.
var noPackageSentinel = regexp.MustCompile("(?i)^none[*_`]*(?:\\s+[—–-]+(?:\\s|$)|$)")

// WorkBreakdownStates returns the state of every entry in text's "Work
// Breakdown Coverage" section, in order: WorkPackageDone,
// WorkPackagePartial, WorkPackageNotStarted, or "" for an entry whose state
// it does not recognize.
//
// The section starts at the first ATX heading whose title, stripped of "#",
// spaces, and "*", is "Work Breakdown Coverage" in any case, and ends at the
// next heading of the same or a higher level; a deeper sub-heading stays
// inside it. An ATX heading is up to three spaces, one to six "#", then a
// space, a tab, or the line's end, so "#812's helper" is text; a line inside
// a fenced code block is no heading, so a shell comment there cannot end the
// section. The block opens on three or more backticks or tildes and closes
// only on a line holding nothing but a run of the same character at least as
// long, so a quoted fence inside it stays content. An entry is a "- " or "* "
// bullet indented by fewer than two spaces; a more deeply indented bullet
// continues the evidence of the entry above it, and a tab-indented or
// numbered line is no entry. An entry that is one parenthetical (an echoed
// instruction) or the "None — …" sentinel names no package and is skipped.
//
// An entry's state is the field after its first separator of the first kind
// it contains — a spaced em dash, then a spaced en dash or hyphen, then a
// colon — so its title cannot supply one: "WP2: Done marker — partial" is
// partial, and "Helm chart — in progress — half-done" is "". A done read there
// yields to the first field past the entry's first that is exactly a state
// word or, past a done-led field, led by partial or not started, so fields
// that disagree never read as done: "WP2: partial — done: the parser" is
// partial, and "WP3 — Done verdict — not started (no commits)" is not started.
//
// It returns nil when the heading is absent or no entry survives the skips, so
// a caller cannot confuse "no packages" with "every package done".
func WorkBreakdownStates(text string) []string {
	var states []string
	level := 0  // the section heading's level once found
	fence := "" // the open fenced block's marker run, "" outside one
	for _, line := range strings.Split(text, "\n") {
		t := strings.TrimLeft(line, " ")
		if fence == "" {
			if m := fenceOpen.FindStringSubmatch(t); m != nil {
				fence = m[1] + m[2]
				continue
			}
		} else if strings.HasPrefix(t, fence) && strings.TrimRight(strings.TrimLeft(t, fence[:1]), " \t\r") == "" {
			fence = ""
			continue
		}
		if n := headingLevel(line); n > 0 && fence == "" {
			if level > 0 {
				if n <= level {
					break
				}
				continue
			}
			if strings.EqualFold(strings.Trim(strings.TrimSpace(line), "# *"), workBreakdownHeading) {
				level = n
			}
			continue
		}
		if level == 0 {
			continue
		}
		rest := strings.TrimPrefix(line, " ")
		if !strings.HasPrefix(rest, "- ") && !strings.HasPrefix(rest, "* ") {
			continue
		}
		if namesNoPackage(rest[2:]) {
			continue
		}
		states = append(states, entryState(rest[2:]))
	}
	return states
}

// headingLevel returns the ATX heading level (1-6) of line, or 0 when line is
// no heading.
func headingLevel(line string) int {
	s := strings.TrimLeft(line, " ")
	if len(line)-len(s) > 3 {
		return 0
	}
	n := len(s) - len(strings.TrimLeft(s, "#"))
	if n == 0 || n > 6 || (n < len(s) && s[n] != ' ' && s[n] != '\t') {
		return 0
	}
	return n
}

// entryState returns the state in an entry's text after its bullet marker, or
// "" when the field after its separator holds no state.
func entryState(entry string) string {
	state := ""
	for _, sep := range entrySeparators {
		loc := sep.FindStringIndex(entry)
		if loc == nil {
			continue
		}
		if m := stateWord.FindStringSubmatch(entry[loc[1]:]); m != nil {
			state = strings.ToLower(m[1])
		}
		break
	}
	if state != WorkPackageDone {
		return state
	}
	// A done here can be a title's word after the title's own dash, or the
	// evidence after a state a colon carried. A field that is exactly a state
	// word names the state; past the first done-led field, a field led by
	// partial or not started names it too, so trailing words cannot hide it.
	seenDone := false
	for _, f := range entryFieldSep.Split(entry, -1)[1:] {
		f = strings.TrimSpace(f)
		if m := stateField.FindStringSubmatch(f); m != nil {
			return strings.ToLower(m[1])
		}
		m := stateWord.FindStringSubmatch(f)
		switch {
		case m == nil:
		case strings.EqualFold(m[1], WorkPackageDone):
			seenDone = true
		case seenDone:
			return strings.ToLower(m[1])
		}
	}
	return state
}

// namesNoPackage reports whether an entry's text after its bullet marker is
// the "None — …" sentinel or a parenthesized instruction rather than a work
// package. The instruction is one parenthetical, its first ")" its last rune,
// so "(b) Renderer — not started — (nothing yet)" is a package.
func namesNoPackage(entry string) bool {
	entry = strings.Trim(strings.TrimSpace(entry), "*_`")
	if strings.HasPrefix(entry, "(") && strings.IndexByte(entry, ')') == len(entry)-1 {
		return true
	}
	return noPackageSentinel.MatchString(entry)
}

// WorkBreakdownComplete reports whether text's Work Breakdown Coverage lists
// at least one work package and every one of them as done.
func WorkBreakdownComplete(text string) bool {
	states := WorkBreakdownStates(text)
	return len(states) > 0 && !slices.ContainsFunc(states, func(s string) bool { return s != WorkPackageDone })
}
