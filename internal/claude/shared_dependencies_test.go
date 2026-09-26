package claude

import (
	"strings"
	"testing"

	"github.com/planwerk/planwerk-agent/internal/github"
)

// The delivery-order rule lives on three surfaces: the Sub Issue bullet
// relations.go renders for a headless elaborate or plan session, and the
// elaborate and implement skills, which read the same edges from the shared
// neighborhood query. A rule that holds on one surface and not another plans
// a Sub Issue against a sibling that has not delivered yet, so the load-bearing
// phrases are pinned across all three, the way shared_crossrepo_test.go pins
// the cross-repository reference rule.
const implementSkillDoc = "../../plugins/planwerk/skills/implement/SKILL.md"

// deliveryOrderMarkers are the phrases every surface must carry. Kept short so
// an unrelated rewording does not fail the test while a dropped rule does.
var deliveryOrderMarkers = []string{
	"delivers first",
	"delivers later",
	"off-limits",
	"landed out of order",
	"never infer that order from issue prose",
}

// TestDeliveryOrderRuleMatchesAcrossSurfaces fails when the rendered Sub Issue
// bullet or either skill stops carrying a marker of the delivery-order rule.
func TestDeliveryOrderRuleMatchesAcrossSurfaces(t *testing.T) {
	var sb strings.Builder
	meta := &github.Issue{Number: 40, Title: "Meta", State: "open"}
	siblings := []github.Issue{{Number: 43, Title: "Sibling", State: "open",
		BlockedBy: []github.Issue{{Number: 42, State: "open"}}}}
	renderIssueRelations(&sb, testRepoFullName, 42, meta, siblings, nil)

	surfaces := []struct{ name, text string }{
		{"the relations prompt block", sb.String()},
		{elaborateSkillDoc, readSharedDoc(t, elaborateSkillDoc)},
		{implementSkillDoc, readSharedDoc(t, implementSkillDoc)},
	}
	for _, s := range surfaces {
		text := flow(s.text)
		for _, marker := range deliveryOrderMarkers {
			if !strings.Contains(text, flow(marker)) {
				t.Errorf("%s does not carry %q; the delivery-order rule has drifted", s.name, marker)
			}
		}
	}
}
