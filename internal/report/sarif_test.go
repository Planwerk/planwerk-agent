package report

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/planwerk/planwerk-agent/internal/attribution"
)

// renderSARIF renders result with RenderSARIF at version "v1.2.3" and returns
// the raw bytes, the log decoded into the renderer's own types, and the log
// decoded generically, for the tests that check which keys are present.
func renderSARIF(t *testing.T, result ReviewResult, minSeverity Severity, minConfidence Confidence) ([]byte, sarifLog, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	if err := NewRenderer(&buf).RenderSARIF(result, minSeverity, minConfidence, "v1.2.3"); err != nil {
		t.Fatalf("RenderSARIF: %v", err)
	}
	var typed sarifLog
	if err := json.Unmarshal(buf.Bytes(), &typed); err != nil {
		t.Fatalf("output is not one JSON document: %v\n%s", err, buf.String())
	}
	var generic map[string]any
	if err := json.Unmarshal(buf.Bytes(), &generic); err != nil {
		t.Fatalf("output is not one JSON object: %v", err)
	}
	if len(typed.Runs) != 1 {
		t.Fatalf("got %d runs, want 1", len(typed.Runs))
	}
	return buf.Bytes(), typed, generic
}

// genericRun returns the first run of a generically decoded log.
func genericRun(t *testing.T, generic map[string]any) map[string]any {
	t.Helper()
	runs, ok := generic["runs"].([]any)
	if !ok || len(runs) != 1 {
		t.Fatalf("runs = %#v, want one run", generic["runs"])
	}
	return runs[0].(map[string]any)
}

// genericResults returns the results of a generically decoded log's run.
func genericResults(t *testing.T, generic map[string]any) []map[string]any {
	t.Helper()
	raw, ok := genericRun(t, generic)["results"].([]any)
	if !ok {
		t.Fatalf("results is not an array: %#v", genericRun(t, generic)["results"])
	}
	results := make([]map[string]any, len(raw))
	for i, r := range raw {
		results[i] = r.(map[string]any)
	}
	return results
}

func TestRenderSARIF_Envelope(t *testing.T) {
	raw, typed, generic := renderSARIF(t, ReviewResult{Summary: "clean"}, SeverityInfo, "")

	if generic["$schema"] != sarifSchemaURI {
		t.Errorf("$schema = %v, want %s", generic["$schema"], sarifSchemaURI)
	}
	if generic["version"] != "2.1.0" {
		t.Errorf("version = %v, want 2.1.0", generic["version"])
	}
	driver := typed.Runs[0].Tool.Driver
	if driver.Name != "planwerk-agent" || driver.Version != "v1.2.3" || driver.InformationURI != attribution.RepoURL {
		t.Errorf("driver = %+v, want planwerk-agent v1.2.3 at %s", driver, attribution.RepoURL)
	}
	// Code scanning rejects a null results array, so an empty run must still
	// carry both lists as arrays.
	for _, want := range []string{`"results": []`, `"rules": []`} {
		if !bytes.Contains(raw, []byte(want)) {
			t.Errorf("output lacks %s:\n%s", want, raw)
		}
	}
	invocations := typed.Runs[0].Invocations
	if len(invocations) != 1 || !invocations[0].ExecutionSuccessful {
		t.Fatalf("invocations = %+v, want one successful invocation", invocations)
	}
	inv := genericRun(t, generic)["invocations"].([]any)[0].(map[string]any)
	if _, ok := inv["toolExecutionNotifications"]; ok {
		t.Errorf("a run without file-less findings must carry no notifications: %v", inv)
	}

	t.Run("an empty version leaves the driver version out", func(t *testing.T) {
		var buf bytes.Buffer
		if err := NewRenderer(&buf).RenderSARIF(ReviewResult{}, SeverityInfo, "", "  "); err != nil {
			t.Fatalf("RenderSARIF: %v", err)
		}
		var generic map[string]any
		if err := json.Unmarshal(buf.Bytes(), &generic); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		driver := genericRun(t, generic)["tool"].(map[string]any)["driver"].(map[string]any)
		if _, ok := driver["version"]; ok {
			t.Errorf("driver = %v, want no version key", driver)
		}
	})
}

