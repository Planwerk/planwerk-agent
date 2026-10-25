package patterns

import (
	"io/fs"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// TestEmbeddedCatalogMatchesOnDisk walks the on-disk catalog under
// internal/patterns/patterns/ and the embedded fs.FS, then asserts the sets of
// relative .md paths are equal. It fails if a new .md is added on disk but not
// embedded (e.g. a misconfigured embed pattern) or vice versa.
func TestEmbeddedCatalogMatchesOnDisk(t *testing.T) {
	const diskRoot = "patterns" // relative to the package dir (internal/patterns)

	onDisk := map[string]bool{}
	err := filepath.WalkDir(diskRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}
		rel, err := filepath.Rel(diskRoot, path)
		if err != nil {
			return err
		}
		onDisk[filepath.ToSlash(rel)] = true
		return nil
	})
	if err != nil {
		t.Fatalf("walking on-disk catalog: %v", err)
	}
	if len(onDisk) == 0 {
		t.Fatal("found no .md files on disk; test setup is wrong")
	}

	embedded := map[string]bool{}
	err = fs.WalkDir(embeddedFS, diskRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}
		embedded[strings.TrimPrefix(path, diskRoot+"/")] = true
		return nil
	})
	if err != nil {
		t.Fatalf("walking embedded catalog: %v", err)
	}

	for rel := range onDisk {
		if !embedded[rel] {
			t.Errorf("on-disk pattern %q is missing from the embedded catalog", rel)
		}
	}
	for rel := range embedded {
		if !onDisk[rel] {
			t.Errorf("embedded pattern %q has no on-disk counterpart", rel)
		}
	}
}

// TestLoadEmbedded_ParsesKnownPatterns pins a couple of stable pattern names so
// a renamed or dropped embed pattern surfaces immediately, and verifies every
// embedded pattern carries the synthetic "embedded:" FilePath prefix the
// catalog-URL builder depends on.
func TestLoadEmbedded_ParsesKnownPatterns(t *testing.T) {
	pats, err := loadEmbedded()
	if err != nil {
		t.Fatalf("loadEmbedded: %v", err)
	}
	if len(pats) < 80 {
		t.Errorf("loadEmbedded returned %d patterns, want >= 80", len(pats))
	}

	names := map[string]bool{}
	for _, p := range pats {
		names[p.Name] = true
		if !strings.HasPrefix(p.FilePath, "embedded:patterns/") {
			t.Errorf("pattern %q FilePath = %q, want embedded:patterns/ prefix", p.Name, p.FilePath)
		}
	}
	for _, want := range []string{"Go Error Wrapping", "YAGNI - You Aren't Gonna Need It"} {
		if !names[want] {
			t.Errorf("loadEmbedded missing expected pattern %q", want)
		}
	}
}

