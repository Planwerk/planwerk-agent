// Package schema holds the JSON Schema documents that describe
// planwerk-agent's machine-readable (--format json) output. The schemas are
// embedded at compile time so the `schema` subcommand can emit them verbatim
// and downstream consumers can validate piped JSON against a declared
// contract. The schemas are the source of truth: the report and propose
// renderers are kept in sync with them by contract tests in schema_test.go.
//
// Two schemas are the exception: AddressResult, the contract for the
// `address` command's per-run Claude output (address has no JSON output
// mode), and FinderOutput, the contract for the per-run Claude output of every
// pass that emits findings. Neither is a `--format json` stdout payload. They
// live here so they reuse the same contract-test harness that guards the
// renderer-backed schemas.
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

// FinderOutput is the JSON Schema (draft 2020-12) for the output of every pass
// that emits findings: the review, the audit, the adversarial pass, each domain
// specialist, the feature-compliance check, the simplify finder and the
// implementation verifier. The review and the audit run on --claude-model; the
// other five run on the --finder-model tier. Like AddressResult it is a per-run
// wire contract, not a `--format json` stdout payload: each of those sessions
// receives it via --json-schema and finishReview decodes the session's output
// against it (decision 109). It mirrors report.ReviewResult minus the
// Go-derived and pipeline-set fields such a pass never emits, the finding id
// among them. A finding requires its severity, actionability and confidence
// labels, and none of the three enums has an empty member.
//
//go:embed finder-output.schema.json
var FinderOutput []byte
