package elaborate

import "strings"

// categoryHeaderPrefix opens the house issue format's header line,
// "**Category**: feature | **Scope**: Medium".
const categoryHeaderPrefix = "**Category**"

// categoryHeader returns the source issue's Category/Scope header line, or ""
// when the issue does not open with one. An elaboration replaces the whole issue
// body, so without this the header a draft-depth issue opened with would be
// silently dropped on the first `elaborate --update-issue`.
//
// Only the first non-empty line is considered: the header is defined to be the
// issue's opening line, and scanning further would match a "**Category**" that a
// Description merely mentions. An issue with no body, or one whose first line is
// something else, yields "" and renders exactly as it did before the header was
// carried through.
func categoryHeader(issueBody string) string {
	for _, line := range strings.Split(issueBody, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, categoryHeaderPrefix) {
			return line
		}
		return ""
	}
	return ""
}
