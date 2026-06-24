package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/planwerk/planwerk-review/internal/cli"
	"github.com/planwerk/planwerk-review/internal/extract"
	"github.com/planwerk/planwerk-review/internal/patterns"
)

// newExtractCmd builds the "extract" subcommand: anchor a target repo's GitHub
// Wiki review patterns into committed, reproducible files. It is mechanical (no
// Claude). By default it opens a PR into the target repo's
// .planwerk/review_patterns/; --local writes the working tree directly; and
// --to-catalog anchors the selected patterns into this checkout's bundled review
// catalog, normalizing their frontmatter to the review category.
func newExtractCmd(deps *runtimeDeps) *cobra.Command {
	var cfg cli.ExtractConfig
	var wikiRef string

	extractCmd := &cobra.Command{
		Use:   "extract <repo-ref>",
		Short: "Anchor wiki review patterns into committed files",
		Long: `Pull review patterns off a target repository's GitHub Wiki and write them
as committed, reproducible files, selecting which entries to anchor.

By default the selected patterns are written into the target repo's
.planwerk/review_patterns/ and a pull request is opened. With --local the
patterns are written directly into the current working tree instead. With
--to-catalog the patterns are anchored into this planwerk-review checkout's
bundled review catalog (internal/patterns/patterns/review/), normalizing their
frontmatter to the review category — the maintainer/contribution path.

By default the patterns are selected interactively; pass --all to take every
pattern, or --pattern <stem> (repeatable) to take specific ones by filename.

Repository reference can be a URL (https://github.com/owner/repo)
or short form (owner/repo).`,
		Args: cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				cfg.RepoRef = args[0]
			}

			if cfg.ToCatalog && cfg.Local {
				return fmt.Errorf("--to-catalog and --local are mutually exclusive")
			}
			if cfg.All && len(cfg.Patterns) > 0 {
				return fmt.Errorf("--all and --pattern are mutually exclusive")
			}
			if cfg.RepoRef == "" {
				switch {
				case cfg.Local:
					// Inferred from the working tree's origin remote.
				case cfg.ToCatalog:
					return fmt.Errorf("--to-catalog requires an explicit repository reference to read the wiki from")
				default:
					return fmt.Errorf("requires a repository reference argument (or use --local)")
				}
			}

			opts := cfg.ToExtractOptions(deps.version)
			opts.Remote = deps.remoteOpts
			opts.Wiki = resolveExtractWiki(wikiRef, cmd.Flags().Changed("wiki-ref"), deps.fileCfg.Wiki)
			return extract.Run(cmd.OutOrStdout(), opts)
		},
	}

	flags := extractCmd.Flags()
	flags.StringSliceVar(&cfg.Patterns, "pattern", nil, "Extract only the named wiki pattern(s) by filename stem (repeatable); default selects interactively")
	flags.BoolVar(&cfg.All, "all", false, "Extract every wiki review pattern without prompting")
	flags.BoolVar(&cfg.ToCatalog, "to-catalog", false, "Anchor into this checkout's bundled review catalog (internal/patterns/patterns/review/), normalizing frontmatter to the review category")
	flags.BoolVar(&cfg.Local, "local", false, "Write directly into the current working tree's .planwerk/review_patterns/ instead of opening a PR")
	flags.BoolVar(&cfg.Force, "force", false, "With --local, skip the confirmation prompt when the working tree is dirty")
	flags.BoolVar(&cfg.Overwrite, "overwrite", false, "With --local or --to-catalog, replace an existing pattern at the destination instead of refusing the collision")
	flags.StringVar(&wikiRef, "wiki-ref", "", "Pin the wiki to a branch, tag, or commit (env: "+envWikiRef+"; empty uses the wiki's default branch)")

	return extractCmd
}

// resolveExtractWiki builds the WikiOptions for extract. The wiki is always
// enabled — reading it is the command's whole purpose — so there is no
// --wiki/--no-wiki here. The ref precedence is --wiki-ref, then
// PLANWERK_WIKI_REF, then the config file; the repo override comes from the
// config file only, mirroring resolveWikiOptions.
func resolveExtractWiki(refFlag string, refChanged bool, fc cli.WikiFileConfig) patterns.WikiOptions {
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
