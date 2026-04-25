package patterns

import (
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// DefaultMaxPatternsInPrompt is the default cap on patterns injected into the
// prompt when no explicit limit is configured. It can be overridden via the
// --max-patterns flag or PLANWERK_MAX_PATTERNS environment variable.
// A value <= 0 disables truncation.
const DefaultMaxPatternsInPrompt = 50

// severityOrder maps severity strings to priority (lower = higher priority for truncation).
var severityOrder = map[string]int{
	"BLOCKING": 0,
	"CRITICAL": 1,
	"WARNING":  2,
	"INFO":     3,
}

// Load reads all .md files recursively from the given sources and parses them
// as patterns. Sources are processed in order; later sources have higher
// priority (repo-specific patterns override general patterns with the same
// name). All patterns are returned regardless of technology tags. Each source
// may be a local directory path or a remote URI accepted by IsRemote.
func Load(sources ...string) ([]Pattern, error) {
	return LoadFiltered(nil, sources...)
}

// LoadFiltered reads patterns from the given sources recursively and
// returns only those that apply to the detected technology tags.
// If tags is nil or empty, all patterns are returned (backward compatible).
// Remote sources are resolved using the package-level remote options
// configured by SetRemoteOptions; for explicit per-call options use
// LoadFilteredWithOptions.
func LoadFiltered(tags []string, sources ...string) ([]Pattern, error) {
	return LoadFilteredWithOptions(LoadOptions{Remote: remoteOpts}, tags, sources...)
}

// LoadOptions bundles tunables that influence pattern loading.
type LoadOptions struct {
	// Remote controls how remote pattern URIs (see IsRemote) are resolved
	// into local directories. The zero value uses sensible defaults.
	Remote RemoteOptions
}

// LoadFilteredWithOptions is the explicit-options variant of LoadFiltered.
// It resolves remote sources via opts.Remote (instead of the package-level
// configuration) so callers — chiefly tests — can isolate themselves from
// any global state.
func LoadFilteredWithOptions(opts LoadOptions, tags []string, sources ...string) ([]Pattern, error) {
	dirs, err := resolveSources(opts.Remote, sources)
	if err != nil {
		return nil, err
	}

	seen := make(map[string]int) // pattern name -> index in result
	var result []Pattern

	for _, dir := range dirs {
		patterns, err := loadDir(dir)
		if err != nil {
			return nil, err
		}

		for _, p := range patterns {
			if idx, ok := seen[p.Name]; ok {
				result[idx] = p // override with higher-priority pattern
			} else {
				seen[p.Name] = len(result)
				result = append(result, p)
			}
		}
	}

	// Filter by technology tags
	if len(tags) > 0 {
		filtered := result[:0]
		for _, p := range result {
			if p.AppliesTo(tags) {
				filtered = append(filtered, p)
			}
		}
		result = filtered
	}

	return result, nil
}

// resolveSources turns each entry into a local directory path: remote URIs
// are materialized into the cache via ResolveRemote, local paths pass
// through unchanged.
func resolveSources(opts RemoteOptions, sources []string) ([]string, error) {
	dirs := make([]string, 0, len(sources))
	for _, src := range sources {
		if IsRemote(src) {
			d, err := ResolveRemote(src, opts)
			if err != nil {
				return nil, fmt.Errorf("resolving remote pattern source %q: %w", src, err)
			}
			dirs = append(dirs, d)
			continue
		}
		dirs = append(dirs, src)
	}
	return dirs, nil
}

// loadDir recursively reads all .md files from a directory and parses them as patterns.
func loadDir(dir string) ([]Pattern, error) {
	info, err := os.Stat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading pattern directory %s: %w", dir, err)
	}
	if !info.IsDir() {
		return nil, nil
	}

	var result []Pattern

	err = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable entries
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}
		// Skip SOURCES.md (documentation catalog, not a pattern)
		if strings.EqualFold(d.Name(), "SOURCES.md") {
			return nil
		}

		content, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("reading pattern %s: %w", path, err)
		}

		p, err := Parse(string(content))
		if err != nil {
			// Skip files that don't parse as patterns (e.g. README.md)
			return nil
		}

		result = append(result, p)
		return nil
	})
	if err != nil {
		return nil, err
	}

	return result, nil
}

// FormatAllForPrompt concatenates all patterns into a single string for prompt inclusion.
func FormatAllForPrompt(patterns []Pattern) string {
	if len(patterns) == 0 {
		return ""
	}
	var sb strings.Builder
	for _, p := range patterns {
		sb.WriteString(p.FormatForPrompt())
		sb.WriteString("\n")
	}
	return sb.String()
}

// FormatGroupedForPrompt groups patterns by category and formats them with XML tags
// for structured prompt injection. If maxPatterns > 0, patterns are truncated
// to that limit, prioritizing by severity. A value <= 0 disables truncation.
func FormatGroupedForPrompt(pats []Pattern, maxPatterns int) string {
	if len(pats) == 0 {
		return ""
	}

	// Apply prompt budget via truncation
	pats = truncatePatterns(pats, maxPatterns)

	// Group by category
	var technology, design, general []Pattern
	for _, p := range pats {
		switch p.Category {
		case "technology":
			technology = append(technology, p)
		case "design-principle":
			design = append(design, p)
		default:
			general = append(general, p)
		}
	}

	var sb strings.Builder

	if len(technology) > 0 {
		sb.WriteString("<technology-patterns>\n")
		for _, p := range technology {
			sb.WriteString(p.FormatForPrompt())
			sb.WriteString("\n")
		}
		sb.WriteString("</technology-patterns>\n\n")
	}

	if len(design) > 0 {
		sb.WriteString("<design-patterns>\n")
		for _, p := range design {
			sb.WriteString(p.FormatForPrompt())
			sb.WriteString("\n")
		}
		sb.WriteString("</design-patterns>\n\n")
	}

	if len(general) > 0 {
		sb.WriteString("<project-patterns>\n")
		for _, p := range general {
			sb.WriteString(p.FormatForPrompt())
			sb.WriteString("\n")
		}
		sb.WriteString("</project-patterns>\n\n")
	}

	return sb.String()
}

// truncatePatterns limits the number of patterns to maxPatterns,
// prioritizing by severity (BLOCKING > CRITICAL > WARNING > INFO).
// A maxPatterns value <= 0 disables truncation and returns pats unchanged.
func truncatePatterns(pats []Pattern, maxPatterns int) []Pattern {
	if maxPatterns <= 0 || len(pats) <= maxPatterns {
		return pats
	}

	// Stable sort by severity priority
	type indexed struct {
		pattern Pattern
		order   int
	}
	items := make([]indexed, len(pats))
	for i, p := range pats {
		ord, ok := severityOrder[p.Severity]
		if !ok {
			ord = 99 // unknown severity goes last
		}
		items[i] = indexed{pattern: p, order: ord}
	}

	// Simple stable selection: pick the first maxPatterns by severity
	// We do a simple multi-pass to preserve original order within same severity
	var result []Pattern
	for targetOrd := 0; targetOrd <= 99 && len(result) < maxPatterns; targetOrd++ {
		for _, item := range items {
			if item.order == targetOrd {
				result = append(result, item.pattern)
				if len(result) >= maxPatterns {
					break
				}
			}
		}
	}

	slog.Warn("patterns truncated for prompt budget", "loaded", len(pats), "kept", maxPatterns)
	return result
}
