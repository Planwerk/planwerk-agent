package patterns_test

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/planwerk/planwerk-review/internal/patterns"
)

func projectRoot() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..")
}

func TestLoadShippedPatterns(t *testing.T) {
	root := projectRoot()
	patsDir := filepath.Join(root, "patterns")

	all, err := patterns.Load(patsDir)
	if err != nil {
		t.Fatalf("loading shipped patterns: %v", err)
	}

	if len(all) < 20 {
		t.Errorf("expected at least 20 shipped patterns, got %d", len(all))
	}

	// Check category distribution
	tech, design := 0, 0
	for _, p := range all {
		switch p.Category {
		case "technology":
			tech++
		case "design-principle":
			design++
		}
	}
	if tech < 10 {
		t.Errorf("expected at least 10 technology patterns, got %d", tech)
	}
	if design < 8 {
		t.Errorf("expected at least 8 design patterns, got %d", design)
	}
}

func TestLoadFilteredShippedPatterns_GoProject(t *testing.T) {
	root := projectRoot()
	patsDir := filepath.Join(root, "patterns")

	pats, err := patterns.LoadFiltered([]string{"go"}, patsDir)
	if err != nil {
		t.Fatalf("loading filtered patterns: %v", err)
	}

	for _, p := range pats {
		if !p.AppliesTo([]string{"go"}) {
			t.Errorf("pattern %q should not be included for go project (AppliesWhen=%v)", p.Name, p.AppliesWhen)
		}
	}

	// Should include Go patterns + design patterns, but not Python/Helm/Terraform-only
	names := map[string]bool{}
	for _, p := range pats {
		names[p.Name] = true
	}

	if !names["Go Error Wrapping"] {
		t.Error("missing Go Error Wrapping")
	}
	if !names["YAGNI - You Aren't Gonna Need It"] {
		t.Error("missing YAGNI (design patterns should always apply)")
	}
	if names["Python Type Hints"] {
		t.Error("Python Type Hints should not appear for Go project")
	}
	if names["Terraform State Safety"] {
		t.Error("Terraform State Safety should not appear for Go project")
	}
}

// TestLoadShippedPatterns_DocumentationDiataxis pins the cross-cutting
// Diátaxis pattern's identity so renames or accidental Applies-When tags
// (which would scope it away from non-doc projects) surface immediately.
func TestLoadShippedPatterns_DocumentationDiataxis(t *testing.T) {
	root := projectRoot()
	patsDir := filepath.Join(root, "patterns")

	all, err := patterns.Load(patsDir)
	if err != nil {
		t.Fatalf("loading shipped patterns: %v", err)
	}

	var doc *patterns.Pattern
	for i := range all {
		if all[i].Name == "Documentation Structure (Diátaxis)" {
			doc = &all[i]
			break
		}
	}
	if doc == nil {
		t.Fatal("missing pattern: Documentation Structure (Diátaxis)")
	}
	if doc.Category != "design-principle" {
		t.Errorf("category = %q, want design-principle (cross-cutting)", doc.Category)
	}
	if len(doc.AppliesWhen) != 0 {
		t.Errorf("AppliesWhen = %v, want empty (must apply to every project)", doc.AppliesWhen)
	}
	if doc.Severity == "" {
		t.Error("Severity must be set")
	}
	// Sanity: the body must mention every Diátaxis mode so the prompt-injected
	// pattern actually covers all four reader needs, not just one.
	for _, mode := range []string{"Tutorial", "How-To", "Reference", "Explanation"} {
		if !strings.Contains(doc.Body, mode) {
			t.Errorf("pattern body missing Diátaxis mode %q", mode)
		}
	}
}

// TestLoadShippedPatterns_PythonDocstrings pins the per-language docstring
// pattern as Python-scoped so it does not pollute non-Python reviews.
func TestLoadShippedPatterns_PythonDocstrings(t *testing.T) {
	root := projectRoot()
	patsDir := filepath.Join(root, "patterns")

	pats, err := patterns.LoadFiltered([]string{"python"}, patsDir)
	if err != nil {
		t.Fatalf("loading python-filtered patterns: %v", err)
	}

	var found bool
	for _, p := range pats {
		if p.Name == "Python Docstrings" {
			found = true
			if !p.AppliesTo([]string{"python"}) {
				t.Errorf("Python Docstrings should apply to python projects")
			}
			if p.AppliesTo([]string{"go"}) {
				t.Errorf("Python Docstrings must NOT apply to non-python projects")
			}
		}
	}
	if !found {
		t.Fatal("missing pattern: Python Docstrings")
	}
}