// TestEmbeddedGoPatterns_GateLoopVariableClaimsOnGoDirective holds every
// line of a Go pattern that reaches a finder prompt (its Detection-Hint
// and each body line) and mentions a loop variable to naming the
// module's `go` directive and the 1.22 boundary on that same line. From
// `go 1.22` each iteration declares its own loop variables, so a capture
// claim without the gate beside it makes a finder flag correct code in a
// modern module; a gate kept on another line drifts away from the claim.
func TestEmbeddedGoPatterns_GateLoopVariableClaimsOnGoDirective(t *testing.T) {
	pats, err := loadEmbedded()
	if err != nil {
		t.Fatalf("loadEmbedded: %v", err)
	}

	loopVar := regexp.MustCompile(`(?i)loop[- ]variable`)
	checked := 0
	for _, p := range pats {
		if !slices.Contains(p.AppliesWhen, "go") {
			continue
		}
		lines := append([]string{p.DetectionHint}, strings.Split(p.Body, "\n")...)
		for _, line := range lines {
			if !loopVar.MatchString(line) {
				continue
			}
			checked++
			if !strings.Contains(line, "`go` directive") || !strings.Contains(line, "1.22") {
				t.Errorf("pattern %q mentions a loop variable without the `go` directive and 1.22 on the same line:\n%s", p.Name, line)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no Go pattern mentions a loop variable; test setup is wrong")
	}
}

// notAFindingHeading opens a pattern's optional carve-out section: the cases
// its checks do not cover (decision 144).
const notAFindingHeading = "## Not a finding"

// convictionWords would turn a Not a finding entry into a confidence bar
// ("unless you are sure"), which decision 98 keeps out of the finder prompts.
// An entry names a case; an uncertain finding is reported with its confidence.
var convictionWords = []string{"confident", "certain", "certainly", "uncertain", "likely", "unlikely", "probably", "are sure"}

// convictionWord matches a convictionWords entry as a whole word, so a case
// that names a "confidential" field is not read as a confidence bar.
var convictionWord = regexp.MustCompile(`(?i)\b(?:` + strings.Join(convictionWords, "|") + `)\b`)

// notAFindingLike matches a heading of any level whose text starts with "not
// a finding" in any case, so a near miss ("## Not a finding:", "### Not a
// finding") is held to notAFindingHeading instead of reaching the finders
// unchecked.
var notAFindingLike = regexp.MustCompile(`(?i)^\s*#+\s*not a finding`)

// seededNotAFinding names the embedded patterns that carry a Not a finding
// section, so a rewrite that drops one fails instead of passing silently.
var seededNotAFinding = []string{
	"Go Formatting and Documentation",
	"Go Package Naming and Design",
	"Go Control Flow Idioms",
	"Go Error Wrapping",
	"Go Receiver and Naming Conventions",
	"Go Testing Patterns",
	"Go Panic and Recover Usage",
	"Go Defer and Resource Management",
	"Python Docstrings",
	"Python Type Hints",
	"Python Exception Handling",
	"OWASP Top 10 Web Application Risks",
	"GitHub Actions Workflow Security",
	"Kubernetes API Conventions",
	"Markdown Quality",
	"DRY - Don't Repeat Yourself",
}

// bodySection is one "## " section of a pattern body: its heading line and
// the lines up to the next heading.
type bodySection struct {
	heading string
	lines   []string
}

// h2Sections splits a pattern body into its "## " sections in order. A "## "
// line inside a ``` fence is content, not a heading. Lines before the first
// heading are dropped; Parse starts the body at one.
func h2Sections(body string) []bodySection {
	var secs []bodySection
	inFence := false
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inFence = !inFence
		}
		if !inFence && strings.HasPrefix(line, "## ") {
			secs = append(secs, bodySection{heading: strings.TrimSpace(line)})
			continue
		}
		if len(secs) > 0 {
			secs[len(secs)-1].lines = append(secs[len(secs)-1].lines, line)
		}
	}
	return secs
}

// notAFindingHeadings returns the lines of body outside ``` fences that
// notAFindingLike matches, in order, with trailing blanks trimmed.
func notAFindingHeadings(body string) []string {
	var heads []string
	inFence := false
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inFence = !inFence
		}
		if !inFence && notAFindingLike.MatchString(line) {
			heads = append(heads, strings.TrimRight(line, " \t"))
		}
	}
	return heads
}

