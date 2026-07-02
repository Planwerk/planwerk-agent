package claude

import (
	"fmt"

	"github.com/planwerk/planwerk-agent/internal/report"
)

// CoverageMap runs a Claude call that analyzes test coverage of changed functions.
// baseBranch determines which branch to diff against (e.g. "main").
func (c *Client) CoverageMap(dir, baseBranch string) (*report.CoverageResult, error) {
	// The coverage map renders no attribution footer, so the resolved model is
	// not threaded out.
	text, _, err := c.runClaude(dir, buildCoveragePrompt(baseBranch), "coverage")
	if err != nil {
		return nil, err
	}

	return c.decodeCoverage(text)
}

// decodeCoverage decodes a coverage-map payload and fails loud when it carries
// no "entries" field. decodeJSONWithRepair recovers a JSON value from
// surrounding prose via extractJSONValue, which returns the FIRST balanced
// object in the output; if a model prepends an unrelated object (a status or
// summary blob) before the real payload, that object decodes cleanly into a
// CoverageResult whose Entries stay nil. CoverageResult has no required fields,
// so nothing else catches it and the pass would silently report empty coverage.
// A missing "entries" field is treated as a decode failure. An entries-present
// but empty payload ({"entries": []}, a diff with no changed functions)
// unmarshals to a non-nil empty slice and is a legitimate result.
func (c *Client) decodeCoverage(text string) (*report.CoverageResult, error) {
	var result report.CoverageResult
	if err := c.decodeJSONWithRepair(text, "coverage map", &result); err != nil {
		return nil, err
	}
	if result.Entries == nil {
		return nil, fmt.Errorf("parsing coverage map as JSON: output has no \"entries\" field\nraw output:\n%s", text)
	}
	return &result, nil
}

func buildCoveragePrompt(baseBranch string) string {
	if baseBranch == "" {
		baseBranch = DefaultBaseBranch
	}
	return fmt.Sprintf(`Analyze test coverage for every function and method that was changed in the current branch compared to origin/%s.

First run: git diff origin/%s --name-only
Then for each changed file, identify all functions/methods that were added or modified.

For each changed function, determine:
1. Is it directly tested? Name the test file and test function.
2. Is it indirectly tested (called by tested code)? Trace the call chain.
3. What code paths within the function are untested?

Additionally, check for E2E / integration test coverage:
4. Search for E2E test directories: chainsaw/, .chainsaw/, e2e/, tests/e2e/, test/e2e/
5. If the project uses Chainsaw tests (chainsaw-test.yaml with apiVersion: chainsaw.kyverno.io), check whether changed features/components have corresponding Chainsaw test scenarios.
6. If the project uses other E2E frameworks (kuttl, Helm chart tests), check for corresponding coverage.
7. For each changed feature or component, determine if there is an E2E test covering its behavior end-to-end.

Rate each function's test coverage:
- "★★★" = All significant paths tested directly, including error cases
- "★★"  = Main happy path tested, some edge cases or error paths missing
- "★"   = Only indirectly tested or only trivial assertion (e.g. "it doesn't panic")
- "GAP" = No test coverage found

`+jsonSchemaOnlyLine()+`

{
  "entries": [
    {
      "function": "functionName()",
      "file": "path/to/file.go",
      "rating": "★★★|★★|★|GAP",
      "test_file": "path/to/test_file.go",
      "test_func": "TestFunctionName",
      "uncovered_paths": ["description of untested path"],
      "e2e_test": "path/to/chainsaw-test.yaml or e2e test file if applicable",
      "e2e_gap": "description of missing E2E coverage, empty if covered or not applicable"
    }
  ]
}

Leave test_file and test_func empty for GAP entries.
Leave uncovered_paths empty for ★★★ entries.
Leave e2e_test empty if no E2E test exists or E2E is not applicable.
Leave e2e_gap empty if E2E coverage exists or the project has no E2E tests.
Include ALL changed functions, even trivial ones.

`, baseBranch, baseBranch) + outputLanguageBlock()
}
