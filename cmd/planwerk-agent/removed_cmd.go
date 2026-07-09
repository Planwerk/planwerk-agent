package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

// skillInstallHint is the two-line install the removal errors point at. It
// mirrors docs/how-to/use-the-skills.md.
const skillInstallHint = `  claude plugin marketplace add planwerk/planwerk-agent
  claude plugin install planwerk@planwerk-agent`

// newRemovedCmds returns hidden tombstones for the subcommands that became
// interactive Claude Code Skills (design decision 64). Without them the root
// command — which defaults to `review` and takes at most one positional —
// answers `planwerk-agent draft owner/repo "an idea"` with "accepts between 0
// and 1 arg(s), received 3", which tells an upgrading user nothing about where
// the command went.
//
// They are Hidden so `--help` advertises only real commands, accept any
// arguments so the old invocation reaches the error rather than an arity
// complaint, and always fail: a tombstone that silently did nothing would be
// worse than the confusing arity error it replaces.
func newRemovedCmds() []*cobra.Command {
	removed := []struct {
		name  string
		skill string
		doc   string
	}{
		{"draft", "/planwerk:draft", "https://planwerk.github.io/planwerk-agent/how-to/draft-an-issue"},
		{"meta", "/planwerk:meta", "https://planwerk.github.io/planwerk-agent/how-to/split-a-meta-issue"},
	}

	cmds := make([]*cobra.Command, 0, len(removed))
	for _, r := range removed {
		cmds = append(cmds, &cobra.Command{
			Use:    r.name,
			Hidden: true,
			Short:  fmt.Sprintf("Removed — use the %s skill", r.skill),
			Args:   cobra.ArbitraryArgs,
			RunE: func(_ *cobra.Command, _ []string) error {
				return fmt.Errorf(
					"the %q subcommand was removed; it is now the %s Claude Code skill, which asks you the questions the command had to guess\n\ninstall it once with:\n%s\n\nthen run %s inside a Claude Code session\n\nsee %s",
					r.name, r.skill, skillInstallHint, r.skill, r.doc)
			},
		})
	}
	return cmds
}
