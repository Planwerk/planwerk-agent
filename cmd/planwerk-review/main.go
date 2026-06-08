package main

import (
	"log/slog"
	"os"

	"github.com/planwerk/planwerk-review/internal/cli"
)

// runtimeDeps carries the process-wide values every subcommand constructor
// needs: the resolved build version and the parsed .planwerk/config.yaml.
// fileCfg is populated by the root command's PersistentPreRunE before any
// subcommand RunE runs, so the review, propose, and audit commands read it
// back through this shared pointer.
type runtimeDeps struct {
	version string
	fileCfg cli.FileConfig
}

func main() {
	deps := &runtimeDeps{version: version}

	rootCmd := newRootCmd(deps)
	rootCmd.AddCommand(
		newProposeCmd(deps),
		newAuditCmd(deps),
		newGapAnalysisCmd(deps),
		newReviewPreparedCmd(deps),
		newElaborateCmd(deps),
		newPromptCmd(deps),
		newFixCmd(deps),
		newImplementCmd(deps),
		newCacheCmd(deps),
		newGenManCmd(deps),
	)

	if err := rootCmd.Execute(); err != nil {
		// Route the final error through slog so it honors --log-format.
		slog.Error(err.Error())
		os.Exit(1)
	}
}
