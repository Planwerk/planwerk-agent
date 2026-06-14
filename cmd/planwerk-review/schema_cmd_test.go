package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/planwerk/planwerk-review/internal/report/schema"
)

// runSchemaCmd executes the schema subcommand with one argument and returns its
// stdout bytes and the execution error.
func runSchemaCmd(t *testing.T, arg string) ([]byte, error) {
	t.Helper()
	var out, errBuf bytes.Buffer
	cmd := newSchemaCmd(&runtimeDeps{})
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	cmd.SetArgs([]string{arg})
	err := cmd.Execute()
	return out.Bytes(), err
}

func TestSchemaCmdEmitsEmbeddedSchema(t *testing.T) {
	for _, tc := range []struct {
		arg  string
		want []byte
	}{
		{arg: "review", want: schema.ReportResult},
		{arg: "audit", want: schema.ReportResult},
		{arg: "propose", want: schema.Proposal},
		{arg: "rebase", want: schema.RebaseAnalysis},
		{arg: "draft", want: schema.Draft},
	} {
		t.Run(tc.arg, func(t *testing.T) {
			got, err := runSchemaCmd(t, tc.arg)
			if err != nil {
				t.Fatalf("schema %s: %v", tc.arg, err)
			}
			if !bytes.Equal(got, tc.want) {
				t.Fatalf("schema %s output does not match the embedded schema bytes", tc.arg)
			}
			if !json.Valid(got) {
				t.Fatalf("schema %s output is not valid JSON", tc.arg)
			}
		})
	}
}

// TestSchemaCmdReviewAndAuditMatch locks in the contract that review and audit
// expose the same schema, since the audit JSON output reuses ReviewResult.
func TestSchemaCmdReviewAndAuditMatch(t *testing.T) {
	review, err := runSchemaCmd(t, "review")
	if err != nil {
		t.Fatalf("schema review: %v", err)
	}
	audit, err := runSchemaCmd(t, "audit")
	if err != nil {
		t.Fatalf("schema audit: %v", err)
	}
	if !bytes.Equal(review, audit) {
		t.Fatal("schema review and schema audit must emit identical output")
	}
}

func TestSchemaCmdUnknownArg(t *testing.T) {
	_, err := runSchemaCmd(t, "bogus")
	if err == nil {
		t.Fatal("expected an error for an unknown schema argument, got nil")
	}
	if !strings.Contains(err.Error(), "unknown schema") {
		t.Fatalf("error = %v, want an unknown-schema message", err)
	}
}
