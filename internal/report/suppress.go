package report

import (
	"encoding/json"
	"strconv"
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

// findingKey identifies a finding by location and title for cross-review
// matching, normalized so trivial title/case differences still match.
func findingKey(f Finding) string {
	return strings.ToLower(strings.TrimSpace(f.File)) + "|" +
		strconv.Itoa(f.Line) + "|" +
		strings.ToLower(strings.TrimSpace(f.Title))
}

// FilterPreviouslyReported suppresses current findings the user already saw and
// did not act on. A current finding is suppressed when an equivalent finding
// (same file+line+title) appeared in the prior review AND its file is unchanged
// since that review (isUnchanged(file) is true).
//
// This needs no explicit "skipped" disposition: a fixed finding does not
// reappear in the current set, so it cannot be suppressed; a finding that
// reappears was not fixed. If its file changed (a possible regression), it is
// kept so the user sees it again. The full current set is preserved by the
// caller for the data block — only the rendered sections drop the suppressed
// ones.
func FilterPreviouslyReported(current, prior []Finding, isUnchanged func(file string) bool) (kept, suppressed []Finding) {
	priorKeys := make(map[string]bool, len(prior))
	for _, p := range prior {
		if p.File != "" {
			priorKeys[findingKey(p)] = true
		}
	}
	for _, f := range current {
		if f.File != "" && priorKeys[findingKey(f)] && isUnchanged(f.File) {
			suppressed = append(suppressed, f)
			continue
		}
		kept = append(kept, f)
	}
	return kept, suppressed
}
