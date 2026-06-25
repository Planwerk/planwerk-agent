package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/planwerk/planwerk-review/internal/cli"
	"github.com/planwerk/planwerk-review/internal/patterns"
	"github.com/planwerk/planwerk-review/internal/sync"
)

// newSyncCmd builds the "sync" subcommand: reconcile a target repository's GitHub
// Wiki knowledge — its review patterns and project-memory pages — against the
// current state of the code. A read-only Claude pass flags entries that are stale
// (they reference code that no longer exists) or redundant (duplicated or
// superseded by another entry) and reports them. --dry-run is the default and
// reports only; --prune/--apply deletes the flagged entries on the wiki and
// pushes, in a separate write phase that asks for confirmation first.
func newSyncCmd(deps *runtimeDeps) *cobra.Command {
	var cfg cli.SyncConfig
	var wikiRef string
	var dryRun bool

	syncCmd := &cobra.Command{
		Use:   "sync <repo-ref>",
		Short: "Reconcile wiki knowledge against the code",
		Long: `Reconcile a target repository's GitHub Wiki knowledge — its review
patterns and project-memory pages — against the current state of the code.

The repo and its wiki are cloned and a read-only analysis pass flags entries
that are stale (they reference code that no longer exists) or redundant
(duplicated or superseded by another entry), then reports them.

--dry-run is the default and reports only. --prune (or its alias --apply)
deletes the flagged entries on the wiki and pushes, in a separate write phase
that asks for confirmation first; pass --yes to confirm non-interactively.

Repository reference can be a URL (https://github.com/owner/repo)
or short form (owner/repo).`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg.RepoRef = args[0]

			prune := cfg.Prune || cfg.Apply
			// --dry-run defaults to true (it names the default behavior), so a
			// conflict exists only when the user explicitly asks for both.
			if prune && cmd.Flags().Changed("dry-run") && dryRun {
				return fmt.Errorf("--dry-run and --prune/--apply are mutually exclusive")
			}

			switch cfg.Format {
			case formatMarkdown, formatJSON:
			default:
				return fmt.Errorf("unknown format %q, supported: markdown, json", cfg.Format)
			}

			opts := cfg.ToSyncOptions(deps.version)
			opts.Remote = deps.remoteOpts
			opts.Wiki = resolveSyncWiki(wikiRef, cmd.Flags().Changed("wiki-ref"), deps.fileCfg.Wiki)
			return sync.Run(os.Stdout, opts, deps.claude.Sync)
		},
	}

	flags := syncCmd.Flags()
	flags.BoolVar(&dryRun, "dry-run", true, "Report stale and redundant entries without changing the wiki (the default)")
	flags.BoolVar(&cfg.Prune, "prune", false, "Delete the flagged entries on the wiki and push (the write phase)")
	flags.BoolVar(&cfg.Apply, "apply", false, "Alias of --prune")
	flags.BoolVar(&cfg.Yes, "yes", false, "Skip the write-phase confirmation prompt (for a non-interactive prune)")
	flags.StringVar(&cfg.Format, "format", "markdown", "Output format (markdown, json)")
	flags.StringVar(&wikiRef, "wiki-ref", "", "Pin the wiki to a branch, tag, or commit (env: "+envWikiRef+"; empty uses the wiki's default branch)")

	return syncCmd
}

// resolveSyncWiki builds the WikiOptions for sync. The wiki is always enabled —
// reconciling it is the command's whole purpose — so there is no --wiki/--no-wiki
// here. The ref precedence is --wiki-ref, then PLANWERK_WIKI_REF, then the config
// file; the repo override comes from the config file only, mirroring
// resolveWikiOptions.
func resolveSyncWiki(refFlag string, refChanged bool, fc cli.WikiFileConfig) patterns.WikiOptions {
	ref := refFlag
	if !refChanged {
		if v := strings.TrimSpace(os.Getenv(envWikiRef)); v != "" {
			ref = v
		} else if fc.Ref != nil {
			ref = *fc.Ref
		}
	}
	opts := patterns.WikiOptions{Enabled: true, Ref: ref}
	if fc.Repo != nil {
		opts.Repo = *fc.Repo
	}
	return opts
}