// TestNotAFindingHeadings pins what the section lint treats as a Not a
// finding heading: every heading level and spelling that starts with the
// phrase, so a near miss fails the lint, and nothing inside a ``` fence or in
// prose.
func TestNotAFindingHeadings(t *testing.T) {
	tests := []struct {
		name string
		body string
		want []string
	}{
		{"exact", "## What to check\n\n1. A check.\n\n## Not a finding\n\n- A case.\n", []string{"## Not a finding"}},
		{"trailing colon", "## Why it matters\n\n## Not a finding:\n\n- A case.\n", []string{"## Not a finding:"}},
		{"h3 under the checks", "## What to check\n\n### Not a finding\n\n- A case.\n", []string{"### Not a finding"}},
		{"case variant", "## NOT A FINDING\n", []string{"## NOT A FINDING"}},
		{"indented", "  ## Not a finding\n", []string{"  ## Not a finding"}},
		{"inside a fence", "```markdown\n## Not a finding\n```\n", nil},
		{"named in prose", "Put it in `## Not a finding`.\n", nil},
		{"other heading", "## Why it matters\n", nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := notAFindingHeadings(tc.body); !slices.Equal(got, tc.want) {
				t.Errorf("notAFindingHeadings = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestConvictionWord pins that the lint matches a conviction word only as a
// whole word, and its negated forms too.
func TestConvictionWord(t *testing.T) {
	tests := []struct {
		line string
		want []string
	}{
		{"- Skip a case unless you are sure it is a bug.", []string{"are sure"}},
		{"- Probably fine in a generated file.", []string{"Probably"}},
		{"- A call that is unlikely to fail.", []string{"unlikely"}},
		{"- A log field marked confidential: the logger redacts it.", nil},
		{"- A certainty-free case.", nil},
	}
	for _, tc := range tests {
		if got := convictionWord.FindAllString(tc.line, -1); !slices.Equal(got, tc.want) {
			t.Errorf("convictionWord in %q = %q, want %q", tc.line, got, tc.want)
		}
	}
}

// TestEmbeddedCatalog_NotAFindingSections holds every embedded Not a finding
// section to the form decision 144 gives it: at most one per pattern, spelled
// exactly (a heading of any level that starts with the phrase must be
// notAFindingHeading), directly after "## What to check" so it sits beside
// the checks it qualifies, a bulleted list (two-space continuations allowed)
// of at least one entry, and no conviction words. Every seededNotAFinding
// pattern must carry one.
func TestEmbeddedCatalog_NotAFindingSections(t *testing.T) {
	pats, err := loadEmbedded()
	if err != nil {
		t.Fatalf("loadEmbedded: %v", err)
	}

	carried := map[string]bool{}
	for _, p := range pats {
		heads := notAFindingHeadings(p.Body)
		for _, h := range heads {
			if h != notAFindingHeading {
				t.Errorf("pattern %q: heading %q, want %q", p.Name, h, notAFindingHeading)
			}
		}
		if len(heads) > 1 {
			t.Errorf("pattern %q: %d %q headings, want at most one", p.Name, len(heads), notAFindingHeading)
		}

		secs := h2Sections(p.Body)
		for i, sec := range secs {
			if sec.heading != notAFindingHeading {
				continue
			}
			carried[p.Name] = true
			if i == 0 || secs[i-1].heading != "## What to check" {
				t.Errorf("pattern %q: %q must directly follow \"## What to check\"", p.Name, notAFindingHeading)
			}
			bullets := 0
			for _, line := range sec.lines {
				switch {
				case strings.TrimSpace(line) == "":
					continue
				case strings.HasPrefix(line, "- "):
					bullets++
				case bullets > 0 && strings.HasPrefix(line, "  "):
					// A continuation of the entry above.
				default:
					t.Errorf("pattern %q: %q line is neither a \"- \" entry nor a two-space continuation:\n%s", p.Name, notAFindingHeading, line)
				}
				for _, w := range convictionWord.FindAllString(line, -1) {
					t.Errorf("pattern %q: %q entry sets a confidence bar with %q; name a case instead:\n%s", p.Name, notAFindingHeading, w, line)
				}
			}
			if bullets == 0 {
				t.Errorf("pattern %q: %q has no entries", p.Name, notAFindingHeading)
			}
		}
	}

	for _, name := range seededNotAFinding {
		if !carried[name] {
			t.Errorf("seeded pattern %q carries no %q section", name, notAFindingHeading)
		}
	}
}
