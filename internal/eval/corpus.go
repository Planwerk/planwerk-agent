// Package eval is a dev-only output-quality harness for the review pipeline. It
// materializes a labeled corpus of seeded-bug cases into throwaway git repos,
// runs the shipped review pipeline against each, and scores the findings for
// precision, recall, and severity accuracy. It never runs in unit CI — RunCase
// invokes the real claude CLI and spends tokens; only the loader and scorer are
// unit-tested.
//
// # Corpus storage
//
// A case's source trees live under internal/eval/corpus/<case>/{base,head}/ as
// files with a .go.txt suffix rather than .go. The suffix keeps the Go
// toolchain from treating those directories as build inputs: a directory with
// no .go files is not a package, so `go build ./internal/eval/...` and
// `go vet ./internal/eval/...` ignore the corpus entirely and only compile the
// eval package itself. materialize (see harness.go) strips the .txt on copy, so
// the trees land in the throwaway repo as real .go files. Any file NOT ending in
// .go.txt (e.g. expected.json) is copied verbatim.
package eval

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// expectedFileName is the per-case label file loaded from each case directory.
const expectedFileName = "expected.json"

// ExpectedFinding describes one seeded bug the review pipeline is expected to
// report. A predicted finding matches it under the rules in score.go: same file,
// line within tolerance, and at least one keyword present in the predicted text.
type ExpectedFinding struct {
	File     string   `json:"file"`
	Line     int      `json:"line"`
	Severity string   `json:"severity"`
	Keywords []string `json:"keywords"`
}

// Expected is the parsed expected.json for a corpus case. A clean case seeds no
// bug and carries no expected findings — it measures false positives, and its
// recall is undefined.
type Expected struct {
	Description string            `json:"description"`
	Clean       bool              `json:"clean"`
	Findings    []ExpectedFinding `json:"findings"`
}

// Case is a loaded, validated corpus case. Dir is the absolute path to the case
// directory (which holds base/, head/, and expected.json).
type Case struct {
	Name     string
	Dir      string
	Expected Expected
}

// LoadCorpus loads and validates every case directory directly under root,
// returning them sorted by name. It fails on the first invalid case so a broken
// corpus surfaces loudly rather than skewing the scores.
func LoadCorpus(root string) ([]Case, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("reading corpus dir %s: %w", root, err)
	}
	var cases []Case
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		c, err := LoadCase(filepath.Join(root, e.Name()))
		if err != nil {
			return nil, err
		}
		cases = append(cases, c)
	}
	if len(cases) == 0 {
		return nil, fmt.Errorf("no cases found in corpus dir %s", root)
	}
	sort.Slice(cases, func(i, j int) bool { return cases[i].Name < cases[j].Name })
	return cases, nil
}

// LoadCase loads and validates a single case directory. It requires base/ and
// head/ subdirectories and a well-formed expected.json; a non-clean case must
// declare at least one expected finding, and every expected finding must name a
// file and carry at least one keyword to match against.
func LoadCase(dir string) (Case, error) {
	name := filepath.Base(dir)

	for _, sub := range []string{"base", "head"} {
		info, err := os.Stat(filepath.Join(dir, sub))
		if err != nil {
			return Case{}, fmt.Errorf("case %s: missing %s/ tree: %w", name, sub, err)
		}
		if !info.IsDir() {
			return Case{}, fmt.Errorf("case %s: %s must be a directory", name, sub)
		}
	}

	data, err := os.ReadFile(filepath.Join(dir, expectedFileName))
	if err != nil {
		return Case{}, fmt.Errorf("case %s: reading %s: %w", name, expectedFileName, err)
	}
	var exp Expected
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&exp); err != nil {
		return Case{}, fmt.Errorf("case %s: parsing %s: %w", name, expectedFileName, err)
	}

	if err := validateExpected(name, exp); err != nil {
		return Case{}, err
	}
	return Case{Name: name, Dir: dir, Expected: exp}, nil
}

// validateExpected enforces the corpus invariants: a non-clean case names at
// least one bug, a clean case names none, and every expected finding is anchored
// to a file with at least one keyword.
func validateExpected(name string, exp Expected) error {
	if exp.Clean && len(exp.Findings) > 0 {
		return fmt.Errorf("case %s: clean case must declare no expected findings, got %d", name, len(exp.Findings))
	}
	if !exp.Clean && len(exp.Findings) == 0 {
		return fmt.Errorf("case %s: non-clean case must declare at least one expected finding", name)
	}
	for i, f := range exp.Findings {
		if f.File == "" {
			return fmt.Errorf("case %s: expected finding %d has no file", name, i)
		}
		if len(f.Keywords) == 0 {
			return fmt.Errorf("case %s: expected finding %d (%s) has no keywords to match", name, i, f.File)
		}
	}
	return nil
}
