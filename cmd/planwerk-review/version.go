package main

import (
	"fmt"
	"io"
	"runtime/debug"
	"strings"
)

// devVersion is the placeholder version string used when no release version
// has been injected via ldflags. Triggers the unreleased-build warning.
const devVersion = "dev"

// version is overridden at build time via -ldflags "-X main.version=...".
var version = devVersion

// buildInfo holds resolved version and build metadata, populated either from
// ldflags (main.version) or from runtime/debug.ReadBuildInfo.
type buildInfo struct {
	Version   string
	Commit    string
	BuildDate string
	GoVersion string
	IsDev     bool
}

// resolveBuildInfo returns build metadata, preferring the ldflags-injected
// version and falling back to debug.ReadBuildInfo when it is unset.
func resolveBuildInfo(ldflagsVersion string) buildInfo {
	bi := buildInfo{Version: ldflagsVersion}

	if info, ok := debug.ReadBuildInfo(); ok {
		bi.GoVersion = info.GoVersion
		if bi.Version == "" || bi.Version == devVersion {
			if v := info.Main.Version; v != "" && v != "(devel)" {
				bi.Version = v
			}
		}
		for _, s := range info.Settings {
			switch s.Key {
			case "vcs.revision":
				bi.Commit = s.Value
			case "vcs.time":
				bi.BuildDate = s.Value
			}
		}
	}

	if bi.Version == "" {
		bi.Version = devVersion
	}
	bi.IsDev = bi.Version == devVersion
	return bi
}

// writeVersion renders the version line, optional verbose build details, and
// a warning when this is an unreleased development build.
func writeVersion(w io.Writer, bi buildInfo, verbose bool) {
	var sb strings.Builder
	fmt.Fprintf(&sb, "planwerk-review version %s\n", bi.Version)
	if verbose {
		if bi.Commit != "" {
			fmt.Fprintf(&sb, "commit: %s\n", bi.Commit)
		}
		if bi.BuildDate != "" {
			fmt.Fprintf(&sb, "built: %s\n", bi.BuildDate)
		}
		if bi.GoVersion != "" {
			fmt.Fprintf(&sb, "go: %s\n", bi.GoVersion)
		}
	}
	if bi.IsDev {
		sb.WriteString("warning: unreleased development build — version metadata unavailable\n")
	}
	_, _ = io.WriteString(w, sb.String())
}
