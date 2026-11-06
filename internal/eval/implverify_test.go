package eval

import (
	"path/filepath"
	"slices"
	"testing"
)

// writeImplementVerifierCase writes a valid case under root/name with the
// given issue.md and plan.md, and returns it loaded.
func writeImplementVerifierCase(t *testing.T, root, name, issue, plan string) Case {
	t.Helper()
	dir := writeValidCase(t, root, name)
	writeFile(t, filepath.Join(dir, implVerifyIssueFile), issue)
	writeFile(t, filepath.Join(dir, implVerifyPlanFile), plan)
	c, err := LoadCase(dir)
	if err != nil {
		t.Fatalf("LoadCase: %v", err)
	}
	return c
}

func TestReadImplementVerifierSpec(t *testing.T) {
	t.Run("splits the title from the body", func(t *testing.T) {
		c := writeImplementVerifierCase(t, t.TempDir(), "sample", "# Title\n\nBody text", "## Implementation Plan (issue #1)\n\nSTATUS: PLAN_READY\n")
		title, body, plan, err := readImplementVerifierSpec(c)
		if err != nil {
			t.Fatalf("readImplementVerifierSpec: %v", err)
		}
		if title != "Title" || body != "Body text" {
			t.Errorf("title, body = %q, %q, want %q, %q", title, body, "Title", "Body text")
		}
		if plan != "## Implementation Plan (issue #1)\n\nSTATUS: PLAN_READY" {
			t.Errorf("plan = %q, want the trimmed plan.md", plan)
		}
	})

	t.Run("an empty plan.md yields no plan", func(t *testing.T) {
		c := writeImplementVerifierCase(t, t.TempDir(), "sample", "# Title\n\nBody text\n", "")
		_, _, plan, err := readImplementVerifierSpec(c)
		if err != nil {
			t.Fatalf("readImplementVerifierSpec: %v", err)
		}
		if plan != "" {
			t.Errorf("plan = %q, want empty", plan)
		}
	})

	t.Run("an issue.md without a title line fails", func(t *testing.T) {
		c := writeImplementVerifierCase(t, t.TempDir(), "sample", "Title\n\nBody text\n", "")
		_, _, _, err := readImplementVerifierSpec(c)
		if want := `case sample: issue.md must open with a "# <title>" line`; err == nil || err.Error() != want {
			t.Errorf("readImplementVerifierSpec = %v, want %q", err, want)
		}
	})

	for _, missing := range []string{implVerifyIssueFile, implVerifyPlanFile} {
		t.Run("a missing "+missing+" fails", func(t *testing.T) {
			root := t.TempDir()
			dir := writeValidCase(t, root, "sample")
			for _, name := range []string{implVerifyIssueFile, implVerifyPlanFile} {
				if name != missing {
					writeFile(t, filepath.Join(dir, name), "# Title\n")
				}
			}
			c, err := LoadCase(dir)
			if err != nil {
				t.Fatalf("LoadCase: %v", err)
			}
			_, _, _, err = readImplementVerifierSpec(c)
			if want := "case sample: " + missing + " is missing"; err == nil || err.Error() != want {
				t.Errorf("readImplementVerifierSpec = %v, want %q", err, want)
			}
		})
	}
}

// TestShippedImplementVerifierCorpusValid loads the implementation verifier
// corpus that ships with the repo, as TestShippedCorpusValid does for the
// review corpus, so a malformed case fails in CI before any run spends tokens.
func TestShippedImplementVerifierCorpusValid(t *testing.T) {
	cases, err := LoadImplementVerifierCases("implverify", "")
	if err != nil {
		t.Fatalf("shipped implementation verifier corpus does not load: %v", err)
	}
	var names []string
	for _, c := range cases {
		names = append(names, c.Name)
		assertFindingsInHead(t, c)
	}
	if want := []string{"clean", "drive-by", "missing-package"}; !slices.Equal(names, want) {
		t.Errorf("cases = %v, want %v", names, want)
	}
}

func TestLoadImplementVerifierCases(t *testing.T) {
	t.Run("a named case loads alone", func(t *testing.T) {
		cases, err := LoadImplementVerifierCases("implverify", "drive-by")
		if err != nil {
			t.Fatalf("LoadImplementVerifierCases: %v", err)
		}
		if len(cases) != 1 || cases[0].Name != "drive-by" {
			t.Errorf("cases = %+v, want only drive-by", cases)
		}
	})

	t.Run("a case without plan.md fails the load", func(t *testing.T) {
		root := t.TempDir()
		writeImplementVerifierCase(t, root, "good", "# Title\n", "")
		dir := writeValidCase(t, root, "bad")
		writeFile(t, filepath.Join(dir, implVerifyIssueFile), "# Title\n")
		_, err := LoadImplementVerifierCases(root, "")
		if want := "case bad: plan.md is missing"; err == nil || err.Error() != want {
			t.Errorf("LoadImplementVerifierCases = %v, want %q", err, want)
		}
	})

	t.Run("a review corpus case fails the load", func(t *testing.T) {
		if _, err := LoadImplementVerifierCases("corpus", "off-by-one"); err == nil {
			t.Error("LoadImplementVerifierCases(review case) = nil error, want issue.md is missing")
		}
	})
}
