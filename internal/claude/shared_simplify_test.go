package claude

import (
	"strings"
	"testing"

	"github.com/planwerk/planwerk-agent/internal/diffscope"
	"github.com/planwerk/planwerk-agent/internal/implement"
)

// The implement skill runs the command's simplify pass in its own session
// (decision 177). The plugin cannot read the binary's prompts, so the skill
// carries its own copy of the decision ladder, the four kinds of finding, the
// guardrail, and the bound on what an applied finding may edit; the test below
// fails when that copy drifts from what the simplify-find and simplify-apply
// prompts render, the way shared_review_test.go pins the review skill.

// simplifyFindMarkers are the phrases the simplify-find prompt and the skill
// must both carry: one per rung of the ladder, one per kind of finding, and
// one per guardrail area. Each is kept short so a rewording elsewhere in
// either text passes while a dropped rung, kind, or area fails.
var simplifyFindMarkers = []string{
	// The decision ladder, top to bottom.
	"Do not build it at all (YAGNI)",
	"The standard library",
	"A platform- or framework-native feature",
	"A dependency already present in the project",
	"A one-liner",
	"the minimum new code",
	// The four kinds of finding.
	"Speculative abstractions",
	"Reinvented wheels",
	"Dead or unreachable complexity",
	"Over-built control flow",
	// The guardrail.
	"removes accidental complexity, never essential behavior",
	"Validation of inputs or arguments",
	"Error handling, error wrapping, or error propagation",
	"Security controls (authn/authz, input sanitization, crypto, secret handling)",
	"Accessibility code",
	"Tests, assertions, or required checks",
}

// simplifyApplyMarkers are the phrases the simplify-apply prompt and the skill
// must both carry: what an applied finding may edit, and what it may not
// change.
var simplifyApplyMarkers = []string{
	"the call sites and imports its collapse requires",
	"changes no observable behavior",
	"removes accidental complexity, never essential behavior",
	"Tests, assertions, or required checks",
}

// TestImplementSkillMatchesTheSimplifyPrompts fails when the simplify prompts
// or the implement skill stop carrying a marker of the simplify pass.
func TestImplementSkillMatchesTheSimplifyPrompts(t *testing.T) {
	skill := flow(readSharedDoc(t, implementSkillDoc))
	for _, tc := range []struct {
		name    string
		prompt  string
		markers []string
	}{
		{"the simplify-find prompt", buildSimplifyFindPrompt(DefaultBaseBranch, diffscope.Diff{}), simplifyFindMarkers},
		{"the simplify-apply prompt", BuildSimplifyApplyPrompt(implement.SimplifyApplyContext{BaseBranch: DefaultBaseBranch}), simplifyApplyMarkers},
	} {
		prompt := flow(tc.prompt)
		for _, marker := range tc.markers {
			m := flow(marker)
			if !strings.Contains(prompt, m) {
				t.Errorf("%s no longer carries %q; change the marker and the implement skill together", tc.name, marker)
			}
			if !strings.Contains(skill, m) {
				t.Errorf("%s does not carry %q; the skill's simplify pass drifted from %s", implementSkillDoc, marker, tc.name)
			}
		}
	}
}