func TestRenderSARIF_Levels(t *testing.T) {
	tests := []struct {
		name           string
		severity       Severity
		confidence     Confidence
		note           string
		wantLevel      string
		wantUnverified bool
	}{
		{"verified BLOCKING is an error", SeverityBlocking, ConfidenceVerified, "", "error", false},
		{"likely CRITICAL is an error", SeverityCritical, ConfidenceLikely, "", "error", false},
		{"WARNING is a warning", SeverityWarning, ConfidenceLikely, "", "warning", false},
		{"INFO is a note", SeverityInfo, ConfidenceVerified, "", "note", false},
		{"uncertain WARNING is an unverified note", SeverityWarning, ConfidenceUncertain, "", "note", true},
		{"uncertain BLOCKING without a refutation stays an error", SeverityBlocking, ConfidenceUncertain, "", "error", false},
		{"refuted CRITICAL is an unverified note", SeverityCritical, ConfidenceUncertain, "refuted: guarded at line 50", "note", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ReviewResult{Findings: []Finding{{
				ID: "F-1", Severity: tt.severity, Confidence: tt.confidence, VerificationNote: tt.note,
				Title: "t", File: "a.go", Line: 1, Problem: "p", Action: "a",
			}}}
			_, typed, _ := renderSARIF(t, result, SeverityInfo, "")
			results := typed.Runs[0].Results
			if len(results) != 1 {
				t.Fatalf("got %d results, want 1", len(results))
			}
			if results[0].Level != tt.wantLevel {
				t.Errorf("level = %q, want %q", results[0].Level, tt.wantLevel)
			}
			if results[0].Properties.Unverified != tt.wantUnverified {
				t.Errorf("properties.unverified = %v, want %v", results[0].Properties.Unverified, tt.wantUnverified)
			}
			if results[0].Properties.Severity != string(tt.severity) {
				t.Errorf("properties.severity = %q, want the four-level %q", results[0].Properties.Severity, tt.severity)
			}
		})
	}
}

func TestRenderSARIF_RuleIDs(t *testing.T) {
	for pattern, want := range map[string]string{
		"Hardcoded secrets":                 "pattern/hardcoded-secrets",
		"Missing context.Context parameter": "pattern/missing-context-context-parameter",
		"  Leading -- and trailing!! ":      "pattern/leading-and-trailing",
		"N+1 queries":                       "pattern/n-1-queries",
		"Prüfung fehlt":                     "pattern/prüfung-fehlt",
		"":                                  "finding",
		" -- ":                              "finding",
	} {
		if got := sarifRuleID(pattern); got != want {
			t.Errorf("sarifRuleID(%q) = %q, want %q", pattern, got, want)
		}
	}

	result := ReviewResult{Findings: []Finding{
		{ID: "W-1", Severity: SeverityWarning, Title: "a", File: "a.go", Line: 1, Pattern: "Hardcoded secrets", CodeSnippet: "x"},
		{ID: "W-2", Severity: SeverityWarning, Title: "b", File: "b.go", Line: 2, Pattern: "Hardcoded secrets", CodeSnippet: "y"},
		{ID: "W-3", Severity: SeverityWarning, Title: "c", File: "c.go", Line: 3, Pattern: " -- "},
	}}
	_, typed, _ := renderSARIF(t, result, SeverityInfo, "")
	rules := typed.Runs[0].Tool.Driver.Rules
	want := []sarifRule{
		{ID: "pattern/hardcoded-secrets", ShortDescription: sarifMessage{Text: "Hardcoded secrets"}},
		{ID: "finding", ShortDescription: sarifMessage{Text: "A planwerk-agent finding that names no review pattern"}},
	}
	if !reflect.DeepEqual(rules, want) {
		t.Errorf("rules = %+v, want each id listed once: %+v", rules, want)
	}
	listed := make(map[string]bool)
	for _, r := range rules {
		listed[r.ID] = true
	}
	for _, r := range typed.Runs[0].Results {
		if !listed[r.RuleID] {
			t.Errorf("result %s has ruleId %q, which no rule lists", r.Properties.FindingID, r.RuleID)
		}
	}
}

