package report

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"maps"
	"sort"
	"strings"
)

// ParseDataBlock extracts the machine-readable planwerk-agent data block from
// a prior review comment body (the one RenderDataBlock emitted) and returns the
// commit SHA it was posted at plus the findings it carried. ok is false when no
// well-formed data block is present.
//
// It reads only the block RenderDataBlock appended: the body's last marker,
// followed by nothing but whitespace and postSignatures. The body also quotes
// text the PR's author controls, its title and the findings' text, so a block
// forged there must never be the one read (decision 128). json.Marshal escapes
// < and >, so the appended block's JSON holds neither the marker nor "-->". A
// body cut before its own block, as truncateComment cuts one, yields none. The
// caller still checks commitSHA before handing it to git.
func ParseDataBlock(commentBody string) (commitSHA string, findings []Finding, ok bool) {
	const open = "<!-- planwerk-agent-data"
	const closeMarker = "-->"
	start := strings.LastIndex(commentBody, open)
	if start < 0 {
		return "", nil, false
	}
	rest := commentBody[start+len(open):]
	end := strings.Index(rest, closeMarker)
	if end < 0 || strings.TrimSpace(postSignatures.Replace(rest[end+len(closeMarker):])) != "" {
		return "", nil, false
	}
	var payload dataBlockPayload
	if err := json.Unmarshal([]byte(strings.TrimSpace(rest[:end])), &payload); err != nil {
		return "", nil, false
	}
	return payload.CommitSHA, payload.Findings, true
}

// postSignatures strips the signatures PostPRComment and SubmitPRReview append
// after a posted body's data block: github's commentSignature and
// reviewSignature. ParseDataBlock accepts a block that only they follow.
var postSignatures = strings.NewReplacer("<!-- planwerk-agent -->", "", "<!-- planwerk-agent-inline -->", "")

// SuppressionKey identifies a finding across reviews by what it quotes: its
// file, lowercased, and its code_snippet with every whitespace run collapsed to
// one space, so a re-indented or rewrapped quote still matches. The line is not
// part of it, so a finding that moved down the file matches, and neither is the
// title, so a reworded finding matches. A finding without a snippet falls back
// to its file and lowercased title. The "|code|" and "|title|" separators keep
// a title from ever matching a snippet. A change to the key changes every
// FindingFingerprint, so the inline comments posted before it dismiss nothing
// (decision 132).
func SuppressionKey(f Finding) string {
	file := strings.ToLower(strings.TrimSpace(f.File))
	if s := normalizedSnippet(f.CodeSnippet); s != "" {
		return file + "|code|" + s
	}
	return file + "|title|" + strings.ToLower(strings.TrimSpace(f.Title))
}

