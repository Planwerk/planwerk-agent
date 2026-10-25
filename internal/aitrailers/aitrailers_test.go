package aitrailers

import "testing"

// ocrRule is the rule that motivated the package, verbatim from
// alibaba/open-code-review@375f64c (Apache-2.0), AGENTS.md:14.
const ocrRule = `6. You must not attribute commits to AI/LLM, including through "Assisted-by", "Co-developed-by", or similar trailers.`

// TestForbids pins which sentences count as a ban on the trailer. The false
// rows matter as much as the true ones: a false match strips the attribution
// from a repository that wants it, so each one is a rule phrased to look like
// a ban (a requirement, a Co-authored-by ban that says what it is for, a
// reversal, a prohibition word that forbids something else or only places
// the trailer, a rule scoped to commits made without an AI, or this
// repository's own convention) that must not read as one. The last group is
// real bans the matcher gives up for that precision.
func TestForbids(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want bool
	}{
		{ocrRule, true},
		{"Do not add an `Assisted-by` trailer to your commits.", true},
		{"Assisted-by trailers must not be added.", true},
		{"**Never** include `Assisted-by:` or `Generated-by:` lines.", true},
		{"No AI attribution in commit messages.", true},
		{"Do not add Co-authored-by or other AI trailers.", true},
		{"Never mention Claude or Claude Code in commit messages.", true},
		{"Please don't attribute your commits to Copilot or another AI tool.", true},
		{"Avoid AI attribution trailers (e.g. Assisted-by).", true},
		{"We won't accept commits with AI trailers.", true},
		{"Don’t add Assisted-by trailers.", true},
		{"Commits must not credit Claude.", true},
		{"Do not add Co-authored-by and Assisted-by trailers.", true},
		{"AI trailers are not welcome here.", true},
		{"Assisted-by lines shouldn't be added.", true},
		{"If an AI helped, do not add an Assisted-by trailer.", true},
		{"Never add AI attribution to a commit: no `Co-authored-by` or `Assisted-by` trailers.", true},
		// GNOME/gtk@c706c00 CONTRIBUTING.md:297-300: the reversing word of the
		// first sentence does not reach the ban in the second.
		{"Always disclose the use of LLM/GenAI tools when creating an issue or a merge request. Do not include trailers like “Co-authored-by:” or “Assisted-by:” in commit messages, since they serve as free advertising for AI companies.", true},
		// k3s-io/k3s@65d7127 CONTRIBUTING.md:24-25, the same in thanos-io/thanos,
		// oauth2-proxy/oauth2-proxy, and agones-dev/agones: the passive reaches
		// back across the list of trailers the target opens.
		{"Listing AI tooling as a co-author, co-signing commits using an AI tool, or using the `assisted-by`, `co-developed` or similar commit trailer is not allowed.", true},
		{"`Assisted-by`, `Co-developed-by` and `Generated-by` trailers are not allowed.", true},
		{"Assisted-by, Co-developed-by, or similar trailers are not accepted.", true},
		// A widening after Co-authored-by carries the ban past it.
		{`You must not attribute commits to AI/LLM, including through "Co-authored-by" or similar trailers.`, true},
		{"Do not attribute commits to an AI in any way, such as Co-authored-by or any similar trailer.", true},
		// A list of verbs is no clause boundary, a "-session" word that is no
		// trailer key is no Co-authored-by ban, and a condition no comma
		// closes is read with its rule.
		{"Never write, add, or keep an Assisted-by trailer.", true},
		{"Do not write, include or append AI trailers.", true},
		{"Never add per-session notes, and never mention Claude in commits.", true},
		{"Do not add AI trailers to multi-session commits.", true},
		{"If you are an AI agent you must not add an Assisted-by trailer.", true},
		// A "no" that opens the statement, past its list or table marker, or
		// follows a colon, a dash, or a parenthesis is a ban; a verb that keeps
		// the trailer is no hedge; a purpose clause ends at its comma; and a
		// further place past the subject makes the rule a ban again.
		{"No Assisted-by trailers.", true},
		{"- No AI attribution in commits.", true},
		{"| Commit trailers | No Assisted-by or Generated-by |", true},
		{"Commit messages: no AI trailers.", true},
		{"Commit messages — no Assisted-by trailers.", true},
		{"Keep commit messages clean (no AI trailers).", true},
		{"Do not keep the Assisted-by trailer.", true},
		{"To keep the history clean, do not add AI trailers.", true},
		{"To comply with the license never add Assisted-by trailers, and sign off.", true},
		{"Do not put AI attribution in the subject or body of a commit.", true},

		{"Contributions should include an Assisted-by tag: `Assisted-by: AGENT_NAME:MODEL_VERSION`.", false},
		{"Assisted-by is required, Co-authored-by is not allowed.", false},
		{"Use Assisted-by, Co-authored-by is not allowed.", false},
		{"AI agents MUST NOT add Signed-off-by tags.", false},
		{"Never add `Co-Authored-By: Claude` to commits.", false},
		{"Never add a Co-authored-by trailer for Claude.", false},
		{"Do not use Co-authored-by for AI, use Assisted-by instead.", false},
		{"Don't forget the Assisted-by trailer.", false},
		{"Never remove the Assisted-by trailer when you amend a commit.", false},
		{"Commits without an Assisted-by trailer are not accepted.", false},
		{"Commits with no Assisted-by trailer are not accepted.", false},
		{"Commits that lack an Assisted-by trailer will not be merged.", false},
		{"Commits that carry no Assisted-by trailer will not be merged.", false},
		{"AI agents must not mention ticket numbers in commit messages.", false},
		{"Do not take credit for AI-generated code.", false},
		{"Assisted-by trailers are welcome.", false},
		{"Never commit directly to main.", false},
		{"Use `Assisted-by: Claude:<model>` on every commit; never `Co-authored-by`.", false},
		{"Never use Co-authored-by, always use Assisted-by.", false},
		{"You must add an Assisted-by trailer, never a Co-authored-by one.", false},
		{"Commits carry `Assisted-by` above `Signed-off-by`, and never `Co-authored-by`.", false},
		{"Name yourself in an `Assisted-by` trailer. Append your exact model id when your runtime provides it (`Assisted-by: Claude:claude-opus-5-5`); otherwise emit `Assisted-by: Claude` alone — never guess the id. Drop any bracketed suffix such as `[1m]`: it names a context window, not the model. Pass the trailer as the final `-m` paragraph, not via `--trailer`: git places `--trailer` values *after* the sign-off, which breaks the order.", false},
		{"Never push to main, and add Assisted-by to every commit.", false},
		{"1. **You must disclose in your initial issue or pull request that you used AI/LLM, as well as the tools/models you used.**", false},
		{"Do not add comments that restate what the code already says, narrate the change itself (that belongs in the commit message or PR description).", false},

		// Bans on Co-authored-by that say what the ban is for.
		{"Do not use `Co-Authored-By` for AI attribution.", false},
		{"When creating git commits, do NOT use `Co-Authored-By` trailers for AI attribution.", false},
		{"Do not add a `Co-authored-by:` trailer naming an AI in your commit.", false},
		{"Do not add `Co-Authored-By` or `Claude-Session` trailers.", false},
		{"Do not add a `Claude-Session:` trailer to a commit.", false},
		{"Never add Claude-Session trailers, and add Assisted-by to every commit.", false},
		{"No Claude attribution: no `Co-Authored-By: Claude` in commits.", false},

		// A prohibition word that forbids something other than the trailer.
		{"Don't use Co-authored-by, use Assisted-by.", false},
		{"If you cannot determine your model id, write `Assisted-by: Claude` alone.", false},
		{"If you cannot determine your model id, the Assisted-by trailer names the agent alone.", false},
		{"We do not require an Assisted-by trailer, but it is appreciated.", false},
		{"Do not hesitate to add an Assisted-by trailer.", false},
		{"For example, if `Assisted-by: Codex:GPT-5.6 Sol` applies, do not also add the less-specific `Assisted-by: Codex:GPT-5` trailer.", false},
		{"Do not add a second Assisted-by trailer for the same agent.", false},
		{"Do not add duplicate Assisted-by trailers.", false},
		{"Do not delete the Assisted-by trailer when you amend a commit.", false},
		{"Never strip the Assisted-by trailer when you squash.", false},
		{"Do not change the format of the Assisted-by trailer.", false},
		{"Avoid modifying the Assisted-by trailer by hand.", false},
		{"To avoid confusion, Assisted-by trailers name the agent and the model.", false},
		{"If the project forbids AI trailers, follow its rule; otherwise add Assisted-by.", false},
		{"If there is no Assisted-by trailer, the hook adds one.", false},
		// plugins/planwerk/shared/commits.md, this repository's own doctrine.
		{"Where the repository forbids attributing commits to an AI, its rule wins: every commit ends with the `Signed-off-by` line alone, and no line of the message names Claude or a model.", false},

		// A prohibition that only places the trailer, says what goes into it,
		// or scopes it to commits made without an AI.
		{"Never place Assisted-by below Signed-off-by.", false},
		{"To avoid confusion, the Assisted-by trailer goes after the Signed-off-by line.", false},
		{"Do not put Assisted-by in the subject line.", false},
		{"Do not guess the model id in the Assisted-by trailer.", false},
		{"If no AI tool was involved, do not add an Assisted-by trailer.", false},
		{"Human-only commits carry no Assisted-by trailer.", false},

		// Real bans the per-sentence Co-authored-by rule gives up: a sentence
		// whose only trailer is Co-authored-by does not count through its
		// other targets, whatever else it forbids. That is the price of
		// leaving the PX4 CLAUDE.md in TestForbiddenBy_RulesThatRequireTheTrailer
		// alone: its "No Claude attribution" sentence bans only Co-authored-by,
		// and the file asks for Assisted-by.
		{"Never mention Claude or Claude Code in commit messages, and never add Co-authored-by.", false},
		{"No AI attribution in commit messages, including Co-authored-by.", false},
		// A "no" further into a sentence is read as describing the commits a
		// requirement is about, the price of leaving "commits that carry no
		// Assisted-by trailer will not be merged" alone.
		{"Commits should carry no Assisted-by trailer.", false},
	} {
		if got := forbids(tc.in); got != tc.want {
			t.Errorf("forbids(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// TestForbiddingLine_WrappedItemAndFence: a list item wrapped over two lines is
// read whole and reported at its first line, and a rule inside a fenced code
// block is an example, not a rule.
func TestForbiddingLine_WrappedItemAndFence(t *testing.T) {
	// Neither line forbids anything alone: the prohibition is on the first,
	// the trailer on the second.
	wrapped := "# Rules\n\nSome intro.\n\n- Do not add an\n  Assisted-by trailer to your commits.\n- Other.\n"
	if n, ok := forbiddingLine(wrapped); !ok || n != 5 {
		t.Errorf("forbiddingLine(wrapped item) = %d, %v; want 5, true", n, ok)
	}

	// The first line alone is a ban; the continuation line turns it into a
	// condition, so the item read whole is not one.
	reversed := "- Never add an Assisted-by trailer\n  without asking the author first.\n"
	if n, ok := forbiddingLine(reversed); ok {
		t.Errorf("forbiddingLine read the first line of a wrapped item alone, matching at line %d", n)
	}

	fenced := "Example:\n\n```\n# never add Assisted-by trailers\ngit commit -s -m msg -m \"Assisted-by: Claude\"\n```\n"
	if n, ok := forbiddingLine(fenced); ok {
		t.Errorf("forbiddingLine matched the fenced example at line %d", n)
	}
}
