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
// prompt when no explicit limit is configured. The default is 0, meaning all
// loaded patterns are injected without truncation. Override via the
// --max-patterns flag or PLANWERK_MAX_PATTERNS environment variable.
// A value <= 0 disables truncation.
const DefaultMaxPatternsInPrompt = 0

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

		// Record the absolute on-disk path so downstream consumers can
		// classify the pattern by source dir (bundled / repo / explicit).
		// Falling back to the as-walked path keeps the field non-empty even
		// if Abs fails, which is good enough for prefix-based matching.
		if abs, err := filepath.Abs(path); err == nil {
			p.FilePath = abs
		} else {
			p.FilePath = path
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

// CatalogReference is a single entry in a bare-prompt pattern catalog. It
// pairs the pattern's identity (name, severity, area, technology hints)
// with a way for a remote Claude session to acquire its body — either a
// public URL (for patterns shipped in this repo) or a path inside the
// session's own checkout (for project-specific patterns).
type CatalogReference struct {
	Name          string
	Severity      string
	Category      string
	ReviewArea    string
	AppliesWhen   []string
	URL           string // public URL (raw markdown); empty when LocalPath is set
	LocalPath     string // path within the session's checkout; empty when URL is set
	OriginNote    string // free-form note shown next to the entry (e.g. "user-supplied via --patterns")
}

// CatalogRefOptions configures BuildCatalogReferences. The orchestrator
// passes in the directory roots it knows about (bundled local catalog,
// target-repo overrides) so the helper can map each pattern's FilePath
// back to either a remote URL or an in-checkout path. Both root paths
// should be absolute (BuildCatalogReferences will Abs them defensively).
type CatalogRefOptions struct {
	// BundledRoot is the on-disk directory the planwerk-review-shipped
	// pattern catalog was loaded from. Patterns whose FilePath sits under
	// this directory are emitted as URLs against BundledURLBase.
	BundledRoot string
	// BundledURLBase is the URL prefix patterns under BundledRoot map to.
	// E.g. https://raw.githubusercontent.com/planwerk/planwerk-review/main/patterns
	// (no trailing slash). Required iff BundledRoot is non-empty.
	BundledURLBase string
	// RepoRoot is the target repository's .planwerk/review_patterns/
	// directory. Patterns under this dir are emitted as relative paths the
	// pasted-into Claude session can read directly from its own checkout.
	RepoRoot string
	// RepoRelBase is the path prefix to print for RepoRoot patterns
	// (typically ".planwerk/review_patterns"). Required iff RepoRoot is
	// non-empty.
	RepoRelBase string
}

// BuildCatalogReferences classifies each pattern by file-path prefix
// against the source roots in opts and returns a catalog suitable for
// embedding in a bare prompt. Patterns whose FilePath does not match any
// known root land in the slice with both URL and LocalPath empty and an
// OriginNote describing the situation — the prompt builder can then
// either skip them or list them by name as a "you'll have to load this
// yourself" footnote.
func BuildCatalogReferences(pats []Pattern, opts CatalogRefOptions) []CatalogReference {
	bundled := absOrEmpty(opts.BundledRoot)
	repo := absOrEmpty(opts.RepoRoot)

	refs := make([]CatalogReference, 0, len(pats))
	for _, p := range pats {
		ref := CatalogReference{
			Name:        p.Name,
			Severity:    p.Severity,
			Category:    p.Category,
			ReviewArea:  p.ReviewArea,
			AppliesWhen: append([]string(nil), p.AppliesWhen...),
		}
		path := p.FilePath
		if abs, err := filepath.Abs(path); err == nil {
			path = abs
		}

		switch {
		case bundled != "" && opts.BundledURLBase != "" && hasDirPrefix(path, bundled):
			rel, err := filepath.Rel(bundled, path)
			if err == nil {
				ref.URL = strings.TrimRight(opts.BundledURLBase, "/") + "/" + filepath.ToSlash(rel)
			}
		case repo != "" && opts.RepoRelBase != "" && hasDirPrefix(path, repo):
			rel, err := filepath.Rel(repo, path)
			if err == nil {
				ref.LocalPath = strings.TrimRight(opts.RepoRelBase, "/") + "/" + filepath.ToSlash(rel)
			}
		default:
			ref.OriginNote = "user-supplied via --patterns; load it yourself if needed"
		}
		refs = append(refs, ref)
	}
	return refs
}

// FormatCatalogReferences renders the catalog produced by
// BuildCatalogReferences as a markdown bullet list suitable for embedding
// in a bare prompt. The result is empty (zero-length string) when refs is
// empty so the caller can branch on `if block != ""`.
func FormatCatalogReferences(refs []CatalogReference) string {
	if len(refs) == 0 {
		return ""
	}
	var sb strings.Builder
	for _, r := range refs {
		sb.WriteString("- **")
		sb.WriteString(r.Name)
		sb.WriteString("**")
		if r.Severity != "" {
			sb.WriteString(" (")
			sb.WriteString(r.Severity)
			sb.WriteString(")")
		}
		switch {
		case r.URL != "":
			sb.WriteString(" — fetch ")
			sb.WriteString(r.URL)
		case r.LocalPath != "":
			sb.WriteString(" — read `")
			sb.WriteString(r.LocalPath)
			sb.WriteString("` from your checkout")
		case r.OriginNote != "":
			sb.WriteString(" — ")
			sb.WriteString(r.OriginNote)
		}
		if r.Category != "" || len(r.AppliesWhen) > 0 || r.ReviewArea != "" {
			sb.WriteString("  \n  ")
			var meta []string
			if r.Category != "" {
				meta = append(meta, "category="+r.Category)
			}
			if r.ReviewArea != "" {
				meta = append(meta, "area="+r.ReviewArea)
			}
			if len(r.AppliesWhen) > 0 {
				meta = append(meta, "applies-when="+strings.Join(r.AppliesWhen, ","))
			}
			sb.WriteString(strings.Join(meta, "; "))
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

func absOrEmpty(p string) string {
	if p == "" {
		return ""
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	return abs
}

// hasDirPrefix reports whether path lives under dir, treating both as
// directory paths so a sibling like "/dir2/x" does not match "/dir".
func hasDirPrefix(path, dir string) bool {
	if dir == "" {
		return false
	}
	dir = strings.TrimRight(dir, string(filepath.Separator))
	return path == dir || strings.HasPrefix(path, dir+string(filepath.Separator))
}
