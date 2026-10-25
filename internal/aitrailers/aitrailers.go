// Package aitrailers reads whether a target repository's own contribution
// rules, as its base branch has them, forbid attributing commits to an AI
// through a commit trailer (decision 150).
//
// planwerk-agent's own convention ends every commit with an Assisted-by
// trailer above the Signed-off-by line. A repository may require the opposite,
// and its rule wins. The rule is read from the base ref rather than from the
// checkout, for the reason decision 97 gives: a pull request must not rewrite
// the rules its own commits are held to.
//
// No reader of free prose is exact, so the matcher favors precision. A missed
// rule leaves the run as it was; a false match would strip the attribution
// from a repository that wants it. A ban on Co-authored-by is deliberately not
// a match, even when it says what it is for ("do not use Co-authored-by for AI
// attribution"): planwerk-agent never writes that trailer, and such rules
// usually only switch off Claude Code's default co-author line, often right
// before they ask for Assisted-by.
package aitrailers

import (
	"log/slog"
	"regexp"
	"strings"

	"github.com/planwerk/planwerk-agent/internal/gitref"
)

// files are the instruction files a rule is read from, in precedence order:
// the first one that forbids the trailer is the one ForbiddenBy names. Entries
// are slash-separated repo-relative paths, the form the prompt cites.
var files = []string{
	"AGENTS.md",
	"CLAUDE.md",
	".claude/CLAUDE.md",
	"CONTRIBUTING.md",
	".github/CONTRIBUTING.md",
	"docs/CONTRIBUTING.md",
}

// maxFileBytes caps each instruction file read. Contribution rules are a few
// pages; a larger blob is not one and is skipped.
const maxFileBytes = 1 << 20

// ForbiddenBy returns the repo-relative path of the first instruction file
// whose text at ref, in the repository at repoDir, forbids attributing commits
// to an AI, or "" when none does. It returns "" when repoDir or ref is empty
// and when no file can be read, so a run without the rule keeps today's
// trailers. A match is logged with its file and line.
//
// The result is always an element of files, never repository text, so it is
// safe to interpolate into a prompt (decision 97).
func ForbiddenBy(repoDir, ref string) string {
	if repoDir == "" || ref == "" {
		return ""
	}
	for _, rel := range files {
		body, ok := gitref.Show(repoDir, ref, rel, maxFileBytes)
		if !ok {
			continue
		}
		if n, hit := forbiddingLine(body); hit {
			slog.Info("the repository forbids AI attribution trailers; commits carry Signed-off-by alone", "file", rel, "line", n, "ref", ref)
			return rel
		}
	}
	return ""
}

// aiWord names an AI in the patterns below: the generic terms and the
// assistants a contribution rule is likely to name.
const aiWord = `(?:ai|llms?|claude|chatgpt|gpt|copilot|gemini|codex|language models?)`

// trailerKey is a hyphenated trailer key as a list names it: "co-developed",
// "generated-by:".
const trailerKey = `[a-z]+(?:-[a-z]+)+:?`

// noTarget is a "no" right before an AI or the Assisted-by trailer.
const noTarget = `\bno\s+(?:` + aiWord + `|assisted-by)\b`

// coAuthorKey names the trailers whose ban does not count: Co-authored-by and
// the session-link keys an assistant writes (Claude-Session).
const coAuthorKey = `\b(?:co-authored-by|` + aiWord + `-session)\b`

