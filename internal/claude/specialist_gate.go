package claude

import (
	"path/filepath"
	"strings"
)

// ShouldRun reports whether the specialist is worth running given the set of
// files changed in the PR diff (repo-relative paths). It is the heart of
// adaptive gating: a specialist whose relevant paths the diff never touches is
// skipped, cutting wall-clock and cost on small PRs.
//
// NeverGate specialists (security, data-migration) always run. An empty
// changedFiles slice means the diff is unknown — the gate fails open and runs
// the specialist rather than risk skipping a real finding on a missing signal.
func (s Specialist) ShouldRun(changedFiles []string) bool {
	if s.NeverGate {
		return true
	}
	if len(changedFiles) == 0 {
		return true
	}
	relevant := isSourceFile
	switch s.Relevance {
	case RelevanceRoutes:
		relevant = isRouteFile
	case RelevanceUI:
		relevant = isUIFile
	case RelevanceDiagram:
		relevant = isDiagramFile
	}
	for _, f := range changedFiles {
		if relevant(f) {
			return true
		}
	}
	return false
}

// String names the kind of changed file that makes a gateable specialist of
// this Relevance run: "route or handler file" for RelevanceRoutes, "UI file"
// for RelevanceUI, "diagram file" for RelevanceDiagram, "source file"
// otherwise, the same split ShouldRun makes.
// review --dry-run prints it after "the diff changes no " for a specialist the
// gate skips (decision 143).
func (r Relevance) String() string {
	switch r {
	case RelevanceRoutes:
		return "route or handler file"
	case RelevanceUI:
		return "UI file"
	case RelevanceDiagram:
		return "diagram file"
	}
	return "source file"
}

// isSourceFile reports whether a repo-relative path is a source-code file, as
// opposed to documentation, configuration, data, or media. The "any source"
// specialists (testing, performance, maintainability) are relevant only when
// the diff changes code, so a docs- or config-only PR skips them.
//
// Classification is by extension using a denylist of clearly non-source types;
// anything else — including unknown extensions and extension-less files such as
// Makefile or Dockerfile — counts as source. The gate therefore errs toward
// running a specialist rather than silently skipping a code change it could not
// classify.
func isSourceFile(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case
		// Documentation
		".md", ".markdown", ".mdx", ".rst", ".adoc", ".asciidoc", ".txt",
		// Configuration and data
		".json", ".yaml", ".yml", ".toml", ".ini", ".cfg", ".conf",
		".xml", ".properties", ".env", ".csv", ".tsv", ".lock",
		// Media and binaries
		".png", ".jpg", ".jpeg", ".gif", ".svg", ".ico", ".webp", ".pdf":
		return false
	}
	return true
}

// routeDirSegments are the directory names that, when present anywhere in a
// changed file's path, mark it as part of the routing / request-handler layer
// the api-contract specialist reviews.
var routeDirSegments = map[string]bool{
	"api":         true,
	"apis":        true,
	"route":       true,
	"routes":      true,
	"router":      true,
	"routers":     true,
	"handler":     true,
	"handlers":    true,
	"controller":  true,
	"controllers": true,
	"endpoint":    true,
	"endpoints":   true,
}

// isRouteFile reports whether a repo-relative path belongs to a routing or
// request-handler layer: a directory segment is one of routeDirSegments, or the
// file's base name (sans extension) names a router, handler, or controller.
// This gates the api-contract specialist to PRs that touch the API surface.
func isRouteFile(path string) bool {
	segs := strings.Split(strings.ToLower(filepath.ToSlash(path)), "/")
	for _, seg := range segs {
		if routeDirSegments[seg] {
			return true
		}
	}
	base := segs[len(segs)-1]
	if dot := strings.LastIndex(base, "."); dot > 0 {
		base = base[:dot]
	}
	return strings.Contains(base, "handler") ||
		strings.Contains(base, "route") ||
		strings.Contains(base, "controller")
}

// uiExtensions are the file extensions that mark a changed file as part of a
// user interface: markup, stylesheets, the component formats of the web
// frameworks, and the server-side view templates. Plain .js and .ts files are
// not on the list, because most of them are not UI code and a backend change
// must not start the ui specialist; a component in one is reached through the
// markup or stylesheet it changes alongside, or through .jsx and .tsx.
var uiExtensions = map[string]bool{
	".html": true, ".htm": true, ".xhtml": true,
	".css": true, ".scss": true, ".sass": true, ".less": true, ".styl": true,
	".jsx": true, ".tsx": true, ".vue": true, ".svelte": true, ".astro": true,
	".erb": true, ".haml": true, ".slim": true, ".hbs": true, ".handlebars": true,
	".mustache": true, ".ejs": true, ".njk": true, ".liquid": true, ".twig": true,
	".jinja": true, ".jinja2": true, ".cshtml": true, ".razor": true, ".gohtml": true,
}

// isUIFile reports whether a repo-relative path is a UI file by its extension
// (uiExtensions) or, for Blade, its double extension. It gates the ui
// specialist, and it picks the files the design detector scans.
func isUIFile(path string) bool {
	lower := strings.ToLower(path)
	return uiExtensions[filepath.Ext(lower)] || strings.HasSuffix(lower, ".blade.php")
}

// diagramSourceExtensions are the file extensions of the text formats diagram
// tools draw from: Mermaid, PlantUML, draw.io, Excalidraw, Graphviz, and D2.
var diagramSourceExtensions = map[string]bool{
	".mmd": true, ".mermaid": true,
	".puml": true, ".plantuml": true, ".pu": true, ".iuml": true, ".wsd": true,
	".drawio": true, ".dio": true, ".excalidraw": true,
	".dot": true, ".gv": true, ".d2": true,
}

// renderedDiagramExtensions are the formats a rendered diagram is committed
// in that a reviewer can read as text: an SVG, and the self-contained HTML
// page the diagram-design skill draws.
var renderedDiagramExtensions = map[string]bool{".svg": true, ".html": true, ".htm": true}

// isDiagramFile reports whether a repo-relative path is a diagram: a file in
// one of diagramSourceExtensions, an image draw.io or Excalidraw embeds its
// source in (a.drawio.svg, a.excalidraw.png), or a rendered diagram
// (renderedDiagramExtensions) whose path names one, in a directory or in its
// own name, since an icon or a page in those formats is not a diagram. It
// gates the diagram specialist. A diagram inside a Markdown fence is not
// seen, because the gate reads paths alone.
func isDiagramFile(path string) bool {
	lower := strings.ToLower(filepath.ToSlash(path))
	ext := filepath.Ext(lower)
	if diagramSourceExtensions[ext] || strings.Contains(lower, ".drawio.") || strings.Contains(lower, ".excalidraw.") {
		return true
	}
	return renderedDiagramExtensions[ext] && strings.Contains(lower, "diagram")
}

// isHTMLDiagram reports whether a diagram file (isDiagramFile) is an HTML
// page, the format the diagram-design skill draws and its checkers read.
func isHTMLDiagram(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return isDiagramFile(path) && (ext == ".html" || ext == ".htm")
}
