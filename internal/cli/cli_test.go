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
