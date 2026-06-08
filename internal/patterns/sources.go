package patterns

import "fmt"

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

// loadOrderedSources resolves opts and sources into parsed-pattern groups in
// ascending priority order (lowest first): the embedded catalog (unless
// opts.NoEmbedded) is the lowest-priority group, followed by one group per
// explicit on-disk/remote source in slice order. The caller dedups across the
// groups by Pattern.Name — later groups win — and applies tag filtering.
func loadOrderedSources(opts LoadOptions, sources []string) ([][]Pattern, error) {
	var groups [][]Pattern

	if !opts.NoEmbedded {
		embedded, err := loadEmbedded()
		if err != nil {
			return nil, fmt.Errorf("loading embedded patterns: %w", err)
		}
		groups = append(groups, embedded)
	}

	dirs, err := resolveSources(opts.Remote, sources)
	if err != nil {
		return nil, err
	}
	for _, dir := range dirs {
		pats, err := loadDir(dir)
		if err != nil {
			return nil, err
		}
		groups = append(groups, pats)
	}

	return groups, nil
}
