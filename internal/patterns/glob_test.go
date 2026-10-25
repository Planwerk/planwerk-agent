package patterns

import (
	"strings"
	"testing"
	"time"
)

func TestMatchPathGlob(t *testing.T) {
	tests := []struct {
		glob, name string
		want       bool
	}{
		{"**/*.md", "README.md", true},
		{"**/*.md", "docs/a/b.md", true},
		{"**/*.md", "Docs/README.MD", true},
		{"**/*.MD", "docs/readme.md", true},
		{"**/*.md", "a.mdx", false},
		{"docs/**", "docs/x/y.go", true},
		{"docs/**", "docs", true},
		{"docs/**", "src/docs/x.go", false},
		{"*.md", "README.md", true},
		{"*.md", "docs/a.md", false},
		{"cmd/*/main.go", "cmd/tool/main.go", true},
		{"cmd/*/main.go", "cmd/main.go", false},
		{"**/testdata/**", "internal/x/testdata/in.json", true},
		{"**/testdata/**/*.json", "testdata/in.json", true},
		{"**/testdata/**/*.json", "testdata/in.yaml", false},
		{"[", "[", false},
		{"docs/[/**", "docs/[/a.md", false},
		{"**/[", "a/b", false},
		{"**/a/**/b/**/c/**/d", "x/a/y/b/z/c/d", true},
		{"**/a/**/b/**/c/**/d/**/e", "a/b/c/d/e", true},
		{"**/a/**/b/**/c/**/d/**/e", "a/b/c/d", false},
		{"**/a/**/b", "a/b/a/c", false},
		{"**/**/*.md", "README.md", true},
		{"**/**/*.md", "docs/a/b.md", true},
		{"**/**/**/**/**/*.md", "docs/a.md", true},
	}
	for _, tt := range tests {
		if got := matchPathGlob(tt.glob, tt.name); got != tt.want {
			t.Errorf("matchPathGlob(%q, %q) = %v, want %v", tt.glob, tt.name, got, tt.want)
		}
	}
}

// TestMatchPathGlob_DeepPathCostsLinearTime pins the bound on a hostile
// match: a wiki glob with many "**" segments against a changed path near the
// 4 KiB path limit. A matcher that retries every split of the path between
// the "**" segments takes hours on these; the bounded one takes microseconds.
func TestMatchPathGlob_DeepPathCostsLinearTime(t *testing.T) {
	deep := strings.Repeat("x/", 2000)
	tests := []struct {
		glob, name string
		want       bool
	}{
		{"**/x/**/x/**/x/**/y", deep + "z", false},
		{"**/x/**/x/**/x/**/z", deep + "z", true},
		{"**/x/**/x/**/x/**/x/**/x/**/x/**/x/**/y", deep + "z", false},
	}
	for _, tt := range tests {
		start := time.Now()
		got := matchPathGlob(tt.glob, tt.name)
		if elapsed := time.Since(start); elapsed > 10*time.Second {
			t.Errorf("matchPathGlob(%q, <%d segments>) took %s", tt.glob, strings.Count(tt.name, "/")+1, elapsed)
		}
		if got != tt.want {
			t.Errorf("matchPathGlob(%q, <%d segments>) = %v, want %v", tt.glob, strings.Count(tt.name, "/")+1, got, tt.want)
		}
	}
}
