package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/planwerk/planwerk-review/internal/cache"
	"github.com/planwerk/planwerk-review/internal/glossary"
)

// newGlossaryCmd builds the "glossary" subcommand: clone a GitHub repository
// and emit a starter domain glossary (CONTEXT.md) in the CONTEXT-FORMAT schema
// to stdout. The output is meant to be reviewed by a human and redirected into
// the repo's own CONTEXT.md, which the review, elaborate, and propose commands
// then read back.
func newGlossaryCmd(deps *runtimeDeps) *cobra.Command {
	var opts glossary.Options

	glossaryCmd := &cobra.Command{
		Use:   "glossary <repo-ref>",
		Short: "Generate a starter domain glossary (CONTEXT.md) for a codebase",
		Long: `Clone a GitHub repository and generate a starter domain glossary in the
CONTEXT-FORMAT schema, printed to stdout. The glossary captures the
repository's own domain vocabulary so that review, elaborate, and propose
phrase their output in the repo's terms once it is committed as CONTEXT.md.

The output is a starter: review and edit it before committing. Redirect it
where you want it to land, for example:

    planwerk-review glossary owner/repo > CONTEXT.md

Repository reference can be a URL (https://github.com/owner/repo)
or short form (owner/repo).`,
		Args: cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				opts.RepoRef = args[0]
			} else if !opts.Local {
				return fmt.Errorf("requires a repository reference argument (or use --local)")
			}
			return glossary.Run(os.Stdout, opts, deps.claude.GenerateGlossary)
		},
	}

	flags := glossaryCmd.Flags()
	flags.BoolVar(&opts.Local, "local", false, "Operate on the current working directory instead of cloning into a temp dir")
	flags.BoolVar(&opts.Force, "force", false, "With --local, skip the confirmation prompt when the working tree is dirty")
	flags.BoolVar(&opts.NoCache, "no-cache", false, "Ignore cache, force a fresh glossary")
	flags.DurationVar(&opts.CacheMaxAge, "cache-max-age", cache.DefaultMaxAge, "Reject cached entries older than this duration (0 disables the TTL)")

	return glossaryCmd
}
