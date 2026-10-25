package aitrailers

import (
	"testing"

	"github.com/planwerk/planwerk-agent/internal/gitref/gitreftest"
)

// harmless is instruction-file text that forbids nothing about trailers.
const harmless = "# Agent guidelines\n\nRun the tests before you push.\n"

// TestForbiddenBy_ReadsTheBaseNotTheHead: a pull request cannot lift the ban
// its base carries by rewriting the file, nor impose one the base lacks.
func TestForbiddenBy_ReadsTheBaseNotTheHead(t *testing.T) {
	dir := gitreftest.Repo(t, map[string]string{"AGENTS.md": "## Rules\n\n" + ocrRule + "\n"})
	gitreftest.Write(t, dir, "AGENTS.md", harmless)
	if got := ForbiddenBy(dir, "main"); got != "AGENTS.md" {
		t.Errorf("ForbiddenBy with the rule on the base = %q, want %q", got, "AGENTS.md")
	}

	dir = gitreftest.Repo(t, map[string]string{"AGENTS.md": harmless})
	gitreftest.Write(t, dir, "AGENTS.md", "## Rules\n\n"+ocrRule+"\n")
	if got := ForbiddenBy(dir, "main"); got != "" {
		t.Errorf("ForbiddenBy with the rule only on the head = %q, want \"\"", got)
	}
}

func TestForbiddenBy_FirstFileInOrderWins(t *testing.T) {
	const rule = "Do not add an Assisted-by trailer to your commits.\n"
	dir := gitreftest.Repo(t, map[string]string{
		"AGENTS.md":       harmless,
		"CLAUDE.md":       rule,
		"CONTRIBUTING.md": rule,
	})
	if got := ForbiddenBy(dir, "main"); got != "CLAUDE.md" {
		t.Errorf("ForbiddenBy = %q, want the first forbidding file in order, %q", got, "CLAUDE.md")
	}

	dir = gitreftest.Repo(t, map[string]string{
		"CONTRIBUTING.md":         harmless,
		".github/CONTRIBUTING.md": rule,
	})
	if got := ForbiddenBy(dir, "main"); got != ".github/CONTRIBUTING.md" {
		t.Errorf("ForbiddenBy = %q, want %q", got, ".github/CONTRIBUTING.md")
	}
}

