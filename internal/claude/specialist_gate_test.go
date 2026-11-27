package claude

import (
	"sort"
	"strings"
	"testing"
)

// runningSpecialists returns the keys of the registered specialists that would
// run for the given changed-file set, in registry order.
func runningSpecialists(changed []string) []string {
	var keys []string
	for _, sp := range Specialists {
		if sp.ShouldRun(changed) {
			keys = append(keys, sp.Key)
		}
	}
	return keys
}

func TestSpecialistShouldRun_Gating(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		changed []string
		want    []string
	}{
		{
			// Acceptance criterion: a markdown-only PR runs at most 2 specialists.
			name:    "markdown only runs only the NeverGate specialists",
			changed: []string{"README.md"},
			want:    []string{"security", "data-migration"},
		},
		{
			name:    "docs and config only still gates out source specialists",
			changed: []string{"docs/guide.rst", ".github/workflows/ci.yml", "config.yaml"},
			want:    []string{"security", "data-migration"},
		},
		{
			name:    "media-only change runs only the NeverGate specialists",
			changed: []string{"assets/logo.png"},
			want:    []string{"security", "data-migration"},
		},
		{
			name:    "non-route source change skips api-contract only",
			changed: []string{"internal/foo.go"},
			want:    []string{"security", "data-migration", "testing", "performance", "maintainability"},
		},
		{
			name:    "route directory change runs every specialist but ui",
			changed: []string{"internal/api/users.go"},
			want:    []string{"security", "data-migration", "testing", "performance", "api-contract", "maintainability"},
		},
		{
			name:    "handler-named file outside a route dir still runs api-contract",
			changed: []string{"internal/login_handler.go"},
			want:    []string{"security", "data-migration", "testing", "performance", "api-contract", "maintainability"},
		},
		{
			name:    "unknown diff fails open and runs every specialist",
			changed: nil,
			want:    []string{"security", "data-migration", "testing", "performance", "api-contract", "maintainability", "ui", "diagram"},
		},
		{
			name:    "a component change runs ui with the source specialists",
			changed: []string{"web/src/Button.tsx"},
			want:    []string{"security", "data-migration", "testing", "performance", "maintainability", "ui"},
		},
		{
			name:    "a stylesheet-only change runs ui with the source specialists",
			changed: []string{"assets/site.scss"},
			want:    []string{"security", "data-migration", "testing", "performance", "maintainability", "ui"},
		},
		{
			name:    "a plain TypeScript change does not run ui",
			changed: []string{"server/db.ts"},
			want:    []string{"security", "data-migration", "testing", "performance", "maintainability"},
		},
		{
			name:    "an exported diagram runs diagram with the NeverGate specialists",
			changed: []string{"docs/diagrams/flow.svg"},
			want:    []string{"security", "data-migration", "diagram"},
		},
		{
			name:    "a Mermaid source runs diagram with the source specialists",
			changed: []string{"docs/flow.mmd"},
			want:    []string{"security", "data-migration", "testing", "performance", "maintainability", "diagram"},
		},
		{
			name:    "an HTML diagram runs diagram and ui with the source specialists",
			changed: []string{"docs/diagrams/flow.html"},
			want:    []string{"security", "data-migration", "testing", "performance", "maintainability", "ui", "diagram"},
		},
		{
			name:    "an icon is no diagram",
			changed: []string{"assets/icons/close.svg"},
			want:    []string{"security", "data-migration"},
		},
		{
			name:    "mixed docs and source runs the source specialists",
			changed: []string{"README.md", "internal/foo.go"},
			want:    []string{"security", "data-migration", "testing", "performance", "maintainability"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := runningSpecialists(tc.changed)
			if !sameStringSet(got, tc.want) {
				t.Errorf("running specialists for %v = %v, want %v", tc.changed, got, tc.want)
			}
		})
	}
}

func TestSpecialistShouldRun_NeverGateAlwaysRuns(t *testing.T) {
	t.Parallel()
	// security and data-migration must run regardless of the diff — including a
	// docs-only diff that gates out every other specialist.
	for _, sp := range Specialists {
		if !sp.NeverGate {
			continue
		}
		for _, changed := range [][]string{nil, {"README.md"}, {"assets/logo.png"}} {
			if !sp.ShouldRun(changed) {
				t.Errorf("NeverGate specialist %q skipped for changed=%v; must always run", sp.Key, changed)
			}
		}
	}
}

