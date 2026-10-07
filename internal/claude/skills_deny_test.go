package claude

import (
	"slices"
	"testing"

	"github.com/planwerk/planwerk-agent/internal/fix"
)

// TestWithDenyRules_ContinueOrOpenTheDisallowedList locks the one flag the
// deny rules ride on: after the read-only list they continue it, for a
// mutating session they open it, and --allowed-tools terminates it either way.
func TestWithDenyRules_ContinueOrOpenTheDisallowedList(t *testing.T) {
	c := NewClient()
	for _, readOnly := range []bool{false, true} {
		args := c.claudeArgs(runSpec{dir: t.TempDir(), model: "opus", effort: "xhigh", readOnly: readOnly, denyRules: []string{"Skill(skill:deploy)"}}, "json")
		deny := slices.Index(args, "--disallowed-tools")
		rule := slices.Index(args, "Skill(skill:deploy)")
		allow := slices.Index(args, "--allowed-tools")
		if deny < 0 || rule < deny || allow < rule {
			t.Errorf("readOnly=%v: want --disallowed-tools … Skill(skill:deploy) … --allowed-tools, got %v", readOnly, args)
		}
		if n := slices.Index(args[deny+1:], "--disallowed-tools"); n >= 0 {
			t.Errorf("readOnly=%v: --disallowed-tools emitted twice: %v", readOnly, args)
		}
	}
	if args := c.claudeArgs(runSpec{dir: t.TempDir(), model: "opus", effort: "xhigh"}, "json"); slices.Contains(args, "--disallowed-tools") {
		t.Errorf("a mutating session without deny rules emitted the flag: %v", args)
	}
}

func TestSkillDenyRules(t *testing.T) {
	if got := skillDenyRules(nil); got != nil {
		t.Errorf("skillDenyRules(nil) = %v, want nil", got)
	}
	if got, want := skillDenyRules([]string{"deploy", "deploy-alias"}), []string{"Skill(skill:deploy)", "Skill(skill:deploy-alias)"}; !slices.Equal(got, want) {
		t.Errorf("skillDenyRules = %v, want %v", got, want)
	}
}

// TestFix_DeniesTheSkillsThePullRequestChanges locks the wiring: the skills
// the context names as changed reach the session as Skill deny rules.
func TestFix_DeniesTheSkillsThePullRequestChanges(t *testing.T) {
	c, calls := scriptedClient(t, func(int, runSpec, string) (string, string, error) {
		return fixReportHeading + "\n\n### Status\nSTATUS: DONE\n", testResolvedModel, nil
	})
	if _, _, err := c.Fix(t.TempDir(), fix.Context{RepoFullName: "o/r", PRNumber: 1, ChangedSkills: []string{"deploy"}}); err != nil {
		t.Fatalf("Fix: %v", err)
	}
	if len(*calls) == 0 {
		t.Fatal("no session ran")
	}
	if got, want := (*calls)[0].spec.denyRules, []string{"Skill(skill:deploy)"}; !slices.Equal(got, want) {
		t.Errorf("denyRules = %v, want %v", got, want)
	}
}
