package detect

import (
	"bufio"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

// maxGoImportLines is the most lines of a Go file FileTechnologies reads for a
// Kubernetes import. The import block sits at the top of a Go file.
const maxGoImportLines = 200

// FileTechnologies tags each of files, repo-relative slash paths under dir,
// with the technology tags Technologies emits for a repository. The result is
// keyed by each path exactly as given, its values are sorted and deduplicated,
// and a file with no tag is absent. The rules:
//   - by base name and extension: go for .go, go.sum and go.work.sum; python
//     for .py and .pyi; rust for .rs; java for .java; typescript for .ts,
//     .tsx, .mts, .cts and tsconfig.json; javascript and typescript for .js,
//     .jsx, .mjs, .cjs and package.json; docker for Dockerfile, Dockerfile.*,
//     *.dockerfile (in any case) and .dockerignore; and the tag of each
//     manifest Technologies reads (go.mod, Cargo.toml, Chart.yaml, ...).
//   - helm: the file's directory, or a directory above it up to dir, holds a
//     regular Chart.yaml.
//   - github-actions: a .yml or .yaml file under .github/workflows/, or a file
//     named action.yml or action.yaml.
//   - kubernetes: a .yml or .yaml file outside .github/ whose first 50 lines
//     carry apiVersion: and kind:, or a .go file whose first 200 lines import a
//     k8s.io/ or sigs.k8s.io/ package.
//
// Content and Chart.yaml are read only from regular files inside dir, through
// os.Root: a path that leaves dir, a deleted file, a symlink or a device keeps
// its name tags and is never opened (decision 141).
func FileTechnologies(dir string, files []string) map[string][]string {
	out := make(map[string][]string, len(files))
	// A dir that cannot be opened leaves every file its name tags alone.
	root, _ := os.OpenRoot(dir)
	if root != nil {
		defer func() { _ = root.Close() }()
	}
	charts := make(map[string]bool) // directory -> holds a regular Chart.yaml
	for _, f := range files {
		tags := nameTags(f)
		if root != nil && filepath.IsLocal(filepath.FromSlash(f)) {
			if underChart(root, f, charts) {
				tags = append(tags, "helm")
			}
			if contentIsKubernetes(root, f) {
				tags = append(tags, "kubernetes")
			}
		}
		if len(tags) == 0 {
			continue
		}
		slices.Sort(tags)
		out[f] = slices.Compact(tags)
	}
	return out
}

// nameTags returns the tags FileTechnologies gives f by its base name, its
// extension and its place under .github/, unsorted and possibly repeated.
func nameTags(f string) []string {
	base := path.Base(f)
	lower := strings.ToLower(base)
	ext := path.Ext(lower)
	var tags []string
	if tag, ok := manifestTags[base]; ok {
		tags = append(tags, tag)
	}
	switch ext {
	case ".go":
		tags = append(tags, "go")
	case ".py", ".pyi":
		tags = append(tags, "python")
	case ".rs":
		tags = append(tags, "rust")
	case ".java":
		tags = append(tags, "java")
	case ".ts", ".tsx", ".mts", ".cts":
		tags = append(tags, "typescript")
	case ".js", ".jsx", ".mjs", ".cjs":
		// The catalog was filtered by the repository's tags already, so the
		// typescript tag only lets a TypeScript repository's patterns govern
		// its .js files.
		tags = append(tags, "javascript", "typescript")
	}
	switch base {
	case "go.sum", "go.work.sum":
		tags = append(tags, "go")
	case "tsconfig.json":
		tags = append(tags, "typescript")
	case "package.json":
		tags = append(tags, "javascript", "typescript")
	case ".dockerignore":
		tags = append(tags, "docker")
	}
	if lower == "dockerfile" || strings.HasPrefix(lower, "dockerfile.") || strings.HasSuffix(lower, ".dockerfile") {
		tags = append(tags, "docker")
	}
	workflow := isYAMLExt(ext) && strings.HasPrefix(f, ".github/workflows/")
	if workflow || base == "action.yml" || base == "action.yaml" {
		tags = append(tags, "github-actions")
	}
	return tags
}

// isYAMLExt reports whether the lower-case extension ext is a YAML one.
func isYAMLExt(ext string) bool {
	return ext == ".yml" || ext == ".yaml"
}

// underChart reports whether f's directory, or one above it up to root, holds
// a regular Chart.yaml. charts memoizes the answer per directory across the
// files of one call.
func underChart(root *os.Root, f string, charts map[string]bool) bool {
	for d := path.Dir(f); ; d = path.Dir(d) {
		held, ok := charts[d]
		if !ok {
			info, err := root.Lstat(filepath.FromSlash(path.Join(d, "Chart.yaml")))
			held = err == nil && info.Mode().IsRegular()
			charts[d] = held
		}
		if held {
			return true
		}
		if d == "." {
			return false
		}
	}
}

// contentIsKubernetes reports whether f, a regular file under root, is a
// Kubernetes manifest outside .github/ or a Go file importing a Kubernetes
// package. Any other file is never opened.
func contentIsKubernetes(root *os.Root, f string) bool {
	ext := strings.ToLower(path.Ext(f))
	var check func(io.Reader) bool
	switch {
	case isYAMLExt(ext) && !strings.HasPrefix(f, ".github/"):
		check = hasKubernetesMarkers
	case ext == ".go":
		check = importsKubernetes
	default:
		return false
	}
	name := filepath.FromSlash(f)
	info, err := root.Lstat(name)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	file, err := root.Open(name)
	if err != nil {
		return false
	}
	defer func() { _ = file.Close() }()
	return check(file)
}

// importsKubernetes reports whether one of the first maxGoImportLines lines of
// r names a k8s.io/ or sigs.k8s.io/ import path. A line longer than the
// scanner's buffer ends the read.
func importsKubernetes(r io.Reader) bool {
	scanner := bufio.NewScanner(r)
	for lines := 0; lines < maxGoImportLines && scanner.Scan(); lines++ {
		line := scanner.Text()
		if strings.Contains(line, `"k8s.io/`) || strings.Contains(line, `"sigs.k8s.io/`) {
			return true
		}
	}
	return false
}
