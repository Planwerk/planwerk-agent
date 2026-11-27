package claude

import (
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/planwerk/planwerk-agent/internal/diagramdesign"
	"github.com/planwerk/planwerk-agent/internal/diffscope"
)

const (
	// maxDiagramLeads caps the checker reports one specialist prompt lists;
	// the block says how many more there were.
	maxDiagramLeads = 80
	// maxDiagramLeadMessage caps the characters of one report, which can
	// quote the markup it is about.
	maxDiagramLeadMessage = 240
	// diagramLeadsTag fences the checkers' reports as data.
	diagramLeadsTag = "diagram-leads"
)

// diagramGuidance returns the diagram section of the diagram specialist's
// prompt for the diff d in dir: the guidance block over the diagram-design
// skill the operator loaded, with its checkers' reports on the changed HTML
// diagrams the diff keeps. The checkers run from the checkout's top level,
// where the paths git lists start, so a run from a subdirectory checks the
// same files and its leads name them as the diff does.
// It returns "" when no diagram skill is loaded. A failure to list the
// changed files or to run a checker is logged, and the block then carries the
// guidance alone: the pass is never failed for a missing lead (decision 179).
func (c *Client) diagramGuidance(dir, baseBranch string, d diffscope.Diff) string {
	if !c.hasDiagramKit {
		return ""
	}
	kit := c.diagramKit
	top, changed, err := diffscope.Changed(dir, diffRevs(baseBranch, ""))
	if err != nil {
		slog.Warn("listing the changed files for the diagram checkers failed; the diagram specialist runs without their leads", "err", err)
		return diagramGuidanceBlock(kit.ReferenceDir(), kit.Guides(), kit.HasTypeReferences(), nil)
	}
	var html []string
	for _, f := range diffscope.Reviewable(changed, d.Excluded) {
		if isHTMLDiagram(f) {
			html = append(html, f)
		}
	}
	leads, err := kit.Check(top, html)
	if err != nil {
		slog.Warn("the diagram checkers failed; the diagram specialist runs without their leads", "err", err)
		leads = nil
	}
	slog.Info("diagram checkers", "html_diagrams", len(html), "leads", len(leads))
	return diagramGuidanceBlock(kit.ReferenceDir(), kit.Guides(), kit.HasTypeReferences(), leads)
}

// diagramGuidanceBlock renders the "## Diagram guidance" section: refDir,
// where the diagram-design skill's reference documents are, guides, the ones
// a review of a diagram reads that the installed version ships, and typeRefs,
// whether it ships the per-type references, then, when the checkers reported
// anything, the reports as leads fenced as data. The documents are pointed
// at, not copied, so the guidance is the installed version's (decision 179).
func diagramGuidanceBlock(refDir string, guides []diagramdesign.Guide, typeRefs bool, leads []diagramdesign.Finding) string {
	var sb strings.Builder
	sb.WriteString("## Diagram guidance\n\n")
	sb.WriteString("The operator loaded the diagram-design skill for this run. Its reference documents are in `" + refDir + "`, which this session may read.")
	for _, g := range guides {
		sb.WriteString(" `" + g.File + "` holds " + g.About + ".")
	}
	if typeRefs {
		sb.WriteString(" The type references, `type-<name>.md`, hold each diagram type's layout rules and anti-patterns.")
	}
	sb.WriteString(" Before you judge a changed diagram, read the sections of these documents that cover its type and what the change draws. Use them for what to check: they do not widen your domain, they hold only a diagram drawn with diagram-design to the skill's own rules, and their palette, typography, and accent rules are visual style, which your domain judges only where the project's documented design decisions rule on it. Their checklists, onboarding, export, and render-and-look steps do not apply in this pass, whose severity ladder, finding labels, and output this prompt sets.\n\n")
	if len(leads) == 0 {
		return sb.String()
	}

	sb.WriteString("### Checker leads\n\n")
	sb.WriteString("The skill's checkers ran over the changed HTML diagrams without a model and reported the problems below. `" + diagramdesign.SelfCheck + "` holds a figure to the accessible-SVG contract, the single-file rules, and its viewBox; `" + diagramdesign.VerifyGeometry + "` holds it to the connector rules by shape heuristics, which can misread a figure the skill did not draw. A lead is a checker's report, not a finding: check each against the file. Drop a lead where the file refutes it, where the project's documented design decisions sanction it, or where it holds a diagram the skill did not draw to the skill's own rules, unless it names a defect a reader meets anyway: a figure without a text alternative, content cut off at the viewBox, a label a node covers. Report every other lead the diff causes as a finding of this pass, at the severity the ladder gives it, with a Confidence label that says how far the file bears it out.\n\n")
	sb.WriteString(untrustedDataLine("It holds the checkers' reports, some of which quote the diagram's markup, shortened, for you to check against the file; quote a finding's code from the file itself.", diagramLeadsTag))
	shown := leads[:min(len(leads), maxDiagramLeads)]
	lines := make([]string, len(shown))
	for i, l := range shown {
		lines[i] = diagramLeadLine(l)
	}
	sb.WriteString(fencedData(diagramLeadsTag, "", strings.Join(lines, "\n")))
	if rest := len(leads) - len(shown); rest > 0 {
		fmt.Fprintf(&sb, "\n%d more leads are not listed.\n", rest)
	}
	sb.WriteString("\n")
	return sb.String()
}

// diagramLeadLine renders one checker report: its place, the checker, and the
// report on one line, cut to maxDiagramLeadMessage characters.
func diagramLeadLine(l diagramdesign.Finding) string {
	place := l.File
	if l.Line > 0 {
		place += ":" + strconv.Itoa(l.Line)
	}
	msg := strings.Join(strings.Fields(l.Message), " ")
	if utf8.RuneCountInString(msg) > maxDiagramLeadMessage {
		msg = string([]rune(msg)[:maxDiagramLeadMessage]) + "…"
	}
	return "- " + place + " — " + l.Check + ": " + msg
}
