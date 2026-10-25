package glossary

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sharedHouseStyleDoc is the skill-side document that tells every planwerk
// skill where a repository's domain glossary lives. glossaryLocations says the
// same for the headless commands, so the two are free to drift, and they have:
// the diagnose skill carried a two-path copy after the loader learned
// GLOSSARY.md.
const sharedHouseStyleDoc = "../../plugins/planwerk/shared/house-style.md"

// glossarySectionHeading opens the house-style section that lists the paths.
const glossarySectionHeading = "## Domain glossary"

// TestSharedHouseStyleListsGlossaryLocations pins the path list across the
// loader and house-style.md: the section names every entry of
// glossaryLocations, as a code span, in the loader's precedence order.
func TestSharedHouseStyleListsGlossaryLocations(t *testing.T) {
	raw, err := os.ReadFile(filepath.Clean(sharedHouseStyleDoc))
	if err != nil {
		t.Fatalf("reading %s: %v", sharedHouseStyleDoc, err)
	}
	doc := string(raw)

	start := strings.Index(doc, glossarySectionHeading+"\n")
	if start < 0 {
		t.Fatalf("%s has no %q section; the skills no longer learn where the glossary lives", sharedHouseStyleDoc, glossarySectionHeading)
	}
	section := doc[start+len(glossarySectionHeading):]
	if end := strings.Index(section, "\n## "); end >= 0 {
		section = section[:end]
	}

	last := -1
	for _, loc := range glossaryLocations {
		want := "`" + filepath.ToSlash(loc) + "`"
		i := strings.Index(section, want)
		if i < 0 {
			t.Errorf("%s, %s, does not name %s; the skill and loader surfaces have drifted", sharedHouseStyleDoc, glossarySectionHeading, want)
			continue
		}
		if i < last {
			t.Errorf("%s, %s, names %s out of the loader's precedence order", sharedHouseStyleDoc, glossarySectionHeading, want)
		}
		last = i
	}
}
