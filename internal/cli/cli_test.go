package cli

import "testing"

func TestToDraftOptions(t *testing.T) {
	t.Run("no-create implies dry-run", func(t *testing.T) {
		opts := DraftConfig{NoCreate: true}.ToDraftOptions("v1")
		if !opts.DryRun {
			t.Error("--no-create should map to DryRun")
		}
	})

	t.Run("dry-run implies dry-run", func(t *testing.T) {
		opts := DraftConfig{DryRun: true}.ToDraftOptions("v1")
		if !opts.DryRun {
			t.Error("--dry-run should map to DryRun")
		}
	})

	t.Run("neither leaves dry-run off", func(t *testing.T) {
		opts := DraftConfig{}.ToDraftOptions("v1")
		if opts.DryRun {
			t.Error("DryRun should be false without --dry-run/--no-create")
		}
	})

	t.Run("fields and version thread through", func(t *testing.T) {
		cfg := DraftConfig{
			RepoRef:       "acme/widgets",
			Seed:          "an idea",
			Local:         true,
			NoInteractive: true,
			Labels:        []string{"enhancement"},
			Format:        "json",
		}
		opts := cfg.ToDraftOptions("v2")
		if opts.RepoRef != "acme/widgets" || opts.Seed != "an idea" || !opts.Local ||
			!opts.NoInteractive || opts.Format != "json" || opts.Version != "v2" ||
			len(opts.Labels) != 1 || opts.Labels[0] != "enhancement" {
			t.Errorf("unexpected options: %+v", opts)
		}
	})
}

// TestToImplementOptions_VerifyFlags guards the verify flag mappings: the two
// passes are independent, and a missing copy in ToImplementOptions would
// silently disable a flag with no compile error.
func TestToImplementOptions_VerifyFlags(t *testing.T) {
	t.Run("verify and verify-adversarial map independently", func(t *testing.T) {
		opts := ImplementConfig{Verify: true, VerifyAdversarial: true}.ToImplementOptions("v1")
		if !opts.Verify || !opts.VerifyAdversarial {
			t.Errorf("Verify=%v VerifyAdversarial=%v, want true/true", opts.Verify, opts.VerifyAdversarial)
		}
	})

	t.Run("verify-adversarial does not require verify", func(t *testing.T) {
		opts := ImplementConfig{VerifyAdversarial: true}.ToImplementOptions("v1")
		if opts.Verify || !opts.VerifyAdversarial {
			t.Errorf("Verify=%v VerifyAdversarial=%v, want false/true", opts.Verify, opts.VerifyAdversarial)
		}
	})

	t.Run("defaults stay off", func(t *testing.T) {
		opts := ImplementConfig{}.ToImplementOptions("v1")
		if opts.Verify || opts.VerifyAdversarial {
			t.Errorf("Verify=%v VerifyAdversarial=%v, want false/false", opts.Verify, opts.VerifyAdversarial)
		}
	})
}

// TestToAddressOptions guards the reply reconciliation (the only non-trivial
// mapping) and that the flags thread through. A missing copy in
// ToAddressOptions would silently drop a flag with no compile error.
func TestToAddressOptions(t *testing.T) {
	t.Run("no-reply overrides reply", func(t *testing.T) {
		opts := AddressConfig{Reply: true, NoReply: true}.ToAddressOptions("v1")
		if opts.Reply {
			t.Error("--no-reply should override --reply")
		}
	})

	t.Run("reply stays on without no-reply", func(t *testing.T) {
		opts := AddressConfig{Reply: true}.ToAddressOptions("v1")
		if !opts.Reply {
			t.Error("Reply should stay on when --no-reply is absent")
		}
	})

	t.Run("outward-facing defaults stay off", func(t *testing.T) {
		opts := AddressConfig{}.ToAddressOptions("v1")
		if opts.Resolve || opts.All || opts.IncludeResolved {
			t.Errorf("Resolve=%v All=%v IncludeResolved=%v, want all false", opts.Resolve, opts.All, opts.IncludeResolved)
		}
	})

	t.Run("fields and version thread through", func(t *testing.T) {
		cfg := AddressConfig{
			PRRef:              "acme/widgets#7",
			All:                true,
			ThreadIDs:          []string{"RT_1", "RT_2"},
			IncludeResolved:    true,
			Resolve:            true,
			OneCommitPerThread: true,
			NoAddressComment:   true,
			MaxIterations:      3,
			Local:              true,
			MaxPatterns:        5,
		}
		opts := cfg.ToAddressOptions("v2")
		if opts.PRRef != "acme/widgets#7" || !opts.All || len(opts.ThreadIDs) != 2 ||
			!opts.IncludeResolved || !opts.Resolve || !opts.OneCommitPerThread ||
			!opts.NoAddressComment || opts.MaxIterations != 3 || !opts.Local ||
			opts.MaxPatterns != 5 || opts.Version != "v2" {
			t.Errorf("unexpected options: %+v", opts)
		}
	})
}
