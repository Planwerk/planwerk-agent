package main

import (
	"strings"
	"testing"
)

// TestRemovedCmds guards the tombstones for the subcommands that became skills.
// A tombstone that stopped failing, or stopped naming its replacement, would
// leave an upgrading user with the root command's unhelpful arity error.
func TestRemovedCmds(t *testing.T) {
	cmds := newRemovedCmds()
	if len(cmds) != 2 {
		t.Fatalf("got %d removed commands, want draft and meta", len(cmds))
	}

	wantSkill := map[string]string{"draft": "/planwerk:draft", "meta": "/planwerk:meta"}
	for _, c := range cmds {
		skill, ok := wantSkill[c.Use]
		if !ok {
			t.Errorf("unexpected removed command %q", c.Use)
			continue
		}
		if !c.Hidden {
			t.Errorf("%q must be hidden so --help advertises only real commands", c.Use)
		}

		// The old invocation carried positionals; the tombstone must accept them
		// and reach its error rather than fail on arity.
		if err := c.Args(c, []string{"owner/repo", "an idea"}); err != nil {
			t.Errorf("%q must accept the old positional arguments, got %v", c.Use, err)
		}

		err := c.RunE(c, []string{"owner/repo", "an idea"})
		if err == nil {
			t.Fatalf("%q must fail; a silent no-op is worse than the arity error it replaces", c.Use)
		}
		for _, want := range []string{skill, "claude plugin install planwerk@planwerk-agent"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%q error must mention %q; got: %v", c.Use, want, err)
			}
		}
	}
}
