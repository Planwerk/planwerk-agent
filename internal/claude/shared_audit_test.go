package claude

import (
	"strings"
	"testing"

	"github.com/planwerk/planwerk-agent/internal/audit"
)

// auditSkillDoc is the audit skill. It restates the audit command's severity
// ladder and suppressions in its own words, as /planwerk:review restates the
// review command's, and the how-to page promises "the same ladder the command
// uses". The tests below fail when the two drift, which they had: the skill
// left three documentation findings without a rung that the command rates
// WARNING (decision 121; the review skill's pair is decision 151).
const auditSkillDoc = "../../plugins/planwerk/skills/audit/SKILL.md"

// auditParityMarkers are the phrases both the command's codebase ladder and
// suppressions and the skill must carry: at least one per rung and one per
// suppression the skill restates. Each is short, so a rewording elsewhere in
// either text passes.
var auditParityMarkers = []string{
	// The ladder, BLOCKING to INFO. The skill says "the code" where the
	// command says "the change", so the BLOCKING marker stops before it.
	"an exploitable security vulnerability, data loss or corruption",
	"a design flaw that needs the",
	"a defect that breaks correct behavior on a normal path",
	"a broken contract with a caller",
	"a defect on an edge or failure path",
	"erodes reliability over time",
	"minor improvements",
	// The codebase suppressions, in the prompt's order.
	"that reference",
	"trivial getter",
	"import order",
	"naming that follows the project's",
	"missing documentation on",
	"minor style preference",
	"aids readability",
	"rot faster than",
	"could be tighter",
	"consistency-only",
	"add logging",
}

// TestAuditSkillMatchesTheAuditPrompt fails when the audit prompt's ladder and
// suppressions, or the audit skill, stop carrying a marker.
func TestAuditSkillMatchesTheAuditPrompt(t *testing.T) {
	prompt := flow(severityLadderBlock(scopeCodebase) + suppressionsBlock(scopeCodebase))
	skill := flow(readSharedDoc(t, auditSkillDoc))
	for _, marker := range auditParityMarkers {
		m := flow(marker)
		if !strings.Contains(prompt, m) {
			t.Errorf("the audit prompt's ladder and suppressions no longer carry %q; change the marker and the audit skill together", marker)
		}
		if !strings.Contains(skill, m) {
			t.Errorf("%s does not carry %q; the skill's ladder or suppressions drifted from the audit prompt", auditSkillDoc, marker)
		}
	}
}

// TestAuditSkillRatesDocumentationAsTheCommandDoes pins the documentation
// findings, which the ladder does not name and each side rates in its own
// list. A class the skill leaves without a rung is INFO there, and the skill
// files no INFO finding as an issue, so a missing rung silently drops a
// finding the command reports.
func TestAuditSkillRatesDocumentationAsTheCommandDoes(t *testing.T) {
	prompt := flow(buildAuditPrompt(audit.AuditContext{}))
	skill := flow(readSharedDoc(t, auditSkillDoc))
	for _, tc := range []struct {
		class, inPrompt, inSkill string
	}{
		{"a page whose mode drifts from its content", `as WARNING titled "Diátaxis Drift`, "drifts from its content — WARNING"},
		{"an undocumented public API, flag, or config key", "Flag undocumented items as WARNING", "with no deprecation note — WARNING"},
		{"a removed or renamed one with no deprecation note", `as WARNING titled "Missing Deprecation Notice`, "with no deprecation note — WARNING"},
		{"one concept under two names", `as INFO titled "Terminology Drift`, "or a dead link — INFO"},
	} {
		if !strings.Contains(prompt, flow(tc.inPrompt)) {
			t.Errorf("%s: the audit prompt no longer says %q; change the marker and the audit skill together", tc.class, tc.inPrompt)
		}
		if !strings.Contains(skill, flow(tc.inSkill)) {
			t.Errorf("%s: %s does not say %q; the skill rates it differently from the audit prompt", tc.class, auditSkillDoc, tc.inSkill)
		}
	}
}