func TestRenderSARIF_Location(t *testing.T) {
	result := ReviewResult{Findings: []Finding{
		{ID: "W-1", Severity: SeverityWarning, Title: "range", File: "range.go", Line: 87, LineEnd: 92},
		{ID: "W-2", Severity: SeverityWarning, Title: "one line", File: "one.go", Line: 42, LineEnd: 42},
		{ID: "W-3", Severity: SeverityWarning, Title: "backwards", File: "back.go", Line: 10, LineEnd: 5},
		{ID: "W-4", Severity: SeverityWarning, Title: "no line", File: "noline.go"},
		{ID: "W-5", Severity: SeverityWarning, Title: "escaped", File: "./a b/c.go", Line: 1},
		{ID: "W-6", Severity: SeverityWarning, Title: "colon", File: "a:b.go", Line: 1},
	}}
	_, _, generic := renderSARIF(t, result, SeverityInfo, "")
	results := genericResults(t, generic)
	if len(results) != len(result.Findings) {
		t.Fatalf("got %d results, want %d", len(results), len(result.Findings))
	}
	physical := func(i int) map[string]any {
		locations := results[i]["locations"].([]any)
		if len(locations) != 1 {
			t.Fatalf("result %d has %d locations, want 1", i, len(locations))
		}
		return locations[0].(map[string]any)["physicalLocation"].(map[string]any)
	}
	uri := func(i int) string {
		return physical(i)["artifactLocation"].(map[string]any)["uri"].(string)
	}
	region := func(i int) (map[string]any, bool) {
		r, ok := physical(i)["region"].(map[string]any)
		return r, ok
	}

	if r, ok := region(0); !ok || r["startLine"] != float64(87) || r["endLine"] != float64(92) {
		t.Errorf("87..92 region = %v, want startLine 87 and endLine 92", r)
	}
	for _, i := range []int{1, 2} {
		r, ok := region(i)
		if !ok || r["startLine"] == nil {
			t.Errorf("result %d region = %v, want a startLine", i, r)
		}
		if _, has := r["endLine"]; has {
			t.Errorf("result %d region = %v, want no endLine when the range does not end after the line", i, r)
		}
	}
	if r, ok := region(3); ok {
		t.Errorf("a finding without a line must have no region, got %v", r)
	}
	if got := uri(4); got != "a%20b/c.go" {
		t.Errorf("uri = %q, want a%%20b/c.go", got)
	}
	if got := uri(5); got != "./a:b.go" {
		t.Errorf("uri = %q, want ./a:b.go, which no reader takes for a scheme", got)
	}
}