// TestForbiddenBy_RulesThatRequireTheTrailer: verbatim excerpts of instruction
// files that ask for Assisted-by and, in the same breath, forbid Co-authored-by
// for AI attribution, "Claude attribution" through Co-authored-by, a
// Claude-Session trailer, or a second, less specific Assisted-by. Read as a
// ban, each would strip the trailer the repository requires.
func TestForbiddenBy_RulesThatRequireTheTrailer(t *testing.T) {
	for _, tc := range []struct{ source, file, body string }{
		{"rossoctl/operator@3b92d6c CLAUDE.md:5-12", "CLAUDE.md", "## Commit Attribution Policy\n\nWhen creating git commits, do NOT use `Co-Authored-By` trailers for AI attribution.\nInstead, use `Assisted-By` to acknowledge AI assistance without inflating contributor stats:\n\n    Assisted-By: Claude (Anthropic AI) <noreply@anthropic.com>\n\nNever add `Co-authored-by`, `Made-with`, or similar trailers that GitHub parses as co-authorship.\n"},
		{"eclipse-threadx/threadx@56c8695 CONTRIBUTING.md:229-245", "CONTRIBUTING.md", "### Commit attribution\n\nAttribute AI assistance with an `Assisted-by` trailer in each commit that contains AI-assisted changes:\n\n```text\nFixed the thread resume status\n\nA suspended thread could report an out-of-date status after resuming.\n\nThe resume path now returns the status set by the operation that woke it.\n\nThe single-core regression suite passed all tests.\n\nAssisted-by: <product> (<model_and_version>) <email>\n```\n\nUse the product's accepted name (`Claude Code`, `Copilot`, `Codex`, or `Gemini`) and the model string reported by the tool. Do not use `Co-Authored-By` for AI attribution. Commit subjects and pull request titles start with a past-tense verb and stay within 72 characters. The message body states the cause, fix, and test result in that order; wrap commit bodies at 88 characters. Pull request descriptions use one line per paragraph.\n"},
		{"thewillyhuman/kconnectors@b5975d1 AGENTS.md:70-74", "AGENTS.md", "- Trailers, and nothing else:\n  - `Assisted-by:` the current AI model, when one assisted\n    (e.g. `Assisted-by: Claude Fable 5`).\n  - `Signed-off-by:` the human git committer.\n- Do not add `Co-Authored-By` or `Claude-Session` trailers.\n"},
		{"libusb/libusb@6a1aff0 AGENTS.md:24-39", "AGENTS.md", "At the end of every commit message, include an explicit attribution trailer indicating which\nAI model assisted, in this format:\n\n```\nAssisted-by: AGENT_NAME:MODEL_VERSION\n```\n\nFor example: `Assisted-by: claude-code:claude-opus-4-7` or\n`Assisted-by: github-copilot:MODEL_VERSION`.\n\nAlways use the most specific model variant available, not a shorter model-family name.\nWhen combining or preserving trailers, including during squash merges, deduplicate\nattributions for the same agent and keep only its most specific model variant. For\nexample, if `Assisted-by: Codex:GPT-5.6 Sol` applies, do not also add the less-specific\n`Assisted-by: Codex:GPT-5` trailer. Multiple `Assisted-by` trailers are appropriate for\ndifferent agents, but not merely for different specificity levels of the same agent.\n"},
		{"Mag-27/PX4-Autopilot_MorphingUAV@b15c780 CLAUDE.md:38-46", "CLAUDE.md", "- **No Claude attribution** — no `Co-Authored-By: Claude`, no \"Generated\n  with Claude Code\" footer. An AI tool is never an author/co-author and\n  never appears in `Signed-off-by`.\n- **AI disclosure is required**: every commit with AI-generated/AI-assisted\n  content must carry `Assisted-by: NAME:MODEL` in the commit body (e.g.\n  `Assisted-by: Claude:claude-fable-5`). This is separate from and\n  additional to the human author's `Signed-off-by` (DCO sign-off via\n  `git commit -s`) — sign-off must never be applied to changes the user\n  hasn't reviewed.\n"},
		{"vivliostyle/vivliostyle.org@83a2293 AGENTS.md:14-21", "AGENTS.md", "- Add an `Assisted-by:` trailer naming the agent and the exact model version to every\n  commit that contains AI-generated changes, in the Linux-kernel format\n  `Assisted-by: AGENT_NAME:MODEL_VERSION` (model version in lowercase, hyphen-separated\n  words), e.g. `Assisted-by: Claude Code:claude-opus-5`. It does not need to be the last\n  trailer. Do not add a `Co-authored-by:` trailer naming an AI yourself, but a\n  `Co-authored-by:` trailer that a tool added automatically (e.g. from GitHub Copilot on\n  PR review commits) is accepted as disclosure: replace it with `Assisted-by:` only in\n  your own commit, and leave it alone in someone else's.\n"},
	} {
		dir := gitreftest.Repo(t, map[string]string{tc.file: tc.body})
		if got := ForbiddenBy(dir, "main"); got != "" {
			t.Errorf("%s: ForbiddenBy = %q, want \"\" (the file requires Assisted-by)", tc.source, got)
		}
	}
}

func TestForbiddenBy_NothingToRead(t *testing.T) {
	dir := gitreftest.Repo(t, map[string]string{"AGENTS.md": ocrRule + "\n"})
	for _, tc := range []struct {
		name, dir, ref string
	}{
		{"empty repoDir", "", "main"},
		{"empty ref", dir, ""},
		{"not a git repository", t.TempDir(), "main"},
	} {
		if got := ForbiddenBy(tc.dir, tc.ref); got != "" {
			t.Errorf("%s: ForbiddenBy = %q, want \"\"", tc.name, got)
		}
	}
}
