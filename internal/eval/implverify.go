package eval

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/planwerk/planwerk-agent/internal/claude"
	"github.com/planwerk/planwerk-agent/internal/hygiene"
	"github.com/planwerk/planwerk-agent/internal/report"
)

// The files an implementation verifier case adds to the corpus layout: the
// issue the verifier checks the head against, which opens with a
// "# <title>" line, and the implementation plan, which may be empty.
const (
	implVerifyIssueFile = "issue.md"
	implVerifyPlanFile  = "plan.md"
)

// implVerifyPass is the provenance label RunImplementVerifierCase stamps on the
// verifier's findings, so the recall by pass names the verifier. It is the
// label the verifier's session runs under, which the usage rows show.
const implVerifyPass = "verify-implementation"

// readImplementVerifierSpec reads the issue and the plan of an implementation
// verifier case from c.Dir. The first line of issue.md after "# " is the title
// and the rest, trimmed, is the body. plan.md, trimmed, is the plan; an empty
// one yields "", and the verifier then checks the issue alone.
func readImplementVerifierSpec(c Case) (title, body, plan string, err error) {
	read := func(name string) (string, error) {
		data, err := os.ReadFile(filepath.Join(c.Dir, name))
		if errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("case %s: %s is missing", c.Name, name)
		}
		if err != nil {
			return "", fmt.Errorf("case %s: reading %s: %w", c.Name, name, err)
		}
		return string(data), nil
	}
	issue, err := read(implVerifyIssueFile)
	if err != nil {
		return "", "", "", err
	}
	planText, err := read(implVerifyPlanFile)
	if err != nil {
		return "", "", "", err
	}
	rest, ok := strings.CutPrefix(issue, "# ")
	if !ok {
		return "", "", "", fmt.Errorf("case %s: %s must open with a \"# <title>\" line", c.Name, implVerifyIssueFile)
	}
	title, body, _ = strings.Cut(rest, "\n")
	return strings.TrimSpace(title), strings.TrimSpace(body), strings.TrimSpace(planText), nil
}

// LoadImplementVerifierCases loads the implementation verifier cases under
// root, or only the one caseName names, and reads every case's issue and plan,
// so a bad case fails before any run spends tokens.
func LoadImplementVerifierCases(root, caseName string) ([]Case, error) {
	var cases []Case
	if caseName != "" {
		c, err := LoadCase(filepath.Join(root, caseName))
		if err != nil {
			return nil, err
		}
		cases = []Case{c}
	} else {
		var err error
		if cases, err = LoadCorpus(root); err != nil {
			return nil, err
		}
	}
	for _, c := range cases {
		if _, _, _, err := readImplementVerifierSpec(c); err != nil {
			return nil, err
		}
	}
	return cases, nil
}

// RunImplementVerifierCase builds c's repository in a throwaway directory
// (setupRepo), runs the implementation verifier `implement` runs over the
// head's diff, and returns its findings, stamped with implVerifyPass, with the
// run's usage. The verifier receives the case's issue and plan, as `implement`
// hands it the issue and the plan it followed. A nil result counts as an empty
// one. Like RunCase it spends tokens, needs a fresh client, and is not
// unit-tested.
func RunImplementVerifierCase(client *claude.Client, c Case) (Run, error) {
	title, body, plan, err := readImplementVerifierSpec(c)
	if err != nil {
		return Run{}, err
	}
	repoDir, err := os.MkdirTemp("", "planwerk-eval-"+c.Name+"-*")
	if err != nil {
		return Run{}, fmt.Errorf("case %s: temp repo: %w", c.Name, err)
	}
	defer func() { _ = os.RemoveAll(repoDir) }()

	if _, err := setupRepo(repoDir, c); err != nil {
		return Run{}, fmt.Errorf("case %s: %w", c.Name, err)
	}
	result, err := client.VerifyImplementation(repoDir, title, body, plan, evalBaseBranch)
	if err != nil {
		return Run{}, fmt.Errorf("case %s: implementation verifier: %w", c.Name, err)
	}
	if result == nil {
		result = &report.ReviewResult{}
	}
	hygiene.TagPass(result, implVerifyPass)
	return Run{Result: *result, Usage: client.UsageTotals()}, nil
}
