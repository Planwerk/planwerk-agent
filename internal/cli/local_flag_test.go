package cli

import "testing"

// TestLocalForcePropagation asserts that the Local and Force fields copy
// through every repo-facing command's ToXxxOptions method. Each subcommand
// owns an independent Config → Options mapping, so a missing copy in any one
// of them would silently disable --local for that command.
func TestLocalForcePropagation(t *testing.T) {
	const version = "test"

	t.Run("review", func(t *testing.T) {
		opts := Config{Local: true, Force: true}.ToReviewOptions(version)
		if !opts.Local || !opts.Force {
			t.Errorf("review: Local=%v Force=%v, want true/true", opts.Local, opts.Force)
		}
	})
	t.Run("propose", func(t *testing.T) {
		opts := ProposeConfig{Local: true, Force: true}.ToProposeOptions(version)
		if !opts.Local || !opts.Force {
			t.Errorf("propose: Local=%v Force=%v, want true/true", opts.Local, opts.Force)
		}
	})
	t.Run("audit", func(t *testing.T) {
		opts := AuditConfig{Local: true, Force: true}.ToAuditOptions(version)
		if !opts.Local || !opts.Force {
			t.Errorf("audit: Local=%v Force=%v, want true/true", opts.Local, opts.Force)
		}
	})
	t.Run("elaborate", func(t *testing.T) {
		opts := ElaborateConfig{Local: true, Force: true}.ToElaborateOptions(version)
		if !opts.Local || !opts.Force {
			t.Errorf("elaborate: Local=%v Force=%v, want true/true", opts.Local, opts.Force)
		}
	})
	t.Run("fix", func(t *testing.T) {
		opts := FixConfig{Local: true, Force: true}.ToFixOptions(version)
		if !opts.Local || !opts.Force {
			t.Errorf("fix: Local=%v Force=%v, want true/true", opts.Local, opts.Force)
		}
	})
	t.Run("implement", func(t *testing.T) {
		opts := ImplementConfig{Local: true, Force: true}.ToImplementOptions(version)
		if !opts.Local || !opts.Force {
			t.Errorf("implement: Local=%v Force=%v, want true/true", opts.Local, opts.Force)
		}
	})
	t.Run("gap-analysis", func(t *testing.T) {
		opts := GapAnalysisConfig{Local: true, Force: true}.ToGapAnalysisOptions(version)
		if !opts.Local || !opts.Force {
			t.Errorf("gap-analysis: Local=%v Force=%v, want true/true", opts.Local, opts.Force)
		}
	})
	t.Run("review-prepared", func(t *testing.T) {
		opts := ReviewPreparedConfig{Local: true, Force: true}.ToReviewPreparedOptions(version)
		if !opts.Local || !opts.Force {
			t.Errorf("review-prepared: Local=%v Force=%v, want true/true", opts.Local, opts.Force)
		}
	})

	// Default must stay false so the no-flag behavior is unchanged.
	t.Run("defaults-false", func(t *testing.T) {
		opts := Config{}.ToReviewOptions(version)
		if opts.Local || opts.Force {
			t.Errorf("review defaults: Local=%v Force=%v, want false/false", opts.Local, opts.Force)
		}
	})
}
