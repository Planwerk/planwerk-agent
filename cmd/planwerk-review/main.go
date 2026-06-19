package main

import (
	"log/slog"
	"os"

	"github.com/planwerk/planwerk-review/internal/claude"
	"github.com/planwerk/planwerk-review/internal/cli"
	"github.com/planwerk/planwerk-review/internal/patterns"
)

// runtimeDeps carries the process-wide values every subcommand constructor
// needs: the resolved build version and the parsed .planwerk/config.yaml.
// fileCfg, remoteOpts, claude, and claudeOpts are populated by the root
// command's PersistentPreRunE before any subcommand RunE runs, so every
// subcommand reads them back through this shared pointer.
type runtimeDeps struct {
	version    string
	fileCfg    cli.FileConfig
	remoteOpts patterns.RemoteOptions
	// claude is the Claude Code client carrying the resolved --claude-* config.
	// Every command except implement injects its bound methods directly.
	claude *claude.Client
	// claudeOpts are the resolved common --claude-* options used to build
	// claude. The implement command appends its --plan-* options to them when
	// constructing its own client so the planning session honors both.
	claudeOpts []claude.Option
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
		newDraftCmd(deps),
		newMetaCmd(deps),
		newPromptCmd(deps),
		newFixCmd(deps),
		newRebaseCmd(deps),
		newAddressCmd(deps),
		newImplementCmd(deps),
		newCacheCmd(deps),
		newSchemaCmd(deps),
		newGenManCmd(deps),
	)

	if err := rootCmd.Execute(); err != nil {
		// Route the final error through slog so it honors --log-format.
		slog.Error(err.Error())
		os.Exit(1)
	}
}
