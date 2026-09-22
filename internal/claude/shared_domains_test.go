package claude

import (
	"strings"
	"testing"

	"github.com/planwerk/planwerk-agent/internal/domains"
	"github.com/planwerk/planwerk-agent/internal/elaborate"
)

// The domain sweep (decision 86) lives on two surfaces: the list the planning
// prompts embed through domainSweepBlock (internal/domains), and
// shared/domains.md, the copy the elaborate skill sweeps when a repository
// commits no .planwerk/domains.md of its own. A domain one surface sweeps and
// the other does not holds the command and the skill to two different bars for
// the same plan, so the copy is pinned to the embedded list the way commits.md
// is pinned to commitTrailerBlock.
const (
	sharedDomainsDoc  = "../../plugins/planwerk/shared/domains.md"
	elaborateSkillDoc = "../../plugins/planwerk/skills/elaborate/SKILL.md"
)

// TestSharedDomainsDocMatchesDefault fails when shared/domains.md stops
// carrying a line of domains.Default(), or lists a domain the default does not.
// Lines are matched with whitespace collapsed, since the shared docs wrap at 80
// columns and the embedded list does not; each line is checked on its own so a
// failure names the domain that drifted.
func TestSharedDomainsDocMatchesDefault(t *testing.T) {
	doc := readSharedDoc(t, sharedDomainsDoc)
	def := domains.Default()

	for _, line := range strings.Split(def, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if !strings.Contains(flow(doc), flow(line)) {
			t.Errorf("%s does not carry this line of domains.Default(): %q", sharedDomainsDoc, line)
		}
	}

	if got, want := strings.Count(doc, "\n- **"), strings.Count(def, "\n- **"); got != want {
		t.Errorf("%s lists %d domains, domains.Default() lists %d", sharedDomainsDoc, got, want)
	}
}

// TestElaborateSkillMatchesDomainLanding fails when the elaborate skill and the
// elaborate prompt stop agreeing on where a swept domain lands. Only the list is
// shared through domainSweepBlock; each caller passes its own landing sentence,
// so the skill's copy of the elaboration's landing is pinned here: a touched
// domain becomes a criterion or an Affected Areas entry, never a section the
// house format has no room for.
func TestElaborateSkillMatchesDomainLanding(t *testing.T) {
	surfaces := map[string]string{
		elaborateSkillDoc:        readSharedDoc(t, elaborateSkillDoc),
		"buildElaboratePrompt()": buildElaboratePrompt(elaborate.Context{}),
	}

	for _, tc := range []struct {
		rule   string
		marker string
	}{
		{"a touched domain lands as a criterion or an affected area", "A domain the issue touches must surface in the issue itself: as an Acceptance Criterion when it adds an observable check, and as an Affected Areas entry when it adds a file to touch."},
		{"an untouched domain costs nothing", "A domain the issue does not touch needs nothing at all."},
		{"the sweep is never reported as a section", "the sweep is how you arrive at the criteria, never something the issue reports"},
		{"sweeping never widens scope", "belongs under Non-Goals with its one sentence of why"},
	} {
		for name, text := range surfaces {
			if !strings.Contains(flow(text), flow(tc.marker)) {
				t.Errorf("%s: %s does not mention %q", tc.rule, name, tc.marker)
			}
		}
	}
}
