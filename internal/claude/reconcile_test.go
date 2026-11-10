package claude

import (
	"testing"

	"github.com/planwerk/planwerk-agent/internal/gapanalysis"
	"github.com/planwerk/planwerk-agent/internal/planwerk"
	"github.com/planwerk/planwerk-agent/internal/reviewprepared"
)

// The structuring call sees the report alone, never the <feature file=…>
// blocks the analysis read, so a file name or a title it returns is a guess.
// Both reconcile steps therefore take the two from the input spec, whatever
// the structured result carries: review-prepared commits the improved spec to
// that file name.

func TestReconcileReviewFeatures_TakesFileAndTitleFromTheSpec(t *testing.T) {
	features := []reviewprepared.PreparedFeature{{
		Feature: &planwerk.Feature{FeatureID: "PX-0001", Title: "Real title", FilePath: "/repo/.planwerk/features/PX-0001-real.json"},
	}}
	for _, guessed := range []string{"PX-0001-guessed.json", "../../outside.json", ""} {
		result := &reviewprepared.Result{Features: []reviewprepared.FeatureReview{
			{
				FeatureID:   "px-0001",
				FeatureFile: guessed,
				Title:       "Guessed title",
				Findings:    []reviewprepared.Finding{{FeatureFile: guessed}},
			},
			{FeatureID: "PX-9999", FeatureFile: "PX-9999-invented.json"},
		}}

		reconcileReviewFeatures(result, features)

		known := result.Features[0]
		if known.FeatureFile != "PX-0001-real.json" || known.Title != "Real title" {
			t.Errorf("guessed %q: feature = (%q, %q), want the input spec's file and title", guessed, known.FeatureFile, known.Title)
		}
		if got := known.Findings[0].FeatureFile; got != "PX-0001-real.json" {
			t.Errorf("guessed %q: finding file = %q, want the input spec's", guessed, got)
		}
		if got := result.Features[1].FeatureFile; got != "" {
			t.Errorf("a feature the input does not carry kept the file %q", got)
		}
	}
}

func TestReconcileFeatures_TakesFileAndTitleFromTheSpec(t *testing.T) {
	features := []*planwerk.Feature{{FeatureID: "CC-0042", Title: "Real title", FilePath: "/repo/.planwerk/completed/CC-0042-real.json"}}
	for _, guessed := range []string{"CC-0042-guessed.json", "../../outside.json", ""} {
		result := &gapanalysis.Result{Features: []gapanalysis.FeatureGaps{
			{
				FeatureID:   "cc-0042",
				FeatureFile: guessed,
				Title:       "Guessed title",
				Gaps:        []gapanalysis.Gap{{FeatureFile: guessed}},
			},
			{FeatureID: "CC-9999", FeatureFile: "CC-9999-invented.json"},
		}}

		reconcileFeatures(result, features)

		known := result.Features[0]
		if known.FeatureFile != "CC-0042-real.json" || known.Title != "Real title" {
			t.Errorf("guessed %q: feature = (%q, %q), want the input spec's file and title", guessed, known.FeatureFile, known.Title)
		}
		if got := known.Gaps[0].FeatureFile; got != "CC-0042-real.json" {
			t.Errorf("guessed %q: gap file = %q, want the input spec's", guessed, got)
		}
		if got := result.Features[1].FeatureFile; got != "" {
			t.Errorf("a feature the input does not carry kept the file %q", got)
		}
	}
}
