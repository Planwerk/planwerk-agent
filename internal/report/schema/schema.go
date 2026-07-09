// Package schema holds the JSON Schema documents that describe
// planwerk-agent's machine-readable (--format json) output. The schemas are
// embedded at compile time so the `schema` subcommand can emit them verbatim
// and downstream consumers can validate piped JSON against a declared
// contract. The schemas are the source of truth: the report and propose
// renderers are kept in sync with them by contract tests in schema_test.go.
//
// One schema (AddressResult) is the exception: it is the contract for the
// `address` command's per-run Claude output, not a `--format json` stdout
// payload (address has no JSON output mode). It lives here so it reuses the
// same contract-test harness that guards the renderer-backed schemas.
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

// AddressResult is the JSON Schema (draft 2020-12) for the `address` command's
// per-run Claude output, i.e. report.AddressResult. Unlike the other schemas
// here it does not back a `--format json` stdout payload — it is the contract
// the address session's structured output is decoded against. A single thread
// result is defined under $defs/addressedThread.
//
//go:embed address-result.schema.json
var AddressResult []byte

// StructuredReview is the JSON Schema (draft 2020-12) for the review structuring
// pass's output. Like AddressResult it is a per-run wire contract, not a
// `--format json` stdout payload: the review pipeline passes it to the CLI via
// --json-schema so the transcribe-only structure tier is constrained to the
// report shape. It mirrors report.ReviewResult plus source_finding_count, minus
// the Go-derived and pipeline-set fields the structure tier never emits, with
// the classification enums widened by the empty string so an unlabeled finding
// is representable at the wire level.
//
//go:embed structured-review.schema.json
var StructuredReview []byte