var (
	// cueRe matches a prohibition. "no AI" and "no Assisted-by" (noTarget)
	// count as one only when the noun follows directly; a sentence in which
	// such a "no" does not open the statement does not count at all
	// (lacksTarget).
	cueRe = regexp.MustCompile(`\b(?:must not|must never|mustn't|do not|don't|does not|doesn't|never|shall not|should not|shouldn't|may not|cannot|can't|will not|won't|not allowed|not permitted|not accepted|not acceptable|not welcome|forbid\w*|prohibit\w*|disallow\w*|refrain from|avoid\w*)\b|` + noTarget)
	// noTargetRe matches a noTarget, and openerRe the text before one that
	// opens the statement: nothing, a list, table, heading, or quote marker, a
	// colon, an opening parenthesis, a table cell's bar, or a dash ("No
	// Assisted-by trailers.", "...: no Assisted-by trailers").
	noTargetRe = regexp.MustCompile(noTarget)
	openerRe   = regexp.MustCompile(`(?:^[-+|#>\s]*|[:(|]\s*|\s[—–]\s*)$`)
	// passiveRe matches a prohibition that follows its target in the same
	// clause, "Assisted-by trailers must not be added", "are not welcome", or
	// past a list of trailer keys that ends in "and" or "or": "Assisted-by,
	// Co-developed-by or similar trailers are not allowed". Any other comma
	// ends the clause ("Assisted-by is required, Co-authored-by is not
	// allowed").
	passiveRe = regexp.MustCompile(`^:?(?:(?:\s*,\s*` + trailerKey + `)*\s*,?\s+(?:and|or)\s+(?:` + trailerKey + `|(?:any\s+)?(?:other|similar)\b))?[^,;]*?\b(?:(?:must|should|may|shall|will|can)(?:not|\s+not|\s+never|n't)\s+be|(?:is|are)\s+(?:not\s+(?:allowed|permitted|accepted|acceptable|welcome)|forbidden|prohibited|disallowed))\b`)
	// counterRe matches a word that turns a rule around ("use Assisted-by
	// instead", "don't forget", "never remove", "commits that lack an
	// Assisted-by trailer will not be merged"). A sentence carrying one never
	// counts, whatever else it says, and neither does one lacksTarget reads
	// the same way.
	counterRe = regexp.MustCompile(`\b(?:instead|rather than|always|forget\w*|omit\w*|without|unless|except|missing|lack\w*|remov\w*|drop\w*|leave out|leaving out|skip\w*|must (?:add|include|use|end|carry|contain))\b`)
	// boundaryRe matches a clause boundary a prohibition does not reach
	// across: "never push to main, and add Assisted-by", or a dash.
	boundaryRe = regexp.MustCompile(`,\s+(?:and|but|then|so)\b|\s[—–]\s`)
	// nextVerbRe matches a comma that opens the next instruction, "don't use
	// Co-authored-by, use Assisted-by". Its group matches the comma or "and" /
	// "or" that makes the verb one of a list instead, which the prohibition
	// reaches across: "never write, add, or keep an Assisted-by trailer".
	nextVerbRe = regexp.MustCompile(`,\s+(?:use|add|write|include|put|append)\b(\s*,|\s+(?:and|or)\b)?`)
	// hedgeRe matches what follows a prohibition word that does not forbid the
	// trailer: "do not hesitate to add", "do not require", "don't need to", a
	// verb that only changes the trailer ("do not delete", "never strip", "do
	// not change the format of"), and "do not also add" or "do not add a
	// second", which forbid one more.
	hedgeRe = regexp.MustCompile(`^\s+(?:also|hesitate\w*|require\w*|need\w*|have to|delet\w*|strip\w*|rewrit\w*|edit\w*|chang\w*|alter\w*|modif\w*|reword\w*|reformat\w*|mov\w*|duplicat\w*|\w+\s+(?:an?\s+)?(?:second|duplicate|another|more than one|multiple))\b`)
	// condRe matches the word that opens a condition: a prohibition inside one
	// ("if you cannot determine the id, the trailer names the agent alone", "if
	// the project forbids AI trailers, follow its rule") is not the rule.
	// purposeRe matches the "to" of a to-infinitive that opens a clause and
	// whose verb is the prohibition: that is the purpose of the rule after it,
	// not the rule ("to avoid confusion, the Assisted-by trailer names the
	// model").
	condRe    = regexp.MustCompile(`\b(?:if|when|where)\b`)
	purposeRe = regexp.MustCompile(`^\s*(?:in order\s+)?to\s+$`)
	// negRe matches a negation in a condition, and scopeRe a subject, that
	// limit the rule after them to commits made without an AI: "if no AI tool
	// was involved, do not add an Assisted-by trailer", "human-only commits
	// carry no Assisted-by trailer".
	negRe   = regexp.MustCompile(`\b(?:no|not|never|none|nothing|cannot)\b|n't\b`)
	scopeRe = regexp.MustCompile(`\b(?:human(?:-[a-z]+)?|trivial|mechanical)\s+(?:commits?|changes?|contributions?|patches?)\b`)
	// insideRe and placeRe match a target that a rule only fills or places:
	// "the model id in the Assisted-by trailer", "Assisted-by below
	// Signed-off-by", "the Assisted-by trailer goes after the sign-off", "put
	// Assisted-by in the subject line". A prohibition there is about what goes
	// into the trailer or where it goes, not about the trailer. placeRe's group
	// matches an "or" or "and" past the subject, which names a further place,
	// "in the subject or body", and so a ban rather than a placement.
	insideRe = regexp.MustCompile(`\b(?:in|inside|within|into)\s+(?:the\s+|an?\s+|your\s+|its\s+|each\s+|every\s+)?$`)
	placeRe  = regexp.MustCompile(`^:?(?:\s+(?:trailers?|lines?|tags?))?(?:\s+(?:goes|go|comes|come|sits|sit|belongs|belong|appears|appear|(?:is|are) placed|(?:must|should) (?:go|come|sit|appear)))?\s+(?:(?:below|above|after|before|beneath|under)\b|in\s+(?:the\s+|an?\s+|your\s+)?(?:subject|title|summary|first line)(?:\s+lines?)?\b(\s*,?\s+(?:or|and)\b)?)`)

	// The four targets a prohibition can govern.
	assistedRe      = regexp.MustCompile(`\bassisted-by\b`)
	attributeToAIRe = regexp.MustCompile(`\battribut\w*\s+(?:\S+\s+){0,3}?(?:to|as)\s+(?:an?\s+|the\s+)?` + aiWord + `\b`)
	namesAIRe       = regexp.MustCompile(`\b(?:mention\w*|credit\w*|nam(?:e|es|ing))\s+(?:an?\s+|the\s+)?` + aiWord + `\b`)
	commitRe        = regexp.MustCompile(`\bcommit`)
	// aiPhraseRe allows one word between the AI and the noun ("AI co-author
	// trailers"), but only after a space, so a hyphenated trailer key such as
	// Claude-Session is not read as an AI phrase.
	aiPhraseRe = regexp.MustCompile(`\b` + aiWord + `(?:[-\s]|\s\S+[-\s])(?:attribut\w*|trailers?|credit\w*)\b`)

	// coAuthorRe matches a coAuthorKey. otherTrailerRe matches the AI trailers
	// that carry a sentence's ban past it, and so do the widening words:
	// widenRe right before a target ("or other AI trailers", "or any AI
	// attribution"), widenPastRe right after the key, past any further keys
	// ("including through Co-authored-by or similar trailers").
	coAuthorRe     = regexp.MustCompile(coAuthorKey)
	otherTrailerRe = regexp.MustCompile(`\b(?:assisted|co-developed|generated)-by\b`)
	widenRe        = regexp.MustCompile(`\b(?:other|any|similar)\s+$`)
	widenPastRe    = regexp.MustCompile(coAuthorKey + `:?(?:\s*,?\s+` + trailerKey + `)*\s*,?\s+(?:or|and)\s+(?:any\s+)?(?:other|similar)\b`)

	listItemRe    = regexp.MustCompile(`^\s*(?:[-*+|]|\d+[.)])\s+`)
	sentenceEndRe = regexp.MustCompile(`[.!?;]+(?:\s+|$)`)
	// norm strips the Markdown emphasis and quoting that splits a phrase,
	// folds curly apostrophes, and drops the periods of abbreviations so they
	// do not end a sentence.
	norm = strings.NewReplacer("’", "'", "‘", "'", "“", "", "”", "", "\"", "", "`", "", "*", "", "_", "", "e.g.", "eg", "i.e.", "ie", "etc.", "etc", "vs.", "vs")
)