// normalizedSnippet collapses every whitespace run in s to one space and trims
// both ends, so re-indentation and line rewrapping do not change the text a
// finding is identified by. It is empty for a snippet of whitespace only.
func normalizedSnippet(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// FindingFingerprint is the first 16 lowercase hex digits of the SHA-256 of
// SuppressionKey(f). FormatInlineComment writes it into the finding marker, and
// a re-review reads it back with ParseFindingMarker to trace a resolved review
// thread to the finding it was posted for (decision 132).
func FindingFingerprint(f Finding) string {
	sum := sha256.Sum256([]byte(SuppressionKey(f)))
	return hex.EncodeToString(sum[:8])
}

// findingMarkerPrefix and findingMarkerSuffix enclose a FindingFingerprint in
// the hidden HTML comment an inline comment ends with.
const (
	findingMarkerPrefix = "<!-- planwerk-agent-finding:"
	findingMarkerSuffix = " -->"
)

// findingMarker is the hidden marker FormatInlineComment appends to f's inline
// comment.
func findingMarker(f Finding) string {
	return findingMarkerPrefix + FindingFingerprint(f) + findingMarkerSuffix
}

// ParseFindingMarker returns the fingerprint in an inline comment body's finding
// marker. It reads the body's last marker, the one FormatInlineComment appends
// after all of the finding's text, so a marker quoted in a title or a problem
// statement is never the one read. ok is false when the body carries no marker
// or the last one does not hold exactly 16 lowercase hex digits followed by
// " -->".
func ParseFindingMarker(body string) (fingerprint string, ok bool) {
	start := strings.LastIndex(body, findingMarkerPrefix)
	if start < 0 {
		return "", false
	}
	rest := body[start+len(findingMarkerPrefix):]
	const n = 16 // hex digits in a FindingFingerprint
	if len(rest) < n || !strings.HasPrefix(rest[n:], findingMarkerSuffix) {
		return "", false
	}
	fingerprint = rest[:n]
	if _, err := hex.DecodeString(fingerprint); err != nil || strings.ToLower(fingerprint) != fingerprint {
		return "", false
	}
	return fingerprint, true
}

// suppressMatches hides each current finding whose key still has budget left,
// one finding per unit, and returns the rest as kept; both keep the input
// order. A finding without a file is never hidden, and eligible is asked only
// about a finding whose key has budget left; one it refuses is kept and spends
// nothing. The budget is spent on the least severe findings first, so when a
// key matches more current findings than it has budget, the most severe stay
// visible; an unknown severity counts as the most severe. budget is
// decremented in place.
func suppressMatches(current []Finding, budget map[string]int, keyOf func(Finding) string, eligible func(Finding) bool) (kept, suppressed []Finding) {
	order := make([]int, len(current))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		return severityOrder[current[order[a]].Severity] > severityOrder[current[order[b]].Severity]
	})
	hidden := make([]bool, len(current))
	for _, i := range order {
		f := current[i]
		if f.File == "" {
			continue
		}
		key := keyOf(f)
		if budget[key] <= 0 || !eligible(f) {
			continue
		}
		budget[key]--
		hidden[i] = true
	}
	for i, f := range current {
		if hidden[i] {
			suppressed = append(suppressed, f)
			continue
		}
		kept = append(kept, f)
	}
	return kept, suppressed
}

// FilterPreviouslyReported suppresses current findings the user already saw and
// did not act on. A current finding is suppressed when the prior review carried
// one with the same SuppressionKey (the same quoted code on the same file or,
// without a snippet, the same title) AND its file is unchanged since that
// review (isUnchanged(file) is true). Matching is one for one: each prior
// finding suppresses at most one current finding, the least severe first.
// Only a finding listed(f) admits is suppressed and spends a prior finding.
// listed should admit what the report would show, so that a repeat below the
// report's thresholds, which no section shows anyway, never uses up the prior
// finding and lets the listed repeat come back (decision 132).
//
// This needs no explicit "skipped" disposition: a fixed finding does not
// reappear in the current set, so it cannot be suppressed; a finding that
// reappears was not fixed. If its file changed (a possible regression), it is
// kept so the user sees it again. The full current set is preserved by the
// caller for the data block — only the rendered sections drop the suppressed
// ones.
func FilterPreviouslyReported(current, prior []Finding, listed func(Finding) bool, isUnchanged func(file string) bool) (kept, suppressed []Finding) {
	budget := make(map[string]int, len(prior))
	for _, p := range prior {
		if p.File != "" {
			budget[SuppressionKey(p)]++
		}
	}
	return suppressMatches(current, budget, SuppressionKey, func(f Finding) bool {
		return listed(f) && isUnchanged(f.File)
	})
}

// FilterDismissed suppresses current findings someone dismissed by resolving
// the inline thread posted for them. dismissed counts, per FindingFingerprint,
// the earlier findings a resolved thread dismissed, and each one suppresses at
// most one current finding with that fingerprint, the least severe first. A finding qualifies only when
// listed(f) admits it, as for FilterPreviouslyReported; when it quotes code;
// and when stillQuoted(f) reports that code still in its file, so a dismissal
// never outlives the code the person looked at. Unlike
// FilterPreviouslyReported, the rest of the file may have changed. stillQuoted
// is not called for a finding listed refuses or one without a snippet, which
// never qualify. dismissed is not modified (decision 132).
func FilterDismissed(current []Finding, dismissed map[string]int, listed, stillQuoted func(Finding) bool) (kept, suppressed []Finding) {
	return suppressMatches(current, maps.Clone(dismissed), FindingFingerprint, func(f Finding) bool {
		return listed(f) && strings.TrimSpace(f.CodeSnippet) != "" && stillQuoted(f)
	})
}
