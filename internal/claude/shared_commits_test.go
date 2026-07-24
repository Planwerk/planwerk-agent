package claude

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/planwerk/planwerk-agent/internal/fix"
)

// sharedCommitsDoc is the commit doctrine the `fix` skill reads. The skill and
// the fix prompts drive the same git operations from two sources — a SKILL.md
// the Claude Code plugin ships, and the Go builders in this package — so the two
// are free to drift. They may not. A commit whose trailers are ordered wrongly,
// or a force-push that is not leased, is the same defect whichever session made
// it. This is the elaborate/issue-format.md arrangement applied to commits:
// duplication bounded mechanically rather than by discipline.
const sharedCommitsDoc = "../../plugins/planwerk/shared/commits.md"

// TestSharedCommitsDocMatchesTrailerBlock fails when the shared commit doctrine
// and commitTrailerBlock() stop agreeing on the trailer convention. Each marker
// is a distinct rule, so a failure names the rule that went missing rather than
// reporting that two blobs differ.
func TestSharedCommitsDocMatchesTrailerBlock(t *testing.T) {
	doc := readSharedCommitsDoc(t)
	block := commitTrailerBlock()

	for _, tc := range []struct {
		rule   string
		marker string
	}{
		{"the Assisted-by trailer names Claude", "Assisted-by: Claude"},
		{"the model id is appended when known", "claude-opus-5"},
		{"git commit -s supplies the sign-off", "Signed-off-by"},
		{"the sign-off comes from `-s`", "`-s`"},
		{"--trailer is rejected because it lands after the sign-off", "--trailer"},
		{"Co-authored-by is banned", "Co-authored-by"},
	} {
		if !strings.Contains(doc, tc.marker) {
			t.Errorf("%s: %s does not mention %q", tc.rule, sharedCommitsDoc, tc.marker)
		}
		if !strings.Contains(block, tc.marker) {
			t.Errorf("%s: commitTrailerBlock() does not mention %q", tc.rule, tc.marker)
		}
	}
}

// TestSharedCommitsDocMatchesFoldDiscipline fails when the shared doctrine and
// the bare fix prompt stop agreeing on how a fix is folded into the commit that
// caused it and published. The bare prompt is the one a human pastes into a
// manual session, which is the closest thing the Go side has to the skill.
func TestSharedCommitsDocMatchesFoldDiscipline(t *testing.T) {
	doc := readSharedCommitsDoc(t)
	prompt := BuildBareFixPrompt(fix.BareContext{RepoFullName: "o/r", PRNumber: 1, Fixup: true})

	for _, tc := range []struct {
		rule   string
		marker string
	}{
		{"a change is recorded as a fixup of its target commit", "git commit --fixup="},
		{"the fold runs without opening an editor", "GIT_SEQUENCE_EDITOR=true"},
		{"the rebase is bounded by the merge-base", "git merge-base origin/"},
		{"the fold is published with a lease", "--force-with-lease"},
		{"plain --force is forbidden", "plain --force"},
		{"the target commit is found by blame or pickaxe", "git log -S<symbol>"},
	} {
		if !strings.Contains(prose(doc), prose(tc.marker)) {
			t.Errorf("%s: %s does not mention %q", tc.rule, sharedCommitsDoc, tc.marker)
		}
		if !strings.Contains(prose(prompt), prose(tc.marker)) {
			t.Errorf("%s: BuildBareFixPrompt does not mention %q", tc.rule, tc.marker)
		}
	}
}

// prose normalizes away the two things the shared markdown and the Go prompt
// disagree on for reasons that carry no meaning: the backticks markdown puts
// around a flag, and whether a hard rule is shouted. A git incantation is the
// same incantation either way.
func prose(s string) string {
	return strings.ToLower(strings.ReplaceAll(s, "`", ""))
}

func readSharedCommitsDoc(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Clean(sharedCommitsDoc))
	if err != nil {
		t.Fatalf("reading %s: %v", sharedCommitsDoc, err)
	}
	return string(raw)
}