// forbiddingLine reports the 1-based first line of the first statement in text
// that forbids attributing commits to an AI. A statement is a paragraph, a list
// item, a table row, or a heading: a blank line ends one, a list marker, table
// row, or "#" starts a new one, and other lines join the current one, so a rule
// wrapped over several lines is read whole. Lines inside a ``` or ~~~ fence are
// skipped, since an example is not a rule, and a fence ends the current
// statement.
func forbiddingLine(text string) (int, bool) {
	var unit []string
	start := 0
	inFence := false
	flush := func() (int, bool) {
		if len(unit) == 0 {
			return 0, false
		}
		u := strings.Join(unit, " ")
		unit = unit[:0]
		if forbids(u) {
			return start, true
		}
		return 0, false
	}
	for i, line := range strings.Split(text, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			if n, ok := flush(); ok {
				return n, true
			}
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		if t == "" || listItemRe.MatchString(line) || strings.HasPrefix(t, "#") {
			if n, ok := flush(); ok {
				return n, true
			}
		}
		if t == "" {
			continue
		}
		if len(unit) == 0 {
			start = i + 1
		}
		unit = append(unit, t)
	}
	return flush()
}

// forbids reports whether one sentence of stmt has a prohibition governing one
// of four targets: the Assisted-by trailer, an attribution of commits to an AI,
// an AI named in commits, or an "AI attribution" / "AI trailer" phrase. A
// sentence with a reversing word (counterRe) or a "no" before a target that
// does not open it (lacksTarget) never counts, and neither do the last three
// targets where they are read as part of a ban on Co-authored-by
// (coAuthorObject).
func forbids(stmt string) bool {
	s := norm.Replace(strings.ToLower(stmt))
	for _, sent := range sentenceEndRe.Split(s, -1) {
		if counterRe.MatchString(sent) || lacksTarget(sent) {
			continue
		}
		for _, m := range assistedRe.FindAllStringIndex(sent, -1) {
			if governed(sent, m[0], m[1], true) {
				return true
			}
		}
		coOnly := coAuthorRe.MatchString(sent) && !otherTrailerRe.MatchString(sent) && !widenPastRe.MatchString(sent)
		for _, m := range attributeToAIRe.FindAllStringIndex(sent, -1) {
			if !coAuthorObject(sent, m[0], coOnly) && governed(sent, m[0], m[1], false) {
				return true
			}
		}
		if commitRe.MatchString(sent) {
			for _, m := range namesAIRe.FindAllStringIndex(sent, -1) {
				if !coAuthorObject(sent, m[0], coOnly) && governed(sent, m[0], m[1], false) {
					return true
				}
			}
		}
		for _, m := range aiPhraseRe.FindAllStringIndex(sent, -1) {
			if !coAuthorObject(sent, m[0], coOnly) && governed(sent, m[0], m[1], true) {
				return true
			}
		}
	}
	return false
}

