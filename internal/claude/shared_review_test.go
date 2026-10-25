package claude

import (
	"regexp"
	"strings"
	"testing"

	"github.com/planwerk/planwerk-agent/internal/checklist"
)

// reviewSkillDoc is the /planwerk:review skill, which reviews a branch in the
// author's checkout before its pull request exists. The plugin cannot read
// the binary's embedded files, so the skill carries its own copy of the review
// prompt's severity ladder, its diff-scope suppressions, and the default
// checklist's classes; the tests below fail when that copy drifts from the
// blocks the review command renders (decision 151).
const reviewSkillDoc = "../../plugins/planwerk/skills/review/SKILL.md"

// reviewParityMarkers are the phrases of the ladder and the suppressions both
// the review prompt and the skill must carry: at least one per rung of the
// ladder and one per suppression, so a dropped rung or suppression fails.
// Each is kept short so a rewording elsewhere in either text passes.
var reviewParityMarkers = []string{
	// The ladder, BLOCKING to INFO.
	"an exploitable security vulnerability, data loss or corruption",
	"a design flaw that needs the change reworked rather than patched",
	"must not be merged",
	"a defect that breaks correct behavior on a normal path",
	"a broken contract with a caller",
	"must be fixed before merge",
	"a defect on an edge or failure path",
	"erodes reliability over time",
	"style suggestions and minor improvements",
	// The diff-scope suppressions, in the prompt's order.
	"an issue tracker",
	"trivial getters/setters",
	"import order",
	"naming that follows the project's",
	"missing documentation on",
	"minor style preference",
	"aids readability",
	"rot faster than",
	"could be tighter",
	"consistency-only",
	"already addressed elsewhere in the same diff",
	"the error path already returns a descriptive error",
	"using X library",
	"pre-existing problems in code that was not changed",
	"breakage the change causes in unchanged code",
}

// TestReviewSkillMatchesTheReviewPrompt fails when the review prompt's ladder
// and suppressions, or the review skill, stop carrying a marker. It reads the
// two blocks rather than the whole rendered prompt, so the labels and the
// verification text around them can change without failing it.
func TestReviewSkillMatchesTheReviewPrompt(t *testing.T) {
	prompt := flow(severityLadderBlock(scopeDiff) + suppressionsBlock(scopeDiff))
	skill := flow(readSharedDoc(t, reviewSkillDoc))
	for _, marker := range reviewParityMarkers {
		m := flow(marker)
		if !strings.Contains(prompt, m) {
			t.Errorf("the review prompt's ladder and suppressions no longer carry %q; change the marker and the review skill together", marker)
		}
		if !strings.Contains(skill, m) {
			t.Errorf("%s does not carry %q; the skill's ladder or suppressions drifted from the review prompt", reviewSkillDoc, marker)
		}
	}
}

// checklistClass matches one class line of the default checklist,
// "- [ ] <class>: <what to check>", and captures the class name.
var checklistClass = regexp.MustCompile(`(?m)^- \[ \] ([^:]+):`)

// TestReviewSkillNamesTheDefaultChecklist fails when a class of the embedded
// default checklist is missing from the review skill, which names the classes
// a review works through when the repository commits no checklist of its own.
func TestReviewSkillNamesTheDefaultChecklist(t *testing.T) {
	classes := checklistClass.FindAllStringSubmatch(checklist.Default(), -1)
	if len(classes) == 0 {
		t.Fatal("the default checklist has no \"- [ ] <class>:\" lines; its format changed, so change checklistClass with it")
	}
	skill := flow(readSharedDoc(t, reviewSkillDoc))
	for _, class := range classes {
		if !strings.Contains(skill, flow(class[1])) {
			t.Errorf("%s does not name the default checklist class %q; add it to the skill's checklist classes", reviewSkillDoc, class[1])
		}
	}
}
