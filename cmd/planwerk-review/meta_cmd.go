package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/planwerk/planwerk-review/internal/cli"
	"github.com/planwerk/planwerk-review/internal/meta"
)

// newMetaCmd builds the "meta" subcommand: expand a Meta Issue into linked,
// draft-depth Sub Issues. It reads the Meta Issue, carves it into the fewest
// sensible Sub Issues, files each one, links it to the Meta Issue via GitHub's
// native sub-issue relationship, and back-fills the Meta Issue body with the
// fresh references. Like draft, it stops at draft depth and does not elaborate
// or implement.
func newMetaCmd(deps *runtimeDeps) *cobra.Command {
	var metaCfg cli.MetaConfig

	metaCmd := &cobra.Command{
		Use:   "meta <issue-ref>",
		Short: "Expand a Meta Issue into linked, draft-depth Sub Issues",
		Long: `Read a Meta Issue — an issue that frames a larger body of work as several
self-contained work packages — and create the Sub Issues for it. The command
decides the breakdown on its own: it reads the Meta Issue, carves it into the
fewest sensible Sub Issues, files each one, links it to the Meta Issue via
GitHub's native sub-issue relationship, and back-fills the Meta Issue body so
its work-package lines reference the freshly created Sub Issues. It does not
ask what to split or how.

Each Sub Issue stops at draft depth — enough context to stand on its own, like
the "draft" command produces — and is deliberately not elaborated. Turning a
Sub Issue into a file-level engineering plan stays the job of the separate
"elaborate" and "implement" commands, run per Sub Issue when you are ready.
The command stops at creating and linking: it does not drive the Sub Issues
through elaborate/implement/fix and does not close the Meta Issue.

Issue reference can be a URL (https://github.com/owner/repo/issues/123)
or short form (owner/repo#123). Use --dry-run to preview the planned split
without filing or linking anything.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			switch metaCfg.Format {
			case formatMarkdown, formatJSON:
			default:
				return fmt.Errorf("unknown format %q, supported: markdown, json", metaCfg.Format)
			}

			metaCfg.IssueRef = args[0]
			opts := metaCfg.ToMetaOptions(deps.version)
			return meta.Run(cmd.OutOrStdout(), opts, deps.claude.Meta)
		},
	}

	metaFlags := metaCmd.Flags()
	metaFlags.BoolVar(&metaCfg.DryRun, "dry-run", false, "Render the planned split without filing or linking any sub-issues")
	metaFlags.BoolVar(&metaCfg.NoCreate, "no-create", false, "Alias of --dry-run: render the planned split without filing")
	metaFlags.StringArrayVar(&metaCfg.Labels, "label", nil, "Label to attach to each created sub-issue (repeatable)")
	metaFlags.StringVar(&metaCfg.Format, "format", "markdown", "Output format (markdown, json)")

	return metaCmd
}
