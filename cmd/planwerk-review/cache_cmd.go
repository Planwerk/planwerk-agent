package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/planwerk/planwerk-review/internal/cache"
	"github.com/planwerk/planwerk-review/internal/elaborate"
	"github.com/planwerk/planwerk-review/internal/gapanalysis"
	"github.com/planwerk/planwerk-review/internal/reviewprepared"
)

// validateCacheScope rejects scope strings that don't match a known command.
// An empty scope means "all commands" and is always valid.
func validateCacheScope(scope string) error {
	switch scope {
	case "",
		cache.CommandReview,
		cache.CommandPropose,
		cache.CommandAudit,
		cache.CommandGlossary,
		elaborate.CommandElaborate,
		gapanalysis.CommandGapAnalysis,
		reviewprepared.CommandReviewPrepared:
		return nil
	default:
		return fmt.Errorf("unknown cache scope %q, supported: review, propose, audit, glossary, elaborate, gap-analysis, review-prepared", scope)
	}
}

// runCacheStats renders a human-readable summary of the cache directory.
func runCacheStats(w io.Writer) error {
	stats, err := cache.Stats()
	if err != nil {
		return err
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "cache dir: %s\n", stats.Dir)
	fmt.Fprintf(&sb, "entries:   %d\n", stats.Total)
	fmt.Fprintf(&sb, "size:      %s\n", humanBytes(stats.TotalSize))
	if stats.Total > 0 {
		fmt.Fprintln(&sb, "by command:")
		names := make([]string, 0, len(stats.ByCommand))
		for name := range stats.ByCommand {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			cs := stats.ByCommand[name]
			fmt.Fprintf(&sb, "  %-8s %3d entries  %s\n", name, cs.Count, humanBytes(cs.Size))
		}
		fmt.Fprintln(&sb, "age distribution:")
		fmt.Fprintf(&sb, "  <= 1 day:    %d\n", stats.Ages.LessThanDay)
		fmt.Fprintf(&sb, "  <= 1 week:   %d\n", stats.Ages.LessThanWeek)
		fmt.Fprintf(&sb, "  <= 1 month:  %d\n", stats.Ages.LessThanMonth)
		fmt.Fprintf(&sb, "  >  1 month:  %d\n", stats.Ages.OlderThanMonth)
		if stats.Newest != nil {
			fmt.Fprintf(&sb, "newest:   %s  %s  (%s ago)\n",
				stats.Newest.Key, stats.Newest.Command, stats.Newest.Age.Round(time.Second))
		}
		if stats.Oldest != nil && (stats.Newest == nil || stats.Newest.Key != stats.Oldest.Key) {
			fmt.Fprintf(&sb, "oldest:   %s  %s  (%s ago)\n",
				stats.Oldest.Key, stats.Oldest.Command, stats.Oldest.Age.Round(time.Second))
		}
	}
	_, err = io.WriteString(w, sb.String())
	return err
}

// runCacheInspect prints metadata plus the pretty-printed JSON payload for a
// single cache key.
func runCacheInspect(w io.Writer, key string) error {
	meta, payload, err := cache.Inspect(key)
	if err != nil {
		if errors.Is(err, cache.ErrNotFound) {
			return fmt.Errorf("no cache entry for key %q", key)
		}
		return err
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "key:       %s\n", meta.Key)
	fmt.Fprintf(&sb, "command:   %s\n", meta.Command)
	if !meta.WrittenAt.IsZero() {
		fmt.Fprintf(&sb, "writtenAt: %s\n", meta.WrittenAt.Format(time.RFC3339))
		fmt.Fprintf(&sb, "age:       %s\n", meta.Age.Round(time.Second))
	} else {
		fmt.Fprintln(&sb, "writtenAt: (unknown — legacy entry)")
	}
	fmt.Fprintf(&sb, "size:      %s\n", humanBytes(meta.Size))
	fmt.Fprintln(&sb, "payload:")
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, payload, "  ", "  "); err != nil {
		fmt.Fprintf(&sb, "  %s\n", string(payload))
	} else {
		fmt.Fprintf(&sb, "  %s\n", pretty.String())
	}
	_, err = io.WriteString(w, sb.String())
	return err
}

// humanBytes formats a byte count using binary units (KiB/MiB/...).
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// newCacheCmd builds the "cache" subcommand group: visibility into the on-disk
// cache. The top-level --cache-stats / --cache-inspect flags on the root
// command remain for compatibility; these subcommands are the preferred entry
// points.
func newCacheCmd(deps *runtimeDeps) *cobra.Command {
	cacheCmd := &cobra.Command{
		Use:   "cache",
		Short: "Inspect and manage cached review/propose/audit results",
		Long: `Inspect and manage planwerk-review's on-disk cache.

Cached entries are keyed by repo + HEAD SHA + flags and are written under the
user cache directory. Use "cache stats" for an overview and "cache inspect
<key>" to dump a single entry.`,
	}

	cacheStatsCmd := &cobra.Command{
		Use:   "stats",
		Short: "Show cache size, age distribution, and per-command breakdown",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCacheStats(cmd.OutOrStdout())
		},
	}

	cacheInspectCmd := &cobra.Command{
		Use:   "inspect <key>",
		Short: "Print metadata and payload for a single cache key",
		Long: `Print the metadata (command, writtenAt, age, size) and pretty-printed
payload for a single cache entry. Keys are listed by "cache stats".`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCacheInspect(cmd.OutOrStdout(), args[0])
		},
	}

	cacheCmd.AddCommand(cacheStatsCmd, cacheInspectCmd)
	return cacheCmd
}
