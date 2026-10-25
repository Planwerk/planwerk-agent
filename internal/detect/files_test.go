package detect

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const k8sManifest = "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: app\n"

// writeNested is writeFile for a slash path under dir whose directories may
// not exist yet.
func writeNested(t *testing.T, dir, rel, content string) {
	t.Helper()
	full := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Dir(full), filepath.Base(full), content)
}

func TestFileTechnologies(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "repo")
	outside := filepath.Join(parent, "outside")
	writeNested(t, parent, "x.yaml", k8sManifest)
	writeNested(t, outside, "dep.yaml", k8sManifest)

	writeNested(t, dir, "a.go", "package a\n")
	writeNested(t, dir, "go.sum", "")
	writeNested(t, dir, "x.pyi", "")
	writeNested(t, dir, "Dockerfile.dev", "FROM alpine\n")
	writeNested(t, dir, ".github/workflows/ci.yml", k8sManifest)
	writeNested(t, dir, "action.yml", "runs:\n  using: composite\n")
	writeNested(t, dir, "deploy/app.yaml", k8sManifest)
	writeNested(t, dir, "config.yaml", "log_level: debug\n")
	writeNested(t, dir, "charts/app/Chart.yaml", "apiVersion: v2\nname: app\n")
	writeNested(t, dir, "charts/app/templates/dep.yaml", k8sManifest)
	writeNested(t, dir, "controllers/r.go", "package controllers\n\nimport (\n\t\"context\"\n\n\tctrl \"sigs.k8s.io/controller-runtime\"\n)\n")
	writeNested(t, dir, "real/manifest.yaml", k8sManifest)
	writeNested(t, dir, "README.md", "# Repo\n")
	if err := os.Symlink(filepath.Join(dir, "real", "manifest.yaml"), filepath.Join(dir, "manifest.yaml")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "linked")); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		file string
		want []string // nil: the file is absent from the result
	}{
		{"a.go", []string{"go"}},
		{"go.sum", []string{"go"}},
		{"x.pyi", []string{"python"}},
		{"Dockerfile.dev", []string{"docker"}},
		{".github/workflows/ci.yml", []string{"github-actions"}},
		{"action.yml", []string{"github-actions"}},
		{"deploy/app.yaml", []string{"kubernetes"}},
		{"config.yaml", nil},
		{"charts/app/templates/dep.yaml", []string{"helm", "kubernetes"}},
		{"controllers/r.go", []string{"go", "kubernetes"}},
		{"gone.yaml", nil},
		{"gone.go", []string{"go"}},
		// The content of a symlink is never read, nor that of a file reached
		// through a directory link that leaves the checkout, nor that of a
		// path outside it.
		{"manifest.yaml", nil},
		{"linked/dep.yaml", nil},
		{"../x.yaml", nil},
		{"README.md", nil},
	}
	files := make([]string, len(tests))
	for i, tt := range tests {
		files[i] = tt.file
	}
	got := FileTechnologies(dir, files)
	for _, tt := range tests {
		tags, ok := got[tt.file]
		if tt.want == nil {
			if ok {
				t.Errorf("%s: tags %v, want the file absent", tt.file, tags)
			}
			continue
		}
		if !reflect.DeepEqual(tags, tt.want) {
			t.Errorf("%s: tags %v, want %v", tt.file, tags, tt.want)
		}
	}
}

// TestFileTechnologies_UnreadableDirKeepsNameTags covers a checkout that cannot
// be opened: the name rules still apply and nothing is read.
func TestFileTechnologies_UnreadableDirKeepsNameTags(t *testing.T) {
	got := FileTechnologies(filepath.Join(t.TempDir(), "missing"), []string{"main.go", "deploy.yaml"})
	want := map[string][]string{"main.go": {"go"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("FileTechnologies = %v, want %v", got, want)
	}
}

// TestFileTechnologies_NameRules pins the extension and base-name table,
// which needs no file on disk.
func TestFileTechnologies_NameRules(t *testing.T) {
	tests := []struct {
		file string
		want []string
	}{
		{"cmd/main.go", []string{"go"}},
		{"go.work.sum", []string{"go"}},
		{"svc/go.mod", []string{"go"}},
		{"app.py", []string{"python"}},
		{"Pipfile", []string{"python"}},
		{"src/lib.rs", []string{"rust"}},
		{"Cargo.toml", []string{"rust"}},
		{"App.java", []string{"java"}},
		{"build.gradle.kts", []string{"java"}},
		{"web/index.tsx", []string{"typescript"}},
		{"tsconfig.json", []string{"typescript"}},
		{"web/app.mjs", []string{"javascript", "typescript"}},
		{"package.json", []string{"javascript", "typescript"}},
		{"Dockerfile", []string{"docker"}},
		{"images/API.Dockerfile", []string{"docker"}},
		{".dockerignore", []string{"docker"}},
		{"docker-compose.yaml", []string{"docker"}},
		{"Chart.yaml", []string{"helm"}},
		{".github/workflows/release.yaml", []string{"github-actions"}},
		{"actions/setup/action.yaml", []string{"github-actions"}},
		{".github/dependabot.yml", nil},
		{"notes.txt", nil},
	}
	files := make([]string, len(tests))
	for i, tt := range tests {
		files[i] = tt.file
	}
	got := FileTechnologies(t.TempDir(), files)
	for _, tt := range tests {
		if tags := got[tt.file]; !reflect.DeepEqual(tags, tt.want) {
			t.Errorf("%s: tags %v, want %v", tt.file, tags, tt.want)
		}
	}
}

// TestImportsKubernetes_StopsAtTheLineBound pins the 200-line read: an import
// past it is not seen.
func TestImportsKubernetes_StopsAtTheLineBound(t *testing.T) {
	dir := t.TempDir()
	body := strings.Repeat("// filler\n", maxGoImportLines)
	writeNested(t, dir, "late.go", body+"import \"k8s.io/api/core/v1\"\n")
	if got := FileTechnologies(dir, []string{"late.go"})["late.go"]; !reflect.DeepEqual(got, []string{"go"}) {
		t.Errorf("late.go: tags %v, want only go", got)
	}
}
