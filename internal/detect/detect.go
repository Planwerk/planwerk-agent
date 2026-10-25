package detect

import (
	"bufio"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// maxWalkDepth limits recursive directory scanning to avoid vendor/node_modules trees.
const maxWalkDepth = 4

// maxYAMLScan is the maximum number of YAML files to inspect for Kubernetes markers.
const maxYAMLScan = 20

// maxYAMLLines is the maximum number of lines to read per YAML file.
const maxYAMLLines = 50

// manifestTags maps the base name of a manifest file to the technology tag it
// marks. Technologies tags a repository by it and FileTechnologies a changed
// file, so the two detectors cannot drift apart on a name. package.json and
// tsconfig.json are not in it: Technologies weighs them, since a docs-only
// package.json tags nothing and TypeScript wins over JavaScript.
var manifestTags = map[string]string{
	"go.mod":              "go",
	"go.work":             "go",
	"pyproject.toml":      "python",
	"setup.py":            "python",
	"requirements.txt":    "python",
	"Pipfile":             "python",
	"Cargo.toml":          "rust",
	"Dockerfile":          "docker",
	"docker-compose.yml":  "docker",
	"docker-compose.yaml": "docker",
	"pom.xml":             "java",
	"build.gradle":        "java",
	"build.gradle.kts":    "java",
	"Chart.yaml":          "helm",
}

// Technologies scans the given directory and returns a sorted list of
// technology tags describing the project (e.g. "go", "kubernetes", "python").
func Technologies(dir string) []string {
	tags := make(map[string]bool)

	var (
		hasPackageJSON bool
		hasTSConfig    bool
	)

	yamlScanned := 0

	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}

		rel, _ := filepath.Rel(dir, path)
		depth := strings.Count(rel, string(os.PathSeparator))
		if depth > maxWalkDepth {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}

		if d.IsDir() {
			base := d.Name()
			// .github is handled separately for the github-actions tag; its
			// YAMLs (workflows, composite actions) would otherwise exhaust
			// the k8s-YAML scan budget before real manifests are reached.
			if base == "vendor" || base == "node_modules" || base == ".git" || base == ".github" {
				return fs.SkipDir
			}
			return nil
		}

		name := d.Name()

		if tag, ok := manifestTags[name]; ok {
			tags[tag] = true
		}
		switch name {
		case "package.json":
			if !isDocsOnlyPackageJSON(path) {
				hasPackageJSON = true
			}
		case "tsconfig.json":
			hasTSConfig = true
		}

		if !tags["kubernetes"] && yamlScanned < maxYAMLScan {
			if strings.HasSuffix(name, ".yaml") || strings.HasSuffix(name, ".yml") {
				if looksLikeKubernetes(path) {
					tags["kubernetes"] = true
				}
				yamlScanned++
			}
		}

		return nil
	})

	if hasTSConfig {
		tags["typescript"] = true
	} else if hasPackageJSON {
		tags["javascript"] = true
	}

	// GitHub Actions workflows live at a fixed repo-root path.
	if dirExists(filepath.Join(dir, ".github", "workflows")) {
		entries, err := os.ReadDir(filepath.Join(dir, ".github", "workflows"))
		if err == nil {
			for _, e := range entries {
				if strings.HasSuffix(e.Name(), ".yml") || strings.HasSuffix(e.Name(), ".yaml") {
					tags["github-actions"] = true
					break
				}
			}
		}
	}

	result := make([]string, 0, len(tags))
	for tag := range tags {
		result = append(result, tag)
	}
	sort.Strings(result)
	return result
}

// looksLikeKubernetes reads the first maxYAMLLines lines of a YAML file
// and checks for both "apiVersion:" and "kind:" markers.
func looksLikeKubernetes(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	return hasKubernetesMarkers(f)
}

// hasKubernetesMarkers is looksLikeKubernetes over an open reader: it reads at
// most maxYAMLLines lines and reports whether lines starting with
// "apiVersion:" and "kind:" are both among them.
func hasKubernetesMarkers(r io.Reader) bool {
	scanner := bufio.NewScanner(r)
	hasAPIVersion := false
	hasKind := false
	lines := 0

	for scanner.Scan() && lines < maxYAMLLines {
		line := scanner.Text()
		if strings.HasPrefix(line, "apiVersion:") {
			hasAPIVersion = true
		}
		if strings.HasPrefix(line, "kind:") {
			hasKind = true
		}
		if hasAPIVersion && hasKind {
			return true
		}
		lines++
	}
	return false
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// isDocsOnlyPackageJSON returns true if a package.json has no runtime
// dependencies and every devDependency is a known documentation tool
// (VitePress, VuePress, Docusaurus, docsify, Nextra, ...). Such files
// exist purely to drive a docs build and should not trigger a JS/TS tag.
func isDocsOnlyPackageJSON(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var pkg struct {
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return false
	}
	if len(pkg.Dependencies) > 0 {
		return false
	}
	if len(pkg.DevDependencies) == 0 {
		return false
	}
	for dep := range pkg.DevDependencies {
		if !isDocsDep(dep) {
			return false
		}
	}
	return true
}

func isDocsDep(name string) bool {
	switch name {
	case "vitepress", "vuepress", "docusaurus", "docsify", "docsify-cli", "nextra":
		return true
	}
	return strings.HasPrefix(name, "@docusaurus/") || strings.HasPrefix(name, "@vuepress/")
}
