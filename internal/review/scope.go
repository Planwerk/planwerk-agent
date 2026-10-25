package review

import (
	"log/slog"

	"github.com/planwerk/planwerk-agent/internal/detect"
	"github.com/planwerk/planwerk-agent/internal/patterns"
)

// scopeCatalog returns the review session's split of pats by the changed
// files under --scope-patterns (claude.ReviewContext.Scope), the catalog its
// index names (ReviewContext.Catalog), and the cleanup that removes that
// catalog. files are the repo-relative paths of the changed files the review
// reads, and dir the checkout detect.FileTechnologies tags them in. It returns
// a nil scope, which renders every body, when on is false, pats is empty, files
// is empty, or the catalog cannot be written; a scope that leaves no pattern
// out writes no catalog. The catalog holds the whole of pats, so the index can
// name any part of it through Catalog.Only. The cleanup is never nil
// (decision 141).
func scopeCatalog(on bool, dir string, files []string, pats []patterns.Pattern) (*patterns.FileScope, patterns.Catalog, func()) {
	noop := func() {}
	if !on || len(pats) == 0 {
		return nil, patterns.Catalog{}, noop
	}
	if len(files) == 0 {
		slog.Warn("--scope-patterns: the changed files are not listed, so the review carries every pattern body")
		return nil, patterns.Catalog{}, noop
	}
	s := patterns.ScopeToFiles(pats, files, detect.FileTechnologies(dir, files))
	if len(s.Rest) == 0 {
		return &s, patterns.Catalog{}, noop
	}
	// MaterializeOrWarn has already said that the sessions carry the bodies
	// when the write fails.
	cat, cleanup := patterns.MaterializeOrWarn(pats)
	if cat.Dir == "" {
		return nil, patterns.Catalog{}, cleanup
	}
	slog.Info("scoped the review's pattern bodies to the changed files", "inline", len(s.Inline), "indexed", len(s.Rest), "files", len(files))
	return &s, cat, cleanup
}