func TestIsSourceFile(t *testing.T) {
	t.Parallel()
	tests := []struct {
		path string
		want bool
	}{
		{"internal/claude/specialist.go", true},
		{"scripts/build.py", true},
		{"src/app.ts", true},
		{"Dockerfile", true},       // extension-less build file counts as source
		{"Makefile", true},         // extension-less build file counts as source
		{"weird.unknownext", true}, // unknown extension errs toward source
		{"README.md", false},
		{"docs/guide.rst", false},
		{"notes.txt", false},
		{"config.yaml", false},
		{".github/workflows/ci.yml", false},
		{"package.json", false},
		{"Cargo.toml", false},
		{"assets/logo.png", false},
		{"diagram.svg", false},
	}
	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			t.Parallel()
			if got := isSourceFile(tc.path); got != tc.want {
				t.Errorf("isSourceFile(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}

func TestIsRouteFile(t *testing.T) {
	t.Parallel()
	tests := []struct {
		path string
		want bool
	}{
		{"internal/api/users.go", true},
		{"routes/web.go", true},
		{"app/controllers/users_controller.rb", true},
		{"internal/handlers/login.go", true},
		{"internal/login_handler.go", true}, // base name names a handler
		{"src/userRoute.ts", true},          // base name names a route
		{"pkg/endpoints/health.go", true},
		{"internal/foo.go", false},
		{"cmd/planwerk-agent/main.go", false},
		{"README.md", false},
		{"internal/apiclient/client.go", false}, // "apiclient" is not the "api" segment
	}
	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			t.Parallel()
			if got := isRouteFile(tc.path); got != tc.want {
				t.Errorf("isRouteFile(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}

// TestRelevanceString pins the names review --dry-run gives a gated-out
// specialist's missing files, one per Relevance value (decision 143).
func TestRelevanceString(t *testing.T) {
	t.Parallel()
	for r, want := range map[Relevance]string{
		RelevanceAnySource: "source file",
		RelevanceRoutes:    "route or handler file",
		RelevanceUI:        "UI file",
		RelevanceDiagram:   "diagram file",
	} {
		if got := r.String(); got != want {
			t.Errorf("Relevance(%d).String() = %q, want %q", r, got, want)
		}
	}
}

// sameStringSet reports whether a and b contain the same elements, ignoring
// order and duplicates.
func sameStringSet(a, b []string) bool {
	as := append([]string(nil), a...)
	bs := append([]string(nil), b...)
	sort.Strings(as)
	sort.Strings(bs)
	return strings.Join(as, "\x00") == strings.Join(bs, "\x00")
}

func TestIsUIFile(t *testing.T) {
	t.Parallel()
	for path, want := range map[string]bool{
		"index.html":                     true,
		"web/App.TSX":                    true,
		"src/components/Card.vue":        true,
		"src/routes/+page.svelte":        true,
		"styles/main.scss":               true,
		"app/views/users/show.html.erb":  true,
		"resources/views/home.blade.php": true,
		"Pages/Index.cshtml":             true,
		"server/handler.ts":              false,
		"src/util.js":                    false,
		"app/models/user.rb":             false,
		"resources/views/home.php":       false,
		"docs/guide.md":                  false,
		"assets/logo.svg":                false,
		"templates/config.yaml":          false,
		"Makefile":                       false,
	} {
		if got := isUIFile(path); got != want {
			t.Errorf("isUIFile(%q) = %v, want %v", path, got, want)
		}
	}
}

func TestIsDiagramFile(t *testing.T) {
	t.Parallel()
	for path, want := range map[string]bool{
		"docs/flow.mmd":                     true,
		"docs/sequence.PUML":                true,
		"architecture.drawio":               true,
		"docs/overview.drawio.svg":          true,
		"docs/sketch.excalidraw.png":        true,
		"board.excalidraw":                  true,
		"deps.dot":                          true,
		"system.d2":                         true,
		"docs/diagrams/flow.svg":            true,
		"docs/Diagrams/flow.html":           true,
		"docs/architecture-diagram.svg":     true,
		"docs/diagrams/flow.png":            false,
		"assets/logo.svg":                   false,
		"web/index.html":                    false,
		"docs/diagrams.md":                  false,
		"internal/diagramdesign/kit.go":     false,
		"docs/diagrams/README.md":           false,
		"internal/claude/specialist_ui.txt": false,
	} {
		if got := isDiagramFile(path); got != want {
			t.Errorf("isDiagramFile(%q) = %v, want %v", path, got, want)
		}
	}
}

func TestIsHTMLDiagram(t *testing.T) {
	t.Parallel()
	for path, want := range map[string]bool{
		"docs/diagrams/flow.html": true,
		"docs/diagrams/flow.HTM":  true,
		"docs/diagrams/flow.svg":  false,
		"docs/flow.mmd":           false,
		"web/index.html":          false,
	} {
		if got := isHTMLDiagram(path); got != want {
			t.Errorf("isHTMLDiagram(%q) = %v, want %v", path, got, want)
		}
	}
}