// lacksTarget reports whether s has a "no" right before an AI or the
// Assisted-by trailer (noTargetRe) that does not open the statement
// (openerRe). Like "without", such a "no" describes the commits a
// requirement is about, "commits that carry no Assisted-by trailer will not
// be merged", so a prohibition in the same sentence does not ban the trailer.
func lacksTarget(s string) bool {
	for _, m := range noTargetRe.FindAllStringIndex(s, -1) {
		if !openerRe.MatchString(s[:m[0]]) {
			return true
		}
	}
	return false
}

// coAuthorObject reports whether the target at s[start:], one that names no
// trailer itself, is read as part of a ban on Co-authored-by and so does not
// count. That holds for every such target when coOnly is set, that is when
// Co-authored-by or a session-link key is the only trailer sentence s names
// and no widenPastRe word follows it, whatever else the sentence forbids: "No
// Claude attribution: no Co-authored-by: Claude", but also "never mention
// Claude in commit messages, and never add Co-authored-by". It holds too when
// Co-authored-by stands between the last prohibition before the target and
// the target ("do not add a Co-authored-by trailer naming an AI"). A widening
// word right before the target ("or other AI trailers") carries the ban past
// Co-authored-by, so the target counts.
func coAuthorObject(s string, start int, coOnly bool) bool {
	if widenRe.MatchString(s[:start]) {
		return false
	}
	if coOnly {
		return true
	}
	from := 0
	if cues := cueRe.FindAllStringIndex(s[:start], -1); len(cues) > 0 {
		from = cues[len(cues)-1][1]
	}
	return coAuthorRe.MatchString(s[from:start])
}

