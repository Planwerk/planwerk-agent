package main

import (
	"testing"

	"github.com/spf13/cobra"

	"github.com/planwerk/planwerk-agent/internal/elaborate"
	"github.com/planwerk/planwerk-agent/internal/prompt"
)

func TestElaborateUpdateMode(t *testing.T) {
	cases := []struct {
		updateIssue, postComment bool
		want                     elaborate.UpdateMode
	}{
		{false, false, elaborate.UpdateNone},
		{true, false, elaborate.UpdateReplace},
		{false, true, elaborate.UpdateComment},
	}
	for _, c := range cases {
		if got := elaborateUpdateMode(c.updateIssue, c.postComment); got != c.want {
			t.Errorf("elaborateUpdateMode(%v, %v) = %v, want %v", c.updateIssue, c.postComment, got, c.want)
		}
	}
}

func TestPromptMode(t *testing.T) {
	cases := map[string]prompt.Mode{"auto": prompt.ModeAuto, "fix": prompt.ModeFix, "implement": prompt.ModeImplement}
	for in, want := range cases {
		if got := promptMode(in); got != want {
			t.Errorf("promptMode(%q) = %v, want %v", in, got, want)
		}
	}
}

// TestShipCarriesNoSpecialistsSwitch documents that ship has no
// --no-specialists control: a ship-driven implement run always inherits the
// default-on first-round fan-out, and --no-review remains ship's whole-pass
// switch.
func TestShipCarriesNoSpecialistsSwitch(t *testing.T) {
	cmd := newShipCmd(&runtimeDeps{})
	if cmd.Flags().Lookup("no-specialists") != nil {
		t.Error("ship must not expose --no-specialists")
	}
	if cmd.Flags().Lookup("no-review") == nil {
		t.Error("ship must expose --no-review, its whole-pass switch")
	}
}

// TestCommandsRegisterTheWikiFlags pins the wiki opt-in on the commands that
// read the project memory beside review, audit, propose, and implement, and
// its absence on a command that reads none.
func TestCommandsRegisterTheWikiFlags(t *testing.T) {
	deps := &runtimeDeps{}
	readers := map[string]*cobra.Command{
		"elaborate": newElaborateCmd(deps),
		"fix":       newFixCmd(deps),
		"address":   newAddressCmd(deps),
	}
	for name, cmd := range readers {
		for _, flag := range []string{"wiki", "no-wiki", "wiki-ref"} {
			if cmd.Flags().Lookup(flag) == nil {
				t.Errorf("%s must expose --%s", name, flag)
			}
		}
	}
	if newRebaseCmd(deps).Flags().Lookup("wiki") != nil {
		t.Error("rebase reads no project memory and must not expose --wiki")
	}
}
