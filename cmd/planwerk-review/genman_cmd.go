package main

import (
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/cobra/doc"
)

// newGenManCmd builds the hidden "gen-man-pages" helper used by release tooling
// to emit man pages for the whole command tree. It walks the tree from
// cmd.Root(), which is the root command this subcommand is registered under.
func newGenManCmd(deps *runtimeDeps) *cobra.Command {
	return &cobra.Command{
		Use:    "gen-man-pages <dir>",
		Short:  "Generate man pages into the given directory",
		Args:   cobra.ExactArgs(1),
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := args[0]
			if err := os.MkdirAll(dir, 0o750); err != nil {
				return err
			}
			header := &doc.GenManHeader{
				Title:   "PLANWERK-REVIEW",
				Section: "1",
				Source:  "planwerk-review " + deps.version,
			}
			return doc.GenManTree(cmd.Root(), header, dir)
		},
	}
}
