// Package mdfence sizes Markdown backtick fences from the text they wrap.
//
// planwerk-agent quotes text it does not control inside backtick fences: a
// review thread's diff hunk, a model-written code snippet or suggested fix. A
// fixed ``` fence ends at the first line of that text that is itself a run of
// three or more backticks, and everything after it escapes the fence. In a
// prompt that is a prompt injection; in Markdown posted to GitHub it garbles
// the comment and lets the quoted text render as the bot's own words.
// CommonMark closes a backtick fence only on a run at least as long as the
// opening one, so a fence one tick longer than the longest run inside the text
// cannot be closed by it. This package is the one place that sizes such a
// fence, for prompts and for Markdown posted to GitHub alike.
package mdfence

import "strings"

// Ticks returns a run of backticks one longer than the longest run of
// backticks in s, and never shorter than three, so a fence built from it
// cannot be closed by any line inside s.
func Ticks(s string) string {
	longest, run := 0, 0
	for _, r := range s {
		if r == '`' {
			if run++; run > longest {
				longest = run
			}
		} else {
			run = 0
		}
	}
	return strings.Repeat("`", max(longest+1, 3))
}

// Wrap returns s inside a backtick fence of Ticks(s):
// Ticks(s) + info + "\n" + s + "\n" + Ticks(s).
// info is the info string after the opening fence ("suggestion") or "" for
// none. Wrap does not trim s: a trailing newline in s becomes a blank line
// before the closing fence, so a caller that does not want one trims s first.
func Wrap(s, info string) string {
	t := Ticks(s)
	return t + info + "\n" + s + "\n" + t
}
