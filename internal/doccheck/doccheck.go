// Package doccheck detects documentation files that may need updating
// based on code changes in a PR.
package doccheck

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// StaleDocHint describes a documentation file that may need updating.
type StaleDocHint struct {
	DocFile     string
	RelatedDirs []string
}

// Check finds documentation files in the repo root that reference code directories
// which were modified in the PR. Returns nil if no stale docs are detected.
func Check(repoDir, baseBranch string) []StaleDocHint {
	if repoDir == "" || baseBranch == "" {
		return nil
	}

	changedDirs := getChangedDirs(repoDir, baseBranch)
	if len(changedDirs) == 0 {
		return nil
	}

	docFiles := findDocFiles(repoDir)
	if len(docFiles) == 0 {
		return nil
	}

	var hints []StaleDocHint
	for _, docFile := range docFiles {
		content, err := os.ReadFile(filepath.Join(repoDir, docFile))
		if err != nil {
			continue
		}
		docContent := string(content)

		var relatedDirs []string
		for _, dir := range changedDirs {
			if strings.Contains(docContent, dir) {
				relatedDirs = append(relatedDirs, dir)
			}
		}
		if len(relatedDirs) > 0 {
			hints = append(hints, StaleDocHint{
				DocFile:     docFile,
				RelatedDirs: relatedDirs,
			})
		}
	}

	return hints
}

// getChangedDirs returns unique directory paths of files changed in the PR.
// A directory holding a byte above 0x7F is returned as stored (decision 131).
func getChangedDirs(repoDir, baseBranch string) []string {
	cmd := exec.Command("git", "-c", "core.quotePath=false", "diff", "--name-only", "origin/"+baseBranch+"...HEAD")
	cmd.Dir = repoDir
	out, err := cmd.Output()
	if err != nil {
		return nil
	}

	seen := make(map[string]bool)
	var dirs []string
	for _, line := range strings.Split(string(out), "\n") {
		if line == "" {
			continue
		}
		dir := filepath.Dir(line)
		if dir == "." {
			continue
		}
		// Add the directory and its parents
		for dir != "." && dir != "" {
			if !seen[dir] {
				seen[dir] = true
				dirs = append(dirs, dir)
			}
			dir = filepath.Dir(dir)
		}
	}
	return dirs
}

// NewFeatureHint describes a new file added in the PR that may need documentation.
type NewFeatureHint struct {
	File        string
	Description string
}

// CheckNewFeatures detects new source files added in the PR that may need
// documentation. Test files and internal configuration are excluded.
// Returns nil if no new features are detected or on error (non-fatal).
func CheckNewFeatures(repoDir, baseBranch string) []NewFeatureHint {
	if repoDir == "" || baseBranch == "" {
		return nil
	}

	newFiles := getNewFiles(repoDir, baseBranch)
	if len(newFiles) == 0 {
		return nil
	}

	var hints []NewFeatureHint
	for _, f := range newFiles {
		if isTestFile(f) || isInternalConfig(f) {
			continue
		}
		hints = append(hints, NewFeatureHint{
			File:        f,
			Description: "new file added",
		})
	}
	return hints
}

// getNewFiles returns files that were added (not just modified) in the PR.
// A path holding a byte above 0x7F is returned as stored (decision 131).
func getNewFiles(repoDir, baseBranch string) []string {
	cmd := exec.Command("git", "-c", "core.quotePath=false", "diff", "--name-only", "--diff-filter=A", "origin/"+baseBranch+"...HEAD")
	cmd.Dir = repoDir
	out, err := cmd.Output()
	if err != nil {
		return nil
	}

	var files []string
	for _, line := range strings.Split(string(out), "\n") {
		if line != "" {
			files = append(files, line)
		}
	}
	return files
}

// isTestFile returns true if the file path matches common test file naming conventions.
func isTestFile(path string) bool {
	base := filepath.Base(path)

	// Go: _test.go
	if strings.HasSuffix(base, "_test.go") {
		return true
	}
	// Python: test_*.py or *_test.py
	if strings.HasSuffix(base, ".py") && (strings.HasPrefix(base, "test_") || strings.HasSuffix(base, "_test.py")) {
		return true
	}
	// JS/TS: *.test.ts, *.test.js, *.spec.ts, *.spec.js
	for _, suffix := range []string{".test.ts", ".test.js", ".test.tsx", ".test.jsx", ".spec.ts", ".spec.js", ".spec.tsx", ".spec.jsx"} {
		if strings.HasSuffix(base, suffix) {
			return true
		}
	}
	// __tests__ directory
	if strings.Contains(path, "__tests__") {
		return true
	}
	// E2E / Chainsaw / infrastructure test directories
	for _, dir := range []string{"/chainsaw/", "/e2e/", "/tests/e2e/", "/test/e2e/", "/.chainsaw/"} {
		if strings.Contains("/"+path, dir) {
			return true
		}
	}
	// Chainsaw test manifest files (chainsaw-test.yaml, chainsaw-test.yml)
	if base == "chainsaw-test.yaml" || base == "chainsaw-test.yml" {
		return true
	}
	return false
}

// isInternalConfig returns true if the file is internal configuration that
// typically does not need user-facing documentation.
func isInternalConfig(path string) bool {
	parts := strings.SplitN(path, "/", 2)
	first := parts[0]

	// Dotfiles and dot-directories
	if strings.HasPrefix(first, ".") {
		return true
	}
	return false
}

// findDocFiles returns markdown files in the repo root directory.
func findDocFiles(repoDir string) []string {
	entries, err := os.ReadDir(repoDir)
	if err != nil {
		return nil
	}

	var docs []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasSuffix(strings.ToLower(name), ".md") {
			docs = append(docs, name)
		}
	}
	return docs
}
