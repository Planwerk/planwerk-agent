package patterns

import (
	"path"
	"strings"
)

// matchPathGlob reports whether the Applies-To-Paths glob matches the
// repo-relative slash path name, ignoring case. A "**" segment matches zero or
// more whole segments, and every other segment follows path.Match within one
// segment. A glob with a malformed segment matches nothing. A glob cannot hold
// a comma, because the "**Applies-To-Paths**:" line separates its globs with
// commas. A wiki pattern is world-editable and a changed path is the pull
// request author's, so the match costs at most the glob's segments times the
// path's, however many "**" the glob holds (decision 141).
func matchPathGlob(glob, name string) bool {
	segs := strings.Split(strings.ToLower(glob), "/")
	for _, seg := range segs {
		if _, err := path.Match(seg, ""); err != nil {
			return false
		}
	}
	return matchSegments(segs, strings.Split(strings.ToLower(name), "/"))
}

// matchSegments matches the glob segments of matchPathGlob against the path
// segments of name. On a mismatch it backtracks only to the last "**" it
// passed, letting that one absorb one more segment: an earlier "**" taking
// more would only start the later one further on, and the later one can take
// those segments itself, so no earlier choice needs retrying. Each retry moves
// that "**" on by one segment of name and replays at most the glob, which
// bounds the work at len(glob) times len(name) segment matches.
func matchSegments(glob, name []string) bool {
	g, n := 0, 0
	star, absorbed := -1, 0 // the last "**" passed, and where its share of name ends
	for n < len(name) {
		switch {
		case g < len(glob) && glob[g] == "**":
			star, absorbed = g, n
			g++
		case g < len(glob) && segmentMatches(glob[g], name[n]):
			g++
			n++
		case star >= 0:
			absorbed++
			g, n = star+1, absorbed
		default:
			return false
		}
	}
	for g < len(glob) && glob[g] == "**" {
		g++
	}
	return g == len(glob)
}

// segmentMatches reports whether the glob segment pattern matches the path
// segment seg; a malformed pattern matches nothing.
func segmentMatches(pattern, seg string) bool {
	ok, err := path.Match(pattern, seg)
	return err == nil && ok
}