// governed reports whether a prohibition cue in s governs the target that
// spans s[start:end]: a cue before it with no clause boundary between, or,
// when passive is set, a passive prohibition right after it. Nothing governs
// a target the rule only fills or places (insideRe, placed). A cue hedgeRe
// turns into something else, one inside a condition or a purpose
// (inCondition), and one whose rule is scoped to commits made without an AI
// (scoped) govern nothing; so does a passive in either.
func governed(s string, start, end int, passive bool) bool {
	if insideRe.MatchString(s[:start]) || placed(s[end:]) {
		return false
	}
	for _, c := range cueRe.FindAllStringIndex(s, -1) {
		if c[0] >= start || hedgeRe.MatchString(s[c[1]:]) || inCondition(s, c[0]) || scoped(s, c[0]) {
			continue
		}
		if c[1] >= start || !crossesBoundary(s[c[1]:start]) {
			return true
		}
	}
	return passive && !inCondition(s, start) && !scoped(s, start) && passiveRe.MatchString(s[end:])
}

// placed reports whether after, the text that follows a target, places the
// target rather than banning it (placeRe): "below Signed-off-by", "in the
// subject line", but not "in the subject or body".
func placed(after string) bool {
	m := placeRe.FindStringSubmatchIndex(after)
	return m != nil && m[2] < 0
}

// crossesBoundary reports whether between, the text from a prohibition to its
// target, holds a clause boundary (boundaryRe) or a comma that opens the next
// instruction rather than one more verb of a list (nextVerbRe).
func crossesBoundary(between string) bool {
	if boundaryRe.MatchString(between) {
		return true
	}
	for _, m := range nextVerbRe.FindAllStringSubmatchIndex(between, -1) {
		if m[2] < 0 {
			return true
		}
	}
	return false
}

// inCondition reports whether s[pos:] starts inside an if, when, or where
// clause, or right after the "to" of an opening to-infinitive (purposeRe),
// that a later comma closes.
// A prohibition there is part of the condition or the purpose, whether the
// target sits inside it or after it.
func inCondition(s string, pos int) bool {
	clause := s[strings.LastIndex(s[:pos], ",")+1 : pos]
	return (condRe.MatchString(clause) || purposeRe.MatchString(clause)) && strings.Contains(s[pos:], ",")
}

// scoped reports whether what comes before s[pos:] limits the rule there to
// commits made without an AI: a subject such as "human-only commits"
// (scopeRe), or a condition closed by a comma before pos that holds a
// negation (negRe), "if no AI tool was involved, do not add ...". A rule
// scoped like that says when the trailer is left off, not that it is banned.
func scoped(s string, pos int) bool {
	if scopeRe.MatchString(s[:pos]) {
		return true
	}
	p := strings.LastIndex(s[:pos], ",")
	if p < 0 {
		return false
	}
	loc := condRe.FindStringIndex(s[:p])
	if loc == nil {
		return false
	}
	cond := s[loc[0]:p]
	if i := strings.IndexByte(cond, ','); i >= 0 {
		cond = cond[:i]
	}
	return negRe.MatchString(cond)
}
