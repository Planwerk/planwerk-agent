// Package schema holds the JSON Schema documents that describe
// planwerk-review's machine-readable (--format json) output. The schemas are
// embedded at compile time so the `schema` subcommand can emit them verbatim
// and downstream consumers can validate piped JSON against a declared
// contract. The schemas are the source of truth: the report and propose
// renderers are kept in sync with them by contract tests in schema_test.go.
package schema

import _ "embed"

// ReportResult is the JSON Schema (draft 2020-12) for the `review` and `audit`
// --format json output, i.e. report.ReviewResult. The review and audit paths
// share this schema because the audit renderer reuses ReviewResult.
//
//go:embed report-result.schema.json
var ReportResult []byte

// Proposal is the JSON Schema (draft 2020-12) for the `propose` --format json
// output. It models the propose.ProposalResult envelope the command actually
// emits; a single proposal is defined under $defs/proposal.
//
//go:embed proposal.schema.json
var Proposal []byte

// RebaseAnalysis is the JSON Schema (draft 2020-12) for the `rebase`
// post-rebase analysis --format json output, i.e. report.RebaseAnalysis. A
// single commit analysis is defined under $defs/commitAnalysis and a single
// adjustment under $defs/adjustment.
//
//go:embed rebase-analysis.schema.json
var RebaseAnalysis []byte
