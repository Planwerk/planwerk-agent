package main

import (
	"github.com/spf13/cobra"

	"github.com/planwerk/planwerk-agent/internal/brain"
)

// newBrainCmd builds the "brain" command group, which reads what a repository
// keeps for its agents. Its one child, "memory", prints the project memory of
// a repository's GitHub Wiki: the index, or one page. The plugin skills call
// it, so a skill reads the memory through the same opt-in, authentication,
// clone cache, and page guards as the headless commands.
func newBrainCmd(deps *runtimeDeps) *cobra.Command {
	var wiki wikiFlags

	brainCmd := &cobra.Command{
		Use:   "brain",
		Short: "Read the project memory of a repository",
	}

	memoryCmd := &cobra.Command{
		Use:   "memory <repo-ref> [page]",
		Short: "Print the project memory index of a repository, or one page",
		Long: `Print the project memory a repository keeps on its GitHub Wiki.

With a repository reference alone, print the memory index: a header line with
the wiki, its commit, and the number of pages, a blank line, and one line per
page with the file name, the title, and after a "|" the page's summary where
it states one. With the file name of a page as a second argument, print that
page.

A page is listed and printed only when its file name starts with a letter or
a digit and holds nothing but letters, digits, ".", "_", and "-", so a caller
can pass the name back on a command line as it is. Any other page is skipped:
one warning gives the number of skipped pages, and --verbose logs their names.
Control characters other than newline and tab are dropped from the output.

The wiki is off by default. It is read when --wiki, the wiki section of
.planwerk/config.yaml in the working directory, or PLANWERK_WIKI enables it,
in that order of precedence. With the wiki off, or with a wiki that has no
memory pages, the index form prints nothing and exits 0. A page that the
memory does not hold is an error.

The command starts no Claude session and writes no file.

Repository reference can be a URL (https://github.com/owner/repo)
or short form (owner/repo).`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := brain.MemoryOptions{
				RepoRef: args[0],
				Wiki:    wiki.resolve(cmd.Flags(), deps.fileCfg.Wiki),
				Remote:  deps.remoteOpts,
			}
			if len(args) == 2 {
				opts.Page = args[1]
			}
			return brain.Memory(cmd.OutOrStdout(), opts)
		},
	}
	wiki.register(memoryCmd.Flags())

	brainCmd.AddCommand(memoryCmd)
	return brainCmd
}
