package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/planwerk/planwerk-agent/internal/report/schema"
)

// newSchemaCmd builds the "schema" subcommand: it prints the JSON Schema that
// describes a command's --format json output to stdout, so downstream tooling
// can validate piped JSON against the same contract the renderers follow. deps
// is unused but kept for signature uniformity with the other subcommand
// constructors.
func newSchemaCmd(_ *runtimeDeps) *cobra.Command {
	return &cobra.Command{
		Use:   "schema [review|audit|propose|rebase]",
		Short: "Print the JSON Schema for a command's --format json output",
		Long: `Print the JSON Schema (draft 2020-12) describing a command's --format json
output to stdout.

The schema is the contract downstream consumers can validate piped JSON
against. "review" and "audit" share one schema (report-result.schema.json)
because the audit output reuses the review result shape; "propose" emits the
proposal-result envelope (proposal.schema.json); "rebase" emits the
post-rebase analysis (rebase-analysis.schema.json).`,
		Args:      cobra.ExactArgs(1),
		ValidArgs: []string{"review", "audit", "propose", "rebase"},
		RunE: func(cmd *cobra.Command, args []string) error {
			doc, err := schemaFor(args[0])
			if err != nil {
				return err
			}
			_, err = cmd.OutOrStdout().Write(doc)
			return err
		},
	}
}

// schemaFor maps a subcommand name to its embedded JSON Schema document.
// review and audit map to the same schema because their JSON output shares the
// report.ReviewResult shape.
func schemaFor(name string) ([]byte, error) {
	switch name {
	case "review", "audit":
		return schema.ReportResult, nil
	case "propose":
		return schema.Proposal, nil
	case "rebase":
		return schema.RebaseAnalysis, nil
	default:
		return nil, fmt.Errorf("unknown schema %q, supported: review, audit, propose, rebase", name)
	}
}
