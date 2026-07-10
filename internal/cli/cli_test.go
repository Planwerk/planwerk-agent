package cli

import "testing"

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

	t.Run("no-simplify maps through", func(t *testing.T) {
		if opts := (ImplementConfig{NoSimplify: true}).ToImplementOptions("v1"); !opts.NoSimplify {
			t.Errorf("NoSimplify=%v, want true", opts.NoSimplify)
		}
		// The simplify pass is on by default, so the zero config leaves it off.
		if opts := (ImplementConfig{}).ToImplementOptions("v1"); opts.NoSimplify {
			t.Errorf("NoSimplify=%v, want false by default", opts.NoSimplify)
		}
	})

	t.Run("no-specialists maps through", func(t *testing.T) {
		if opts := (ImplementConfig{NoSpecialists: true}).ToImplementOptions("v1"); !opts.NoSpecialists {
			t.Errorf("NoSpecialists=%v, want true", opts.NoSpecialists)
		}
		// The first-round specialist fan-out is on by default, so the zero config
		// leaves the flag off.
		if opts := (ImplementConfig{}).ToImplementOptions("v1"); opts.NoSpecialists {
			t.Errorf("NoSpecialists=%v, want false by default", opts.NoSpecialists)
		}
	})

	t.Run("no-capture maps through", func(t *testing.T) {
		if opts := (ImplementConfig{NoCapture: true}).ToImplementOptions("v1"); !opts.NoCapture {
			t.Errorf("NoCapture=%v, want true", opts.NoCapture)
		}
		// The capture pass is on by default (gated on a resolved wiki), so the
		// zero config leaves the flag off.
		if opts := (ImplementConfig{}).ToImplementOptions("v1"); opts.NoCapture {
			t.Errorf("NoCapture=%v, want false by default", opts.NoCapture)
		}
	})

	t.Run("no-resume maps through", func(t *testing.T) {
		if opts := (ImplementConfig{NoResume: true}).ToImplementOptions("v1"); !opts.NoResume {
			t.Errorf("NoResume=%v, want true", opts.NoResume)
		}
		// Resume is on by default, so the zero config leaves the flag off.
		if opts := (ImplementConfig{}).ToImplementOptions("v1"); opts.NoResume {
			t.Errorf("NoResume=%v, want false by default", opts.NoResume)
		}
	})

	t.Run("capture-wiki and yes map through", func(t *testing.T) {
		opts := ImplementConfig{CaptureWiki: true, Yes: true}.ToImplementOptions("v1")
		if !opts.CaptureWiki || !opts.Yes {
			t.Errorf("CaptureWiki=%v Yes=%v, want true/true", opts.CaptureWiki, opts.Yes)
		}
		// The write-back is off by default, so the zero config keeps a run
		// propose-only.
		if opts := (ImplementConfig{}).ToImplementOptions("v1"); opts.CaptureWiki || opts.Yes {
			t.Errorf("CaptureWiki=%v Yes=%v, want false/false by default", opts.CaptureWiki, opts.Yes)
		}
	})
}

// TestToShipImplementOptions_NoSpecialistsAbsent documents that ship carries no
// --no-specialists control: a ship-driven implement run always inherits the
// default-on first-round fan-out, and --no-review remains ship's whole-pass
// switch. A ShipConfig has no NoSpecialists field, so the mapped options leave it
// false.
func TestToShipImplementOptions_NoSpecialistsAbsent(t *testing.T) {
	if opts := (ShipConfig{}).ToShipImplementOptions("v1"); opts.NoSpecialists {
		t.Errorf("NoSpecialists=%v, want false — ship has no fan-out off-switch", opts.NoSpecialists)
	}
	// --no-review still disables the whole pass (fan-out included) for ship runs.
	if opts := (ShipConfig{NoReview: true}).ToShipImplementOptions("v1"); !opts.NoReview {
		t.Errorf("NoReview=%v, want true — ship threads --no-review into the implement run", opts.NoReview)
	}
}

// TestToReviewOptions_CaptureFlags guards that the capture flags thread through
// to review.Options — a missing copy would silently disable capture on review
// with no compile error.
func TestToReviewOptions_CaptureFlags(t *testing.T) {
	opts := Config{NoCapture: true, CaptureWiki: true, Yes: true}.ToReviewOptions("v1")
	if !opts.NoCapture || !opts.CaptureWiki || !opts.Yes {
		t.Errorf("NoCapture=%v CaptureWiki=%v Yes=%v, want true/true/true", opts.NoCapture, opts.CaptureWiki, opts.Yes)
	}
	// Capture is on by default (gated on a resolved wiki) and the write-back is
	// off, so the zero config leaves all three flags off.
	if d := (Config{}).ToReviewOptions("v1"); d.NoCapture || d.CaptureWiki || d.Yes {
		t.Errorf("defaults NoCapture=%v CaptureWiki=%v Yes=%v, want false/false/false", d.NoCapture, d.CaptureWiki, d.Yes)
	}
}

// TestToAuditOptions_CaptureFlags guards the same threading for audit.Options.
func TestToAuditOptions_CaptureFlags(t *testing.T) {
	opts := AuditConfig{NoCapture: true, CaptureWiki: true, Yes: true}.ToAuditOptions("v1")
	if !opts.NoCapture || !opts.CaptureWiki || !opts.Yes {
		t.Errorf("NoCapture=%v CaptureWiki=%v Yes=%v, want true/true/true", opts.NoCapture, opts.CaptureWiki, opts.Yes)
	}
	if d := (AuditConfig{}).ToAuditOptions("v1"); d.NoCapture || d.CaptureWiki || d.Yes {
		t.Errorf("defaults NoCapture=%v CaptureWiki=%v Yes=%v, want false/false/false", d.NoCapture, d.CaptureWiki, d.Yes)
	}
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
