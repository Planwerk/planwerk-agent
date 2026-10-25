package patterns

import (
	"bufio"
	"fmt"
	"regexp"
	"strings"
)

// Source references an external best-practice guide or document.
type Source struct {
	Title string
	URL   string // optional
}

type Pattern struct {
	Name          string
	ReviewArea    string
	DetectionHint string
	Severity      string
	Category      string   // "technology", "design-principle", "review", or "" (general/legacy)
	AppliesWhen   []string // technology tags; empty = always applies
	// AppliesToPaths are the globs (matchPathGlob) over the repo-relative
	// paths of the changed files that a pattern governs, from its
	// "**Applies-To-Paths**:" line. Only `review --scope-patterns` reads them
	// (ScopeToFiles); the repository-wide load ignores them (decision 141).
	AppliesToPaths []string
	Sources        []Source
	Body           string
	// FilePath is the absolute path the loader read this pattern from, when
	// known. Empty for patterns parsed from in-memory strings (tests, etc.).
	// Consumers like the bare-prompt URL builder use this to map a loaded
	// pattern back to its source directory and emit a remote URL instead of
	// inlining the body.
	FilePath string
}

// Parse parses a pattern from its markdown content.
// The format uses a simple frontmatter-like header with key-value pairs.
// Every line from the first "## " heading on is kept verbatim as Body, so an
// optional section such as "## Not a finding" reaches FormatForPrompt and the
// catalog files unchanged (decision 144).
func Parse(content string) (Pattern, error) {
	scanner := bufio.NewScanner(strings.NewReader(content))
	var p Pattern
	var bodyLines []string
	inBody := false

	for scanner.Scan() {
		line := scanner.Text()

		if !inBody {
			if strings.HasPrefix(line, "# Review Pattern:") {
				p.Name = strings.TrimSpace(strings.TrimPrefix(line, "# Review Pattern:"))
				continue
			}
			if strings.HasPrefix(line, "**Review-Area**:") {
				p.ReviewArea = extractValue(line, "**Review-Area**:")
				continue
			}
			if strings.HasPrefix(line, "**Detection-Hint**:") {
				p.DetectionHint = extractValue(line, "**Detection-Hint**:")
				continue
			}
			if strings.HasPrefix(line, "**Severity**:") {
				p.Severity = extractValue(line, "**Severity**:")
				continue
			}
			if strings.HasPrefix(line, "**Category**:") {
				// Normalize to lower case so the category grouping switch and
				// CountByCategory match regardless of how the markdown was
				// capitalized (e.g. "Review" or "REVIEW" must still bucket as
				// the canonical lowercase "review").
				p.Category = strings.ToLower(extractValue(line, "**Category**:"))
				continue
			}
			if strings.HasPrefix(line, "**Applies-When**:") {
				raw := extractValue(line, "**Applies-When**:")
				for _, tag := range strings.Split(raw, ",") {
					tag = strings.TrimSpace(tag)
					if tag != "" {
						p.AppliesWhen = append(p.AppliesWhen, tag)
					}
				}
				continue
			}
			if strings.HasPrefix(line, "**Applies-To-Paths**:") {
				raw := extractValue(line, "**Applies-To-Paths**:")
				for _, glob := range strings.Split(raw, ",") {
					glob = strings.TrimSpace(glob)
					if glob != "" {
						p.AppliesToPaths = append(p.AppliesToPaths, glob)
					}
				}
				continue
			}
			if strings.HasPrefix(line, "**Sources**:") {
				p.Sources = parseSources(extractValue(line, "**Sources**:"))
				continue
			}
			if strings.HasPrefix(line, "## ") {
				inBody = true
				bodyLines = append(bodyLines, line)
				continue
			}
		} else {
			bodyLines = append(bodyLines, line)
		}
	}

	p.Body = strings.TrimSpace(strings.Join(bodyLines, "\n"))

	if p.Name == "" {
		return p, fmt.Errorf("pattern has no name (missing '# Review Pattern: ...' header)")
	}

	return p, nil
}

func extractValue(line, prefix string) string {
	return strings.TrimSpace(strings.TrimPrefix(line, prefix))
}

// sourceRe matches "Title (URL)" entries.
var sourceRe = regexp.MustCompile(`^(.+?)\s*\((\S+?)\)\s*$`)

// parseSources parses a comma-separated list of "Title (URL)" or plain "Title" entries.
func parseSources(raw string) []Source {
	var sources []Source
	for _, entry := range strings.Split(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if m := sourceRe.FindStringSubmatch(entry); m != nil {
			sources = append(sources, Source{Title: strings.TrimSpace(m[1]), URL: m[2]})
		} else {
			sources = append(sources, Source{Title: entry})
		}
	}
	return sources
}

// AppliesTo returns true if the pattern should be included given the detected technology tags.
// Patterns with no AppliesWhen restriction always apply (backward compatible).
func (p Pattern) AppliesTo(tags []string) bool {
	if len(p.AppliesWhen) == 0 {
		return true
	}
	tagSet := make(map[string]bool, len(tags))
	for _, t := range tags {
		tagSet[t] = true
	}
	for _, required := range p.AppliesWhen {
		if tagSet[required] {
			return true
		}
	}
	return false
}

// AppliesToEveryFile reports whether the pattern names neither a technology
// nor a path, so that ScopeToFiles keeps its body for every changed file.
func (p Pattern) AppliesToEveryFile() bool {
	return len(p.AppliesWhen) == 0 && len(p.AppliesToPaths) == 0
}

// MatchesFile reports whether the pattern governs the changed file at the
// repo-relative slash path name, whose technology tags are tags
// (detect.FileTechnologies): one of its AppliesWhen tags is among tags, as
// AppliesTo decides, and, when it has AppliesToPaths, one of them matches
// name (decision 141).
func (p Pattern) MatchesFile(name string, tags []string) bool {
	if !p.AppliesTo(tags) {
		return false
	}
	if len(p.AppliesToPaths) == 0 {
		return true
	}
	for _, glob := range p.AppliesToPaths {
		if matchPathGlob(glob, name) {
			return true
		}
	}
	return false
}

// FormatForPrompt returns a concise representation of the pattern suitable for inclusion in a prompt.
func (p Pattern) FormatForPrompt() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "### Pattern: %s\n", p.Name)
	if p.Category != "" {
		fmt.Fprintf(&sb, "- Category: %s\n", p.Category)
	}
	fmt.Fprintf(&sb, "- Area: %s\n", p.ReviewArea)
	fmt.Fprintf(&sb, "- Detection: %s\n", p.DetectionHint)
	fmt.Fprintf(&sb, "- Severity: %s\n", p.Severity)
	if len(p.Sources) > 0 {
		fmt.Fprintf(&sb, "- Sources: %s\n", formatSourceTitles(p.Sources))
	}
	sb.WriteString(p.Body)
	sb.WriteString("\n")
	return sb.String()
}

// formatSourceTitles returns a comma-separated list of source titles (URLs omitted for prompt brevity).
func formatSourceTitles(sources []Source) string {
	titles := make([]string, len(sources))
	for i, s := range sources {
		titles[i] = s.Title
	}
	return strings.Join(titles, ", ")
}
