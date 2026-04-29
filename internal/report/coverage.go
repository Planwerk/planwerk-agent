package report

import (
	"fmt"
	"io"
	"strings"
)

// CoverageEntry represents test coverage information for a single function.
type CoverageEntry struct {
	Function       string   `json:"function"`
	File           string   `json:"file"`
	Rating         string   `json:"rating"` // "★★★", "★★", "★", "GAP"
	TestFile       string   `json:"test_file,omitempty"`
	TestFunc       string   `json:"test_func,omitempty"`
	UncoveredPaths []string `json:"uncovered_paths,omitempty"`
	E2ETest        string   `json:"e2e_test,omitempty"`
	E2EGap         string   `json:"e2e_gap,omitempty"`
}

// CoverageResult holds the coverage map for all changed functions.
type CoverageResult struct {
	Entries []CoverageEntry `json:"entries"`
}

// RenderCoverageMap writes an ASCII coverage map table.
func RenderCoverageMap(w io.Writer, result CoverageResult) {
	if len(result.Entries) == 0 {
		_, _ = fmt.Fprint(w, "\n## Test Coverage Map\n\nNo changed functions found.\n")
		return
	}

	_, _ = fmt.Fprint(w, "\n## Test Coverage Map\n\n")
	_, _ = fmt.Fprintln(w, "| Function | File | Coverage | Test | Gaps |")
	_, _ = fmt.Fprintln(w, "|----------|------|----------|------|------|")

	tested := 0
	var e2eGaps []CoverageEntry
	for _, e := range result.Entries {
		testRef := emDashPlaceholder
		if e.TestFile != "" {
			if e.TestFunc != "" {
				testRef = fmt.Sprintf("%s:%s", e.TestFile, e.TestFunc)
			} else {
				testRef = e.TestFile
			}
		}

		gaps := emDashPlaceholder
		if len(e.UncoveredPaths) > 0 {
			gaps = strings.Join(e.UncoveredPaths, "; ")
		}

		_, _ = fmt.Fprintf(w, "| %s | %s | %s | %s | %s |\n",
			e.Function, e.File, e.Rating, testRef, gaps)

		if e.Rating != "GAP" {
			tested++
		}
		if e.E2EGap != "" {
			e2eGaps = append(e2eGaps, e)
		}
	}

	total := len(result.Entries)
	pct := 0
	if total > 0 {
		pct = (tested * 100) / total
	}
	_, _ = fmt.Fprintf(w, "\nCoverage: %d/%d functions tested (%d%%)\n", tested, total, pct)

	if len(e2eGaps) > 0 {
		_, _ = fmt.Fprint(w, "\n### E2E Test Gaps\n\n")
		for _, e := range e2eGaps {
			_, _ = fmt.Fprintf(w, "- **%s** (%s): %s\n", e.Function, e.File, e.E2EGap)
		}
	}
}