func TestRenderSARIF_Message(t *testing.T) {
	tests := []struct {
		name, title, problem, want string
	}{
		{"title then problem", "T", "P", "T. P"},
		{"a title that ends a sentence joins with a space", "Is this nil?", "It is read unchecked.", "Is this nil? It is read unchecked."},
		{"an empty problem leaves the title", "  Title only ", "", "Title only"},
		{"an empty title leaves the problem", "", "Problem only", "Problem only"},
		{"neither names the finding", "", "", "planwerk-agent finding W-1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ReviewResult{Findings: []Finding{{
				ID: "W-1", Severity: SeverityWarning, Title: tt.title, Problem: tt.problem, File: "a.go", Line: 1,
			}}}
			_, typed, _ := renderSARIF(t, result, SeverityInfo, "")
			if got := typed.Runs[0].Results[0].Message.Text; got != tt.want {
				t.Errorf("message.text = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRenderSARIF_FilelessFindingIsANotification(t *testing.T) {
	result := ReviewResult{Findings: []Finding{
		{ID: "W-1", Severity: SeverityWarning, Title: "In a file", File: "a.go", Line: 3},
		{ID: "W-2", Severity: SeverityWarning, Title: "Docs not updated", File: ""},
		{ID: "I-1", Severity: SeverityInfo, Title: "Scope drift", File: "   "},
	}}
	_, typed, _ := renderSARIF(t, result, SeverityInfo, "")
	run := typed.Runs[0]
	if len(run.Results) != 1 || run.Results[0].Properties.FindingID != "W-1" {
		t.Fatalf("results = %+v, want only W-1", run.Results)
	}
	inv := run.Invocations[0]
	if !inv.ExecutionSuccessful {
		t.Error("a file-less finding must not mark the run as failed")
	}
	if len(inv.ToolExecutionNotifications) != 1 {
		t.Fatalf("notifications = %+v, want one", inv.ToolExecutionNotifications)
	}
	n := inv.ToolExecutionNotifications[0]
	if n.Level != "warning" {
		t.Errorf("notification level = %q, want warning", n.Level)
	}
	for _, want := range []string{"2 finding(s) name no file", "W-2 Docs not updated", "I-1 Scope drift"} {
		if !strings.Contains(n.Message.Text, want) {
			t.Errorf("notification %q lacks %q", n.Message.Text, want)
		}
	}
}

func TestRenderSARIF_AppliesTheReportFilters(t *testing.T) {
	result := ReviewResult{Findings: []Finding{
		{ID: "I-1", Severity: SeverityInfo, Confidence: ConfidenceVerified, Title: "info", File: "a.go", Line: 1},
		{ID: "W-1", Severity: SeverityWarning, Confidence: ConfidenceUncertain, Title: "uncertain", File: "a.go", Line: 2},
		{ID: "W-2", Severity: SeverityWarning, Confidence: ConfidenceLikely, Title: "likely", File: "a.go", Line: 3},
		{ID: "C-1", Severity: SeverityCritical, Confidence: ConfidenceLikely, Title: "critical", File: "a.go", Line: 4},
		{ID: "B-1", Severity: SeverityBlocking, Confidence: ConfidenceVerified, Title: "blocking", File: "a.go", Line: 5},
	}}
	_, typed, _ := renderSARIF(t, result, SeverityWarning, ConfidenceLikely)
	var sarifIDs []string
	for _, r := range typed.Runs[0].Results {
		sarifIDs = append(sarifIDs, r.Properties.FindingID)
	}

	var buf bytes.Buffer
	if err := NewRenderer(&buf).RenderJSON(result, SeverityWarning, ConfidenceLikely); err != nil {
		t.Fatalf("RenderJSON: %v", err)
	}
	var printed ReviewResult
	if err := json.Unmarshal(buf.Bytes(), &printed); err != nil {
		t.Fatalf("unmarshal JSON: %v", err)
	}
	var jsonIDs []string
	for _, f := range printed.Findings {
		jsonIDs = append(jsonIDs, f.ID)
	}

	want := []string{"B-1", "C-1", "W-2"}
	if !reflect.DeepEqual(sarifIDs, want) {
		t.Errorf("SARIF results = %v, want %v", sarifIDs, want)
	}
	if !reflect.DeepEqual(sarifIDs, jsonIDs) {
		t.Errorf("SARIF results %v differ from the --format json findings %v", sarifIDs, jsonIDs)
	}
}

func TestRenderSARIF_Fingerprints(t *testing.T) {
	fingerprintOf := func(t *testing.T, fs ...Finding) []string {
		t.Helper()
		_, typed, _ := renderSARIF(t, ReviewResult{Findings: fs}, SeverityInfo, "")
		var out []string
		for _, r := range typed.Runs[0].Results {
			fp, ok := r.PartialFingerprints["planwerkFinding/v1"]
			if !ok || len(r.PartialFingerprints) != 1 {
				t.Fatalf("partialFingerprints = %v, want only planwerkFinding/v1", r.PartialFingerprints)
			}
			out = append(out, fp)
		}
		return out
	}
	finding := func(file string, line int, snippet string) Finding {
		return Finding{ID: "W-1", Severity: SeverityWarning, Title: "t", File: file, Line: line, Pattern: "Raw SQL", CodeSnippet: snippet}
	}
	one := func(t *testing.T, f Finding) string {
		t.Helper()
		return fingerprintOf(t, f)[0]
	}

	if one(t, finding("db.go", 10, "db.Query(q)")) != one(t, finding("db.go", 50, "db.Query(q)")) {
		t.Error("a finding whose quoted code moved down the file must keep its fingerprint")
	}
	if one(t, finding("db.go", 10, "if x {\n\tdb.Query(q)\n}")) != one(t, finding("db.go", 10, "if x { db.Query(q) }")) {
		t.Error("a re-indented quote must keep its fingerprint")
	}
	if one(t, finding("DB.go", 10, "db.Query(q)")) != one(t, finding("db.go", 10, "db.Query(q)")) {
		t.Error("the file must be compared case-insensitively")
	}
	if one(t, finding("Ä.go", 10, "db.Query(q)")) != one(t, finding("ä.go", 10, "db.Query(q)")) {
		t.Error("a non-ASCII file must be compared case-insensitively too, so it is folded before it is escaped")
	}
	if one(t, finding("db.go", 10, "db.Query(q)")) == one(t, finding("db.go", 10, "db.Exec(q)")) {
		t.Error("different quoted code must give different fingerprints")
	}
	if one(t, finding("db.go", 10, "db.Query(q)")) == one(t, finding("other.go", 10, "db.Query(q)")) {
		t.Error("the same quote in another file must give a different fingerprint")
	}
	if one(t, finding("db.go", 10, "")) == one(t, finding("db.go", 11, "")) {
		t.Error("without a quote, the line must anchor the fingerprint")
	}
	if one(t, finding("db.go", 10, "  ")) != one(t, finding("db.go", 10, "")) {
		t.Error("a whitespace-only quote must fall back to the line, as an empty one does")
	}

	dups := fingerprintOf(t, finding("db.go", 10, "x"), finding("db.go", 20, "x"), finding("db.go", 30, "x"))
	if dups[1] != dups[0]+"#1" || dups[2] != dups[0]+"#2" {
		t.Errorf("repeated fingerprints = %v, want the base, then #1 and #2", dups)
	}
}

func TestRenderSARIF_Properties(t *testing.T) {
	result := ReviewResult{Findings: []Finding{
		{ID: "C-1", Severity: SeverityCritical, Confidence: ConfidenceVerified, Actionability: ActionabilityAutoFix, Title: "full", File: "a.go", Line: 1},
		{ID: "W-1", Severity: SeverityWarning, Title: "bare", File: "a.go", Line: 2},
	}}
	_, _, generic := renderSARIF(t, result, SeverityInfo, "")
	results := genericResults(t, generic)
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}

	full := results[0]["properties"].(map[string]any)
	want := map[string]any{
		"finding_id":    "C-1",
		"severity":      "CRITICAL",
		"confidence":    "verified",
		"actionability": string(ActionabilityAutoFix),
		"unverified":    false,
	}
	if !reflect.DeepEqual(full, want) {
		t.Errorf("properties = %v, want %v", full, want)
	}

	bare := results[1]["properties"].(map[string]any)
	for _, key := range []string{"confidence", "actionability"} {
		if _, ok := bare[key]; ok {
			t.Errorf("properties = %v, want no empty %s", bare, key)
		}
	}
	if v, ok := bare["unverified"]; !ok || v != false {
		t.Errorf("properties = %v, want unverified written as false", bare)
	}
}
